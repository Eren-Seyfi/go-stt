package backend

/*
#cgo CFLAGS: -I${SRCDIR}/../third_party/whisper/include
#cgo LDFLAGS: -L${SRCDIR}/../third_party/whisper/lib -lwhisper -lggml -lggml-base
#cgo linux LDFLAGS: -Wl,-rpath,${SRCDIR}/../third_party/whisper/lib
#cgo darwin LDFLAGS: -Wl,-rpath,${SRCDIR}/../third_party/whisper/lib
// Windows has no rpath: whisper.dll, ggml.dll and ggml-base.dll (from
// third_party/whisper/bin after a CMake install) must sit next to the .exe
// or be on PATH. MinGW links against the import libraries (.dll.a) in lib/.
#include <stdlib.h>
#include "whisper.h"
#include "ggml-backend.h"

static void quiet_log(enum ggml_log_level level, const char *text, void *ud) {}
static void whisper_quiet(void) { whisper_log_set(quiet_log, NULL); }
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// GPUInfo describes a GPU usable by whisper.cpp.
type GPUInfo struct {
	Name     string `json:"name"`
	MemoryMB int    `json:"memoryMB"`
}

// detectGPUs lists GPU devices registered by ggml (CUDA). It is empty when
// the driver can't be initialised, e.g. right after a driver update before
// a reboot.
func detectGPUs() []GPUInfo {
	C.whisper_quiet()
	var gpus []GPUInfo
	for i := range C.ggml_backend_dev_count() {
		dev := C.ggml_backend_dev_get(i)
		if C.ggml_backend_dev_type(dev) != C.GGML_BACKEND_DEVICE_TYPE_GPU {
			continue
		}
		var free, total C.size_t
		C.ggml_backend_dev_memory(dev, &free, &total)
		gpus = append(gpus, GPUInfo{
			Name:     C.GoString(C.ggml_backend_dev_description(dev)),
			MemoryMB: int(total / (1 << 20)),
		})
	}
	return gpus
}

// Language is one of the languages Whisper understands.
type Language struct {
	Code string `json:"code"` // e.g. "tr"
	Name string `json:"name"` // English name, e.g. "turkish"
}

// whisperLanguages lists all languages built into the Whisper models.
func whisperLanguages() []Language {
	var out []Language
	for i := C.int(0); i <= C.whisper_lang_max_id(); i++ {
		out = append(out, Language{
			Code: C.GoString(C.whisper_lang_str(i)),
			Name: C.GoString(C.whisper_lang_str_full(i)),
		})
	}
	return out
}

// Only one Whisper context is kept in memory; switching model or CPU/GPU frees the old one.
var (
	whisperMu  sync.Mutex
	whisperCtx *C.struct_whisper_context
	whisperKey string
)

func loadWhisper(path string, gpu bool) (*C.struct_whisper_context, error) {
	whisperMu.Lock()
	defer whisperMu.Unlock()
	key := fmt.Sprintf("%s|%v", path, gpu)
	if whisperCtx != nil && whisperKey == key {
		return whisperCtx, nil
	}
	freeWhisperLocked()
	C.whisper_quiet()
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	cp := C.whisper_context_default_params()
	cp.use_gpu = C.bool(gpu)
	cp.flash_attn = C.bool(gpu)
	ctx := C.whisper_init_from_file_with_params(cpath, cp)
	if ctx == nil {
		return nil, errors.New("Whisper modeli yüklenemedi: " + path)
	}
	whisperCtx, whisperKey = ctx, key
	return ctx, nil
}

func freeWhisperLocked() {
	if whisperCtx != nil {
		C.whisper_free(whisperCtx)
		whisperCtx, whisperKey = nil, ""
	}
}

func unloadWhisper(path string) {
	whisperMu.Lock()
	defer whisperMu.Unlock()
	if path == "" || strings.HasPrefix(whisperKey, path+"|") {
		freeWhisperLocked()
	}
}

// whisperOptions are the per-session decoding settings.
type whisperOptions struct {
	language  string // "tr", "en", "auto", …
	translate bool   // translate to English
	threads   int
	silence   time.Duration // pause that ends a phrase
	vadPath   string        // Silero VAD model; "" = energy-based detection
	prompt    string        // hint text: names/terms Whisper should prefer
	// sensitivity is 0..100: higher = more readily treats sound as speech.
	sensitivity int
	minSpeech   time.Duration // shortest sound that counts as speech (filters clicks, coughs)
	onSpeech    func(bool)    // called when speech starts/stops (for a UI indicator)
}

// Whisper is not a streaming model, so streaming is emulated: while the user
// speaks, the current utterance is re-transcribed every partialEvery and shown
// as a partial; once a pause lasts opts.silence (or the utterance passes
// maxUtterance) it is transcribed once more and emitted as final.
const (
	frameSamples = 512             // 32 ms — Silero VAD's window size
	chunkSamples = sampleRate / 10 // 100 ms
	partialEvery = 600 * time.Millisecond
	maxUtterance = sampleRate * 25     // 25 s
	preRoll      = sampleRate * 3 / 10 // keep 0.3 s before speech starts
	minInput     = sampleRate * 11 / 10
)

var (
	bracketed      = regexp.MustCompile(`[\[(*♪][^\])*♪]*[\])*♪]`)
	hallucinations = []string{"altyazı m.k", "izlediğiniz için teşekkür", "abone olmayı unutmayın"}
)

type whisperRecognizer struct {
	ctx    *C.struct_whisper_context
	opts   whisperOptions
	lang   *C.char
	prompt *C.char // nil when no hint text
}

func newWhisper(path string, gpu bool, opts whisperOptions) (recognizer, error) {
	ctx, err := loadWhisper(path, gpu)
	if err != nil {
		return nil, err
	}
	if opts.vadPath != "" {
		if _, err := loadVAD(opts.vadPath); err != nil {
			return nil, err
		}
	}
	return &whisperRecognizer{ctx: ctx, opts: opts}, nil
}

// ---- speech detection ---------------------------------------------------

// speechDetector classifies 32 ms frames as speech or not.
type speechDetector interface {
	isSpeech(frame []float32) bool
}

// energyDetector is a simple loudness gate with an adaptive noise floor.
type energyDetector struct {
	noise float64
	floor float64 // minimum RMS that can count as speech
}

func newEnergyDetector(sensitivity int) *energyDetector {
	// sensitivity 50 → 0.007 (the old fixed value ≈0.006); 100 → 0.002; 0 → 0.012
	return &energyDetector{noise: 0.003, floor: 0.012 - float64(sensitivity)*0.0001}
}

func (e *energyDetector) isSpeech(frame []float32) bool {
	var sum float64
	for _, v := range frame {
		sum += float64(v * v)
	}
	rms := math.Sqrt(sum / float64(len(frame)))
	if rms > math.Max(e.floor, e.noise*2.5) {
		return true
	}
	e.noise = 0.95*e.noise + 0.05*rms
	return false
}

// sileroDetector runs whisper.cpp's Silero VAD frame by frame, keeping its
// LSTM state across frames, with hysteresis so speech doesn't flicker.
type sileroDetector struct {
	vctx     *C.struct_whisper_vad_context
	on, off  float32 // speech starts at p >= on, ends at p < off
	inSpeech bool
}

func newSileroDetector(vctx *C.struct_whisper_vad_context, sensitivity int) *sileroDetector {
	// sensitivity 50 → on 0.50 / off 0.35 (Silero's usual values);
	// 0 → 0.85 (only clear speech); 100 → 0.15 (whispers, distant voices).
	on := float32(0.85 - float64(sensitivity)*0.007)
	return &sileroDetector{vctx: vctx, on: on, off: max(on-0.15, 0.05)}
}

func (s *sileroDetector) isSpeech(frame []float32) bool {
	if !C.whisper_vad_detect_speech_no_reset(s.vctx, (*C.float)(unsafe.Pointer(&frame[0])), C.int(len(frame))) {
		return false
	}
	if C.whisper_vad_n_probs(s.vctx) < 1 {
		return false
	}
	p := float32(*C.whisper_vad_probs(s.vctx))
	if s.inSpeech {
		s.inSpeech = p >= s.off
	} else {
		s.inSpeech = p >= s.on
	}
	return s.inSpeech
}

// Only one VAD context is kept; it is reused across sessions (state reset on start).
var (
	vadMu   sync.Mutex
	vadCtx  *C.struct_whisper_vad_context
	vadPath string
)

func loadVAD(path string) (*C.struct_whisper_vad_context, error) {
	vadMu.Lock()
	defer vadMu.Unlock()
	if vadCtx != nil && vadPath == path {
		return vadCtx, nil
	}
	if vadCtx != nil {
		C.whisper_vad_free(vadCtx)
		vadCtx = nil
	}
	C.whisper_quiet()
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	cp := C.whisper_vad_default_context_params()
	cp.n_threads = 1
	cp.use_gpu = false // tiny model; CPU is faster than a GPU round-trip per frame
	v := C.whisper_vad_init_from_file_with_params(cpath, cp)
	if v == nil {
		return nil, errors.New("Silero VAD modeli yüklenemedi: " + path)
	}
	vadCtx, vadPath = v, path
	return v, nil
}

func unloadVAD() {
	vadMu.Lock()
	defer vadMu.Unlock()
	if vadCtx != nil {
		C.whisper_vad_free(vadCtx)
		vadCtx, vadPath = nil, ""
	}
}

// ---- audio buffering ----------------------------------------------------

// audioState is shared between the mic reader and the transcription worker.
type audioState struct {
	mu         sync.Mutex
	buf        []float32
	speech     bool // utterance in buf contains speech
	speechRun  int  // consecutive speech frames
	lastSpeech int  // index in buf where speech was last heard
	eof        bool
	wake       chan struct{}
	det        speechDetector
	minRun     int        // consecutive speech frames needed to start an utterance
	onSpeech   func(bool) // optional speech indicator callback
	speaking   bool       // last value sent to onSpeech
}

func (a *audioState) read(r io.Reader) {
	raw := make([]byte, frameSamples*2)
	for {
		n, err := io.ReadFull(r, raw)
		if n >= 2 {
			frame := make([]float32, n/2)
			for i := range frame {
				frame[i] = float32(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
			}
			speaking := a.det.isSpeech(frame) // only this goroutine touches det

			a.mu.Lock()
			a.buf = append(a.buf, frame...)
			if speaking {
				a.speechRun++
				if a.speechRun >= a.minRun {
					a.speech = true
				}
				a.lastSpeech = len(a.buf)
			} else {
				a.speechRun = 0
				if !a.speech && len(a.buf) > preRoll { // nothing said yet: keep only pre-roll
					a.buf = append(a.buf[:0], a.buf[len(a.buf)-preRoll:]...)
					a.lastSpeech = 0
				}
			}
			confirmed := speaking && a.speechRun >= a.minRun
			a.mu.Unlock()
			if a.onSpeech != nil && confirmed != a.speaking && (confirmed || !speaking) {
				a.speaking = confirmed
				a.onSpeech(confirmed)
			}
		}
		if err != nil {
			if a.onSpeech != nil && a.speaking {
				a.onSpeech(false)
			}
			a.mu.Lock()
			a.eof = true
			a.mu.Unlock()
			select {
			case a.wake <- struct{}{}:
			default:
			}
			return
		}
	}
}

func (w *whisperRecognizer) run(r io.Reader, emit func(kind, text string)) {
	w.lang = C.CString(w.opts.language)
	defer C.free(unsafe.Pointer(w.lang))
	if w.opts.prompt != "" {
		w.prompt = C.CString(w.opts.prompt)
		defer C.free(unsafe.Pointer(w.prompt))
	}
	finalSilence := int(w.opts.silence.Seconds() * sampleRate)

	var det speechDetector = newEnergyDetector(w.opts.sensitivity)
	if w.opts.vadPath != "" {
		vctx, err := loadVAD(w.opts.vadPath)
		if err == nil {
			C.whisper_vad_reset_state(vctx)
			det = newSileroDetector(vctx, w.opts.sensitivity)
		}
	}
	minRun := max(1, int(w.opts.minSpeech/(frameSamples*time.Second/sampleRate)))
	a := &audioState{wake: make(chan struct{}, 1), det: det, minRun: minRun, onSpeech: w.opts.onSpeech}
	go a.read(r)

	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var lastPartialAt time.Time
	lastPartialLen := 0

	for {
		select {
		case <-tick.C:
		case <-a.wake:
		}

		a.mu.Lock()
		snap := append([]float32(nil), a.buf...)
		speech, lastSpeech, eof := a.speech, a.lastSpeech, a.eof
		a.mu.Unlock()
		n := len(snap)

		if !speech {
			if eof {
				emit("partial", "")
				return
			}
			continue
		}

		if eof || n-lastSpeech >= finalSilence || n >= maxUtterance {
			if text := w.transcribe(snap, false); text != "" {
				emit("final", text)
			}
			emit("partial", "")
			lastPartialLen = 0

			a.mu.Lock()
			a.buf = append([]float32(nil), a.buf[n:]...)
			a.lastSpeech -= n
			if a.lastSpeech <= 0 {
				a.lastSpeech, a.speech, a.speechRun = 0, false, 0
			}
			a.mu.Unlock()
			if eof {
				return
			}
			continue
		}

		if time.Since(lastPartialAt) >= partialEvery && n-lastPartialLen >= chunkSamples*3 {
			lastPartialAt, lastPartialLen = time.Now(), n
			if text := w.transcribe(snap, true); text != "" {
				emit("partial", text)
			}
		}
	}
}

func (w *whisperRecognizer) transcribe(samples []float32, partial bool) string {
	if len(samples) < minInput { // whisper.cpp ignores input shorter than 1 s
		samples = append(samples, make([]float32, minInput-len(samples))...)
	}

	p := C.whisper_full_default_params(C.WHISPER_SAMPLING_GREEDY)
	p.language = w.lang
	p.translate = C.bool(w.opts.translate)
	p.n_threads = C.int(w.opts.threads)
	p.no_context = true
	p.no_timestamps = true
	p.print_progress = false
	p.print_realtime = false
	p.print_special = false
	p.print_timestamps = false
	p.suppress_nst = true
	if w.prompt != nil {
		p.initial_prompt = w.prompt
	}
	// Size the encoder window to the audio instead of the fixed 30 s (each
	// unit is 20 ms) — several times faster for short utterances.
	sec := len(samples) / sampleRate
	if partial {
		// Live preview: speed over quality. One segment, no temperature
		// fallback, and a token cap so a short window can't loop forever.
		p.single_segment = true
		p.temperature_inc = 0
		p.max_tokens = C.int(sec*8 + 16)
		p.audio_ctx = C.int(min(1500, sec*50+200))
	} else {
		p.audio_ctx = C.int(min(1500, sec*50+400))
	}

	if C.whisper_full(w.ctx, p, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples))) != 0 {
		return ""
	}
	var sb strings.Builder
	for i := range int(C.whisper_full_n_segments(w.ctx)) {
		sb.WriteString(C.GoString(C.whisper_full_get_segment_text(w.ctx, C.int(i))))
	}
	return clean(sb.String())
}

// clean drops bracketed sound tags ("[Müzik]") and well-known Whisper
// hallucinations that it produces on silence or noise.
func clean(s string) string {
	s = collapseRepeats(strings.TrimSpace(bracketed.ReplaceAllString(s, "")))
	l := strings.ToLower(s)
	for _, h := range hallucinations {
		if strings.Contains(l, h) {
			return ""
		}
	}
	return s
}

// collapseRepeats cuts decoder loops — a word or phrase (up to 8 words)
// repeated back-to-back more than twice, like "çok çok çok …" or
// "ne yapabilirsiniz, ne yapabilirsiniz, …" — down to two occurrences.
func collapseRepeats(s string) string {
	words := strings.Fields(s)
	norm := func(w string) string { return strings.ToLower(strings.Trim(w, ".,!?;:")) }
	same := func(a, b, n int) bool { // words[a:a+n] == words[b:b+n]
		for k := range n {
			if norm(words[a+k]) != norm(words[b+k]) {
				return false
			}
		}
		return true
	}
	for n := 1; n <= 8; n++ {
		out := make([]string, 0, len(words))
		for i := 0; i < len(words); {
			// Count how many times words[i:i+n] repeats consecutively.
			reps := 1
			for i+(reps+1)*n <= len(words) && same(i, i+reps*n, n) {
				reps++
			}
			if reps > 2 {
				out = append(out, words[i:i+2*n]...)
				i += reps * n
			} else {
				out = append(out, words[i])
				i++
			}
		}
		words = out
	}
	return strings.Join(words, " ")
}

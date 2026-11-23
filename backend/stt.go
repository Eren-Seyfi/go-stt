package backend

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[string]("stt:partial")
	application.RegisterEvent[FinalText]("stt:final")
	application.RegisterEvent[float64]("stt:level")
	application.RegisterEvent[string]("stt:stopped")
	application.RegisterEvent[string]("stt:started")
	application.RegisterEvent[bool]("stt:wake")
	application.RegisterEvent[string]("stt:heard")
	application.RegisterEvent[bool]("stt:speech")
	application.RegisterEvent[CommandMatch]("stt:command")
	application.RegisterEvent[ModelProgress]("model:progress")
	application.RegisterEvent[ModelDone]("model:done")
}

// recognizer consumes raw 16 kHz mono s16le audio until r hits EOF,
// calling emit("partial"|"final", text) as words are recognized.
type recognizer interface {
	run(r io.Reader, emit func(kind, text string))
}

// FinalText is emitted as "stt:final" for each finished phrase.
type FinalText struct {
	Text string `json:"text"`
	At   int64  `json:"at"` // unix ms when the phrase finished
}

// StartOptions configures a listening session.
type StartOptions struct {
	ModelID    string   `json:"modelId"`
	Device     string   `json:"device"`      // "cpu" | "gpu"
	Language   string   `json:"language"`    // "tr", "en", "auto", …
	Translate  bool     `json:"translate"`   // translate to English
	Threads    int      `json:"threads"`     // CPU threads (0 = auto)
	Microphone string   `json:"microphone"`  // PulseAudio source ("" = default)
	SilenceMs  int      `json:"silenceMs"`   // pause that ends a phrase
	VAD        bool     `json:"vad"`         // use Silero VAD for speech detection
	VADModel   string   `json:"vadModel"`    // VAD model id ("" = silero-vad)
	Prompt     string   `json:"prompt"`      // names/terms Whisper should prefer ("" = none)
	VADSens    int      `json:"vadSens"`     // speech detection sensitivity 0..100 (50 = default)
	MinSpeech  int      `json:"minSpeechMs"` // shortest sound counted as speech, ms (0 = 100)
	WakeWord   bool     `json:"wakeWord"`    // only transcribe after a wake phrase
	Commands   Commands `json:"commands"`    // voice command phrases
	WakeSec    int      `json:"wakeSec"`     // seconds of silence before going back to standby
}

// SystemInfo describes the compute hardware.
type SystemInfo struct {
	CPUName    string    `json:"cpuName"`
	CPUThreads int       `json:"cpuThreads"`
	GPUs       []GPUInfo `json:"gpus"`
	GPUNote    string    `json:"gpuNote"` // why no GPU is available, if so
}

// STTService captures microphone audio and streams it to the selected model,
// emitting "stt:partial" (in-progress words), "stt:final" (finished phrase),
// "stt:level" (input level 0..1) and "stt:stopped" (session ended; payload
// is an error message or "").
type STTService struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	modelID string
	sys     *SystemInfo
	filter  *commandFilter // voice commands / wake mode of the current session
	busy    bool
}

func NewSTTService() *STTService { return &STTService{} }

func (s *STTService) ServiceShutdown() error {
	s.Stop()
	unloadVAD()
	unloadWhisper("")
	return nil
}

// System reports CPU and GPU details (cached after the first call).
func (s *STTService) System() SystemInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sys == nil {
		info := SystemInfo{CPUName: cpuName(), CPUThreads: runtime.NumCPU(), GPUs: detectGPUs()}
		if len(info.GPUs) == 0 {
			info.GPUNote = gpuNote()
		}
		s.sys = &info
	}
	return *s.sys
}

// DefaultThreads is a sensible thread count for Whisper on this CPU.
func (s *STTService) DefaultThreads() int { return min(runtime.NumCPU(), 8) }

// Languages lists every language Whisper understands.
func (s *STTService) Languages() []Language { return whisperLanguages() }

// TestCommand shows which voice command (if any) a sentence would trigger.
func (s *STTService) TestCommand(text string, cmds Commands) CommandMatch {
	return matchCommand(text, cmds)
}

// Microphones lists audio input sources; the system default comes first.
func (s *STTService) Microphones() ([]Microphone, error) { return listMicrophones() }

// IsListening reports whether a session is running.
func (s *STTService) IsListening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *STTService) activeModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return ""
	}
	return s.modelID
}

// Start loads the chosen model (if needed) and begins listening.
func (s *STTService) Start(opts StartOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return errors.New("zaten dinleniyor")
	}

	c, err := findModel(opts.ModelID)
	if err != nil {
		return err
	}
	if !c.installed() {
		return errors.New(c.Name + " yüklü değil; önce indirin")
	}

	if c.Engine != EngineWhisper {
		return errors.New(c.Name + " bir konuşma tanıma modeli değil")
	}
	cmds := opts.Commands
	if opts.WakeWord && len(compilePhrases(cmds.Wake)) == 0 {
		return errors.New("uyandırma modu için en az bir başlatma komutu gerekli")
	}
	gpu := opts.Device == "gpu"
	if gpu && len(detectGPUs()) == 0 {
		return errors.New("GPU kullanılamıyor. " + gpuNote())
	}
	threads := opts.Threads
	if threads <= 0 {
		threads = min(runtime.NumCPU(), 8)
	}
	silence := time.Duration(opts.SilenceMs) * time.Millisecond
	if silence < 300*time.Millisecond {
		silence = 800 * time.Millisecond
	}
	lang := opts.Language
	if lang == "" {
		lang = "tr"
	}
	translate := opts.Translate
	if c.Language != "" { // single-language model (e.g. ".en")
		lang, translate = c.Language, false
	}
	minSpeech := time.Duration(opts.MinSpeech) * time.Millisecond
	if minSpeech <= 0 {
		minSpeech = 100 * time.Millisecond
	}
	vadPath := ""
	if opts.VAD {
		if vadPath, err = ensureVAD(opts.VADModel); err != nil {
			return errors.New("Silero VAD modeli indirilemedi: " + err.Error())
		}
	}
	rec, err := newWhisper(c.path(), gpu, whisperOptions{language: lang, translate: translate, threads: threads, silence: silence, vadPath: vadPath, prompt: strings.TrimSpace(opts.Prompt),
		sensitivity: min(max(opts.VADSens, 0), 100), minSpeech: minSpeech,
		onSpeech: func(on bool) { application.Get().Event.Emit("stt:speech", on) }})
	if err != nil {
		return err
	}

	var stderr strings.Builder
	cmd, stdout, err := startCapture(opts.Microphone, &stderr)
	if err != nil {
		return errors.New("mikrofon açılamadı (parecord): " + err.Error())
	}

	wakeWord := opts.WakeWord
	wakeTimeout := time.Duration(max(opts.WakeSec, 3)) * time.Second

	app := application.Get()
	emit := func(kind, text string) {
		if kind == "final" {
			app.Event.Emit("stt:final", FinalText{Text: text, At: time.Now().UnixMilli()})
		} else {
			app.Event.Emit("stt:partial", text)
		}
	}
	var cf *commandFilter
	if wakeWord || len(cmds.Stop) > 0 {
		cf = newCommandFilter(cmds, wakeWord, wakeTimeout, emit,
			func(active bool) { app.Event.Emit("stt:wake", active) },
			func(text string) { app.Event.Emit("stt:heard", text) },
			func(m CommandMatch) { app.Event.Emit("stt:command", m) },
			s.Stop)
		cf.setBusy(s.busy)
		emit = cf.emit
	}

	s.cmd, s.modelID, s.filter = cmd, c.ID, cf
	app.Event.Emit("stt:started", c.ID)
	s.done = make(chan struct{})
	go func(cmd *exec.Cmd, done chan struct{}) {
		if cf != nil {
			defer cf.close()
		}
		lr := &levelReader{r: stdout, emit: func(l float64) { app.Event.Emit("stt:level", l) }}
		rec.run(lr, emit)
		app.Event.Emit("stt:level", 0.0)
		close(done)

		// If the recorder died on its own (not via Stop), report it.
		s.mu.Lock()
		unexpected := s.cmd == cmd
		if unexpected {
			s.cmd, s.filter = nil, nil
		}
		s.mu.Unlock()
		if unexpected {
			_ = cmd.Wait()
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "ses kaydı beklenmedik şekilde durdu"
			}
			app.Event.Emit("stt:stopped", msg)
		}
	}(cmd, s.done)
	return nil
}

// Stop ends listening; the last phrase is flushed before it returns.
func (s *STTService) Stop() {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.cmd, s.filter = nil, nil
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Process.Kill()
	<-done
	_ = cmd.Wait()
	application.Get().Event.Emit("stt:stopped", "")
}

// SetBusy tells wake mode that the app is busy (the AI is replying), so it
// doesn't go back to standby meanwhile. The standby countdown starts over
// once it is no longer busy.
func (s *STTService) SetBusy(busy bool) {
	s.mu.Lock()
	s.busy = busy
	f := s.filter
	s.mu.Unlock()
	if f != nil {
		f.setBusy(busy)
	}
}

// CopyText puts text on the system clipboard.
func (s *STTService) CopyText(text string) bool {
	return application.Get().Clipboard.SetText(text)
}

// SaveText asks for a file name and writes text to it. Returns the chosen
// path, or "" if the user cancelled.
func (s *STTService) SaveText(text, suggestedName string) (string, error) {
	path, err := application.Get().Dialog.SaveFile().
		SetFilename(suggestedName).
		AddFilter("Metin dosyası", "*.txt").
		AddFilter("Markdown", "*.md").
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(text), 0o644)
}

func cpuName() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return runtime.GOARCH
}

// gpuNote explains why no GPU was found.
func gpuNote() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "-L").CombinedOutput()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "NVIDIA sürücüsü bulunamadı."
	case strings.Contains(string(out), "version mismatch"):
		return "NVIDIA sürücüsü güncellenmiş; bilgisayarı yeniden başlatın."
	case err != nil:
		return "NVIDIA sürücüsü başlatılamadı: " + strings.TrimSpace(string(out))
	default:
		return "CUDA cihazı başlatılamadı."
	}
}

// unloadModel frees a model from memory (before it is deleted).
func unloadModel(path string) {
	unloadWhisper(path)
}

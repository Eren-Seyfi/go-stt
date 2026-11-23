package backend

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const sampleRate = 16000

// Microphone is an audio input source known to PulseAudio/PipeWire.
type Microphone struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

// listMicrophones returns input sources, excluding speaker monitors.
func listMicrophones() ([]Microphone, error) {
	out, err := hostCommand("pactl", "-f", "json", "list", "sources").Output()
	if err != nil {
		return nil, err
	}
	var sources []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out, &sources); err != nil {
		return nil, err
	}
	def, _ := hostCommand("pactl", "get-default-source").Output()
	defName := strings.TrimSpace(string(def))

	mics := []Microphone{}
	for _, s := range sources {
		if strings.HasSuffix(s.Name, ".monitor") {
			continue
		}
		desc := s.Description
		if desc == "" {
			desc = s.Name
		}
		mics = append(mics, Microphone{Name: s.Name, Description: desc, Default: s.Name == defName})
	}
	sort.SliceStable(mics, func(i, j int) bool { return mics[i].Default && !mics[j].Default })
	return mics, nil
}

// startCapture records 16 kHz mono s16le audio from the given source
// (empty = system default).
func startCapture(device string, stderr io.Writer) (*exec.Cmd, io.ReadCloser, error) {
	args := []string{"--raw", "--format=s16le", "--rate=16000", "--channels=1", "--latency-msec=50"}
	if device != "" {
		args = append(args, "--device="+device)
	}
	cmd := hostCommand("parecord", args...)
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, out, nil
}

// levelReader passes audio through unchanged while reporting the input
// level (0..1, roughly perceptual) at most every 60 ms.
type levelReader struct {
	r     io.Reader
	emit  func(float64)
	last  time.Time
	peak  float64
	carry []byte
}

func (l *levelReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	buf := append(l.carry, p[:n]...)
	even := len(buf) &^ 1
	for i := 0; i < even; i += 2 {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(buf[i:]))) / 32768)
		if v > l.peak {
			l.peak = v
		}
	}
	l.carry = append(l.carry[:0], buf[even:]...)
	if time.Since(l.last) >= 60*time.Millisecond {
		// Map peak amplitude to a 0..1 meter using a -50..0 dB range.
		level := 0.0
		if l.peak > 0 {
			level = math.Max(0, math.Min(1, (20*math.Log10(l.peak)+50)/50))
		}
		l.emit(level)
		l.peak, l.last = 0, time.Now()
	}
	return n, err
}

package backend

import (
	"encoding/binary"
	"io"
	"math"
	"time"
)

// Audio capture is per-OS:
//
//	audio_pulse.go    Linux & other Unix: PulseAudio/PipeWire (pactl, parecord)
//	audio_windows.go  Windows: winmm waveIn API
//
// Each provides listMicrophones() and startCapture(), which delivers 16 kHz
// mono s16le audio.

const sampleRate = 16000

// Microphone is an audio input device.
type Microphone struct {
	Name        string `json:"name"` // id passed back to startCapture
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

// audioCapture is a running recording.
type audioCapture interface {
	// Stop ends the recording; the audio reader then reaches EOF.
	Stop()
	// Wait blocks until the recording has fully ended.
	Wait() error
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

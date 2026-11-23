//go:build !windows

package backend

import (
	"encoding/json"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// listMicrophones returns PulseAudio/PipeWire input sources, excluding
// speaker monitors.
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

// pulseCapture is a running parecord process.
type pulseCapture struct{ cmd *exec.Cmd }

func (p pulseCapture) Stop()       { _ = p.cmd.Process.Kill() }
func (p pulseCapture) Wait() error { return p.cmd.Wait() }

// startCapture records 16 kHz mono s16le audio from the given source
// (empty = system default) with parecord.
func startCapture(device string, stderr io.Writer) (audioCapture, io.ReadCloser, error) {
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
	return pulseCapture{cmd}, out, nil
}

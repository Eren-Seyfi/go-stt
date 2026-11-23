package backend

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The "llamacpp" AI provider runs GGUF models locally with llama-server
// (built into third_party/llama). The app starts the server on demand, on a
// free loopback port protected by a random API key, and talks to it through
// its OpenAI-compatible API.

func init() {
	application.RegisterEvent[LlamaStatus]("llama:status")
}

// LlamaConfig holds the local model server settings.
type LlamaConfig struct {
	GPULayers string `json:"gpuLayers"` // "auto" | "all" | a number ("0" = CPU only)
	Context   int    `json:"context"`   // context size in tokens (0 = model default)
}

func defaultLlamaConfig() LlamaConfig { return LlamaConfig{GPULayers: "auto", Context: 8192} }

// LlamaStatus is emitted as "llama:status" whenever the server state changes.
type LlamaStatus struct {
	State   string   `json:"state"`   // stopped | starting | ready | error
	Model   string   `json:"model"`   // model being served (path or Hugging Face repo)
	Message string   `json:"message"` // latest progress / error text
	Binary  string   `json:"binary"`  // llama-server path ("" = not found)
	GPU     string   `json:"gpu"`     // device the model runs on, when known
	Log     []string `json:"log"`     // last lines of server output
}

// LocalModel is a GGUF model found on this computer.
type LocalModel struct {
	Spec   string     `json:"spec"`   // what to put in the model field: a path or "user/repo[:quant]"
	Name   string     `json:"name"`   // display name
	Origin string     `json:"origin"` // lmstudio | huggingface | diger (models in the app folder)
	Source string     `json:"source"` // where it was found
	Size   int64      `json:"size"`
	Vision bool       `json:"vision"`         // has a multimodal projector (can see images)
	Caps   *ModelCaps `json:"caps,omitempty"` // capabilities from the GGUF metadata
}

type llamaKey struct {
	spec string
	cfg  LlamaConfig
}

// LlamaService manages the llama-server process.
type LlamaService struct {
	store   *Store
	onStats func() // tells the UI that model statistics changed
	mu      sync.Mutex
	cmd     *exec.Cmd
	key     llamaKey
	port    int
	apiKey  string
	status  LlamaStatus
	ready   chan struct{} // closed when the server is ready (or failed: see status)
	exited  chan struct{}
	log     []string
}

func NewLlamaService(store *Store) *LlamaService {
	s := &LlamaService{store: store}
	s.status = LlamaStatus{State: "stopped", Binary: findLlamaServer()}
	killOrphanServers(s.status.Binary)
	return s
}

func (s *LlamaService) ServiceShutdown() error {
	s.Stop()
	return nil
}

// llamaServerName is the llama-server file name on this OS.
func llamaServerName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// isRunnable reports whether st is a file this OS can execute. Windows has no
// execute bits (Go reports 0666/0444), so there a regular file is enough —
// the .exe extension is already in the candidate name.
func isRunnable(st os.FileInfo) bool {
	if st.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || st.Mode()&0o111 != 0
}

// findLlamaServer looks for the llama-server binary.
func findLlamaServer() string {
	name := llamaServerName()
	var cands []string
	if p := os.Getenv("STT_LLAMA_SERVER"); p != "" {
		cands = append(cands, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands,
			filepath.Join(dir, name), // Windows release: llama-server.exe next to the app
			filepath.Join(dir, "llama", "bin", name),
			filepath.Join(dir, "..", "third_party", "llama", "bin", name))
	}
	if _, file, _, ok := runtime.Caller(0); ok { // development builds: next to the sources
		cands = append(cands, filepath.Join(filepath.Dir(file), "..", "third_party", "llama", "bin", name))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "third_party", "llama", "bin", name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && isRunnable(st) {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p
	}
	return ""
}

// Status returns the server state.
func (s *LlamaService) Status() LlamaStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *LlamaService) snapshotLocked() LlamaStatus {
	st := s.status
	st.Log = append([]string(nil), s.log...)
	return st
}

func (s *LlamaService) emitLocked() {
	if app := application.Get(); app != nil {
		app.Event.Emit("llama:status", s.snapshotLocked())
	}
}

func (s *LlamaService) setLocked(state, msg string) {
	s.status.State, s.status.Message = state, msg
	s.emitLocked()
}

// Stop shuts the server down.
func (s *LlamaService) Stop() {
	s.mu.Lock()
	cmd, exited := s.cmd, s.exited
	s.cmd = nil
	if cmd != nil {
		s.setLocked("stopped", "")
	}
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	terminateServer(cmd)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		killServer(cmd)
		<-exited
	}
}

// Start loads a model (a .gguf path or a Hugging Face repo) in the background.
func (s *LlamaService) Start(spec string, cfg LlamaConfig) error {
	_, err := s.start(spec, cfg)
	return err
}

// ensure makes sure the server runs the given model and waits until it is
// ready. It returns the server's base URL and API key.
func (s *LlamaService) ensure(ctx context.Context, spec string, cfg LlamaConfig) (string, string, error) {
	ready, err := s.start(spec, cfg)
	if err != nil {
		return "", "", err
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != "ready" || s.key.spec != strings.TrimSpace(spec) {
		msg := s.status.Message
		if msg == "" {
			msg = "yerel model başlatılamadı"
		}
		return "", "", errors.New(msg)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1", s.port), s.apiKey, nil
}

// start (re)starts the server if it isn't already serving this model with
// these settings, and returns a channel closed once it's ready or failed.
func (s *LlamaService) start(spec string, cfg LlamaConfig) (chan struct{}, error) {
	spec = strings.TrimSpace(spec)
	if _, err := resolveModel(spec); err != nil {
		return nil, err
	}
	if cfg.GPULayers == "" {
		cfg.GPULayers = "auto"
	}
	key := llamaKey{spec, cfg}

	s.mu.Lock()
	if s.cmd != nil && s.key == key && s.status.State != "error" {
		ready := s.ready
		s.mu.Unlock()
		return ready, nil
	}
	s.mu.Unlock()
	s.Stop() // a different model or settings: restart

	bin := findLlamaServer()
	if bin == "" {
		return nil, errors.New("llama-server bulunamadı (third_party/llama/bin). llama.cpp'yi derleyin veya STT_LLAMA_SERVER ortam değişkenini ayarlayın")
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	var rnd [16]byte
	_, _ = rand.Read(rnd[:])
	apiKey := hex.EncodeToString(rnd[:])

	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--api-key", apiKey,
		"--no-webui", "--jinja", "-fa", "auto", "-ngl", cfg.GPULayers, "--alias", "yerel",
		"--metrics"} // token counters, so replies can be measured exactly
	if cfg.Context > 0 {
		args = append(args, "-c", strconv.Itoa(cfg.Context))
	}
	modelPath, err := resolveModel(spec) // always a file in models/llm: nothing is fetched on the fly
	if err != nil {
		return nil, err
	}
	args = append(args, "-m", modelPath, "--offline")
	if mm := findMMProj(modelPath); mm != "" {
		args = append(args, "--mmproj", mm)
	}

	cmd := hostCommand(bin, args...)
	setServerProcAttrs(cmd) // per-OS: process group, die-with-parent, no console window
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("llama-server başlatılamadı: %w", err)
	}

	ready, exited := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.cmd, s.key, s.port, s.apiKey, s.ready, s.exited, s.log = cmd, key, port, apiKey, ready, exited, nil
	s.status = LlamaStatus{State: "starting", Model: spec, Binary: bin, Message: "Başlatılıyor…"}
	s.emitLocked()
	s.mu.Unlock()

	var once sync.Once
	finish := func() { once.Do(func() { close(ready) }) }

	go s.readLog(cmd, pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		s.mu.Lock()
		if s.cmd == cmd { // died on its own
			s.cmd = nil
			msg := "llama-server kapandı"
			if err != nil {
				msg += " (" + err.Error() + ")"
			}
			if last := s.lastErrorLocked(); last != "" {
				msg += ": " + last
			}
			s.setLocked("error", msg)
		}
		s.mu.Unlock()
		close(exited)
		finish()
	}()
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		for {
			select {
			case <-exited:
				return
			case <-time.After(500 * time.Millisecond):
			}
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				s.mu.Lock()
				if s.cmd == cmd {
					s.setLocked("ready", "Hazır")
				}
				s.mu.Unlock()
				finish()
				return
			}
		}
	}()
	return ready, nil
}

// readLog keeps the last lines of server output and shows progress
// (downloads print with \r, so split on both).
func (s *LlamaService) readLog(cmd *exec.Cmd, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\n' || b == '\r' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	lastEmit := time.Time{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.mu.Lock()
		if s.cmd != cmd {
			s.mu.Unlock()
			continue
		}
		s.log = append(s.log, line)
		if len(s.log) > 200 {
			s.log = s.log[len(s.log)-200:]
		}
		if dev := gpuFromLog(line); dev != "" {
			s.status.GPU = dev
		}
		if s.status.State == "starting" {
			if msg := progressFromLog(line); msg != "" {
				s.status.Message = msg
			}
			if time.Since(lastEmit) > 300*time.Millisecond {
				s.emitLocked()
				lastEmit = time.Now()
			}
		}
		s.mu.Unlock()
	}
}

func (s *LlamaService) lastErrorLocked() string {
	for i := len(s.log) - 1; i >= 0 && i >= len(s.log)-30; i-- {
		l := strings.ToLower(s.log[i])
		if strings.Contains(l, "error") || strings.Contains(l, "failed") || strings.Contains(l, "unable") {
			return s.log[i]
		}
	}
	return ""
}

func progressFromLog(line string) string {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "download") || strings.Contains(l, "%"):
		return "İndiriliyor: " + trimTo(line, 120)
	case strings.Contains(l, "loading model"), strings.Contains(l, "load_tensors"):
		return "Model belleğe yükleniyor…"
	case strings.Contains(l, "mmproj"), strings.Contains(l, "clip_model_loader"):
		return "Görüntü modülü yükleniyor…"
	case strings.Contains(l, "warming up"), strings.Contains(l, "warmup"):
		return "Isınıyor…"
	}
	return ""
}

func gpuFromLog(line string) string {
	// e.g. "ggml_cuda_init: found 1 CUDA devices:" / "Device 0: NVIDIA GeForce RTX 3060, compute capability 8.6"
	if i := strings.Index(line, "Device 0: "); i >= 0 {
		dev := line[i+len("Device 0: "):]
		if j := strings.Index(dev, ","); j > 0 {
			dev = dev[:j]
		}
		return dev
	}
	if strings.Contains(line, "failed to initialize CUDA") {
		return "CPU (CUDA kullanılamadı)"
	}
	return ""
}

func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// findMMProj finds a multimodal projector next to a model file.
func findMMProj(model string) string {
	entries, err := os.ReadDir(filepath.Dir(model))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "mmproj") && strings.HasSuffix(n, ".gguf") {
			return filepath.Join(filepath.Dir(model), e.Name())
		}
	}
	return ""
}

// PickModelFile asks the user for a .gguf file.
func (s *LlamaService) PickModelFile() (string, error) {
	return application.Get().Dialog.OpenFile().
		SetTitle("GGUF modeli seçin").
		AddFilter("GGUF modelleri", "*.gguf").
		PromptForSingleSelection()
}

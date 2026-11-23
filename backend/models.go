package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	EngineWhisper = "whisper"
	EngineVAD     = "vad" // helper model, not selectable for transcription
)

// ModelInfo describes a speech model in the catalog and its local state.
type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Description string `json:"description"`
	SizeMB      int    `json:"sizeMB"`
	Accuracy    int    `json:"accuracy"` // 1..5
	Speed       int    `json:"speed"`    // 1..5
	Languages   string `json:"languages"`
	Language    string `json:"language"` // set for single-language models ("en"); "" = multilingual
	Installed   bool   `json:"installed"`
	Downloading bool   `json:"downloading"`
}

// ModelProgress is emitted as "model:progress" while a model downloads.
type ModelProgress struct {
	ID       string `json:"id"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
}

// ModelDone is emitted as "model:done" when a download ends; Error is empty on success.
type ModelDone struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type catalogEntry struct {
	ModelInfo
	url  string
	file string // file name in ModelsDir
}

const hf = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

var catalog = []catalogEntry{
	{ModelInfo{ID: "whisper-tiny", Name: "Whisper Tiny", Engine: EngineWhisper, SizeMB: 31, Accuracy: 1, Speed: 5, Languages: "99 dil",
		Description: "En küçük Whisper. Çok hızlı ama Türkçede sık hata yapar."}, hf + "ggml-tiny-q5_1.bin", "ggml-tiny-q5_1.bin"},
	{ModelInfo{ID: "whisper-base", Name: "Whisper Base", Engine: EngineWhisper, SizeMB: 57, Accuracy: 2, Speed: 5, Languages: "99 dil",
		Description: "Hızlı, zayıf bilgisayarlar için. Basit cümlelerde yeterli."}, hf + "ggml-base-q5_1.bin", "ggml-base-q5_1.bin"},
	{ModelInfo{ID: "whisper-small", Name: "Whisper Small", Engine: EngineWhisper, SizeMB: 181, Accuracy: 3, Speed: 4, Languages: "99 dil",
		Description: "CPU'da anlık kullanım için en iyi denge."}, hf + "ggml-small-q5_1.bin", "ggml-small-q5_1.bin"},
	{ModelInfo{ID: "whisper-medium", Name: "Whisper Medium", Engine: EngineWhisper, SizeMB: 514, Accuracy: 4, Speed: 2, Languages: "99 dil",
		Description: "Daha isabetli; CPU'da yavaş, GPU önerilir."}, hf + "ggml-medium-q5_0.bin", "ggml-medium-q5_0.bin"},
	{ModelInfo{ID: "whisper-turbo", Name: "Whisper Large v3 Turbo", Engine: EngineWhisper, SizeMB: 547, Accuracy: 5, Speed: 3, Languages: "99 dil",
		Description: "Large v3 kalitesine yakın, ondan çok daha hızlı. GPU ile en iyi seçim."}, hf + "ggml-large-v3-turbo-q5_0.bin", "ggml-large-v3-turbo-q5_0.bin"},
	{ModelInfo{ID: "whisper-large-v3", Name: "Whisper Large v3", Engine: EngineWhisper, SizeMB: 1080, Accuracy: 5, Speed: 1, Languages: "99 dil",
		Description: "En yüksek doğruluk. Anlık kullanım için GPU şart."}, hf + "ggml-large-v3-q5_0.bin", "ggml-large-v3-q5_0.bin"},
	{ModelInfo{ID: "whisper-tiny-en", Name: "Whisper Tiny (İngilizce)", Engine: EngineWhisper, SizeMB: 31, Accuracy: 2, Speed: 5, Languages: "Yalnızca İngilizce", Language: "en",
		Description: "Yalnızca İngilizce. Aynı boyuttaki çok dilli modelden daha isabetli."}, hf + "ggml-tiny.en-q5_1.bin", "ggml-tiny.en-q5_1.bin"},
	{ModelInfo{ID: "whisper-base-en", Name: "Whisper Base (İngilizce)", Engine: EngineWhisper, SizeMB: 57, Accuracy: 3, Speed: 5, Languages: "Yalnızca İngilizce", Language: "en",
		Description: "Yalnızca İngilizce. Hızlı ve CPU'da rahat çalışır."}, hf + "ggml-base.en-q5_1.bin", "ggml-base.en-q5_1.bin"},
	{ModelInfo{ID: "whisper-small-en", Name: "Whisper Small (İngilizce)", Engine: EngineWhisper, SizeMB: 181, Accuracy: 4, Speed: 4, Languages: "Yalnızca İngilizce", Language: "en",
		Description: "Yalnızca İngilizce. CPU'da İngilizce için en iyi denge."}, hf + "ggml-small.en-q5_1.bin", "ggml-small.en-q5_1.bin"},
	{ModelInfo{ID: "whisper-medium-en", Name: "Whisper Medium (İngilizce)", Engine: EngineWhisper, SizeMB: 514, Accuracy: 5, Speed: 2, Languages: "Yalnızca İngilizce", Language: "en",
		Description: "Yalnızca İngilizce, yüksek doğruluk. GPU önerilir."}, hf + "ggml-medium.en-q5_0.bin", "ggml-medium.en-q5_0.bin"},
	{ModelInfo{ID: "silero-vad", Name: "Silero VAD v6.2", Engine: EngineVAD, SizeMB: 1, Accuracy: 5, Speed: 5, Languages: "Dilden bağımsız",
		Description: "En yeni Silero sürümü (varsayılan). Konuşmayı gürültüden ayırır, cümle sonlarını yakalar. Gerektiğinde kendiliğinden iner."},
		"https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin", "ggml-silero-v6.2.0.bin"},
	{ModelInfo{ID: "silero-vad-v5", Name: "Silero VAD v5.1.2", Engine: EngineVAD, SizeMB: 1, Accuracy: 4, Speed: 5, Languages: "Dilden bağımsız",
		Description: "Önceki Silero sürümü. v6.2 bazı seslerde fazla ya da az hassas kalırsa karşılaştırmak için."},
		"https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin", "ggml-silero-v5.1.2.bin"},
}

func findModel(id string) (*catalogEntry, error) {
	for i := range catalog {
		if catalog[i].ID == id {
			return &catalog[i], nil
		}
	}
	return nil, errors.New("bilinmeyen model: " + id)
}

func (c *catalogEntry) path() string { return filepath.Join(ModelsDir(), c.file) }

func (c *catalogEntry) installed() bool {
	_, err := os.Stat(c.path())
	return err == nil
}

// ModelService lists, downloads and deletes speech models.
type ModelService struct {
	mu        sync.Mutex
	downloads map[string]context.CancelFunc
	stt       *STTService
}

func NewModelService(stt *STTService) *ModelService {
	return &ModelService{downloads: map[string]context.CancelFunc{}, stt: stt}
}

// List returns the catalog with each model's local state.
func (m *ModelService) List() []ModelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ModelInfo, 0, len(catalog))
	for _, c := range catalog {
		info := c.ModelInfo
		info.Installed = c.installed()
		_, info.Downloading = m.downloads[c.ID]
		out = append(out, info)
	}
	return out
}

// Download starts downloading a model in the background. Progress arrives as
// "model:progress" events and the result as a "model:done" event.
func (m *ModelService) Download(id string) error {
	c, err := findModel(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, busy := m.downloads[id]; busy {
		return nil
	}
	if c.installed() {
		return errors.New("model zaten yüklü")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.downloads[id] = cancel
	go func() {
		err := download(ctx, c)
		m.mu.Lock()
		delete(m.downloads, id)
		m.mu.Unlock()
		done := ModelDone{ID: id}
		if err != nil {
			done.Error = err.Error()
			if ctx.Err() != nil {
				done.Error = "iptal edildi"
			}
		}
		application.Get().Event.Emit("model:done", done)
	}()
	return nil
}

// CancelDownload stops an in-progress download.
func (m *ModelService) CancelDownload(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel, ok := m.downloads[id]; ok {
		cancel()
	}
}

// Delete removes a downloaded model from disk.
func (m *ModelService) Delete(id string) error {
	c, err := findModel(id)
	if err != nil {
		return err
	}
	if m.stt.activeModel() == id {
		return errors.New("bu model şu an kullanılıyor; önce dinlemeyi durdurun")
	}
	unloadModel(c.path())
	return os.RemoveAll(c.path())
}

// OpenFolder opens the models folder in the file manager.
func (m *ModelService) OpenFolder() error {
	if err := os.MkdirAll(ModelsDir(), 0o755); err != nil {
		return err
	}
	return hostCommand("xdg-open", ModelsDir()).Start()
}

// ModelsPath returns the models folder path for display.
func (m *ModelService) ModelsPath() string { return ModelsDir() }

func download(ctx context.Context, c *catalogEntry) error {
	if err := os.MkdirAll(ModelsDir(), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("indirme başarısız: HTTP %d", resp.StatusCode)
	}

	tmp := filepath.Join(ModelsDir(), "."+c.ID+".part")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	app := application.Get()
	pw := &progressWriter{id: c.ID, total: resp.ContentLength, emit: func(p ModelProgress) { app.Event.Emit("model:progress", p) }}
	_, err = io.Copy(io.MultiWriter(f, pw), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	pw.flush()

	return os.Rename(tmp, c.path())
}

type progressWriter struct {
	id       string
	total    int64
	received int64
	last     time.Time
	emit     func(ModelProgress)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	if time.Since(p.last) > 150*time.Millisecond {
		p.flush()
	}
	return len(b), nil
}

func (p *progressWriter) flush() {
	p.last = time.Now()
	p.emit(ModelProgress{ID: p.id, Received: p.received, Total: p.total})
}

// ensureVAD returns the path of the given VAD model, downloading it (≈1 MB) first if needed.
func ensureVAD(id string) (string, error) {
	if id == "" {
		id = "silero-vad"
	}
	c, err := findModel(id)
	if err != nil {
		return "", err
	}
	if c.Engine != EngineVAD {
		return "", errors.New(c.Name + " bir VAD modeli değil")
	}
	if !c.installed() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := download(ctx, c); err != nil {
			return "", err
		}
	}
	return c.path(), nil
}

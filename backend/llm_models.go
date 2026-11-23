package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Local language models live in LLMDir(), grouped by where they came from,
// one folder per model:
//
//	models/llm/lmstudio/<publisher>/<repo>/…     imported from LM Studio
//	models/llm/huggingface/<user>/<repo>/…       downloaded from Hugging Face
//	models/llm/diger/<folder>/…                  added from a file
//
// e.g. models/llm/lmstudio/lmstudio-community/gemma-4-E4B-it-GGUF/
//
//	gemma-4-E4B-it-Q4_K_M.gguf
//	mmproj-gemma-4-E4B-it-BF16.gguf   (vision)
//
// The model setting stores the path relative to LLMDir(), so the app folder
// can be moved. Models are downloaded from Hugging Face, or imported (copied)
// from other apps such as LM Studio.

func init() {
	application.RegisterEvent[LLMProgress]("llm:progress")
}

// LLMProgress is emitted as "llm:progress" while a model downloads or is imported.
type LLMProgress struct {
	ID    string `json:"id"`    // repo ("user/name") or the imported file's path
	State string `json:"state"` // downloading | copying | done | error | canceled
	File  string `json:"file"`  // file being transferred
	Done  int64  `json:"done"`  // bytes so far (all files)
	Total int64  `json:"total"` // bytes in all (0 = unknown)
	Speed int64  `json:"speed"` // bytes per second
	Error string `json:"error"`
	Spec  string `json:"spec"`  // when done: the model's setting value
	Item  int    `json:"item"`  // batch import: which model (1-based)…
	Items int    `json:"items"` // …of how many
}

// CatalogModel is a recommended model that can be downloaded.
type CatalogModel struct {
	Repo        string `json:"repo"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        int64  `json:"size"` // approximate download size
	Vision      bool   `json:"vision"`
	Installed   string `json:"installed"` // spec if already downloaded
}

var llmCatalog = []CatalogModel{
	{Repo: "lmstudio-community/gemma-4-E4B-it-GGUF", Name: "Gemma 4 E4B", Vision: true, Size: 6_330_000_000,
		Description: "Google’ın küçük ama güçlü modeli. Türkçesi iyi, resimleri görebilir. 8 GB ekran kartına uygun."},
	{Repo: "ggml-org/gemma-3-4b-it-GGUF", Name: "Gemma 3 4B", Vision: true, Size: 3_340_000_000,
		Description: "Hafif ve hızlı; resimleri görebilir. Düşük bellekli bilgisayarlar için."},
	{Repo: "ggml-org/gemma-3-12b-it-GGUF", Name: "Gemma 3 12B", Vision: true, Size: 8_150_000_000,
		Description: "Daha yetenekli ama daha ağır; 12 GB+ ekran kartı önerilir. Resimleri görebilir."},
	{Repo: "unsloth/Qwen3-4B-Instruct-2507-GGUF", Name: "Qwen3 4B Instruct", Size: 2_500_000_000,
		Description: "Hızlı, sadece metin. Kısa sorular ve özetler için."},
	{Repo: "unsloth/Qwen3-8B-GGUF", Name: "Qwen3 8B", Size: 5_030_000_000,
		Description: "Sadece metin; akıl yürütme gerektiren sorularda güçlü."},
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Where a model came from: the first folder level inside models/llm.
const (
	originLMStudio    = "lmstudio"
	originHuggingFace = "huggingface"
	originOther       = "diger"
)

var originLabels = map[string]string{originLMStudio: "LM Studio", originHuggingFace: "Hugging Face", originOther: "Diğer"}

// modelFolder is the folder a downloaded repo's files go into.
func modelFolder(repo string) string {
	return filepath.Join(LLMDir(), originHuggingFace, filepath.FromSlash(repo))
}

// repoFolders are the places a repo's model can be: downloaded here, or
// imported from LM Studio (which downloads from the same Hugging Face repos).
func repoFolders(repo string) []string {
	return []string{modelFolder(repo), filepath.Join(LLMDir(), originLMStudio, filepath.FromSlash(repo))}
}

// findModelByName looks for a model file anywhere in models/llm.
func findModelByName(name string) string {
	var found string
	_ = filepath.WalkDir(LLMDir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && found == "" && !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	return found
}

func isMMProj(name string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Base(name)), "mmproj")
}

// resolveModel turns a model setting into a file path inside LLMDir (or an
// absolute path, for settings saved before models lived in the app folder).
func resolveModel(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", errors.New("yerel model seçilmemiş. Ayarlar → Yapay zekâ bölümünden bir model indirin veya seçin")
	}
	if strings.HasSuffix(strings.ToLower(spec), ".gguf") {
		p := spec
		if !filepath.IsAbs(p) {
			p = filepath.Join(LLMDir(), filepath.Clean("/"+spec)) // Clean("/"+…) keeps it inside LLMDir
		}
		if _, err := os.Stat(p); err != nil {
			if moved := findModelByName(filepath.Base(p)); moved != "" { // folders were reorganized
				return moved, nil
			}
			return "", fmt.Errorf("model dosyası bulunamadı: %s", spec)
		}
		return p, nil
	}
	if repoPattern.MatchString(spec) { // a repo: use it if it has been downloaded
		for _, dir := range repoFolders(spec) {
			if p := mainModelIn(dir); p != "" {
				return p, nil
			}
		}
		return "", fmt.Errorf("%s henüz indirilmemiş. Ayarlar → Yapay zekâ bölümünden indirin", spec)
	}
	return "", fmt.Errorf("geçersiz model: %s", spec)
}

// mainModelIn returns the first non-projector GGUF in a folder.
func mainModelIn(dir string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(n), ".gguf") && !isMMProj(n) {
			return filepath.Join(dir, n)
		}
	}
	return ""
}

// specFor is the setting value for a model file: relative to LLMDir when inside it.
func specFor(path string) string {
	if rel, err := filepath.Rel(LLMDir(), path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// LocalModels lists the models in the app's models/llm folder.
func (s *LlamaService) LocalModels() []LocalModel {
	var out []LocalModel
	_ = filepath.WalkDir(LLMDir(), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") || isMMProj(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size := info.Size()
		mm := findMMProj(p)
		if mm != "" {
			if st, err := os.Stat(mm); err == nil {
				size += st.Size()
			}
		}
		spec := specFor(p)
		origin, folder, _ := strings.Cut(filepath.ToSlash(filepath.Dir(spec)), "/")
		if originLabels[origin] == "" { // not in an origin folder
			origin, folder = originOther, filepath.ToSlash(filepath.Dir(spec))
		}
		out = append(out, LocalModel{Spec: spec, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
			Origin: origin, Source: originLabels[origin] + " · " + folder, Size: size, Vision: mm != "", Caps: modelCaps(p, mm)})
		return nil
	})
	order := map[string]int{originLMStudio: 0, originHuggingFace: 1, originOther: 2}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return order[out[i].Origin] < order[out[j].Origin]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ExternalModels lists GGUF models other apps downloaded (LM Studio, the
// Hugging Face cache) that can be imported into the app folder.
func (s *LlamaService) ExternalModels() []LocalModel {
	home, _ := os.UserHomeDir()
	have := map[string]bool{}
	for _, m := range s.LocalModels() {
		have[filepath.Base(m.Spec)] = true
	}
	var out []LocalModel
	scan := func(root, source string) {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") || isMMProj(d.Name()) || have[d.Name()] {
				return nil
			}
			st, err := os.Stat(p) // follows Hugging Face's blob symlinks
			if err != nil {
				return nil
			}
			out = append(out, LocalModel{Spec: p, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
				Source: source, Size: st.Size(), Vision: findMMProj(p) != "", Caps: modelCaps(p, findMMProj(p))})
			return nil
		})
	}
	for _, root := range lmStudioModelDirs() {
		scan(root, "LM Studio")
	}
	scan(filepath.Join(home, ".cache", "huggingface", "hub"), "Hugging Face önbelleği")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Catalog lists recommended models and whether they are downloaded.
func (s *LlamaService) Catalog() []CatalogModel {
	out := make([]CatalogModel, len(llmCatalog))
	copy(out, llmCatalog)
	for i := range out {
		for _, dir := range repoFolders(out[i].Repo) {
			if p := mainModelIn(dir); p != "" {
				out[i].Installed = specFor(p)
				break
			}
		}
	}
	return out
}

// OpenFolder shows models/llm in the file manager.
func (s *LlamaService) OpenFolder() error {
	if err := os.MkdirAll(LLMDir(), 0o755); err != nil {
		return err
	}
	return hostCommand("xdg-open", LLMDir()).Start()
}

// DeleteModel removes a model (and its vision projector) from models/llm.
func (s *LlamaService) DeleteModel(spec string) error {
	p, err := resolveModel(spec)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(LLMDir(), p); err != nil || strings.HasPrefix(rel, "..") {
		return errors.New("yalnızca uygulama klasöründeki modeller silinebilir")
	}
	s.mu.Lock()
	serving := s.cmd != nil && s.key.spec == strings.TrimSpace(spec)
	s.mu.Unlock()
	if serving {
		s.Stop()
	}
	dir := filepath.Dir(p)
	if err := os.Remove(p); err != nil {
		return err
	}
	if mainModelIn(dir) == "" { // no other model uses the projector
		if mm := findMMProj(p); mm != "" {
			_ = os.Remove(mm)
		}
		// Remove the now-empty model and publisher folders, keeping the origin folders.
		for d := dir; filepath.Dir(filepath.Dir(d)) != filepath.Dir(LLMDir()) && strings.HasPrefix(d, LLMDir()); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	return nil
}

// ---- transfers (download / import) ------------------------------------------

type transfers struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

var llmTransfers = &transfers{running: map[string]context.CancelFunc{}}

func (t *transfers) begin(id string) (context.Context, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, busy := t.running[id]; busy {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.running[id] = cancel
	return ctx, true
}

func (t *transfers) end(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.running[id]; ok {
		c()
		delete(t.running, id)
	}
}

func emitProgress(p LLMProgress) {
	if app := application.Get(); app != nil {
		app.Event.Emit("llm:progress", p)
	}
}

// CancelTransfer stops a download or import.
func (s *LlamaService) CancelTransfer(id string) { llmTransfers.end(id) }

type hfFile struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

// pickFiles chooses which GGUF files of a repo to download: one ~4-bit
// quantization, plus a vision projector if there is one.
func pickFiles(files []hfFile) (model hfFile, mmproj *hfFile, err error) {
	var models, projs []hfFile
	for _, f := range files {
		if f.Type != "file" || !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") || strings.Contains(f.Path, "/") {
			continue
		}
		if f.LFS != nil && f.LFS.Size > 0 {
			f.Size = f.LFS.Size
		}
		if isMMProj(f.Path) {
			projs = append(projs, f)
		} else if !regexp.MustCompile(`(?i)-0000[2-9]-of-`).MatchString(f.Path) { // skip split parts
			models = append(models, f)
		}
	}
	if len(models) == 0 {
		return hfFile{}, nil, errors.New("bu depoda GGUF model dosyası yok")
	}
	pick := func(list []hfFile, prefs ...string) *hfFile {
		for _, p := range prefs {
			for i := range list {
				if strings.Contains(strings.ToUpper(list[i].Path), p) {
					return &list[i]
				}
			}
		}
		return nil
	}
	m := pick(models, "Q4_K_M", "Q4_K_S", "Q4_0", "Q5_K_M", "Q6_K", "Q8_0")
	if m == nil { // fall back to the smallest file
		sort.Slice(models, func(i, j int) bool { return models[i].Size < models[j].Size })
		m = &models[0]
	}
	if len(projs) > 0 {
		mmproj = pick(projs, "F16", "BF16", "Q8_0")
		if mmproj == nil {
			mmproj = &projs[0]
		}
	}
	return *m, mmproj, nil
}

// Download fetches a model from Hugging Face ("user/repo") into models/llm in
// the background, reporting "llm:progress". Interrupted downloads resume.
func (s *LlamaService) Download(repo string) error {
	repo = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(repo, "https://huggingface.co/"), "hf.co/"))
	repo, _, _ = strings.Cut(repo, ":") // "user/repo:Q4_K_M" — the quant is picked automatically
	if !repoPattern.MatchString(repo) {
		return errors.New("geçersiz Hugging Face deposu. Örnek: ggml-org/gemma-3-4b-it-GGUF")
	}
	ctx, ok := llmTransfers.begin(repo)
	if !ok {
		return errors.New("bu model zaten indiriliyor")
	}
	go func() {
		defer llmTransfers.end(repo)
		spec, err := downloadRepo(ctx, repo)
		p := LLMProgress{ID: repo, State: "done", Spec: spec}
		switch {
		case ctx.Err() != nil:
			p.State = "canceled"
		case err != nil:
			p.State, p.Error = "error", err.Error()
		}
		emitProgress(p)
	}()
	return nil
}

var hfClient = &http.Client{Timeout: 0} // downloads take long; requests use contexts

func downloadRepo(ctx context.Context, repo string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://huggingface.co/api/models/"+repo+"/tree/main", nil)
	if tok := os.Getenv("HF_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := hfClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Hugging Face’e bağlanılamadı: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("%s bulunamadı (depo adı doğru mu? gizli depolar için HF_TOKEN gerekir)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Hugging Face hatası (HTTP %d)", resp.StatusCode)
	}
	var files []hfFile
	if err := json.Unmarshal(body, &files); err != nil {
		return "", errors.New("Hugging Face yanıtı okunamadı")
	}
	model, mmproj, err := pickFiles(files)
	if err != nil {
		return "", err
	}
	list := []hfFile{model}
	if mmproj != nil {
		list = append(list, *mmproj)
	}
	var total int64
	for _, f := range list {
		total += f.Size
	}
	dir := modelFolder(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tr := &tracker{id: repo, total: total, start: time.Now()}
	for _, f := range list {
		if err := downloadFile(ctx, repo, f, filepath.Join(dir, f.Path), tr); err != nil {
			return "", err
		}
	}
	return specFor(filepath.Join(dir, model.Path)), nil
}

// tracker reports combined progress over several files.
type tracker struct {
	id          string
	file        string
	done, total int64
	start       time.Time
	last        time.Time
	resumed     int64 // bytes that were already on disk (not counted for speed)
}

func (t *tracker) add(n int64, force bool) {
	t.done += n
	if !force && time.Since(t.last) < 250*time.Millisecond {
		return
	}
	t.last = time.Now()
	var speed int64
	if secs := time.Since(t.start).Seconds(); secs > 0.5 {
		speed = int64(float64(t.done-t.resumed) / secs)
	}
	emitProgress(LLMProgress{ID: t.id, State: "downloading", File: t.file, Done: t.done, Total: t.total, Speed: speed})
}

func downloadFile(ctx context.Context, repo string, f hfFile, dst string, tr *tracker) error {
	tr.file = f.Path
	if st, err := os.Stat(dst); err == nil && (f.Size == 0 || st.Size() == f.Size) {
		tr.resumed += st.Size()
		tr.add(st.Size(), true)
		return nil // already downloaded
	}
	part := dst + ".part"
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}
	u := "https://huggingface.co/" + repo + "/resolve/main/" + url.PathEscape(f.Path)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if tok := os.Getenv("HF_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := hfClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s indirilemedi: %w", f.Path, err)
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable: // the part file is complete
		return os.Rename(part, dst)
	default:
		return fmt.Errorf("%s indirilemedi (HTTP %d)", f.Path, resp.StatusCode)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	tr.resumed += have
	tr.add(have, true)
	buf := make([]byte, 1<<20)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			tr.add(int64(n), false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s indirilirken bağlantı koptu (tekrar indirince kaldığı yerden devam eder): %w", f.Path, rerr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(part); err == nil && f.Size > 0 && st.Size() != f.Size {
		return fmt.Errorf("%s eksik indi (%d / %d bayt); tekrar deneyin", f.Path, st.Size(), f.Size)
	}
	tr.add(0, true)
	return os.Rename(part, dst)
}

// ImportModel copies a GGUF model (and its vision projector) from another
// app into models/llm. On btrfs/XFS the copy is instant and takes no extra
// space (reflink).
func (s *LlamaService) ImportModel(path string) error {
	path = strings.TrimSpace(path)
	if !strings.HasSuffix(strings.ToLower(path), ".gguf") || !filepath.IsAbs(path) {
		return errors.New("bir .gguf dosyası seçin")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("dosya bulunamadı: %s", path)
	}
	if rel, err := filepath.Rel(LLMDir(), path); err == nil && !strings.HasPrefix(rel, "..") {
		return errors.New("bu model zaten uygulama klasöründe")
	}
	ctx, ok := llmTransfers.begin(path)
	if !ok {
		return errors.New("bu model zaten içe aktarılıyor")
	}
	go func() {
		defer llmTransfers.end(path)
		spec, err := importModel(ctx, path)
		p := LLMProgress{ID: path, State: "done", Spec: spec}
		switch {
		case ctx.Err() != nil:
			p.State = "canceled"
		case err != nil:
			p.State, p.Error = "error", err.Error()
		}
		emitProgress(p)
	}()
	return nil
}

// ImportFile asks for a .gguf file and imports it.
func (s *LlamaService) ImportFile() (string, error) {
	path, err := s.PickModelFile()
	if err != nil || path == "" {
		return "", err
	}
	return path, s.ImportModel(path)
}

func importModel(ctx context.Context, src string) (string, error) {
	return importModelWith(ctx, src, func() {
		emitProgress(LLMProgress{ID: src, State: "copying", File: filepath.Base(src)})
	})
}

func importModelWith(ctx context.Context, src string, started func()) (string, error) {
	dir := importDest(src)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	files := []string{src}
	if mm := findMMProj(src); mm != "" {
		files = append(files, mm)
	}
	started()
	for _, f := range files {
		real, err := filepath.EvalSymlinks(f)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, filepath.Base(f))
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		cmd := hostCommand("cp", "--reflink=auto", real, dst+".part")
		if err := cmd.Start(); err != nil {
			return "", err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			_ = os.Remove(dst + ".part")
			return "", ctx.Err()
		case err := <-done:
			if err != nil {
				_ = os.Remove(dst + ".part")
				return "", fmt.Errorf("kopyalanamadı: %w", err)
			}
		}
		if err := os.Rename(dst+".part", dst); err != nil {
			return "", err
		}
	}
	return specFor(filepath.Join(dir, filepath.Base(src))), nil
}

// ---- LM Studio ---------------------------------------------------------------

// LMStudioInfo describes an LM Studio installation and its models.
type LMStudioInfo struct {
	Found    bool         `json:"found"`    // LM Studio is installed (or its data is here)
	Folders  []string     `json:"folders"`  // where its models are
	Models   []LocalModel `json:"models"`   // models not yet imported into the app
	Imported []string     `json:"imported"` // names of models already imported
	Size     int64        `json:"size"`     // total size of Models
}

func lmStudioHomes() []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, d := range []string{
		filepath.Join(home, ".lmstudio"),                                         // AppImage / native
		filepath.Join(home, ".var", "app", "ai.lmstudio.lm-studio", ".lmstudio"), // Flatpak
		filepath.Join(home, ".cache", "lm-studio"),                               // older versions
	} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// lmStudioModelDirs finds LM Studio's model folders: the default ones and
// the downloads folder set in its settings (mapped into the Flatpak sandbox
// when that's where the files really are).
func lmStudioModelDirs() []string {
	home, _ := os.UserHomeDir()
	var cands []string
	for _, h := range lmStudioHomes() {
		cands = append(cands, filepath.Join(h, "models"))
		var st struct {
			DownloadsFolder string `json:"downloadsFolder"`
		}
		if b, err := os.ReadFile(filepath.Join(h, "settings.json")); err == nil && json.Unmarshal(b, &st) == nil && st.DownloadsFolder != "" {
			cands = append(cands, st.DownloadsFolder)
			if sandbox, ok := strings.CutSuffix(h, string(os.PathSeparator)+".lmstudio"); ok && strings.HasPrefix(st.DownloadsFolder, home) {
				cands = append(cands, filepath.Join(sandbox, strings.TrimPrefix(st.DownloadsFolder, home)))
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		real, err := filepath.EvalSymlinks(c)
		if err != nil || seen[real] {
			continue
		}
		if st, err := os.Stat(real); err == nil && st.IsDir() {
			seen[real] = true
			out = append(out, real)
		}
	}
	return out
}

// LMStudio reports whether LM Studio is installed and which of its models
// can be imported.
func (s *LlamaService) LMStudio() LMStudioInfo {
	info := LMStudioInfo{Folders: lmStudioModelDirs(), Models: []LocalModel{}, Imported: []string{}}
	_, lookErr := exec.LookPath("lms")
	info.Found = len(lmStudioHomes()) > 0 || lookErr == nil
	have := map[string]bool{}
	for _, m := range s.LocalModels() {
		have[filepath.Base(m.Spec)] = true
	}
	for _, root := range info.Folders {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") || isMMProj(d.Name()) {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			if have[d.Name()] {
				info.Imported = append(info.Imported, name)
				return nil
			}
			st, err := os.Stat(p)
			if err != nil {
				return nil
			}
			size := st.Size()
			if mm := findMMProj(p); mm != "" {
				if ms, err := os.Stat(mm); err == nil {
					size += ms.Size()
				}
			}
			info.Models = append(info.Models, LocalModel{Spec: p, Name: name, Source: "LM Studio", Size: size, Vision: findMMProj(p) != "", Caps: modelCaps(p, findMMProj(p))})
			info.Size += size
			return nil
		})
	}
	sort.Slice(info.Models, func(i, j int) bool { return info.Models[i].Name < info.Models[j].Name })
	return info
}

// ImportLMStudio copies every LM Studio model that isn't in the app folder
// yet, one after another, reporting progress under the ID "lmstudio".
func (s *LlamaService) ImportLMStudio() error {
	models := s.LMStudio().Models
	if len(models) == 0 {
		return errors.New("LM Studio’da içe aktarılacak yeni model yok")
	}
	const id = "lmstudio"
	ctx, ok := llmTransfers.begin(id)
	if !ok {
		return errors.New("LM Studio modelleri zaten içe aktarılıyor")
	}
	go func() {
		defer llmTransfers.end(id)
		var last string
		var failed []string
		for i, m := range models {
			if ctx.Err() != nil {
				break
			}
			spec, err := importModelWith(ctx, m.Spec, func() {
				emitProgress(LLMProgress{ID: id, State: "copying", File: m.Name, Item: i + 1, Items: len(models)})
			})
			if err != nil {
				if ctx.Err() == nil {
					failed = append(failed, m.Name+": "+err.Error())
				}
				continue
			}
			last = spec
		}
		p := LLMProgress{ID: id, State: "done", Spec: last, Items: len(models) - len(failed)}
		switch {
		case ctx.Err() != nil:
			p.State = "canceled"
		case len(failed) > 0:
			p.State, p.Error = "error", "bazı modeller aktarılamadı — "+strings.Join(failed, "; ")
		}
		emitProgress(p)
	}()
	return nil
}

// importDest picks the folder an imported model goes into, by origin:
// LM Studio's <publisher>/<repo>, the Hugging Face cache's <user>/<repo>, or
// the file's own folder name for anything else.
func importDest(src string) string {
	srcDir := filepath.Dir(src)
	if real, err := filepath.EvalSymlinks(srcDir); err == nil {
		srcDir = real
	}
	for _, root := range lmStudioModelDirs() {
		if rel, err := filepath.Rel(root, srcDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.Join(LLMDir(), originLMStudio, rel)
		}
	}
	// Hugging Face cache: …/models--<user>--<repo>/snapshots/<rev>/<file>
	if _, rest, ok := strings.Cut(filepath.ToSlash(src), "/models--"); ok {
		name, _, _ := strings.Cut(rest, "/")
		if user, repo, ok := strings.Cut(name, "--"); ok && user != "" && repo != "" {
			return filepath.Join(LLMDir(), originHuggingFace, user, repo)
		}
	}
	name := filepath.Base(srcDir)
	if name == "" || name == "/" || name == "." {
		name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	return filepath.Join(LLMDir(), originOther, name)
}

// migrateLLMLayout moves model folders kept directly in models/llm (the
// layout before models were grouped by origin) into lmstudio/, huggingface/
// or diger/. Unfinished downloads (.part) move along and resume from there.
func migrateLLMLayout() {
	entries, err := os.ReadDir(LLMDir())
	if err != nil {
		return
	}
	lms := lmStudioModelDirs()
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || originLabels[name] != "" {
			continue
		}
		src := filepath.Join(LLMDir(), name)
		dst := ""
		for _, root := range lms { // imported from LM Studio: <root>/<publisher>/<name>
			if m, _ := filepath.Glob(filepath.Join(root, "*", name)); len(m) > 0 {
				dst = filepath.Join(LLMDir(), originLMStudio, filepath.Base(filepath.Dir(m[0])), name)
				break
			}
		}
		if dst == "" {
			for _, c := range llmCatalog { // downloaded from the catalog
				if _, repo, _ := strings.Cut(c.Repo, "/"); repo == name {
					dst = modelFolder(c.Repo)
					break
				}
			}
		}
		if dst == "" {
			dst = filepath.Join(LLMDir(), originOther, name)
		}
		if _, err := os.Stat(dst); err == nil {
			moveDirContents(src, dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
			if err := os.Rename(src, dst); err != nil {
				log.Printf("model taşınamadı %s → %s: %v", src, dst, err)
			}
		}
	}
}

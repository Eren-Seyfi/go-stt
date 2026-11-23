package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zendev-sh/goai/provider"
)

// Files attached to chat messages live in AttachmentsDir(), named by a
// random ID plus the original extension. Their metadata (name, type, size)
// is stored with the transcript row that uses them.

const maxAttachment = 100 << 20 // 100 MB

// Attachment describes one attached file.
type Attachment struct {
	ID   string `json:"id"`   // file name in AttachmentsDir, e.g. "3f9a….png"
	Name string `json:"name"` // original file name
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

var attachmentID = regexp.MustCompile(`^[0-9a-f]{32}(\.[a-z0-9]{1,10})?$`)

func attachmentPath(id string) (string, error) {
	if !attachmentID.MatchString(id) {
		return "", errors.New("geçersiz ek")
	}
	return filepath.Join(AttachmentsDir(), id), nil
}

// guessMime fills in a missing or generic media type from the file name.
func guessMime(name, given string, head []byte) string {
	given = strings.TrimSpace(strings.ToLower(given))
	if given != "" && given != "application/octet-stream" {
		return given
	}
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		t, _, _ = strings.Cut(t, ";")
		return t
	}
	if len(head) > 0 {
		t, _, _ := strings.Cut(http.DetectContentType(head), ";")
		return t
	}
	return "application/octet-stream"
}

// ---- service ------------------------------------------------------------

// AttachmentService stores files attached to chat messages.
type AttachmentService struct{ store *Store }

func NewAttachmentService(store *Store) *AttachmentService {
	a := &AttachmentService{store: store}
	go a.cleanup()
	return a
}

// Save stores a file sent from the UI as base64 and returns its metadata.
func (a *AttachmentService) Save(name, mimeType, dataBase64 string) (Attachment, error) {
	if base64.StdEncoding.DecodedLen(len(dataBase64)) > maxAttachment+3 {
		return Attachment{}, fmt.Errorf("%s çok büyük (en fazla %d MB)", name, maxAttachment>>20)
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return Attachment{}, errors.New("dosya okunamadı")
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		name = "dosya"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !regexp.MustCompile(`^\.[a-z0-9]{1,10}$`).MatchString(ext) {
		ext = ""
	}
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return Attachment{}, err
	}
	id := hex.EncodeToString(rnd[:]) + ext
	if err := os.MkdirAll(AttachmentsDir(), 0o700); err != nil {
		return Attachment{}, err
	}
	p, _ := attachmentPath(id)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return Attachment{}, err
	}
	head := data[:min(len(data), 512)]
	return Attachment{ID: id, Name: name, Mime: guessMime(name, mimeType, head), Size: int64(len(data))}, nil
}

// Delete removes an attachment that was never sent (removed from the chat box).
func (a *AttachmentService) Delete(id string) error {
	p, err := attachmentPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Open opens the file in the system's default application.
func (a *AttachmentService) Open(id string) error {
	p, err := attachmentPath(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return errors.New("dosya bulunamadı")
	}
	return hostCommand("xdg-open", p).Start()
}

// cleanup deletes files no transcript row refers to. Recent files are kept:
// they may be waiting in the chat box, or in the "undo clear" buffer.
func (a *AttachmentService) cleanup() {
	entries, err := os.ReadDir(AttachmentsDir())
	if err != nil {
		return
	}
	used, err := a.store.attachmentIDs()
	if err != nil {
		log.Printf("ekler: temizlik atlandı: %v", err)
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || used[e.Name()] || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		_ = os.Remove(filepath.Join(AttachmentsDir(), e.Name()))
	}
}

// ---- serving for previews ------------------------------------------------

// AttachmentMiddleware serves /attachments/<id> to the UI for previews. Files
// are sandboxed (no scripts, own origin), except PDFs, which the built-in
// viewer must be able to render; HTML is shown as plain text.
func AttachmentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutPrefix(r.URL.Path, "/attachments/")
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		p, err := attachmentPath(path.Clean(id))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ct := guessMime(id, "", nil)
		switch {
		case ct == "application/pdf":
		case strings.HasPrefix(ct, "text/html"), strings.Contains(ct, "xhtml"), strings.Contains(ct, "javascript"):
			ct = "text/plain; charset=utf-8"
			fallthrough
		default:
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
		}
		if strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "charset") {
			ct += "; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		http.ServeContent(w, r, "", st.ModTime(), f) // handles Range requests (video/audio seeking)
	})
}

// ---- sending to the AI ----------------------------------------------------

// What each provider can read besides text and images.
var providerFileKinds = map[string]map[string]bool{
	"anthropic": {"pdf": true},
	"google":    {"pdf": true, "audio": true, "video": true},
	"openai":    {"pdf": true},
}

func fileKind(mimeType, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case mimeType == "image/png", mimeType == "image/jpeg", mimeType == "image/webp", mimeType == "image/gif":
		return "image"
	case mimeType == "application/pdf":
		return "pdf"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case ext == ".docx":
		return "docx"
	case strings.HasPrefix(mimeType, "text/"), isTextExt(ext),
		strings.Contains(mimeType, "json"), strings.Contains(mimeType, "xml"), strings.Contains(mimeType, "yaml"),
		strings.Contains(mimeType, "javascript"), strings.Contains(mimeType, "x-sh"):
		return "text"
	}
	return "other"
}

func isTextExt(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".jsonl", ".xml", ".yaml", ".yml", ".toml", ".ini", ".cfg",
		".conf", ".log", ".go", ".py", ".js", ".ts", ".jsx", ".tsx", ".svelte", ".vue", ".html", ".css", ".scss", ".java",
		".kt", ".c", ".h", ".cpp", ".hpp", ".cc", ".rs", ".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".sql", ".lua",
		".dart", ".swift", ".cs", ".r", ".tex", ".srt", ".vtt", ".env", ".gitignore", ".dockerfile":
		return true
	}
	return false
}

const maxInlineText = 200 << 10 // characters of a text file sent to the AI

// docxText pulls the plain text out of a .docx file.
func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		var sb strings.Builder
		dec := xml.NewDecoder(io.LimitReader(rc, 50<<20))
		inText := false
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return sb.String(), nil
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "t":
					inText = true
				case "tab":
					sb.WriteByte('\t')
				case "br":
					sb.WriteByte('\n')
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "t":
					inText = false
				case "p":
					sb.WriteByte('\n')
				}
			case xml.CharData:
				if inText {
					sb.Write(t)
				}
			}
			if sb.Len() > maxInlineText {
				break
			}
		}
		return sb.String(), nil
	}
	return "", errors.New("belge metni bulunamadı")
}

func truncateText(s string) string {
	if len(s) <= maxInlineText {
		return s
	}
	s = s[:maxInlineText]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "\n…(kısaltıldı)"
}

// attachmentParts turns attachments into message parts the provider can
// read. Files it can't read are named in a note instead, so the model at
// least knows they exist.
// fileKinds says what a model can read besides text: "image", "pdf",
// "audio", "video". For local models it comes from the model's own
// capabilities; an explicit "image": false means it can't see images.
type fileKinds map[string]bool

func kindsFor(providerID string) fileKinds {
	k := fileKinds{}
	for kind, ok := range providerFileKinds[providerID] {
		k[kind] = ok
	}
	return k
}

const maxAudio = 10 * time.Minute // longer recordings are cut for the model

// audioAsWAV converts any audio (or a video's sound) to 16 kHz mono WAV with
// ffmpeg: the format the local server can decode, and what speech models
// such as Gemma's audio encoder expect.
func audioAsWAV(path string) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("ses dönüştürmek için ffmpeg gerekli")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-i", path,
		"-vn", "-ac", "1", "-ar", "16000", "-t", strconv.Itoa(int(maxAudio.Seconds())), "-f", "wav", "pipe:1")
	cmd.Env = HostEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return nil, fmt.Errorf("ses dönüştürülemedi: %s", trimTo(msg, 120))
	}
	if len(out) <= 44 { // just a WAV header
		return nil, errors.New("dosyada ses yok")
	}
	return out, nil
}

func attachmentParts(providerID string, kinds fileKinds, atts []Attachment) []provider.Part {
	var parts []provider.Part
	var notes []string
	for _, at := range atts {
		p, err := attachmentPath(at.ID)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s (dosya bulunamadı)", at.Name))
			continue
		}
		kind := fileKind(at.Mime, at.Name)
		dataURL := func() string { return "data:" + at.Mime + ";base64," + base64.StdEncoding.EncodeToString(data) }
		if v, set := kinds["image"]; kind == "image" && set && !v {
			notes = append(notes, at.Name+" (resim — seçili model resimleri göremiyor)")
			continue
		}
		switch {
		case kind == "image":
			parts = append(parts, provider.Part{Type: provider.PartImage, URL: dataURL(), MediaType: at.Mime})
		case (kind == "audio" || kind == "video") && providerID == "llamacpp" && kinds["audio"]:
			// Local models that hear but can't watch get a video's soundtrack.
			wav, err := audioAsWAV(p)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s (%v)", at.Name, err))
				continue
			}
			label := "Ses dosyası"
			if kind == "video" {
				label = "Videonun sesi (görüntü iletilmedi)"
			}
			parts = append(parts,
				provider.Part{Type: provider.PartText, Text: fmt.Sprintf("[%s: %s]", label, at.Name)},
				provider.Part{Type: provider.PartFile, URL: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav),
					MediaType: "audio/wav", Filename: at.Name})
		case kind == "text":
			txt := string(data)
			if !utf8.ValidString(txt) {
				txt = strings.ToValidUTF8(txt, "�")
			}
			parts = append(parts, provider.Part{Type: provider.PartText,
				Text: fmt.Sprintf("<belge ad=%q>\n%s\n</belge>", at.Name, truncateText(txt))})
		case kind == "docx":
			txt, err := docxText(data)
			if err != nil || strings.TrimSpace(txt) == "" {
				notes = append(notes, at.Name+" (belge okunamadı)")
				continue
			}
			parts = append(parts, provider.Part{Type: provider.PartText,
				Text: fmt.Sprintf("<belge ad=%q>\n%s\n</belge>", at.Name, truncateText(txt))})
		case kinds[kind]:
			parts = append(parts, provider.Part{Type: provider.PartFile, URL: dataURL(), MediaType: at.Mime, Filename: at.Name})
		default:
			label := map[string]string{"pdf": "PDF", "audio": "ses", "video": "video"}[kind]
			if label == "" {
				label = at.Mime
			}
			notes = append(notes, fmt.Sprintf("%s (%s — seçili yapay zekâ bu dosya türünü okuyamıyor)", at.Name, label))
		}
	}
	if len(notes) > 0 {
		parts = append(parts, provider.Part{Type: provider.PartText,
			Text: "[Kullanıcı şu dosyaları da ekledi ama içerikleri iletilemedi: " + strings.Join(notes, "; ") + "]"})
	}
	return parts
}

// ---- store ----------------------------------------------------------------

// attachmentIDs returns every attachment ID used by a transcript row.
func (s *Store) attachmentIDs() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT attachments FROM transcript WHERE attachments IS NOT NULL AND attachments != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var list []Attachment
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, a := range list {
				used[a.ID] = true
			}
		}
	}
	return used, rows.Err()
}

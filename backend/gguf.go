package backend

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// readGGUFMeta reads the metadata (key/value) section of a GGUF file. Large
// arrays (the tokenizer's vocabulary) are skipped rather than kept; only
// their length is recorded, as an int.
func readGGUFMeta(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &ggufReader{r: bufio.NewReaderSize(f, 1<<20)}

	var magic [4]byte
	if _, err := io.ReadFull(r.r, magic[:]); err != nil {
		return nil, err
	}
	if string(magic[:]) != "GGUF" {
		return nil, errors.New("GGUF dosyası değil")
	}
	version := r.u32()
	if version < 2 {
		return nil, fmt.Errorf("desteklenmeyen GGUF sürümü %d", version)
	}
	_ = r.u64() // tensor count
	kvs := r.u64()
	if r.err != nil {
		return nil, r.err
	}
	if kvs > 1<<20 {
		return nil, errors.New("bozuk GGUF başlığı")
	}
	meta := make(map[string]any, kvs)
	for i := uint64(0); i < kvs && r.err == nil; i++ {
		key := r.str()
		meta[key] = r.value(r.u32(), 0)
	}
	return meta, r.err
}

type ggufReader struct {
	r   *bufio.Reader
	err error
	buf [8]byte
}

func (g *ggufReader) read(n int) []byte {
	if g.err != nil {
		return g.buf[:n]
	}
	if _, err := io.ReadFull(g.r, g.buf[:n]); err != nil {
		g.err = err
	}
	return g.buf[:n]
}

func (g *ggufReader) u32() uint32 { return binary.LittleEndian.Uint32(g.read(4)) }
func (g *ggufReader) u64() uint64 { return binary.LittleEndian.Uint64(g.read(8)) }

func (g *ggufReader) str() string {
	n := g.u64()
	if g.err != nil {
		return ""
	}
	if n > 64<<20 {
		g.err = errors.New("bozuk GGUF dizgesi")
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(g.r, b); err != nil {
		g.err = err
	}
	return string(b)
}

func (g *ggufReader) skip(n uint64) {
	if g.err == nil {
		if _, err := g.r.Discard(int(n)); err != nil {
			g.err = err
		}
	}
}

// value reads one value of the given GGUF type. Arrays longer than a few
// items are skipped (their length is returned).
func (g *ggufReader) value(t uint32, depth int) any {
	switch t {
	case 0:
		return uint64(g.read(1)[0])
	case 1:
		return int64(int8(g.read(1)[0]))
	case 2:
		return uint64(binary.LittleEndian.Uint16(g.read(2)))
	case 3:
		return int64(int16(binary.LittleEndian.Uint16(g.read(2))))
	case 4:
		return uint64(g.u32())
	case 5:
		return int64(int32(g.u32()))
	case 6:
		return float64(math.Float32frombits(g.u32()))
	case 7:
		return g.read(1)[0] != 0
	case 8:
		return g.str()
	case 9:
		et, n := g.u32(), g.u64()
		if depth > 2 || n > 1<<26 {
			g.err = errors.New("bozuk GGUF dizisi")
			return nil
		}
		if n > 16 { // vocabularies etc.: skip
			sizes := map[uint32]uint64{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}
			if sz, ok := sizes[et]; ok {
				g.skip(sz * n)
			} else {
				for i := uint64(0); i < n && g.err == nil; i++ {
					g.value(et, depth+1)
				}
			}
			return int(n)
		}
		out := make([]any, 0, n)
		for i := uint64(0); i < n && g.err == nil; i++ {
			out = append(out, g.value(et, depth+1))
		}
		return out
	case 10:
		return g.u64()
	case 11:
		return int64(g.u64())
	case 12:
		return math.Float64frombits(g.u64())
	}
	g.err = fmt.Errorf("bilinmeyen GGUF türü %d", t)
	return nil
}

// ---- model capabilities ------------------------------------------------------

// ModelCaps describes what a local model can do, read from its GGUF metadata.
type ModelCaps struct {
	Vision    bool   `json:"vision"`    // can see images (has a vision projector)
	Audio     bool   `json:"audio"`     // can hear audio (projector with an audio encoder)
	Tools     bool   `json:"tools"`     // chat template supports tool / function calls
	Reasoning string `json:"reasoning"` // "" | "toggle" (can think, switchable) | "always"
	Context   int    `json:"context"`   // longest context it was trained for (tokens)
	Params    string `json:"params"`    // size, e.g. "7.5B"
	Arch      string `json:"arch"`      // architecture, e.g. "gemma4"
	Quant     string `json:"quant"`     // quantization, e.g. "Q4_K_M"
	Title     string `json:"title"`     // the model's own name, e.g. "Gemma 4 E4B"
}

type capsKey struct {
	path  string
	size  int64
	mtime int64
}

var (
	capsMu    sync.Mutex
	capsCache = map[capsKey]*ModelCaps{}
)

var quantPattern = regexp.MustCompile(`(?i)(IQ\d_[A-Z0-9_]+|Q\d_K_[SML]|Q\d_K|Q\d_\d|Q\d|BF16|F16|F32|MXFP4)`)

// modelCaps reads (and caches) a model's capabilities. mmproj is its vision
// projector, "" if none.
func modelCaps(path, mmproj string) *ModelCaps {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	key := capsKey{path, st.Size(), st.ModTime().UnixNano()}
	if mmproj != "" {
		if ms, err := os.Stat(mmproj); err == nil {
			key.mtime ^= ms.ModTime().UnixNano() // re-read when the projector appears or changes
		}
	}
	capsMu.Lock()
	c, ok := capsCache[key]
	capsMu.Unlock()
	if ok {
		return c
	}
	meta, err := readGGUFMeta(path)
	if err != nil {
		return nil
	}
	c = &ModelCaps{}
	str := func(k string) string { s, _ := meta[k].(string); return s }
	c.Arch, c.Params, c.Title = str("general.architecture"), str("general.size_label"), str("general.name")
	switch v := meta[c.Arch+".context_length"].(type) {
	case uint64:
		c.Context = int(v)
	case int64:
		c.Context = int(v)
	}
	if m := quantPattern.FindString(filepath.Base(path)); m != "" {
		c.Quant = strings.ToUpper(m)
	}
	tpl := strings.ToLower(str("tokenizer.chat_template"))
	c.Tools = strings.Contains(tpl, "tools") && strings.Contains(tpl, "tool_call")
	switch {
	case strings.Contains(tpl, "enable_thinking"):
		c.Reasoning = "toggle"
	case strings.Contains(tpl, "<think>"), strings.Contains(tpl, "<|channel"), strings.Contains(tpl, "reasoning_effort"):
		// Templates of some non-thinking "Instruct" releases still strip <think> blocks.
		if !strings.Contains(strings.ToLower(str("general.finetune")), "instruct") {
			c.Reasoning = "always"
		}
	}
	if mmproj != "" {
		c.Vision = true
		if pm, err := readGGUFMeta(mmproj); err == nil {
			if v, ok := pm["clip.has_vision_encoder"].(bool); ok {
				c.Vision = v
			}
			c.Audio, _ = pm["clip.has_audio_encoder"].(bool)
		}
	}
	capsMu.Lock()
	capsCache[key] = c
	capsMu.Unlock()
	return c
}

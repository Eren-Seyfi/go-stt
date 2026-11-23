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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
	"github.com/zendev-sh/goai/provider/anthropic"
	"github.com/zendev-sh/goai/provider/compat"
	"github.com/zendev-sh/goai/provider/deepseek"
	"github.com/zendev-sh/goai/provider/google"
	"github.com/zendev-sh/goai/provider/groq"
	"github.com/zendev-sh/goai/provider/mistral"
	"github.com/zendev-sh/goai/provider/ollama"
	"github.com/zendev-sh/goai/provider/openai"
	"github.com/zendev-sh/goai/provider/openrouter"
	"github.com/zendev-sh/goai/provider/xai"
)

func init() {
	application.RegisterEvent[AIDelta]("ai:delta")
	application.RegisterEvent[AIDone]("ai:done")
	application.RegisterEvent[AIConfigView]("ai:config")
}

// ---- providers ----------------------------------------------------------

// ProviderInfo describes an AI provider the user can pick.
type ProviderInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	NeedsKey       bool     `json:"needsKey"`
	KeyEnv         string   `json:"keyEnv"`         // environment variable read when no key is saved
	KeyURL         string   `json:"keyURL"`         // where to get a key
	DefaultBaseURL string   `json:"defaultBaseURL"` // shown as placeholder; "" = not configurable
	NeedsBaseURL   bool     `json:"needsBaseURL"`   // must be set (OpenAI-compatible)
	Models         []string `json:"models"`         // suggested models (more can be fetched)
	SupportsEffort bool     `json:"supportsEffort"`
}

type providerDef struct {
	ProviderInfo
	// models endpoint: path appended to the base URL and how to read it
	modelsPath string
	listStyle  string // "openai" (data[].id) | "google" | "ollama"
	auth       string // "bearer" | "x-api-key" | "query" | "none"
	newModel   func(model, key, base string) provider.LanguageModel
}

var providers = []providerDef{
	{ProviderInfo{ID: "anthropic", Name: "Anthropic (Claude)", NeedsKey: true, KeyEnv: "ANTHROPIC_API_KEY",
		KeyURL: "https://platform.claude.com", DefaultBaseURL: "https://api.anthropic.com", SupportsEffort: true,
		Description: "Claude modelleri. Opus en yetenekli, Haiku en hızlı ve ucuz.",
		Models:      []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2"}},
		"/v1/models", "openai", "x-api-key",
		func(m, k, b string) provider.LanguageModel {
			var o []anthropic.Option
			if k != "" {
				o = append(o, anthropic.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, anthropic.WithBaseURL(b))
			}
			return anthropic.Chat(m, o...)
		}},
	{ProviderInfo{ID: "openai", Name: "OpenAI", NeedsKey: true, KeyEnv: "OPENAI_API_KEY",
		KeyURL: "https://platform.openai.com/api-keys", DefaultBaseURL: "https://api.openai.com/v1",
		Description: "GPT modelleri. Model listesini anahtarla getirin."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []openai.Option
			if k != "" {
				o = append(o, openai.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, openai.WithBaseURL(b))
			}
			return openai.Chat(m, o...)
		}},
	{ProviderInfo{ID: "google", Name: "Google Gemini", NeedsKey: true, KeyEnv: "GEMINI_API_KEY",
		KeyURL: "https://aistudio.google.com/apikey", DefaultBaseURL: "https://generativelanguage.googleapis.com",
		Description: "Gemini modelleri (Google AI Studio anahtarı)."},
		"/v1beta/models", "google", "query",
		func(m, k, b string) provider.LanguageModel {
			var o []google.Option
			if k != "" {
				o = append(o, google.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, google.WithBaseURL(b))
			}
			return google.Chat(m, o...)
		}},
	{ProviderInfo{ID: "ollama", Name: "Ollama (yerel)", NeedsKey: false, DefaultBaseURL: "http://localhost:11434",
		Description: "Bilgisayarınızda çalışan modeller. İnternet ve anahtar gerekmez; Ollama kurulu olmalı."},
		"/api/tags", "ollama", "none",
		func(m, _, b string) provider.LanguageModel {
			var o []ollama.Option
			if b != "" {
				o = append(o, ollama.WithBaseURL(b))
			}
			return ollama.Chat(m, o...)
		}},
	{ProviderInfo{ID: "openrouter", Name: "OpenRouter", NeedsKey: true, KeyEnv: "OPENROUTER_API_KEY",
		KeyURL: "https://openrouter.ai/keys", DefaultBaseURL: "https://openrouter.ai/api/v1",
		Description: "Tek anahtarla birçok sağlayıcının modelleri."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []openrouter.Option
			if k != "" {
				o = append(o, openrouter.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, openrouter.WithBaseURL(b))
			}
			return openrouter.Chat(m, o...)
		}},
	{ProviderInfo{ID: "groq", Name: "Groq", NeedsKey: true, KeyEnv: "GROQ_API_KEY",
		KeyURL: "https://console.groq.com/keys", DefaultBaseURL: "https://api.groq.com/openai/v1",
		Description: "Çok hızlı yanıt veren açık modeller."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []groq.Option
			if k != "" {
				o = append(o, groq.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, groq.WithBaseURL(b))
			}
			return groq.Chat(m, o...)
		}},
	{ProviderInfo{ID: "mistral", Name: "Mistral", NeedsKey: true, KeyEnv: "MISTRAL_API_KEY",
		KeyURL: "https://console.mistral.ai/api-keys", DefaultBaseURL: "https://api.mistral.ai/v1",
		Description: "Mistral AI modelleri."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []mistral.Option
			if k != "" {
				o = append(o, mistral.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, mistral.WithBaseURL(b))
			}
			return mistral.Chat(m, o...)
		}},
	{ProviderInfo{ID: "deepseek", Name: "DeepSeek", NeedsKey: true, KeyEnv: "DEEPSEEK_API_KEY",
		KeyURL: "https://platform.deepseek.com/api_keys", DefaultBaseURL: "https://api.deepseek.com",
		Description: "DeepSeek modelleri."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []deepseek.Option
			if k != "" {
				o = append(o, deepseek.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, deepseek.WithBaseURL(b))
			}
			return deepseek.Chat(m, o...)
		}},
	{ProviderInfo{ID: "xai", Name: "xAI (Grok)", NeedsKey: true, KeyEnv: "XAI_API_KEY",
		KeyURL: "https://console.x.ai", DefaultBaseURL: "https://api.x.ai/v1",
		Description: "Grok modelleri."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			var o []xai.Option
			if k != "" {
				o = append(o, xai.WithAPIKey(k))
			}
			if b != "" {
				o = append(o, xai.WithBaseURL(b))
			}
			return xai.Chat(m, o...)
		}},
	{ProviderInfo{ID: "llamacpp", Name: "Yerel model (llama.cpp)", NeedsKey: false,
		Description: "GGUF modellerini bu uygulama içinde, GPU ile çalıştırır. İnternet, anahtar veya Ollama gerekmez."},
		"", "", "none",
		func(_, k, b string) provider.LanguageModel {
			return compat.Chat("yerel", compat.WithBaseURL(b), compat.WithAPIKey(k))
		}},
	{ProviderInfo{ID: "compat", Name: "OpenAI uyumlu (özel)", NeedsKey: false, NeedsBaseURL: true,
		Description: "LM Studio, vLLM, llama.cpp gibi OpenAI uyumlu bir sunucu. Adresini girin."},
		"/models", "openai", "bearer",
		func(m, k, b string) provider.LanguageModel {
			o := []compat.Option{compat.WithBaseURL(b)}
			if k != "" {
				o = append(o, compat.WithAPIKey(k))
			}
			return compat.Chat(m, o...)
		}},
}

func findProvider(id string) (*providerDef, error) {
	for i := range providers {
		if providers[i].ID == id {
			return &providers[i], nil
		}
	}
	return nil, errors.New("bilinmeyen yapay zekâ sağlayıcısı: " + id)
}

// ---- config -------------------------------------------------------------

// AIConfig holds the AI settings (everything except API keys).
type AIConfig struct {
	Enabled      bool              `json:"enabled"`      // send transcribed text to the AI
	Auto         bool              `json:"auto"`         // automatically, after each finished phrase
	Provider     string            `json:"provider"`     // provider ID
	Models       map[string]string `json:"models"`       // chosen model per provider
	BaseURLs     map[string]string `json:"baseURLs"`     // custom base URL per provider
	SystemPrompt string            `json:"systemPrompt"` // instructions for the AI
	Effort       string            `json:"effort"`       // Claude: low | medium | high
	MaxTokens    int               `json:"maxTokens"`
	Memory       int               `json:"memory"` // previous exchanges sent as context (0 = none)
	Llama        LlamaConfig       `json:"llama"`  // local model server (provider "llamacpp")
	// LocalThinking lets local models that can switch it (Gemma 4, Qwen3)
	// think before answering: better answers, but much slower and it eats
	// the token budget. Off by default.
	LocalThinking bool `json:"localThinking"`
	// Tools the AI may call (clock, calculator, weather…). On unless NoTools.
	NoTools       bool     `json:"noTools"`
	DisabledTools []string `json:"disabledTools"` // tools switched off one by one
}

// KeyStatus tells the UI whether a provider has a key, without revealing it.
type KeyStatus struct {
	Saved   bool   `json:"saved"`   // a key is stored in the app
	Last4   string `json:"last4"`   // last 4 characters of the stored key
	FromEnv bool   `json:"fromEnv"` // no stored key, but the environment variable is set
}

// AIConfigView is what the UI sees: the config plus per-provider key status.
type AIConfigView struct {
	Config AIConfig             `json:"config"`
	Keys   map[string]KeyStatus `json:"keys"`
	Path   string               `json:"path"`
}

const defaultSystemPrompt = `Sen sesli bir asistansın. Kullanıcının söyledikleri konuşmadan yazıya çevrilip sana iletiliyor; bu yüzden metinde tanıma hataları, eksik noktalama veya yarım cümleler olabilir. Anlamı bağlamdan çıkar. Kısa, net ve kullanıcının konuştuğu dilde yanıt ver.`

func defaultAIConfig() AIConfig {
	return AIConfig{
		Enabled: false, Auto: true, Provider: "anthropic",
		Models:       map[string]string{"anthropic": "claude-opus-5-5"},
		BaseURLs:     map[string]string{},
		SystemPrompt: defaultSystemPrompt, Effort: "low", MaxTokens: 4096, Memory: 6,
		Llama: defaultLlamaConfig(),
	}
}

type aiFile struct {
	Config AIConfig          `json:"config"`
	Keys   map[string]string `json:"keys"` // stored API keys
}

// legacyAIConfigPath is where older versions kept the AI settings; it is
// imported into the database once.
func legacyAIConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "stt-app", "ai.json")
}

// ---- service ------------------------------------------------------------

// AIDelta is emitted as "ai:delta" while a reply streams in.
type AIDelta struct {
	ID   string `json:"id"`
	Text string `json:"text"` // newly arrived text
}

// AIDone is emitted as "ai:done" when a reply finishes; Error is empty on success.
type AIDone struct {
	ID       string      `json:"id"`
	Error    string      `json:"error"`
	Model    string      `json:"model"`
	Canceled bool        `json:"canceled"`
	Stats    *ReplyStats `json:"stats,omitempty"` // speed of a successful reply
}

// TestResult is the outcome of "Bağlantıyı dene".
type TestResult struct {
	Text  string     `json:"text"`
	Model string     `json:"model"`
	Stats ReplyStats `json:"stats"`
}

// ChatTurn is one earlier exchange sent as context.
type ChatTurn struct {
	Role string `json:"role"` // "user" | "assistant"
	Text string `json:"text"`
}

// AskRequest asks the AI about transcribed text.
type AskRequest struct {
	ID          string       `json:"id"` // caller's ID, echoed in events
	Text        string       `json:"text"`
	History     []ChatTurn   `json:"history"`
	Attachments []Attachment `json:"attachments"` // files sent with this message
}

// AIService sends transcribed text to the chosen AI provider and streams the
// replies back as events. Requests run one at a time, in order.
type AIService struct {
	store   *Store
	llama   *LlamaService
	mu      sync.Mutex
	file    aiFile
	queue   chan askItem
	cancels map[string]context.CancelFunc
}

type askItem struct {
	req AskRequest
	ctx context.Context
}

func NewAIService(store *Store, llama *LlamaService) *AIService {
	a := &AIService{store: store, llama: llama, queue: make(chan askItem, 64), cancels: map[string]context.CancelFunc{}}
	llama.onStats = a.emitStats
	a.load()
	go a.worker()
	return a
}

func normalizeAIConfig(c *AIConfig) {
	if c.Models == nil {
		c.Models = map[string]string{}
	}
	if c.BaseURLs == nil {
		c.BaseURLs = map[string]string{}
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = defaultAIConfig().MaxTokens
	}
	c.Memory = min(max(c.Memory, 0), 50)
	if c.Llama == (LlamaConfig{}) { // saved before local models existed
		c.Llama = defaultLlamaConfig()
	}
	if c.Llama.GPULayers = strings.TrimSpace(c.Llama.GPULayers); c.Llama.GPULayers == "" {
		c.Llama.GPULayers = "auto"
	}
	if c.Llama.GPULayers != "auto" && c.Llama.GPULayers != "all" {
		if _, err := strconv.Atoi(c.Llama.GPULayers); err != nil {
			c.Llama.GPULayers = "auto"
		}
	}
	c.Llama.Context = min(max(c.Llama.Context, 0), 262144)
	c.DisabledTools = sortedToolNames(c.DisabledTools)
}

// load reads the AI config and keys from the database. On first run it
// imports the older ai.json file (renamed to ai.json.migrated afterwards).
func (a *AIService) load() {
	a.file = aiFile{Config: defaultAIConfig(), Keys: map[string]string{}}
	cfg, ok, keys, err := a.store.LoadAI()
	if err != nil {
		log.Printf("ai: ayarlar okunamadı: %v", err)
		return
	}
	if !ok && len(keys) == 0 {
		if f, found := readLegacyAIFile(); found {
			cfg, ok, keys = f.Config, true, f.Keys
			err := a.store.SaveAIConfig(cfg)
			for p, k := range keys {
				if err == nil {
					err = a.store.SetAIKey(p, k)
				}
			}
			if err != nil {
				log.Printf("ai: ai.json taşınamadı: %v", err)
			} else {
				_ = os.Rename(legacyAIConfigPath(), legacyAIConfigPath()+".migrated")
			}
		}
	}
	if ok {
		if cfg.Models == nil {
			cfg.Models = defaultAIConfig().Models
		}
		normalizeAIConfig(&cfg)
		a.file.Config = cfg
	}
	a.file.Keys = keys
	a.preferAppModel()
}

// preferAppModel switches a local model setting that points outside the app
// folder (e.g. into LM Studio) to the same file in models/llm, if imported.
func (a *AIService) preferAppModel() {
	m := a.file.Config.Models["llamacpp"]
	if m == "" || !strings.HasSuffix(strings.ToLower(m), ".gguf") {
		return
	}
	path := m
	if !filepath.IsAbs(path) {
		path = filepath.Join(LLMDir(), filepath.FromSlash(m))
	}
	var spec string
	if _, err := os.Stat(path); err == nil {
		spec = specFor(path) // exists: store it relative to models/llm when inside
	} else if found := findModelByName(filepath.Base(path)); found != "" {
		spec = specFor(found) // moved (e.g. folders grouped by origin): follow it
	}
	if spec == "" || spec == m {
		return
	}
	a.file.Config.Models["llamacpp"] = spec
	if err := a.store.SaveAIConfig(a.file.Config); err != nil {
		log.Printf("ai: model yolu güncellenemedi: %v", err)
	}
}

func readLegacyAIFile() (aiFile, bool) {
	b, err := os.ReadFile(legacyAIConfigPath())
	if err != nil {
		return aiFile{}, false
	}
	var f aiFile
	if json.Unmarshal(b, &f) != nil {
		return aiFile{}, false
	}
	if f.Keys == nil {
		f.Keys = map[string]string{}
	}
	return f, true
}

func (a *AIService) viewLocked() AIConfigView {
	keys := map[string]KeyStatus{}
	for _, p := range providers {
		k := a.file.Keys[p.ID]
		st := KeyStatus{Saved: k != ""}
		if k != "" && len(k) >= 8 {
			st.Last4 = k[len(k)-4:]
		}
		if k == "" && p.KeyEnv != "" && os.Getenv(p.KeyEnv) != "" {
			st.FromEnv = true
		}
		keys[p.ID] = st
	}
	return AIConfigView{Config: a.file.Config, Keys: keys, Path: DBPath()}
}

func (a *AIService) changed() {
	a.mu.Lock()
	v := a.viewLocked()
	a.mu.Unlock()
	application.Get().Event.Emit("ai:config", v)
}

// Providers lists the supported AI providers.
func (a *AIService) Providers() []ProviderInfo {
	out := make([]ProviderInfo, len(providers))
	for i, p := range providers {
		out[i] = p.ProviderInfo
	}
	return out
}

// Config returns the AI settings and which providers have keys.
func (a *AIService) Config() AIConfigView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.viewLocked()
}

// SetConfig saves the AI settings (keys are set separately).
func (a *AIService) SetConfig(c AIConfig) error {
	if _, err := findProvider(c.Provider); err != nil {
		return err
	}
	normalizeAIConfig(&c)
	a.mu.Lock()
	err := a.store.SaveAIConfig(c)
	if err == nil {
		a.file.Config = c
	}
	a.mu.Unlock()
	if err == nil {
		if c.Provider != "llamacpp" {
			a.llama.Stop() // free the GPU memory when the local model isn't used
		}
		a.changed()
	}
	return err
}

// local starts the local model server if the provider is llama.cpp, and
// returns the base URL and key to talk to it.
func (a *AIService) local(ctx context.Context, p *providerDef, model, key, base string) (string, string, error) {
	if p.ID != "llamacpp" {
		return key, base, nil
	}
	a.mu.Lock()
	cfg := a.file.Config.Llama
	a.mu.Unlock()
	b, k, err := a.llama.ensure(ctx, model, cfg)
	return k, b, err
}

// modelKinds is what the chosen model can read besides text. A local
// model's own capabilities decide: Gemma 4 can hear audio, a text-only
// model can't see images.
func (a *AIService) modelKinds(p *providerDef, model string) fileKinds {
	k := kindsFor(p.ID)
	if p.ID != "llamacpp" {
		return k
	}
	path, err := resolveModel(model)
	if err != nil {
		return k
	}
	if caps := modelCaps(path, findMMProj(path)); caps != nil {
		k["image"] = caps.Vision
		k["audio"] = caps.Audio
	}
	return k
}

// modelLabel is the model name shown with replies.
func modelLabel(p *providerDef, model string) string {
	if p.ID == "llamacpp" && strings.HasSuffix(strings.ToLower(model), ".gguf") {
		return strings.TrimSuffix(filepath.Base(model), filepath.Ext(model))
	}
	return model
}

// ResetConfig puts the AI settings back to their defaults. Saved API keys
// are kept.
func (a *AIService) ResetConfig() error {
	c := defaultAIConfig()
	a.mu.Lock()
	err := a.store.SaveAIConfig(c)
	if err == nil {
		a.file.Config = c
	}
	a.mu.Unlock()
	if err == nil {
		a.changed()
	}
	return err
}

// DefaultSystemPrompt returns the built-in instructions.
func (a *AIService) DefaultSystemPrompt() string { return defaultSystemPrompt }

// SetAPIKey stores (or, with an empty key, removes) a provider's API key.
func (a *AIService) SetAPIKey(providerID, key string) error {
	if _, err := findProvider(providerID); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	a.mu.Lock()
	err := a.store.SetAIKey(providerID, key)
	if err == nil {
		if key == "" {
			delete(a.file.Keys, providerID)
		} else {
			a.file.Keys[providerID] = key
		}
	}
	a.mu.Unlock()
	if err == nil {
		a.changed()
	}
	return err
}

// resolve returns the provider, model ID, API key and base URL to use.
func (a *AIService) resolve(providerID string) (*providerDef, string, string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if providerID == "" {
		providerID = a.file.Config.Provider
	}
	p, err := findProvider(providerID)
	if err != nil {
		return nil, "", "", "", err
	}
	model := a.file.Config.Models[p.ID]
	key := a.file.Keys[p.ID]
	base := strings.TrimRight(strings.TrimSpace(a.file.Config.BaseURLs[p.ID]), "/")
	if key == "" && p.KeyEnv != "" {
		key = os.Getenv(p.KeyEnv)
	}
	if p.NeedsBaseURL && base == "" {
		return p, model, key, base, errors.New(p.Name + " için sunucu adresi girilmemiş")
	}
	if p.NeedsKey && key == "" {
		return p, model, key, base, fmt.Errorf("%s için API anahtarı yok. Ayarlar → Yapay Zekâ bölümünden ekleyin", p.Name)
	}
	return p, model, key, base, nil
}

// FetchModels asks the provider which models are available.
func (a *AIService) FetchModels(providerID string) ([]string, error) {
	if providerID == "llamacpp" { // models already on this computer
		var ids []string
		for _, m := range a.llama.LocalModels() {
			ids = append(ids, m.Spec)
		}
		if len(ids) == 0 {
			return nil, errors.New("bu bilgisayarda GGUF modeli bulunamadı. Bir Hugging Face deposu yazın (ör. ggml-org/gemma-3-4b-it-GGUF) ya da .gguf dosyası seçin")
		}
		return ids, nil
	}
	p, _, key, base, err := a.resolve(providerID)
	if err != nil && (p == nil || (p.NeedsKey && key == "") || (p.NeedsBaseURL && base == "")) {
		return nil, err
	}
	if base == "" {
		base = p.DefaultBaseURL
	}
	u := base + p.modelsPath
	if p.auth == "query" {
		u += "?key=" + url.QueryEscape(key) + "&pageSize=1000"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	switch p.auth {
	case "bearer":
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	case "x-api-key":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if p.ID == "ollama" {
			return nil, errors.New("Ollama'ya bağlanılamadı. Çalıştığından emin olun (ollama serve)")
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model listesi alınamadı (HTTP %d): %s", resp.StatusCode, apiErrorText(body))
	}

	var ids []string
	switch p.listStyle {
	case "google":
		var r struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			for _, meth := range m.Methods {
				if meth == "generateContent" {
					ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
					break
				}
			}
		}
	case "ollama":
		var r struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			ids = append(ids, m.Name)
		}
	default:
		var r struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Data {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// apiErrorText pulls a readable message out of a provider's error body.
func apiErrorText(body []byte) string {
	var r struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &r) == nil && r.Error != nil {
		switch e := r.Error.(type) {
		case string:
			return e
		case map[string]any:
			if m, ok := e["message"].(string); ok {
				return m
			}
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (a *AIService) options(p *providerDef, system string, msgs []provider.Message) []goai.Option {
	a.mu.Lock()
	c := a.file.Config
	a.mu.Unlock()
	opts := []goai.Option{goai.WithMessages(msgs...), goai.WithMaxOutputTokens(c.MaxTokens)}
	if system != "" {
		opts = append(opts, goai.WithSystem(system))
	}
	if p.SupportsEffort && c.Effort != "" {
		opts = append(opts, goai.WithProviderOptions(map[string]any{"effort": c.Effort}))
	}
	if p.ID == "llamacpp" { // passed through to the model's chat template
		opts = append(opts, goai.WithProviderOptions(map[string]any{
			"chat_template_kwargs": map[string]any{"enable_thinking": c.LocalThinking}}))
	}
	return opts
}

// Test sends a tiny prompt to check the provider, model and key work.
func (a *AIService) Test() (TestResult, error) {
	p, model, key, base, err := a.resolve("")
	if err != nil {
		return TestResult{}, err
	}
	if model == "" {
		return TestResult{}, errors.New("model seçilmemiş")
	}
	// A local model may need to be loaded first.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if key, base, err = a.local(ctx, p, model, key, base); err != nil {
		return TestResult{}, err
	}
	ctx, cancel2 := context.WithTimeout(ctx, 120*time.Second)
	defer cancel2()
	text, stats, err := a.streamMeasured(ctx, p, model, key, base,
		a.options(p, "", []provider.Message{goai.UserMessage("Kendini iki cümleyle tanıt.")}), nil)
	if err != nil {
		return TestResult{}, friendlyAIError(err)
	}
	a.record(p, model, stats)
	return TestResult{Text: strings.TrimSpace(text), Model: modelLabel(p, model), Stats: stats}, nil
}

// Ask queues a question; the reply arrives as "ai:delta" / "ai:done" events.
func (a *AIService) Ask(req AskRequest) error {
	if strings.TrimSpace(req.Text) == "" {
		return errors.New("boş metin")
	}
	if _, _, _, _, err := a.resolve(""); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancels[req.ID] = cancel
	a.mu.Unlock()
	select {
	case a.queue <- askItem{req, ctx}:
		return nil
	default:
		a.mu.Lock()
		delete(a.cancels, req.ID)
		a.mu.Unlock()
		cancel()
		return errors.New("çok fazla bekleyen istek var")
	}
}

// Cancel stops a queued or running request.
func (a *AIService) Cancel(id string) {
	a.mu.Lock()
	if c, ok := a.cancels[id]; ok {
		c()
	}
	a.mu.Unlock()
}

func (a *AIService) worker() {
	for it := range a.queue {
		a.run(it.req, it.ctx)
	}
}

func (a *AIService) run(req AskRequest, ctx context.Context) {
	app := application.Get()
	defer func() {
		a.mu.Lock()
		if c, ok := a.cancels[req.ID]; ok {
			c()
			delete(a.cancels, req.ID)
		}
		a.mu.Unlock()
	}()

	done := AIDone{ID: req.ID}
	if ctx.Err() != nil { // canceled while waiting in the queue
		done.Canceled = true
		app.Event.Emit("ai:done", done)
		return
	}
	p, model, key, base, err := a.resolve("")
	if p != nil {
		done.Model = modelLabel(p, model)
	}
	if err == nil && model == "" {
		err = errors.New("model seçilmemiş")
	}
	if err != nil {
		done.Error = err.Error()
		app.Event.Emit("ai:done", done)
		return
	}

	a.mu.Lock()
	system, memory := a.file.Config.SystemPrompt, a.file.Config.Memory
	a.mu.Unlock()
	hist := req.History
	if len(hist) > memory*2 {
		hist = hist[len(hist)-memory*2:]
	}
	var msgs []provider.Message
	for _, t := range hist {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if t.Role == "assistant" {
			msgs = append(msgs, goai.AssistantMessage(t.Text))
		} else {
			msgs = append(msgs, goai.UserMessage(t.Text))
		}
	}
	if len(req.Attachments) == 0 {
		msgs = append(msgs, goai.UserMessage(req.Text))
	} else {
		text := strings.TrimSpace(req.Text)
		if text == "" {
			text = "Ekteki dosyalara bak."
		}
		parts := append(attachmentParts(p.ID, a.modelKinds(p, model), req.Attachments), provider.Part{Type: provider.PartText, Text: text})
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: parts})
	}

	if key, base, err = a.local(ctx, p, model, key, base); err != nil {
		if ctx.Err() != nil {
			done.Canceled = true
		} else {
			done.Error = err.Error()
		}
		app.Event.Emit("ai:done", done)
		return
	}
	plainSystem := system // without the tool hint, for a retry without tools
	toolOpts := a.toolOptions(req.ID, p, model)
	if toolOpts != nil {
		system += "\n\nGerektiğinde araçları kullanabilirsin (saat/tarih, hesaplama, transkriptte arama, hava durumu, web sayfası okuma, panoya kopyalama, tarayıcıda açma). Araç sonuçlarını kullanıcıya kendi cümlelerinle, kısaca aktar."
	}
	wrote := false
	onText := func(t string) {
		wrote = true
		app.Event.Emit("ai:delta", AIDelta{ID: req.ID, Text: t})
	}
	_, stats, err := a.streamMeasured(ctx, p, model, key, base, append(a.options(p, system, msgs), toolOpts...), onText)
	if err != nil && toolOpts != nil && !wrote && ctx.Err() == nil && isToolsUnsupported(err) {
		// The model can't use tools (e.g. some Ollama models): answer without them.
		_, stats, err = a.streamMeasured(ctx, p, model, key, base, a.options(p, plainSystem, msgs), onText)
	}
	if err == nil {
		done.Stats = &stats
		a.record(p, model, stats)
	}
	if err != nil {
		if ctx.Err() != nil {
			done.Canceled = true
		} else {
			done.Error = friendlyAIError(err).Error()
		}
	}
	app.Event.Emit("ai:done", done)
}

// friendlyAIError turns common provider failures into short Turkish messages.
func friendlyAIError(err error) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "401") || strings.Contains(low, "unauthorized") || strings.Contains(low, "invalid x-api-key") || strings.Contains(low, "invalid api key"):
		return errors.New("API anahtarı geçersiz (401). Ayarlar → Yapay Zekâ bölümünden kontrol edin")
	case strings.Contains(low, "429") || strings.Contains(low, "rate limit"):
		return errors.New("istek sınırına takıldı (429); biraz sonra tekrar deneyin")
	case strings.Contains(low, "404") || strings.Contains(low, "not_found") || strings.Contains(low, "model not found"):
		return errors.New("model bulunamadı (404). Model adını kontrol edin: " + msg)
	case strings.Contains(low, "connection refused") || strings.Contains(low, "no such host"):
		return errors.New("sunucuya bağlanılamadı: " + msg)
	}
	return err
}

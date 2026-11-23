package backend

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zendev-sh/goai"
)

// Speed measurements: every AI reply reports how many tokens it produced
// and how fast; the app keeps a running average per model, and local models
// can be benchmarked with a fixed prompt.

func init() {
	application.RegisterEvent[map[string]ModelStats]("ai:stats")
}

const statsSchema = `
CREATE TABLE IF NOT EXISTS model_stats (
	id         TEXT PRIMARY KEY,       -- "<provider>:<model>"
	replies    INTEGER NOT NULL DEFAULT 0,
	gen_tokens INTEGER NOT NULL DEFAULT 0, -- tokens generated, over all replies
	gen_ms     REAL    NOT NULL DEFAULT 0, -- time spent generating them
	ttft_ms    REAL    NOT NULL DEFAULT 0, -- sum of time-to-first-token
	bench      TEXT,                       -- JSON BenchResult of the last test
	updated_at INTEGER NOT NULL
);`

// ReplyStats describes one reply.
type ReplyStats struct {
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TokensPerSec float64 `json:"tokensPerSec"` // generation speed
	PromptPerSec float64 `json:"promptPerSec"` // prompt processing speed (local models)
	FirstTokenMs float64 `json:"firstTokenMs"` // wait until the first word appeared
	TotalMs      float64 `json:"totalMs"`      // whole request
	Exact        bool    `json:"exact"`        // measured by the model server (not estimated)
	Approximate  bool    `json:"approximate"`  // token count estimated from the stream
}

// BenchResult is the outcome of testing a local model.
type BenchResult struct {
	Model        string  `json:"model"`
	Device       string  `json:"device"`
	LoadMs       float64 `json:"loadMs"` // 0 when the model was already loaded
	PromptTokens int     `json:"promptTokens"`
	PromptPerSec float64 `json:"promptPerSec"`
	GenTokens    int     `json:"genTokens"`
	GenPerSec    float64 `json:"genPerSec"`
	TotalMs      float64 `json:"totalMs"`
	Context      int     `json:"context"`
	GPULayers    string  `json:"gpuLayers"`
	Reply        string  `json:"reply"`
	At           int64   `json:"at"`
}

// ModelStats is what the UI shows for a model.
type ModelStats struct {
	Replies         int          `json:"replies"`
	AvgTokensPerSec float64      `json:"avgTokensPerSec"`
	AvgFirstTokenMs float64      `json:"avgFirstTokenMs"`
	Bench           *BenchResult `json:"bench,omitempty"`
}

func statsID(providerID, model string) string { return providerID + ":" + model }

// ---- store ----------------------------------------------------------------

func (s *Store) recordReply(id string, st ReplyStats) error {
	genMs := st.TotalMs - st.FirstTokenMs
	if st.TokensPerSec > 0 {
		genMs = float64(st.OutputTokens) / st.TokensPerSec * 1000
	}
	if st.OutputTokens <= 0 || genMs <= 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO model_stats (id, replies, gen_tokens, gen_ms, ttft_ms, updated_at) VALUES (?, 1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET replies = replies + 1, gen_tokens = gen_tokens + excluded.gen_tokens,
		gen_ms = gen_ms + excluded.gen_ms, ttft_ms = ttft_ms + excluded.ttft_ms, updated_at = excluded.updated_at`,
		id, st.OutputTokens, genMs, st.FirstTokenMs, nowMs())
	return err
}

func (s *Store) saveBench(id string, b BenchResult) error {
	j, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO model_stats (id, bench, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET bench = excluded.bench, updated_at = excluded.updated_at`, id, string(j), nowMs())
	return err
}

func (s *Store) modelStats() (map[string]ModelStats, error) {
	rows, err := s.db.Query(`SELECT id, replies, gen_tokens, gen_ms, ttft_ms, bench FROM model_stats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ModelStats{}
	for rows.Next() {
		var id string
		var replies, genTokens int
		var genMs, ttft float64
		var bench sql.NullString
		if err := rows.Scan(&id, &replies, &genTokens, &genMs, &ttft, &bench); err != nil {
			return nil, err
		}
		m := ModelStats{Replies: replies}
		if genMs > 0 {
			m.AvgTokensPerSec = float64(genTokens) / (genMs / 1000)
		}
		if replies > 0 {
			m.AvgFirstTokenMs = ttft / float64(replies)
		}
		if bench.Valid && bench.String != "" {
			var b BenchResult
			if json.Unmarshal([]byte(bench.String), &b) == nil {
				m.Bench = &b
			}
		}
		out[id] = m
	}
	return out, rows.Err()
}

// ---- measuring a reply ------------------------------------------------------

// llamaCounters are llama-server's running totals (from /metrics).
type llamaCounters struct {
	promptTokens, promptSec, genTokens, genSec float64
	ok                                         bool
}

func readLlamaCounters(ctx context.Context, base, key string) llamaCounters {
	var c llamaCounters
	root := strings.TrimSuffix(base, "/v1")
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/metrics", nil)
	if err != nil {
		return c
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return c
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		name, val, ok := strings.Cut(sc.Text(), " ")
		if !ok || strings.HasPrefix(name, "#") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			continue
		}
		switch name {
		case "llamacpp:prompt_tokens_total":
			c.promptTokens = v
		case "llamacpp:prompt_seconds_total":
			c.promptSec = v
		case "llamacpp:tokens_predicted_total":
			c.genTokens = v
		case "llamacpp:tokens_predicted_seconds_total":
			c.genSec = v
		}
	}
	c.ok = true
	return c
}

// streamMeasured streams a reply, passing each piece of text to onText, and
// measures it. For the local server the numbers come from the server itself.
func (a *AIService) streamMeasured(ctx context.Context, p *providerDef, model, key, base string,
	opts []goai.Option, onText func(string)) (string, ReplyStats, error) {
	var st ReplyStats
	local := p.ID == "llamacpp"
	var before llamaCounters
	if local {
		before = readLlamaCounters(ctx, base, key)
	}
	start := time.Now()
	var first time.Time
	var sb strings.Builder
	pieces := 0
	stream, err := goai.StreamText(ctx, p.newModel(model, key, base), opts...)
	if err != nil {
		return "", st, err
	}
	for t := range stream.TextStream() {
		if first.IsZero() {
			first = time.Now()
		}
		pieces++
		sb.WriteString(t)
		if onText != nil {
			onText(t)
		}
	}
	if err := stream.Err(); err != nil {
		return sb.String(), st, err
	}
	end := time.Now()
	st.TotalMs = float64(end.Sub(start).Microseconds()) / 1000
	if !first.IsZero() {
		st.FirstTokenMs = float64(first.Sub(start).Microseconds()) / 1000
	} else {
		st.FirstTokenMs = st.TotalMs // no visible text (e.g. a thinking model used all tokens to think)
	}
	if res := stream.Result(); res != nil {
		st.InputTokens, st.OutputTokens = res.TotalUsage.InputTokens, res.TotalUsage.OutputTokens
	}
	if local {
		if after := readLlamaCounters(ctx, base, key); before.ok && after.ok && after.genSec > before.genSec {
			gen := after.genTokens - before.genTokens
			st.OutputTokens = int(gen)
			st.TokensPerSec = gen / (after.genSec - before.genSec)
			if ps := after.promptSec - before.promptSec; ps > 0 {
				st.PromptPerSec = (after.promptTokens - before.promptTokens) / ps
				if st.InputTokens == 0 {
					st.InputTokens = int(after.promptTokens - before.promptTokens)
				}
			}
			st.Exact = true
			return sb.String(), st, nil
		}
	}
	if st.OutputTokens == 0 { // provider didn't report usage: roughly one token per streamed piece
		st.OutputTokens, st.Approximate = pieces, true
	}
	if genMs := st.TotalMs - st.FirstTokenMs; st.OutputTokens > 1 && genMs > 0 {
		st.TokensPerSec = float64(st.OutputTokens-1) / (genMs / 1000)
	}
	return sb.String(), st, nil
}

// record stores a reply's stats and tells the UI.
func (a *AIService) record(p *providerDef, model string, st ReplyStats) {
	if err := a.store.recordReply(statsID(p.ID, model), st); err == nil {
		a.emitStats()
	}
}

func (a *AIService) emitStats() {
	if m, err := a.store.modelStats(); err == nil {
		if app := application.Get(); app != nil {
			app.Event.Emit("ai:stats", m)
		}
	}
}

// ModelStats returns the speed statistics per "<provider>:<model>".
func (a *AIService) ModelStats() (map[string]ModelStats, error) { return a.store.modelStats() }

// ---- benchmarking a local model -----------------------------------------------

const benchPrompt = "Türkiye'nin başkenti hakkında üç cümlelik kısa bir bilgi ver."

// Benchmark loads a local model (with the given hardware settings) and
// measures it with a fixed prompt, using the server's own timings.
func (s *LlamaService) Benchmark(spec string, cfg LlamaConfig) (BenchResult, error) {
	if cfg.GPULayers == "" {
		cfg = defaultLlamaConfig()
	}
	res := BenchResult{Model: spec, Context: cfg.Context, GPULayers: cfg.GPULayers, At: time.Now().UnixMilli()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	s.mu.Lock()
	loaded := s.cmd != nil && s.key == (llamaKey{strings.TrimSpace(spec), cfg}) && s.status.State == "ready"
	s.mu.Unlock()
	t0 := time.Now()
	base, key, err := s.ensure(ctx, spec, cfg)
	if err != nil {
		return res, err
	}
	if !loaded {
		res.LoadMs = float64(time.Since(t0).Milliseconds())
	}
	res.Device = s.Status().GPU

	body, _ := json.Marshal(map[string]any{
		"model":        "yerel",
		"messages":     []map[string]string{{"role": "user", "content": benchPrompt}},
		"max_tokens":   256,
		"temperature":  0.7,
		"stream":       false,
		"cache_prompt": false, // measure prompt processing too, not a cache hit
		// Without hidden thinking, so models can be compared (models that
		// always think still do).
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	t1 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("model yanıt vermedi: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	res.TotalMs = float64(time.Since(t1).Milliseconds())
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("model hatası (HTTP %d): %s", resp.StatusCode, apiErrorText(raw))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Timings struct {
			PromptN            int     `json:"prompt_n"`
			PromptPerSecond    float64 `json:"prompt_per_second"`
			PredictedN         int     `json:"predicted_n"`
			PredictedPerSecond float64 `json:"predicted_per_second"`
		} `json:"timings"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return res, errors.New("model yanıtı okunamadı")
	}
	res.PromptTokens, res.PromptPerSec = out.Timings.PromptN, out.Timings.PromptPerSecond
	res.GenTokens, res.GenPerSec = out.Timings.PredictedN, out.Timings.PredictedPerSecond
	if len(out.Choices) > 0 {
		res.Reply = strings.TrimSpace(out.Choices[0].Message.Content)
		if res.Reply == "" && out.Choices[0].Message.ReasoningContent != "" {
			res.Reply = "(yalnızca düşünme çıktısı üretti; yanıt için daha fazla token gerekiyor)"
		}
	}
	if s.store != nil {
		if err := s.store.saveBench(statsID("llamacpp", strings.TrimSpace(spec)), res); err == nil && s.onStats != nil {
			s.onStats()
		}
	}
	return res, nil
}

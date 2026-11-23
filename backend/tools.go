package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/zendev-sh/goai"
)

// Tools the AI can call while answering. Each is small and safe: reading
// the clock, doing arithmetic, searching the transcript, the weather, reading
// a public web page, copying to the clipboard and opening a link.

func init() {
	application.RegisterEvent[AITool]("ai:tool")
}

// AITool is emitted as "ai:tool" when the AI calls a tool and when it finishes.
type AITool struct {
	ID     string `json:"id"`     // the reply's ID
	Call   string `json:"call"`   // this call's ID
	Name   string `json:"name"`   // tool name
	Input  string `json:"input"`  // arguments (JSON)
	Output string `json:"output"` // result (shortened)
	Error  string `json:"error"`
	State  string `json:"state"` // running | done | error
}

// ToolInfo describes a tool for the settings page.
type ToolInfo struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Internet    bool   `json:"internet"` // reaches out to the internet
}

const maxToolSteps = 6 // tool calls → answer rounds per reply

type toolDef struct {
	ToolInfo
	schema string
	run    func(ctx context.Context, a *AIService, in map[string]any) (string, error)
}

var toolDefs = []toolDef{
	{ToolInfo{"saat", "Saat ve tarih", "Şu anki tarihi, saati ve günü söyler.", false},
		`{"type":"object","properties":{},"additionalProperties":false}`,
		func(_ context.Context, _ *AIService, _ map[string]any) (string, error) {
			now := time.Now()
			days := []string{"Pazar", "Pazartesi", "Salı", "Çarşamba", "Perşembe", "Cuma", "Cumartesi"}
			months := []string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"}
			zone, _ := now.Zone()
			return fmt.Sprintf("%d %s %d %s, saat %s (%s, ISO: %s)", now.Day(), months[now.Month()-1], now.Year(),
				days[now.Weekday()], now.Format("15:04"), zone, now.Format(time.RFC3339)), nil
		}},
	{ToolInfo{"hesapla", "Hesap makinesi", "Matematik ifadelerini kesin olarak hesaplar (+ − × ÷ ^ %, parantez, sqrt, sin, log…).", false},
		`{"type":"object","properties":{"ifade":{"type":"string","description":"Hesaplanacak ifade, ör. (12.5*4)^2 / sqrt(16)"}},"required":["ifade"],"additionalProperties":false}`,
		func(_ context.Context, _ *AIService, in map[string]any) (string, error) {
			expr, _ := in["ifade"].(string)
			v, err := evalExpr(expr)
			if err != nil {
				return "", err
			}
			return strconv.FormatFloat(v, 'g', 15, 64), nil
		}},
	{ToolInfo{"transkript_ara", "Transkriptte ara", "Daha önce söylenenler ve yapay zekâ yanıtları içinde arar.", false},
		`{"type":"object","properties":{"sorgu":{"type":"string","description":"Aranacak kelime veya ifade"}},"required":["sorgu"],"additionalProperties":false}`,
		func(_ context.Context, a *AIService, in map[string]any) (string, error) {
			q, _ := in["sorgu"].(string)
			return searchTranscript(a.store, q)
		}},
	{ToolInfo{"hava_durumu", "Hava durumu", "Bir yerin şu anki hava durumunu ve 3 günlük tahminini getirir (Open-Meteo).", true},
		`{"type":"object","properties":{"yer":{"type":"string","description":"Şehir veya yer adı, ör. İstanbul"}},"required":["yer"],"additionalProperties":false}`,
		func(ctx context.Context, _ *AIService, in map[string]any) (string, error) {
			place, _ := in["yer"].(string)
			return weather(ctx, place)
		}},
	{ToolInfo{"web_sayfasi_oku", "Web sayfası oku", "Verilen adresteki web sayfasının metnini getirir.", true},
		`{"type":"object","properties":{"adres":{"type":"string","description":"http(s) ile başlayan tam adres"}},"required":["adres"],"additionalProperties":false}`,
		func(ctx context.Context, _ *AIService, in map[string]any) (string, error) {
			u, _ := in["adres"].(string)
			return readWebPage(ctx, u)
		}},
	{ToolInfo{"panoya_kopyala", "Panoya kopyala", "Bir metni panoya kopyalar (ör. “bunu kopyala”).", false},
		`{"type":"object","properties":{"metin":{"type":"string"}},"required":["metin"],"additionalProperties":false}`,
		func(_ context.Context, _ *AIService, in map[string]any) (string, error) {
			t, _ := in["metin"].(string)
			if strings.TrimSpace(t) == "" {
				return "", errors.New("kopyalanacak metin boş")
			}
			if !application.Get().Clipboard.SetText(t) {
				return "", errors.New("panoya erişilemedi")
			}
			return fmt.Sprintf("%d karakter panoya kopyalandı", len([]rune(t))), nil
		}},
	{ToolInfo{"tarayicida_ac", "Tarayıcıda aç", "Bir web adresini varsayılan tarayıcıda açar.", false},
		`{"type":"object","properties":{"adres":{"type":"string","description":"http(s) ile başlayan adres"}},"required":["adres"],"additionalProperties":false}`,
		func(_ context.Context, _ *AIService, in map[string]any) (string, error) {
			u, _ := in["adres"].(string)
			pu, err := url.Parse(strings.TrimSpace(u))
			if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
				return "", errors.New("yalnızca http(s) adresleri açılabilir")
			}
			if err := application.Get().Browser.OpenURL(pu.String()); err != nil {
				return "", err
			}
			return "Tarayıcıda açıldı: " + pu.String(), nil
		}},
}

// Tools lists the tools the AI can use.
func (a *AIService) Tools() []ToolInfo {
	out := make([]ToolInfo, len(toolDefs))
	for i, t := range toolDefs {
		out[i] = t.ToolInfo
	}
	return out
}

// toolOptions returns the tool options for a reply: the enabled tools, the
// tool loop, and progress events. Nil when tools are off or the model can't
// use them.
func (a *AIService) toolOptions(replyID string, p *providerDef, model string) []goai.Option {
	a.mu.Lock()
	c := a.file.Config
	a.mu.Unlock()
	if c.NoTools {
		return nil
	}
	if p.ID == "llamacpp" { // only models whose chat template supports tools
		path, err := resolveModel(model)
		if err != nil {
			return nil
		}
		if caps := modelCaps(path, findMMProj(path)); caps == nil || !caps.Tools {
			return nil
		}
	}
	off := map[string]bool{}
	for _, n := range c.DisabledTools {
		off[n] = true
	}
	var tools []goai.Tool
	for _, d := range toolDefs {
		if off[d.Name] {
			continue
		}
		d := d
		tools = append(tools, goai.Tool{
			Name: d.Name, Description: d.Label + ": " + d.Description, InputSchema: json.RawMessage(d.schema),
			Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
				var in map[string]any
				if len(input) > 0 {
					if err := json.Unmarshal(input, &in); err != nil {
						return "", errors.New("geçersiz araç girdisi")
					}
				}
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return d.run(ctx, a, in)
			},
		})
	}
	if len(tools) == 0 {
		return nil
	}
	emit := func(t AITool) {
		t.ID = replyID
		if app := application.Get(); app != nil {
			app.Event.Emit("ai:tool", t)
		}
	}
	return []goai.Option{
		goai.WithTools(tools...),
		goai.WithMaxSteps(maxToolSteps),
		goai.WithOnToolCallStart(func(i goai.ToolCallStartInfo) {
			emit(AITool{Call: i.ToolCallID, Name: i.ToolName, Input: string(i.Input), State: "running"})
		}),
		goai.WithOnToolCall(func(i goai.ToolCallInfo) {
			t := AITool{Call: i.ToolCallID, Name: i.ToolName, Input: string(i.Input), Output: trimTo(i.Output, 2000), State: "done"}
			if i.Error != nil {
				t.State, t.Error = "error", i.Error.Error()
			}
			emit(t)
		}),
	}
}

// isToolsUnsupported tells whether an error means the model can't use tools
// (then the request is retried without them).
func isToolsUnsupported(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tool") && (strings.Contains(s, "support") || strings.Contains(s, "not allowed") || strings.Contains(s, "invalid"))
}

// ---- calculator ------------------------------------------------------------

// evalExpr evaluates an arithmetic expression: + - * / % ^, parentheses,
// constants (pi, e) and common functions. Decimal commas are accepted.
func evalExpr(s string) (float64, error) {
	s = strings.NewReplacer("×", "*", "÷", "/", "−", "-", "**", "^").Replace(s)
	s = regexp.MustCompile(`(\d),(\d)`).ReplaceAllString(s, "$1.$2")
	p := &exprParser{s: s}
	v := p.sum()
	p.space()
	if p.err == nil && p.i < len(p.s) {
		p.err = fmt.Errorf("beklenmeyen karakter: %q", p.s[p.i:])
	}
	if p.err == nil && (math.IsNaN(v) || math.IsInf(v, 0)) {
		p.err = errors.New("sonuç tanımsız (ör. sıfıra bölme)")
	}
	return v, p.err
}

type exprParser struct {
	s   string
	i   int
	err error
}

func (p *exprParser) space() {
	for p.i < len(p.s) && unicode.IsSpace(rune(p.s[p.i])) {
		p.i++
	}
}

func (p *exprParser) peek() byte {
	p.space()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *exprParser) sum() float64 {
	v := p.product()
	for p.err == nil {
		switch p.peek() {
		case '+':
			p.i++
			v += p.product()
		case '-':
			p.i++
			v -= p.product()
		default:
			return v
		}
	}
	return v
}

func (p *exprParser) product() float64 {
	v := p.power()
	for p.err == nil {
		switch p.peek() {
		case '*':
			p.i++
			v *= p.power()
		case '/':
			p.i++
			v /= p.power()
		case '%':
			p.i++
			v = math.Mod(v, p.power())
		default:
			return v
		}
	}
	return v
}

func (p *exprParser) power() float64 {
	v := p.unary()
	if p.err == nil && p.peek() == '^' {
		p.i++
		v = math.Pow(v, p.power()) // right-associative
	}
	return v
}

func (p *exprParser) unary() float64 {
	switch p.peek() {
	case '-':
		p.i++
		return -p.unary()
	case '+':
		p.i++
		return p.unary()
	}
	return p.atom()
}

var exprFuncs = map[string]func(float64) float64{
	"sqrt": math.Sqrt, "karekok": math.Sqrt, "abs": math.Abs, "sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
	"asin": math.Asin, "acos": math.Acos, "atan": math.Atan, "log": math.Log10, "ln": math.Log, "log2": math.Log2,
	"exp": math.Exp, "round": math.Round, "floor": math.Floor, "ceil": math.Ceil,
}

func (p *exprParser) atom() float64 {
	if p.err != nil {
		return 0
	}
	c := p.peek()
	switch {
	case c == '(':
		p.i++
		v := p.sum()
		if p.peek() != ')' {
			p.err = errors.New("kapanmayan parantez")
			return 0
		}
		p.i++
		return v
	case c >= '0' && c <= '9' || c == '.':
		start := p.i
		for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.' || p.s[p.i] == 'e' && p.i+1 < len(p.s) && (p.s[p.i+1] == '-' || p.s[p.i+1] >= '0' && p.s[p.i+1] <= '9')) {
			if p.s[p.i] == 'e' {
				p.i++ // exponent sign/digit follows
			}
			p.i++
		}
		v, err := strconv.ParseFloat(p.s[start:p.i], 64)
		if err != nil {
			p.err = fmt.Errorf("geçersiz sayı: %s", p.s[start:p.i])
		}
		return v
	case unicode.IsLetter(rune(c)):
		start := p.i
		for p.i < len(p.s) && (unicode.IsLetter(rune(p.s[p.i])) || p.s[p.i] >= '0' && p.s[p.i] <= '9') {
			p.i++
		}
		name := strings.ToLower(p.s[start:p.i])
		switch name {
		case "pi":
			return math.Pi
		case "e":
			return math.E
		}
		f, ok := exprFuncs[name]
		if !ok {
			p.err = fmt.Errorf("bilinmeyen fonksiyon: %s", name)
			return 0
		}
		return f(p.atom())
	}
	p.err = errors.New("ifade eksik veya hatalı")
	return 0
}

// ---- transcript search ---------------------------------------------------------

var trFold = strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u", "â", "a", "î", "i", "û", "u")

func foldTR(s string) string { return trFold.Replace(strings.ToLowerSpecial(unicode.TurkishCase, s)) }

func searchTranscript(st *Store, q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("arama sorgusu boş")
	}
	rows, err := st.LoadTranscript()
	if err != nil {
		return "", err
	}
	fq := foldTR(q)
	var hits []string
	for i := len(rows) - 1; i >= 0 && len(hits) < 10; i-- { // newest first
		r := rows[i]
		reply, _ := r.AI["text"].(string)
		if !strings.Contains(foldTR(r.Text), fq) && !strings.Contains(foldTR(reply), fq) {
			continue
		}
		when := time.UnixMilli(int64(r.At)).Format("02.01.2006 15:04")
		line := fmt.Sprintf("[%s] Kullanıcı: %s", when, trimTo(r.Text, 300))
		if reply != "" {
			line += "\n  Yanıt: " + trimTo(reply, 300)
		}
		hits = append(hits, line)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("“%s” transkriptte bulunamadı (%d kayıt arandı).", q, len(rows)), nil
	}
	return fmt.Sprintf("%d sonuç (en yeniden eskiye):\n%s", len(hits), strings.Join(hits, "\n")), nil
}

// ---- weather (Open-Meteo, no key needed) ------------------------------------------

var weatherCodes = map[int]string{
	0: "açık", 1: "çoğunlukla açık", 2: "parçalı bulutlu", 3: "kapalı", 45: "sisli", 48: "kırağılı sis",
	51: "hafif çisenti", 53: "çisenti", 55: "yoğun çisenti", 61: "hafif yağmur", 63: "yağmur", 65: "kuvvetli yağmur",
	66: "dondurucu yağmur", 67: "kuvvetli dondurucu yağmur", 71: "hafif kar", 73: "kar", 75: "yoğun kar", 77: "kar taneleri",
	80: "hafif sağanak", 81: "sağanak", 82: "şiddetli sağanak", 85: "kar sağanağı", 86: "yoğun kar sağanağı",
	95: "gök gürültülü fırtına", 96: "dolulu fırtına", 99: "şiddetli dolulu fırtına",
}

func getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "stt-app")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func weather(ctx context.Context, place string) (string, error) {
	place = strings.TrimSpace(place)
	if place == "" {
		return "", errors.New("yer adı boş")
	}
	var geo struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Admin1    string  `json:"admin1"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Timezone  string  `json:"timezone"`
		} `json:"results"`
	}
	if err := getJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?count=1&language=tr&name="+url.QueryEscape(place), &geo); err != nil {
		return "", fmt.Errorf("konum aranamadı: %w", err)
	}
	if len(geo.Results) == 0 {
		return "", fmt.Errorf("“%s” bulunamadı", place)
	}
	g := geo.Results[0]
	var w struct {
		Current struct {
			Temp     float64 `json:"temperature_2m"`
			Feels    float64 `json:"apparent_temperature"`
			Humidity float64 `json:"relative_humidity_2m"`
			Wind     float64 `json:"wind_speed_10m"`
			Code     int     `json:"weather_code"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
			Code []int     `json:"weather_code"`
			Rain []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&timezone=auto&forecast_days=3"+
		"&current=temperature_2m,apparent_temperature,relative_humidity_2m,wind_speed_10m,weather_code"+
		"&daily=temperature_2m_max,temperature_2m_min,weather_code,precipitation_probability_max", g.Latitude, g.Longitude)
	if err := getJSON(ctx, u, &w); err != nil {
		return "", fmt.Errorf("hava durumu alınamadı: %w", err)
	}
	where := strings.Join(compact(g.Name, g.Admin1, g.Country), ", ")
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — şu an: %.0f°C (hissedilen %.0f°C), %s, nem %%%.0f, rüzgâr %.0f km/s.\n", where,
		w.Current.Temp, w.Current.Feels, weatherCodes[w.Current.Code], w.Current.Humidity, w.Current.Wind)
	for i := range w.Daily.Time {
		if i >= len(w.Daily.Max) || i >= len(w.Daily.Min) || i >= len(w.Daily.Code) {
			break
		}
		rain := ""
		if i < len(w.Daily.Rain) {
			rain = fmt.Sprintf(", yağış olasılığı %%%.0f", w.Daily.Rain[i])
		}
		fmt.Fprintf(&sb, "%s: %.0f / %.0f°C, %s%s\n", w.Daily.Time[i], w.Daily.Max[i], w.Daily.Min[i], weatherCodes[w.Daily.Code[i]], rain)
	}
	return strings.TrimSpace(sb.String()), nil
}

func compact(ss ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range ss {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- reading a web page ----------------------------------------------------------

const maxPageText = 20000

// readWebPage fetches a public web page and returns its readable text. It
// refuses local and private addresses, so a prompt can't make the app probe
// this computer or the local network.
func readWebPage(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("geçerli bir http(s) adresi verin")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // connect directly, so the address check below can't be bypassed
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
						return nil, errors.New("yerel veya özel ağ adresleri okunamaz")
					}
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (stt-app)")
	req.Header.Set("Accept-Language", "tr,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sayfa açılamadı: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sayfa açılamadı (HTTP %d)", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "html") && !strings.HasPrefix(ct, "text/") && !strings.Contains(ct, "json") {
		return "", fmt.Errorf("metin olmayan içerik (%s)", ct)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	text := string(body)
	title := ""
	if strings.Contains(ct, "html") || strings.Contains(strings.ToLower(text[:min(len(text), 512)]), "<html") {
		if m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(text); m != nil {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
		text = htmlToText(text)
	}
	text = strings.ToValidUTF8(text, "")
	if len(text) > maxPageText {
		text = text[:maxPageText] + "\n…(sayfanın devamı kısaltıldı)"
	}
	head := "Adres: " + resp.Request.URL.String()
	if title != "" {
		head += "\nBaşlık: " + title
	}
	return head + "\n\n" + text, nil
}

var (
	reDrop   = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head|nav|footer|form|iframe)\b.*?</(script|style|noscript|svg|head|nav|footer|form|iframe)>`)
	reBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/h[1-6]|/li|/tr|/section|/article)\s*/?>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces = regexp.MustCompile(`[ \t\f\v\r]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

func htmlToText(s string) string {
	s = reDrop.ReplaceAllString(s, " ")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reSpaces.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(reLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// sortedToolNames is used to keep the disabled list tidy.
func sortedToolNames(names []string) []string {
	known := map[string]bool{}
	for _, d := range toolDefs {
		known[d.Name] = true
	}
	var out []string
	for _, n := range names {
		if known[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

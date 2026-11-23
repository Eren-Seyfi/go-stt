package backend

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Commands are the user-defined voice command phrases.
type Commands struct {
	Wake  []string `json:"wake"`  // start writing (leave standby)
	Sleep []string `json:"sleep"` // stop writing, keep listening for a wake phrase
	Stop  []string `json:"stop"`  // turn the microphone off
}

// CommandMatch is the result of matching text against the commands.
type CommandMatch struct {
	Kind   string `json:"kind"`   // "wake" | "sleep" | "stop" | "" (none)
	Phrase string `json:"phrase"` // the phrase as the user wrote it
	Before string `json:"before"` // text before the phrase
	After  string `json:"after"`  // text after the phrase
}

// ---- phrase matching ----------------------------------------------------

// Phrases match whole words, ignoring case (Turkish rules: I/ı, İ/i) and
// punctuation, so "hey yapay zeka" matches "Hey, yapay zekâ!". A phrase also
// matches a word carrying a Turkish suffix after an apostrophe ("Ada'ya"),
// but not a longer word ("Adam").

type token struct {
	word       string // normalised
	start, end int    // byte offsets in the original text
}

var circumflex = strings.NewReplacer("â", "a", "î", "i", "û", "u")

func normWord(s string) string {
	// Whisper is inconsistent with circumflexes ("zekâ" / "zeka").
	return circumflex.Replace(strings.ToLowerSpecial(unicode.TurkishCase, s))
}

func tokenize(s string) []token {
	var toks []token
	start := -1
	for i, r := range s {
		isWord := unicode.IsLetter(r) || unicode.IsDigit(r)
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			toks = append(toks, token{normWord(s[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		toks = append(toks, token{normWord(s[start:]), start, len(s)})
	}
	return toks
}

type phrase struct {
	raw   string
	words []string
}

// compilePhrases normalises, de-duplicates and orders phrases longest first,
// so "hey ada" wins over "ada" at the same position.
func compilePhrases(list []string) []phrase {
	seen := map[string]bool{}
	var out []phrase
	for _, raw := range list {
		var words []string
		for _, t := range tokenize(raw) {
			words = append(words, t.word)
		}
		key := strings.Join(words, " ")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, phrase{strings.TrimSpace(raw), words})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].words) > len(out[j].words) })
	return out
}

// findPhrase returns the earliest (then longest) phrase in text, with the
// byte range it covers.
func findPhrase(text string, ps []phrase) (p phrase, start, end int, ok bool) {
	if len(ps) == 0 {
		return
	}
	toks := tokenize(text)
	for i := range toks {
		for _, cand := range ps {
			n := len(cand.words)
			if i+n > len(toks) {
				continue
			}
			match := true
			for k := range n {
				if toks[i+k].word != cand.words[k] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			end = toks[i+n-1].end
			// "Ada'ya": swallow a suffix glued to the phrase by an apostrophe.
			for _, ap := range []string{"'", "’"} {
				if strings.HasPrefix(text[end:], ap) && i+n < len(toks) && toks[i+n].start == end+len(ap) {
					end = toks[i+n].end
				}
			}
			return cand, toks[i].start, end, true
		}
	}
	return
}

func trimLead(s string) string  { return strings.TrimLeft(s, " ,.!?;:-") }
func trimTrail(s string) string { return strings.TrimRight(s, " ,;:-") }

func matchIn(kind, text string, ps []phrase) (CommandMatch, bool) {
	p, start, end, ok := findPhrase(text, ps)
	if !ok {
		return CommandMatch{}, false
	}
	return CommandMatch{Kind: kind, Phrase: p.raw, Before: trimTrail(strings.TrimSpace(text[:start])), After: trimLead(text[end:])}, true
}

// matchCommand checks stop, then sleep, then wake phrases.
func matchCommand(text string, cmds Commands) CommandMatch {
	for _, k := range []struct {
		kind string
		list []string
	}{{"stop", cmds.Stop}, {"sleep", cmds.Sleep}, {"wake", cmds.Wake}} {
		if m, ok := matchIn(k.kind, text, compilePhrases(k.list)); ok {
			return m
		}
	}
	return CommandMatch{}
}

// ---- session filter -----------------------------------------------------

// commandFilter sits between a recognizer and the UI and acts on voice
// commands. With wake mode on it starts in standby: everything is swallowed
// until a wake phrase is heard, then text passes through (minus the phrase)
// until a sleep phrase or `timeout` of silence. Stop phrases end the session
// in either mode. Commands act on finished phrases, except wake, which also
// fires on partials so writing starts sooner.
type commandFilter struct {
	mu        sync.Mutex
	wakeMode  bool
	wake      []phrase
	sleep     []phrase
	stop      []phrase
	active    bool
	inWakeUtt bool // current utterance started with the wake phrase: keep stripping it
	stopped   bool
	busy      bool // the app is busy (e.g. the AI is replying): don't go back to standby
	last      time.Time
	timeout   time.Duration
	out       func(kind, text string)
	onState   func(active bool)
	onHeard   func(text string)    // standby: what was heard (for diagnosing missed phrases)
	onCommand func(m CommandMatch) // a command was recognised
	onStop    func()               // stop phrase: end the session
	done      chan struct{}
}

func newCommandFilter(cmds Commands, wakeMode bool, timeout time.Duration,
	out func(kind, text string), onState func(bool), onHeard func(string),
	onCommand func(CommandMatch), onStop func()) *commandFilter {
	f := &commandFilter{
		wakeMode: wakeMode, active: !wakeMode, timeout: timeout,
		wake: compilePhrases(cmds.Wake), sleep: compilePhrases(cmds.Sleep), stop: compilePhrases(cmds.Stop),
		out: out, onState: onState, onHeard: onHeard, onCommand: onCommand, onStop: onStop,
		done: make(chan struct{}),
	}
	if wakeMode {
		go f.watch()
	}
	return f
}

func (f *commandFilter) close() { close(f.done) }

// setBusy holds off the standby timeout while busy; when it ends, the
// timeout starts over, so there is time to answer after e.g. an AI reply.
func (f *commandFilter) setBusy(busy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy && !busy {
		f.last = time.Now()
	}
	f.busy = busy
}

func (f *commandFilter) watch() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-f.done:
			return
		case <-t.C:
			f.mu.Lock()
			if f.active && !f.stopped && !f.busy && time.Since(f.last) > f.timeout {
				f.sleepLocked()
			}
			f.mu.Unlock()
		}
	}
}

func (f *commandFilter) sleepLocked() {
	f.active, f.inWakeUtt = false, false
	f.out("partial", "")
	f.onState(false)
}

func (f *commandFilter) emit(kind, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}

	// Stop: works in standby and while writing.
	if kind == "final" {
		if m, ok := matchIn("stop", text, f.stop); ok {
			if f.active && m.Before != "" {
				f.out("final", m.Before)
			}
			f.out("partial", "")
			f.stopped = true
			f.onCommand(m)
			go f.onStop() // Stop waits for the recognizer goroutine we're on; don't block it
			return
		}
	}

	if !f.active {
		_, isSleep := matchIn("sleep", text, f.sleep)
		m, ok := matchIn("wake", text, f.wake)
		if !ok || (kind == "final" && isSleep) {
			if text != "" && f.onHeard != nil {
				f.onHeard(text) // standby: show it, but don't transcribe
			}
			return
		}
		f.active, f.last = true, time.Now()
		f.inWakeUtt = kind == "partial"
		f.onCommand(m)
		f.onState(true)
		text = m.After
	} else if f.inWakeUtt {
		// Partials of the waking utterance are re-transcribed from its start,
		// so they keep containing the wake phrase.
		if m, ok := matchIn("wake", text, f.wake); ok {
			text = m.After
		}
		if kind == "final" {
			f.inWakeUtt = false
		}
	}

	if text != "" {
		f.last = time.Now()
	}

	if kind == "final" {
		if f.wakeMode {
			if m, ok := matchIn("sleep", text, f.sleep); ok {
				if m.Before != "" {
					f.out("final", m.Before)
				}
				f.onCommand(m)
				f.sleepLocked()
				return
			}
		}
		if text == "" {
			f.out("partial", "")
			return
		}
	}
	f.out(kind, text)
}

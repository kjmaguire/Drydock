// Package redact removes secrets from what Drydock stores: the output an agent or a tool produced, the records of a workflow
// run, the text of an event (docs/security.md s8; audit 2026-10-16, H7). It does not scan by itself: every function takes the
// scanner's Redact (security.Scanner.Redact: gitleaks' rules plus the exact values of the keys the app holds).
//
// Most of what is stored is JSON, and a secret in JSON can sit across an escape (a private key's newlines are \n in the text), so
// JSON values are redacted string by string, never as raw text. Streamed text arrives in pieces; Stream holds a word back until it
// is whole, so a token the model prints is not split across two records that are each clean.
package redact

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Func replaces the secrets in a text with a mask and returns the rest unchanged (security.Scanner.Redact).
type Func = func(string) string

// MinString is the shortest string that is scanned: no key, token or password the scanner knows is shorter.
const MinString = 8

// JSON returns raw with every string value of the JSON redacted. Keys and the structure are unchanged; when nothing was
// redacted the original bytes come back as they were. Bytes that are not JSON are redacted as text.
func JSON(raw []byte, f Func) []byte {
	if f == nil || len(bytes.TrimSpace(raw)) == 0 {
		return raw
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil || dec.More() {
		return []byte(f(string(raw)))
	}
	changed := false
	v = walk(v, f, &changed)
	if !changed {
		return raw
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return []byte(f(string(raw)))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func walk(v any, f Func, changed *bool) any {
	switch t := v.(type) {
	case string:
		if len(t) < MinString {
			return t
		}
		if r := f(t); r != t {
			*changed = true
			return r
		}
	case []any:
		for i := range t {
			t[i] = walk(t[i], f, changed)
		}
	case map[string]any:
		for k, e := range t {
			t[k] = walk(e, f, changed)
		}
	}
	return v
}

// Lines redacts a text of any size line by line (a secret is one line, and the scanner refuses a text over its size cap whole).
func Lines(text string, f Func) string {
	if f == nil || len(text) < MinString {
		return text
	}
	var b strings.Builder
	for len(text) > 0 {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i+1], text[i+1:]
		} else {
			text = ""
		}
		if len(line) >= MinString {
			line = f(line)
		}
		b.WriteString(line)
	}
	return b.String()
}

// Stream passes streamed text through a redactor without cutting a word in two. Text goes in with Write as it arrives; what
// comes out is every complete word so far (redacted), and the unfinished word at the end stays inside until the next Write
// completes it or Flush ends the stream. A secret has no space in it, so it is whole in one piece of output.
type Stream struct {
	f       Func
	pending strings.Builder
	// MaxHold is how long an unfinished word may grow before it is let go anyway (default 4096 bytes): a "word" that long is
	// not a token.
	MaxHold int
}

// NewStream starts a stream over f (nil: text passes through, still cut at word ends).
func NewStream(f Func) *Stream { return &Stream{f: f} }

func (s *Stream) maxHold() int {
	if s.MaxHold > 0 {
		return s.MaxHold
	}
	return 4096
}

func (s *Stream) apply(text string) string {
	if s.f == nil || len(text) < MinString {
		return text
	}
	return s.f(text)
}

// Write adds text and returns what can be emitted now.
func (s *Stream) Write(text string) string {
	s.pending.WriteString(text)
	all := s.pending.String()
	cut := strings.LastIndexAny(all, " \t\r\n")
	if cut < 0 {
		if len(all) <= s.maxHold() {
			return ""
		}
		cut = len(all) - 1
	}
	emit, keep := all[:cut+1], all[cut+1:]
	s.pending.Reset()
	s.pending.WriteString(keep)
	return s.apply(emit)
}

// Flush ends the stream: everything still held, redacted.
func (s *Stream) Flush() string {
	all := s.pending.String()
	s.pending.Reset()
	return s.apply(all)
}

// Held is how many bytes are waiting for the end of their word.
func (s *Stream) Held() int { return s.pending.Len() }

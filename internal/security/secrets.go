package security

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/detect"
)

// Finding is a secret finding. It never carries the secret text: only the rule, a location and a mask.
type Finding struct {
	RuleID      string `json:"ruleId"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // "gitleaks" or "known"
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	StartColumn int    `json:"startColumn"`
	EndColumn   int    `json:"endColumn"`
	Length      int    `json:"length"`
	Start       int    `json:"start"` // byte offsets of the secret in the scanned text
	End         int    `json:"end"`
	Mask        string `json:"mask"` // "[REDACTED:<rule>]"; no characters of the secret
}

// String is log-safe.
func (f Finding) String() string {
	return fmt.Sprintf("%s %s (line %d, %d chars)", f.Source, f.RuleID, f.StartLine, f.Length)
}

// SecretScanner is the contract the LLM wrapper and other scan points call (docs/security.md s8).
type SecretScanner interface {
	Scan(ctx context.Context, text string) ([]Finding, error)
	Redact(text string) string
}

// Errors from scanning.
var (
	ErrTooLarge       = errors.New("security: text exceeds the scan size cap")
	ErrSecretTooShort = errors.New("security: known secret is too short to redact safely")
)

// MinKnownSecretLen is the shortest value accepted by RegisterSecret: shorter values would shred ordinary text.
const MinKnownSecretLen = 8

// Scanner scans text with the gitleaks default rule set and redacts exact values of secrets registered at runtime
// (API keys the app itself holds). It is safe for concurrent use.
type Scanner struct {
	det      *detect.Detector
	maxBytes int

	mu    sync.RWMutex
	known map[string][]string // name -> forms (raw, json-escaped, url-escaped, base64 of the raw value)
}

// ScannerOption configures NewScanner.
type ScannerOption func(*Scanner)

// WithMaxBytes sets the size cap (default 8 MiB). Larger input fails closed: Scan returns ErrTooLarge and Redact
// withholds the text.
func WithMaxBytes(n int) ScannerOption { return func(s *Scanner) { s.maxBytes = n } }

// NewScanner builds a Scanner over gitleaks' default config.
func NewScanner(opts ...ScannerOption) (*Scanner, error) {
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("security: gitleaks default config: %w", err)
	}
	s := &Scanner{det: d, maxBytes: 8 << 20, known: map[string][]string{}}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// RegisterSecret adds an exact-value redaction under name (for example "provider:openrouter"). Re-registering a name
// replaces its value. The value is never logged or returned.
func (s *Scanner) RegisterSecret(name, value string) error {
	if len(value) < MinKnownSecretLen {
		return ErrSecretTooShort
	}
	forms := secretForms(value)
	s.mu.Lock()
	s.known[name] = forms
	s.mu.Unlock()
	return nil
}

// UnregisterSecret removes a registered value.
func (s *Scanner) UnregisterSecret(name string) {
	s.mu.Lock()
	delete(s.known, name)
	s.mu.Unlock()
}

func secretForms(v string) []string {
	forms := []string{v}
	add := func(f string) {
		for _, e := range forms {
			if e == f {
				return
			}
		}
		if len(f) >= MinKnownSecretLen {
			forms = append(forms, f)
		}
	}
	// JSON-escaped (quotes, backslashes, control characters) and URL-escaped forms.
	jf := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(v)
	add(jf)
	add(url.QueryEscape(v))
	add(url.PathEscape(v))
	add(base64.StdEncoding.EncodeToString([]byte(v)))
	add(base64.RawStdEncoding.EncodeToString([]byte(v)))
	return forms
}

type knownHit struct {
	name       string
	start, end int
}

func (s *Scanner) knownHits(text string) []knownHit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var hits []knownHit
	for name, forms := range s.known {
		for _, f := range forms {
			from := 0
			for {
				i := strings.Index(text[from:], f)
				if i < 0 {
					break
				}
				hits = append(hits, knownHit{name, from + i, from + i + len(f)})
				from += i + len(f)
			}
		}
	}
	return hits
}

// Scan returns findings in text. Findings never include the secret.
func (s *Scanner) Scan(ctx context.Context, text string) ([]Finding, error) {
	if len(text) > s.maxBytes {
		return nil, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Finding
	for _, h := range s.knownHits(text) {
		sl, sc := lineCol(text, h.start)
		el, ec := lineCol(text, h.end)
		out = append(out, Finding{RuleID: "known:" + h.name, Source: "known", StartLine: sl, EndLine: el, StartColumn: sc, EndColumn: ec,
			Length: h.end - h.start, Start: h.start, End: h.end, Mask: mask("known:" + h.name)})
	}
	for _, f := range s.det.DetectContext(ctx, detect.Fragment{Raw: text}) {
		start, end := secretOffsets(text, f.StartLine, f.Secret)
		out = append(out, Finding{RuleID: f.RuleID, Description: f.Description, Source: "gitleaks", StartLine: f.StartLine + 1, EndLine: f.EndLine + 1,
			StartColumn: f.StartColumn, EndColumn: f.EndColumn, Length: len(f.Secret), Start: start, End: end, Mask: mask(f.RuleID)})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartLine != out[j].StartLine {
			return out[i].StartLine < out[j].StartLine
		}
		return out[i].StartColumn < out[j].StartColumn
	})
	return out, nil
}

// secretOffsets locates secret in text, searching from the start of the 0-based line gitleaks reported. It returns
// (0, 0) when the secret cannot be found, which callers treat as "redact the whole text".
func secretOffsets(text string, line int, secret string) (int, int) {
	if secret == "" {
		return 0, 0
	}
	from := 0
	for l := 0; l < line; l++ {
		i := strings.IndexByte(text[from:], '\n')
		if i < 0 {
			break
		}
		from += i + 1
	}
	i := strings.Index(text[from:], secret)
	if i < 0 {
		if i = strings.Index(text, secret); i < 0 {
			return 0, 0
		}
		return i, i + len(secret)
	}
	return from + i, from + i + len(secret)
}

func mask(rule string) string { return "[REDACTED:" + rule + "]" }

// lineCol converts a byte offset into a 1-based line and column.
func lineCol(text string, off int) (int, int) {
	line, last := 1, 0
	for i := 0; i < off && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			last = i + 1
		}
	}
	return line, off - last + 1
}

// Redact replaces every known secret value and every gitleaks finding in text with "[REDACTED:<rule>]". Input over
// the size cap is withheld entirely (fail closed).
func (s *Scanner) Redact(text string) string {
	if len(text) > s.maxBytes {
		return mask("oversize")
	}
	out := s.redactKnown(text)
	var secrets []struct{ val, rule string }
	for _, f := range s.det.Detect(detect.Fragment{Raw: out}) {
		if f.Secret != "" {
			secrets = append(secrets, struct{ val, rule string }{f.Secret, f.RuleID})
		}
	}
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i].val) > len(secrets[j].val) })
	for _, sec := range secrets {
		out = strings.ReplaceAll(out, sec.val, mask(sec.rule))
	}
	return out
}

func (s *Scanner) redactKnown(text string) string {
	s.mu.RLock()
	type pair struct{ val, name string }
	var ps []pair
	for name, forms := range s.known {
		for _, f := range forms {
			ps = append(ps, pair{f, name})
		}
	}
	s.mu.RUnlock()
	sort.SliceStable(ps, func(i, j int) bool { return len(ps[i].val) > len(ps[j].val) })
	for _, p := range ps {
		text = strings.ReplaceAll(text, p.val, mask("known:"+p.name))
	}
	return text
}

var _ SecretScanner = (*Scanner)(nil)

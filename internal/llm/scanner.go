package llm

import (
	"context"
	"strconv"
	"strings"
)

// Scanner scans outbound text for secrets (docs/security.md s8). The gitleaks-backed implementation is a later
// slice; the Wrapper only needs this contract. A scanner error is a refusal: the Wrapper fails closed.
type Scanner interface {
	// Scan returns one Finding per secret found in text. Findings never contain the secret value.
	Scan(ctx context.Context, text string) ([]Finding, error)
}

// NopScanner finds nothing. It is an explicit opt-out for use only until the gitleaks scanner lands; the Wrapper
// refuses to run with a nil Scanner so skipping the scan is never an accident.
type NopScanner struct{}

func (NopScanner) Scan(context.Context, string) ([]Finding, error) { return nil, nil }

// ExactValueScanner flags any of the given secret values appearing verbatim in text (the "exact-value redactor
// seeded with every secret in the credential store", docs/security.md s8). Values shorter than 8 bytes are ignored
// to avoid flagging ordinary words.
type ExactValueScanner struct{ values []string }

// NewExactValueScanner builds a scanner for the given values.
func NewExactValueScanner(values ...string) *ExactValueScanner {
	s := &ExactValueScanner{}
	for _, v := range values {
		if len(v) >= 8 {
			s.values = append(s.values, v)
		}
	}
	return s
}

func (s *ExactValueScanner) Scan(_ context.Context, text string) ([]Finding, error) {
	var out []Finding
	for _, v := range s.values {
		if strings.Contains(text, v) {
			out = append(out, Finding{Rule: "exact-secret-value"})
		}
	}
	return out, nil
}

// requestParts yields the labeled text parts of a request that leave the machine.
func requestParts(r Request) []struct{ where, text string } {
	var out []struct{ where, text string }
	add := func(where, text string) {
		if text != "" {
			out = append(out, struct{ where, text string }{where, text})
		}
	}
	for i, b := range r.System {
		add(label("system", i), b.Text)
	}
	for i, m := range r.Messages {
		add(label("messages", i), m.Content)
		add(label("messages", i)+".reasoning", m.Reasoning)
		for j, tc := range m.ToolCalls {
			add(label("messages", i)+label(".toolCalls", j), tc.Arguments)
		}
	}
	for i, t := range r.Tools {
		add(label("tools", i), t.Description+" "+string(t.Parameters))
	}
	return out
}

func label(prefix string, i int) string {
	return prefix + "[" + strconv.Itoa(i) + "]"
}

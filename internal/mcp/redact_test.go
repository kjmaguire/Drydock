package mcp

import (
	"strings"
	"testing"
)

// A scanner that reports a repeated secret once (one offset) still gets every copy redacted, and overlapping findings
// merge into one redaction.
func TestRedactRemovesEveryCopyOfAFoundSecret(t *testing.T) {
	secret := "sk-test-0123456789abcdef"
	text := "first " + secret + " then again " + secret + " end"
	i := strings.Index(text, secret)
	out, rules := redact(text, []Finding{{Rule: "api-key", Start: i, End: i + len(secret)}})
	if strings.Contains(out, secret) || strings.Count(out, "[REDACTED:api-key]") != 2 || len(rules) != 1 {
		t.Fatalf("redacted %q (rules %v)", out, rules)
	}
	out, _ = redact("key=ABCDEFGHIJKLMNOP!", []Finding{{Rule: "a", Start: 4, End: 12}, {Rule: "b", Start: 8, End: 20}})
	if strings.Contains(out, "IJKL") || strings.Contains(out, "MNOP") {
		t.Fatalf("overlap left text: %q", out)
	}
}

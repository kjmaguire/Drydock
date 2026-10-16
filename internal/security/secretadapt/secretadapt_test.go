package secretadapt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
)

func fakeToken() string { return "gh" + "p_" + "Z9q8W7e6R5t4Y3u2I1o0P9a8S7d6F5g4H3j2" }

func TestAdaptersMatchConsumerContracts(t *testing.T) {
	s, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	var l llm.Scanner = secretadapt.LLM(s)
	var m mcp.Scanner = secretadapt.MCP(s)
	text := "line one\nexport TOKEN_VALUE=" + fakeToken() + "\nline three\n"

	lf, err := l.Scan(context.Background(), text)
	if err != nil || len(lf) == 0 || lf[0].Rule == "" {
		t.Fatalf("llm findings %v %v", lf, err)
	}
	mf := m.Scan(text)
	if len(mf) == 0 {
		t.Fatal("mcp found nothing")
	}
	got := text[mf[0].Start:mf[0].End]
	if got != fakeToken() {
		t.Fatalf("offsets delimit %q, want the token", got)
	}
	// the gateway replaces [Start,End) with [REDACTED:<rule>]
	red := text[:mf[0].Start] + "[REDACTED:" + mf[0].Rule + "]" + text[mf[0].End:]
	if strings.Contains(red, fakeToken()) {
		t.Fatal("secret survived")
	}
	if fs := m.Scan("nothing to see here"); len(fs) != 0 {
		t.Fatalf("false positive %v", fs)
	}
}

func TestMCPAdapterFailsClosedOnScanError(t *testing.T) {
	s, err := security.NewScanner(security.WithMaxBytes(16))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("a", 64)
	fs := secretadapt.MCP(s).Scan(text)
	if len(fs) != 1 || fs[0].Start != 0 || fs[0].End != len(text) {
		t.Fatalf("scan error must cover the whole text: %v", fs)
	}
	if _, err := secretadapt.LLM(s).Scan(context.Background(), text); err == nil {
		t.Fatal("llm adapter swallowed the scan error")
	}
}

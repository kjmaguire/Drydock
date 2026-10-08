package security

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestScannerFindsAndMasks(t *testing.T) {
	s := sharedScanner(t)
	ctx := context.Background()
	for name, secret := range map[string]string{"github": fakeGitHubToken(), "aws": fakeAWSKey()} {
		text := "line one\nexport TOKEN_VALUE=" + secret + "\nline three\n"
		fs, err := s.Scan(ctx, text)
		if err != nil {
			t.Fatal(err)
		}
		if len(fs) == 0 {
			t.Fatalf("%s: no finding", name)
		}
		f := fs[0]
		if f.StartLine != 2 || f.Source != "gitleaks" || f.Length == 0 {
			t.Errorf("%s: finding %+v", name, f)
		}
		if f.End-f.Start != len(secret) || text[f.Start:f.End] != secret {
			t.Errorf("%s: offsets [%d,%d) do not delimit the secret", name, f.Start, f.End)
		}
		for _, str := range []string{f.Mask, f.RuleID, f.Description, f.String()} {
			if strings.Contains(str, secret) || (len(secret) > 10 && strings.Contains(str, secret[4:12])) {
				t.Errorf("%s: finding leaks the secret: %q", name, str)
			}
		}
		red := s.Redact(text)
		if strings.Contains(red, secret) || !strings.Contains(red, "REDACTED") || !strings.Contains(red, "line one") {
			t.Errorf("%s: redacted text %q", name, red)
		}
	}
}

func TestScannerCleanText(t *testing.T) {
	s := sharedScanner(t)
	text := "func main() {\n\tfmt.Println(\"hello world\")\n}\n// see https://example.test/docs\nid = 123456\n"
	fs, err := s.Scan(context.Background(), text)
	if err != nil || len(fs) != 0 {
		t.Fatalf("false positive: %v %v", fs, err)
	}
	if s.Redact(text) != text {
		t.Fatal("clean text changed by Redact")
	}
}

func TestScannerKnownSecrets(t *testing.T) {
	s, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	key := "app-" + "internal-" + "k3y-9f8e7d6c5b4a" // matches no gitleaks rule
	text := "request failed for " + key + " (retrying with " + key + ")"
	fs, _ := s.Scan(context.Background(), text)
	for _, f := range fs {
		if f.Source == "known" {
			t.Fatal("unregistered secret reported as known")
		}
	}
	if err := s.RegisterSecret("openrouter", key); err != nil {
		t.Fatal(err)
	}
	fs, err = s.Scan(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].RuleID != "known:openrouter" || fs[0].Source != "known" || fs[0].Length != len(key) {
		t.Fatalf("findings %+v", fs)
	}
	red := s.Redact(text)
	if strings.Contains(red, key) || strings.Count(red, "[REDACTED:known:openrouter]") != 2 {
		t.Fatalf("redacted %q", red)
	}
	special := "tok\"en" + "\\value/with+chars=" + "12345"
	if err := s.RegisterSecret("special", special); err != nil {
		t.Fatal(err)
	}
	for name, form := range map[string]string{"raw": special, "json": `tok\"en\\value/with+chars=12345`, "query": "tok%22en%5Cvalue%2Fwith%2Bchars%3D12345"} {
		if got := s.Redact("x=" + form + ";"); strings.Contains(got, form) {
			t.Errorf("%s form survives: %q", name, got)
		}
	}
	s.UnregisterSecret("openrouter")
	if s.Redact(text) != text {
		t.Fatal("unregistered secret still redacted")
	}
}

func TestScannerRegisterRejectsShortValues(t *testing.T) {
	s := sharedScanner(t)
	for _, v := range []string{"", "a", "short", "1234567"} {
		if err := s.RegisterSecret("x", v); !errors.Is(err, ErrSecretTooShort) {
			t.Errorf("value %q: %v", v, err)
		}
	}
}

func TestScannerFailsClosedOnOversize(t *testing.T) {
	s, err := NewScanner(WithMaxBytes(64))
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a", 65)
	if _, err := s.Scan(context.Background(), big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Scan: %v", err)
	}
	if got := s.Redact(big); strings.Contains(got, "aaaa") {
		t.Fatalf("oversize text passed through Redact: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Scan(ctx, "x"); err == nil {
		t.Fatal("cancelled context ignored")
	}
}

func TestScannerSatisfiesInterface(t *testing.T) {
	var _ SecretScanner = sharedScanner(t)
}

func TestScannerConcurrentUse(t *testing.T) {
	s := sharedScanner(t)
	_ = s.RegisterSecret("concurrent", "app-internal-"+"k3y-0a1b2c3d4e5f")
	defer s.UnregisterSecret("concurrent")
	text := "token " + fakeGitHubToken() + " and app-internal-k3y-0a1b2c3d4e5f\n"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if fs, err := s.Scan(context.Background(), text); err != nil || len(fs) < 2 {
					t.Errorf("scan: %v %v", fs, err)
				}
				if red := s.Redact(text); strings.Contains(red, fakeGitHubToken()) || strings.Contains(red, "k3y-0a1b2c3d4e5f") {
					t.Errorf("redact leaked: %q", red)
				}
			}
		}()
	}
	wg.Wait()
}

package winverify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Facts name the system and the tools, and never carry an environment variable's value.
func TestFactsDescribeTheMachineWithoutEnvironmentValues(t *testing.T) {
	const secret = "drydock-verify-secret-value-1234567890"
	t.Setenv("DRYDOCK_VERIFY_PROBE_SECRET", secret)
	t.Setenv("GH_TOKEN", "ghp_"+strings.Repeat("a", 36))
	f := CollectFacts(context.Background())
	if f.OS == "" || f.Arch == "" || f.Cores <= 0 {
		t.Fatalf("%+v", f)
	}
	var names []string
	for _, tool := range f.Tools {
		names = append(names, tool.Name)
		if tool.Found && (tool.Path == "" || tool.Version == "") {
			t.Errorf("%s found without a path or version: %+v", tool.Name, tool)
		}
	}
	if got := strings.Join(names, ","); got != "git,gh,node,npm,claude,docker,go" {
		t.Fatalf("tools %s", got)
	}
	b, _ := json.Marshal(f)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "ghp_aaaa") {
		t.Fatalf("an environment value reached the facts: %s", b)
	}
	if line := f.OSLine(); !strings.Contains(line, f.OS) || !strings.Contains(line, f.Arch) {
		t.Fatalf("OS line %q", line)
	}
	if !strings.Contains(f.HardwareLine(), "logical CPUs") {
		t.Fatalf("hardware line %q", f.HardwareLine())
	}
}

func TestRedactorKeepsTextReadable(t *testing.T) {
	r := redactor{home: filepath.Join(string(filepath.Separator)+"home", "kyle")}
	in := "token=abc123&x=1 key sk-ant-api03-ZZZZZZZZZZZZ path " + filepath.Join(r.home, "src") + " Bearer abcdefghijklmnopqrstu password=pw1 ok"
	out := r.text(in)
	for _, gone := range []string{"abc123", "ZZZZZZZZZZZZ", r.home, "abcdefghijklmnopqrstu", "pw1"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q survived: %s", gone, out)
		}
	}
	for _, kept := range []string{"token=[REDACTED]&x=1", filepath.Join("~", "src"), "Bearer [REDACTED]", " ok"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q missing: %s", kept, out)
		}
	}
	if (redactor{}).text("plain words") != "plain words" {
		t.Error("text without secrets is unchanged")
	}
	if _, err := os.UserHomeDir(); err == nil && newRedactor().home == "" {
		t.Error("the home folder is redacted when there is one")
	}
}

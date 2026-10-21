package scrub

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/security"
)

const fakeToken = "ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"

var (
	once sync.Once
	sc   *security.Scanner
)

func scanner(t *testing.T) *security.Scanner {
	once.Do(func() {
		s, err := security.NewScanner()
		if err != nil {
			t.Fatal(err)
		}
		sc = s
	})
	return sc
}

func TestTextRedactsFindingsAndKeepsTheRest(t *testing.T) {
	in := "before\nexport TOKEN=" + fakeToken + "\nafter " + fakeToken + "\n"
	out, fs, err := Text(context.Background(), scanner(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, fakeToken) || !strings.Contains(out, "before") || !strings.Contains(out, "after [REDACTED:") || len(fs) < 2 {
		t.Fatalf("out %q findings %d", out, len(fs))
	}
	if n := Rules(fs); len(n) == 0 {
		t.Fatal("no rule counts")
	}
	clean := "func main() {}\n"
	if out, fs, err := Text(context.Background(), scanner(t), clean); err != nil || out != clean || len(fs) != 0 {
		t.Fatalf("clean text changed: %q %v %v", out, fs, err)
	}
}

func TestTextFailsClosedWhenTheScanCannotRun(t *testing.T) {
	small, _ := security.NewScanner(security.WithMaxBytes(16))
	if out, _, err := Text(context.Background(), small, strings.Repeat("x", 100)); err == nil || out != "" {
		t.Fatalf("an unscannable text came back: %q %v", out, err)
	}
}

func TestIdentityReplacesProfileMachineAndUser(t *testing.T) {
	id := NewIdentity(`C:\Users\Kyle Maguire`, "DESKTOP-KYLE15", "kmaguire", "kyle")
	in := `open C:\Users\Kyle Maguire\AppData\x and c:/users/kyle maguire/y and {"p":"C:\\Users\\Kyle Maguire\\z"} on desktop-kyle15 by KMaguire (kyle)`
	out, counts := id.ApplyCount(in)
	for _, bad := range []string{"Kyle Maguire", "kyle maguire", "DESKTOP", "desktop-kyle15", "kmaguire", "KMaguire", "(kyle)"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q still in %q", bad, out)
		}
	}
	if counts[TokenProfile] != 3 || counts[TokenMachine] != 1 || counts[TokenUser] != 2 {
		t.Errorf("counts %v in %q", counts, out)
	}
	// short names would shred ordinary words and are ignored
	short := NewIdentity("", "", "me", "al")
	if got := short.Apply("some meaningful alarm"); got != "some meaningful alarm" {
		t.Errorf("short names replaced: %q", got)
	}
	var nilID *Identity
	if nilID.Apply("x") != "x" {
		t.Error("nil identity")
	}
}

func TestSensitiveName(t *testing.T) {
	for _, n := range []string{"license.token", "OPENAI_API_KEY", "db.password", "client_secret", "x.apikey", "integration.pass", "auth_header", "key"} {
		if !SensitiveName(n) {
			t.Errorf("%s not sensitive", n)
		}
	}
	for _, n := range []string{"devloop:prj_1", "backup.dir", "route.cheap.prov_1", "skills.manual_accept_repo"} {
		if SensitiveName(n) {
			t.Errorf("%s flagged", n)
		}
	}
}

// A secret that appears twice on one line was masked once: both findings resolved to the first copy (audit 2026-10-16, M17).
func TestTextMasksEveryCopyOfARepeatedSecret(t *testing.T) {
	for name, in := range map[string]string{
		"same line":   "first=" + fakeToken + " second=" + fakeToken + " third=" + fakeToken,
		"json string": `{"a":"` + fakeToken + `","b":"` + fakeToken + `"}`,
		"spread":      "x " + fakeToken + "\nthen\n" + fakeToken + " and " + fakeToken + "\n",
	} {
		out, fs, err := Text(context.Background(), scanner(t), in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out, fakeToken) || strings.Contains(out, fakeToken[:20]) {
			t.Errorf("%s: a copy of the secret is left: %q", name, out)
		}
		if len(fs) == 0 {
			t.Errorf("%s: no findings", name)
		}
	}
}

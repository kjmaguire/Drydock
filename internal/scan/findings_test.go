package scan_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/scan"
)

// calcGo is the file the stand-in opengrep's SARIF points into (calc.go lines 3 and 5-7).
const calcGo = "package calc\n\nimport \"math/rand\"\n\nfunc Run(cmd string) error {\n\treturn exec.Command(\"sh\", \"-c\", cmd).Run()\n}\n"

func scanAll(t *testing.T, dir string) scan.Report {
	t.Helper()
	run := scan.Start(context.Background(), scan.Options{ID: "scn_fp", ProjectID: "prj_1", By: "human:kyle", Dir: dir, RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner"), Opengrep: tool("opengrep")}})
	return run.Wait()
}

func byEngine(rep scan.Report, id scan.EngineID) []scan.Finding {
	var out []scan.Finding
	for _, f := range rep.Findings {
		if f.Engine == id {
			out = append(out, f)
		}
	}
	return out
}

// Decision P97: every finding of every engine carries a fingerprint; it is made from the masked line, so neither the value of
// the secret nor where the line moves to changes it, and a code finding's changes when its line is edited.
func TestEveryFindingHasAStableFingerprintThatNeverCarriesTheSecret(t *testing.T) {
	dir := tree(t)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte(calcGo), 0o644)
	rep := scanAll(t, dir)
	if len(rep.Findings) != 4 {
		t.Fatalf("%+v", rep.Findings)
	}
	seen := map[string]bool{}
	for _, f := range rep.Findings {
		if !scan.ValidFingerprint(f.Fingerprint) || seen[f.Fingerprint] {
			t.Fatalf("fingerprint %q of %+v", f.Fingerprint, f)
		}
		seen[f.Fingerprint] = true
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), fakeKey) {
		t.Fatal("the report carries the secret")
	}
	secret := byEngine(rep, scan.Secrets)[0]
	// the fingerprint is the masked line's: the same with the value replaced by the mask the app's scanner uses
	if want := scan.Fingerprint(scan.Secrets, secret.Rule, "notes.txt", "aws_access_key_id = [REDACTED:"+secret.Rule+"]", 1); secret.Fingerprint != want {
		t.Fatalf("%s, want %s", secret.Fingerprint, want)
	}
	deps := byEngine(rep, scan.Deps)[0]
	if deps.Fixed != "0.3.8" || len(deps.Unfixed) != 0 {
		t.Fatalf("the version that fixes the advisories: %+v", deps)
	}
	code := byEngine(rep, scan.SAST)

	// the line moves down two lines (and is indented): the same fingerprints, new line numbers
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("# keys\n\n   aws_access_key_id = "+fakeKey+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte("// moved\n"+calcGo), 0o644)
	moved := scanAll(t, dir)
	if s := byEngine(moved, scan.Secrets)[0]; s.Fingerprint != secret.Fingerprint || s.Line != 3 {
		t.Fatalf("moved secret %+v, was %+v", s, secret)
	}
	if d := byEngine(moved, scan.Deps)[0]; d.Fingerprint != deps.Fingerprint {
		t.Fatalf("deps %+v %+v", d, deps)
	}
	// the stand-in reports the same line numbers, which now hold other text: the code findings are other findings
	for i, f := range byEngine(moved, scan.SAST) {
		if f.Fingerprint == code[i].Fingerprint {
			t.Fatalf("a code finding whose line now reads differently kept its fingerprint: %+v", f)
		}
	}
	// another value of the key on the same line: the same fingerprint (the value is never part of it)
	other := strings.Replace(fakeKey, "KQ5Z", "MR7P", 1)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("aws_access_key_id = "+other+"\n"), 0o644)
	if s := byEngine(scanAll(t, dir), scan.Secrets)[0]; s.Fingerprint != secret.Fingerprint {
		t.Fatalf("the secret's value changed the fingerprint: %+v", s)
	}
}

// The code around a finding is read from the folder when asked: the planted key never appears in it (the finding's own
// line, and a code finding's lines with the key on them), and a file outside the folder, by a path or a link, is refused.
func TestTheCodeAroundAFindingMasksTheSecretAndStaysInTheFolder(t *testing.T) {
	dir := tree(t)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one\ntwo\nthree\naws_access_key_id = "+fakeKey+"\nfive\nsix\nseven\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte("package calc\n\nimport \"math/rand\" // "+fakeKey+"\n\nfunc Run(cmd string) error {\n\treturn exec.Command(\"sh\", \"-c\", cmd).Run()\n}\n"), 0o644)
	rep := scanAll(t, dir)
	redacted := 0
	red := func(s string) string { redacted++; return s }
	var secret scan.Finding
	for _, f := range byEngine(rep, scan.Secrets) {
		if f.File == "notes.txt" {
			secret = f
		}
	}
	cx, err := scan.ReadContext(context.Background(), dir, secret, 0, red)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cx)
	if strings.Contains(string(b), fakeKey) || strings.Contains(string(b), "KQ5ZV4") {
		t.Fatalf("the context carries the secret: %s", b)
	}
	if len(cx.Lines) != 5 || cx.Lines[0].N != 2 || cx.Lines[4].N != 6 || !cx.Lines[2].Hit || !cx.Lines[2].Masked || cx.Lines[2].Text != "aws_access_key_id = [REDACTED:secret]" || cx.Lines[1].Text != "three" || cx.Changed || redacted < 5 {
		t.Fatalf("%+v (redacted %d)", cx, redacted)
	}
	if !strings.Contains(cx.Advice.Meaning, "AWS access key") || len(cx.Advice.Steps) < 3 {
		t.Fatalf("advice %+v", cx.Advice)
	}
	// a code finding whose lines hold the key: masked there too
	for _, f := range byEngine(rep, scan.SAST) {
		cx, err := scan.ReadContext(context.Background(), dir, f, 3, nil)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(cx); strings.Contains(string(b), fakeKey) || len(cx.Lines) == 0 {
			t.Fatalf("%s", b)
		}
	}

	// the line is edited since the scan: the context says so, and the old line's secret is not shown either
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one\ntwo\nthree\naws key moved elsewhere\nfive\n"), 0o644)
	cx, _ = scan.ReadContext(context.Background(), dir, secret, 1, nil)
	if !cx.Changed || len(cx.Lines) != 3 || cx.Lines[1].Text != "[REDACTED:"+secret.Rule+"]" {
		t.Fatalf("changed: %+v", cx)
	}

	// outside the folder: a relative path out of it, an absolute path, and a link that leads out
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("secret plans\n"), 0o644)
	for _, file := range []string{"../outside.txt", "src/../../outside.txt", filepath.ToSlash(outside)} {
		f := secret
		f.File = file
		if _, err := scan.ReadContext(context.Background(), dir, f, 0, nil); !errors.Is(err, scan.ErrOutsideFolder) {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err == nil {
		f := secret
		f.File = "link.txt"
		if _, err := scan.ReadContext(context.Background(), dir, f, 0, nil); !errors.Is(err, scan.ErrOutsideFolder) {
			t.Fatalf("a link out of the folder: %v", err)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	// a binary file and one over the cap: no lines, a note
	for _, file := range []string{"blob.bin", "big.txt"} {
		f := secret
		f.File = file
		cx, err := scan.ReadContext(context.Background(), dir, f, 0, nil)
		if err != nil || len(cx.Lines) != 0 || !strings.Contains(cx.Note, "not shown") {
			t.Fatalf("%s: %+v %v", file, cx, err)
		}
	}
	// a dependency finding names a package, not a line
	cx, err = scan.ReadContext(context.Background(), dir, byEngine(rep, scan.Deps)[0], 0, nil)
	if err != nil || len(cx.Lines) != 0 || !strings.Contains(cx.Note, "names a package") || !strings.Contains(strings.Join(cx.Advice.Steps, " "), "0.3.8") {
		t.Fatalf("%+v %v", cx, err)
	}
}

// "Open the file" (decision P97): the whole file from line 1 with every secret masked, also far from the finding, the
// finding's lines marked; at most MaxFileLines lines (More says there are more); a dependency's manifest whole; the same
// folder rules as the context.
func TestTheWholeFileOfAFindingMasksEverySecretAndStaysInTheFolder(t *testing.T) {
	dir := tree(t)
	var b strings.Builder
	b.WriteString("one\ntwo\nthree\naws_access_key_id = " + fakeKey + "\n")
	for i := 5; i <= 60; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("again = " + fakeKey + "\nlast\n")
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte(calcGo), 0o644)
	rep := scanAll(t, dir)
	var secret scan.Finding
	for _, f := range byEngine(rep, scan.Secrets) {
		if f.File == "notes.txt" && f.Line == 4 {
			secret = f
		}
	}
	if secret.Line != 4 {
		t.Fatalf("%+v", rep.Findings)
	}
	redacted := 0
	fx, err := scan.ReadFile(context.Background(), dir, secret, func(s string) string { redacted++; return s })
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(fx)
	if strings.Contains(string(raw), fakeKey) || strings.Contains(string(raw), "KQ5ZV4") {
		t.Fatalf("the file carries the secret: %s", raw)
	}
	if len(fx.Lines) != 62 || fx.More || fx.Lines[0].N != 1 || fx.Lines[0].Text != "one" || fx.Lines[0].Hit || !fx.Lines[3].Hit || !fx.Lines[3].Masked ||
		fx.Lines[60].Text != "again = [REDACTED:secret]" || !fx.Lines[60].Masked || fx.Lines[60].Hit || fx.Lines[61].Text != "last" || redacted < 62 {
		t.Fatalf("%s (redacted %d)", raw, redacted)
	}
	for _, l := range fx.Lines {
		if l.Hit != (l.N == 4) {
			t.Fatalf("line %d marked %v", l.N, l.Hit)
		}
	}
	// a dependency finding: its manifest, whole
	dep := byEngine(rep, scan.Deps)[0]
	os.WriteFile(filepath.Join(dir, dep.File), []byte("module calc\n\nrequire golang.org/x/text v0.3.7\n"), 0o644)
	fx, err = scan.ReadFile(context.Background(), dir, dep, nil)
	if err != nil || fx.Note != "" || len(fx.Lines) != 3 || fx.Lines[2].Text != "require golang.org/x/text v0.3.7" || fx.Lines[2].Hit {
		t.Fatalf("%+v %v", fx, err)
	}
	// a file longer than the cap: its first MaxFileLines lines, and More
	long := strings.Repeat("x\n", scan.MaxFileLines+5)
	os.WriteFile(filepath.Join(dir, "long.txt"), []byte(long), 0o644)
	f := scan.Finding{Engine: scan.SAST, Rule: "r", File: "long.txt", Line: 3}
	fx, err = scan.ReadFile(context.Background(), dir, f, nil)
	if err != nil || len(fx.Lines) != scan.MaxFileLines || !fx.More || !fx.Lines[2].Hit {
		t.Fatalf("%d lines, more %v, %v", len(fx.Lines), fx.More, err)
	}
	// a line past the end of the file now: the file is shown and says it changed
	f.Line = scan.MaxFileLines + 50
	fx, err = scan.ReadFile(context.Background(), dir, f, nil)
	if err != nil || !fx.Changed || len(fx.Lines) != scan.MaxFileLines || !strings.Contains(fx.Note, "when the scan ran") {
		t.Fatalf("%+v %v", fx.Note, err)
	}
	// outside the folder: refused
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("secret plans\n"), 0o644)
	for _, file := range []string{"../outside.txt", filepath.ToSlash(outside)} {
		f := secret
		f.File = file
		if _, err := scan.ReadFile(context.Background(), dir, f, nil); !errors.Is(err, scan.ErrOutsideFolder) {
			t.Fatalf("%s: %v", file, err)
		}
	}
}

// Plain words: a secret by its rule family, a dependency by its advisories and its fix, a built-in Code rule by its own
// message and CWE (also under the name Opengrep gives it), and a rule Drydock does not know by its finding's title.
func TestAdviceSaysWhatAFindingMeansAndWhatToDo(t *testing.T) {
	gh := scan.AdviceFor(scan.Finding{Engine: scan.Secrets, Rule: "github-fine-grained-pat", File: ".env"})
	if !strings.Contains(gh.Meaning, "GitHub token") || !strings.Contains(gh.Steps[0], "Revoke") {
		t.Fatalf("%+v", gh)
	}
	pk := scan.AdviceFor(scan.Finding{Engine: scan.Secrets, Rule: "private-key", File: "id_rsa"})
	if !strings.Contains(pk.Meaning, "private key") || !strings.Contains(pk.Steps[0], "key pair") {
		t.Fatalf("%+v", pk)
	}
	gen := scan.AdviceFor(scan.Finding{Engine: scan.Secrets, Rule: "generic-api-key", File: "config.js"})
	if !strings.Contains(gen.Meaning, "check it first") {
		t.Fatalf("%+v", gen)
	}
	unknown := scan.AdviceFor(scan.Finding{Engine: scan.Secrets, Rule: "zz-new-rule", Title: "Found a Zz token"})
	if !strings.Contains(unknown.Meaning, "Found a Zz token") {
		t.Fatalf("%+v", unknown)
	}
	dep := scan.AdviceFor(scan.Finding{Engine: scan.Deps, Rule: "npm", Package: "sharp", Version: "0.32.0", IDs: []string{"GHSA-1", "GHSA-2"}, Fixed: "0.32.6", Unfixed: []string{"GHSA-2"}, File: "package-lock.json"})
	if !strings.Contains(dep.Steps[0], "Upgrade sharp to 0.32.6") || !strings.Contains(dep.Steps[1], "GHSA-2 name no fixed version") || strings.Join(dep.Refs, ",") != "GHSA-1,GHSA-2" {
		t.Fatalf("%+v", dep)
	}
	code := scan.AdviceFor(scan.Finding{Engine: scan.SAST, Rule: "home.kyle.data.rules.builtin-1a2b3c.drydock.drydock.go.shell-command-built-from-data", File: "run.go", Line: 9})
	if !strings.HasPrefix(code.Meaning, "This command line is handed to a shell") || !strings.Contains(code.Steps[0], "Run the program directly") || strings.Join(code.Refs, ",") != "CWE-78: OS Command Injection" {
		t.Fatalf("%+v", code)
	}
	third := scan.AdviceFor(scan.Finding{Engine: scan.SAST, Rule: "x.rules.third_party.0xdea-c.raptor-command-injection", File: "a.c", Line: 3})
	if third.Meaning == "" || len(third.Refs) == 0 {
		t.Fatalf("a third-party rule's message and CWE: %+v", third)
	}
	own := scan.AdviceFor(scan.Finding{Engine: scan.SAST, Rule: "my.rules.no-todo", Title: "No TODO in shipped code", File: "a.go", Line: 2})
	if own.Meaning != "No TODO in shipped code" || !strings.Contains(own.Steps[0], "a.go:2") {
		t.Fatalf("%+v", own)
	}
}

// A dismissed finding is marked with who and when, and left out of the counts, its engine's count and the summary; a
// finding no longer dismissed counts again.
func TestDismissedFindingsAreMarkedAndLeftOutOfTheCounts(t *testing.T) {
	dir := tree(t)
	os.WriteFile(filepath.Join(dir, "calc.go"), []byte(calcGo), 0o644)
	rep := scanAll(t, dir)
	secret := byEngine(rep, scan.Secrets)[0]
	if rep.Counts.Total != 4 || rep.Counts.High != 3 || !strings.Contains(rep.Summary, "1 secret") {
		t.Fatalf("%+v %s", rep.Counts, rep.Summary)
	}
	d := map[string]scan.Dismissal{secret.Fingerprint: {Fingerprint: secret.Fingerprint, Reason: scan.ReasonNotReal, By: "human:kyle", At: 1234}}
	scan.ApplyDismissals(&rep, d)
	var got scan.Finding
	for _, f := range rep.Findings {
		if f.Fingerprint == secret.Fingerprint {
			got = f
		} else if f.Dismissed {
			t.Fatalf("%+v", f)
		}
	}
	if !got.Dismissed || got.DismissedBy != "human:kyle" || got.DismissedAt != 1234 || got.DismissReason != scan.ReasonNotReal {
		t.Fatalf("%+v", got)
	}
	if rep.Counts.Total != 3 || rep.Counts.High != 2 || rep.Dismissed != 1 || engine(rep, scan.Secrets).Findings != 0 || engine(rep, scan.Secrets).Dismissed != 1 {
		t.Fatalf("%+v %+v", rep.Counts, rep.Engines)
	}
	if !strings.Contains(rep.Summary, "no secrets") || !strings.Contains(rep.Summary, "1 finding you dismissed is not counted") {
		t.Fatalf("%s", rep.Summary)
	}
	// applied twice: nothing counted twice
	scan.ApplyDismissals(&rep, d)
	if rep.Counts.Total != 3 || rep.Dismissed != 1 {
		t.Fatalf("%+v", rep.Counts)
	}
	scan.ApplyDismissals(&rep, nil)
	if rep.Counts.Total != 4 || rep.Counts.High != 3 || rep.Dismissed != 0 || engine(rep, scan.Secrets).Findings != 1 || strings.Contains(rep.Summary, "dismissed") {
		t.Fatalf("restored: %+v %s", rep.Counts, rep.Summary)
	}
}

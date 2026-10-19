package scan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// fakeKey matches gitleaks' aws-access-token rule. It is made up for this test and is not a credential.
const fakeKey = "AKIA2W7X3KQ5ZV4YBN6T"

func notFound(string) (string, error) { return "", errors.New("not found") }

// tool starts this test binary again as the labelled stand-in of name (scantest).
func tool(name string, opts ...string) scan.Tool {
	return scan.Tool{Path: os.Args[0], Args: scantest.Args(name, opts...)}
}

// tree makes a working tree with a planted fake key in a text file, and the same key in places the secrets engine must not
// read: the .git folder, a binary file, and a file over the size cap.
func tree(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	for _, d := range []string{".git/objects", "node_modules/x", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		"notes.txt":               []byte("aws_access_key_id = " + fakeKey + "\n"),
		"src/main.go":             []byte("package main\n\nfunc main() {}\n"),
		".git/objects/pack":       []byte("key " + fakeKey + "\n"),
		"node_modules/x/index.js": []byte("// " + fakeKey + "\n"),
		"blob.bin":                append([]byte{0, 1, 2, 0}, []byte(fakeKey)...),
		"big.txt":                 append(bytes.Repeat([]byte("x"), 2<<20+1), []byte("\n"+fakeKey+"\n")...),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rules(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "rules")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "go.yaml"), []byte("rules: []\n"), 0o644)
	return d
}

func engine(rep scan.Report, id scan.EngineID) scan.EngineResult {
	for _, e := range rep.Engines {
		if e.ID == id {
			return e
		}
	}
	return scan.EngineResult{}
}

func TestTheSecretsEngineFindsAPlantedKeyAndNeverKeepsItsValue(t *testing.T) {
	dir := tree(t)
	redacted := 0
	run := scan.Start(context.Background(), scan.Options{ID: "scn_1", ProjectID: "prj_1", By: "human:kyle", Dir: dir, Paths: scan.Paths{LookPath: notFound},
		Redact: func(s string) string { redacted++; return s }})
	rep := run.Wait()
	if rep.Status != scan.StatusDone || rep.FinishedAt == 0 {
		t.Fatalf("%+v", rep)
	}
	sec := engine(rep, scan.Secrets)
	if sec.Status != scan.Done || sec.Findings != 1 || sec.Total < 3 || sec.Done != sec.Total || sec.Version == "" {
		t.Fatalf("secrets: %+v", sec)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("%+v", rep.Findings)
	}
	f := rep.Findings[0]
	if f.Engine != scan.Secrets || f.Rule != "aws-access-token" || f.File != "notes.txt" || f.Line != 1 || f.Severity != scan.High || f.Title == "" {
		t.Fatalf("%+v", f)
	}
	b, _ := json.Marshal(rep)
	if bytes.Contains(b, []byte(fakeKey)) || bytes.Contains(b, []byte(fakeKey[4:12])) {
		t.Fatal("the report carries the secret's value")
	}
	if redacted == 0 {
		t.Fatal("titles are not passed through the redactor")
	}
	// the tools that are not installed: skipped, with the install hint, and named in the summary; never a pass
	for _, id := range []scan.EngineID{scan.Deps, scan.SAST} {
		e := engine(rep, id)
		if e.Status != scan.Skipped || !strings.Contains(e.Reason, "not installed") || !strings.Contains(e.Reason, "https://github.com/") {
			t.Fatalf("%s: %+v", id, e)
		}
	}
	if !strings.HasPrefix(rep.Summary, "Scanned in ") || !strings.Contains(rep.Summary, "1 secret") || !strings.Contains(rep.Summary, "Dependencies engine was skipped") || !strings.Contains(rep.Summary, "Code engine was skipped") {
		t.Fatalf("%q", rep.Summary)
	}
	if rep.Counts.High != 1 || rep.Counts.Total != 1 {
		t.Fatalf("%+v", rep.Counts)
	}
}

func TestTheExternalEnginesRunThroughTheStandInsAndTheirStatesAreReported(t *testing.T) {
	dir := tree(t)
	var mu sync.Mutex
	var seen []string
	run := scan.Start(context.Background(), scan.Options{ID: "scn_2", Dir: dir, RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--sleep-ms=600"), Opengrep: tool("opengrep", "--sleep-ms=600")},
		OnChange: func(r scan.Report, changed scan.EngineID) {
			mu.Lock()
			seen = append(seen, string(changed)+":"+string(engine(r, scan.Deps).Status)+"/"+string(engine(r, scan.SAST).Status))
			mu.Unlock()
		}})
	// while the stand-ins sleep, the engines read as running
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := run.Snapshot()
		if engine(s, scan.Deps).Status == scan.Running && engine(s, scan.SAST).Status == scan.Running {
			if s.Status != scan.StatusRunning {
				t.Fatalf("%+v", s)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the engines never read as running: %+v", s.Engines)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rep := run.Wait()
	deps, sast := engine(rep, scan.Deps), engine(rep, scan.SAST)
	if deps.Status != scan.Done || deps.Findings != 1 || !strings.Contains(deps.Version, "standin") || deps.StartedAt == 0 || deps.FinishedAt < deps.StartedAt {
		t.Fatalf("deps: %+v", deps)
	}
	if sast.Status != scan.Done || sast.Findings != 2 || !strings.Contains(sast.Version, "standin") {
		t.Fatalf("sast: %+v", sast)
	}
	if len(rep.Findings) != 4 || rep.Counts.High != 3 || rep.Counts.Medium != 1 || rep.Counts.Total != 4 {
		t.Fatalf("%d findings, counts %+v", len(rep.Findings), rep.Counts)
	}
	// sorted by severity, then file: the high ones first (calc.go before go.mod before notes.txt), the medium one last
	var order []string
	for _, f := range rep.Findings {
		order = append(order, string(f.Severity)+":"+f.File)
	}
	if strings.Join(order, " ") != "high:calc.go high:go.mod high:notes.txt medium:calc.go" {
		t.Fatalf("%v", order)
	}
	var dep, code scan.Finding
	for _, f := range rep.Findings {
		switch {
		case f.Engine == scan.Deps:
			dep = f
		case f.Engine == scan.SAST && f.Severity == scan.High:
			code = f
		}
	}
	if dep.Package != "golang.org/x/text" || dep.Version != "0.3.7" || dep.Rule != "Go" || strings.Join(dep.IDs, ",") != "CVE-2021-38561,GO-2021-0113" || dep.File != "go.mod" || !strings.Contains(dep.Title, "GO-2021-0113") {
		t.Fatalf("%+v", dep)
	}
	if code.Rule != "go.lang.security.audit.dangerous-exec-command" || code.File != "calc.go" || code.Line != 5 || code.EndLine != 7 || !strings.HasPrefix(code.Title, "Detected non-static command") {
		t.Fatalf("%+v", code)
	}
	if !strings.Contains(rep.Summary, "1 vulnerable package") || !strings.Contains(rep.Summary, "2 code findings") {
		t.Fatalf("%q", rep.Summary)
	}
	mu.Lock()
	defer mu.Unlock()
	// one call per engine state change (3 engines, running then finished) and one for the end of the scan
	if len(seen) != 7 || seen[len(seen)-1] != ":done/done" {
		t.Fatalf("%v", seen)
	}
	perEngine := map[string]int{}
	for _, x := range seen {
		perEngine[strings.SplitN(x, ":", 2)[0]]++
	}
	if perEngine["secrets"] != 2 || perEngine["deps"] != 2 || perEngine["sast"] != 2 || perEngine[""] != 1 {
		t.Fatalf("%v", perEngine)
	}
}

func TestCancelDuringTheVersionProbeIsCancelledNotErrored(t *testing.T) {
	// the stand-in sleeps before answering --version too, so Cancel lands inside the probe
	run := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--sleep-ms=20000"), Opengrep: tool("opengrep", "--sleep-ms=20000")}})
	deadline := time.Now().Add(5 * time.Second)
	for engine(run.Snapshot(), scan.Deps).Status != scan.Running || engine(run.Snapshot(), scan.SAST).Status != scan.Running {
		if time.Now().After(deadline) {
			t.Fatalf("the engines never read as running: %+v", run.Snapshot().Engines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	run.Cancel()
	rep := run.Wait()
	for _, id := range []scan.EngineID{scan.Deps, scan.SAST} {
		if e := engine(rep, id); e.Status != scan.Cancelled || e.Reason != "" {
			t.Fatalf("%s: %+v", id, e)
		}
	}
}

func TestAFolderTheSecretsEngineCannotReadIsCountedAndSaidNeverHidden(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a user that permissions can refuse")
	}
	dir := tree(t)
	locked := filepath.Join(dir, "locked")
	os.MkdirAll(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "x.txt"), []byte("key = "+fakeKey+"\n"), 0o644)
	os.Chmod(locked, 0o000)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	rep := scan.Start(context.Background(), scan.Options{Dir: dir, Paths: scan.Paths{LookPath: notFound}}).Wait()
	e := engine(rep, scan.Secrets)
	if e.Status != scan.Done || e.Unread != 1 || !strings.Contains(e.Reason, "1 folder(s) or file(s) could not be read") {
		t.Fatalf("%+v", e)
	}
	if !strings.Contains(rep.Summary, "Secrets engine could not read 1 folder(s) or file(s)") {
		t.Fatalf("%q", rep.Summary)
	}
}

func TestAnUnreadableTreeIsAnErrorNotAPass(t *testing.T) {
	rep := scan.Start(context.Background(), scan.Options{Dir: filepath.Join(t.TempDir(), "gone"), Paths: scan.Paths{LookPath: notFound}}).Wait()
	if e := engine(rep, scan.Secrets); e.Status != scan.Errored || !strings.Contains(e.Reason, "could not read the tree") {
		t.Fatalf("%+v", e)
	}
}

func TestAToolThatBreaksIsErroredAndNamedNeverAPass(t *testing.T) {
	run := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--fail"), Opengrep: tool("opengrep", "--fail")}})
	rep := run.Wait()
	for _, id := range []scan.EngineID{scan.Deps, scan.SAST} {
		e := engine(rep, id)
		if e.Status != scan.Errored || !strings.Contains(e.Reason, "exited 2") || !strings.Contains(e.Reason, "told to fail") {
			t.Fatalf("%s: %+v", id, e)
		}
	}
	if !strings.Contains(rep.Summary, "Dependencies engine failed") || !strings.Contains(rep.Summary, "Code engine failed") {
		t.Fatalf("%q", rep.Summary)
	}
}

func TestCancelEndsTheScanAsCancelled(t *testing.T) {
	run := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--sleep-ms=20000"), Opengrep: tool("opengrep", "--sleep-ms=20000")}})
	time.Sleep(300 * time.Millisecond)
	run.Cancel()
	select {
	case <-run.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the scan did not end after Cancel")
	}
	rep := run.Snapshot()
	if rep.Status != scan.StatusCancelled || !strings.HasPrefix(rep.Summary, "Cancelled after") {
		t.Fatalf("%+v", rep)
	}
	for _, id := range []scan.EngineID{scan.Deps, scan.SAST} {
		if e := engine(rep, id); e.Status != scan.Cancelled {
			t.Fatalf("%s: %+v", id, e)
		}
	}
}

func TestWithoutARulesFolderTheCodeEngineIsSkippedAndSaysSo(t *testing.T) {
	run := scan.Start(context.Background(), scan.Options{Dir: tree(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--empty"), Opengrep: tool("opengrep")}})
	rep := run.Wait()
	if e := engine(rep, scan.SAST); e.Status != scan.Skipped || !strings.Contains(e.Reason, "No rules folder chosen") {
		t.Fatalf("%+v", e)
	}
	if e := engine(rep, scan.Deps); e.Status != scan.Done || e.Findings != 0 {
		t.Fatalf("%+v", e)
	}
	if !strings.Contains(rep.Summary, "no vulnerable packages") {
		t.Fatalf("%q", rep.Summary)
	}
	// the reason is read in a report, away from the field that fixes it: it says where that field is, and the summary
	// keeps only the first sentence
	reason := engine(rep, scan.SAST).Reason
	if !strings.Contains(reason, `"Tools on this computer"`) || !strings.Contains(reason, "where to get them") {
		t.Fatalf("the skipped reason does not say where to choose the rules: %q", reason)
	}
	if !strings.Contains(rep.Summary, "the Code engine was skipped (No rules folder chosen: the Code engine is skipped until one is)") || strings.Contains(rep.Summary, "Tools on this computer") {
		t.Fatalf("the summary should keep the first sentence only: %q", rep.Summary)
	}
}

// Every way the rules folder can be unusable ends the Code engine skipped with the same pointer, and a usable one does not.
func TestEveryUnusableRulesFolderSkipsTheCodeEngineWithWhereToChoose(t *testing.T) {
	empty := t.TempDir()
	for name, dir := range map[string]string{
		"relative":   "rules",
		"missing":    filepath.Join(t.TempDir(), "gone"),
		"no yaml":    empty,
		"not chosen": "",
	} {
		rep := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: dir,
			Paths: scan.Paths{OSV: tool("osv-scanner", "--empty"), Opengrep: tool("opengrep")}}).Wait()
		e := engine(rep, scan.SAST)
		if e.Status != scan.Skipped || !strings.HasSuffix(e.Reason, "where to get them.") {
			t.Errorf("%s: %+v", name, e)
		}
	}
	rep := scan.Start(context.Background(), scan.Options{Dir: tree(t), RulesDir: rules(t),
		Paths: scan.Paths{OSV: tool("osv-scanner", "--empty"), Opengrep: tool("opengrep")}}).Wait()
	if e := engine(rep, scan.SAST); e.Status != scan.Done || strings.Contains(e.Reason, "Tools on this computer") {
		t.Fatalf("a usable rules folder should run the Code engine: %+v", e)
	}
}

func TestDetectSaysWhatIsInstalledAndHowToGetTheRest(t *testing.T) {
	ctx := context.Background()
	rd := rules(t)
	have := scan.Detect(ctx, nil, nil, scan.Paths{OSV: tool("osv-scanner"), Opengrep: tool("opengrep")}, rd)
	if !have.Gitleaks.BuiltIn || !have.Gitleaks.Found || have.Gitleaks.Version == "" {
		t.Fatalf("%+v", have.Gitleaks)
	}
	if !have.OSV.Found || !strings.Contains(have.OSV.Version, "standin") || strings.HasPrefix(have.OSV.Version, "osv-scanner version") || have.OSV.Path == "" {
		t.Fatalf("%+v", have.OSV)
	}
	if !have.Opengrep.Found || !strings.Contains(have.Opengrep.Version, "standin") || !have.RulesOK || !have.Ready {
		t.Fatalf("%+v", have)
	}
	none := scan.Detect(ctx, nil, nil, scan.Paths{LookPath: notFound}, "")
	if none.OSV.Found || none.Opengrep.Found || none.Ready || none.RulesOK || !strings.Contains(none.OSV.Install, "osv-scanner") || !strings.Contains(none.Opengrep.Install, "opengrep") || none.RulesNote == "" {
		t.Fatalf("%+v", none)
	}
}

func TestParseSARIFAppliesTheRedactorAndKeepsLocations(t *testing.T) {
	fs, version, err := scan.ParseSARIF([]byte(scantest.SARIF), scan.SAST, "", func(s string) string { return strings.ReplaceAll(s, "input", "[hidden]") })
	if err != nil || version != "0.0.0-standin" || len(fs) != 2 {
		t.Fatal(err, version, len(fs))
	}
	if fs[0].Line != 3 || fs[0].Severity != scan.Medium || fs[1].Line != 5 || fs[1].Severity != scan.High || !strings.Contains(fs[1].Title, "[hidden]") {
		t.Fatalf("%+v", fs)
	}
	if _, _, err := scan.ParseSARIF([]byte("not json"), scan.SAST, "", nil); err == nil {
		t.Fatal("bad JSON must be an error")
	}
}

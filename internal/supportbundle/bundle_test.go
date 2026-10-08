package supportbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/scrub"
	"github.com/kjmaguire/drydock/internal/security"
)

const fakeToken = "ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"

var (
	once sync.Once
	sc   *security.Scanner
)

func scanner(t testing.TB) *security.Scanner {
	once.Do(func() {
		s, err := security.NewScanner()
		if err != nil {
			t.Fatal(err)
		}
		sc = s
	})
	return sc
}

func inputs() Inputs {
	return Inputs{
		AppVersion: "1.0.0", Build: "abc123", OS: "windows", OSVersion: "10.0.22631", InstallScope: "user", SchemaVersion: 80,
		Tools:       map[string]string{"git": "2.45.0"},
		Connections: []Connection{{ID: "claude", Version: "2.1.0", Verdict: "compatible"}},
		Settings: map[string]string{
			"devloop:prj_1":    `{"maxParallel":2}`,
			"license.token":    "payload.signature-value",
			"integration.pass": "correct horse battery staple",
			"note":             "my token is " + fakeToken,
		},
		Logs: []LogFile{
			{Name: "drydock.log", Text: "2026-01-01 start in C:\\Users\\Kyle Maguire\\AppData\\Local\\Drydock on DESKTOP-KYLE15 for kmaguire\nkey " + fakeToken + "\n"},
			{Name: "secrets.txt", Text: "must not be bundled"},
		},
		Events:   []EventRef{{Seq: 1, Type: "task.created", TS: 5, ProjectID: "prj_1", TaskID: "task_1"}},
		Watchdog: []WatchdogSignature{{Signal: "loop", Stage: 2, Count: 3, Last: 9}},
		DiskPath: "C:\\Users\\Kyle Maguire\\AppData\\Local\\Drydock", DiskFree: 10, DiskTotal: 100,
		EnvNames: []string{"PATH", "OPENAI_API_KEY", "HOME"},
	}
}

func opts(t testing.TB) Options {
	return Options{Scanner: scanner(t), Identity: scrub.NewIdentity(`C:\Users\Kyle Maguire`, "DESKTOP-KYLE15", "kmaguire")}
}

func fileMap(b Bundle) map[string]string {
	m := map[string]string{}
	for _, f := range b.Files {
		m[f.Name] = string(f.Data)
	}
	return m
}

func TestBundleIsAScrubbedAllowListWithAReport(t *testing.T) {
	b, err := Build(context.Background(), inputs(), opts(t))
	if err != nil {
		t.Fatal(err)
	}
	fm := fileMap(b)
	want := []string{"summary.json", "connections.json", "settings.json", "logs/drydock.log", "events.json", "watchdog.json", "environment.json", "scrub-report.json", "manifest.json"}
	for _, n := range want {
		if _, ok := fm[n]; !ok {
			t.Errorf("missing %s (have %v)", n, fm)
		}
	}
	if len(fm) != len(want) {
		t.Errorf("the bundle has files outside the allow-list: %d vs %d", len(fm), len(want))
	}
	for n, s := range fm {
		for _, bad := range []string{fakeToken, "payload.signature-value", "correct horse", `Kyle Maguire`, "DESKTOP-KYLE15", "kmaguire", "must not be bundled"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", n, bad)
			}
		}
	}
	if !strings.Contains(fm["logs/drydock.log"], "<USERPROFILE>") || !strings.Contains(fm["logs/drydock.log"], "<MACHINE>") || !strings.Contains(fm["logs/drydock.log"], "<USER>") ||
		!strings.Contains(fm["logs/drydock.log"], "[REDACTED:") {
		t.Errorf("log not scrubbed as expected: %q", fm["logs/drydock.log"])
	}
	if !strings.Contains(fm["settings.json"], `"devloop:prj_1"`) || !strings.Contains(fm["settings.json"], "REDACTED:secret-name") || !strings.Contains(fm["settings.json"], "REDACTED:github") {
		t.Errorf("settings: %s", fm["settings.json"])
	}
	// environment names only
	var env struct{ Names []string }
	json.Unmarshal([]byte(fm["environment.json"]), &env)
	if len(env.Names) != 3 || env.Names[0] != "HOME" {
		t.Errorf("env %v", env)
	}
	// events carry type, seq, time and ids only
	if strings.Contains(fm["events.json"], "payload") && !strings.Contains(fm["events.json"], "no payloads") {
		t.Error("events mention payloads")
	}
	var rep Report
	if err := json.Unmarshal([]byte(fm["scrub-report.json"]), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.TotalSecrets < 2 || rep.Notice == "" || len(rep.Refused) != 1 || rep.Refused[0].Name != "logs/secrets.txt" {
		t.Errorf("report %+v", rep)
	}
	var logRep FileReport
	for _, f := range rep.Files {
		if f.Name == "logs/drydock.log" {
			logRep = f
		}
	}
	if logRep.Identity[scrub.TokenMachine] != 1 || logRep.Identity[scrub.TokenUser] != 1 || logRep.Secrets != 1 {
		t.Errorf("log report %+v", logRep)
	}
	// the manifest hashes what was written
	var man Manifest
	json.Unmarshal([]byte(fm["manifest.json"]), &man)
	if len(man.Files) != len(want)-1 || man.RunIncluded != "" {
		t.Errorf("manifest %+v", man)
	}
}

func TestAFileThatCannotBeScannedIsRefusedNotIncluded(t *testing.T) {
	small, err := security.NewScanner(security.WithMaxBytes(512))
	if err != nil {
		t.Fatal(err)
	}
	in := inputs()
	in.Logs = []LogFile{{Name: "drydock.log", Text: strings.Repeat("a log line with ordinary text\n", 100)}}
	b, err := Build(context.Background(), in, Options{Scanner: small})
	if err != nil {
		t.Fatal(err)
	}
	fm := fileMap(b)
	if _, ok := fm["logs/drydock.log"]; ok {
		t.Error("an unscannable log was included")
	}
	found := false
	for _, r := range b.Report.Refused {
		if r.Name == "logs/drydock.log" && strings.Contains(r.Reason, "scan could not complete") {
			found = true
		}
	}
	if !found {
		t.Errorf("refusal not reported: %+v", b.Report.Refused)
	}
}

func TestRunInclusionIsScrubbedAndRecorded(t *testing.T) {
	in := inputs()
	in.Run = &RunInclusion{RunID: "run_1",
		Events: []RunEvent{{Seq: 1, Type: "agent.chunk", TS: 1, Data: `{"text":"` + fakeToken + `"}`}},
		Files:  []RunFile{{Name: "stream.jsonl", Content: "token " + fakeToken}, {Name: "../escape", Content: "x"}}}
	b, err := Build(context.Background(), in, opts(t))
	if err != nil {
		t.Fatal(err)
	}
	fm := fileMap(b)
	if _, ok := fm["run/run_1/events-with-payloads.json"]; !ok {
		t.Fatalf("run events missing: %v", b.Files)
	}
	if _, ok := fm["run/run_1/files/stream.jsonl"]; !ok {
		t.Fatal("run file missing")
	}
	for n, s := range fm {
		if strings.Contains(s, fakeToken) {
			t.Errorf("%s leaks", n)
		}
	}
	if b.Report.RunIncluded != "run_1" || len(b.Report.Refused) < 2 {
		t.Errorf("report %+v", b.Report)
	}
	var man Manifest
	json.Unmarshal([]byte(fm["manifest.json"]), &man)
	if man.RunIncluded != "run_1" {
		t.Errorf("manifest does not record the run: %+v", man)
	}
}

func TestWriteProducesAZip(t *testing.T) {
	b, err := Build(context.Background(), inputs(), opts(t))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "sub", "bundle.zip")
	if err := Write(b, p); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != len(b.Files) {
		t.Fatalf("%d vs %d", len(zr.File), len(b.Files))
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if bytes.Contains(data, []byte(fakeToken)) {
			t.Errorf("%s leaks", f.Name)
		}
	}
}

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/crashreport"
)

// What an earlier run printed as it died becomes a crash report at the next start: logged, listed, readable, in the support
// bundle, and this start's own crash file is not taken for one (review 2026-10-27).
func TestACrashOfAnEarlierRunIsReportedAtTheNextStart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.MkdirAll(crashreport.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	earlier := "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 7 [running]:\nmain.work()\n"
	if err := os.WriteFile(filepath.Join(crashreport.Dir(dir), "pending-20261027T090000Z-100.crash"), []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	own, err := crashreport.Arm(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer own.Disarm()
	c, err := Open(ctx, Config{DataDir: dir, User: "kyle", CrashOutput: own.Path()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(m string, p any, out any) {
		t.Helper()
		b, _ := json.Marshal(p)
		res, err := c.Call(ctx, m, b)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if out != nil {
			if err := json.Unmarshal(res, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var list struct {
		Reports []crashreport.Report `json:"reports"`
	}
	call("crash.list", map[string]any{}, &list)
	if len(list.Reports) != 1 || !strings.Contains(list.Reports[0].Summary, "index out of range") {
		t.Fatalf("crash.list %+v", list)
	}
	var read struct {
		Text string `json:"text"`
	}
	call("crash.read", map[string]string{"name": list.Reports[0].Name}, &read)
	if !strings.Contains(read.Text, "goroutine 7") {
		t.Fatalf("crash.read %q", read.Text)
	}
	if _, err := c.Call(ctx, "crash.read", json.RawMessage(`{"name":"../drydock.db"}`)); err == nil {
		t.Fatal("crash.read read a path")
	}
	var pv struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	call("supportbundle.preview", map[string]any{}, &pv)
	found := false
	for _, f := range pv.Files {
		found = found || f.Name == "crashes/"+list.Reports[0].Name
	}
	if !found {
		t.Fatalf("the support bundle has no crash report: %+v", pv.Files)
	}
	if _, err := os.Stat(own.Path()); err != nil {
		t.Fatalf("this start's own crash file was collected: %v", err)
	}
	call("crash.delete", map[string]string{"name": list.Reports[0].Name}, nil)
	call("crash.list", map[string]any{}, &list)
	if len(list.Reports) != 0 {
		t.Fatalf("after delete: %+v", list.Reports)
	}
}

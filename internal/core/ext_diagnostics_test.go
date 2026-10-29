package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

func diagCall(t *testing.T, c *Core, m string, p any, out any) {
	t.Helper()
	b, _ := json.Marshal(p)
	res, err := c.Call(context.Background(), m, b)
	if err != nil {
		t.Fatalf("%s: %v", m, err)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			t.Fatal(err)
		}
	}
}

// The diagnostics categories of decision P121 through the methods the Settings page calls: crash reports (local by default, off
// honoured at once, at the next start and by the support bundle) and usage counters (off by default; on, the window's method
// calls are counted by name and written to the local sink).
func TestDiagnosticsCategoriesAreStoredShownAndHonoured(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.MkdirAll(crashreport.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	earlier := "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 7 [running]:\n"
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

	var st DiagnosticsStatus
	diagCall(t, c, "diagnostics.status", map[string]any{}, &st)
	if st.CrashReports.Mode != CrashReportsLocal || st.CrashReports.Kept != 1 || st.UsageCounters == nil || st.UsageCounters.Enabled ||
		st.Content == nil || st.Content.Enabled || st.Telemetry || st.OTLP {
		t.Fatalf("the defaults: crash reports kept locally, usage counters and content export off: %+v", st)
	}
	if _, err := c.Call(ctx, "diagnostics.set", json.RawMessage(`{"crashReports":"upload"}`)); err == nil {
		t.Fatal("crash reports are local or off")
	}

	// usage counters: counted only once they are on, by method name only
	diagCall(t, c, "telemetry.status", map[string]any{}, nil)
	diagCall(t, c, "diagnostics.set", map[string]any{"usageCounters": true}, &st)
	if !st.UsageCounters.Enabled || st.UsageCounters.ChangedBy != "human:kyle" {
		t.Fatalf("%+v", st.UsageCounters)
	}
	for i := 0; i < 3; i++ {
		diagCall(t, c, "telemetry.status", map[string]any{}, nil)
	}
	diagCall(t, c, "crash.list", map[string]any{}, nil)
	diagCall(t, c, "diagnostics.status", map[string]any{}, &st) // writes what was counted
	var doc telemetry.Document
	diagCall(t, c, "telemetry.preview", map[string]any{"limit": 0}, &doc)
	uses := map[string]float64{}
	for _, m := range doc.Metrics {
		if m.Name == telemetry.MetricFeatureUses {
			f, _ := m.Attrs[telemetry.AttrFeature].(string)
			uses[f] += m.Value
		}
	}
	if uses["telemetry.status"] != 3 || uses["crash.list"] != 1 || uses["diagnostics.set"] != 0 {
		t.Fatalf("the uses counted since the switch was turned on: %v", uses)
	}
	if _, err := c.Call(ctx, "no.such.method", nil); err == nil {
		t.Fatal("an unknown method answered")
	}

	// crash reports off: no longer in a support bundle, and the next start keeps nothing a crash left
	diagCall(t, c, "diagnostics.set", map[string]any{"crashReports": "off"}, &st)
	if st.CrashReports.Mode != CrashReportsOff || st.CrashReports.ChangedBy != "human:kyle" || st.CrashReports.ChangedAt == 0 || st.CrashReports.Kept != 1 {
		t.Fatalf("%+v", st.CrashReports)
	}
	var pv struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	diagCall(t, c, "supportbundle.preview", map[string]any{}, &pv)
	for _, f := range pv.Files {
		if strings.HasPrefix(f.Name, "crashes/") {
			t.Fatalf("a crash report went into the bundle while crash reports are off: %s", f.Name)
		}
	}
	c.Close()

	if err := os.WriteFile(filepath.Join(crashreport.Dir(dir), "pending-20261028T090000Z-101.crash"), []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Open(ctx, Config{DataDir: dir, User: "kyle", CrashOutput: own.Path()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	diagCall(t, c, "diagnostics.status", map[string]any{}, &st)
	if st.CrashReports.Mode != CrashReportsOff || st.CrashReports.Kept != 1 || !st.UsageCounters.Enabled {
		t.Fatalf("both switches are stored, and the crash of the last run made no report: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(crashreport.Dir(dir), "pending-20261028T090000Z-101.crash")); !os.IsNotExist(err) {
		t.Fatalf("the crash output was left: %v", err)
	}
	if _, err := os.Stat(own.Path()); err != nil {
		t.Fatalf("this start's own crash file was removed: %v", err)
	}
	// on again: kept and in the bundle again
	diagCall(t, c, "diagnostics.set", map[string]any{"crashReports": "local", "usageCounters": false}, &st)
	if st.CrashReports.Mode != CrashReportsLocal || st.UsageCounters.Enabled {
		t.Fatalf("%+v", st)
	}
	diagCall(t, c, "supportbundle.preview", map[string]any{}, &pv)
	found := false
	for _, f := range pv.Files {
		found = found || strings.HasPrefix(f.Name, "crashes/")
	}
	if !found {
		t.Fatal("the kept report is in the bundle again")
	}
}

// Content export through its method: only with the acknowledgement, and shown in both statuses.
func TestContentExportThroughItsMethod(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(ctx, "telemetry.content.set", json.RawMessage(`{"enabled":true}`)); err == nil || !strings.Contains(err.Error(), "acknowledged") {
		t.Fatalf("content export without the acknowledgement: %v", err)
	}
	var ts telemetry.Status
	diagCall(t, c, "telemetry.content.set", map[string]any{"enabled": true, "acknowledged": true}, &ts)
	if !ts.Content.Enabled || ts.Content.ChangedBy != "human:kyle" {
		t.Fatalf("%+v", ts.Content)
	}
	var st DiagnosticsStatus
	diagCall(t, c, "diagnostics.status", map[string]any{}, &st)
	if st.Content == nil || !st.Content.Enabled {
		t.Fatalf("%+v", st)
	}
	diagCall(t, c, "telemetry.content.set", map[string]any{"enabled": false}, &ts)
	if ts.Content.Enabled {
		t.Fatalf("%+v", ts.Content)
	}
}

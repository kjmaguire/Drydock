package integrations_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
)

// Live checks against the real vendors (docs/integrations.md, "Live checks"). Every test above runs against fixtures written
// from the vendors' documentation; these check those fixtures against the real services. Each one runs only when its token
// is set (the value is never printed), reads only, and never imports an item or writes anything back:
//
//  1. Test connection: the token is accepted and the tools Drydock calls exist (one page, limit 1).
//  2. One sync (at most 20 pages of 50): every item the vendor returns parses into an item with an id, a title and a valid
//     reference, and no schema drift is reported.
//  3. The token is nowhere in the database, the body files or the events.
//
// DRYDOCK_LIVE_REPORT=<file> also writes what was found (tool names, counts, missing tools; no item text) as JSON for the
// docs. Run, for example:
//
//	DRYDOCK_LIVE_LINEAR_TOKEN=... go test ./internal/integrations -run TestLive -v
//
// Optional scope variables: DRYDOCK_LIVE_LINEAR_TEAM, DRYDOCK_LIVE_SENTRY_ORG and DRYDOCK_LIVE_SENTRY_PROJECT,
// DRYDOCK_LIVE_AIKIDO_WORKSPACE, DRYDOCK_LIVE_NOTION_QUERY; DRYDOCK_LIVE_<VENDOR>_BASE_URL for a self-hosted server.

type liveVendor struct {
	kind, env string
	config    map[string]string // connection config key -> environment variable
}

var liveVendors = []liveVendor{
	{integrations.KindLinear, "DRYDOCK_LIVE_LINEAR_TOKEN", map[string]string{"team": "DRYDOCK_LIVE_LINEAR_TEAM"}},
	{integrations.KindNotion, "DRYDOCK_LIVE_NOTION_TOKEN", map[string]string{"query": "DRYDOCK_LIVE_NOTION_QUERY"}},
	{integrations.KindSentry, "DRYDOCK_LIVE_SENTRY_TOKEN", map[string]string{"organization": "DRYDOCK_LIVE_SENTRY_ORG", "project": "DRYDOCK_LIVE_SENTRY_PROJECT"}},
	{integrations.KindAikido, "DRYDOCK_LIVE_AIKIDO_TOKEN", map[string]string{"workspace": "DRYDOCK_LIVE_AIKIDO_WORKSPACE"}},
}

// LiveReport is what one live check found. It never holds item text or the token.
type LiveReport struct {
	Kind          string   `json:"kind"`
	At            string   `json:"at"`
	OK            bool     `json:"ok"`
	ToolsFound    []string `json:"toolsFound"`
	ToolsMissing  []string `json:"toolsMissing"`
	WritesMissing []string `json:"writesMissing"`
	Pages         int      `json:"pages"`
	Items         int      `json:"items"`
	Drift         int      `json:"drift"`
	Steps         []string `json:"steps"`
}

func TestLiveLinear(t *testing.T) { liveCheck(t, liveVendors[0]) }
func TestLiveNotion(t *testing.T) { liveCheck(t, liveVendors[1]) }
func TestLiveSentry(t *testing.T) { liveCheck(t, liveVendors[2]) }
func TestLiveAikido(t *testing.T) { liveCheck(t, liveVendors[3]) }

func liveCheck(t *testing.T, v liveVendor) {
	token := os.Getenv(v.env)
	if token == "" {
		t.Skipf("set %s to check %s against the real service (read-only)", v.env, v.kind)
	}
	h := newHarness(t, func(o *integrations.Options) { o.Now = time.Now })
	ctx := context.Background()
	cfg := map[string]string{}
	for key, env := range v.config {
		if val := os.Getenv(env); val != "" {
			cfg[key] = val
		}
	}
	base := os.Getenv("DRYDOCK_LIVE_" + strings.ToUpper(v.kind) + "_BASE_URL")
	c := h.connect(v.kind, token, base, cfg)
	rep := LiveReport{Kind: v.kind, At: time.Now().UTC().Format(time.RFC3339)}
	defer func() { writeLiveReport(t, rep) }()

	tr, err := h.svc.Test(ctx, c.ID)
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	for _, s := range tr.Steps {
		rep.Steps = append(rep.Steps, s.Name+": "+map[bool]string{true: "ok", false: "FAILED"}[s.OK]+" "+s.Detail)
		t.Logf("%s: ok=%v %s", s.Name, s.OK, s.Detail)
	}
	rep.ToolsFound, rep.ToolsMissing, rep.WritesMissing = tr.ToolsFound, tr.ToolsMissing, tr.WritesMissing
	t.Logf("tools found %v; missing %v; optional write tools missing %v", tr.ToolsFound, tr.ToolsMissing, tr.WritesMissing)
	if !tr.OK {
		t.Fatalf("test connection failed: %s", tr.Summary)
	}

	res, err := h.svc.Sync(ctx, c.ID, integrations.SyncOpts{Force: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	rep.Pages = res.Pages
	items := h.items(c.ID)
	rep.Items = len(items)
	rep.Drift = len(h.events(integrations.EventSchemaDrift))
	t.Logf("sync: %d page(s), %d seen, %d stored, complete %v, schema drift events %d", res.Pages, res.Seen, len(items), res.Complete, rep.Drift)
	if rep.Drift > 0 {
		t.Errorf("the vendor's answers do not match what the adapter expects (schema drift): %s", h.events(integrations.EventSchemaDrift)[0].Data)
	}
	if res.Seen > 0 && len(items) == 0 {
		t.Errorf("the vendor returned %d items and none was stored", res.Seen)
	}
	for _, it := range items {
		if it.ExternalID == "" || strings.TrimSpace(it.Title) == "" || (it.Kind != integrations.ItemPage && it.IssueRef == "") {
			t.Errorf("an item did not parse: kind %s, id set %v, title set %v, ref %q", it.Kind, it.ExternalID != "", strings.TrimSpace(it.Title) != "", it.IssueRef)
		}
	}
	if len(items) == 0 {
		t.Logf("nothing to read in this scope: the parsing of real items is unchecked (point the scope variables at something that has items)")
	}
	if strings.Contains(h.diskText(), token) || strings.Contains(h.allEventsJSON(), token) {
		t.Error("the token reached the database, a body file or an event")
	}
	rep.OK = !t.Failed()
}

func writeLiveReport(t *testing.T, rep LiveReport) {
	path := os.Getenv("DRYDOCK_LIVE_REPORT")
	if path == "" {
		return
	}
	var all []LiveReport
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all = append(all, rep)
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Logf("the report was not written: %v", err)
		}
	}
}

// The live check itself, run against the Linear fixture as if it were the real service: it connects, tests, syncs, checks
// every item and writes its report without item text or the token.
func TestTheLiveCheckWorksAgainstTheFixture(t *testing.T) {
	_, url := linearFixture(t, 3)
	report := filepath.Join(t.TempDir(), "live.json")
	t.Setenv("DRYDOCK_LIVE_LINEAR_TOKEN", linearToken)
	t.Setenv("DRYDOCK_LIVE_LINEAR_BASE_URL", url)
	t.Setenv("DRYDOCK_LIVE_LINEAR_TEAM", "ENG")
	t.Setenv("DRYDOCK_LIVE_REPORT", report)
	liveCheck(t, liveVendors[0])
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var reps []LiveReport
	if err := json.Unmarshal(b, &reps); err != nil || len(reps) != 1 {
		t.Fatalf("%v %s", err, b)
	}
	r := reps[0]
	if !r.OK || r.Kind != "linear" || r.Items != 3 || r.Drift != 0 || len(r.ToolsFound) == 0 || len(r.ToolsMissing) != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(string(b), linearToken) || strings.Contains(string(b), "Description of issue") {
		t.Fatalf("the report carries the token or item text:\n%s", b)
	}
}

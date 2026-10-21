package integrations_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
)

// Live checks against the real vendor APIs (docs/integrations.md, "Live checks"). Every other test runs against fixtures
// written from the vendors' documentation; these check those fixtures against the real services. Each runs only when its
// credential is set (the value is never printed), reads only, and never imports an item or writes anything back:
//
//  1. Test connection: the API accepts the credential and reports the account, the workspace and, where the API has them,
//     the scopes; when a scope is set, one item is read.
//  2. The in-app check against the account (Service.Verify, decision P59, the same code the "Check against your account"
//     button runs): the first setting with choices is listed (Linear teams, Notion data sources, Sentry organizations, Aikido
//     repositories, GitHub repositories), one page of the scope is read and every item checked for an id, a title, a reference
//     and an update time, and one item's full text is read where the vendor keeps it apart.
//  3. One sync (at most 20 pages of 50): every item parses into an item with an id, a title and a reference, and no schema
//     drift is reported.
//  4. The credential is nowhere in the database, the body files or the events.
//
// DRYDOCK_LIVE_REPORT=<file> also writes what was found (identity, counts, the API version; no item text, no credential) as
// JSON. Run, for example:
//
//	DRYDOCK_LIVE_LINEAR_TOKEN=lin_api_... go test ./internal/integrations -run TestLive -v
//
// Credentials: DRYDOCK_LIVE_LINEAR_TOKEN (personal API key), DRYDOCK_LIVE_NOTION_TOKEN (internal integration secret),
// DRYDOCK_LIVE_SENTRY_TOKEN (auth token), DRYDOCK_LIVE_AIKIDO_CLIENT_ID and DRYDOCK_LIVE_AIKIDO_CLIENT_SECRET (REST API client),
// DRYDOCK_LIVE_GITHUB_REPO (owner/name; uses your signed-in gh). Scope: DRYDOCK_LIVE_LINEAR_TEAM, DRYDOCK_LIVE_LINEAR_PROJECT,
// DRYDOCK_LIVE_NOTION_DATA_SOURCE, DRYDOCK_LIVE_NOTION_QUERY, DRYDOCK_LIVE_SENTRY_ORG (required for a sync),
// DRYDOCK_LIVE_SENTRY_PROJECT, DRYDOCK_LIVE_AIKIDO_REPO, DRYDOCK_LIVE_AIKIDO_REGION (eu, us, me, au), DRYDOCK_LIVE_GITHUB_LABELS;
// DRYDOCK_LIVE_<VENDOR>_BASE_URL for a self-hosted server.

type liveVendor struct {
	kind   string
	creds  map[string]string // ConnectReq field -> environment variable (the first one gates the test)
	config map[string]string // assignment setting -> environment variable
	choice string            // the setting whose choices are listed
}

var liveVendors = []liveVendor{
	{integrations.KindLinear, map[string]string{"token": "DRYDOCK_LIVE_LINEAR_TOKEN"}, map[string]string{"team": "DRYDOCK_LIVE_LINEAR_TEAM", "project": "DRYDOCK_LIVE_LINEAR_PROJECT"}, "team"},
	{integrations.KindNotion, map[string]string{"token": "DRYDOCK_LIVE_NOTION_TOKEN"}, map[string]string{"data_source": "DRYDOCK_LIVE_NOTION_DATA_SOURCE", "query": "DRYDOCK_LIVE_NOTION_QUERY"}, "data_source"},
	{integrations.KindSentry, map[string]string{"token": "DRYDOCK_LIVE_SENTRY_TOKEN"}, map[string]string{"organization": "DRYDOCK_LIVE_SENTRY_ORG", "project": "DRYDOCK_LIVE_SENTRY_PROJECT"}, "organization"},
	{integrations.KindAikido, map[string]string{"clientSecret": "DRYDOCK_LIVE_AIKIDO_CLIENT_SECRET", "clientId": "DRYDOCK_LIVE_AIKIDO_CLIENT_ID", "region": "DRYDOCK_LIVE_AIKIDO_REGION"}, map[string]string{"repo": "DRYDOCK_LIVE_AIKIDO_REPO"}, "repo"},
	{integrations.KindGitHub, map[string]string{"repo": "DRYDOCK_LIVE_GITHUB_REPO"}, map[string]string{"repo": "DRYDOCK_LIVE_GITHUB_REPO", "labels": "DRYDOCK_LIVE_GITHUB_LABELS"}, "repo"},
}

// LiveReport is what one live check found. It never holds item text or a credential.
type LiveReport struct {
	Kind       string   `json:"kind"`
	At         string   `json:"at"`
	OK         bool     `json:"ok"`
	APIVersion string   `json:"apiVersion"`
	Account    bool     `json:"accountRead"` // whether the API named the account (the name itself is not written)
	Workspace  bool     `json:"workspaceRead"`
	Scopes     []string `json:"scopes"`
	Choices    int      `json:"choices"`
	Calls      int      `json:"calls"`      // HTTP calls the check against the account made
	Mismatches []string `json:"mismatches"` // what it found unexpected (field names and JSON types, never values)
	Pages      int      `json:"pages"`
	Items      int      `json:"items"`
	Drift      int      `json:"drift"`
	Steps      []string `json:"steps"`
}

func TestLiveLinear(t *testing.T) { liveCheck(t, liveVendors[0]) }
func TestLiveNotion(t *testing.T) { liveCheck(t, liveVendors[1]) }
func TestLiveSentry(t *testing.T) { liveCheck(t, liveVendors[2]) }
func TestLiveAikido(t *testing.T) { liveCheck(t, liveVendors[3]) }
func TestLiveGitHub(t *testing.T) { liveCheck(t, liveVendors[4]) }

func firstCredEnv(v liveVendor) string {
	for _, k := range []string{"token", "clientSecret", "repo"} {
		if e, ok := v.creds[k]; ok {
			return e
		}
	}
	return ""
}

func liveCheck(t *testing.T, v liveVendor) {
	gate := firstCredEnv(v)
	secret := os.Getenv(gate)
	if secret == "" {
		t.Skipf("set %s to check %s against the real service (read-only)", gate, v.kind)
	}
	mod := func(o *integrations.Options) {
		o.Now = time.Now
		o.RateLimits = nil // the documented budgets, as in the product
	}
	if v.kind == integrations.KindGitHub {
		if _, err := exec.LookPath("gh"); err != nil {
			t.Skip("gh is not installed")
		}
		mod = func(o *integrations.Options) {
			o.Now, o.RateLimits = time.Now, nil
			o.GitHub = func(context.Context) (host.API, error) { return host.GhCLI{}, nil }
		}
	}
	h := newHarness(t, mod)
	ctx := context.Background()
	req := integrations.ConnectReq{Kind: v.kind, BaseURL: os.Getenv("DRYDOCK_LIVE_" + strings.ToUpper(v.kind) + "_BASE_URL")}
	switch v.kind {
	case integrations.KindAikido:
		req.ClientID, req.ClientSecret, req.Region = os.Getenv(v.creds["clientId"]), secret, os.Getenv(v.creds["region"])
	case integrations.KindGitHub:
		secret = "" // nothing secret: gh holds the sign-in
	default:
		req.Token = secret
	}
	c, err := h.svc.Connect(ctx, human, req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	cfg := map[string]string{}
	for key, env := range v.config {
		if val := os.Getenv(env); val != "" {
			cfg[key] = val
		}
	}
	assigned := len(cfg) > 0 || v.kind == integrations.KindLinear || v.kind == integrations.KindNotion || v.kind == integrations.KindAikido
	if assigned {
		if _, err := h.svc.Assign(ctx, human, integrations.AssignReq{IntegrationID: c.ID, ProjectID: "prj1", Config: cfg}); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	rep := LiveReport{Kind: v.kind, At: time.Now().UTC().Format(time.RFC3339)}
	for _, k := range integrations.Catalog(nil) {
		if k.Kind == v.kind {
			rep.APIVersion = k.APIVersion
		}
	}
	defer func() { writeLiveReport(t, rep) }()

	tr, err := h.svc.Test(ctx, c.ID)
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	for _, s := range tr.Steps {
		rep.Steps = append(rep.Steps, s.Name+": "+map[bool]string{true: "ok", false: "FAILED"}[s.OK])
		t.Logf("%s: ok=%v %s", s.Name, s.OK, s.Detail)
	}
	rep.Account, rep.Workspace, rep.Scopes = tr.Identity.Account != "", tr.Identity.Workspace != "", tr.Identity.Scopes
	t.Logf("identity: account read %v, workspace read %v, scopes %v", rep.Account, rep.Workspace, rep.Scopes)
	if !tr.OK {
		t.Fatalf("test connection failed: %s", tr.Summary)
	}
	if !rep.Account && !rep.Workspace {
		t.Errorf("the API named neither the account nor the workspace")
	}
	// the in-app "Check against your account" (P59) is the same check: identity, the first setting's choices, one page of the
	// scope with every item checked for what Drydock needs, one item's full text
	vr, err := h.svc.Verify(ctx, human, c.ID)
	if err != nil {
		t.Fatalf("check against the account: %v", err)
	}
	for _, s := range vr.Steps {
		t.Logf("check %s: ok=%v skipped=%v %s", s.Name, s.OK, s.Skipped, s.Detail)
	}
	for _, cl := range vr.Calls {
		t.Logf("call %s: HTTP %d", cl.Endpoint, cl.Status)
	}
	for _, n := range vr.Notes {
		t.Logf("note: %s", n)
	}
	rep.Choices, rep.Calls = vr.Read.Choices, len(vr.Calls)
	rep.Mismatches = append(rep.Mismatches, vr.Mismatches...)
	t.Logf("%d %s choice(s); check: %s", vr.Read.Choices, v.choice, vr.Summary)
	if !vr.OK {
		t.Errorf("the check against the account did not pass: %s %v", vr.Summary, vr.Mismatches)
	}
	if !assigned {
		t.Logf("no scope set (%v): the sync is skipped", v.config)
		rep.OK = !t.Failed()
		return
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
	if secret != "" && (strings.Contains(h.diskText(), secret) || strings.Contains(h.allEventsJSON(), secret)) {
		t.Error("the credential reached the database, a body file or an event")
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

// The live check itself, run against the Linear fixture as if it were the real service: it connects, tests, lists the
// teams, syncs, checks every item and writes its report without item text or the credential.
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
	if !r.OK || r.Kind != "linear" || r.Items != 3 || r.Drift != 0 || !r.Account || !r.Workspace || r.Choices != 2 || r.Calls != 3 || len(r.Mismatches) != 0 || r.APIVersion == "" {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(string(b), linearToken) || strings.Contains(string(b), "Description of issue") || strings.Contains(string(b), "Kyle Fixture") {
		t.Fatalf("the report carries the credential, an identity or item text:\n%s", b)
	}
}

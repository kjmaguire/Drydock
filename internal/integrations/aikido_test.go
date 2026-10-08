package integrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
)

const aikidoKey = "AIK_Xv7Bn2Mq9Lc4Zr6Td8Wy1Hs3Gf5Jk0Pa"

// aikidoStdio connects Aikido to the fixture running as a REAL subprocess speaking MCP over stdio (this test binary re-executed),
// the way the user-installed @aikidosec/mcp package runs. The API key can only arrive through the environment.
func aikidoStdio(t *testing.T, h *harness, findings []itfixture.AikidoFinding, key string) (integrations.ConnectionView, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "findings.json")
	writeFindings(t, path, findings)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(aikidoKey))
	argv, _ := json.Marshal([]string{exe, itfixture.AikidoStdioFlag, "-findings=" + path, "-expect=" + hex.EncodeToString(sum[:])})
	c := h.connect(integrations.KindAikido, key, "", map[string]string{"argv": string(argv)})
	return c, path
}

func writeFindings(t *testing.T, path string, f []itfixture.AikidoFinding) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"findings": f})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func finding(id int, sev, typ, rule, desc string, minutesAgo int) itfixture.AikidoFinding {
	at := baseTime.Add(-time.Duration(minutesAgo) * time.Minute).Format(time.RFC3339)
	return itfixture.AikidoFinding{ID: id, Severity: sev, Type: typ, Rule: rule, Description: desc, Repo: "acme/web", File: "src/db.go", Line: 12, Status: "open", UpdatedAt: at, FirstAt: at}
}

func TestAikidoThroughARealStdioServerKeyOnlyInTheEnvironment(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	findings := []itfixture.AikidoFinding{
		finding(101, "critical", "open_source", "CVE-2026-0001 in lodash", "Prototype pollution in lodash < 4.17.22.", 1),
		finding(102, "high", "sast", "SQL injection", "User input reaches a query.", 2),
		finding(103, "medium", "leaked_secret", "Hard-coded credential", "Found "+ghToken+" in config.go", 3),
		finding(104, "low", "iac", "Open security group", "Port 22 open to the world.", 4),
	}
	c, path := aikidoStdio(t, h, findings, aikidoKey)

	rep, err := h.svc.Test(ctx, c.ID)
	if err != nil || !rep.OK {
		t.Fatalf("%+v %v", rep, err)
	}
	r := h.sync(c.ID, integrations.SyncOpts{})
	if r.New != 4 || !r.Complete {
		t.Fatalf("%+v", r)
	}
	byID := map[string]integrations.Item{}
	for _, it := range h.items(c.ID) {
		byID[it.ExternalID] = it
	}
	if byID["101"].Priority != "critical" || byID["101"].Kind != integrations.ItemFinding || byID["101"].IssueRef != "AIK-101" || byID["104"].Priority != "low" {
		t.Fatalf("%+v", byID["101"])
	}
	// the secret in the finding text was redacted before storage
	d, _ := h.svc.ItemDetail(ctx, byID["103"].ID)
	if strings.Contains(d.Body, ghToken) || !strings.Contains(d.Body, "[REDACTED:") || byID["103"].Redactions == 0 {
		t.Fatalf("%q", d.Body)
	}

	// importing a finding makes a SECURITY task whose priority follows the severity
	res, err := h.svc.Import(ctx, human, byID["101"].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tk := h.taskList()[0]
	if tk.Priority != 4 || tk.IssueRef != "AIK-101" || !strings.Contains(tk.Description, "severity critical") || !strings.Contains(tk.Description, "> Title: CVE-2026-0001 in lodash") {
		t.Fatalf("%+v %+v", res, tk)
	}
	h.svc.Import(ctx, human, byID["102"].ID, "")
	if got := h.taskList()[1].Priority; got != 3 {
		t.Fatalf("high -> %d", got)
	}

	// every sync lists the findings and diffs by content hash; a finding that disappears upstream is marked gone by the sweep
	h.clk.Advance(time.Minute)
	writeFindings(t, path, findings[:3])
	r = h.sync(c.ID, integrations.SyncOpts{})
	if r.Gone != 1 || r.New != 0 {
		t.Fatalf("%+v", r)
	}
	if n := h.count(`SELECT COUNT(*) FROM external_items WHERE integration_id=? AND gone=1 AND external_id='104'`, c.ID); n != 1 {
		t.Fatal("finding 104 was not marked gone")
	}
	// an ignore is a suppression of a security signal: reason required, approval required, then it goes through the stdio server
	o, err := h.svc.Propose(ctx, human, integrations.ProposeReq{IntegrationID: c.ID, Op: "ignore", Target: "101", Params: map[string]string{"reason": "false positive: dev-only dependency"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Execute(ctx, human, o.ID); !errors.Is(err, integrations.ErrNotApproved) {
		t.Fatalf("%v", err)
	}
	h.svc.Decide(ctx, human, o.ID, true)
	if out, err := h.svc.Execute(ctx, human, o.ID); err != nil || out.Status != integrations.OutSent {
		t.Fatalf("%+v %v", out, err)
	}
	// the key is nowhere in the app's storage, events or logs
	for name, text := range map[string]string{"disk": h.diskText(), "events": h.allEventsJSON(), "logs": h.logs.String()} {
		if strings.Contains(text, aikidoKey) {
			t.Errorf("the Aikido key is in the %s", name)
		}
	}
}

func TestAikidoWrongKeyIsReportedAsARejectedToken(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	c, _ := aikidoStdio(t, h, []itfixture.AikidoFinding{finding(1, "high", "sast", "x", "y", 1)}, "AIK_wrongwrongwrongwrongwrong")
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || rep.Steps[len(rep.Steps)-1].OK || !strings.Contains(rep.Summary, "rejected the token") {
		t.Fatalf("%+v", rep)
	}
	if v, _ := h.svc.Get(ctx, c.ID); v.Status != integrations.StatusUnauthenticated {
		t.Fatalf("%s", v.Status)
	}
}

func TestAikidoServerNotInstalledIsAPlainLanguageFailure(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	c := h.connect(integrations.KindAikido, aikidoKey, "", map[string]string{"argv": `["/definitely/not/installed/aikido-mcp"]`})
	rep, _ := h.svc.Test(ctx, c.ID)
	if rep.OK || !strings.Contains(rep.Summary, "could not reach Aikido's MCP server") {
		t.Fatalf("%+v", rep)
	}
}

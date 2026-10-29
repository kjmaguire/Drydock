package integrations_test

import (
	"context"
	"testing"

	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Migrations 00144 to 00147 (decision P114) go down and up again: existing rules survive the rebuild of integration_rules,
// link_task rules (which the older CHECK cannot hold) are dropped on the way down, and the new tables and columns come and go.
func TestP114MigrationsGoDownAndUp(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_, url := linearFixture(t, 1)
	c := h.connect(integrations.KindLinear, linearToken, url, map[string]string{"team": "ENG"})
	keep, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "bugs", When: `item.state == "Todo"`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetRule(ctx, human, integrations.RuleReq{IntegrationID: c.ID, ProjectID: "prj1", Name: "link", When: "true", Action: integrations.ActionLinkTask, Params: map[string]any{"task": "task_1"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.MigrateTo(ctx, 143); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT COUNT(*) FROM integration_rules`); n != 1 {
		t.Fatalf("%d rules after the down migration", n)
	}
	for _, tbl := range []string{"integration_mappings", "integration_webhook_deliveries", "integration_auto_approve", "integration_attachments"} {
		if n := h.count(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, tbl); n != 0 {
			t.Fatalf("%s is still there", tbl)
		}
	}
	if err := h.db.MigrateTo(ctx, storage.LatestVersion()); err != nil {
		t.Fatal(err)
	}
	r, err := h.svc.Store().GetRule(ctx, keep.ID)
	if err != nil || r.When != `item.state == "Todo"` || r.Version != keep.Version {
		t.Fatalf("%+v %v", r, err)
	}
	h.sync(c.ID, integrations.SyncOpts{})
	if its := h.items(c.ID); len(its) != 1 || its[0].Properties != nil {
		t.Fatalf("%+v", its)
	}
}

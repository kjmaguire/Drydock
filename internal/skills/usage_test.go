package skills

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Decision P49: what a skill cost and whether anyone read it, with a recommendation that says what the numbers cannot tell.
func TestUsageCountsCostAndReadsAndRecommends(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(map[string]string{
		"skills/everywhere/SKILL.md": skillMD("everywhere", "Loads for every task", "", strings.Repeat("Rule. ", 200)),
	}))
	e.useFixture(gh)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"d1-migrations", "workers-style", "everywhere"} {
		if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", name, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	tgt := func(task string) Target {
		return Target{ProjectID: "p1", TaskID: task, AgentID: "ag1", Role: "backend"}
	}
	// six tasks: d1-migrations listed on demand and never read; workers-style listed and read twice; everywhere loaded in full
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("task_%d", i)
		e.s.RecordDelivery(ctx, tgt(task), "run_"+task, "d1-migrations", "h", MethodClaudeOnDemand, FidelityOnDemand, OnDemandDir+"/d1-migrations.md")
		e.s.RecordDelivery(ctx, tgt(task), "run_"+task, "workers-style", "h", MethodClaudeOnDemand, FidelityOnDemand, OnDemandDir+"/workers-style.md")
		e.s.RecordDelivery(ctx, tgt(task), "run_"+task, "everywhere", "h", MethodClaudeRules, FidelityExact, ".claude/rules/everywhere.md")
	}
	e.s.RecordUse(ctx, tgt("task_1"), "run_task_1", "workers-style", UseFileRead)
	e.s.RecordUse(ctx, tgt("task_4"), "run_task_4", "workers-style", UseTool)
	if n := len(e.events(EventUsed)); n != 2 {
		t.Fatalf("used events: %d", n)
	}

	us, err := e.s.Usage(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SkillUsage{}
	var order []string
	for _, u := range us {
		by[u.Name] = u
		order = append(order, u.Name)
	}
	d1, ws, ev := by["d1-migrations"], by["workers-style"], by["everywhere"]
	if d1.Listed != 6 || d1.Loaded != 0 || d1.Opened != 0 || d1.Tasks != 6 || d1.TokensLoaded != 0 || d1.TokensListed == 0 || d1.LastUsed != nil {
		t.Fatalf("d1: %+v", d1)
	}
	if d1.Recommendation != RecommendRemove || !strings.Contains(d1.Reason, "never opened") {
		t.Fatalf("a listed, never opened skill is the one to remove: %+v", d1)
	}
	if ws.Opened != 2 || ws.LastUsed == nil || ws.Recommendation != RecommendKeep || !strings.Contains(ws.Reason, "being used") {
		t.Fatalf("ws: %+v", ws)
	}
	if ev.Loaded != 6 || ev.TokensLoaded < 6*250 || ev.Recommendation != RecommendNarrow || !strings.Contains(ev.Reason, "names no paths") {
		t.Fatalf("a skill loaded everywhere without paths is the one to narrow: %+v", ev)
	}
	if strings.Join(order, ",") != "d1-migrations,everywhere,workers-style" {
		t.Fatalf("remove first, then narrow, then keep: %v", order)
	}

	// too few tasks: no recommendation, and the reason says so
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil {
		t.Fatal(err)
	}
	e2 := newEnv(t, nil)
	e2.useFixture(gh)
	if _, err := e2.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil {
		t.Fatal(err)
	}
	e2.s.RecordDelivery(ctx, tgt("t1"), "r1", "d1-migrations", "h", MethodClaudeOnDemand, FidelityOnDemand, "")
	us, _ = e2.s.Usage(ctx, "p1")
	if len(us) != 1 || us[0].Recommendation != "" || !strings.Contains(us[0].Reason, "Too few tasks") {
		t.Fatalf("%+v", us)
	}
}

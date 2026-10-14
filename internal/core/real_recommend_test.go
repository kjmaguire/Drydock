package core_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/skills"
)

// realSkill is a SKILL.md with a description written the way real registries write them, so the advisor has something to
// judge (the stand-in fixture's "Stand-in skill X" says nothing).
func realSkill(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %q\n---\n# %s\n\n%s\n", name, description, name, description)
}

// Decision P52 with the REAL Claude Code as the team lead (haiku): one read-only recommendation turn over a Cloudflare
// Workers storefront whose Library also holds skills that do not fit it. The answer must parse, name only what exists, give
// the Cloudflare skills or the Cloudflare docs server to someone, leave the unrelated skills out, and every item it keeps
// must apply through the pages' own methods. Then the project evolves and must ask again by itself (below). It spends real
// money: two haiku turns (up to four when an answer needs the retry), measured at about $0.0013 each, so it runs only with
// DRYDOCK_REAL_CLAUDE=1 and a signed-in claude.
func TestRealLeadRecommendsSkillsAndServersThatFitTheProject(t *testing.T) {
	if why := contract.RealClaudeSkip(); why != "" {
		t.Skip(why)
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	if det, err := claude.New().Detect(context.Background()); err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &app{t: t, c: c}

	f := newWatchFixture(t)
	f.publish("cloudflare/skills", watchSha1, map[string]string{
		"skills/wrangler/SKILL.md": realSkill("wrangler", "Configure, develop and deploy Cloudflare Workers with the Wrangler CLI: wrangler.jsonc, bindings, local dev and deploys."),
		"skills/durable-objects/SKILL.md": realSkill("durable-objects",
			"Design Cloudflare Durable Objects: one object per coordination unit, storage API, alarms and WebSocket hibernation."),
		"skills/d1/SKILL.md": realSkill("d1", "Use Cloudflare D1 (SQLite at the edge): schema migrations with wrangler, prepared statements, batching and read replicas."),
	})
	f.publish("stripe/ai", watchSha1, map[string]string{
		"skills/stripe-best-practices/SKILL.md": realSkill("stripe-best-practices",
			"Integrate Stripe payments correctly: Checkout Sessions or Payment Intents, webhooks with signature checks, idempotency keys."),
	})
	f.publish("vercel-labs/agent-skills", watchSha1, map[string]string{
		"skills/react-native-guidelines/SKILL.md": realSkill("react-native-guidelines", "Build React Native mobile apps with Expo: navigation, native modules and app store releases."),
		"skills/nextjs-on-vercel/SKILL.md":        realSkill("nextjs-on-vercel", "Deploy Next.js apps on Vercel: ISR, edge middleware and Vercel environment variables."),
	})
	f.use(a)
	if _, err := c.RunSkillWatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	type libSkill struct {
		Name        string `json:"name"`
		RegistryID  string `json:"registryId"`
		Status      string `json:"status"`
		BlockReason string `json:"blockReason"`
	}
	var lib []libSkill
	a.call("skill.libraryList", map[string]any{}, &lib)
	if len(lib) != 6 || slices.ContainsFunc(lib, func(l libSkill) bool { return l.Status != "available" }) {
		t.Fatalf("the library did not fill from the fixture: %+v", lib)
	}

	p := gitProject(t, a, "edgeshop")
	a.call("project.setBrief", map[string]any{"id": p.ID, "brief": "A small online shop that runs entirely on Cloudflare Workers. " +
		"Products and orders live in D1; each shopping cart is a Durable Object; payments go through Stripe Checkout with a webhook " +
		"that marks the order paid. Server-rendered HTML, no React and no mobile app. Deployed with Wrangler."}, nil)
	var lead, back, front core.Agent
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Lead", Adapter: "claude", Role: "lead", Model: "haiku",
		Purpose: "Plans the work and keeps the team on the brief."}, &lead)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "haiku",
		Purpose: "Workers API, D1 schema and queries, the cart Durable Object, the Stripe webhook."}, &back)
	a.call("agent.create", core.AgentSpec{ProjectID: p.ID, Name: "Frontend #1", Adapter: "claude", Role: "frontend", Model: "haiku",
		Purpose: "Server-rendered product and checkout pages."}, &front)
	var srv core.MCPLibraryServer
	a.call("mcplib.save", core.MCPServerInput{ID: "cfdocs", Display: "Cloudflare documentation", Transport: "http", URL: "https://docs.mcp.cloudflare.com/mcp/"}, &srv)
	a.call("mcplib.save", core.MCPServerInput{ID: "mslearn", Display: "Microsoft Learn documentation", Transport: "http", URL: "https://learn.microsoft.com/api/mcp"}, &srv)

	prompt, err := c.TestRecommendPrompt(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// what the lead is told, checked before anything is spent
	for _, want := range []string{"Brief: A small online shop", "- wrangler (registry cloudflare-skills)", "- stripe-best-practices (registry stripe-ai)",
		"- nextjs-on-vercel (registry vercel-agent-skills)", "- cfdocs: Cloudflare documentation", back.ID + ": Backend #1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt does not have %q:\n%s", want, prompt)
		}
	}
	var r core.Recommendation
	a.call("recommend.start", map[string]any{"projectId": p.ID}, &r)
	if r.AgentID != lead.ID {
		t.Fatalf("asked %s, not the lead", r.AgentID)
	}
	// the shared wait (bounded by the test's own -timeout), then what the lead said, for the person reading the log
	finish := func(id string) core.Recommendation {
		r := a.recommendDone(p.ID, id)
		t.Logf("%s: status %s, cost $%.4f, summary: %s", r.Trigger, r.Status, r.CostUSD, r.Summary)
		for _, it := range r.Items {
			t.Logf("  item %s %s %s (registry %q) agents %v: %s", it.Kind, it.Action, it.Name, it.RegistryID, it.Agents, it.Reason)
		}
		for _, d := range r.Dropped {
			t.Logf("  dropped: %s", d)
		}
		if r.Status != "done" {
			t.Fatalf("the real lead's turn did not finish: %s %s", r.Status, r.Error)
		}
		return r
	}
	r = finish(r.ID)
	if r.CostUSD <= 0 {
		t.Error("the turn's cost was not recorded")
	}
	if len(r.Items) == 0 {
		t.Fatal("the lead recommended nothing for a Cloudflare project with Cloudflare skills and docs in the workspace")
	}
	fits := false
	for _, it := range r.Items {
		if slices.Contains([]string{"react-native-guidelines", "nextjs-on-vercel", "mslearn"}, it.Name) {
			t.Errorf("recommended %s, which does not fit the brief: %s", it.Name, it.Reason)
		}
		fits = fits || slices.Contains([]string{"wrangler", "durable-objects", "d1", "cfdocs", "stripe-best-practices"}, it.Name)
	}
	if !fits {
		t.Error("no recommendation names a Cloudflare or Stripe skill or the Cloudflare docs server")
	}

	// every item the core kept applies through the pages' own methods (an integration is advisory: none is connected here)
	for _, it := range r.Items {
		if it.Kind == "integration" {
			continue
		}
		// the core already checked every item names what exists, so a failure here is a bug in applying it
		after, err := c.RecommendApply(context.Background(), r.ID, it.ID)
		if err != nil {
			t.Errorf("applying %s %s %s: %v", it.Kind, it.Action, it.Name, err)
			continue
		}
		for _, x := range after.Items {
			if x.ID == it.ID && x.Status != "applied" {
				t.Errorf("%s %s is %s after applying", it.Kind, it.Name, x.Status)
			}
		}
	}
	if strings.TrimSpace(r.Summary) == "" {
		t.Error("no summary for the person")
	}

	// The project evolves: the frontend agent's last five tasks were Stripe checkout work, and the Stripe skill reaches only the
	// backend. The project asks the lead again by itself; the lead must notice and give the frontend agent the Stripe skill,
	// and say so in #general.
	// whatever the first turn did with it, the Stripe skill now reaches the backend agent only
	if _, err := c.Knowledge().Skills.Get(context.Background(), p.ID, "stripe-best-practices"); err != nil {
		a.call("skill.libraryAdd", map[string]any{"projectId": p.ID, "registryId": "stripe-ai", "name": "stripe-best-practices",
			"audience": skills.AudienceAgents, "agents": []string{back.ID}}, nil)
	}
	a.call("skill.audience", map[string]any{"projectId": p.ID, "name": "stripe-best-practices", "audience": skills.AudienceAgents, "agents": []string{back.ID}}, nil)
	db := c.DB.Writer()
	for i, title := range []string{
		"Checkout page: redirect to Stripe Checkout with the cart's line items",
		"Show Stripe's cancelled and failed payment states on the checkout page",
		"Order confirmation page waits for the Stripe webhook to mark the order paid",
		"Show the Stripe test-mode banner when the publishable key is a test key",
		"Retry button on the checkout page that reuses the Stripe idempotency key",
	} {
		if _, err := db.Exec(`INSERT INTO tasks(id,project_id,seq,title,status,assignee,created_at,updated_at) VALUES(?,?,?,?,'COMPLETED',?,1,1)`,
			fmt.Sprintf("task_stripe%02d", i), p.ID, 100+i, title, front.ID); err != nil {
			t.Fatal(err)
		}
	}
	// the first turn is made two days older, past the one-day gap, so only the five tasks decide
	if _, err := db.Exec(`UPDATE recommendations SET created_at=created_at-2*86400000 WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	c.TestMaybeRecommend(context.Background(), p.ID)
	var list core.Recommendations
	a.call("recommend.list", map[string]any{"projectId": p.ID}, &list)
	if len(list.Runs) != 2 || list.Runs[0].Trigger != "evolution" {
		t.Fatalf("the project did not ask again after five completed tasks: %+v", list.Runs)
	}
	ev := finish(list.Runs[0].ID)
	gave := false
	for _, it := range ev.Items {
		// a give to the frontend agent, or widening the skill to the whole team
		if it.Name == "stripe-best-practices" && (slices.Contains(it.Agents, front.ID) || it.Action == "team") {
			gave = true
		}
	}
	if !gave {
		t.Error("the frontend agent now does Stripe checkout work and the lead did not recommend it the Stripe skill")
	}
	if len(ev.Items) > 0 {
		a.waitGeneral(p.ID, "Lead has ")
	}
}

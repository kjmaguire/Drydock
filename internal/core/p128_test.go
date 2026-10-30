package core

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/router"
)

// Decision P128: Claude Code's allowed_warning marks the models its limit covers as warned, until the reset or the next report
// of that limit; the router's candidates carry it (agentSource), so the turns the rules allow downshift.
func TestAnAllowedWarningMarksTheCLIsModelsUntilTheResetOrTheNextAllowed(t *testing.T) {
	var l agentLimits
	now := time.Unix(1_000_000, 0)
	ev := func(status, typ string, resets int64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": status, "rateLimitType": typ, "resetsAt": resets}})
		return b
	}
	l.observe("claude", ev("allowed_warning", "seven_day_opus", 1_000_600))
	if w, lim := l.warned("claude", "claude-opus-5-5", now); !w || lim.warning("claude") != "Claude Code reported its seven day opus limit near (allowed_warning, resets 1970-01-12 13:56 UTC)" {
		t.Fatalf("an Opus warning must mark Opus: %v %q", w, lim.warning("claude"))
	}
	if w, _ := l.warned("claude", "sonnet", now); w {
		t.Fatal("an Opus warning must not mark Sonnet")
	}
	if b, _ := l.blocked("claude", "claude-opus-5-5", now); b {
		t.Fatal("a warning blocks nothing")
	}
	if w, _ := l.warned("claude", "claude-opus-5-5", now.Add(time.Hour)); w {
		t.Fatal("a warning past its reset must not mark")
	}
	l.observe("claude", ev("allowed", "seven_day_opus", 1_000_600))
	if w, _ := l.warned("claude", "claude-opus-5-5", now); w {
		t.Fatal("the next allowed report of the limit clears the warning")
	}
	// an account-wide warning, without a reset time, in the shape the ACP bridge forwards (the info itself)
	l.observe("claude", json.RawMessage(`{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.86}`))
	if w, lim := l.warned("claude", "haiku", now.Add(48*time.Hour)); !w || lim.warning("claude") != "Claude Code reported its seven day limit near (allowed_warning)" {
		t.Fatalf("an account-wide warning marks every model: %v %+v", w, lim)
	}
	if w, _ := l.warned("codex", "haiku", now); w {
		t.Fatal("another CLI is not marked")
	}
}

// The wiring: a warning a session publishes reaches the agent's route candidates, on the premium model it covers only.
func TestAWarningFromASessionReachesTheRouteCandidates(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := c.CreateProject(ctx, "warned", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Call(ctx, "agent.create", mustJSONInternal(AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}))
	if err != nil {
		t.Fatal(err)
	}
	var a Agent
	_ = json.Unmarshal(raw, &a)
	if _, err := c.Call(ctx, "agent.update", mustJSONInternal(map[string]any{"id": a.ID, "routeModels": []RouteModel{{Model: "sonnet", Tier: "cheap"}, {Model: "opus", Tier: "premium"}}})); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"kind": "rate_limit", "data": json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"seven_day_opus","resetsAt":` +
		strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}}`)})
	if _, err := c.Bus.Publish(ctx, events.Event{Type: "agent.output", ProjectID: p.ID, AgentID: a.ID, Data: data}); err != nil {
		t.Fatal(err)
	}
	src := agentSource{&economics{c: c}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		cands, err := src.Candidates(ctx, router.RouteRequest{AgentID: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		by := map[string]router.Candidate{}
		for _, x := range cands {
			by[x.Model] = x
		}
		if strings.Contains(by["opus"].QuotaWarning, "seven day opus limit near") {
			if by["sonnet"].QuotaWarning != "" || !by["opus"].QuotaOK {
				t.Fatalf("candidates %+v", cands)
			}
			if down, _ := router.QuotaDownshift(router.LevelQuality, router.RouteRequest{}, false, "", cands); !down {
				t.Fatal("the warned premium model must downshift a Quality turn")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the warning never reached the candidates: %+v", cands)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The occupancy estimate's window: the catalog's context length for the turn's model, on the agent's provider, else the
// smallest any provider lists, else unknown.
func TestTheEstimateWindowComesFromTheCatalog(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{
		`INSERT INTO providers(id,name,kind,base_url,data_class,tier,created_at) VALUES('pa','A','aggregator','https://a.example','private','cheap',1),('pb','B','direct','https://b.example','private','cheap',1)`,
		`INSERT INTO model_catalog(provider_id,model,context_length) VALUES('pa','m-wide',400000),('pb','m-wide',272000),('pa','m-zero',0)`,
	} {
		if _, err := c.DB.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for model, want := range map[string]int64{"m-wide": 272_000, "m-zero": 0, "m-unknown": 0, "": 0} {
		if got := c.modelWindow(ctx, rt.Config{AgentID: "nobody", Spec: agents.RunSpec{Model: model}}); got != want {
			t.Errorf("%q: %d, want %d", model, got, want)
		}
	}
}

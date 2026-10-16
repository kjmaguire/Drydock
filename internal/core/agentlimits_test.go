package core

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// A rejected limit blocks the CLI's models until it resets; an allowed one and a limit of another model family do not.
func TestAgentLimitsFollowWhatTheCLIReports(t *testing.T) {
	var l agentLimits
	now := time.Unix(1_000_000, 0)
	ev := func(status, typ string, resets int64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": status, "rateLimitType": typ, "resetsAt": resets}})
		return b
	}
	l.observe("claude", ev("allowed", "five_hour", 1_000_600))
	if b, _ := l.blocked("claude", "sonnet", now); b {
		t.Fatal("an allowed limit blocked")
	}
	l.observe("claude", ev("rejected", "seven_day_opus", 1_000_600))
	if b, _ := l.blocked("claude", "claude-opus-5-5", now); !b {
		t.Fatal("a rejected Opus limit must block Opus")
	}
	if b, _ := l.blocked("claude", "sonnet", now); b {
		t.Fatal("a rejected Opus limit must not block Sonnet")
	}
	if b, _ := l.blocked("claude", "claude-opus-5-5", now.Add(time.Hour)); b {
		t.Fatal("a limit past its reset must not block")
	}
	l.observe("claude", ev("rejected", "five_hour", 1_000_600))
	if b, lim := l.blocked("claude", "haiku", now); !b || lim.Type != "five_hour" {
		t.Fatalf("a rejected account limit blocks every model: %v %+v", b, lim)
	}
	if b, _ := l.blocked("codex", "haiku", now); b {
		t.Fatal("another CLI is not blocked")
	}
	l.observe("claude", ev("allowed", "five_hour", 1_000_600))
	if b, _ := l.blocked("claude", "haiku", now); b {
		t.Fatal("a later allowed report clears the limit")
	}
}

// The wiring: a rate_limit chunk an agent's session publishes (agent.output) reaches the limits the turn router reads.
func TestARejectedLimitFromASessionReachesTheRouter(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := c.CreateProject(ctx, "limits", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Call(ctx, "agent.create", mustJSONInternal(AgentSpec{ProjectID: p.ID, Name: "Backend #1", Adapter: "claude", Role: "backend", Model: "sonnet"}))
	if err != nil {
		t.Fatal(err)
	}
	var a Agent
	_ = json.Unmarshal(raw, &a)
	data, _ := json.Marshal(map[string]any{"kind": "rate_limit", "data": json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","resetsAt":` +
		strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}}`)})
	if _, err := c.Bus.Publish(ctx, events.Event{Type: "agent.output", ProjectID: p.ID, AgentID: a.ID, Data: data}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, _ := c.limits.blocked("claude", "sonnet", time.Now()); b {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the rejected limit never reached the router's view")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustJSONInternal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

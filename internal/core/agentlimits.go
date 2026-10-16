package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Agent CLI rate limits (docs/model-routing.md s6). Claude Code reports its account's limits mid-turn as rate_limit_event
// lines (the parser passes them on as rate_limit chunks, published as agent.output events). When a limit's status is
// "rejected", the CLI will not run more turns on it until resetsAt, so the turn router treats that CLI's models as unavailable
// until then and picks another of the agent's models when it has one. A limit whose type names a model family (for example
// a weekly Opus limit) blocks only that family. Nothing here is stored: the next event, or the reset time, clears it.

type agentLimit struct {
	Status   string    // allowed | allowed_warning | rejected
	Type     string    // five_hour, seven_day, seven_day_opus, ...
	ResetsAt time.Time // zero when not given
}

type agentLimits struct {
	mu sync.Mutex
	m  map[string]agentLimit // by adapter and limit type
}

func (l *agentLimits) observe(adapter string, raw json.RawMessage) {
	var ev struct {
		Info struct {
			Status   string `json:"status"`
			ResetsAt int64  `json:"resetsAt"`
			Type     string `json:"rateLimitType"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Info.Status == "" {
		return
	}
	lim := agentLimit{Status: ev.Info.Status, Type: ev.Info.Type}
	if ev.Info.ResetsAt > 0 {
		lim.ResetsAt = time.Unix(ev.Info.ResetsAt, 0)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]agentLimit{}
	}
	l.m[adapter+"|"+lim.Type] = lim
}

// blocked reports whether a rejected limit of adapter covers model now, and which.
func (l *agentLimits) blocked(adapter, model string, now time.Time) (bool, agentLimit) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, lim := range l.m {
		if !strings.HasPrefix(k, adapter+"|") || lim.Status != "rejected" {
			continue
		}
		if !lim.ResetsAt.IsZero() && !now.Before(lim.ResetsAt) {
			continue
		}
		if covers(lim.Type, model) {
			return true, lim
		}
	}
	return false, agentLimit{}
}

// covers reports whether a limit of this type applies to model: a type that names a model family covers only that family.
func covers(limitType, model string) bool {
	for _, family := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(limitType, family) {
			return strings.Contains(strings.ToLower(model), family)
		}
	}
	return true
}

func init() {
	RegisterExtension(Extension{Name: "agentlimits", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ch, cancel := c.Bus.Subscribe(events.Filter{Types: []string{"agent.output"}})
		safe.Go("core.init", func() {
			for ev := range ch {
				var d struct {
					Kind string          `json:"kind"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(ev.Data, &d) != nil || d.Kind != "rate_limit" || ev.AgentID == "" {
					continue
				}
				a, err := c.agent(ctx, ev.AgentID)
				if err != nil {
					continue
				}
				c.limits.observe(a.Adapter, d.Data)
			}
		})
		return nil, cancel, nil
	}})
}

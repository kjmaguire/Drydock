package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Adapter drift (docs/connections.md s6). A parser that meets an event type it does not read passes it on as a warning chunk
// with code adapter_drift (published as an agent.output event). The first time an adapter sends a type, the core records it,
// publishes adapter.drift {adapter, kind: schema, eventType}, and the adapter's Connection shows degraded (reason
// drift:schema) until a Test connection passes. The record survives a restart.

const driftKey = "connection.drift."

// driftTypesMax bounds the event types a record lists.
const driftTypesMax = 20

func init() {
	RegisterExtension(Extension{Name: "adapterdrift", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		// events.Watch goes on after the bus dropped the watcher as slow: it replays what it missed (audit 2026-10-16, L2)
		events.Watch(ctx, c.Bus, events.Filter{Types: []string{"agent.output"}}, func(ev events.Event) {
			var d struct {
				Kind string              `json:"kind"`
				Data workflow.ErrorChunk `json:"data"`
			}
			if json.Unmarshal(ev.Data, &d) != nil || d.Kind != workflow.ChunkError || d.Data.Code != workflow.CodeAdapterDrift || ev.AgentID == "" {
				return
			}
			a, err := c.agent(ctx, ev.AgentID)
			if err != nil {
				return
			}
			c.observeDrift(ctx, a.Adapter, d.Data.Detail)
		})
		return nil, cancel, nil
	}})
}

// observeDrift records that adapter sent an event of type eventType it does not read.
func (c *Core) observeDrift(ctx context.Context, adapter, eventType string) {
	eventType = strings.TrimSpace(eventType)
	if adapter == "" || eventType == "" || len(eventType) > 100 {
		return
	}
	rec, _ := c.Connections.Drift(adapter)
	if slices.Contains(rec.Types, eventType) || len(rec.Types) >= driftTypesMax {
		return
	}
	rec.Kind = "schema"
	rec.Types = append(slices.Clone(rec.Types), eventType)
	rec.At = time.Now().UnixMilli()
	c.Connections.SetDrift(adapter, &rec)
	if b, err := json.Marshal(rec); err == nil {
		if err := c.setSetting(ctx, driftKey+adapter, string(b)); err != nil {
			c.logf("keep the adapter drift: %v", err)
		}
	}
	c.publish(ctx, "adapter.drift", "", map[string]any{"adapter": adapter, "kind": rec.Kind, "eventType": eventType})
}

// loadDrift reads the kept drift records, by adapter id.
func (c *Core) loadDrift(ctx context.Context) map[string]connections.DriftRecord {
	out := map[string]connections.DriftRecord{}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE ?`, driftKey+"%")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		var d connections.DriftRecord
		if rows.Scan(&k, &v) == nil && json.Unmarshal([]byte(v), &d) == nil {
			out[strings.TrimPrefix(k, driftKey)] = d
		}
	}
	return out
}

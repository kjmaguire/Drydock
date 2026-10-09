package responder_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
)

// A real Claude Code turn, in the room, in read-only mode, on the cheapest model. It runs only when `claude` is installed
// and logged in on this machine (the user's own login, decision D1) and is skipped, with the reason, otherwise.
func TestRealClaudeAnswersInTheRoomReadOnly(t *testing.T) {
	if os.Getenv("DRYDOCK_NO_REAL_AGENTS") == "1" {
		t.Skip("DRYDOCK_NO_REAL_AGENTS=1")
	}
	t.Setenv("DRYDOCK_FAKE_AGENT", "") // the TestMain switch must not leak into a real run
	ad := claude.New()
	det, err := ad.Detect(context.Background())
	if err != nil || det.Status != agents.StatusReady {
		t.Skipf("real claude not usable here: status=%s problems=%v err=%v", det.Status, det.Problems, err)
	}
	t.Logf("real claude %s (%s)", det.Version, det.AuthDetail)

	h := newHarness(t, responder.Options{TurnTimeout: 150 * time.Second})
	t.Setenv("DRYDOCK_FAKE_AGENT", "")
	real := &spyAdapter{Adapter: ad}
	h.rt = &rt.Runtime{PM: proc.New(), Bus: h.bus, DB: h.db, Adapters: map[string]agents.Adapter{"claude": real}}
	h.r = responder.New(responder.Deps{Room: h.room, Runtime: h.rt, Context: h.ce, DB: h.db, Bus: h.bus,
		LookupAgent: func(ctx context.Context, id string) (responder.Agent, error) {
			var a responder.Agent
			err := h.db.Reader().QueryRowContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id FROM agents WHERE id=?`, id).
				Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID)
			return a, err
		},
		Cwd: func(context.Context, room.Room) (string, error) { return h.dir, nil },
		Spec: func(a responder.Agent, base agents.RunSpec) agents.RunSpec {
			base.ExtraArgs = append(base.ExtraArgs, "--no-session-persistence")
			return base
		},
	}, responder.Options{TurnTimeout: 150 * time.Second})

	notes := filepath.Join(h.dir, "NOTES.txt")
	if err := os.WriteFile(notes, []byte("The deployment codeword is MARIGOLD-4417.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,model,created_at) VALUES('real1',?,?,?,?,?,?)`, h.project, "Haiku", "claude", "backend", "haiku", storage.Now()); err != nil {
		t.Fatal(err)
	}
	// a file outside the project: a read-only reply must not be able to read it (no tool-wide Read allow rule)
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE-SECRET-HERON-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch := h.channel("dev", "real1")
	q := h.human(ch, "@real1 Read NOTES.txt in the current directory and tell me the deployment codeword. Then try to read "+outside+" with the Read tool and quote it if you can. Answer in two short sentences.")
	ctx, cancel := context.WithTimeout(h.ctx, 170*time.Second)
	defer cancel()
	out, err := h.r.Handle(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Turns) != 1 || out.Turns[0].Status != responder.StatusDone {
		t.Fatalf("%+v", out)
	}
	ms := h.messages(ch)
	reply := ms[len(ms)-1]
	if reply.Author.Key() != "real1" || !strings.Contains(reply.Body, "MARIGOLD-4417") {
		t.Fatalf("reply %q from %s", reply.Body, reply.Author.Key())
	}
	if strings.Contains(reply.Body, "HERON-42") {
		t.Fatalf("a room reply read a file outside the project: %q", reply.Body)
	}
	var model string
	h.db.Reader().QueryRow(`SELECT model FROM model_usage WHERE agent_id='real1' ORDER BY cost_usd DESC LIMIT 1`).Scan(&model)
	if !strings.Contains(model, "haiku") {
		t.Fatalf("a haiku room reply was served by %q (plan mode makes Claude answer haiku with Sonnet)", model)
	}
	var in float64
	reply.PayloadField("input_tokens", &in)
	var rows int
	var cost float64
	h.db.Reader().QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_usd),0) FROM model_usage WHERE agent_id='real1'`).Scan(&rows, &cost)
	t.Logf("reply: %q (input tokens %.0f, usage rows %d, cost $%.5f)", reply.Body, in, rows, cost)
	if rows == 0 || in == 0 {
		t.Fatalf("usage must be recorded: rows=%d input=%.0f", rows, in)
	}
	specs := real.all()
	if len(specs) != 1 || !specs[0].ReadOnly || len(specs[0].AllowedTools) != 0 {
		t.Fatalf("a room reply is read-only, with no tool-wide allow rule: %+v", specs)
	}
	if b, _ := os.ReadFile(notes); string(b) != "The deployment codeword is MARIGOLD-4417.\n" {
		t.Fatal("a read-only room reply must not change the project")
	}
}

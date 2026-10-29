package responder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/memory"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// TestMain turns the test binary into the scripted stand-in agent when the runtime launches it (the same labeled test
// double the pipeline and core tests use: a real process speaking Claude's stream-json dialect, scripted per agent).
func TestMain(m *testing.M) {
	if os.Getenv("DRYDOCK_FAKE_AGENT") == "1" {
		os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

// spyAdapter records the RunSpec of every session the responder starts, then delegates.
type spyAdapter struct {
	agents.Adapter
	mu    sync.Mutex
	specs []agents.RunSpec
}

func (s *spyAdapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return s.Adapter.Start(ctx, pm, spec)
}

func (s *spyAdapter) all() []agents.RunSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agents.RunSpec(nil), s.specs...)
}

type harness struct {
	t       *testing.T
	ctx     context.Context
	dir     string // project root: what agents read
	db      *storage.DB
	bus     *events.SQLBus
	room    *room.Service
	rt      *rt.Runtime
	ce      *ctxeng.Engine
	spy     *spyAdapter
	project string
	scripts map[string]string
	logs    map[string]string
	r       *responder.Responder
	gate    func(ctx context.Context, project string, a responder.Agent) (func(), error)
	budget  func(ctx context.Context, project, task string, a responder.Agent) error
	route   func(ctx context.Context, t responder.ReplyTurn, attempt responder.Attempt) error // the model router's hook, when a test sets it
	costs   map[string]float64
	mu      sync.Mutex
}

func newHarness(t *testing.T, opt responder.Options) *harness {
	t.Helper()
	ctx := context.Background()
	h := &harness{t: t, ctx: ctx, dir: t.TempDir(), project: "prj_1", scripts: map[string]string{}, logs: map[string]string{}, costs: map[string]float64{}}
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.db = db
	h.bus = events.NewSQLBus(db, 4096)
	t.Cleanup(func() { h.bus.Close(); db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES(?,?,?,?)`, h.project, "demo", h.dir, storage.Now()); err != nil {
		t.Fatal(err)
	}
	sc, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	h.room = room.New(db, h.bus, room.Options{Redact: func(b string) (string, error) { return sc.Redact(b), nil }, OnError: func(err error) { t.Logf("room: %v", err) }})
	t.Setenv("DRYDOCK_FAKE_AGENT", "1")
	h.spy = &spyAdapter{Adapter: &claude.Adapter{Exe: os.Args[0]}}
	h.rt = &rt.Runtime{PM: proc.New(), Bus: h.bus, DB: db, Adapters: map[string]agents.Adapter{"claude": h.spy}, NudgeWait: time.Second, Grants: h.room}
	h.ce = &ctxeng.Engine{DB: db, Memory: memory.Store{DB: db}}
	h.r = responder.New(responder.Deps{
		Room: h.room, Runtime: h.rt, Context: h.ce, DB: db, Bus: h.bus,
		LookupAgent: func(ctx context.Context, id string) (responder.Agent, error) {
			var a responder.Agent
			err := db.Reader().QueryRowContext(ctx, `SELECT id,project_id,name,adapter,role,model,effort,provider_id FROM agents WHERE id=?`, id).
				Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model, &a.Effort, &a.ProviderID)
			return a, err
		},
		Cwd: func(ctx context.Context, r room.Room) (string, error) { return h.dir, nil },
		Budget: func(ctx context.Context, p, task string, a responder.Agent) error {
			if h.budget != nil {
				return h.budget(ctx, p, task, a)
			}
			return nil
		},
		Route: func(ctx context.Context, t responder.ReplyTurn, attempt responder.Attempt) error {
			if h.route != nil {
				return h.route(ctx, t, attempt)
			}
			return attempt(ctx, responder.Routed{}, nil)
		},
		Gate: func(ctx context.Context, p string, a responder.Agent) (func(), error) {
			if h.gate != nil {
				return h.gate(ctx, p, a)
			}
			return func() {}, nil
		},
		OnCost: func(ctx context.Context, taskID string, usd float64) {
			h.mu.Lock()
			h.costs[taskID] += usd
			h.mu.Unlock()
		},
		Spec: func(a responder.Agent, base agents.RunSpec) agents.RunSpec {
			base.Env = append(base.Env, "DRYDOCK_FAKE_SCRIPT="+h.scripts[a.ID], "DRYDOCK_FAKE_PROMPT_LOG="+h.logs[a.ID])
			return base
		},
		Logf: func(f string, a ...any) { t.Logf("responder: "+f, a...) },
	}, opt)
	return h
}

var kyle = room.Human("kyle")

// agent inserts a team member with a script for the stand-in process.
func (h *harness) agent(id, name, role string, script fakeagent.Script) string {
	h.t.Helper()
	if _, err := h.db.Writer().Exec(`INSERT INTO agents(id,project_id,name,adapter,role,model,created_at) VALUES(?,?,?,?,?,?,?)`, id, h.project, name, "claude", role, "fake-1", storage.Now()); err != nil {
		h.t.Fatal(err)
	}
	b, _ := json.Marshal(script)
	dir := h.t.TempDir()
	sp := filepath.Join(dir, id+".json")
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.scripts[id] = sp
	h.logs[id] = filepath.Join(dir, id+".prompts")
	return id
}

func (h *harness) channel(name string, members ...string) room.Room {
	h.t.Helper()
	r, err := h.room.CreateChannel(h.ctx, h.project, name, "", kyle)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, id := range members {
		var role string
		h.db.Reader().QueryRow(`SELECT role FROM agents WHERE id=?`, id).Scan(&role)
		if err := h.room.AddMember(h.ctx, r.ID, room.Agent(id), role, room.StateActive, kyle); err != nil {
			h.t.Fatal(err)
		}
	}
	return r
}

func (h *harness) human(r room.Room, body string) room.Message {
	h.t.Helper()
	p, err := h.room.Post(h.ctx, room.PostParams{RoomID: r.ID, Author: kyle, Body: body})
	if err != nil {
		h.t.Fatal(err)
	}
	return p.Message
}

func (h *harness) prompts(agentID string) []string {
	b, _ := os.ReadFile(h.logs[agentID])
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s string
		if json.Unmarshal([]byte(l), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func (h *harness) messages(r room.Room) []room.Message {
	h.t.Helper()
	ms, err := h.room.Messages(h.ctx, r.ID, 0, 500)
	if err != nil {
		h.t.Fatal(err)
	}
	return ms
}

func say(s string) fakeagent.Script {
	return fakeagent.Script{Default: []fakeagent.Step{{Say: s}}}
}

func authors(ms []room.Message) string {
	var out []string
	for _, m := range ms {
		out = append(out, fmt.Sprintf("%s:%s", m.Author.Key(), m.Type))
	}
	return strings.Join(out, " ")
}

// setMode changes the room mode as the human, reading the current version (adding members bumps it).
func (h *harness) setMode(r room.Room, mode room.Mode, params room.ModeParams) {
	h.t.Helper()
	cur, err := h.room.GetRoom(h.ctx, r.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.room.SetMode(h.ctx, r.ID, mode, params, cur.Version, kyle); err != nil {
		h.t.Fatal(err)
	}
}

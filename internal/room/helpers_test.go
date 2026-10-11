package room

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

type env struct {
	t       *testing.T
	ctx     context.Context
	db      *storage.DB
	bus     *events.SQLBus
	svc     *Service
	project string
	errs    chan error
	n       atomic.Int32
}

func newEnv(t *testing.T) *env {
	t.Helper()
	t.Parallel() // each test owns its database file
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	e := &env{t: t, ctx: ctx, db: db, bus: bus, project: "proj_1", errs: make(chan error, 64)}
	e.svc = New(db, bus, Options{OnError: func(err error) { e.errs <- err }})
	t.Cleanup(func() { bus.Close(); db.Close() })
	e.exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES(?,?,?,?)`, e.project, "demo", "/tmp/demo", storage.Now())
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("exec %q: %v", q, err)
	}
}

// agent inserts an agents row and returns its id.
func (e *env) agent(name string) string { return e.agentIn(e.project, name) }

func (e *env) agentIn(project, name string) string {
	id := fmt.Sprintf("agent_%s_%d", name, e.n.Add(1))
	e.exec(`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES(?,?,?,?,?,?)`, id, project, name, "fake", "dev", storage.Now())
	return id
}

var kyle = Human("kyle")

func (e *env) channel(name string) Room {
	e.t.Helper()
	r, err := e.svc.CreateChannel(e.ctx, e.project, name, "", kyle)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) join(r Room, a Author, role string) {
	e.t.Helper()
	if err := e.svc.AddMember(e.ctx, r.ID, a, role, StateActive, kyle); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) say(r Room, a Author, body string) Message {
	e.t.Helper()
	p, err := e.svc.Post(e.ctx, PostParams{RoomID: r.ID, Author: a, Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Message
}

func (e *env) post(p PostParams) Posted {
	e.t.Helper()
	out, err := e.svc.Post(e.ctx, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) events(types ...string) []events.Event {
	e.t.Helper()
	evs, err := e.bus.Replay(e.ctx, 0, events.Filter{Types: types}, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	return evs
}

func (e *env) noErrs() {
	e.t.Helper()
	select {
	case err := <-e.errs:
		e.t.Fatalf("service reported: %v", err)
	default:
	}
}

func (e *env) approval(id, kind, status, decidedBy string) {
	e.exec(`INSERT INTO approvals(id,task_id,project_id,kind,title,action_hash,status,decision,decided_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, "", e.project, kind, "Run rm -rf build", "hash_"+id, status, "", decidedBy, storage.Now())
}

func eventually(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func evTask(typ, taskID string) events.Event { return events.Event{Type: typ, TaskID: taskID} }

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

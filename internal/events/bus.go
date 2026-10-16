// Package events is Drydock's in-process event bus. Publish persists to SQLite (group commit,
// monotonic sequence) and then fans out to subscribers; slow subscribers never block publishers.
// It has no dependency on Wails: the UI is just another subscriber that replays from its last seen sequence.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Event is an append-only fact. Data is JSON.
type Event struct {
	Seq       uint64          `json:"seq"`
	Time      time.Time       `json:"time"`
	Type      string          `json:"type"`
	ProjectID string          `json:"projectId,omitempty"`
	RunID     string          `json:"runId,omitempty"`
	TaskID    string          `json:"taskId,omitempty"`
	AgentID   string          `json:"agentId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Filter selects events. Empty fields match everything. A Type ending in ".*" or "." matches by prefix
// (for example "workflow.*" matches "workflow.step_started"); otherwise the match is exact.
type Filter struct {
	Types     []string
	ProjectID string
	RunID     string
	TaskID    string
	AgentID   string
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Event) bool {
	if f.ProjectID != "" && e.ProjectID != f.ProjectID {
		return false
	}
	if f.RunID != "" && e.RunID != f.RunID {
		return false
	}
	if f.TaskID != "" && e.TaskID != f.TaskID {
		return false
	}
	if f.AgentID != "" && e.AgentID != f.AgentID {
		return false
	}
	if len(f.Types) == 0 {
		return true
	}
	for _, t := range f.Types {
		if matchType(t, e.Type) {
			return true
		}
	}
	return false
}

func matchType(pattern, typ string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(typ, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == typ
}

// Bus is the event bus contract (brief section 19).
type Bus interface {
	// Publish persists e, assigns its sequence number, then fans out. It returns after the event is durable.
	Publish(ctx context.Context, e Event) (uint64, error)
	// Subscribe returns a channel of live events matching f. If the subscriber falls behind its buffer the
	// channel is closed (without the cancel func being called): the subscriber must Replay from its last seq and
	// subscribe again.
	Subscribe(f Filter) (<-chan Event, func())
	// Replay returns persisted events with seq > after matching f, oldest first, at most limit (0 = 1000).
	Replay(ctx context.Context, after uint64, f Filter, limit int) ([]Event, error)
}

// ErrClosed is returned by Publish after Close.
var ErrClosed = errors.New("events: bus closed")

type request struct {
	e     Event
	reply chan result
}
type result struct {
	seq uint64
	err error
}

type subscriber struct {
	f    Filter
	ch   chan Event
	done bool
}

// SQLBus is the SQLite-backed Bus.
type SQLBus struct {
	db   *storage.DB
	in   chan request
	quit chan struct{}
	// stopped is closed when the writer has exited: a request queued after its last drain is never written
	stopped chan struct{}
	wg      sync.WaitGroup
	mu      sync.RWMutex
	subs    map[*subscriber]struct{}
	bufSz   int
	once    sync.Once
	// redactor, when set, rewrites an event before it is stored and fanned out (SetRedactor).
	redactor atomic.Pointer[func(Event) Event]
}

// SetRedactor installs a function that rewrites every event before it is persisted and delivered: the place where secrets in
// agent and tool output are removed before they reach the database, a subscriber or the UI (docs/security.md s8; audit
// 2026-10-16, H7). The function must be fast for events it does not change and must not publish. Nil removes it.
func (b *SQLBus) SetRedactor(f func(Event) Event) {
	if f == nil {
		b.redactor.Store(nil)
		return
	}
	b.redactor.Store(&f)
}

// NewSQLBus starts the group-commit writer. bufSize is the per-subscriber channel capacity.
func NewSQLBus(db *storage.DB, bufSize int) *SQLBus {
	if bufSize <= 0 {
		bufSize = 1024
	}
	b := &SQLBus{db: db, in: make(chan request, 4096), quit: make(chan struct{}), stopped: make(chan struct{}), subs: map[*subscriber]struct{}{}, bufSz: bufSize}
	b.wg.Add(1)
	safe.Go("events.NewSQLBus", func() { b.writer() })
	return b
}

// Close stops the writer after draining queued events.
func (b *SQLBus) Close() {
	b.once.Do(func() {
		close(b.quit)
		b.wg.Wait()
		b.mu.Lock()
		for s := range b.subs {
			if !s.done {
				s.done = true
				close(s.ch)
			}
		}
		b.subs = map[*subscriber]struct{}{}
		b.mu.Unlock()
	})
}

func (b *SQLBus) Publish(ctx context.Context, e Event) (uint64, error) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage(`{}`)
	}
	if f := b.redactor.Load(); f != nil {
		e = (*f)(e)
	}
	r := request{e: e, reply: make(chan result, 1)}
	select {
	case <-b.quit:
		return 0, ErrClosed
	default:
	}
	select {
	case b.in <- r:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-b.quit:
		return 0, ErrClosed
	}
	select {
	case res := <-r.reply:
		return res.seq, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-b.stopped:
		// the writer exited: either it wrote this request in its last drain (the reply is waiting) or the request arrived
		// after it, and nothing will ever answer it
		select {
		case res := <-r.reply:
			return res.seq, res.err
		default:
			return 0, ErrClosed
		}
	}
}

// writer batches queued events into one transaction (group commit), then fans out in sequence order.
func (b *SQLBus) writer() {
	defer b.wg.Done()
	defer close(b.stopped)
	for {
		var first request
		select {
		case first = <-b.in:
		case <-b.quit:
			// drain what is queued
			for {
				select {
				case r := <-b.in:
					b.flushSafely([]request{r})
				default:
					return
				}
			}
		}
		batch := []request{first}
	collect:
		for len(batch) < 256 {
			select {
			case r := <-b.in:
				batch = append(batch, r)
			default:
				break collect
			}
		}
		b.flushSafely(batch)
	}
}

// flushSafely writes a batch and, if that panics, answers every caller of the batch with the error instead of leaving them
// waiting for a writer that has ended (audit 2026-10-16, H9). The writer goes on with the next batch.
func (b *SQLBus) flushSafely(batch []request) {
	if err := safe.Do("events.flush", func() { b.flush(batch) }); err != nil {
		for _, r := range batch {
			select {
			case r.reply <- result{err: err}:
			default: // already answered before the panic
			}
		}
	}
}

func (b *SQLBus) flush(batch []request) {
	seqs := make([]uint64, len(batch))
	err := b.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO events(ts,type,project_id,run_id,task_id,agent_id,data) VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for i, r := range batch {
			res, err := stmt.Exec(r.e.Time.UnixMilli(), r.e.Type, r.e.ProjectID, r.e.RunID, r.e.TaskID, r.e.AgentID, []byte(r.e.Data))
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			seqs[i] = uint64(id)
		}
		return nil
	})
	if err != nil {
		for _, r := range batch {
			r.reply <- result{err: err}
		}
		return
	}
	for i, r := range batch {
		e := r.e
		e.Seq = seqs[i]
		b.fanout(e)
		r.reply <- result{seq: e.Seq}
	}
}

func (b *SQLBus) fanout(e Event) {
	b.mu.RLock()
	var lagged []*subscriber
	for s := range b.subs {
		if s.done || !s.f.Match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			lagged = append(lagged, s)
		}
	}
	b.mu.RUnlock()
	if len(lagged) > 0 {
		b.mu.Lock()
		for _, s := range lagged {
			if !s.done {
				s.done = true
				close(s.ch)
			}
			delete(b.subs, s)
		}
		b.mu.Unlock()
	}
}

func (b *SQLBus) Subscribe(f Filter) (<-chan Event, func()) {
	s := &subscriber{f: f, ch: make(chan Event, b.bufSz)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if !s.done {
			s.done = true
			close(s.ch)
		}
		delete(b.subs, s)
		b.mu.Unlock()
	}
	return s.ch, cancel
}

func (b *SQLBus) Replay(ctx context.Context, after uint64, f Filter, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT seq,ts,type,project_id,run_id,task_id,agent_id,data FROM events WHERE seq > ?`
	args := []any{after}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.RunID != "" {
		q += ` AND run_id = ?`
		args = append(args, f.RunID)
	}
	if f.TaskID != "" {
		q += ` AND task_id = ?`
		args = append(args, f.TaskID)
	}
	if f.AgentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, f.AgentID)
	}
	if len(f.Types) > 0 {
		q += ` AND (`
		for i, t := range f.Types {
			if i > 0 {
				q += ` OR `
			}
			if strings.HasSuffix(t, "*") {
				q += `type LIKE ? ESCAPE '\'`
				args = append(args, escapeLike(strings.TrimSuffix(t, "*"))+"%")
			} else {
				q += `type = ?`
				args = append(args, t)
			}
		}
		q += `)`
	}
	q += ` ORDER BY seq LIMIT ?`
	args = append(args, limit)
	rows, err := b.db.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		var data []byte
		if err := rows.Scan(&e.Seq, &ts, &e.Type, &e.ProjectID, &e.RunID, &e.TaskID, &e.AgentID, &data); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts).UTC()
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// LastSeq returns the highest persisted sequence number (0 if none).
func (b *SQLBus) LastSeq(ctx context.Context) (uint64, error) {
	var n sql.NullInt64
	if err := b.db.Reader().QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&n); err != nil {
		return 0, err
	}
	return uint64(n.Int64), nil
}

// Emit is a convenience that marshals data and publishes.
func Emit(ctx context.Context, b Bus, typ string, scope Event, data any) (uint64, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	scope.Type = typ
	scope.Data = raw
	return b.Publish(ctx, scope)
}

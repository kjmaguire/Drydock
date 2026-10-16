package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Options tunes a Service. The zero value is valid.
type Options struct {
	// Redact runs the secret scan over every inbound body (security.md s8). Returning an error blocks the message.
	Redact func(body string) (string, error)
	// OnError receives non-fatal failures, such as a bus publish after the message was already committed.
	OnError func(error)
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Service is the Engineering Room over SQLite and the event bus. It is safe for concurrent use: all writes go through
// the storage single writer, and events are published only after the write transaction commits (the bus shares that
// writer, so publishing inside a transaction would deadlock).
type Service struct {
	db  *storage.DB
	bus events.Bus
	opt Options
}

// New returns a Service. bus may be nil, in which case nothing is published (messages keep event_seq 0 until Reconcile).
func New(db *storage.DB, bus events.Bus, opt Options) *Service {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Service{db: db, bus: bus, opt: opt}
}

func (s *Service) nowMs() int64 { return s.opt.Now().UTC().UnixMilli() }

func (s *Service) report(err error) {
	if err != nil && s.opt.OnError != nil {
		s.opt.OnError(err)
	}
}

type pendingEvent struct {
	typ   string
	scope events.Event
	data  any
	msgID string // set for room.message_posted: its bus seq is recorded on the message row
}

// flush publishes events after the transaction, then records the bus sequence on message rows.
func (s *Service) flush(ctx context.Context, evs []pendingEvent) {
	if s.bus == nil || len(evs) == 0 {
		return
	}
	type rec struct {
		id  string
		seq uint64
	}
	var recs []rec
	for _, p := range evs {
		seq, err := events.Emit(ctx, s.bus, p.typ, p.scope, p.data)
		if err != nil {
			s.report(fmt.Errorf("room: publish %s: %w", p.typ, err))
			continue
		}
		if p.msgID != "" {
			recs = append(recs, rec{p.msgID, seq})
		}
	}
	if len(recs) == 0 {
		return
	}
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, r := range recs {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET event_seq=? WHERE id=? AND event_seq=0`, r.seq, r.id); err != nil {
				return err
			}
		}
		return nil
	})
	s.report(err)
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

type scanner interface{ Scan(dest ...any) error }

func fromMs(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

const roomCols = `id,project_id,kind,name,topic,mode,mode_params,task_id,archived,archived_at,created_by,created_at,version`

func scanRoom(sc scanner) (Room, error) {
	var r Room
	var kind, mode, params string
	var archived int
	var archivedAt, createdAt int64
	if err := sc.Scan(&r.ID, &r.ProjectID, &kind, &r.Name, &r.Topic, &mode, &params, &r.TaskID, &archived, &archivedAt, &r.CreatedBy, &createdAt, &r.Version); err != nil {
		return Room{}, err
	}
	r.Kind, r.Mode = Kind(kind), Mode(mode)
	_ = json.Unmarshal([]byte(params), &r.ModeParams)
	r.Archived = archived != 0
	r.ArchivedAt, r.CreatedAt = fromMs(archivedAt), fromMs(createdAt)
	return r, nil
}

func getRoom(ctx context.Context, q querier, id string) (Room, error) {
	r, err := scanRoom(q.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("%w: room %s", ErrNotFound, id)
	}
	return r, err
}

// GetRoom returns one room.
func (s *Service) GetRoom(ctx context.Context, id string) (Room, error) {
	return getRoom(ctx, s.db.Reader(), id)
}

// ListRooms returns a project's rooms ordered by kind and name.
func (s *Service) ListRooms(ctx context.Context, projectID string, includeArchived bool) ([]Room, error) {
	q := `SELECT ` + roomCols + ` FROM rooms WHERE project_id=?`
	if !includeArchived {
		q += ` AND archived=0`
	}
	rows, err := s.db.Reader().QueryContext(ctx, q+` ORDER BY kind, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		r, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RoomByTask returns the task room for a task id.
func (s *Service) RoomByTask(ctx context.Context, taskID string) (Room, error) {
	r, err := scanRoom(s.db.Reader().QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE kind='task' AND task_id<>'' AND task_id=?`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("%w: task room for %s", ErrNotFound, taskID)
	}
	return r, err
}

type memberSpec struct {
	a     Author
	role  string
	state MemberState
}

func (m memberSpec) state0() MemberState {
	if m.state == "" {
		return StateActive
	}
	return m.state
}

func validState(st MemberState) bool {
	return st == StateActive || st == StateObserver || st == StateMuted
}

var channelNameRE = nameRE

// createRoom inserts a room and its members unless find reports an existing one. Events are published after commit.
func (s *Service) createRoom(ctx context.Context, r Room, members []memberSpec, by Author, find func(tx *sql.Tx) (Room, error)) (Room, bool, error) {
	if err := by.Validate(); err != nil {
		return Room{}, false, err
	}
	if by.Kind == AuthorAgent || by.Kind == AuthorTool {
		return Room{}, false, fmt.Errorf("%w: rooms are created by humans, rules or the system", ErrForbidden)
	}
	var created bool
	var evs []pendingEvent
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		created, evs = false, nil
		if find != nil {
			ex, err := find(tx)
			if err == nil {
				r = ex
				return nil
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		now := s.nowMs()
		r.CreatedAt = fromMs(now)
		r.Version = 1
		params, _ := json.Marshal(r.ModeParams)
		if _, err := tx.ExecContext(ctx, `INSERT INTO rooms(id,project_id,kind,name,topic,mode,mode_params,task_id,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.ProjectID, string(r.Kind), r.Name, r.Topic, string(r.Mode), string(params), r.TaskID, by.Key(), now); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: room %q already exists", ErrConflict, r.Name)
			}
			return err
		}
		for _, m := range members {
			if err := insertMemberTx(ctx, tx, r, m, by, now); err != nil {
				return err
			}
		}
		created = true
		scope := events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}
		evs = append(evs, pendingEvent{typ: "room.created", scope: scope, data: map[string]any{"room": r.ID, "kind": r.Kind, "name": r.Name}})
		for _, m := range members {
			evs = append(evs, memberAdded(scope, r.ID, m))
		}
		return nil
	})
	if err != nil {
		return Room{}, false, err
	}
	if created {
		s.flush(ctx, evs)
	}
	return r, created, nil
}

func memberAdded(scope events.Event, roomID string, m memberSpec) pendingEvent {
	return pendingEvent{typ: "room.member_added", scope: scope, data: map[string]any{"room": roomID, "member": m.a.Key(), "kind": m.a.Kind, "role": m.role, "state": m.state0()}}
}

func insertMemberTx(ctx context.Context, tx *sql.Tx, r Room, m memberSpec, by Author, now int64) error {
	if err := m.a.Validate(); err != nil {
		return err
	}
	if m.a.Kind == AuthorSystem {
		return fmt.Errorf("%w: system cannot be a member", ErrInvalid)
	}
	if !validState(m.state0()) {
		return fmt.Errorf("%w: member state %q", ErrInvalid, m.state)
	}
	switch m.a.Kind {
	case AuthorAgent:
		var proj string
		err := tx.QueryRowContext(ctx, `SELECT project_id FROM agents WHERE id=?`, m.a.ID).Scan(&proj)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && proj != r.ProjectID) {
			return fmt.Errorf("%w: %s", ErrUnknownAgent, m.a.ID)
		}
		if err != nil {
			return err
		}
	case AuthorTool:
		if _, ok := ToolByName(m.a.ID); !ok {
			return fmt.Errorf("%w: unknown tool member %q", ErrInvalid, m.a.ID)
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO room_members(room_id,member_id,kind,role,state,added_by,added_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(room_id,member_id) DO NOTHING`, r.ID, m.a.Key(), string(m.a.Kind), m.role, string(m.state0()), by.Key(), now)
	if err != nil {
		return err
	}
	_ = res
	return nil
}

// CreateChannel creates a project channel (a human action).
func (s *Service) CreateChannel(ctx context.Context, projectID, name, topic string, by Author) (Room, error) {
	if !channelNameRE.MatchString(name) {
		return Room{}, fmt.Errorf("%w: channel name %q", ErrInvalid, name)
	}
	r := Room{ID: ids.New("room"), ProjectID: projectID, Kind: KindChannel, Name: name, Topic: topic, Mode: ModeAuto}
	var mem []memberSpec
	if by.Kind == AuthorHuman {
		mem = append(mem, memberSpec{a: by, role: "owner"})
	}
	r, _, err := s.createRoom(ctx, r, mem, by, func(tx *sql.Tx) (Room, error) {
		return scanRoomErr(tx.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE project_id=? AND kind='channel' AND name=?`, projectID, name))
	})
	return r, err
}

func scanRoomErr(row *sql.Row) (Room, error) {
	r, err := scanRoom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	return r, err
}

// DefaultChannels are created for every project.
var DefaultChannels = []struct{ Name, Topic string }{
	{"general", "Project-wide discussion"},
	{"reviews", "Review requests and verdicts"},
	{"decisions", "Decisions and ADR candidates"},
	{"alerts", "Backup, restore and license notices"},
}

// EnsureDefaultChannels creates #general, #reviews, #decisions and #alerts if missing (idempotent).
func (s *Service) EnsureDefaultChannels(ctx context.Context, projectID string, by Author) ([]Room, error) {
	var out []Room
	for _, d := range DefaultChannels {
		r, err := s.CreateChannel(ctx, projectID, d.Name, d.Topic, by)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// TaskChannelSpec describes the room created with a task.
type TaskChannelSpec struct {
	ProjectID string
	TaskID    string
	Number    int    // task number for the name; 0 derives it from the id
	Title     string // slug source
	Owner     string // agent id of the assignee (role "owner"), optional
	Assignees []string
	Reviewers []string
	CreatedBy Author
}

// TaskChannelName is "task-<n>-<slug>" (room-protocol s1 names it task-<id>; the number form is the owner's request).
func TaskChannelName(taskID string, number int, title string) string {
	n := fmt.Sprint(number)
	if number <= 0 {
		n = strings.TrimPrefix(taskID, "task_")
	}
	name := "task-" + n
	if sl := slug(title, 40); sl != "" {
		name += "-" + sl
	}
	return name
}

// CreateTaskChannel creates the room for a task with its owner, assignees, reviewers and every tool member
// (room-protocol s10 b). Idempotent by task id.
func (s *Service) CreateTaskChannel(ctx context.Context, sp TaskChannelSpec) (Room, error) {
	if sp.TaskID == "" || sp.ProjectID == "" {
		return Room{}, fmt.Errorf("%w: task channel needs project and task ids", ErrInvalid)
	}
	r := Room{ID: ids.New("room"), ProjectID: sp.ProjectID, Kind: KindTask, Name: TaskChannelName(sp.TaskID, sp.Number, sp.Title), Topic: sp.Title, Mode: ModeAuto, TaskID: sp.TaskID}
	seen := map[string]bool{}
	var mem []memberSpec
	add := func(a Author, role string) {
		if !seen[a.Key()] {
			seen[a.Key()] = true
			mem = append(mem, memberSpec{a: a, role: role})
		}
	}
	if sp.CreatedBy.Kind == AuthorHuman {
		add(sp.CreatedBy, "owner")
	}
	if sp.Owner != "" {
		add(Agent(sp.Owner), "owner")
	}
	for _, a := range sp.Assignees {
		add(Agent(a), "assignee")
	}
	for _, a := range sp.Reviewers {
		add(Agent(a), "reviewer")
	}
	for _, t := range ToolMembers {
		add(Tool(t.Name), "tool")
	}
	r, _, err := s.createRoom(ctx, r, mem, sp.CreatedBy, func(tx *sql.Tx) (Room, error) {
		return scanRoomErr(tx.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE kind='task' AND task_id<>'' AND task_id=?`, sp.TaskID))
	})
	return r, err
}

// ArchiveTaskChannel archives a task's room on completion, cancellation or failure; messages are kept.
func (s *Service) ArchiveTaskChannel(ctx context.Context, taskID, outcome string) error {
	r, err := s.RoomByTask(ctx, taskID)
	if err != nil {
		return err
	}
	return s.ArchiveRoom(ctx, r.ID, System, "task "+outcome)
}

// WatchTasks archives task rooms when task.completed, task.cancelled or task.failed events arrive, until ctx ends.
// The event names follow brief section 12; no task service publishes them yet.
func (s *Service) WatchTasks(ctx context.Context) error {
	if s.bus == nil {
		return errors.New("room: no bus")
	}
	ch, cancel := s.bus.Subscribe(events.Filter{Types: []string{"task.completed", "task.cancelled", "task.failed"}})
	safe.Go("room.Service.WatchTasks", func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.TaskID == "" {
					continue
				}
				if err := s.ArchiveTaskChannel(ctx, e.TaskID, strings.TrimPrefix(e.Type, "task.")); err != nil && !errors.Is(err, ErrNotFound) {
					s.report(err)
				}
			}
		}
	})
	return nil
}

// CreateDM creates (or returns) the DM between one human and one agent.
func (s *Service) CreateDM(ctx context.Context, projectID string, human, agent Author) (Room, error) {
	if human.Kind != AuthorHuman || agent.Kind != AuthorAgent {
		return Room{}, fmt.Errorf("%w: a DM is a human and an agent", ErrInvalid)
	}
	name := "dm:" + human.Key() + "+" + agent.Key()
	r := Room{ID: ids.New("room"), ProjectID: projectID, Kind: KindDM, Name: name, Mode: ModeAuto}
	r, _, err := s.createRoom(ctx, r, []memberSpec{{a: human, role: "owner"}, {a: agent}}, human, func(tx *sql.Tx) (Room, error) {
		return scanRoomErr(tx.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE project_id=? AND kind='dm' AND name=?`, projectID, name))
	})
	return r, err
}

// CreateGroup creates an ad hoc group chat with an explicit member list.
func (s *Service) CreateGroup(ctx context.Context, projectID, name string, members []Author, by Author) (Room, error) {
	if !channelNameRE.MatchString(name) {
		return Room{}, fmt.Errorf("%w: group name %q", ErrInvalid, name)
	}
	r := Room{ID: ids.New("room"), ProjectID: projectID, Kind: KindGroup, Name: name, Mode: ModeSequential}
	seen := map[string]bool{}
	var mem []memberSpec
	for _, m := range append([]Author{by}, members...) {
		if m.Kind == AuthorSystem || seen[m.Key()] {
			continue
		}
		seen[m.Key()] = true
		mem = append(mem, memberSpec{a: m})
	}
	r, _, err := s.createRoom(ctx, r, mem, by, nil)
	return r, err
}

// ConvertGroupToChannel turns a group chat into a channel, optionally renaming it. It is a compare-and-swap on version.
func (s *Service) ConvertGroupToChannel(ctx context.Context, roomID, newName string, expectedVersion int, by Author) (Room, error) {
	if by.Kind != AuthorHuman {
		return Room{}, fmt.Errorf("%w: only a human converts a group", ErrForbidden)
	}
	if newName != "" && !channelNameRE.MatchString(newName) {
		return Room{}, fmt.Errorf("%w: channel name %q", ErrInvalid, newName)
	}
	var r Room
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		cur, err := getRoom(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if cur.Kind != KindGroup || cur.Archived {
			return fmt.Errorf("%w: only an active group converts", ErrInvalid)
		}
		name := cur.Name
		if newName != "" {
			name = newName
		}
		res, err := tx.ExecContext(ctx, `UPDATE rooms SET kind='channel', name=?, version=version+1 WHERE id=? AND version=?`, name, roomID, expectedVersion)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: channel %q exists", ErrConflict, name)
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: room %s is at version %d", ErrConflict, roomID, cur.Version)
		}
		r, err = getRoom(ctx, tx, roomID)
		return err
	})
	if err != nil {
		return Room{}, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.kind_changed", scope: events.Event{ProjectID: r.ProjectID}, data: map[string]any{"room": r.ID, "old": KindGroup, "new": KindChannel}}})
	_, err = s.Post(ctx, PostParams{RoomID: r.ID, Type: TypeSystem, Author: System, Body: "Group converted to channel #" + r.Name,
		Payload: map[string]any{"event": "kind_changed", "params": map[string]any{"by": by.Key()}}})
	return r, err
}

// SetMode changes the room mode with compare-and-swap on version (a human action).
func (s *Service) SetMode(ctx context.Context, roomID string, mode Mode, params ModeParams, expectedVersion int, by Author) (Room, error) {
	if by.Kind != AuthorHuman {
		return Room{}, fmt.Errorf("%w: only a human changes the mode", ErrForbidden)
	}
	if mode != ModeAuto && mode != ModeSequential && mode != ModeModerator {
		return Room{}, fmt.Errorf("%w: mode %q", ErrInvalid, mode)
	}
	pj, _ := json.Marshal(params)
	var old, r Room
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if old, err = getRoom(ctx, tx, roomID); err != nil {
			return err
		}
		if old.Archived {
			return ErrArchived
		}
		res, err := tx.ExecContext(ctx, `UPDATE rooms SET mode=?, mode_params=?, version=version+1 WHERE id=? AND version=?`, string(mode), string(pj), roomID, expectedVersion)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: room %s is at version %d", ErrConflict, roomID, old.Version)
		}
		r, err = getRoom(ctx, tx, roomID)
		return err
	})
	if err != nil {
		return Room{}, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.mode_changed", scope: events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}, data: map[string]any{"room": r.ID, "old": old.Mode, "new": mode}}})
	_, err = s.Post(ctx, PostParams{RoomID: r.ID, Type: TypeSystem, Author: System, Body: fmt.Sprintf("Mode changed from %s to %s", old.Mode, mode),
		Payload: map[string]any{"event": "mode_changed", "params": map[string]any{"old": old.Mode, "new": mode, "by": by.Key()}}})
	return r, err
}

// ArchiveRoom archives a room (idempotent). A SYSTEM message is posted first so the transcript records why.
func (s *Service) ArchiveRoom(ctx context.Context, roomID string, by Author, reason string) error {
	r, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return err
	}
	if r.Archived {
		return nil
	}
	if by.Kind == AuthorAgent || by.Kind == AuthorTool {
		return fmt.Errorf("%w: agents do not archive rooms", ErrForbidden)
	}
	if _, err := s.Post(ctx, PostParams{RoomID: roomID, Type: TypeSystem, Author: System, Body: "Room archived: " + reason,
		Payload: map[string]any{"event": "archived", "params": map[string]any{"reason": reason, "by": by.Key()}}}); err != nil && !errors.Is(err, ErrArchived) {
		return err
	}
	var changed bool
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE rooms SET archived=1, archived_at=?, version=version+1 WHERE id=? AND archived=0`, s.nowMs(), roomID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		changed = n > 0
		return nil
	})
	if err != nil || !changed {
		return err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.archived", scope: events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}, data: map[string]any{"room": r.ID, "kind": r.Kind, "reason": reason}}})
	return nil
}

// AddMember adds a member. Agents never join on their own (room-protocol s10): by must be a human, the system, or a
// stored rule, and the agent must belong to the room's project. A DM holds exactly two members.
func (s *Service) AddMember(ctx context.Context, roomID string, member Author, role string, state MemberState, by Author) error {
	if err := by.Validate(); err != nil {
		return err
	}
	if by.Kind == AuthorAgent || by.Kind == AuthorTool {
		return fmt.Errorf("%w: only a human, a stored rule or the system adds members", ErrForbidden)
	}
	var r Room
	var added bool
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		added = false
		var err error
		if r, err = getRoom(ctx, tx, roomID); err != nil {
			return err
		}
		if r.Archived {
			return ErrArchived
		}
		if r.Kind == KindDM {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id=? AND member_id<>?`, roomID, member.Key()).Scan(&n); err != nil {
				return err
			}
			if n >= 2 {
				return ErrDMFull
			}
		}
		var had int
		_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id=? AND member_id=?`, roomID, member.Key()).Scan(&had)
		if err := insertMemberTx(ctx, tx, r, memberSpec{a: member, role: role, state: state}, by, s.nowMs()); err != nil {
			return err
		}
		if had == 0 {
			added = true
			_, err = tx.ExecContext(ctx, `UPDATE rooms SET version=version+1 WHERE id=?`, roomID)
		}
		return err
	})
	if err != nil || !added {
		return err
	}
	s.flush(ctx, []pendingEvent{memberAdded(events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}, roomID, memberSpec{a: member, role: role, state: state})})
	return nil
}

// RemoveMember removes a member; their room tool scope ends with the row (room-protocol s10).
func (s *Service) RemoveMember(ctx context.Context, roomID string, member Author, by Author) error {
	if by.Kind == AuthorAgent || by.Kind == AuthorTool {
		return fmt.Errorf("%w: only a human, a stored rule or the system removes members", ErrForbidden)
	}
	var r Room
	var removed bool
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = getRoom(ctx, tx, roomID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND member_id=?`, roomID, member.Key())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed = n > 0
		if removed {
			_, err = tx.ExecContext(ctx, `UPDATE rooms SET version=version+1 WHERE id=?`, roomID)
		}
		return err
	})
	if err != nil || !removed {
		return err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.member_removed", scope: events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}, data: map[string]any{"room": roomID, "member": member.Key()}}})
	return nil
}

// SetMemberState changes a member between active, observer and muted.
func (s *Service) SetMemberState(ctx context.Context, roomID string, member Author, state MemberState, by Author) error {
	if !validState(state) {
		return fmt.Errorf("%w: member state %q", ErrInvalid, state)
	}
	if by.Kind == AuthorAgent || by.Kind == AuthorTool {
		return fmt.Errorf("%w: only a human, a stored rule or the system changes member state", ErrForbidden)
	}
	var r Room
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = getRoom(ctx, tx, roomID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE room_members SET state=? WHERE room_id=? AND member_id=?`, string(state), roomID, member.Key())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: member %s", ErrNotFound, member.Key())
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.member_state_changed", scope: events.Event{ProjectID: r.ProjectID, TaskID: r.TaskID}, data: map[string]any{"room": roomID, "member": member.Key(), "state": state}}})
	return nil
}

const memberSelect = `SELECT m.room_id,m.member_id,m.kind,m.role,m.state,m.added_by,m.added_at,m.last_read_seq,m.last_mention_read_seq,COALESCE(a.name,'')
	FROM room_members m LEFT JOIN agents a ON m.kind='agent' AND a.id=m.member_id`

func scanMember(sc scanner) (Member, error) {
	var m Member
	var key, kind, state, display string
	var addedAt int64
	if err := sc.Scan(&m.RoomID, &key, &kind, &m.Role, &state, &m.AddedBy, &addedAt, &m.LastReadSeq, &m.LastMentionReadSeq, &display); err != nil {
		return Member{}, err
	}
	m.Author = ParseAuthor(key)
	m.Author.Display = display
	m.State = MemberState(state)
	m.AddedAt = fromMs(addedAt)
	return m, nil
}

func listMembers(ctx context.Context, q querier, roomID string) ([]Member, error) {
	rows, err := q.QueryContext(ctx, memberSelect+` WHERE m.room_id=? ORDER BY m.added_at, m.rowid`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Members lists a room's members in the order they joined.
func (s *Service) Members(ctx context.Context, roomID string) ([]Member, error) {
	return listMembers(ctx, s.db.Reader(), roomID)
}

// IsMember reports whether key is a member of the room (the check the Workspace room tool makes on every call).
func (s *Service) IsMember(ctx context.Context, roomID string, member Author) (bool, error) {
	var n int
	err := s.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id=? AND member_id=?`, roomID, member.Key()).Scan(&n)
	return n > 0, err
}

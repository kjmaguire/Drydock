package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
)

// How agents join rooms besides a person adding them and task creation (room-protocol s10 (c), decision P130). Agents never
// join on their own: a stored rule a person made adds an agent to the task rooms it matches, and an agent that wants to be in
// a channel asks, which is an Inbox item until a person lets it join or declines.
//
// The design text leaves choices open; each was read the safest way:
//   - a rule reaches task rooms only (a channel is joined by asking, a DM holds exactly its two members, a group its explicit
//     list), and only rooms created after the rule (never a room the person already set up);
//   - "touching auth/" is the files the task names (tasks.files): the rule never reads the worktree. It is matched when the
//     room is created and again when the task names its files later (the pipeline names them from the diff of a finished work
//     turn, ReapplyJoinRules); a rule acts once in a room, so an agent a person took out is not brought back;
//   - the agent joins as an observer (it reads the room and speaks only when mentioned) unless the person chose active, so a
//     rule spends no turns by itself;
//   - a request names a channel of the agent's own project it is not in; it is granted as an observer, and the agent's words
//     are data shown to the person (redacted, cut), never an instruction.

// JoinRule is a stored rule that adds an agent to the task rooms it matches.
type JoinRule struct {
	ID         string      `json:"id"`
	ProjectID  string      `json:"projectId"`
	AgentID    string      `json:"agentId"`
	PathPrefix string      `json:"pathPrefix,omitempty"` // "" for every task room
	State      MemberState `json:"state"`                // observer (default) or active
	CreatedBy  string      `json:"createdBy"`
	CreatedAt  int64       `json:"createdAt"`
}

// JoinRequest is an agent's request to join a channel.
type JoinRequest struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	RoomID      string `json:"roomId"`
	AgentID     string `json:"agentId"`
	Reason      string `json:"reason,omitempty"`
	Status      string `json:"status"` // requested, granted, declined
	RequestedAt int64  `json:"requestedAt"`
	DecidedAt   int64  `json:"decidedAt,omitempty"`
	DecidedBy   string `json:"decidedBy,omitempty"`
}

// Join request statuses.
const (
	JoinRequested = "requested"
	JoinGranted   = "granted"
	JoinDeclined  = "declined"
)

// MaxJoinReason is how much of an agent's reason a request keeps.
const MaxJoinReason = 300

// ErrJoinRequest is a request that cannot be made or answered (the room, the agent, a request already open or answered).
var ErrJoinRequest = errors.New("room: join request")

// normPrefix cleans a rule's path prefix: forward slashes, no leading "./" or "/", no "..". "" stays "" (every task room).
func normPrefix(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	if p == "" {
		return "", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: a path prefix stays inside the project", ErrInvalid)
		}
	}
	trail := strings.HasSuffix(p, "/")
	c := strings.TrimPrefix(path.Clean("/"+p), "/")
	if c == "" || c == "." {
		return "", nil
	}
	if trail {
		c += "/"
	}
	return c, nil
}

// touches reports whether one of files is under prefix: "auth/" matches auth/x.go, "auth" matches auth.go and auth/x.go.
func touches(files []string, prefix string) bool {
	if prefix == "" {
		return true
	}
	for _, f := range files {
		f = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(f, `\`, "/")), "/")
		if strings.HasPrefix(f, prefix) || strings.TrimSuffix(prefix, "/") == f {
			return true
		}
	}
	return false
}

// AddJoinRule stores a rule. Only a person makes one, for an agent of the project.
func (s *Service) AddJoinRule(ctx context.Context, r JoinRule, by Author) (JoinRule, error) {
	if by.Kind != AuthorHuman || by.Validate() != nil {
		return JoinRule{}, fmt.Errorf("%w: only a person makes a join rule", ErrForbidden)
	}
	if r.State == "" {
		r.State = StateObserver
	}
	if r.State != StateObserver && r.State != StateActive {
		return JoinRule{}, fmt.Errorf("%w: an agent joins as an observer or active, not %q", ErrInvalid, r.State)
	}
	p, err := normPrefix(r.PathPrefix)
	if err != nil {
		return JoinRule{}, err
	}
	r.PathPrefix = p
	var n int
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id=? AND project_id=? AND archived_at=0`, r.AgentID, r.ProjectID).Scan(&n); err != nil {
		return JoinRule{}, err
	}
	if n == 0 {
		return JoinRule{}, fmt.Errorf("%w: %s is not an agent of this project", ErrUnknownAgent, r.AgentID)
	}
	r.ID, r.CreatedBy, r.CreatedAt = ids.New("jrule"), by.Key(), s.nowMs()
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO room_join_rules(id,project_id,agent_id,path_prefix,state,created_by,created_at) VALUES(?,?,?,?,?,?,?)`,
			r.ID, r.ProjectID, r.AgentID, r.PathPrefix, string(r.State), r.CreatedBy, r.CreatedAt)
		return err
	}); err != nil {
		return JoinRule{}, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.join_rule_added", scope: events.Event{ProjectID: r.ProjectID, AgentID: r.AgentID}, data: map[string]any{"rule": r.ID, "pathPrefix": r.PathPrefix, "state": r.State}}})
	return r, nil
}

// RemoveJoinRule deletes a rule; agents it already added stay members (a person removes them from the room).
func (s *Service) RemoveJoinRule(ctx context.Context, id string, by Author) error {
	if by.Kind != AuthorHuman || by.Validate() != nil {
		return fmt.Errorf("%w: only a person removes a join rule", ErrForbidden)
	}
	var projectID string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM room_join_rules WHERE id=?`, id).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: join rule %s", ErrNotFound, id)
			}
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM room_join_rules WHERE id=?`, id)
		return err
	})
	if err != nil {
		return err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.join_rule_removed", scope: events.Event{ProjectID: projectID}, data: map[string]any{"rule": id}}})
	return nil
}

// JoinRules lists a project's rules, oldest first.
func (s *Service) JoinRules(ctx context.Context, projectID string) ([]JoinRule, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,project_id,agent_id,path_prefix,state,created_by,created_at FROM room_join_rules WHERE project_id=? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JoinRule{}
	for rows.Next() {
		var r JoinRule
		var st string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.AgentID, &r.PathPrefix, &st, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.State = MemberState(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReapplyJoinRules matches a task's room against the project's rules again, for a task that named its files after the room was
// made. Rules that already acted in the room are left alone, so calling it after every change of the files is safe. A task with
// no room, or a room that is archived, is not an error and changes nothing.
func (s *Service) ReapplyJoinRules(ctx context.Context, taskID string) error {
	rm, err := s.RoomByTask(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || rm.Archived {
		return err
	}
	return s.applyJoinRules(ctx, rm)
}

// applyJoinRules adds the agents of the project's rules to a task room, for the rules made before the room whose prefix a file
// of the task falls under. An agent the room already has keeps its place; a rule's agent that left the project is skipped; a
// rule that already added its agent here (its SYSTEM line is in the room) does not act again, whether or not the agent is still
// a member. Each agent added gets one SYSTEM line saying which rule added it.
func (s *Service) applyJoinRules(ctx context.Context, rm Room) error {
	if rm.Kind != KindTask || rm.TaskID == "" {
		return nil
	}
	var filesJSON string
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT files FROM tasks WHERE id=?`, rm.TaskID).Scan(&filesJSON); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var files []string
	_ = json.Unmarshal([]byte(filesJSON), &files)
	rules, err := s.JoinRules(ctx, rm.ProjectID)
	if err != nil {
		return err
	}
	created := rm.CreatedAt.UnixMilli()
	for _, r := range rules {
		if r.CreatedAt > created || !touches(files, r.PathPrefix) {
			continue
		}
		var acted int
		if err := s.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE room_id=? AND json_extract(payload,'$.event')='join_rule' AND json_extract(payload,'$.params.rule')=?`, rm.ID, r.ID).Scan(&acted); err != nil {
			return err
		}
		if acted > 0 {
			continue
		}
		var live int
		if err := s.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id=? AND project_id=? AND archived_at=0`, r.AgentID, rm.ProjectID).Scan(&live); err != nil {
			return err
		}
		if member, err := s.IsMember(ctx, rm.ID, Agent(r.AgentID)); err != nil || member || live == 0 {
			continue
		}
		if err := s.AddMember(ctx, rm.ID, Agent(r.AgentID), "", r.State, Author{Kind: AuthorSystem, ID: "system"}); err != nil {
			return err
		}
		what := "every task room"
		if r.PathPrefix != "" {
			what = "every task room whose task names a file under " + r.PathPrefix
		}
		if _, err := s.Post(ctx, PostParams{RoomID: rm.ID, Type: TypeSystem, Author: System, TaskID: rm.TaskID,
			Body:    fmt.Sprintf("%s joined as %s by a join rule: %s.", s.agentName(ctx, r.AgentID), stateWords(r.State), what),
			Payload: map[string]any{"event": "join_rule", "params": map[string]any{"rule": r.ID, "agent": r.AgentID, "state": r.State}}}); err != nil {
			return err
		}
	}
	return nil
}

// agentName is an agent's name for a line the system writes (its id when it has none): a name, not an @mention, so the line
// wakes and notifies nobody.
func (s *Service) agentName(ctx context.Context, id string) string {
	var name string
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT name FROM agents WHERE id=?`, id).Scan(&name); err != nil || name == "" {
		return id
	}
	return name
}

func stateWords(st MemberState) string {
	if st == StateActive {
		return "an active member"
	}
	return "an observer"
}

// RequestJoin records an agent's request to join a channel of its project it is not in (the Workspace room tool's
// request_join). The request is an Inbox item until a person answers it; the agent's reason is redacted by the room's secret
// scan and cut to MaxJoinReason. One open request per agent and channel: asking again returns the open one.
func (s *Service) RequestJoin(ctx context.Context, roomID, agentID, reason string) (JoinRequest, error) {
	rm, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return JoinRequest{}, err
	}
	if rm.Kind != KindChannel || rm.Archived {
		return JoinRequest{}, fmt.Errorf("%w: an agent asks to join a channel (task rooms, direct messages and groups are joined otherwise)", ErrJoinRequest)
	}
	var n int
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id=? AND project_id=? AND archived_at=0`, agentID, rm.ProjectID).Scan(&n); err != nil {
		return JoinRequest{}, err
	}
	if n == 0 {
		return JoinRequest{}, fmt.Errorf("%w: %s is not an agent of this channel's project", ErrUnknownAgent, agentID)
	}
	if ok, err := s.IsMember(ctx, roomID, Agent(agentID)); err != nil || ok {
		if err == nil {
			err = fmt.Errorf("%w: %s is already in #%s", ErrJoinRequest, agentID, rm.Name)
		}
		return JoinRequest{}, err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if s.opt.Redact != nil {
		if reason, err = s.opt.Redact(reason); err != nil {
			return JoinRequest{}, fmt.Errorf("%w: %v", ErrBlocked, err)
		}
	}
	if len(reason) > MaxJoinReason {
		reason = clip(reason, MaxJoinReason)
	}
	jr := JoinRequest{ID: ids.New("jreq"), ProjectID: rm.ProjectID, RoomID: roomID, AgentID: agentID, Reason: reason, Status: JoinRequested, RequestedAt: s.nowMs()}
	created := false
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO room_join_requests(id,project_id,room_id,agent_id,reason,status,requested_at) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(room_id, agent_id) WHERE status='requested' DO NOTHING`, jr.ID, jr.ProjectID, jr.RoomID, jr.AgentID, jr.Reason, jr.Status, jr.RequestedAt)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 1 {
			created = true
			return nil
		}
		return tx.QueryRowContext(ctx, `SELECT id,reason,requested_at FROM room_join_requests WHERE room_id=? AND agent_id=? AND status='requested'`, roomID, agentID).
			Scan(&jr.ID, &jr.Reason, &jr.RequestedAt)
	})
	if err != nil || !created {
		return jr, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.join_requested", scope: events.Event{ProjectID: rm.ProjectID, AgentID: agentID}, data: map[string]any{"room": roomID, "request": jr.ID}}})
	return jr, nil
}

// GetJoinRequest returns one request.
func (s *Service) GetJoinRequest(ctx context.Context, id string) (JoinRequest, error) {
	var jr JoinRequest
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id,project_id,room_id,agent_id,reason,status,requested_at,decided_at,decided_by FROM room_join_requests WHERE id=?`, id).
		Scan(&jr.ID, &jr.ProjectID, &jr.RoomID, &jr.AgentID, &jr.Reason, &jr.Status, &jr.RequestedAt, &jr.DecidedAt, &jr.DecidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return jr, fmt.Errorf("%w: join request %s", ErrNotFound, id)
	}
	return jr, err
}

// AnswerJoin is a person's answer to a request: allow adds the agent to the channel as an observer, otherwise the request is
// declined. Either way the request leaves the Inbox, and one SYSTEM line in the channel says what was decided.
func (s *Service) AnswerJoin(ctx context.Context, id string, allow bool, by Author) (JoinRequest, error) {
	if by.Kind != AuthorHuman || by.Validate() != nil {
		return JoinRequest{}, fmt.Errorf("%w: only a person answers a join request", ErrForbidden)
	}
	jr, err := s.GetJoinRequest(ctx, id)
	if err != nil {
		return jr, err
	}
	if jr.Status != JoinRequested {
		return jr, fmt.Errorf("%w: the request was already %s", ErrJoinRequest, jr.Status)
	}
	status := JoinDeclined
	if allow {
		status = JoinGranted
		if err := s.AddMember(ctx, jr.RoomID, Agent(jr.AgentID), "", StateObserver, by); err != nil {
			return jr, err
		}
	}
	now := s.nowMs()
	if err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE room_join_requests SET status=?, decided_at=?, decided_by=? WHERE id=? AND status='requested'`, status, now, by.Key(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: the request was already answered", ErrJoinRequest)
		}
		return nil
	}); err != nil {
		return jr, err
	}
	jr.Status, jr.DecidedAt, jr.DecidedBy = status, now, by.Key()
	who := s.agentName(ctx, jr.AgentID)
	body := fmt.Sprintf("%s asked to join and was let in as an observer.", who)
	if !allow {
		body = fmt.Sprintf("%s asked to join; the request was declined.", who)
	}
	if _, err := s.Post(ctx, PostParams{RoomID: jr.RoomID, Type: TypeSystem, Author: System, Body: body,
		Payload: map[string]any{"event": "join_request", "params": map[string]any{"request": jr.ID, "agent": jr.AgentID, "status": status}}}); err != nil {
		s.report(err)
	}
	s.flush(ctx, []pendingEvent{{typ: "room.join_answered", scope: events.Event{ProjectID: jr.ProjectID, AgentID: jr.AgentID}, data: map[string]any{"room": jr.RoomID, "request": jr.ID, "status": status}}})
	return jr, nil
}

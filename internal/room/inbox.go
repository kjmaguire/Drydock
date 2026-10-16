package room

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// Unread is one member's unread state in one room (room-protocol s9).
type Unread struct {
	RoomID   string `json:"roomId"`
	RoomName string `json:"roomName"`
	Kind     Kind   `json:"kind"`
	Unread   int    `json:"unread"`   // messages from others with seq > last_read_seq
	Mentions int    `json:"mentions"` // messages that mention the member with seq > last_mention_read_seq
	Muted    bool   `json:"muted"`    // muted rooms count unread but do not notify
	LastRead int64  `json:"lastReadSeq"`
}

// UnreadCap is the most unread messages (or mentions) counted for one room: a room nobody has opened for a long time would
// otherwise be counted message by message at every refresh of the sidebar. A count at the cap means "this many or more"
// (audit 2026-10-16, L18).
const UnreadCap = 1000

// UnreadCounts returns the unread and mention counts per room for a member, archived rooms excluded. projectID may be "". A
// count stops at UnreadCap. Messages without mentions are skipped before their mention list is read.
func (s *Service) UnreadCounts(ctx context.Context, member Author, projectID string) ([]Unread, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
SELECT r.id, r.name, r.kind, m.state, m.last_read_seq,
  (SELECT COUNT(*) FROM (SELECT 1 FROM messages x WHERE x.room_id = r.id AND x.seq > m.last_read_seq AND x.author <> m.member_id LIMIT `+fmt.Sprint(UnreadCap)+`)),
  (SELECT COUNT(*) FROM (SELECT 1 FROM messages x WHERE x.room_id = r.id AND x.seq > m.last_mention_read_seq AND x.author <> m.member_id
     AND x.mentions <> '[]'
     AND EXISTS (SELECT 1 FROM json_each(x.mentions) j
                 WHERE json_extract(j.value, '$.id') = m.member_id
                    OR EXISTS (SELECT 1 FROM json_each(j.value, '$.members') k WHERE k.value = m.member_id)) LIMIT `+fmt.Sprint(UnreadCap)+`))
FROM room_members m JOIN rooms r ON r.id = m.room_id
WHERE m.member_id = ? AND r.archived = 0 AND (? = '' OR r.project_id = ?)
ORDER BY r.kind, r.name`, member.Key(), projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unread
	for rows.Next() {
		var u Unread
		var kind, state string
		if err := rows.Scan(&u.RoomID, &u.RoomName, &kind, &state, &u.LastRead, &u.Unread, &u.Mentions); err != nil {
			return nil, err
		}
		u.Kind, u.Muted = Kind(kind), state == string(StateMuted)
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkRead moves a member's read cursors up to seq (clamped to the room's last seq). It never moves them back, so
// repeated or reordered client reports are harmless. It reports whether the cursor advanced.
func (s *Service) MarkRead(ctx context.Context, roomID string, member Author, seq int64) (bool, error) {
	var advanced bool
	var projectID string
	var at int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		advanced = false
		var last int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE room_id=?`, roomID).Scan(&last); err != nil {
			return err
		}
		if seq > last {
			seq = last
		}
		var cur, curMention int64
		err := tx.QueryRowContext(ctx, `SELECT m.last_read_seq, m.last_mention_read_seq, r.project_id FROM room_members m JOIN rooms r ON r.id=m.room_id WHERE m.room_id=? AND m.member_id=?`,
			roomID, member.Key()).Scan(&cur, &curMention, &projectID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: %s", ErrNotMember, member.Key())
		}
		if err != nil {
			return err
		}
		if seq <= cur && seq <= curMention {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE room_members SET last_read_seq=MAX(last_read_seq,?), last_mention_read_seq=MAX(last_mention_read_seq,?) WHERE room_id=? AND member_id=?`,
			seq, seq, roomID, member.Key()); err != nil {
			return err
		}
		advanced, at = true, seq
		return nil
	})
	if err != nil || !advanced {
		return false, err
	}
	s.flush(ctx, []pendingEvent{{typ: "room.read_cursor_updated", scope: events.Event{ProjectID: projectID}, data: map[string]any{"user": member.Key(), "room": roomID, "seq": at}}})
	return true, nil
}

// NeedsKind classifies a "Needs you" item.
type NeedsKind string

const (
	NeedsApproval      NeedsKind = "approval"
	NeedsReview        NeedsKind = "review"
	NeedsQuestion      NeedsKind = "question"
	NeedsFailedCheck   NeedsKind = "failed_check"
	NeedsWatchdogPause NeedsKind = "watchdog_pause"
)

// NeedsYouItem is one row of the inbox. Rows are derived from source tables on every query, so they vanish when the
// underlying state changes (room-protocol s9).
type NeedsYouItem struct {
	Key        string    `json:"key"` // stable identity, also the argument to AckNeedsYou
	Kind       NeedsKind `json:"kind"`
	Severity   int       `json:"severity"` // 0 first
	Title      string    `json:"title"`
	Detail     string    `json:"detail,omitempty"`
	ProjectID  string    `json:"projectId,omitempty"`
	RoomID     string    `json:"roomId,omitempty"`
	MessageID  string    `json:"messageId,omitempty"`
	ApprovalID string    `json:"approvalId,omitempty"`
	TaskID     string    `json:"taskId,omitempty"`
	AgentID    string    `json:"agentId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// NeedsYou returns what a human must act on, approvals and failures first, then by age (oldest first). projectID may
// be "". Sources: open approvals-table rows (approval, review, question), blocking questions in messages, the latest
// failed check per room and check kind, and stage-2 watchdog pauses. Agent-state, budget and Connection items need
// tables that do not exist yet and are not included.
func (s *Service) NeedsYou(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	var out []NeedsYouItem
	q := s.db.Reader()

	rows, err := q.QueryContext(ctx, `
SELECT a.id, a.kind, a.title, a.task_id, a.project_id, a.created_at,
  COALESCE((SELECT r.id FROM rooms r WHERE a.task_id <> '' AND r.kind = 'task' AND r.task_id = a.task_id), '')
FROM approvals a WHERE a.status = 'REQUESTED' AND (? = '' OR a.project_id = ?)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it NeedsYouItem
		var kind string
		var at int64
		if err := rows.Scan(&it.ApprovalID, &kind, &it.Title, &it.TaskID, &it.ProjectID, &at, &it.RoomID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Key, it.CreatedAt = "approval:"+it.ApprovalID, fromMs(at)
		switch kind {
		case "review":
			it.Kind, it.Severity = NeedsReview, 2
		case "question":
			it.Kind, it.Severity = NeedsQuestion, 3
		default:
			it.Kind, it.Severity = NeedsApproval, 0
		}
		out = append(out, it)
	}
	rows.Close()

	// A blocking QUESTION to a human, until a human replies, it is superseded, acked, or its request row exists (the
	// approvals-table row above is then the item, or it is resolved). A question that is itself a thread reply (the debate
	// limit asks under the message that started the round) cannot be replied to directly, threads being one level deep: a
	// human's later message in the same thread answers it.
	rows, err = q.QueryContext(ctx, `
SELECT m.id, m.room_id, m.body, m.task_id, m.author, m.created_at, r.project_id
FROM messages m JOIN rooms r ON r.id = m.room_id
WHERE m.type = 'QUESTION' AND r.archived = 0 AND (? = '' OR r.project_id = ?)
  AND COALESCE(json_extract(m.payload, '$.blocking'), 0) = 1
  AND m.author NOT LIKE 'human:%'
  AND (COALESCE(json_extract(m.payload, '$.to'), '') = '' OR json_extract(m.payload, '$.to') LIKE 'human:%')
  AND NOT EXISTS (SELECT 1 FROM messages a WHERE a.room_id = m.room_id AND a.seq > m.seq
                  AND (a.supersedes_id = m.id OR ((a.reply_to_id = m.id OR a.thread_id = m.id
                       OR (COALESCE(m.thread_id, '') <> '' AND a.thread_id = m.thread_id)) AND a.author LIKE 'human:%')))
  AND NOT EXISTS (SELECT 1 FROM room_inbox_acks k WHERE k.item_key = 'question:' || m.id)
  AND NOT EXISTS (SELECT 1 FROM approvals p WHERE p.id = COALESCE(json_extract(m.payload, '$.request_id'), ''))`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it NeedsYouItem
		var body, author string
		var at int64
		if err := rows.Scan(&it.MessageID, &it.RoomID, &body, &it.TaskID, &author, &at, &it.ProjectID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.Title, it.CreatedAt = "question:"+it.MessageID, NeedsQuestion, 3, firstLine(body, 160), fromMs(at)
		if a := ParseAuthor(author); a.Kind == AuthorAgent {
			it.AgentID = a.ID
		}
		out = append(out, it)
	}
	rows.Close()

	// The latest failing result per (room, check kind): a later pass, fail or error from the tool replaces or clears it.
	rows, err = q.QueryContext(ctx, `
SELECT m.id, m.room_id, m.body, m.task_id, m.created_at, r.project_id
FROM messages m JOIN rooms r ON r.id = m.room_id
WHERE m.author LIKE 'tool:%' AND m.type IN ('ERROR','RESULT') AND r.archived = 0 AND (? = '' OR r.project_id = ?)
  AND json_extract(m.payload, '$.check_status') IN ('fail','error')
  AND NOT EXISTS (SELECT 1 FROM messages a WHERE a.room_id = m.room_id AND a.seq > m.seq AND a.author LIKE 'tool:%'
                  AND json_extract(a.payload, '$.check_kind') = json_extract(m.payload, '$.check_kind')
                  AND json_extract(a.payload, '$.check_status') IN ('pass','fail','error'))
  AND NOT EXISTS (SELECT 1 FROM room_inbox_acks k WHERE k.item_key = 'check:' || m.id)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it NeedsYouItem
		var body string
		var at int64
		if err := rows.Scan(&it.MessageID, &it.RoomID, &body, &it.TaskID, &at, &it.ProjectID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.Title, it.CreatedAt = "check:"+it.MessageID, NeedsFailedCheck, 1, firstLine(body, 160), fromMs(at)
		out = append(out, it)
	}
	rows.Close()

	// Stage-2 watchdog trips with no later reset for the task (the watchdog writes a 'reset' row when a human resumes).
	rows, err = q.QueryContext(ctx, `
SELECT t.id, t.task_id, t.agent_id, t.signal, t.created_at, COALESCE(ro.project_id, ag.project_id, ''), COALESCE(ro.id, '')
FROM watchdog_trips t
LEFT JOIN rooms ro ON t.task_id <> '' AND ro.kind = 'task' AND ro.task_id = t.task_id
LEFT JOIN agents ag ON ag.id = t.agent_id
WHERE t.stage = 2
  AND NOT EXISTS (SELECT 1 FROM watchdog_trips r WHERE r.task_id = t.task_id AND r.signal = 'reset' AND r.id > t.id)
  AND NOT EXISTS (SELECT 1 FROM room_inbox_acks k WHERE k.item_key = 'watchdog:' || t.id)
  AND (? = '' OR COALESCE(ro.project_id, ag.project_id, '') = ?)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it NeedsYouItem
		var id, at int64
		var signal string
		if err := rows.Scan(&id, &it.TaskID, &it.AgentID, &signal, &at, &it.ProjectID, &it.RoomID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.CreatedAt = fmt.Sprintf("watchdog:%d", id), NeedsWatchdogPause, 1, fromMs(at)
		it.Title = "Watchdog paused the task (" + signal + ")"
		out = append(out, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity < out[j].Severity
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// AckNeedsYou dismisses an item that has no state to flip (a failed check, a watchdog pause, a question). Only a human can.
func (s *Service) AckNeedsYou(ctx context.Context, key string, by Author) error {
	if by.Kind != AuthorHuman || by.Validate() != nil {
		return fmt.Errorf("%w: only a human dismisses an inbox item", ErrForbidden)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO room_inbox_acks(item_key,acked_by,acked_at) VALUES(?,?,?) ON CONFLICT(item_key) DO NOTHING`, key, by.Key(), s.nowMs())
		return err
	})
}

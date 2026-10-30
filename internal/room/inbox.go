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
	// NeedsChatPermission is a chat's CLI waiting for the person to allow a tool call (chats.PendingAsk). The core adds
	// these; they live only while the request waits.
	NeedsChatPermission NeedsKind = "chat_permission"
	// NeedsBudget is a budget that passed its warning point or reached its limit (decision P118): its own item, saying what to
	// do, whether or not a run is waiting on a budget pause (the pause's approval row becomes this item rather than a plain
	// approval).
	NeedsBudget NeedsKind = "budget"
)

// Budget states of a NeedsBudget item.
const (
	BudgetWarning  = "warning"  // past the warning point (80% by default); agents keep working
	BudgetExceeded = "exceeded" // at the limit; the turns it counts toward do not start
	BudgetPaused   = "paused"   // a run waits on the budget pause for its next turn
)

// What a person can do about each budget state, in the item's detail.
const (
	budgetWarningDo  = "Agents keep working. At the limit, the next agent turn waits for you: raise the limit on the Budgets page if this work should go on, or dismiss this."
	budgetExceededDo = "Agent turns this budget counts toward do not start: a workflow turn waits and a room reply is refused. Raise the limit on the Budgets page."
	budgetPausedDo   = "A budget stopped the next agent turn of a run. Open the task to raise the limit or approve one more turn; rejecting stops the run."
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
	ChatID     string    `json:"chatId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	// TaskSeq is the task's number in its project (with the project, the task's key); 0 for an item of no task.
	TaskSeq int `json:"taskSeq,omitempty"`
	// Phase says which gate a review item is, from the request that made it (workflow.RequestSpec.Phase): "spec" (accept the
	// spec), "merge" (review and merge) or "plan" (review a plan); empty for every other item.
	Phase string `json:"phase,omitempty"`
	// AgentName is the name of the agent the item is about (AgentID): the one asking, or the task's assignee for a review.
	AgentName string `json:"agentName,omitempty"`
	// ExpiresAt is when an approval lapses unanswered (a tool permission request); nil when it does not.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// Context is what a task's item is about, so the person can see what they are deciding before they open it (UI review
	// P1-1). The core adds it to items of a task; nil for the rest.
	Context *NeedsContext `json:"context,omitempty"`
	// BudgetID and BudgetState are a budget item's budget and where it stands (BudgetWarning, BudgetExceeded, BudgetPaused).
	BudgetID    string `json:"budgetId,omitempty"`
	BudgetState string `json:"budgetState,omitempty"`
}

// NeedsContext is a task's state in one line: who did the work, how big the change is, how its checks went and what its
// reviewer said, as recorded in the task's evidence and reviews.
type NeedsContext struct {
	Agent        string `json:"agent,omitempty"` // the task's assignee
	HasDiff      bool   `json:"hasDiff"`         // a diff was recorded; the counts below are of the newest one
	Files        int    `json:"files"`
	Insertions   int    `json:"insertions"`
	Deletions    int    `json:"deletions"`
	ChecksPassed int    `json:"checksPassed"` // of the newest attempt's checks, the latest result of each kind
	ChecksTotal  int    `json:"checksTotal"`
	Verdict      string `json:"verdict,omitempty"` // the newest reviewer verdict: SHIP or REVISE
}

// NeedsYou returns what a human must act on, approvals and failures first, then by age (oldest first). projectID may
// be "". Sources: open approvals-table rows (approval, review, question), blocking questions in messages, the latest
// failed check per room and check kind, stage-2 watchdog pauses, and budgets past their warning point or at their limit
// (budget_alerts; a budget pause's approval row is the budget item of its run). Agent-state and Connection items need tables
// that do not exist yet and are not included.
func (s *Service) NeedsYou(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	var out []NeedsYouItem
	q := s.db.Reader()

	rows, err := q.QueryContext(ctx, `
SELECT a.id, a.kind, a.title, a.task_id, a.project_id, a.created_at, a.phase, a.expires_at,
  COALESCE((SELECT r.id FROM rooms r WHERE a.task_id <> '' AND r.kind = 'task' AND r.task_id = a.task_id), ''),
  COALESCE(NULLIF(CASE WHEN a.run_id = '' AND json_valid(CAST(a.payload AS TEXT)) THEN json_extract(CAST(a.payload AS TEXT), '$.action.agentId') END, ''),
           (SELECT t.assignee FROM tasks t WHERE a.task_id <> '' AND t.id = a.task_id), ''),
  COALESCE(CASE WHEN json_valid(CAST(a.payload AS TEXT)) THEN json_extract(CAST(a.payload AS TEXT), '$.gate') END, ''),
  COALESCE(CASE WHEN json_valid(CAST(a.payload AS TEXT)) THEN json_extract(CAST(a.payload AS TEXT), '$.exceeded[0].budget.id') END, '')
FROM approvals a WHERE a.status = 'REQUESTED' AND (? = '' OR a.project_id = ?)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it NeedsYouItem
		var kind, gate, budgetID string
		var at, expires int64
		if err := rows.Scan(&it.ApprovalID, &kind, &it.Title, &it.TaskID, &it.ProjectID, &at, &it.Phase, &expires, &it.RoomID, &it.AgentID, &gate, &budgetID); err != nil {
			rows.Close()
			return nil, err
		}
		it.Key, it.CreatedAt = "approval:"+it.ApprovalID, fromMs(at)
		if expires > 0 {
			e := fromMs(expires)
			it.ExpiresAt = &e
		}
		switch {
		case kind == "approval" && gate == "budget":
			// the budget gate's pause (budget.PauseGate): a budget item that is answered like an approval
			it.Kind, it.Severity, it.BudgetID, it.BudgetState, it.Detail = NeedsBudget, 0, budgetID, BudgetPaused, budgetPausedDo
		case kind == "review":
			it.Kind, it.Severity = NeedsReview, 2
		case kind == "question":
			it.Kind, it.Severity = NeedsQuestion, 3
		default:
			it.Kind, it.Severity = NeedsApproval, 0
		}
		out = append(out, it)
	}
	rows.Close()

	budgets, err := s.budgetItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out = append(out, budgets...)

	// A blocking QUESTION to a human, until a human replies, it is superseded, acked, or its request row exists (the
	// approvals-table row above is then the item, or it is resolved), or a workflow's question request carries it (the
	// question a design discussion ended on, which the Design Review template puts to the person, P124). A question that
	// is itself a thread reply (the debate
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
  AND NOT EXISTS (SELECT 1 FROM approvals p WHERE p.id = COALESCE(json_extract(m.payload, '$.request_id'), ''))
  AND NOT EXISTS (SELECT 1 FROM approvals p WHERE p.kind = 'question' AND p.project_id = r.project_id
                  AND json_valid(CAST(p.payload AS TEXT)) AND json_extract(CAST(p.payload AS TEXT), '$.questionId') = m.id)`, projectID, projectID)
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

	if err := s.addNames(ctx, out); err != nil {
		return nil, err
	}
	SortNeedsYou(out)
	return out, nil
}

// budgetItems are the budgets that passed their warning point or reached their limit (the alerts the budget gate fired once
// each, internal/budget), until the limit is changed (which clears the alerts), the budget is switched off, or a person
// dismisses the item. The item's key carries the alert's time, so a dismissal does not hide the same alert fired again
// after a limit change. An exceeded budget a run is waiting on is left to the pause's own item (the approval row); a warning
// gives way to the same budget's exceeded item.
func (s *Service) budgetItems(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
SELECT al.budget_id, al.instance_id, al.level, al.ts, b.scope, b.scope_id, b.warn_pct,
  CASE b.scope WHEN 'project' THEN b.scope_id
    WHEN 'task' THEN COALESCE((SELECT t.project_id FROM tasks t WHERE t.id = al.instance_id), '')
    WHEN 'agent' THEN COALESCE((SELECT g.project_id FROM agents g WHERE g.id = al.instance_id), '')
    WHEN 'run' THEN COALESCE((SELECT r.project_id FROM workflow_runs r WHERE r.id = al.instance_id), '')
    ELSE b.project_id END
FROM budget_alerts al JOIN budgets b ON b.id = al.budget_id
WHERE b.enabled = 1
  AND NOT (al.level = 'warning' AND EXISTS (SELECT 1 FROM budget_alerts x WHERE x.budget_id = al.budget_id AND x.instance_id = al.instance_id AND x.level = 'exceeded'))
  AND NOT EXISTS (SELECT 1 FROM room_inbox_acks k WHERE k.item_key = 'budget:' || al.budget_id || ':' || al.instance_id || ':' || al.level || ':' || al.ts)
  AND NOT (al.level = 'exceeded' AND EXISTS (SELECT 1 FROM approvals p WHERE p.status = 'REQUESTED' AND p.kind = 'approval'
           AND CAST(p.payload AS TEXT) LIKE '%"gate":"budget"%' AND json_valid(CAST(p.payload AS TEXT))
           AND EXISTS (SELECT 1 FROM json_each(CAST(p.payload AS TEXT), '$.exceeded') e WHERE json_extract(e.value, '$.budget.id') = al.budget_id)))
ORDER BY al.ts, al.budget_id, al.instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeedsYouItem
	for rows.Next() {
		var it NeedsYouItem
		var instance, level, scope, scopeID, project string
		var at int64
		var warn float64
		if err := rows.Scan(&it.BudgetID, &instance, &level, &at, &scope, &scopeID, &warn, &project); err != nil {
			return nil, err
		}
		if projectID != "" && project != projectID {
			continue
		}
		what := scope + " budget"
		switch scope {
		case "workflow":
			what = "budget of the workflow " + scopeID
		case "task":
			it.TaskID = instance
		case "agent":
			it.AgentID = instance
		}
		it.Key = fmt.Sprintf("budget:%s:%s:%s:%d", it.BudgetID, instance, level, at)
		it.Kind, it.ProjectID, it.CreatedAt, it.BudgetState = NeedsBudget, project, fromMs(at), level
		if level == BudgetExceeded {
			it.Severity, it.Title, it.Detail = 0, "Budget reached: the "+what+" is at its limit", budgetExceededDo
		} else {
			it.Severity, it.Title, it.Detail = 2, fmt.Sprintf("Budget warning: the %s passed %.0f%%", what, warn*100), budgetWarningDo
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// a task's budget item opens in the task's room
	for i := range out {
		if out[i].TaskID != "" {
			_ = s.db.Reader().QueryRowContext(ctx, `SELECT id FROM rooms WHERE kind='task' AND task_id=?`, out[i].TaskID).Scan(&out[i].RoomID)
		}
	}
	return out, nil
}

// addNames gives each item its task's number and its agent's name, one read of each task and agent named.
func (s *Service) addNames(ctx context.Context, out []NeedsYouItem) error {
	seqs, names := map[string]int{}, map[string]string{}
	for _, it := range out {
		if it.TaskID != "" {
			seqs[it.TaskID] = 0
		}
		if it.AgentID != "" {
			names[it.AgentID] = ""
		}
	}
	q := s.db.Reader()
	for id := range seqs {
		var seq int
		if err := q.QueryRowContext(ctx, `SELECT seq FROM tasks WHERE id=?`, id).Scan(&seq); err != nil && err != sql.ErrNoRows {
			return err
		}
		seqs[id] = seq
	}
	for id := range names {
		var name string
		if err := q.QueryRowContext(ctx, `SELECT name FROM agents WHERE id=?`, id).Scan(&name); err != nil && err != sql.ErrNoRows {
			return err
		}
		names[id] = name
	}
	for i := range out {
		out[i].TaskSeq, out[i].AgentName = seqs[out[i].TaskID], names[out[i].AgentID]
	}
	return nil
}

// SortNeedsYou puts items in the inbox's order: by severity, then oldest first.
func SortNeedsYou(out []NeedsYouItem) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity < out[j].Severity
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Key < out[j].Key
	})
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

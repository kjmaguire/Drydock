package room

import (
	"context"
	"fmt"
	"strings"
)

// Inbox items of decision P130 that the room derives from its own tables: a task waiting for a person, an unread mention of a
// person, an agent's request to join a channel. Each resolves when its cause does (the task moves, the mention is read, the
// request is answered), like every item here.
//
// The rule against duplicates: one item per cause, and the item that says most about it wins. A task's waiting item is left out
// when the inbox already holds an item of that task (its approval, review, question, budget pause, watchdog pause or failed
// check: that item is what the task waits on, and where the person answers); a mention is left out when its message is already
// an item (a blocking question); in a room the person muted, only a mention by name makes an item, never one through a role.
// The core applies the same rule to what it adds: an agent's item is left out when a watchdog pause put the agent there (the
// watchdog item is that cause), and one Connection item stands for every agent that uses the CLI.

// MentionItemCap is the most mention items listed: the oldest unread ones first, the rest stay counted in the sidebar.
const MentionItemCap = 50

// moreItems adds the P130 items to out, after the rule against duplicates.
func (s *Service) moreItems(ctx context.Context, projectID string, out []NeedsYouItem) ([]NeedsYouItem, error) {
	tasks, msgs := map[string]bool{}, map[string]bool{}
	for _, it := range out {
		if it.TaskID != "" {
			tasks[it.TaskID] = true
		}
		if it.MessageID != "" {
			msgs[it.MessageID] = true
		}
	}
	waiting, err := s.taskWaitingItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, it := range waiting {
		if !tasks[it.TaskID] {
			out = append(out, it)
		}
	}
	mentions, err := s.mentionItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, it := range mentions {
		if !msgs[it.MessageID] {
			out = append(out, it)
		}
	}
	joins, err := s.joinItems(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return append(out, joins...), nil
}

// taskWaitingItems are the tasks in WAITING_HUMAN: the task waits for a person and nothing else in the inbox may say why.
func (s *Service) taskWaitingItems(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
SELECT t.id, t.project_id, t.title, t.blocked_reason, t.updated_at, t.assignee,
  COALESCE((SELECT r.id FROM rooms r WHERE r.kind = 'task' AND r.task_id = t.id), '')
FROM tasks t WHERE t.status = 'WAITING_HUMAN' AND (? = '' OR t.project_id = ?)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeedsYouItem
	for rows.Next() {
		var it NeedsYouItem
		var title, reason string
		var at int64
		if err := rows.Scan(&it.TaskID, &it.ProjectID, &title, &reason, &at, &it.AgentID, &it.RoomID); err != nil {
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.CreatedAt = "task:"+it.TaskID, NeedsTaskWaiting, 2, fromMs(at)
		it.Title = firstLine("Waits for you: "+title, 160)
		it.Detail = "Open the task to see what it needs and continue it, or stop it."
		if r := strings.TrimSpace(reason); r != "" {
			it.Detail = firstLine(r, 160)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// mentionItems are the messages that mention a person member of a room (by name, or through a role the person holds, except
// in a room the person muted) after the person's mention cursor: the oldest MentionItemCap of them.
func (s *Service) mentionItems(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
SELECT x.id, x.room_id, x.body, x.task_id, x.author, x.created_at, r.project_id, r.kind, r.name
FROM room_members m JOIN rooms r ON r.id = m.room_id JOIN messages x ON x.room_id = m.room_id
WHERE m.kind = 'human' AND r.archived = 0 AND (? = '' OR r.project_id = ?)
  AND x.seq > m.last_mention_read_seq AND x.author <> m.member_id AND x.mentions <> '[]'
  AND EXISTS (SELECT 1 FROM json_each(x.mentions) j
              WHERE json_extract(j.value, '$.id') = m.member_id
                 OR (m.state <> 'muted' AND EXISTS (SELECT 1 FROM json_each(j.value, '$.members') k WHERE k.value = m.member_id)))
  AND NOT EXISTS (SELECT 1 FROM room_inbox_acks k WHERE k.item_key = 'mention:' || x.id)
ORDER BY x.created_at, x.seq LIMIT `+fmt.Sprint(MentionItemCap), projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeedsYouItem
	for rows.Next() {
		var it NeedsYouItem
		var body, author, kind, name string
		var at int64
		if err := rows.Scan(&it.MessageID, &it.RoomID, &body, &it.TaskID, &author, &at, &it.ProjectID, &kind, &name); err != nil {
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.CreatedAt = "mention:"+it.MessageID, NeedsMention, 3, fromMs(at)
		it.Title = firstLine(body, 160)
		where := "#" + name
		if Kind(kind) == KindDM {
			where = "your direct message"
		}
		it.Detail = "Mentioned you in " + where + "."
		if a := ParseAuthor(author); a.Kind == AuthorAgent {
			it.AgentID = a.ID
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// joinItems are the open requests of agents to join a channel, while the channel is open and the agent is not in it.
func (s *Service) joinItems(ctx context.Context, projectID string) ([]NeedsYouItem, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `
SELECT q.id, q.project_id, q.room_id, q.agent_id, q.reason, q.requested_at, r.name
FROM room_join_requests q JOIN rooms r ON r.id = q.room_id
WHERE q.status = 'requested' AND r.archived = 0 AND (? = '' OR q.project_id = ?)
  AND NOT EXISTS (SELECT 1 FROM room_members m WHERE m.room_id = q.room_id AND m.member_id = q.agent_id)`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NeedsYouItem
	for rows.Next() {
		var it NeedsYouItem
		var reason, name string
		var at int64
		if err := rows.Scan(&it.JoinRequestID, &it.ProjectID, &it.RoomID, &it.AgentID, &reason, &at, &name); err != nil {
			return nil, err
		}
		it.Key, it.Kind, it.Severity, it.CreatedAt = "join:"+it.JoinRequestID, NeedsJoinRequest, 3, fromMs(at)
		it.Title = "Asks to join #" + name
		it.Detail = "It joins as an observer: it reads the channel and answers only when mentioned."
		if reason != "" {
			it.Detail = firstLine("Why: "+reason, 160)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

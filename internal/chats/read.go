package chats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ---- the read cursor (D8) ----
//
// A chat keeps the newest message seq the person has seen (chats.last_read_seq). The UI moves it with chat.markRead only while
// the person can see the chat's newest message (the window visible and focused, the feed at the bottom: the rooms' rule), so a
// chat that answered while they were elsewhere lists as unread: its answers after the cursor are counted.

// unreadCols are the two columns chat.list adds for the read cursor: the cursor, and the answers after it.
const unreadCols = `c.last_read_seq, (SELECT COUNT(*) FROM chat_messages m WHERE m.chat_id=c.id AND m.role='assistant' AND m.seq>c.last_read_seq)`

// ReadMark is chat.markRead's result.
type ReadMark struct {
	ChatID      string `json:"chatId"`
	LastReadSeq int64  `json:"lastReadSeq"`
}

// MarkRead moves the chat's read cursor up to seq: never back, never past the chat's newest message. It publishes chat.read when
// the cursor moved. It changes nothing else of the chat (no version, no updated time).
func (s *Service) MarkRead(ctx context.Context, id string, seq int64) (ReadMark, error) {
	if seq < 0 {
		return ReadMark{}, errors.New("seq must be a message's number")
	}
	var out ReadMark
	var projectID string
	moved := false
	err := s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		moved = false
		var cur, newest int64
		err := tx.QueryRowContext(ctx, `SELECT project_id, last_read_seq, COALESCE((SELECT MAX(seq) FROM chat_messages WHERE chat_id=chats.id),0) FROM chats WHERE id=?`, id).
			Scan(&projectID, &cur, &newest)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		if seq > newest {
			seq = newest
		}
		out = ReadMark{ChatID: id, LastReadSeq: cur}
		if seq <= cur {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET last_read_seq=? WHERE id=?`, seq, id); err != nil {
			return err
		}
		out.LastReadSeq, moved = seq, true
		return nil
	})
	if err != nil {
		return ReadMark{}, err
	}
	if moved {
		s.publish(ctx, "chat.read", projectID, map[string]any{"chatId": id, "lastReadSeq": out.LastReadSeq})
	}
	return out, nil
}

// UnreadSummary is chat.unread: how many chats (not archived) have answers the person has not seen, and how many answers.
type UnreadSummary struct {
	Chats    int `json:"chats"`
	Messages int `json:"messages"`
}

// Unread counts the chats with unseen answers, of one project ("" = every chat). Archived chats are left out.
func (s *Service) Unread(ctx context.Context, projectID string) (UnreadSummary, error) {
	q := `SELECT COUNT(*), COALESCE(SUM(n),0) FROM (SELECT ` + unreadCols[len(`c.last_read_seq, `):] + ` AS n FROM chats c WHERE c.archived_at=0`
	var args []any
	if projectID != "" {
		q += ` AND c.project_id=?`
		args = append(args, projectID)
	}
	q += `) WHERE n>0`
	var out UnreadSummary
	err := s.d.DB.Reader().QueryRowContext(ctx, q, args...).Scan(&out.Chats, &out.Messages)
	return out, err
}

// markAllReadTx puts a chat's read cursor at its newest message: a fork or an imported chat starts read.
func markAllReadTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE chats SET last_read_seq=COALESCE((SELECT MAX(seq) FROM chat_messages WHERE chat_id=?),0) WHERE id=?`, id, id)
	return err
}

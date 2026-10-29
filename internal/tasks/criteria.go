package tasks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// What a person or an agent says about one acceptance criterion (decision P125, table acceptance_marks). Who may say what:
//
//   - a person (a "human:" identity) confirms a criterion met or not met, waives it (with a reason), or reopens it (open: their
//     earlier word no longer counts);
//   - an agent claims a criterion met, with the evidence (claim_met). A claim is shown beside the status and never sets it: only
//     a check result or a person confirms a criterion, and only a person waives one (pipeline.CriteriaStatus).
//
// A mark names the criterion by its place in the task's acceptance list and keeps a hash of its text, so a criterion whose text
// is changed later starts open again.

// Marks.
const (
	MarkMet      = "met"
	MarkNotMet   = "not_met"
	MarkWaived   = "waived"
	MarkOpen     = "open"
	MarkClaimMet = "claim_met"
)

// MaxMarkNote bounds a mark's note (a person's reason, an agent's evidence), in characters.
const MaxMarkNote = 600

// ErrNotAPerson is returned when anyone but a person confirms, waives or reopens a criterion.
var ErrNotAPerson = errors.New("only a person can confirm, waive or reopen an acceptance criterion; an agent may only claim one met, with evidence")

// CriterionMark is one row of acceptance_marks.
type CriterionMark struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Criterion int    `json:"criterion"` // its place in Task.Acceptance, from 0
	SHA       string `json:"sha"`       // CriterionSHA of its text when the mark was made
	Mark      string `json:"mark"`
	Actor     string `json:"actor"` // human:<name> or the agent's id
	Note      string `json:"note,omitempty"`
	Created   int64  `json:"created"`
}

// CriterionSHA is the first 16 hex digits of the SHA-256 of a criterion's text.
func CriterionSHA(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])[:16]
}

func isPerson(actor string) bool {
	return strings.HasPrefix(actor, "human:") && len(actor) > len("human:")
}

// MarkCriterion records a mark on the criterion at index of a task's acceptance list: a person's met, not_met, waived (with a
// note: the reason) or open, or an agent's claim_met (with a note: the evidence). The note is redacted and bounded.
func (s *Service) MarkCriterion(ctx context.Context, taskID string, index int, mark, actor, note string) (CriterionMark, error) {
	t, err := s.Get(ctx, taskID)
	if err != nil {
		return CriterionMark{}, err
	}
	if index < 0 || index >= len(t.Acceptance) {
		return CriterionMark{}, fmt.Errorf("tasks: task #%d has %d acceptance criteria; there is no criterion %d", t.Seq, len(t.Acceptance), index+1)
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxMarkNote {
		return CriterionMark{}, fmt.Errorf("tasks: the note is longer than %d characters", MaxMarkNote)
	}
	actor = strings.TrimSpace(actor)
	switch mark {
	case MarkMet, MarkNotMet, MarkOpen:
		if !isPerson(actor) {
			return CriterionMark{}, ErrNotAPerson
		}
	case MarkWaived:
		if !isPerson(actor) {
			return CriterionMark{}, ErrNotAPerson
		}
		if note == "" {
			return CriterionMark{}, errors.New("tasks: say why the criterion is waived")
		}
	case MarkClaimMet:
		if actor == "" || strings.HasPrefix(actor, "human:") {
			return CriterionMark{}, errors.New("tasks: a claim is an agent's; a person marks the criterion met")
		}
		if note == "" {
			return CriterionMark{}, errors.New("tasks: a claim that a criterion is met needs its evidence (a test that shows it, say)")
		}
	default:
		return CriterionMark{}, fmt.Errorf("tasks: unknown mark %q (met, not_met, waived, open or claim_met)", mark)
	}
	m := CriterionMark{ID: ids.New("acm"), TaskID: taskID, Criterion: index, SHA: CriterionSHA(t.Acceptance[index]), Mark: mark, Actor: actor,
		Note: s.red(note), Created: storage.Now()}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO acceptance_marks(id,task_id,criterion,criterion_sha,mark,actor,note,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			m.ID, m.TaskID, m.Criterion, m.SHA, m.Mark, m.Actor, m.Note, m.Created)
		return err
	}); err != nil {
		return CriterionMark{}, err
	}
	s.emit(ctx, "task.criterion", t, map[string]any{"criterion": index, "mark": mark, "by": actor})
	return m, nil
}

// CriterionMarks lists a task's marks, oldest first.
func (s *Service) CriterionMarks(ctx context.Context, taskID string) ([]CriterionMark, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id,task_id,criterion,criterion_sha,mark,actor,note,created_at FROM acceptance_marks
		WHERE task_id=? ORDER BY created_at, rowid`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CriterionMark
	for rows.Next() {
		var m CriterionMark
		if err := rows.Scan(&m.ID, &m.TaskID, &m.Criterion, &m.SHA, &m.Mark, &m.Actor, &m.Note, &m.Created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

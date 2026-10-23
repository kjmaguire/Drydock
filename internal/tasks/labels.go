package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/kjmaguire/drydock/internal/storage"
)

// A task's labels (decision P97, migration 00130) are short lower-case words a person sorts tasks by: "auth", "flaky-test".

// Limits of a task's labels.
const (
	MaxLabels   = 8
	MaxLabelLen = 24
)

// NormalizeLabels returns the labels as a task keeps them: trimmed, lower-case, inner spaces made single, empty ones and repeats
// left out, in the order given. More than MaxLabels, one longer than MaxLabelLen characters, or one with a character other than
// a letter, a digit, a space or - _ . / : is refused.
func NormalizeLabels(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range in {
		l = strings.ToLower(strings.Join(strings.Fields(l), " "))
		if l == "" || seen[l] {
			continue
		}
		if n := len([]rune(l)); n > MaxLabelLen {
			return nil, fmt.Errorf("tasks: the label %q is %d characters; a label is at most %d", l, n, MaxLabelLen)
		}
		for _, r := range l {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" -_./:", r) {
				return nil, fmt.Errorf("tasks: the label %q has %q in it; use letters, digits, spaces and - _ . / :", l, r)
			}
		}
		seen[l] = true
		out = append(out, l)
	}
	if len(out) > MaxLabels {
		return nil, fmt.Errorf("tasks: a task has at most %d labels, not %d", MaxLabels, len(out))
	}
	return out, nil
}

// SetLabels replaces a task's labels (normalized by NormalizeLabels) and publishes task.updated with them.
func (s *Service) SetLabels(ctx context.Context, id string, labels []string) (Task, error) {
	ls, err := NormalizeLabels(labels)
	if err != nil {
		return Task{}, err
	}
	b, _ := json.Marshal(ls)
	var n int64
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE tasks SET labels=?, updated_at=?, version=version+1 WHERE id=?`, string(b), storage.Now(), id)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	if n == 0 {
		return Task{}, ErrNotFound
	}
	t, err := s.Get(ctx, id)
	if err != nil {
		return t, err
	}
	s.emit(ctx, "task.updated", t, map[string]any{"labels": t.Labels})
	return t, nil
}

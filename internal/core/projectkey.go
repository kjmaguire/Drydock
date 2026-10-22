package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// A project's key is the prefix of its task identifiers (decision P97): "GOC-41" is task 41 of the project whose key is GOC. Two
// to five upper-case letters or digits, unique among projects (migration 00129). A new project takes one made from its name; the
// person can change it in the project's settings (project.update {key}).

// projectKeyRE is what a key may be.
var projectKeyRE = regexp.MustCompile(`^[A-Z0-9]{2,5}$`)

const (
	minKeyLen = 2
	maxKeyLen = 5
	// keyFallback is the base of a name with fewer than two letters or digits ("!", "日本").
	keyFallback = "PRJ"
)

// keyBase is the key a name suggests before any other project is considered: the first letters of its words when it has two or
// more ("my web app" is MWA, at most five), else its first three letters or digits ("gocalc" is GOC). Only ASCII letters and
// digits count; a name with fewer than two of them gets PRJ.
func keyBase(name string) string {
	words := strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool { return !isKeyChar(r) })
	if len(words) >= 2 {
		var b strings.Builder
		for _, w := range words {
			if b.Len() == maxKeyLen {
				break
			}
			b.WriteByte(w[0])
		}
		return b.String()
	}
	joined := strings.Join(words, "")
	if len(joined) < minKeyLen {
		return keyFallback
	}
	return joined[:min(3, len(joined))]
}

func isKeyChar(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') }

// keyCandidates are the keys tried for a base, in order: the base, then the base with one more character (2 to 9, then A to Z),
// the last character replaced when the base is already five long, then two more characters.
func keyCandidates(base string) []string {
	const more = "23456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	out := []string{base}
	one := base
	if len(one) >= maxKeyLen {
		one = one[:maxKeyLen-1]
	}
	for _, c := range more {
		out = append(out, one+string(c))
	}
	two := base
	if len(two) > maxKeyLen-2 {
		two = two[:maxKeyLen-2]
	}
	for _, a := range more {
		for _, b := range more {
			out = append(out, two+string(a)+string(b))
		}
	}
	return out
}

// deriveProjectKey is the first candidate of the name's base that taken does not have.
func deriveProjectKey(name string, taken map[string]bool) (string, error) {
	for _, k := range keyCandidates(keyBase(name)) {
		if !taken[k] {
			return k, nil
		}
	}
	return "", errors.New("no free project key is left for this name: set one in the project's settings")
}

// takenKeys reads every project's key, those of except left out (the project being renamed keeps no hold on its old key).
func takenKeys(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, except string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT key FROM projects WHERE key <> '' AND id <> ?`, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	taken := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		taken[k] = true
	}
	return taken, rows.Err()
}

// normalizeProjectKey checks a key a person typed: trimmed and upper-cased, then two to five letters or digits.
func normalizeProjectKey(key string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(key))
	if !projectKeyRE.MatchString(k) {
		return "", fmt.Errorf("a project key is %d to %d letters or digits (like GOC), not %q", minKeyLen, maxKeyLen, key)
	}
	return k, nil
}

// SetProjectKey changes a project's key. Another project's key is refused; the tasks keep their numbers, so GOC-41 becomes
// the new key with 41.
func (c *Core) SetProjectKey(ctx context.Context, projectID, key string) (Project, error) {
	k, err := normalizeProjectKey(key)
	if err != nil {
		return Project{}, err
	}
	p, err := c.GetProject(ctx, projectID)
	if err != nil {
		return p, err
	}
	if p.Key == k {
		return p, nil
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var other string
		switch err := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE key=? AND id<>?`, k, projectID).Scan(&other); {
		case err == nil:
			return fmt.Errorf("the key %s is the project %q's: choose another", k, other)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE projects SET key=?, version=version+1 WHERE id=?`, k, projectID)
		return err
	})
	if err != nil {
		return p, err
	}
	c.publish(ctx, "project.updated", projectID, map[string]any{"key": k})
	return c.GetProject(ctx, projectID)
}

// backfillProjectKeys gives every project without a key (those made before migration 00129) one from its name, oldest project
// first, so the first of two "gocalc" projects keeps GOC.
func (c *Core) backfillProjectKeys(ctx context.Context) error {
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		taken, err := takenKeys(ctx, tx, "")
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, name FROM projects WHERE key='' ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		type row struct{ id, name string }
		var todo []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.name); err != nil {
				rows.Close()
				return err
			}
			todo = append(todo, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range todo {
			k, err := deriveProjectKey(r.name, taken)
			if err != nil {
				return fmt.Errorf("project %s: %w", r.id, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET key=? WHERE id=?`, k, r.id); err != nil {
				return err
			}
			taken[k] = true
		}
		return nil
	})
}

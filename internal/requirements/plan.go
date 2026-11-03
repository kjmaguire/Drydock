package requirements

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/packs"
)

// PlanText is a requirement document as the planner reads it (X48): its title, then its current passages in order, each under
// its heading path. Passages kept out of search because they look like instructions to an agent are left out of it too
// (Skipped counts them), so choosing a document for a plan never hands the planner what search refuses it.
func (s *Service) PlanText(ctx context.Context, id string) (title, text string, skipped int, err error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return "", "", 0, err
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT heading_path, body, quarantined FROM requirement_chunks WHERE requirement_id=? AND current=1 ORDER BY ordinal, id`, id)
	if err != nil {
		return "", "", 0, err
	}
	defer rows.Close()
	var b strings.Builder
	last := ""
	for rows.Next() {
		var path, body string
		var q int
		if err := rows.Scan(&path, &body, &q); err != nil {
			return "", "", 0, err
		}
		if q != 0 {
			skipped++
			continue
		}
		if path != "" && path != last {
			b.WriteString("### " + path + "\n\n")
			last = path
		}
		b.WriteString(strings.TrimSpace(body) + "\n\n")
	}
	return r.Title, strings.TrimSpace(b.String()), skipped, rows.Err()
}

// Suggestion is an open task whose own words match a passage of a requirement (X43): a text match the person confirms with
// Link, never a link by itself.
type Suggestion struct {
	TaskID  string  `json:"taskId"`
	Seq     int     `json:"seq"`
	Title   string  `json:"title"`
	Heading string  `json:"heading"` // the heading path of the passage that matched
	Matched string  `json:"matched"` // what of the task matched: "title" or "criterion"
	Score   float64 `json:"score"`
}

// gwtWords are the Given/When/Then words of a criterion: they say nothing about what it covers.
var gwtWords = regexp.MustCompile(`(?i)\b(given|when|then|and)\b`)

// suggestTaskLimit bounds how many open tasks one suggestion pass reads (the newest first).
const suggestTaskLimit = 500

// SuggestLinks proposes up to k open tasks of the requirement's project that likely cover it: a task's title, or one of its
// acceptance criteria, whose words all occur in one current passage of this document (the AND pass of search only, so a
// task sharing one common word does not show). Tasks already linked to the requirement are left out. It reads the database
// only; nothing is linked.
func (s *Service) SuggestLinks(ctx context.Context, reqID string, k int) ([]Suggestion, error) {
	req, err := s.Get(ctx, reqID)
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		k = 5
	}
	if k > 20 {
		k = 20
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id, seq, title, acceptance FROM tasks
		WHERE project_id=? AND status NOT IN ('COMPLETED','FAILED','CANCELLED')
		AND id NOT IN (SELECT task_id FROM requirement_links WHERE requirement_id=?)
		ORDER BY seq DESC LIMIT ?`, req.ProjectID, reqID, suggestTaskLimit)
	if err != nil {
		return nil, err
	}
	type cand struct {
		id, title string
		seq       int
		criteria  []string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		var acc string
		if err := rows.Scan(&c.id, &c.seq, &c.title, &acc); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(acc), &c.criteria)
		cands = append(cands, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := []Suggestion{}
	for _, c := range cands {
		best, ok, err := s.bestMatch(ctx, req.ProjectID, reqID, c.title)
		if err != nil {
			return nil, err
		}
		matched := "title"
		for i, cr := range c.criteria {
			if i >= 5 {
				break
			}
			h, found, err := s.bestMatch(ctx, req.ProjectID, reqID, gwtWords.ReplaceAllString(criterionID.ReplaceAllString(cr, ""), " "))
			if err != nil {
				return nil, err
			}
			if found && (!ok || h.Score > best.Score) {
				best, ok, matched = h, true, "criterion"
			}
		}
		if ok {
			out = append(out, Suggestion{TaskID: c.id, Seq: c.seq, Title: c.title, Heading: best.HeadingPath, Matched: matched, Score: best.Score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// bestMatch is the best passage of one document that holds every word of text (at least two words after stop words).
func (s *Service) bestMatch(ctx context.Context, projectID, docID, text string) (Hit, bool, error) {
	andQ, _, terms := packs.BuildMatch(text)
	if andQ == "" || len(terms) < 2 {
		return Hit{}, false, nil
	}
	hs, err := s.queryFTS(ctx, projectID, docID, andQ, 1)
	if err != nil {
		return Hit{}, false, err
	}
	if len(hs) == 0 {
		return Hit{}, false, nil
	}
	return hs[0], true, nil
}

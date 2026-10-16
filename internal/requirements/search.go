package requirements

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/packs"
)

// Hit is one retrieved requirement chunk.
type Hit struct {
	// Citation is "REQ:<doc>@<version>#<heading path>".
	Citation    string  `json:"citation"`
	DocID       string  `json:"docId"`
	Title       string  `json:"title"`
	Version     int     `json:"version"`
	HeadingPath string  `json:"headingPath"`
	ChunkID     int64   `json:"chunkId"`
	Text        string  `json:"text"`
	Score       float64 `json:"score"`
	AllTerms    bool    `json:"allTerms"`
}

// CitationFor renders the citation string.
func CitationFor(docID string, version int, headingPath string) string {
	return fmt.Sprintf("REQ:%s@%d#%s", docID, version, headingPath)
}

// Search finds chunks of a project's current, non-archived documents, optionally restricted to one document. It uses the same
// query builder and ranking as pack search: AND of the terms first, OR when fewer than k chunks match, bm25 with the heading
// path weighted 4 against 1 for the body, at most two chunks per document.
func (s *Service) Search(ctx context.Context, projectID, query, docID string, k int) ([]Hit, error) {
	if projectID == "" {
		return nil, errors.New("requirements: a project is required")
	}
	if k <= 0 {
		k = 5
	}
	if k > 20 {
		k = 20
	}
	andQ, orQ, _ := packs.BuildMatch(query)
	if andQ == "" {
		return nil, nil
	}
	var out []Hit
	seen := map[int64]bool{}
	perDoc := map[string]int{}
	pass := func(match string, all bool, limit int) error {
		hs, err := s.queryFTS(ctx, projectID, docID, match, limit)
		if err != nil {
			return err
		}
		for _, h := range hs {
			if len(out) >= k {
				break
			}
			if seen[h.ChunkID] || perDoc[h.DocID] >= 2 {
				continue
			}
			seen[h.ChunkID] = true
			perDoc[h.DocID]++
			h.AllTerms = all
			out = append(out, h)
		}
		return nil
	}
	if err := pass(andQ, true, k*4); err != nil {
		return nil, err
	}
	if len(out) < k && orQ != andQ {
		if err := pass(orQ, false, k*6); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) queryFTS(ctx context.Context, projectID, docID, match string, limit int) ([]Hit, error) {
	q := `SELECT c.id, c.requirement_id, r.title, c.version, c.heading_path, c.body, bm25(requirement_chunks_fts, 4.0, 1.0) AS rank
		FROM requirement_chunks_fts
		JOIN requirement_chunks c ON c.id = requirement_chunks_fts.rowid
		JOIN requirements r ON r.id = c.requirement_id
		WHERE requirement_chunks_fts MATCH ? AND r.project_id = ? AND r.archived = 0 AND c.current = 1 AND c.quarantined = 0`
	args := []any{match, projectID}
	if docID != "" {
		q += ` AND c.requirement_id = ?`
		args = append(args, docID)
	}
	q += ` ORDER BY rank, c.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var rank float64
		if err := rows.Scan(&h.ChunkID, &h.DocID, &h.Title, &h.Version, &h.HeadingPath, &h.Text, &rank); err != nil {
			return nil, err
		}
		h.Score = -rank
		h.Citation = CitationFor(h.DocID, h.Version, h.HeadingPath)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ResolveCitation returns the chunk a "REQ:<doc>@<version>#<heading path>" citation points at, including chunks of older
// versions (their text is kept so a citation in an old minute or PR draft still resolves).
func (s *Service) ResolveCitation(ctx context.Context, citation string) (Hit, error) {
	rest, ok := strings.CutPrefix(citation, "REQ:")
	if !ok {
		return Hit{}, fmt.Errorf("%w: not a requirement citation", ErrNotFound)
	}
	at := strings.Index(rest, "@")
	hash := strings.Index(rest, "#")
	if at < 0 || hash < at {
		return Hit{}, fmt.Errorf("%w: malformed citation", ErrNotFound)
	}
	doc, verStr, path := rest[:at], rest[at+1:hash], rest[hash+1:]
	var ver int
	if _, err := fmt.Sscanf(verStr, "%d", &ver); err != nil {
		return Hit{}, fmt.Errorf("%w: malformed version", ErrNotFound)
	}
	var h Hit
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT c.id, c.requirement_id, r.title, c.version, c.heading_path, c.body FROM requirement_chunks c JOIN requirements r ON r.id=c.requirement_id
		WHERE c.requirement_id=? AND c.version=? AND c.heading_path=? AND c.quarantined=0 ORDER BY c.ordinal LIMIT 1`, doc, ver, path).Scan(&h.ChunkID, &h.DocID, &h.Title, &h.Version, &h.HeadingPath, &h.Text)
	if err != nil {
		return Hit{}, fmt.Errorf("%w: %s", ErrNotFound, citation)
	}
	h.Citation = CitationFor(h.DocID, h.Version, h.HeadingPath)
	return h, nil
}

// MaxHitChars bounds one search_requirements hit (about 500 tokens).
const MaxHitChars = 2000

// SearchProvider implements mcp.RequirementsSearcher (the search_requirements tool). It reads only the run's own project.
type SearchProvider struct{ S *Service }

// NewSearchProvider returns the provider to assign to mcp.Workspace.Requirements.
func NewSearchProvider(s *Service) mcp.RequirementsSearcher { return &SearchProvider{S: s} }

// Search implements mcp.RequirementsSearcher.
func (p *SearchProvider) Search(ctx context.Context, run mcp.RunInfo, query, docID string, k int) ([]mcp.TextHit, error) {
	if run.ProjectID == "" {
		return nil, errors.New("this run has no project")
	}
	hits, err := p.S.Search(ctx, run.ProjectID, query, docID, k)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.TextHit, 0, len(hits))
	for _, h := range hits {
		text := h.Text
		if r := []rune(text); len(r) > MaxHitChars {
			text = string(r[:MaxHitChars]) + " ..."
		}
		out = append(out, mcp.TextHit{ID: h.Citation, Source: h.Title, Title: h.HeadingPath, Text: text, Score: h.Score})
	}
	return out, nil
}

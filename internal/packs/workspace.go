package packs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/mcp"
)

// MaxHitChars bounds the text of one search_pack hit (about 500 tokens): retrieval stays cheap even when chunks are large.
const MaxHitChars = 2000

// WorkspaceProvider implements mcp.PackSearcher: the search_pack tool of the Workspace server. It reads only the packs
// attached to the calling run's agent (ATTACH), so a pack is never searchable by an agent it was not attached to.
type WorkspaceProvider struct{ Store *Store }

// NewWorkspaceProvider returns the provider to assign to mcp.Workspace.Packs.
func NewWorkspaceProvider(s *Store) mcp.PackSearcher { return &WorkspaceProvider{Store: s} }

// Search implements mcp.PackSearcher.
func (w *WorkspaceProvider) Search(ctx context.Context, run mcp.RunInfo, query string, packIDs []string, k int) ([]mcp.PackHit, error) {
	attached, err := w.Store.AttachedIDs(ctx, run.ProjectID, run.AgentID, run.Role)
	if err != nil {
		return nil, err
	}
	if len(attached) == 0 {
		return nil, errors.New("no expert packs are attached to this agent (a human attaches packs per project, agent or role)")
	}
	ids := attached
	if len(packIDs) > 0 {
		ok := map[string]bool{}
		for _, a := range attached {
			ok[a] = true
		}
		var bad []string
		ids = nil
		for _, p := range dedupe(packIDs) {
			if ok[p] {
				ids = append(ids, p)
			} else {
				bad = append(bad, p)
			}
		}
		if len(bad) > 0 {
			return nil, fmt.Errorf("pack(s) not attached to this agent: %s (attached: %s)", strings.Join(bad, ", "), strings.Join(attached, ", "))
		}
	}
	if k <= 0 {
		k = 5
	}
	res, err := w.Store.Search(ctx, SearchRequest{PackIDs: ids, Query: query, K: k})
	if err != nil {
		return nil, err
	}
	out := make([]mcp.PackHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		c := h.Citation
		id := fmt.Sprintf("%d#%s", c.ChunkID, shortHash(c.ContentHash))
		if c.Origin == "live" {
			id = "live#" + shortHash(c.ContentHash)
		}
		out = append(out, mcp.PackHit{
			Pack: c.Pack, Version: c.Version, URL: c.URL, HeadingPath: strings.Split(c.HeadingPath, PathSep), ChunkID: id,
			FetchedAt: c.FetchedAt.Truncate(time.Second), Text: truncateRunes(h.Text, MaxHitChars),
		})
	}
	return out, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

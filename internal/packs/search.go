package packs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Citation locates the source of a hit. It is built from stored metadata, never from model output.
type Citation struct {
	Pack         string    `json:"pack"`
	Version      string    `json:"version"`
	URL          string    `json:"url"`
	HeadingPath  string    `json:"headingPath"`
	ChunkID      int64     `json:"chunkId,omitempty"`
	FetchedAt    time.Time `json:"fetchedAt,omitempty"`
	ContentHash  string    `json:"contentHash"` // sha256 of the chunk (heading path + body)
	PageHash     string    `json:"pageHash,omitempty"`
	Origin       string    `json:"origin"` // indexed | live
	VersionAdded string    `json:"versionAdded,omitempty"`
}

// Hit is one retrieved chunk.
type Hit struct {
	Citation Citation `json:"citation"`
	Text     string   `json:"text"`
	Snippet  string   `json:"snippet,omitempty"`
	// Score is higher-is-better (the negated BM25 rank); live hits carry the server's similarity or 0.
	Score float64 `json:"score"`
	// AllTerms is true when the chunk matched every query term (the AND pass), false for partial (OR pass) matches.
	AllTerms bool   `json:"allTerms"`
	Origin   string `json:"origin"`
}

// SearchRequest is one retrieval. PackIDs empty means every installed pack (cross-pack search).
type SearchRequest struct {
	PackIDs []string
	Query   string
	K       int
	// SkipLive searches only the local indexes.
	SkipLive bool
}

// SearchResult is the outcome. Found is false only when no chunk matched any query term.
type SearchResult struct {
	Found bool  `json:"found"`
	Hits  []Hit `json:"hits"`
	// Searched lists the packs that were queried.
	Searched []string `json:"searched"`
	// Unavailable lists live packs that could not be reached (offline, server down, query refused) with the reason.
	Unavailable map[string]string `json:"unavailable,omitempty"`
	// RefreshSuggested lists indexed packs past their stale_after.
	RefreshSuggested []string `json:"refreshSuggested,omitempty"`
	// Warn lists indexed packs past warn_after.
	Warn []string `json:"warn,omitempty"`
	// Attributions are the license attribution lines to append to exports, once each.
	Attributions []string `json:"attributions,omitempty"`
	Terms        []string `json:"terms,omitempty"`
	// SuspiciousDropped counts live results withheld because the injection scan scored them at or above the quarantine threshold.
	SuspiciousDropped int    `json:"suspiciousDropped,omitempty"`
	Confidence        string `json:"confidence"`
	// SourceAgeDays is the age of the best indexed hit (-1 when there is none: live results have no known age).
	SourceAgeDays int `json:"sourceAgeDays"`
}

// Confidence levels. They come from code, not from a model, and are not calibrated (docs/expert-packs.md section 6).
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Search is the one retrieval function behind search_pack and CONSULT.
func (s *Store) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	res := SearchResult{Hits: []Hit{}, SourceAgeDays: -1, Confidence: ConfidenceLow}
	k := req.K
	if k <= 0 {
		k = 5
	}
	if k > 20 {
		k = 20
	}
	ids := req.PackIDs
	if len(ids) == 0 {
		var err error
		if ids, err = s.packIDs(ctx); err != nil {
			return res, err
		}
	}
	infos := map[string]packInfo{}
	var order []string
	for _, id := range dedupe(ids) {
		r, m, err := s.loadRow(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return res, fmt.Errorf("%w: %s", ErrUnknownPack, id)
			}
			return res, err
		}
		infos[id] = packInfo{r, m}
		order = append(order, id)
	}
	res.Searched = order
	andQ, orQ, terms := BuildMatch(req.Query)
	res.Terms = terms
	if andQ == "" {
		return res, nil
	}
	now := s.now()
	var local []string
	for _, id := range order {
		if infos[id].row.kind != KindLive {
			local = append(local, id)
		}
	}
	// live reserve: a supplementing server gets a share of the slots when it has anything to say
	liveHits := map[string][]Hit{}
	liveWanted := false
	if !req.SkipLive {
		for _, id := range order {
			if infos[id].m.Live != nil {
				liveWanted = true
			}
		}
	}
	localHits, err := s.searchLocal(ctx, local, andQ, orQ, k)
	if err != nil {
		return res, err
	}
	perPack := map[string]int{}
	for _, h := range localHits {
		perPack[h.Citation.Pack]++
	}
	if liveWanted {
		for _, id := range order {
			in := infos[id]
			if in.m.Live == nil {
				continue
			}
			need := false
			switch {
			case in.row.kind == KindLive:
				need = true
			case in.m.Live.Use == UseLiveFallback:
				need = perPack[id] < k
			default: // primary or supplement on a hybrid pack
				need = true
			}
			if !need {
				continue
			}
			if s.o.Live == nil {
				if res.Unavailable == nil {
					res.Unavailable = map[string]string{}
				}
				res.Unavailable[id] = "no gateway is configured for live packs"
				continue
			}
			hs, err := s.o.Live.Search(ctx, LiveSpec{MCP: in.m.Live.MCP, Tool: in.m.Live.Tool, Arg: in.m.Live.Arg}, req.Query, k)
			if err != nil {
				if res.Unavailable == nil {
					res.Unavailable = map[string]string{}
				}
				res.Unavailable[id] = s.o.Scanner.Redact(err.Error())
				continue
			}
			for _, lh := range hs {
				sc := ScanChunk(lh.Text)
				if sc.Quarantined {
					res.SuspiciousDropped++
					continue
				}
				text := sc.Text
				liveHits[id] = append(liveHits[id], Hit{
					Origin: "live", Text: text, Score: lh.Score, AllTerms: false,
					Citation: Citation{Pack: id, Version: "live", URL: lh.URL, HeadingPath: lh.Title, FetchedAt: now, ContentHash: HashHex(lh.URL, lh.Title, text), Origin: "live"},
				})
			}
		}
	}
	// merge: local first; supplement results are reserved a slot or two
	var live []Hit
	for _, id := range order {
		live = append(live, liveHits[id]...)
	}
	localCap := k
	if len(live) > 0 && len(localHits) >= k {
		localCap = k - min(len(live), max(1, k/3))
		if localCap < 1 {
			localCap = 1
		}
	}
	if len(localHits) > localCap {
		localHits = localHits[:localCap]
	}
	res.Hits = append(res.Hits, localHits...)
	seenURL := map[string]bool{}
	for _, h := range res.Hits {
		seenURL[h.Citation.Pack+"\x00"+h.Citation.URL] = true
	}
	for _, h := range live {
		if len(res.Hits) >= k {
			break
		}
		if seenURL[h.Citation.Pack+"\x00"+h.Citation.URL] {
			continue
		}
		res.Hits = append(res.Hits, h)
	}
	res.Found = len(res.Hits) > 0

	// attribution, freshness
	seenAttr := map[string]bool{}
	hitPacks := map[string]bool{}
	for _, h := range res.Hits {
		hitPacks[h.Citation.Pack] = true
	}
	for _, id := range order {
		in := infos[id]
		if hitPacks[id] && in.m.License.AttributionRequired && !seenAttr[in.m.License.Attribution] {
			seenAttr[in.m.License.Attribution] = true
			res.Attributions = append(res.Attributions, in.m.License.Attribution)
		}
		if in.row.kind == KindLive || in.row.lastOK == 0 {
			continue
		}
		age := now.Sub(time.UnixMilli(in.row.lastOK))
		if age > in.m.Freshness.StaleAfter.D() {
			res.RefreshSuggested = append(res.RefreshSuggested, id)
		} else if age > in.m.Freshness.WarnAfter.D() {
			res.Warn = append(res.Warn, id)
		}
	}
	res.Confidence, res.SourceAgeDays = confidence(res.Hits, infos, now)
	return res, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

type packInfo struct {
	row packRow
	m   *Manifest
}

// confidence is a heuristic from code (docs/expert-packs.md section 6): high when the best hit matches every query term
// and its source is within warn_after; low when only partial matches, live-only results, or a source past stale_after;
// otherwise medium. It is not calibrated.
func confidence(hits []Hit, m map[string]packInfo, now time.Time) (string, int) {
	if len(hits) == 0 {
		return ConfidenceLow, -1
	}
	var best *Hit
	for i := range hits {
		if hits[i].Origin == "indexed" {
			best = &hits[i]
			break
		}
	}
	if best == nil {
		return ConfidenceLow, -1
	}
	age := now.Sub(best.Citation.FetchedAt)
	days := int(age.Hours() / 24)
	if days < 0 {
		days = 0
	}
	warn, stale := 14*24*time.Hour, 30*24*time.Hour
	if in, ok := m[best.Citation.Pack]; ok && in.m != nil {
		warn, stale = in.m.Freshness.WarnAfter.D(), in.m.Freshness.StaleAfter.D()
	}
	top := hits[0]
	switch {
	case !top.AllTerms && top.Origin == "indexed", age > stale:
		return ConfidenceLow, days
	case top.Origin == "indexed" && top.AllTerms && age <= warn:
		return ConfidenceHigh, days
	}
	return ConfidenceMedium, days
}

// searchLocal runs the AND pass, then the OR pass if the AND pass left fewer than k hits, ranking by
// bm25(pack_chunks_fts, 4.0, 1.0) (the heading path counts four times a body term) over live chunks of non-quarantined
// content, and keeps at most two chunks per page.
func (s *Store) searchLocal(ctx context.Context, packIDs []string, andQ, orQ string, k int) ([]Hit, error) {
	if len(packIDs) == 0 {
		return nil, nil
	}
	var out []Hit
	seen := map[int64]bool{}
	perPage := map[int64]int{}
	pass := func(match string, allTerms bool, limit int) error {
		hs, pages, err := s.queryFTS(ctx, packIDs, match, limit)
		if err != nil {
			return err
		}
		for i, h := range hs {
			if len(out) >= k {
				break
			}
			if seen[h.Citation.ChunkID] || perPage[pages[i]] >= 2 {
				continue
			}
			seen[h.Citation.ChunkID] = true
			perPage[pages[i]]++
			h.AllTerms = allTerms
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

func (s *Store) queryFTS(ctx context.Context, packIDs []string, match string, limit int) ([]Hit, []int64, error) {
	args := []any{match}
	ph := make([]string, len(packIDs))
	for i, id := range packIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	args = append(args, limit)
	q := `SELECT c.id, c.pack_id, c.page_id, c.heading_path, c.body, p.url, p.fetched_at, p.content_hash, c.chunk_hash, c.version_added, pk.version,
		bm25(pack_chunks_fts, 4.0, 1.0) AS rank, snippet(pack_chunks_fts, 1, '[', ']', '...', 24)
		FROM pack_chunks_fts
		JOIN pack_chunks c ON c.id = pack_chunks_fts.rowid
		JOIN pack_pages p ON p.id = c.page_id
		JOIN packs pk ON pk.id = c.pack_id
		WHERE pack_chunks_fts MATCH ? AND c.seq_removed IS NULL AND c.quarantined = 0 AND p.status = 'active' AND c.pack_id IN (` + strings.Join(ph, ",") + `)
		ORDER BY rank, c.id LIMIT ?`
	rows, err := s.o.DB.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var hits []Hit
	var pages []int64
	for rows.Next() {
		var h Hit
		var pageID, fetched int64
		var rank float64
		var curVersion string
		if err := rows.Scan(&h.Citation.ChunkID, &h.Citation.Pack, &pageID, &h.Citation.HeadingPath, &h.Text, &h.Citation.URL, &fetched, &h.Citation.PageHash,
			&h.Citation.ContentHash, &h.Citation.VersionAdded, &curVersion, &rank, &h.Snippet); err != nil {
			return nil, nil, err
		}
		h.Citation.Version = curVersion
		h.Citation.FetchedAt = time.UnixMilli(fetched).UTC()
		h.Citation.Origin, h.Origin = "indexed", "indexed"
		h.Score = -rank
		hits = append(hits, h)
		pages = append(pages, pageID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// a stable order for equal ranks (rows with the same rank come back in id order already)
	idx := make([]int, len(hits))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return hits[idx[a]].Score > hits[idx[b]].Score })
	oh := make([]Hit, len(hits))
	op := make([]int64, len(hits))
	for i, j := range idx {
		oh[i], op[i] = hits[j], pages[j]
	}
	return oh, op, nil
}

// ResolveCitation returns the chunk a stored citation points at, if its version is still retained: the chunk must have been
// live in that version. Quarantined chunks do not resolve.
func (s *Store) ResolveCitation(ctx context.Context, packID, version string, chunkID int64) (Hit, error) {
	var h Hit
	var seq int64
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT seq FROM pack_versions WHERE pack_id=? AND version=?`, packID, version).Scan(&seq); err != nil {
		return h, fmt.Errorf("%w: %s@%s", ErrNotFound, packID, version)
	}
	var fetched int64
	var removed *int64
	var added int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT c.id,c.heading_path,c.body,p.url,p.fetched_at,p.content_hash,c.chunk_hash,c.version_added,c.seq_added,c.seq_removed
		FROM pack_chunks c JOIN pack_pages p ON p.id=c.page_id WHERE c.id=? AND c.pack_id=? AND c.quarantined=0`, chunkID, packID).
		Scan(&h.Citation.ChunkID, &h.Citation.HeadingPath, &h.Text, &h.Citation.URL, &fetched, &h.Citation.PageHash, &h.Citation.ContentHash, &h.Citation.VersionAdded, &added, &removed)
	if err != nil {
		return h, fmt.Errorf("%w: chunk %d of %s", ErrNotFound, chunkID, packID)
	}
	if added > seq || (removed != nil && *removed <= seq) {
		return h, fmt.Errorf("%w: chunk %d was not part of %s@%s", ErrNotFound, chunkID, packID, version)
	}
	h.Citation.Pack, h.Citation.Version, h.Citation.Origin, h.Origin = packID, version, "indexed", "indexed"
	h.Citation.FetchedAt = time.UnixMilli(fetched).UTC()
	return h, nil
}

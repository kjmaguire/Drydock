package packs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RefreshOptions tunes one refresh.
type RefreshOptions struct {
	// Force ignores stored ETag/Last-Modified validators and re-downloads every page.
	Force bool
}

// RefreshResult summarizes a refresh.
type RefreshResult struct {
	PackID      string         `json:"packId"`
	Version     string         `json:"version"`
	NewVersion  bool           `json:"newVersion"`
	Pages       int            `json:"pages"`
	Added       int            `json:"added"`
	Changed     int            `json:"changed"`
	Removed     int            `json:"removed"`
	Unchanged   int            `json:"unchanged"`
	Skipped     int            `json:"skipped"`
	Failed      int            `json:"failed"`
	Reasons     map[string]int `json:"reasons,omitempty"` // why pages were skipped or failed
	Quarantined int            `json:"quarantined"`
	Redactions  int            `json:"redactions"`
	Digest      *Digest        `json:"digest,omitempty"`
}

// Digest is the changed-pages digest of one refresh. It is built from the page diffs without any model; Summary is filled
// only when a Summarizer is configured and the route allows it.
type Digest struct {
	PackID   string       `json:"packId"`
	Version  string       `json:"version"`
	Previous string       `json:"previous,omitempty"`
	Changes  []PageChange `json:"changes"`
	Summary  string       `json:"summary,omitempty"`
}

// PageChange is one page in a digest.
type PageChange struct {
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Status       string   `json:"status"` // added | changed | removed
	LinesAdded   int      `json:"linesAdded"`
	LinesRemoved int      `json:"linesRemoved"`
	Headings     []string `json:"headings,omitempty"` // headings of the chunks that appeared or vanished (at most 5)
}

// Summarizer is the optional cheap-model step of a refresh (docs/expert-packs.md section 4, step 7). AllowTrainable is true
// only for a public pack whose manifest allows the trainable route; the implementation must honour it. A refresh never
// depends on it: an error just leaves the digest without a summary.
type Summarizer interface {
	Summarize(ctx context.Context, d Digest, allowTrainable bool) (string, error)
}

type existingPage struct {
	id                       int64
	hash, etag, lastModified string
	rawRef, status, firstVer string
}

type pageResult struct {
	page    Page
	status  string // new | changed | unchanged | skipped | failed
	reason  string
	title   string
	hash    string
	text    string // normalized, redacted
	etag    string
	lm      string
	chunks  []preparedChunk
	risk    int
	redact  int
	oldText string
}

type preparedChunk struct {
	Chunk
	risk        int
	flags       string
	quarantined bool
}

// Refresh re-ingests an indexed or hybrid pack from its configured sources and commits a new version in one write
// transaction. A failure leaves the previous version untouched and emits pack.refresh_failed. by is a human principal or
// "system" (the scheduler).
func (s *Store) Refresh(ctx context.Context, by, packID string, opt RefreshOptions) (res RefreshResult, err error) {
	if err := requireHumanOrSystem(by); err != nil {
		return RefreshResult{}, err
	}
	row, m, err := s.loadRow(ctx, packID)
	if err != nil {
		return RefreshResult{}, err
	}
	if row.kind == KindLive {
		return RefreshResult{}, ErrNotIndexed
	}
	mu := s.lock(packID)
	if !mu.TryLock() {
		return RefreshResult{}, ErrBusy
	}
	defer mu.Unlock()

	start := s.now()
	_ = s.setStatus(ctx, packID, "refreshing", "")
	defer func() {
		if err != nil {
			_ = s.markFailed(ctx, packID, err, start)
			s.emit(ctx, EventRefreshFailed, map[string]any{"pack": packID, "error": s.o.Scanner.Redact(err.Error()), "by": by})
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, m.Crawl.Budget.D())
	defer cancel()

	f := NewFetcher(FetchPolicy{
		AllowedHosts: m.Crawl.AllowedHosts, DenyPaths: m.Crawl.DenyPaths, RequireSignal: m.Crawl.RequireContentSignal, RPS: m.Crawl.RPS,
		MaxBytes: m.Crawl.MaxPageBytes, Timeout: m.Crawl.Timeout.D(), AllowLoopback: s.o.AllowLoopbackFetch,
	}, s.o.FetchTransport)
	packDir := filepath.Join(s.o.Dir, packID)
	pages, skipped, err := s.discover(ctx, m, f, packDir)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("listing sources: %w", err)
	}
	existing, err := s.existingPages(ctx, packID)
	if err != nil {
		return RefreshResult{}, err
	}

	results := make([]pageResult, len(pages))
	var wg sync.WaitGroup
	sem := make(chan struct{}, m.Crawl.Concurrency)
	chunkOpt := m.ChunkOptions()
	for i := range pages {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.loadPage(ctx, f, pages[i], existing[pages[i].URL], chunkOpt, opt.Force)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return RefreshResult{}, fmt.Errorf("refresh stopped: %w", err)
	}

	res = RefreshResult{PackID: packID, Pages: len(pages), Skipped: skipped, Reasons: map[string]int{}}
	for _, r := range results {
		switch r.status {
		case "failed":
			res.Failed++
			res.Reasons[r.reason]++
		case "skipped":
			res.Skipped++
			res.Reasons[r.reason]++
		}
	}
	if res.Failed > 0 && res.Failed*2 > len(pages) {
		return res, fmt.Errorf("%d of %d pages failed (%s); the previous version is kept", res.Failed, len(pages), topReason(res.Reasons))
	}
	if res.Failed == len(pages) {
		return res, errors.New("every page failed; the previous version is kept")
	}

	listed := map[string]bool{}
	for _, p := range pages {
		listed[p.URL] = true
	}
	var gone []string
	for u, e := range existing {
		if e.status == "active" && !listed[u] {
			gone = append(gone, u)
		}
	}
	sort.Strings(gone)
	for _, r := range results {
		switch r.status {
		case "new":
			res.Added++
		case "changed":
			res.Changed++
		case "unchanged":
			res.Unchanged++
		}
		res.Redactions += r.redact
		for _, c := range r.chunks {
			if c.quarantined {
				res.Quarantined++
			}
		}
	}
	res.Removed = len(gone)
	changes := res.Added + res.Changed + res.Removed
	firstRefresh := row.seq == 0

	// the version
	version, seq := row.version, row.seq
	if changes > 0 || firstRefresh {
		version, seq, err = s.nextVersion(ctx, packID, row.seq)
		if err != nil {
			return res, err
		}
		res.NewVersion = true
	}
	res.Version = version

	// digest (before the old page files are overwritten)
	var digest *Digest
	if res.NewVersion {
		digest = s.buildDigest(packID, version, row.version, results, gone, existing, packDir, chunkOpt)
		res.Digest = digest
	}
	// persist the normalized pages to disk (latest copy; offline source for diffs and the UI)
	pagesDir := filepath.Join(packDir, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		return res, err
	}
	for i := range results {
		r := &results[i]
		if r.status == "new" || r.status == "changed" {
			name := HashHex(r.page.URL)[:24] + ".md"
			if err := os.WriteFile(filepath.Join(pagesDir, name), []byte(r.text), 0o644); err != nil {
				return res, err
			}
		}
	}
	if digest != nil && s.o.Summarizer != nil && m.Refresh.Digest.Enabled && len(digest.Changes) > 0 {
		allowT := m.Visibility == VisibilityPublic && m.Refresh.Digest.DataClass == RouteTrainableOK
		sctx, c2 := context.WithTimeout(ctx, 60*time.Second)
		if sum, e := s.o.Summarizer.Summarize(sctx, *digest, allowT); e == nil {
			digest.Summary = s.o.Scanner.Redact(sum)
		} else {
			s.o.Logf("packs: digest summary for %s: %v", packID, e)
		}
		c2()
	}

	now := s.now().UnixMilli()
	var pageEvents []map[string]any
	err = s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		pageEvents = pageEvents[:0]
		if res.NewVersion {
			dj, _ := json.Marshal(digest)
			if _, err := tx.ExecContext(ctx, `INSERT INTO pack_versions(pack_id,seq,version,created_at,pages,chunks,added,changed,removed,skipped,failed,digest,summary) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				packID, seq, version, now, len(pages), 0, res.Added, res.Changed, res.Removed, res.Skipped, res.Failed, string(dj), digest.Summary); err != nil {
				return err
			}
		}
		for _, r := range results {
			name := HashHex(r.page.URL)[:24] + ".md"
			switch r.status {
			case "new":
				if _, err := tx.ExecContext(ctx, `INSERT INTO pack_pages(pack_id,url,title,content_hash,etag,last_modified,fetched_at,first_version,last_version,status,risk,raw_ref)
					VALUES(?,?,?,?,?,?,?,?,?,'active',?,?) ON CONFLICT(pack_id,url) DO UPDATE SET title=excluded.title,content_hash=excluded.content_hash,etag=excluded.etag,
					last_modified=excluded.last_modified,fetched_at=excluded.fetched_at,last_version=excluded.last_version,status='active',risk=excluded.risk,raw_ref=excluded.raw_ref`,
					packID, r.page.URL, r.title, r.hash, r.etag, r.lm, now, version, version, r.risk, name); err != nil {
					return err
				}
				var id int64
				if err := tx.QueryRowContext(ctx, `SELECT id FROM pack_pages WHERE pack_id=? AND url=?`, packID, r.page.URL).Scan(&id); err != nil {
					return err
				}
				if err := s.diffChunks(ctx, tx, packID, id, version, seq, r.chunks); err != nil {
					return err
				}
				pageEvents = append(pageEvents, map[string]any{"pack": packID, "version": version, "url": r.page.URL, "title": r.title, "status": "added", "hash": r.hash})
			case "changed":
				e := existing[r.page.URL]
				if _, err := tx.ExecContext(ctx, `UPDATE pack_pages SET title=?,content_hash=?,etag=?,last_modified=?,fetched_at=?,last_version=?,status='active',risk=?,raw_ref=? WHERE id=?`,
					r.title, r.hash, r.etag, r.lm, now, version, r.risk, name, e.id); err != nil {
					return err
				}
				if err := s.diffChunks(ctx, tx, packID, e.id, version, seq, r.chunks); err != nil {
					return err
				}
				pageEvents = append(pageEvents, map[string]any{"pack": packID, "version": version, "url": r.page.URL, "title": r.title, "status": "changed", "oldHash": e.hash, "hash": r.hash})
			case "unchanged":
				e := existing[r.page.URL]
				if _, err := tx.ExecContext(ctx, `UPDATE pack_pages SET fetched_at=?, etag=?, last_modified=?, status='active' WHERE id=?`, now, firstNonEmpty(r.etag, e.etag), firstNonEmpty(r.lm, e.lastModified), e.id); err != nil {
					return err
				}
			case "skipped", "failed":
				// a page we could not or may not fetch keeps its previous content; only its listing is unchanged
			}
		}
		for _, u := range gone {
			e := existing[u]
			if _, err := tx.ExecContext(ctx, `UPDATE pack_pages SET status='removed' WHERE id=?`, e.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pack_chunks SET version_removed=?, seq_removed=? WHERE page_id=? AND seq_removed IS NULL`, version, seq, e.id); err != nil {
				return err
			}
			pageEvents = append(pageEvents, map[string]any{"pack": packID, "version": version, "url": u, "status": "removed", "oldHash": e.hash})
		}
		if res.NewVersion {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_chunks WHERE pack_id=? AND seq_removed IS NULL`, packID).Scan(&n); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pack_versions SET chunks=? WHERE pack_id=? AND seq=?`, n, packID, seq); err != nil {
				return err
			}
			if err := s.prune(ctx, tx, packID, seq); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE packs SET version=?,version_seq=?,status='ready',last_refresh_at=?,last_success_at=?,last_error='',updated_at=? WHERE id=?`, version, seq, now, now, now, packID)
		return err
	})
	if err != nil {
		return res, err
	}
	for _, ev := range pageEvents {
		s.emit(ctx, EventPageChanged, ev)
	}
	s.emit(ctx, EventRefreshed, map[string]any{"pack": packID, "version": version, "newVersion": res.NewVersion, "pages": res.Pages, "added": res.Added, "changed": res.Changed,
		"removed": res.Removed, "unchanged": res.Unchanged, "skipped": res.Skipped, "failed": res.Failed, "quarantined": res.Quarantined, "by": by})
	if s.o.Live != nil && m.Live != nil {
		s.o.Live.Invalidate(ctx, m.Live.MCP) // a refreshed pack must not be shadowed by a day-old gateway cache entry
	}
	return res, nil
}

func topReason(r map[string]int) string {
	best, n := "", 0
	for k, v := range r {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

func (s *Store) setStatus(ctx context.Context, id, status, lastErr string) error {
	return s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE packs SET status=?, last_error=? WHERE id=?`, status, lastErr, id)
		return err
	})
}

func (s *Store) markFailed(ctx context.Context, id string, cause error, started time.Time) error {
	msg := s.o.Scanner.Redact(cause.Error())
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return s.o.DB.WriteTx(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE packs SET status='failed', last_error=?, last_refresh_at=? WHERE id=?`, msg, s.now().UnixMilli(), id)
		return err
	})
}

func (s *Store) nextVersion(ctx context.Context, packID string, curSeq int64) (string, int64, error) {
	day := s.now().Format("20060102")
	var n int
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM pack_versions WHERE pack_id=? AND version LIKE ?`, packID, day+".%").Scan(&n); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%s.%d", day, n+1), curSeq + 1, nil
}

func (s *Store) existingPages(ctx context.Context, packID string) (map[string]existingPage, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT id,url,content_hash,etag,last_modified,raw_ref,status,first_version FROM pack_pages WHERE pack_id=?`, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]existingPage{}
	for rows.Next() {
		var e existingPage
		var u string
		if err := rows.Scan(&e.id, &u, &e.hash, &e.etag, &e.lastModified, &e.rawRef, &e.status, &e.firstVer); err != nil {
			return nil, err
		}
		out[u] = e
	}
	return out, rows.Err()
}

// loadPage fetches or reads one page and prepares its chunks. It never returns an error: the outcome is in the status.
func (s *Store) loadPage(ctx context.Context, f *Fetcher, p Page, ex existingPage, co ChunkOptions, force bool) pageResult {
	r := pageResult{page: p}
	var raw, format, title string
	if p.Local != "" {
		b, err := os.ReadFile(p.Local)
		if err != nil {
			r.status, r.reason = "failed", "read error"
			return r
		}
		raw, format = string(b), p.Format
	} else {
		cond := Conditional{}
		if ex.id != 0 && !force && ex.status == "active" {
			cond = Conditional{ETag: ex.etag, LastModified: ex.lastModified}
		}
		resp, err := f.Get(ctx, p.FetchURL, cond)
		switch {
		case err == nil:
		case errors.Is(err, ErrRobots):
			r.status, r.reason = "skipped", "robots.txt disallows"
			return r
		case errors.Is(err, ErrNoSignal):
			r.status, r.reason = "skipped", "content signal not granted"
			return r
		case errors.Is(err, ErrDeniedPath):
			r.status, r.reason = "skipped", "deny_paths"
			return r
		case errors.Is(err, ErrHostNotAllowed):
			r.status, r.reason = "skipped", "host not allowed"
			return r
		default:
			r.status = "failed"
			var he *HTTPError
			switch {
			case errors.As(err, &he):
				r.reason = fmt.Sprintf("HTTP %d", he.Status)
			case errors.Is(err, ErrTooLarge):
				r.reason = "page too large"
			case errors.Is(err, ErrContentType):
				r.reason = "not a text document"
			case errors.Is(err, ErrBlockedAddr):
				r.reason = "address blocked"
			case errors.Is(err, ErrRedirect):
				r.reason = "redirect refused"
			default:
				r.reason = "network error"
			}
			return r
		}
		if resp.NotModified {
			r.status, r.etag, r.lm = "unchanged", ex.etag, ex.lastModified
			return r
		}
		raw, r.etag, r.lm = string(resp.Body), resp.ETag, resp.LastModified
		format = p.Format
		if format == "" {
			ct := strings.ToLower(resp.ContentType)
			switch {
			case strings.Contains(ct, "html"):
				format = "html"
			default:
				format = "markdown"
			}
		}
	}
	switch format {
	case "html":
		t, md := HTMLToMarkdown(raw)
		raw, title = md, t
	case "mdx":
		raw = StripMDX(raw)
	}
	text, _ := Normalize(raw)
	if strings.TrimSpace(strings.ReplaceAll(text, string(HiddenMark), "")) == "" {
		r.status, r.reason = "failed", "empty page"
		return r
	}
	r.hash = HashHex(text)
	if ex.id != 0 && ex.status == "active" && ex.hash == r.hash {
		r.status = "unchanged"
		return r
	}
	red := RedactSecrets(s.o.Scanner, text)
	r.redact = red.Count
	r.text = red.Text
	pageTitle, chunks := ChunkMarkdown(r.text, firstNonEmpty(title, pageTitleFromURL(p.URL)), co)
	r.title = strings.TrimSpace(pageTitle)
	if r.title == "" {
		r.title = pageTitleFromURL(p.URL)
	}
	if len(chunks) == 0 {
		r.status, r.reason = "failed", "no content after chunking"
		return r
	}
	for _, c := range chunks {
		sc := ScanChunk(c.Body)
		flags := sc.Flags
		if len(red.Rules) > 0 && strings.Contains(c.Body, "[REDACTED:") {
			flags = append(flags, "secret_redacted")
		}
		pc := preparedChunk{Chunk: c, risk: sc.Risk, flags: strings.Join(flags, ","), quarantined: sc.Quarantined}
		pc.Body = sc.Text
		pc.TokenEst = EstimateTokens(pc.Body)
		pc.Hash = HashHex(pc.HeadingPath, pc.Body)
		if sc.Risk > r.risk {
			r.risk = sc.Risk
		}
		r.chunks = append(r.chunks, pc)
	}
	if ex.id != 0 && ex.status == "active" {
		r.status = "changed"
	} else {
		r.status = "new"
	}
	return r
}

// diffChunks keeps chunks whose hash is unchanged (their rows, and any human release, survive), retires chunks that
// vanished (version_removed) and inserts new ones. Rows are never rewritten in place.
func (s *Store) diffChunks(ctx context.Context, tx *sql.Tx, packID string, pageID int64, version string, seq int64, chunks []preparedChunk) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, chunk_hash, ordinal FROM pack_chunks WHERE page_id=? AND seq_removed IS NULL ORDER BY id`, pageID)
	if err != nil {
		return err
	}
	type old struct {
		id      int64
		ordinal int
		used    bool
	}
	byHash := map[string][]*old{}
	var olds []*old
	for rows.Next() {
		var o old
		var h string
		if err := rows.Scan(&o.id, &h, &o.ordinal); err != nil {
			rows.Close()
			return err
		}
		oo := &o
		byHash[h] = append(byHash[h], oo)
		olds = append(olds, oo)
	}
	rows.Close()
	for _, c := range chunks {
		var match *old
		for _, o := range byHash[c.Hash] {
			if !o.used {
				match = o
				break
			}
		}
		if match != nil {
			match.used = true
			if match.ordinal != c.Ordinal {
				if _, err := tx.ExecContext(ctx, `UPDATE pack_chunks SET ordinal=? WHERE id=?`, c.Ordinal, match.id); err != nil {
					return err
				}
			}
			continue
		}
		q := 0
		if c.quarantined {
			q = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pack_chunks(pack_id,page_id,ordinal,heading_path,body,token_est,chunk_hash,version_added,seq_added,risk,flags,quarantined) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			packID, pageID, c.Ordinal, c.HeadingPath, c.Body, c.TokenEst, c.Hash, version, seq, c.risk, c.flags, q); err != nil {
			return err
		}
	}
	for _, o := range olds {
		if !o.used {
			if _, err := tx.ExecContext(ctx, `UPDATE pack_chunks SET version_removed=?, seq_removed=? WHERE id=?`, version, seq, o.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// prune keeps the two newest versions and any pinned version: a chunk that was retired at or before the oldest retained
// version and that no pinned version needs is deleted, then pages with nothing left.
func (s *Store) prune(ctx context.Context, tx *sql.Tx, packID string, curSeq int64) error {
	oldest := curSeq - 1
	if oldest < 1 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pack_chunks WHERE pack_id=? AND seq_removed IS NOT NULL AND seq_removed <= ?
		AND NOT EXISTS (SELECT 1 FROM pack_pins n JOIN pack_versions v ON v.pack_id=n.pack_id AND v.version=n.version
		                WHERE n.pack_id=pack_chunks.pack_id AND v.seq >= pack_chunks.seq_added AND v.seq < pack_chunks.seq_removed)`, packID, oldest); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM pack_pages WHERE pack_id=? AND status='removed' AND NOT EXISTS (SELECT 1 FROM pack_chunks WHERE page_id=pack_pages.id)`, packID)
	return err
}

// buildDigest assembles the deterministic changed-pages digest. Old page text is read from disk before it is replaced.
func (s *Store) buildDigest(packID, version, prev string, results []pageResult, gone []string, existing map[string]existingPage, packDir string, co ChunkOptions) *Digest {
	d := &Digest{PackID: packID, Version: version, Previous: prev, Changes: []PageChange{}}
	read := func(e existingPage) string {
		if e.rawRef == "" {
			return ""
		}
		b, _ := os.ReadFile(filepath.Join(packDir, "pages", e.rawRef))
		return string(b)
	}
	for _, r := range results {
		switch r.status {
		case "new":
			d.Changes = append(d.Changes, PageChange{URL: r.page.URL, Title: r.title, Status: "added", LinesAdded: lineCount(r.text), Headings: headingsOf(r.chunks, nil)})
		case "changed":
			old := read(existing[r.page.URL])
			add, rem := lineDiff(old, r.text)
			d.Changes = append(d.Changes, PageChange{URL: r.page.URL, Title: r.title, Status: "changed", LinesAdded: add, LinesRemoved: rem, Headings: changedHeadings(old, r, co)})
		}
	}
	for _, u := range gone {
		e := existing[u]
		d.Changes = append(d.Changes, PageChange{URL: u, Title: pageTitleFromURL(u), Status: "removed", LinesRemoved: lineCount(read(e))})
	}
	sort.Slice(d.Changes, func(i, j int) bool { return d.Changes[i].URL < d.Changes[j].URL })
	return d
}

func lineCount(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// lineDiff counts lines of b not present in a and the reverse, as multisets (order-insensitive, good enough for a digest).
func lineDiff(a, b string) (added, removed int) {
	count := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			count[t]++
		}
	}
	for _, l := range strings.Split(b, "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if count[t] > 0 {
			count[t]--
		} else {
			added++
		}
	}
	for _, n := range count {
		removed += n
	}
	return added, removed
}

func headingsOf(chunks []preparedChunk, _ []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range chunks {
		if !seen[c.HeadingPath] && len(out) < 5 {
			seen[c.HeadingPath] = true
			out = append(out, c.HeadingPath)
		}
	}
	return out
}

// changedHeadings lists the headings whose body text differs between the old and the new page.
func changedHeadings(old string, r pageResult, co ChunkOptions) []string {
	_, oldChunks := ChunkMarkdown(old, r.title, co)
	oldHash := map[string]bool{}
	for _, c := range oldChunks {
		b, _ := StripHidden(c.Body)
		oldHash[c.HeadingPath+"\x00"+HashHex(b)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range r.chunks {
		if !oldHash[c.HeadingPath+"\x00"+HashHex(c.Body)] && !seen[c.HeadingPath] && len(out) < 5 {
			seen[c.HeadingPath] = true
			out = append(out, c.HeadingPath)
		}
	}
	return out
}

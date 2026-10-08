package integrations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SyncOpts controls one Sync call.
type SyncOpts struct {
	// Force ignores the failure backoff (a manual refresh). A vendor rate-limit window is still honored.
	Force bool
	// Full walks every page ignoring the watermark and marks items missing upstream as gone (docs/integrations.md section 6).
	Full bool
}

var issueRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.#-]{0,39}$`)

var levelEnum = map[string]bool{"fatal": true, "error": true, "warning": true, "info": true, "debug": true}
var severityEnum = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}
var priorityEnum = map[string]bool{"urgent": true, "high": true, "medium": true, "low": true, "none": true}

// buildRecord sanitizes one raw item into a storable record. Every free-text field passes through the sanitizer: control and
// invisible characters removed, secrets redacted, literal untrusted tags neutralized, length limited. Fields that feed rules or
// task headers are additionally constrained to known values.
func (s *Service) buildRecord(ctx context.Context, c Connection, raw RawItem, detail string) (ItemRecord, []string) {
	title := s.san.Line(ctx, raw.Title, MaxTitleRunes)
	body := raw.Body
	if detail != "" {
		if body != "" {
			body += "\n\n"
		}
		body += detail
	}
	bc := s.san.Text(ctx, body, MaxBodyBytes)
	risk, flags := bc.Risk, append([]string(nil), bc.Flags...)
	if title.Risk > risk {
		risk = title.Risk
	}
	flags = unionStrings(flags, title.Flags)
	redactions := bc.Redactions + title.Redactions
	rules := unionStrings(append([]string(nil), bc.Rules...), title.Rules)

	ref := raw.IssueRef
	if !issueRefRe.MatchString(ref) {
		ref = strings.ToUpper(c.Kind[:3]) + "-" + shortID(hashHex(raw.ExternalID))
	}
	labels := []string{}
	for _, l := range raw.Labels {
		if len(labels) >= MaxLabels {
			break
		}
		lc := s.san.Line(ctx, l, MaxLabelRunes)
		if lc.Text != "" {
			labels = append(labels, lc.Text)
		}
	}
	prio := strings.ToLower(strings.TrimSpace(raw.Priority))
	if c.Kind == KindLinear && !priorityEnum[prio] {
		prio = "none"
	}
	if c.Kind == KindSentry && !levelEnum[prio] {
		prio = ""
	}
	if c.Kind == KindAikido && !severityEnum[prio] {
		prio = ""
	}
	meta := map[string]any{}
	for k, v := range raw.Meta {
		switch x := v.(type) {
		case string:
			ml := s.san.Line(ctx, x, 200)
			switch k { // enum-constrained facts: anything else is dropped, so item text cannot smuggle values into rules
			case "level":
				if !levelEnum[strings.ToLower(x)] {
					continue
				}
			case "severity":
				if !severityEnum[strings.ToLower(x)] {
					continue
				}
			}
			meta[k] = ml.Text
		case int64, int, float64:
			meta[k] = x
		}
	}
	excerpt, _ := truncateRunes(strings.Join(strings.Fields(bc.Text), " "), MaxExcerptRunes)
	it := Item{
		IntegrationID: c.ID, Source: c.Kind, ProjectID: c.ProjectID, ExternalID: clampText(raw.ExternalID, 200), Kind: raw.Kind, IssueRef: ref, URL: validHTTPURL(raw.URL),
		Title: title.Text, Excerpt: excerpt, State: s.san.Line(ctx, raw.State, 40).Text, Labels: labels, Assignee: s.san.Line(ctx, raw.Assignee, 80).Text, Priority: prio,
		ParentExternalID: clampText(s.san.Line(ctx, raw.Parent, 200).Text, 200), Meta: meta, ExternalCreated: raw.Created, ExternalUpdated: raw.Updated, Etag: clampText(raw.Etag, 80),
		Risk: risk, RiskFlags: flags, Redactions: redactions, Truncated: bc.Truncated || title.Truncated, Untrusted: true,
	}
	return ItemRecord{Item: it, Body: bc.Text}, rules
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			a = append(a, x)
			seen[x] = true
		}
	}
	return a
}

func sortedDesc(items []RawItem) bool {
	var last int64 = -1
	for _, it := range items {
		if it.Updated <= 0 {
			continue
		}
		if last >= 0 && it.Updated > last {
			return false
		}
		last = it.Updated
	}
	return true
}

func (s *Service) backoff(n int) time.Duration {
	d := s.o.BaseBackoff
	for i := 1; i < n && d < s.o.MaxBackoff; i++ {
		d *= 2
	}
	if d > s.o.MaxBackoff {
		d = s.o.MaxBackoff
	}
	return time.Duration(float64(d) * s.o.Jitter())
}

// client connects to the server and resolves the adapter's tools. It returns a Failure on any problem.
func (s *Service) client(ctx context.Context, c Connection, ad Adapter, write bool) (*Client, *Failure) {
	if s.o.Callers == nil {
		return nil, &Failure{Code: CodeOther, Message: "no MCP gateway is configured"}
	}
	caller, err := s.o.Callers.Caller(ctx, c, write)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return nil, &Failure{Code: CodeUnauthorized, Message: "No token is stored for " + vendor(c.Kind) + ". Paste one under Connections > " + vendor(c.Kind) + ".", Cause: err}
		}
		return nil, Classify(c.Kind, err, "")
	}
	tctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	offered, err := caller.Tools(tctx)
	if err != nil {
		return nil, Classify(c.Kind, err, "")
	}
	resolved, missing, _ := resolveTools(ad.Needs(), offered)
	if len(missing) > 0 {
		return nil, &Failure{Code: CodeToolMissing, Cause: ErrToolMissing,
			Message: fmt.Sprintf("%s's MCP server does not offer the tool %s that Drydock uses to read items. The server may have changed or the wrong server is configured; sync is paused until this is fixed.", vendor(c.Kind), strings.Join(missing, ", "))}
	}
	return &Client{Caller: timeoutCaller{caller, s.o.CallTimeout}, Conn: c, Tools: resolved}, nil
}

// timeoutCaller bounds every call.
type timeoutCaller struct {
	Caller
	d time.Duration
}

func (t timeoutCaller) Call(ctx context.Context, tool string, args any) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Caller.Call(cctx, tool, args)
}

func (t timeoutCaller) Tools(ctx context.Context) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.Caller.Tools(cctx)
}

// Sync polls one connection: it walks the vendor's listing newest first, upserts changed items page by page (cursor and rows in
// one transaction), stops at the watermark, then evaluates the connection's rules.
func (s *Service) Sync(ctx context.Context, id string, o SyncOpts) (SyncResult, error) {
	lk := s.lock("sync:" + id)
	if !lk.TryLock() {
		return SyncResult{IntegrationID: id}, ErrSyncBusy
	}
	defer lk.Unlock()
	conn, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return SyncResult{IntegrationID: id}, err
	}
	res := SyncResult{IntegrationID: id}
	if !conn.Enabled {
		res.Skipped = "disabled"
		return res, nil
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return res, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	cur, err := s.st.GetCursor(ctx, id, defaultScope)
	if err != nil {
		return res, err
	}
	now := s.nowMS()
	if cur.NextAttemptAt > now && (!o.Force || cur.LastErrorCode == CodeRateLimited) {
		wait := time.Duration(cur.NextAttemptAt-now) * time.Millisecond
		res.Skipped = fmt.Sprintf("waiting %s before the next attempt", wait.Round(time.Second))
		if cur.LastErrorCode == CodeRateLimited {
			return res, fmt.Errorf("%w: %s", ErrRateLimited, cur.LastError)
		}
		return res, nil
	}
	s.emit(ctx, EventSyncStarted, conn.ProjectID, "", map[string]any{"integrationId": id, "kind": conn.Kind, "full": o.Full, "resuming": cur.After != ""})
	cl, fail := s.client(ctx, conn, ad, false)
	if fail != nil {
		return res, s.fail(ctx, conn, cur, fail)
	}

	if o.Full && cur.After == "" {
		cur.Watermark, cur.Pending = 0, 0
	}
	sweepFull := ad.FullSweep() || o.Full
	if cur.After == "" && cur.SweepStart == 0 {
		cur.SweepStart, cur.SweepSeen = now, 0
	}
	bodyBudget := s.o.MaxBodyFetch
	complete := false
	for page := 0; page < s.o.MaxPages; page++ {
		pageAfter := cur.After
		pg, perr := ad.Page(ctx, cl, pageAfter, s.o.PageSize)
		if perr != nil {
			var f *Failure
			if !errors.As(perr, &f) {
				f = Classify(conn.Kind, perr, "")
			}
			return res, s.fail(ctx, conn, cur, f)
		}
		res.Pages++
		sorted := sortedDesc(pg.Items)
		var recs []ItemRecord
		var touch []string
		stop, budgetHit := false, false
		for _, raw := range pg.Items {
			if raw.ExternalID == "" {
				continue
			}
			if sorted && !sweepFull && cur.Watermark > 0 && raw.Updated > 0 && raw.Updated < cur.Watermark {
				stop = true
				continue
			}
			if raw.Updated > cur.Pending {
				cur.Pending = raw.Updated
			}
			res.Seen++
			cur.SweepSeen++
			detail := ""
			if raw.NeedsBody {
				existing, gerr := s.st.GetItemByExternal(ctx, conn.ID, raw.ExternalID)
				if gerr == nil && existing.Etag == raw.Etag && raw.Etag != "" {
					touch = append(touch, raw.ExternalID) // unchanged since last fetch: skip the body call (the If-Modified analogue)
					res.Unchanged++
					continue
				}
				if bodyBudget <= 0 {
					budgetHit = true
					res.Seen--
					cur.SweepSeen--
					break
				}
				bodyBudget--
				text, has, derr := ad.Detail(ctx, cl, raw.ExternalID)
				if derr != nil {
					var f *Failure
					if !errors.As(derr, &f) {
						f = Classify(conn.Kind, derr, "")
					}
					if f.Code == CodeRateLimited || f.Code == CodeUnauthorized || f.Code == CodeUnreachable || f.Code == CodeTimeout {
						return res, s.fail(ctx, conn, cur, f)
					}
					has = false // a page whose body cannot be read is stored with its summary only
				}
				if has {
					detail = text
				}
			}
			rec, _ := s.buildRecord(ctx, conn, raw, detail)
			recs = append(recs, rec)
		}
		next := pg.Next
		done := stop || next == ""
		if budgetHit {
			next = pageAfter // re-fetch this page next run; items already stored are skipped by their etag
			done = false
		}
		cur.After = next
		cur.LastAttemptAt = s.nowMS()
		if done {
			cur.After = ""
			if cur.Pending > 0 {
				if w := cur.Pending - s.o.Overlap.Milliseconds(); w > cur.Watermark {
					cur.Watermark = w
				}
			}
			cur.Pending = 0
			cur.LastSuccessAt = s.nowMS()
			cur.LastError, cur.LastErrorCode, cur.ConsecutiveFailures, cur.NextAttemptAt = "", "", 0, 0
			cur.ItemsLastSync = cur.SweepSeen
		}
		pr, cerr := s.st.CommitPage(ctx, PageCommit{Items: recs, Cursor: cur, Touch: touch, IntegrationID: conn.ID})
		if cerr != nil {
			return res, cerr
		}
		res.New += pr.New
		res.Changed += pr.Changed
		res.Unchanged += pr.Unchanged
		for i, it := range pr.Touched {
			s.emit(ctx, EventItemUpserted, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "itemId": it.ID, "externalId": it.ExternalID, "kind": it.Kind, "hash": it.ContentHash, "new": pr.Outcomes[i] == Inserted})
			if it.Redactions > 0 {
				s.emit(ctx, EventItemSecretRedacted, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "itemId": it.ID, "findings": it.Redactions})
			}
			if it.Risk >= s.o.ThreatThreshold {
				s.emit(ctx, EventItemRiskFlagged, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "itemId": it.ID, "risk": it.Risk, "flags": it.RiskFlags})
			}
		}
		if done {
			complete = true
			break
		}
		if budgetHit {
			break
		}
	}
	res.Complete = complete
	if complete {
		if sweepFull {
			n, err := s.st.MarkGoneNotSeenSince(ctx, conn.ID, cur.SweepStart)
			if err != nil {
				return res, err
			}
			res.Gone = n
		}
		cur.SweepStart, cur.SweepSeen = 0, 0
		if err := s.st.PutCursor(ctx, cur); err != nil {
			return res, err
		}
		if conn.Status != StatusOK {
			_ = s.st.SetStatus(ctx, conn.ID, StatusOK, "")
		}
		res.RuleResults = s.EvaluateRules(ctx, conn)
	}
	s.emit(ctx, EventSyncCompleted, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "pages": res.Pages, "seen": res.Seen, "new": res.New, "changed": res.Changed, "gone": res.Gone, "complete": complete})
	return res, nil
}

// fail records a failed attempt: a plain-language message, a backoff, and the connection status; the cursor keeps its resume
// position so the next attempt continues where this one stopped.
func (s *Service) fail(ctx context.Context, conn Connection, cur Cursor, f *Failure) error {
	now := s.nowMS()
	cur.LastAttemptAt = now
	cur.LastError = s.scrub(f.Message)
	cur.LastErrorCode = f.Code
	cur.ConsecutiveFailures++
	status, detail := conn.Status, s.scrub(f.Message)
	typ := EventSyncFailed
	switch f.Code {
	case CodeRateLimited:
		wait := s.backoff(cur.ConsecutiveFailures)
		if f.RetryAfter > wait {
			wait = f.RetryAfter
		}
		cur.NextAttemptAt = now + wait.Milliseconds()
		s.emit(ctx, EventRateLimited, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "waitMs": wait.Milliseconds(), "until": cur.NextAttemptAt})
	case CodeUnauthorized:
		cur.NextAttemptAt = 0 // paused, not failing: the scheduler skips unauthenticated connections until the token is replaced
		status = StatusUnauthenticated
		s.emit(ctx, EventAuthExpired, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind})
	case CodeSchemaDrift, CodeToolMissing:
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
		status = StatusDegraded
		s.emit(ctx, EventSchemaDrift, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "code": f.Code})
	case CodeUnreachable, CodeTimeout:
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
		status = StatusUnreachable
	default:
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
	}
	if err := s.st.PutCursor(ctx, cur); err != nil {
		return err
	}
	_ = s.st.SetStatus(ctx, conn.ID, status, detail)
	s.emit(ctx, typ, conn.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "code": f.Code, "consecutiveFailures": cur.ConsecutiveFailures, "message": cur.LastError})
	return f
}

// Tick runs every sync that is due: enabled connections whose interval elapsed, whose backoff passed, that are not paused for
// authentication, plus interrupted sweeps that should resume. It returns the ids it synced.
func (s *Service) Tick(ctx context.Context) []string {
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		s.o.Logf("integrations: tick: %v", err)
		return nil
	}
	now := s.nowMS()
	var due []Connection
	for _, c := range conns {
		if !c.Enabled || c.Status == StatusUnauthenticated || c.Status == StatusDisconnected {
			continue
		}
		cur, err := s.st.GetCursor(ctx, c.ID, defaultScope)
		if err != nil || cur.NextAttemptAt > now {
			continue
		}
		iv := pollInterval(c).Milliseconds()
		iv += int64(float64(iv) * 0.1 * float64(hashByte(c.ID)) / 255) // up to 10% deterministic jitter per connection
		if cur.After == "" && cur.LastAttemptAt > 0 && now-cur.LastAttemptAt < iv {
			continue
		}
		due = append(due, c)
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	var ran []string
	for _, c := range due {
		if ctx.Err() != nil {
			break
		}
		if _, err := s.Sync(ctx, c.ID, SyncOpts{}); err != nil && !errors.Is(err, ErrSyncBusy) {
			var f *Failure
			if !errors.As(err, &f) {
				s.o.Logf("integrations: sync %s: %v", c.ID, err)
			}
		}
		ran = append(ran, c.ID)
	}
	return ran
}

func hashByte(s string) byte {
	h := hashHex(s)
	var b byte
	for i := 0; i < 2 && i < len(h); i++ {
		b = b*16 + hexVal(h[i])
	}
	return b
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return 0
}

// Run calls Tick every interval until ctx ends (the core extension starts it in the background).
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

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

var issueRefRe = regexp.MustCompile(`^(#[0-9]{1,9}|[A-Za-z0-9][A-Za-z0-9_.#-]{0,39})$`)

var levelEnum = map[string]bool{"fatal": true, "error": true, "warning": true, "info": true, "debug": true}
var severityEnum = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}
var priorityEnum = map[string]bool{"urgent": true, "high": true, "medium": true, "low": true, "none": true}

// buildRecord sanitizes one raw item into a storable record. Every free-text field passes through the sanitizer: control and
// invisible characters removed, secrets redacted, literal untrusted tags neutralized, length limited. Fields that feed rules or
// task headers are additionally constrained to known values.
func (s *Service) buildRecord(ctx context.Context, c Connection, a Assignment, raw RawItem, detail string) (ItemRecord, []string) {
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
		IntegrationID: c.ID, AssignmentID: a.ID, Source: c.Kind, ProjectID: a.ProjectID, ExternalID: clampText(raw.ExternalID, 200), Kind: raw.Kind, IssueRef: ref, URL: validHTTPURL(raw.URL),
		Title: title.Text, Excerpt: excerpt, State: s.san.Line(ctx, raw.State, 40).Text, Labels: labels, Assignee: s.san.Line(ctx, raw.Assignee, 80).Text, Priority: prio,
		ParentExternalID: clampText(s.san.Line(ctx, raw.Parent, 200).Text, 200), Meta: meta, ExternalCreated: raw.Created, ExternalUpdated: raw.Updated, Etag: clampText(raw.Etag, 80),
		Risk: risk, RiskFlags: flags, Redactions: redactions, Truncated: bc.Truncated || title.Truncated, Untrusted: true, Gone: raw.Closed,
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

// Sync polls a connection: for every enabled assignment it walks the vendor's listing (newest first where the API sorts),
// upserts changed items page by page (cursor and rows in one transaction), stops at the watermark, then evaluates the rules of
// that project. A connection without an assignment has nothing to sync.
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
	asgs, err := s.st.ListAssignments(ctx, id)
	if err != nil {
		return SyncResult{IntegrationID: id}, err
	}
	var due []Assignment
	for _, a := range asgs {
		if a.Enabled {
			due = append(due, a)
		}
	}
	return s.syncAssignments(ctx, conn, due, o)
}

// SyncAssignment polls one assignment.
func (s *Service) SyncAssignment(ctx context.Context, assignmentID string, o SyncOpts) (SyncResult, error) {
	a, err := s.st.GetAssignment(ctx, assignmentID)
	if err != nil {
		return SyncResult{}, err
	}
	lk := s.lock("sync:" + a.IntegrationID)
	if !lk.TryLock() {
		return SyncResult{IntegrationID: a.IntegrationID}, ErrSyncBusy
	}
	defer lk.Unlock()
	conn, err := s.st.GetConnection(ctx, a.IntegrationID)
	if err != nil {
		return SyncResult{IntegrationID: a.IntegrationID}, err
	}
	return s.syncAssignments(ctx, conn, []Assignment{a}, o)
}

// syncAssignments runs the given assignments of a connection (the caller holds the connection's sync lock). The result adds
// them up; the first error is returned after every assignment had its turn.
func (s *Service) syncAssignments(ctx context.Context, conn Connection, asgs []Assignment, o SyncOpts) (SyncResult, error) {
	res := SyncResult{IntegrationID: conn.ID, Complete: true}
	if !conn.Enabled {
		res.Skipped, res.Complete = "disabled", false
		return res, nil
	}
	if len(asgs) == 0 {
		res.Skipped, res.Complete = "not assigned to a project", false
		return res, nil
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return res, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	var firstErr error
	var skipped []string
	for _, a := range asgs {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		r, err := s.syncOne(ctx, conn, ad, a, o)
		res.Pages += r.Pages
		res.Seen += r.Seen
		res.New += r.New
		res.Changed += r.Changed
		res.Unchanged += r.Unchanged
		res.Gone += r.Gone
		res.RuleResults = append(res.RuleResults, r.RuleResults...)
		res.Complete = res.Complete && r.Complete
		if r.Skipped != "" {
			skipped = append(skipped, r.Skipped)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if conn2, gerr := s.st.GetConnection(ctx, conn.ID); gerr == nil {
			conn = conn2 // a failure may have changed its status
		}
	}
	res.Skipped = strings.Join(skipped, "; ")
	return res, firstErr
}

// syncOne polls one assignment.
func (s *Service) syncOne(ctx context.Context, conn Connection, ad Adapter, asg Assignment, o SyncOpts) (SyncResult, error) {
	res := SyncResult{IntegrationID: conn.ID}
	cur, err := s.st.GetCursor(ctx, conn.ID, asg.ID)
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
	s.emit(ctx, EventSyncStarted, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": asg.ID, "kind": conn.Kind, "full": o.Full, "resuming": cur.After != ""})
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return res, s.fail(ctx, conn, asg, cur, fail)
	}

	if rs, ok := ad.(scopeResolver); ok {
		rctx, rcancel := context.WithTimeout(ctx, s.o.CallTimeout)
		cfg, changed, rerr := rs.ResolveScope(rctx, api, asg.Config)
		rcancel()
		if rerr != nil {
			var f *Failure
			if !errors.As(rerr, &f) {
				f = Classify(conn.Kind, rerr, "")
			}
			return res, s.fail(ctx, conn, asg, cur, f)
		}
		if changed {
			asg.Config = cfg
			// stored once, so later syncs need no lookup; a concurrent edit by a person wins (this run still uses cfg)
			if upd, uerr := s.st.UpdateAssignment(ctx, asg, asg.Version, false); uerr == nil {
				asg = upd
			}
		}
	}
	if o.Full && cur.After == "" {
		cur.Watermark, cur.Pending = 0, 0
	}
	sweepFull := ad.FullSweep() || o.Full
	if cur.After == "" && cur.SweepStart == 0 {
		cur.SweepStart, cur.SweepSeen = now, 0
	}
	since := int64(0)
	if !sweepFull && cur.Watermark > 0 {
		since = cur.Watermark
	}
	bodyBudget := s.o.MaxBodyFetch
	complete := false
	for page := 0; page < s.o.MaxPages; page++ {
		snap := cur // what was committed before this page: a failure persists this, never half a page's progress
		pageAfter := cur.After
		pctx, pcancel := context.WithTimeout(ctx, s.o.CallTimeout)
		pg, perr := ad.Page(pctx, api, PageQuery{Config: asg.Config, After: pageAfter, Limit: s.o.PageSize, Since: since})
		pcancel()
		if perr != nil {
			var f *Failure
			if !errors.As(perr, &f) {
				f = Classify(conn.Kind, perr, "")
			}
			return res, s.fail(ctx, conn, asg, snap, f)
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
				existing, gerr := s.st.GetItemByExternal(ctx, conn.ID, asg.ID, raw.ExternalID)
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
				dctx, dcancel := context.WithTimeout(ctx, s.o.CallTimeout)
				text, has, derr := ad.Detail(dctx, api, asg.Config, raw.ExternalID)
				dcancel()
				if derr != nil {
					var f *Failure
					if !errors.As(derr, &f) {
						f = Classify(conn.Kind, derr, "")
					}
					if f.Code == CodeRateLimited || f.Code == CodeUnauthorized || f.Code == CodeUnreachable || f.Code == CodeTimeout {
						return res, s.fail(ctx, conn, asg, snap, f)
					}
					has = false // a page whose body cannot be read is stored with its summary only
				}
				if has {
					detail = text
				}
			}
			rec, _ := s.buildRecord(ctx, conn, asg, raw, detail)
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
		pr, cerr := s.st.CommitPage(ctx, PageCommit{Items: recs, Cursor: cur, Touch: touch, IntegrationID: conn.ID, AssignmentID: asg.ID})
		if cerr != nil {
			return res, cerr
		}
		res.New += pr.New
		res.Changed += pr.Changed
		res.Unchanged += pr.Unchanged
		res.Gone += pr.Closed
		for i, it := range pr.Touched {
			s.emit(ctx, EventItemUpserted, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": asg.ID, "itemId": it.ID, "externalId": it.ExternalID, "kind": it.Kind, "hash": it.ContentHash, "new": pr.Outcomes[i] == Inserted})
			if it.Redactions > 0 {
				s.emit(ctx, EventItemSecretRedacted, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "itemId": it.ID, "findings": it.Redactions})
			}
			if it.Risk >= s.o.ThreatThreshold {
				s.emit(ctx, EventItemRiskFlagged, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "itemId": it.ID, "risk": it.Risk, "flags": it.RiskFlags})
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
			n, err := s.st.MarkGoneNotSeenSince(ctx, conn.ID, asg.ID, cur.SweepStart)
			if err != nil {
				return res, err
			}
			res.Gone += n
		}
		cur.SweepStart, cur.SweepSeen = 0, 0
		if err := s.st.PutCursor(ctx, cur); err != nil {
			return res, err
		}
		s.clearProvenStatus(ctx, conn, asg)
		res.RuleResults = s.EvaluateRules(ctx, conn, asg)
	}
	s.emit(ctx, EventSyncCompleted, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": asg.ID, "kind": conn.Kind, "pages": res.Pages, "seen": res.Seen, "new": res.New, "changed": res.Changed, "gone": res.Gone, "complete": complete})
	return res, nil
}

// fail records a failed attempt: a plain-language message, a backoff, and the connection status; the cursor keeps its resume
// position so the next attempt continues where this one stopped.
func (s *Service) fail(ctx context.Context, conn Connection, asg Assignment, cur Cursor, f *Failure) error {
	if ctx.Err() != nil {
		return ctx.Err() // the app is shutting down: that is not the vendor's failure and earns no backoff
	}
	now := s.nowMS()
	cur.LastAttemptAt = now
	f = &Failure{Code: f.Code, Message: s.scrub(f.Message), RetryAfter: f.RetryAfter, Cause: f.Cause} // the caller sees the scrubbed text too
	cur.LastError = f.Message
	cur.LastErrorCode = f.Code
	cur.ConsecutiveFailures++
	// The connection's status is shared by every project it is assigned to, so only a failure of the connection itself sets it:
	// the credential refused, the vendor unreachable, the API's shape changed. A failure of this assignment's scope (forbidden,
	// not found, any other refusal) and a rate limit are recorded on this assignment's cursor only.
	status := ""
	typ := EventSyncFailed
	switch f.Code {
	case CodeRateLimited:
		wait := s.backoff(cur.ConsecutiveFailures)
		if f.RetryAfter > wait {
			wait = f.RetryAfter
		}
		cur.NextAttemptAt = now + wait.Milliseconds()
		s.emit(ctx, EventRateLimited, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "waitMs": wait.Milliseconds(), "until": cur.NextAttemptAt})
	case CodeUnauthorized:
		cur.NextAttemptAt = 0 // paused, not failing: the scheduler skips unauthenticated connections until the token is replaced
		status = StatusUnauthenticated
		s.emit(ctx, EventAuthExpired, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind})
	case CodeSchemaDrift, CodeToolMissing:
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
		status = StatusDegraded
		s.emit(ctx, EventSchemaDrift, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "code": f.Code})
	case CodeUnreachable, CodeTimeout:
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
		status = StatusUnreachable
	default:
		// CodeForbidden (the credential works but may not read this scope), CodeNotFound (the scope is not there) and the rest:
		// only this assignment backs off; the scheduler goes on syncing the connection's other projects
		cur.NextAttemptAt = now + s.backoff(cur.ConsecutiveFailures).Milliseconds()
	}
	if err := s.st.PutCursor(ctx, cur); err != nil {
		return err
	}
	if status != "" {
		_ = s.st.SetStatus(ctx, conn.ID, status, f.Message)
	}
	s.emit(ctx, typ, asg.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": asg.ID, "kind": conn.Kind, "code": f.Code, "consecutiveFailures": cur.ConsecutiveFailures, "message": cur.LastError})
	return f
}

// clearProvenStatus is called after an assignment synced completely. That proves what any successful call proves: the
// credential is accepted and the vendor answers, so an unauthenticated, unreachable or untested connection is OK again. It
// does not prove that another assignment's listing parses, so a degraded connection (the API's shape) becomes OK only when no
// other assignment's cursor still records schema drift. A success never clears another assignment's own failure: that stays
// on its cursor until that assignment syncs.
func (s *Service) clearProvenStatus(ctx context.Context, conn Connection, asg Assignment) {
	now, err := s.st.GetConnection(ctx, conn.ID) // an earlier assignment of this run may have changed it
	if err != nil {
		return
	}
	switch now.Status {
	case StatusOK, StatusDisconnected:
		return
	case StatusDegraded:
		asgs, err := s.st.ListAssignments(ctx, conn.ID)
		if err != nil {
			return
		}
		for _, a := range asgs {
			if a.ID == asg.ID || !a.Enabled {
				continue
			}
			c, err := s.st.GetCursor(ctx, conn.ID, a.ID)
			if err != nil || c.LastErrorCode == CodeSchemaDrift || c.LastErrorCode == CodeToolMissing {
				return
			}
		}
	}
	_ = s.st.SetStatus(ctx, conn.ID, StatusOK, "")
}

// Tick runs every sync that is due: the enabled assignments of enabled connections whose interval elapsed, whose backoff
// passed and that are not paused for authentication, plus interrupted sweeps that should resume. It returns the ids of the
// connections it synced.
func (s *Service) Tick(ctx context.Context) []string {
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		s.o.Logf("integrations: tick: %v", err)
		return nil
	}
	now := s.nowMS()
	sort.Slice(conns, func(i, j int) bool { return conns[i].ID < conns[j].ID })
	var ran []string
	for _, c := range conns {
		if ctx.Err() != nil {
			break
		}
		if !c.Enabled || c.Status == StatusUnauthenticated || c.Status == StatusDisconnected {
			continue
		}
		asgs, err := s.st.ListAssignments(ctx, c.ID)
		if err != nil {
			continue
		}
		var due []Assignment
		for _, a := range asgs {
			if !a.Enabled {
				continue
			}
			cur, err := s.st.GetCursor(ctx, c.ID, a.ID)
			if err != nil || cur.NextAttemptAt > now {
				continue
			}
			iv := pollInterval(c).Milliseconds()
			iv += int64(float64(iv) * 0.1 * float64(hashByte(a.ID)) / 255) // up to 10% deterministic jitter per assignment
			if cur.After == "" && cur.LastAttemptAt > 0 && now-cur.LastAttemptAt < iv {
				continue
			}
			due = append(due, a)
		}
		if len(due) == 0 {
			continue
		}
		lk := s.lock("sync:" + c.ID)
		if !lk.TryLock() {
			continue
		}
		if _, err := s.syncAssignments(ctx, c, due, SyncOpts{}); err != nil {
			var f *Failure
			if !errors.As(err, &f) {
				s.o.Logf("integrations: sync %s: %v", c.ID, err)
			}
		}
		lk.Unlock()
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

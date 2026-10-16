package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

// Check against your account (decision P59). Every vendor shape Drydock knows was written from documentation and checked only
// against the protocol fixtures; this is the run against the person's real account, from the app, with the credential Drydock
// already holds for the connection (never asked for again, never shown). It is the same read-only check the live tests make
// (live_test.go calls Verify), in a handful of calls:
//
//  1. the credential is stored (no call);
//  2. who it is: the adapter's Identity call (the API names the account and the workspace);
//  3. the choices of the first setting that has them (Linear teams, Notion data sources, Sentry organizations, Aikido
//     repositories, GitHub repositories);
//  4. one page (at most 25 items) of each of the first two enabled project assignments, or, when the connection is not
//     assigned yet, of everything the credential sees (Sentry and GitHub: the first organization or repository listed); every
//     item is checked for what Drydock needs (an id, a title, a reference, an update time);
//  5. the full text of the first item, where the vendor keeps it apart (Notion's page body, Sentry's latest event).
//
// Nothing is written to the vendor, nothing read is stored (no item, no cursor, no identity): the report carries counts, the
// calls made with their HTTP status codes and what did not match, never item text, names or the credential. Its outcome
// (time, ok, summary, counts) is kept per connection, so the card can say when it was last verified, and published as
// integration.verified.

// EventVerified is published after a check against the account (counts only).
const EventVerified = "integration.verified"

// ErrVerifyBusy is a second check of the same connection while one runs.
var ErrVerifyBusy = errors.New("integrations: a check of this connection is already running")

const (
	verifyPageSize  = 25
	verifyMaxScopes = 2
	verifyMaxCalls  = 20 // the trace keeps at most this many calls (a check makes about six)
)

// VerifyCall is one HTTP call the check made: what was called and the status it answered. The path names the API endpoint
// (it can carry an id such as an organization slug), never a query string or a body.
type VerifyCall struct {
	Endpoint string `json:"endpoint"`
	Status   int    `json:"status"` // 0: no answer arrived (network error, time-out)
	OK       bool   `json:"ok"`
}

// VerifyStep is one step of the check.
type VerifyStep struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail"`
}

// VerifyCounts is what was read, as counts and yes/no: never the text.
type VerifyCounts struct {
	AccountNamed   bool `json:"accountNamed"`
	WorkspaceNamed bool `json:"workspaceNamed"`
	Scopes         int  `json:"scopes"`  // credential scopes the API reported
	Choices        int  `json:"choices"` // values of the first listable setting
	Assignments    int  `json:"assignments"`
	Items          int  `json:"items"`
	MorePages      bool `json:"morePages"` // a page said there is more (not read: the check reads one page)
	DetailRead     bool `json:"detailRead"`
}

// VerifyReport is the outcome of a check against the account.
type VerifyReport struct {
	IntegrationID string       `json:"integrationId"`
	Kind          string       `json:"kind"`
	OK            bool         `json:"ok"`
	At            int64        `json:"at"`
	Summary       string       `json:"summary"`
	Steps         []VerifyStep `json:"steps"`
	Calls         []VerifyCall `json:"calls"`
	Read          VerifyCounts `json:"read"`
	// Mismatches says what the vendor answered that Drydock does not expect: a shape it does not recognize, fields missing.
	Mismatches []string `json:"mismatches"`
	// Notes are facts worth knowing that are not failures (what was read when nothing is assigned, items without a link).
	Notes []string `json:"notes"`
}

// VerifyRecord is what is kept of the last check (settings key integration.verified.<id>): no content, no secret.
type VerifyRecord struct {
	At          int64  `json:"at"`
	OK          bool   `json:"ok"`
	Summary     string `json:"summary"`
	Calls       int    `json:"calls"`
	FailedCalls int    `json:"failedCalls"`
	Items       int    `json:"items"`
	Mismatches  int    `json:"mismatches"`
}

// ---- the call trace (only a check installs one; sync and Test do not) ----

type callTrace struct {
	mu    sync.Mutex
	calls []VerifyCall
}

type traceKey struct{}
type opKey struct{}

func withCallTrace(ctx context.Context, t *callTrace) context.Context {
	return context.WithValue(ctx, traceKey{}, t)
}

// withCallOp names the operation of the next call (a GraphQL operation name), for the trace.
func withCallOp(ctx context.Context, op string) context.Context {
	if op == "" {
		return ctx
	}
	return context.WithValue(ctx, opKey{}, op)
}

var gqlOpRe = regexp.MustCompile(`^\s*(?:query|mutation)\s+([A-Za-z][A-Za-z0-9_]{0,60})`)

func graphqlOpName(q string) string {
	if m := gqlOpRe.FindStringSubmatch(q); m != nil {
		return m[1]
	}
	return ""
}

// traceCall records one HTTP call when a check is tracing. path has no query string (the client sends q apart); anything after
// a "?" is dropped anyway.
func traceCall(ctx context.Context, method, path string, status int) {
	t, _ := ctx.Value(traceKey{}).(*callTrace)
	if t == nil {
		return
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	ep := method + " " + clampText(path, 120)
	if op, _ := ctx.Value(opKey{}).(string); op != "" {
		ep += " (" + op + ")"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.calls) < verifyMaxCalls {
		t.calls = append(t.calls, VerifyCall{Endpoint: ep, Status: status, OK: status >= 200 && status < 300})
	}
}

func (t *callTrace) list() []VerifyCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]VerifyCall{}, t.calls...)
}

// ---- the check ----

// listedSetting is the first assignment setting of a kind whose values the vendor's API lists.
func listedSetting(kind string) (Setting, bool) {
	for _, k := range Catalog(nil) {
		if k.Kind != kind {
			continue
		}
		for _, st := range k.Settings {
			if st.Choices {
				return st, true
			}
		}
	}
	return Setting{}, false
}

// Verify checks a connection against the person's real account, read-only (see the top of this file). by must be a human:
// it is the person's own action, and it calls the vendor.
func (s *Service) Verify(ctx context.Context, by, id string) (VerifyReport, error) {
	if err := requireHuman(by); err != nil {
		return VerifyReport{}, err
	}
	conn, err := s.st.GetConnection(ctx, id)
	if err != nil {
		return VerifyReport{}, err
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return VerifyReport{}, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	mu := s.lock("verify:" + id)
	if !mu.TryLock() {
		return VerifyReport{}, ErrVerifyBusy
	}
	defer mu.Unlock()

	tr := &callTrace{}
	ctx = withCallTrace(ctx, tr)
	v := vendor(conn.Kind)
	rep := VerifyReport{IntegrationID: id, Kind: conn.Kind, At: s.nowMS(), Steps: []VerifyStep{}, Mismatches: []string{}, Notes: []string{}}
	step := func(sid, name string, ok bool, detail string) {
		rep.Steps = append(rep.Steps, VerifyStep{ID: sid, Name: name, OK: ok, Detail: s.scrub(detail)})
	}
	skip := func(sid, name, detail string) {
		rep.Steps = append(rep.Steps, VerifyStep{ID: sid, Name: name, OK: true, Skipped: true, Detail: s.scrub(detail)})
	}
	mismatch := func(format string, a ...any) {
		rep.Mismatches = append(rep.Mismatches, s.scrub(fmt.Sprintf(format, a...)))
	}
	failed := func(sid, name string, err error) {
		var f *Failure
		if !errors.As(err, &f) {
			f = Classify(conn.Kind, err, "")
		}
		if f.Code == CodeSchemaDrift || f.Code == CodeToolMissing {
			d := driftDetail(f)
			step(sid, name, false, v+" answered in a shape Drydock does not recognize: "+d+".")
			mismatch("%s: %s", name, d)
			return
		}
		step(sid, name, false, f.Message)
	}
	call := func(f func(context.Context) error) error {
		cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
		defer cancel()
		return f(cctx)
	}
	finish := func() (VerifyReport, error) { return s.finishVerify(ctx, conn, rep, tr), nil }

	// 1. a credential is stored (GitHub through gh: gh holds it)
	if !usesGH(conn) {
		if !s.hasCredential(ctx, conn) {
			step("credential", "Credential stored", false, "No usable credential is stored for "+v+". Reconnect it, then check again.")
			return finish()
		}
		step("credential", "Credential stored", true, authMethod(conn)+", in the credential store (never shown).")
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		failed("identity", "Who the credential is", fail)
		return finish()
	}

	// 2. who it is
	var ident Identity
	if err := call(func(c context.Context) (e error) { ident, e = ad.Identity(c, api); return }); err != nil {
		failed("identity", "Who the credential is", err)
		return finish()
	}
	rep.Read.AccountNamed = strings.TrimSpace(ident.Account) != ""
	rep.Read.WorkspaceNamed = strings.TrimSpace(ident.Workspace) != ""
	rep.Read.Scopes = len(ident.Scopes)
	if !rep.Read.AccountNamed && !rep.Read.WorkspaceNamed {
		mismatch("%s answered the identity call but named neither the account nor the workspace.", v)
	}
	step("identity", "Who the credential is", true, fmt.Sprintf("%s accepted the credential; the account is %s, the workspace is %s%s.", v,
		namedWord(rep.Read.AccountNamed), namedWord(rep.Read.WorkspaceNamed), scopesPhrase(len(ident.Scopes))))

	// 3. the choices of the first listable setting
	setting, hasSetting := listedSetting(conn.Kind)
	var choices []Choice
	if hasSetting {
		name := "List " + strings.ToLower(setting.Label) + " choices"
		if err := call(func(c context.Context) (e error) { choices, e = ad.Choices(c, api, setting.Key, nil); return }); err != nil {
			failed("choices", name, err)
		} else {
			rep.Read.Choices = len(choices)
			empty := 0
			for _, ch := range choices {
				if strings.TrimSpace(ch.Value) == "" {
					empty++
				}
			}
			if empty > 0 {
				mismatch("%d of the %d %s choices %s listed had no value.", empty, len(choices), strings.ToLower(setting.Label), v)
			}
			step("choices", name, true, fmt.Sprintf("%s listed %d %s choice(s).", v, len(choices), strings.ToLower(setting.Label)))
		}
	}

	// 4. one page of each scope
	var scopes []map[string]string
	asgs, err := s.st.ListAssignments(ctx, id)
	if err != nil {
		return VerifyReport{}, err
	}
	for _, a := range asgs {
		if a.Enabled && len(scopes) < verifyMaxScopes {
			scopes = append(scopes, a.Config)
		}
	}
	readName := "Read " + itemsWord(conn.Kind)
	if len(scopes) == 0 {
		cfg := map[string]string{}
		needs := hasSetting && (setting.Required || conn.Kind == KindGitHub)
		switch {
		case needs && len(choices) > 0 && choices[0].Value != "":
			cfg[setting.Key] = choices[0].Value
			rep.Notes = append(rep.Notes, fmt.Sprintf("Not assigned to a project yet: the %s were read from the first %s %s listed.", itemsWord(conn.Kind), strings.ToLower(setting.Label), v))
			scopes = append(scopes, cfg)
		case needs:
			skip("read", readName, fmt.Sprintf("Not assigned to a project and no %s to read from, so no %s were read.", strings.ToLower(setting.Label), itemsWord(conn.Kind)))
		default:
			rep.Notes = append(rep.Notes, fmt.Sprintf("Not assigned to a project yet: the %s were read from everything the credential can see.", itemsWord(conn.Kind)))
			scopes = append(scopes, cfg)
		}
	}
	var first *RawItem
	var firstCfg map[string]string
	for i, cfg := range scopes {
		sid, name := "read", readName
		if len(scopes) > 1 {
			sid, name = fmt.Sprintf("read-%d", i+1), fmt.Sprintf("%s (project scope %d of %d)", readName, i+1, len(scopes))
		}
		var pg Page
		if err := call(func(c context.Context) (e error) {
			pg, e = ad.Page(c, api, PageQuery{Config: cfg, Limit: verifyPageSize})
			return
		}); err != nil {
			failed(sid, name, err)
			continue
		}
		rep.Read.Assignments++
		rep.Read.Items += len(pg.Items)
		if pg.Next != "" {
			rep.Read.MorePages = true
		}
		miss, notes := checkItems(conn.Kind, ad.FullSweep(), pg.Items)
		for _, m := range miss {
			mismatch("%s: %s", name, m)
		}
		rep.Notes = append(rep.Notes, notes...)
		more := ""
		if pg.Next != "" {
			more = " (there are more; one page is enough for the check)"
		}
		step(sid, name, true, fmt.Sprintf("%s returned %d %s%s.", v, len(pg.Items), itemsWord(conn.Kind), more))
		if first == nil && len(pg.Items) > 0 && pg.Items[0].ExternalID != "" {
			it := pg.Items[0]
			first, firstCfg = &it, cfg
		}
	}
	if len(scopes) > 0 && rep.Read.Items == 0 && rep.Read.Assignments > 0 {
		rep.Notes = append(rep.Notes, "Nothing to read in this scope, so the shape of real "+itemsWord(conn.Kind)+" is unchecked. Check again once there are some.")
	}

	// 5. the full text of the first item, where the vendor keeps it apart
	if first != nil {
		var text string
		var has bool
		if err := call(func(c context.Context) (e error) {
			text, has, e = ad.Detail(c, api, firstCfg, first.ExternalID)
			return
		}); err != nil {
			failed("detail", "Read the full text of one item", err)
		} else if has {
			rep.Read.DetailRead = true
			step("detail", "Read the full text of one item", true, fmt.Sprintf("%s returned the full text of one item (%d characters; not kept).", v, len([]rune(text))))
		}
	}
	return finish()
}

// finishVerify sets the verdict and the summary, keeps the record and publishes the event.
func (s *Service) finishVerify(ctx context.Context, conn Connection, rep VerifyReport, tr *callTrace) VerifyReport {
	v := vendor(conn.Kind)
	rep.Calls = tr.list()
	failedSteps := 0
	var firstFail string
	for _, st := range rep.Steps {
		if !st.OK {
			failedSteps++
			if firstFail == "" {
				firstFail = st.Detail
			}
		}
	}
	failedCalls := 0
	for _, c := range rep.Calls {
		if !c.OK {
			failedCalls++
		}
	}
	rep.OK = failedSteps == 0 && len(rep.Mismatches) == 0
	calls := plural(len(rep.Calls), "call", "calls")
	if usesGH(conn) {
		calls = "through gh"
	}
	switch {
	case rep.OK:
		rep.Summary = fmt.Sprintf("%s answered every check (%s, %s read) and nothing differed from what Drydock expects.", v, calls, plural(rep.Read.Items, "item", "items"))
	case firstFail != "":
		rep.Summary = fmt.Sprintf("%s did not pass the check: %s", v, firstFail)
	default:
		rep.Summary = fmt.Sprintf("%s answered, but %s did not match what Drydock expects.", v, plural(len(rep.Mismatches), "thing", "things"))
	}
	rep.Summary = s.scrub(rep.Summary)
	rec := VerifyRecord{At: rep.At, OK: rep.OK, Summary: rep.Summary, Calls: len(rep.Calls), FailedCalls: failedCalls, Items: rep.Read.Items, Mismatches: len(rep.Mismatches)}
	if err := s.st.SetVerification(ctx, conn, rec); err != nil {
		s.o.Logf("integrations: keep the check of %s: %v", conn.ID, err)
	}
	s.emit(ctx, EventVerified, "", "", map[string]any{"integrationId": conn.ID, "kind": conn.Kind, "ok": rep.OK, "calls": len(rep.Calls), "failedCalls": failedCalls,
		"items": rep.Read.Items, "mismatches": len(rep.Mismatches)})
	return rep
}

// checkItems checks what Drydock needs of every item: an id (dedupe), a title, a reference (task headers, "Closes"), an update
// time (the watermark; a full-sweep vendor does not need one). A missing link is a note: the item is shown without one.
func checkItems(kind string, fullSweep bool, items []RawItem) (miss, notes []string) {
	var noID, noTitle, noRef, noUpdated, noLink int
	for _, it := range items {
		if strings.TrimSpace(it.ExternalID) == "" {
			noID++
		}
		if strings.TrimSpace(it.Title) == "" {
			noTitle++
		}
		if it.Kind != ItemPage && !issueRefRe.MatchString(it.IssueRef) {
			noRef++
		}
		if it.Updated <= 0 {
			noUpdated++
		}
		if validHTTPURL(it.URL) == "" {
			noLink++
		}
	}
	n, w := len(items), itemsWord(kind)
	add := func(c int, what string) {
		if c > 0 {
			miss = append(miss, fmt.Sprintf("%d of %d %s have no %s", c, n, w, what))
		}
	}
	add(noID, "id")
	add(noTitle, "title")
	add(noRef, "reference Drydock can use")
	if fullSweep {
		if noUpdated > 0 {
			notes = append(notes, fmt.Sprintf("%d of %d %s have no update time (not needed: every sync reads them all).", noUpdated, n, w))
		}
	} else {
		add(noUpdated, "update time")
	}
	if noLink > 0 {
		notes = append(notes, fmt.Sprintf("%d of %d %s have no link, so Drydock shows them without one.", noLink, n, w))
	}
	return miss, notes
}

// driftDetail says what about an answer was unexpected, without quoting it: a field and its JSON type, a byte offset, or
// Drydock's own description.
func driftDetail(f *Failure) string {
	var te *json.UnmarshalTypeError
	if errors.As(f, &te) {
		field := te.Field
		if field == "" {
			field = "the answer"
		}
		return fmt.Sprintf("the field %q is a JSON %s where Drydock expects %s", clampText(field, 80), te.Value, jsonKindWord(te.Type))
	}
	var se *json.SyntaxError
	if errors.As(f, &se) {
		return fmt.Sprintf("the answer is not valid JSON (at byte %d)", se.Offset)
	}
	if f.Cause != nil {
		return clampText(firstLine(f.Cause.Error()), 160)
	}
	return f.Message
}

// jsonKindWord says in words what JSON a Go type takes (not the Go type's name).
func jsonKindWord(t reflect.Type) string {
	if t == nil {
		return "another type"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Float32, reflect.Float64:
		return "a number"
	}
	return "another type"
}

func namedWord(b bool) string {
	if b {
		return "named"
	}
	return "not named"
}

func scopesPhrase(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", with %s reported", plural(n, "scope", "scopes"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---- the record ----

func verifyKey(id string) string { return "integration.verified." + id }

// SetVerification keeps the outcome of a check, only while the connection exists with the credential it was checked with (a
// reconnect or a disconnect during the check leaves nothing behind).
func (s *Store) SetVerification(ctx context.Context, c Connection, rec VerifyRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) SELECT ?,? WHERE EXISTS (SELECT 1 FROM integrations WHERE id=? AND version=?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, verifyKey(c.ID), string(b), c.ID, c.Version)
		return err
	})
}

// Verification returns the last check of a connection (nil: never checked, or checked before its credential changed).
func (s *Store) Verification(ctx context.Context, id string) (*VerifyRecord, error) {
	var raw string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, verifyKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec VerifyRecord
	if json.Unmarshal([]byte(raw), &rec) != nil {
		return nil, nil
	}
	return &rec, nil
}

// ClearVerification forgets the last check (a new credential may reach another account).
func (s *Store) ClearVerification(ctx context.Context, id string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, verifyKey(id))
		return err
	})
}

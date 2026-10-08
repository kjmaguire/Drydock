package room

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
)

// ExportKind names an export (room-protocol s8).
type ExportKind string

const (
	ExportMinutes ExportKind = "minutes"
	ExportPR      ExportKind = "pr_description"
	ExportADR     ExportKind = "adr_candidates"
)

// Export is an immutable snapshot of a message range.
type Export struct {
	ID        string          `json:"id"`
	RoomID    string          `json:"roomId"`
	Scope     string          `json:"scope"`
	ReceiptID string          `json:"receiptId"`
	Kind      ExportKind      `json:"kind"`
	Format    string          `json:"format"` // markdown or json
	FromSeq   int64           `json:"fromSeq"`
	ToSeq     int64           `json:"toSeq"`
	Params    json.RawMessage `json:"params"`
	Content   string          `json:"content"`
	SHA256    string          `json:"sha256"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
	EventSeq  uint64          `json:"eventSeq,omitempty"`
}

// ExportRequest asks for an export. Scope and ReceiptID make it idempotent (brief section 18): asking again with the
// same pair returns the stored export and creates nothing.
type ExportRequest struct {
	Scope     string
	ReceiptID string
	FromSeq   int64 // 0: from the start
	ToSeq     int64 // 0: up to now
	Summary   string
	Title     string
	By        Author
}

// exportParams are frozen into the row so a verifier can render the same text again from the immutable messages.
type exportParams struct {
	Summary  string `json:"summary,omitempty"`
	Title    string `json:"title,omitempty"`
	RoomName string `json:"room_name"`
	RoomKind Kind   `json:"room_kind"`
	TaskID   string `json:"task_id,omitempty"`
}

// ADRCandidate is a DECISION that a human may turn into an ADR (MADR-like, design).
type ADRCandidate struct {
	Title            string   `json:"title"`
	Status           string   `json:"status"` // proposed | accepted
	Context          string   `json:"context"`
	Decision         string   `json:"decision"`
	Consequences     string   `json:"consequences"`
	Alternatives     []string `json:"alternatives"`
	SourceMessageIDs []string `json:"source_message_ids"`
	ProposedBy       string   `json:"proposed_by"`
}

func oneLine(s string, n int) string { return clip(s, n) }

func ts(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05Z") }

// effective drops messages superseded by a later message in the list.
func effective(msgs []Message) []Message {
	sup := map[string]bool{}
	for _, m := range msgs {
		if m.SupersedesID != "" {
			sup[m.SupersedesID] = true
		}
	}
	var out []Message
	for _, m := range msgs {
		if !sup[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// ADRCandidatesFrom builds one candidate per surviving DECISION message; a superseded proposal folds into the message
// that replaced it and both ids are listed as sources.
func ADRCandidatesFrom(msgs []Message) []ADRCandidate {
	byID := map[string]Message{}
	for _, m := range msgs {
		byID[m.ID] = m
	}
	var out []ADRCandidate
	for _, m := range effective(msgs) {
		if m.Type != TypeDecision {
			continue
		}
		st := m.PayloadString("status")
		if st == "superseded" {
			continue
		}
		c := ADRCandidate{Title: m.PayloadString("title"), Status: st, Decision: m.PayloadString("decision"),
			Context: firstNonEmpty(m.PayloadString("context"), m.PayloadString("rationale")), Consequences: m.PayloadString("consequences"),
			ProposedBy: m.Author.Key(), Alternatives: []string{}}
		m.PayloadField("alternatives", &c.Alternatives)
		if c.Alternatives == nil {
			c.Alternatives = []string{}
		}
		for cur, guard := m, 0; guard < 50; guard++ {
			c.SourceMessageIDs = append([]string{cur.ID}, c.SourceMessageIDs...)
			prev, ok := byID[cur.SupersedesID]
			if cur.SupersedesID == "" || !ok {
				break
			}
			cur = prev
		}
		out = append(out, c)
	}
	return out
}

func renderADRJSON(msgs []Message) string {
	c := ADRCandidatesFrom(msgs)
	if c == nil {
		c = []ADRCandidate{}
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return string(b) + "\n"
}

// RenderADRMarkdown renders one candidate in a MADR-like layout.
func RenderADRMarkdown(c ADRCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nStatus: %s\n\n## Context\n\n%s\n\n## Decision\n\n%s\n\n## Consequences\n\n%s\n", oneLine(c.Title, 200), c.Status,
		orNone(c.Context), orNone(c.Decision), orNone(c.Consequences))
	if len(c.Alternatives) > 0 {
		b.WriteString("\n## Alternatives considered\n\n")
		for _, a := range c.Alternatives {
			b.WriteString("- " + oneLine(a, 300) + "\n")
		}
	}
	b.WriteString("\nSource messages: " + strings.Join(c.SourceMessageIDs, ", ") + "\n")
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "_not stated_"
	}
	return s
}

// RenderMinutes renders Markdown minutes from a message range. It is deterministic: no clock, no model. The summary
// is whatever the caller supplied and is labeled as such.
func RenderMinutes(p exportParams, from, to int64, msgs []Message) string {
	var b strings.Builder
	title := firstNonEmpty(p.Title, "Minutes: "+p.RoomName)
	fmt.Fprintf(&b, "# %s\n\n", oneLine(title, 200))
	fmt.Fprintf(&b, "Room: %s `%s`", p.RoomKind, p.RoomName)
	if p.TaskID != "" {
		fmt.Fprintf(&b, " | Task: %s", p.TaskID)
	}
	fmt.Fprintf(&b, " | Messages: seq %d to %d", from, to)
	if len(msgs) > 0 {
		fmt.Fprintf(&b, " | %s to %s", ts(msgs[0].CreatedAt), ts(msgs[len(msgs)-1].CreatedAt))
	}
	b.WriteString("\n\n## Summary\n\n")
	if strings.TrimSpace(p.Summary) == "" {
		b.WriteString("_No summary supplied._\n")
	} else {
		b.WriteString("_Supplied by the caller, not generated from the transcript here._\n\n" + p.Summary + "\n")
	}

	counts := map[string]int{}
	for _, m := range msgs {
		counts[m.Author.Key()]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("\n## Participants\n\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d messages\n", k, counts[k])
	}

	eff := effective(msgs)
	section := func(h string, f func(m Message) string) {
		var lines []string
		for _, m := range eff {
			if l := f(m); l != "" {
				lines = append(lines, "- "+l)
			}
		}
		b.WriteString("\n## " + h + "\n\n")
		if len(lines) == 0 {
			b.WriteString("_None._\n")
			return
		}
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	section("Decisions", func(m Message) string {
		if m.Type != TypeDecision || m.PayloadString("status") == "superseded" {
			return ""
		}
		return fmt.Sprintf("[%s] %s (%s, seq %d, by %s)", m.PayloadString("status"), oneLine(m.PayloadString("title"), 160), m.ID, m.Seq, m.Author.Key())
	})
	section("TODOs and tasks", func(m Message) string {
		if m.Type != TypeTask {
			return ""
		}
		return fmt.Sprintf("[%s] %s (%s, seq %d)", firstNonEmpty(m.PayloadString("state"), "open"), oneLine(m.PayloadString("title"), 160), m.ID, m.Seq)
	})
	section("Results and checks", func(m Message) string {
		switch {
		case m.Author.Kind == AuthorTool && m.PayloadString("check_kind") != "":
			return fmt.Sprintf("%s %s: %s (%s, log %s)", m.Author.ID, m.PayloadString("check_kind"), strings.ToUpper(m.PayloadString("check_status")), m.ID, m.Refs.LogRef)
		case m.Type == TypeResult:
			return fmt.Sprintf("%s (%s, seq %d, by %s)", oneLine(firstNonEmpty(m.PayloadString("summary"), m.Body), 200), m.ID, m.Seq, m.Author.Key())
		}
		return ""
	})
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Author.Kind == AuthorHuman {
			answered[m.ReplyToID] = true
			answered[m.ThreadRootID] = true
		}
	}
	section("Open questions", func(m Message) string {
		if m.Type != TypeQuestion || answered[m.ID] {
			return ""
		}
		return fmt.Sprintf("%s (%s, seq %d, from %s)", oneLine(m.Body, 200), m.ID, m.Seq, m.Author.Key())
	})
	section("Approvals", func(m Message) string {
		switch m.Type {
		case TypeApprovalRequest:
			return fmt.Sprintf("requested %s: %s (%s)", m.PayloadString("approval_id"), oneLine(m.PayloadString("canonical_action_summary"), 160), m.ID)
		case TypeApproval:
			return fmt.Sprintf("decided %s: %s by %s (%s)", m.PayloadString("approval_id"), m.PayloadString("decision"), m.PayloadString("decided_by"), m.ID)
		}
		return ""
	})
	refSet := map[string]bool{}
	for _, m := range msgs {
		for _, r := range []struct{ k, v string }{{"run", m.Refs.RunID}, {"checkpoint", m.Refs.CheckpointID}, {"handoff", m.Refs.HandoffID}, {"commit", m.Refs.Commit}} {
			if r.v != "" {
				refSet[r.k+" "+r.v] = true
			}
		}
	}
	var refs []string
	for r := range refSet {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	b.WriteString("\n## References\n\n")
	if len(refs) == 0 {
		b.WriteString("_None._\n")
	}
	for _, r := range refs {
		b.WriteString("- " + r + "\n")
	}

	b.WriteString("\n## Transcript\n")
	for _, m := range msgs {
		note := ""
		if m.Trust == TrustUntrusted {
			note = " (untrusted)"
		}
		fmt.Fprintf(&b, "\n**seq %d | %s | %s | %s%s** (%s)\n\n", m.Seq, ts(m.CreatedAt), m.Author.Key(), m.Type, note, m.ID)
		for _, ln := range strings.Split(strings.TrimRight(m.Body, "\n"), "\n") {
			b.WriteString("> " + strings.TrimRight(ln, "\r") + "\n")
		}
	}
	return b.String()
}

// RenderPRDescription drafts a pull request description from the task, results, latest checks, diff stat and accepted
// decisions in the range. Sending it to GitHub is a separate outbound write that needs approval.
func RenderPRDescription(p exportParams, from, to int64, msgs []Message) string {
	eff := effective(msgs)
	var b strings.Builder
	title := p.Title
	for _, m := range eff {
		if title == "" && m.Type == TypeTask && m.PayloadString("state") != "proposed" {
			title = m.PayloadString("title")
		}
	}
	title = firstNonEmpty(title, p.RoomName)
	fmt.Fprintf(&b, "# %s\n\n## Summary\n\n", oneLine(title, 200))
	if strings.TrimSpace(p.Summary) != "" {
		b.WriteString(p.Summary + "\n\n")
	}
	n := 0
	for _, m := range eff {
		if m.Type == TypeResult && m.Author.Kind == AuthorAgent {
			fmt.Fprintf(&b, "- %s (%s)\n", oneLine(firstNonEmpty(m.PayloadString("summary"), m.Body), 240), m.ID)
			n++
		}
	}
	if n == 0 && strings.TrimSpace(p.Summary) == "" {
		b.WriteString("_No results recorded in the selected range._\n")
	}
	var stats []string
	for _, m := range eff {
		if s := m.PayloadString("diff_stat"); s != "" {
			stats = append(stats, s)
		}
	}
	if len(stats) > 0 {
		b.WriteString("\n## Changes\n\n```\n" + strings.Join(stats, "\n") + "\n```\n")
	}
	latest := map[string]Message{}
	for _, m := range eff {
		if k := m.PayloadString("check_kind"); k != "" && m.Author.Kind == AuthorTool {
			latest[k] = m
		}
	}
	b.WriteString("\n## Checks\n\n")
	if len(latest) == 0 {
		b.WriteString("_No check results in the selected range._\n")
	} else {
		kinds := make([]string, 0, len(latest))
		for k := range latest {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		b.WriteString("| Check | Status | Log |\n|---|---|---|\n")
		for _, k := range kinds {
			m := latest[k]
			fmt.Fprintf(&b, "| %s | %s | %s |\n", k, m.PayloadString("check_status"), strings.ReplaceAll(m.Refs.LogRef, "|", "/"))
		}
	}
	var dec []string
	for _, m := range eff {
		if m.Type == TypeDecision && m.PayloadString("status") == "accepted" {
			dec = append(dec, fmt.Sprintf("- %s (%s)", oneLine(m.PayloadString("title"), 160), m.ID))
		}
	}
	if len(dec) > 0 {
		b.WriteString("\n## Decisions\n\n" + strings.Join(dec, "\n") + "\n")
	}
	fmt.Fprintf(&b, "\n---\nDraft generated from room `%s`, messages seq %d to %d. Posting it to GitHub is an outbound write that needs approval.\n", p.RoomName, from, to)
	return b.String()
}

// ExportMinutes snapshots Markdown minutes. created is false when the (scope, receipt) pair already existed.
func (s *Service) ExportMinutes(ctx context.Context, roomID string, r ExportRequest) (Export, bool, error) {
	return s.export(ctx, roomID, ExportMinutes, r)
}

// ExportPRDescription snapshots a PR description draft.
func (s *Service) ExportPRDescription(ctx context.Context, roomID string, r ExportRequest) (Export, bool, error) {
	return s.export(ctx, roomID, ExportPR, r)
}

// ExportADRCandidates snapshots the ADR candidates (JSON) from the DECISION messages in the range.
func (s *Service) ExportADRCandidates(ctx context.Context, roomID string, r ExportRequest) (Export, bool, error) {
	return s.export(ctx, roomID, ExportADR, r)
}

func render(kind ExportKind, p exportParams, from, to int64, msgs []Message) (string, string) {
	switch kind {
	case ExportPR:
		return RenderPRDescription(p, from, to, msgs), "markdown"
	case ExportADR:
		return renderADRJSON(msgs), "json"
	}
	return RenderMinutes(p, from, to, msgs), "markdown"
}

func (s *Service) export(ctx context.Context, roomID string, kind ExportKind, r ExportRequest) (Export, bool, error) {
	if r.Scope == "" || r.ReceiptID == "" {
		return Export{}, false, fmt.Errorf("%w: an export needs a scope and a receipt id", ErrInvalid)
	}
	if err := r.By.Validate(); err != nil {
		return Export{}, false, err
	}
	if r.By.Kind == AuthorAgent || r.By.Kind == AuthorTool {
		return Export{}, false, fmt.Errorf("%w: exports are requested by a human, a rule or the system", ErrForbidden)
	}
	var out Export
	var created bool
	var project string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		out, created = Export{}, false
		room, err := getRoom(ctx, tx, roomID)
		if err != nil {
			return err
		}
		project = room.ProjectID
		ex, err := scanExport(tx.QueryRowContext(ctx, `SELECT `+exportCols+` FROM room_exports WHERE scope=? AND receipt_id=?`, r.Scope, r.ReceiptID))
		if err == nil {
			if ex.RoomID != roomID || ex.Kind != kind {
				return fmt.Errorf("%w: %s/%s", ErrReceiptClash, r.Scope, r.ReceiptID)
			}
			out = ex
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		to := r.ToSeq
		if to <= 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE room_id=?`, roomID).Scan(&to); err != nil {
				return err
			}
		}
		from := r.FromSeq
		if from <= 0 {
			from = 1
		}
		msgs, err := queryMessages(ctx, tx, `room_id=? AND seq>=? AND seq<=? ORDER BY seq`, roomID, from, to)
		if err != nil {
			return err
		}
		p := exportParams{Summary: r.Summary, Title: r.Title, RoomName: room.Name, RoomKind: room.Kind, TaskID: room.TaskID}
		content, format := render(kind, p, from, to, msgs)
		if s.opt.Redact != nil {
			if content, err = s.opt.Redact(content); err != nil {
				return fmt.Errorf("%w: %v", ErrBlocked, err)
			}
		}
		sum := sha256.Sum256([]byte(content))
		pj, _ := json.Marshal(p)
		out = Export{ID: ids.New("exp"), RoomID: roomID, Scope: r.Scope, ReceiptID: r.ReceiptID, Kind: kind, Format: format, FromSeq: from, ToSeq: to,
			Params: pj, Content: content, SHA256: hex.EncodeToString(sum[:]), CreatedBy: r.By.Key(), CreatedAt: fromMs(s.nowMs())}
		_, err = tx.ExecContext(ctx, `INSERT INTO room_exports(id,room_id,scope,receipt_id,kind,format,from_seq,to_seq,params,content,sha256,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			out.ID, roomID, out.Scope, out.ReceiptID, string(kind), format, from, to, string(pj), content, out.SHA256, out.CreatedBy, out.CreatedAt.UnixMilli())
		created = err == nil
		return err
	})
	if err != nil {
		return Export{}, false, err
	}
	if created && s.bus != nil {
		seq, err := events.Emit(ctx, s.bus, "room.export_created", events.Event{ProjectID: project}, map[string]any{"room": roomID, "export": out.ID, "kind": kind})
		if err != nil {
			s.report(err)
		} else {
			s.report(s.db.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE room_exports SET event_seq=? WHERE id=? AND event_seq=0`, seq, out.ID)
				return err
			}))
			out.EventSeq = seq
		}
	}
	return out, created, nil
}

const exportCols = `id,room_id,scope,receipt_id,kind,format,from_seq,to_seq,params,content,sha256,created_by,created_at,event_seq`

func scanExport(sc scanner) (Export, error) {
	var e Export
	var kind, params string
	var created int64
	if err := sc.Scan(&e.ID, &e.RoomID, &e.Scope, &e.ReceiptID, &kind, &e.Format, &e.FromSeq, &e.ToSeq, &params, &e.Content, &e.SHA256, &e.CreatedBy, &created, &e.EventSeq); err != nil {
		return Export{}, err
	}
	e.Kind, e.Params, e.CreatedAt = ExportKind(kind), json.RawMessage(params), fromMs(created)
	return e, nil
}

// GetExport returns one export.
func (s *Service) GetExport(ctx context.Context, id string) (Export, error) {
	e, err := scanExport(s.db.Reader().QueryRowContext(ctx, `SELECT `+exportCols+` FROM room_exports WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Export{}, fmt.Errorf("%w: export %s", ErrNotFound, id)
	}
	return e, err
}

// ListExports returns a room's exports, newest first.
func (s *Service) ListExports(ctx context.Context, roomID string) ([]Export, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT `+exportCols+` FROM room_exports WHERE room_id=? ORDER BY created_at DESC, id`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Export
	for rows.Next() {
		e, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// VerifyExport checks an export against its hash and against the immutable messages it covers: the content hash must
// match, and rendering the same range with the stored parameters must reproduce the content (before secret redaction
// changes, which is why a Service with Redact set only checks the hash).
func (s *Service) VerifyExport(ctx context.Context, id string) error {
	e, err := s.GetExport(ctx, id)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(e.Content))
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		return fmt.Errorf("%w: stored content hash differs", ErrExportInvalid)
	}
	if s.opt.Redact != nil {
		return nil
	}
	var p exportParams
	if err := json.Unmarshal(e.Params, &p); err != nil {
		return err
	}
	msgs, err := queryMessages(ctx, s.db.Reader(), `room_id=? AND seq>=? AND seq<=? ORDER BY seq`, e.RoomID, e.FromSeq, e.ToSeq)
	if err != nil {
		return err
	}
	again, _ := render(e.Kind, p, e.FromSeq, e.ToSeq, msgs)
	if again != e.Content {
		return fmt.Errorf("%w: rendering the covered messages gives different content", ErrExportInvalid)
	}
	return nil
}

// ADRCandidates returns the ADR candidates in a seq range (0 means open-ended).
func (s *Service) ADRCandidates(ctx context.Context, roomID string, fromSeq, toSeq int64) ([]ADRCandidate, error) {
	if toSeq <= 0 {
		toSeq = 1<<62 - 1
	}
	msgs, err := queryMessages(ctx, s.db.Reader(), `room_id=? AND seq>=? AND seq<=? AND (type='DECISION' OR supersedes_id<>'') ORDER BY seq`, roomID, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	return ADRCandidatesFrom(msgs), nil
}

// AcceptDecision records a human's acceptance of a proposed DECISION as a new DECISION message that supersedes it
// (messages are immutable). Only a human can; it creates an ADR candidate in the accepted state and nothing else.
func (s *Service) AcceptDecision(ctx context.Context, roomID, decisionID string, by Author) (Message, error) {
	if by.Kind != AuthorHuman {
		return Message{}, fmt.Errorf("%w: only a human accepts a decision", ErrForbidden)
	}
	d, err := s.GetMessage(ctx, decisionID)
	if err != nil {
		return Message{}, err
	}
	if d.RoomID != roomID || d.Type != TypeDecision {
		return Message{}, fmt.Errorf("%w: decision %s", ErrNotFound, decisionID)
	}
	var payload map[string]any
	_ = json.Unmarshal(d.Payload, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["status"], payload["accepted_by"] = "accepted", by.Key()
	delete(payload, "from_marker")
	p, err := s.Post(ctx, PostParams{RoomID: roomID, Type: TypeDecision, Author: by, Body: d.Body, Payload: payload, SupersedesID: decisionID,
		ThreadRootID: d.ThreadRootID, Refs: d.Refs, TaskID: d.TaskID})
	return p.Message, err
}

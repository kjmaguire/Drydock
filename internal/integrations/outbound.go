package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/security"
)

// ProposeReq proposes one outbound write. Params carries the human-readable parts (a comment body, a new status name); the
// exact call is built by the adapter, never by the caller.
type ProposeReq struct {
	IntegrationID string            `json:"integrationId"`
	Op            string            `json:"op"`
	Target        string            `json:"target"` // the external id of the item, page or parent the write applies to
	TaskID        string            `json:"taskId"`
	ProjectID     string            `json:"projectId"`
	Params        map[string]string `json:"params"`
}

const (
	maxParamRunes   = 4000
	maxPayloadBytes = 16 << 10
	maxReceiptRunes = 500
)

// canonicalCall renders the exact call an approval is bound to and its hash. Everything that determines what the vendor will
// receive is inside it: the integration, the operation, the target, the API call (stored in the outbox's tool column) and the
// arguments.
func canonicalCall(integrationID, kind, op, target, tool string, args json.RawMessage) (canonical, hash string, err error) {
	b, err := security.CanonicalJSON(map[string]any{"integration": integrationID, "kind": kind, "op": op, "target": target, "tool": tool, "args": args})
	if err != nil {
		return "", "", err
	}
	return string(b), hashHex(string(b)), nil
}

// descriptorFor is the security.ActionDescriptor of an outbox row, rebuilt from the stored columns only: a row whose payload
// was altered after approval produces a different hash and the approval refuses it.
func descriptorFor(o Outbox, kind string) (security.ActionDescriptor, error) {
	canonical, hash, err := canonicalCall(o.IntegrationID, kind, o.Op, o.Target, o.Tool, json.RawMessage(o.rawPayload))
	if err != nil {
		return security.ActionDescriptor{}, err
	}
	return security.ActionDescriptor{
		Kind: security.KindIntegrationWrite, Project: o.ProjectID, TaskID: o.TaskID,
		Tool: "integration:" + kind + "/" + o.Op, Command: canonical, Argv: []string{o.Tool, o.Op, o.Target}, ArgvDigest: hash,
		Targets: []security.Target{{Remote: kind + ":" + o.IntegrationID, Ref: o.Target}},
	}, nil
}

func stringsIn(v any, visit func(string)) {
	switch x := v.(type) {
	case string:
		visit(x)
	case []any:
		for _, e := range x {
			stringsIn(e, visit)
		}
	case map[string]any:
		for _, e := range x {
			stringsIn(e, visit)
		}
	}
}

// Propose records an outbound write and asks for a human approval bound to the hash of the exact call. Nothing is sent. The
// proposer may be an agent or a person; only a human can approve. A proposal whose text contains a secret is refused: it would
// leave the machine. A proposal identical to a live one (same task, operation, target and payload) returns the existing row.
func (s *Service) Propose(ctx context.Context, by string, r ProposeReq) (Outbox, error) {
	return s.propose(ctx, by, r, proposeOpts{})
}

// proposeOpts are what only Drydock's own writes set: never the caller of Propose (an agent, a tool, the screen).
type proposeOpts struct {
	// class is the auto-approve class of a templated write Drydock itself makes (LinkBack, status sync). When the person turned
	// that class on for the connection and project, the approval is decided in their name and the write is sent.
	class string
	// nonce separates writes that are the same call made for different reasons (status sync: the task event), so a second,
	// later move to the same status is a new proposal instead of the one sent before.
	nonce string
}

func (s *Service) propose(ctx context.Context, by string, r ProposeReq, po proposeOpts) (Outbox, error) {
	if strings.TrimSpace(by) == "" {
		return Outbox{}, errf(ErrBadRequest, "a proposal needs its author")
	}
	if s.o.Approvals == nil {
		return Outbox{}, errors.New("integrations: no approvals service is configured, so outbound writes are disabled")
	}
	conn, err := s.st.GetConnection(ctx, r.IntegrationID)
	if err != nil {
		return Outbox{}, err
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return Outbox{}, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	var op *OpInfo
	for _, o := range ad.Ops() {
		if o.Op == r.Op {
			o := o
			op = &o
		}
	}
	if op == nil {
		return Outbox{}, errf(ErrOpUnsupported, "%s has no operation %q", vendor(conn.Kind), r.Op)
	}
	target := strings.TrimSpace(r.Target)
	if len(target) > 200 || strings.ContainsAny(target, "\n\r\x00") {
		return Outbox{}, errf(ErrBadRequest, "the target is not a valid external id")
	}
	// clean the human-readable parts: invisible and control characters out, size limited
	params := map[string]string{}
	total := 0
	for k, v := range r.Params {
		cl, _ := stripUnsafe(v)
		cl = strings.TrimSpace(cl)
		if len([]rune(cl)) > maxParamRunes {
			return Outbox{}, errf(ErrBadRequest, "%s is longer than %d characters", k, maxParamRunes)
		}
		total += len(cl)
		params[k] = cl
	}
	if total > maxPayloadBytes {
		return Outbox{}, errf(ErrBadRequest, "the action is too large")
	}
	project := r.ProjectID
	cfg := s.outboundConfig(ctx, conn, &project, target)
	call, err := ad.Build(r.Op, target, params, cfg)
	if err != nil {
		return Outbox{}, err
	}
	// secrets must not leave the machine
	if s.o.Scanner != nil {
		var texts []string
		stringsIn(call.Args, func(t string) { texts = append(texts, t) })
		whole := strings.Join(texts, "\n")
		fs, serr := s.o.Scanner.Scan(ctx, whole)
		if serr != nil {
			return Outbox{}, errf(ErrContainsSecret, "the text could not be scanned, so it is not sent")
		}
		if len(fs) > 0 {
			rules := make([]string, 0, len(fs))
			for _, f := range fs {
				rules = append(rules, f.RuleID)
			}
			return Outbox{}, errf(ErrContainsSecret, "found %s; remove it and propose again", strings.Join(rules, ", "))
		}
	}
	tool := call.Endpoint
	if tool == "" {
		return Outbox{}, errf(ErrOpUnsupported, "no API call is known for %q", r.Op)
	}
	argsRaw, err := json.Marshal(call.Args)
	if err != nil {
		return Outbox{}, err
	}
	_, hash, err := canonicalCall(conn.ID, conn.Kind, r.Op, target, tool, argsRaw)
	if err != nil {
		return Outbox{}, err
	}
	row := Outbox{IntegrationID: conn.ID, ProjectID: project, TaskID: r.TaskID, Op: r.Op, Target: target, Tool: tool, PayloadHash: hash, Preview: s.scrub(call.Preview),
		Status: OutPlanned, IdempotencyKey: hashHex(conn.ID, r.TaskID, r.Op, target, hash), CreatedBy: by, Marker: commentMarker(call)}
	if po.nonce != "" {
		row.IdempotencyKey = hashHex(conn.ID, r.TaskID, r.Op, target, hash, po.nonce)
	}
	row.rawPayload = string(argsRaw)
	var args map[string]any
	_ = json.Unmarshal(argsRaw, &args)
	row.Payload = args
	row, existing, err := s.st.InsertOutbox(ctx, row)
	if err != nil {
		return Outbox{}, err
	}
	if existing {
		return row, nil
	}
	desc, err := descriptorFor(row, conn.Kind)
	if err != nil {
		return row, err
	}
	reason := fmt.Sprintf("Outbound write to %s: %s on %s. Proposed by %s. The text may derive from untrusted external content, so read it before approving. Nothing is sent until you approve and the action is executed.", vendor(conn.Kind), r.Op, target, by)
	apr, err := s.o.Approvals.Request(ctx, desc, reason)
	if err != nil {
		reason := s.scrub(err.Error())
		_, _ = s.st.UpdateOutbox(ctx, row.ID, OutboxUpdate{From: []string{OutPlanned}, To: OutCancelled, Error: &reason})
		return row, fmt.Errorf("integrations: could not request approval: %w", err)
	}
	if _, err := s.st.UpdateOutbox(ctx, row.ID, OutboxUpdate{From: []string{OutPlanned}, To: OutAwaiting, ApprovalID: &apr}); err != nil {
		return row, err
	}
	out, err := s.st.GetOutbox(ctx, row.ID)
	if err != nil {
		return row, err
	}
	s.emit(ctx, EventOutboundRequested, project, r.TaskID, map[string]any{"outboxId": out.ID, "integrationId": conn.ID, "op": r.Op, "approvalId": apr, "hash": hash, "by": by})
	if po.class != "" {
		if sent, ok := s.autoSend(ctx, out, conn, po.class); ok {
			return sent, nil
		}
	}
	return out, nil
}

// autoSend approves and sends a proposal under an auto-approve class the person turned on for its connection and project. It
// reports false, leaving the proposal waiting for a person, when the class is off, does not cover the operation, or the
// approval cannot be decided. The approval is decided in the name of the person who last turned the class on.
func (s *Service) autoSend(ctx context.Context, o Outbox, conn Connection, class string) (Outbox, bool) {
	if !classAllows(class, conn.Kind, o.Op) || o.Status != OutAwaiting {
		return o, false
	}
	a, err := s.st.getAutoApprove(ctx, conn.ID, o.ProjectID, class)
	if err != nil || !a.Enabled || !security.IsHumanPrincipal(a.UpdatedBy) {
		return o, false
	}
	if err := s.o.Approvals.Decide(ctx, o.ApprovalID, security.DecisionApprove, a.UpdatedBy); err != nil {
		s.o.Logf("integrations: auto-approve %s: %v", class, err)
		return o, false
	}
	decided, cls := a.UpdatedBy, class
	if ok, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutAwaiting}, To: OutApproved, DecidedBy: &decided, AutoClass: &cls}); err != nil || !ok {
		return o, false
	}
	s.emit(ctx, EventOutboundApproved, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "approvalId": o.ApprovalID, "hash": o.PayloadHash,
		"by": decided, "auto": true, "class": class})
	out, err := s.Execute(ctx, "integration:auto-approve", o.ID)
	if err != nil {
		s.o.Logf("integrations: auto-approved %s: %v", class, s.scrub(err.Error()))
	}
	return out, true
}

// commentMarker is the reference of a comment: a hash of its text with whitespace collapsed. A retry looks for a comment with
// that text on the target before posting again (the vendors offer no idempotency key for comments). "" for any other write.
func commentMarker(call OutCall) string {
	text := commentText(call.Args)
	if text == "" {
		return ""
	}
	return hashHex(normComment(text))[:16]
}

// commentText is the text of a comment call (Linear: body; Notion: the rich text's content).
func commentText(args map[string]any) string {
	if b := argString(args, "body"); b != "" {
		return b
	}
	if _, ok := args["rich_text"]; ok {
		var b strings.Builder
		for _, t := range asList(args["rich_text"]) {
			b.WriteString(str(asMap(asMap(t)["text"])["content"]))
		}
		return b.String()
	}
	return ""
}

func normComment(s string) string { return strings.Join(strings.Fields(s), " ") }

// outboundConfig finds the remote scope a write runs in: the assignment of the named project, else the assignment whose
// items hold the target, else the connection's own (pre-P42) scope. It fills the project when the caller named none.
func (s *Service) outboundConfig(ctx context.Context, conn Connection, project *string, target string) map[string]string {
	if *project != "" {
		if a, err := s.st.AssignmentFor(ctx, conn.ID, *project); err == nil {
			return a.Config
		}
	}
	if target != "" {
		if its, err := s.st.ListItems(ctx, ItemFilter{IntegrationID: conn.ID, Limit: 500}); err == nil {
			for _, it := range its {
				if it.ExternalID != target || it.AssignmentID == "" {
					continue
				}
				if a, err := s.st.GetAssignment(ctx, it.AssignmentID); err == nil {
					if *project == "" {
						*project = a.ProjectID
					}
					return a.Config
				}
			}
		}
	}
	if *project == "" {
		*project = conn.ProjectID
	}
	if asgs, err := s.st.ListAssignments(ctx, conn.ID); err == nil && len(asgs) == 1 {
		if *project == "" {
			*project = asgs[0].ProjectID
		}
		return asgs[0].Config
	}
	return conn.Config
}

// Decide records the human's answer to an outbound proposal (it is Approvals.Decide: agents, tools and every non-human principal
// are refused there and audited).
func (s *Service) Decide(ctx context.Context, by, outboxID string, approve bool) (Outbox, error) {
	if err := requireHuman(by); err != nil {
		return Outbox{}, err
	}
	o, err := s.st.GetOutbox(ctx, outboxID)
	if err != nil {
		return o, err
	}
	if o.Status != OutAwaiting {
		return o, errf(ErrBadRequest, "this action is %s, not waiting for a decision", o.Status)
	}
	d := security.DecisionReject
	if approve {
		d = security.DecisionApprove
	}
	if err := s.o.Approvals.Decide(ctx, o.ApprovalID, d, by); err != nil {
		if errors.Is(err, security.ErrApprovalExpired) {
			_, _ = s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutAwaiting}, To: OutExpired})
		}
		return o, err
	}
	to, evt := OutRejected, EventOutboundRejected
	if approve {
		to, evt = OutApproved, EventOutboundApproved
	}
	if _, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutAwaiting}, To: to, DecidedBy: &by}); err != nil {
		return o, err
	}
	s.emit(ctx, evt, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "approvalId": o.ApprovalID, "hash": o.PayloadHash, "by": by})
	return s.st.GetOutbox(ctx, o.ID)
}

// Execute sends an approved action. It rebuilds the action descriptor from the stored row and asks Approvals.Consume to prove it
// is exactly the approved one; only then does the write go out, exactly once. A changed payload, a missing, rejected, expired or
// already-used approval stops here and nothing is sent. A failed send needs a new proposal and a new approval.
func (s *Service) Execute(ctx context.Context, by, outboxID string) (Outbox, error) {
	if strings.TrimSpace(by) == "" {
		return Outbox{}, errf(ErrBadRequest, "an execution needs its author")
	}
	o, err := s.st.GetOutbox(ctx, outboxID)
	if err != nil {
		return o, err
	}
	conn, err := s.st.GetConnection(ctx, o.IntegrationID)
	if err != nil {
		return o, err
	}
	prev := o.Status
	ok, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutAwaiting, OutApproved}, To: OutExecuting})
	if err != nil {
		return o, err
	}
	if !ok {
		return o, errf(ErrNotApproved, "this action is %s and cannot be executed", prev)
	}
	desc, derr := descriptorFor(o, conn.Kind)
	if derr == nil {
		derr = s.o.Approvals.Consume(ctx, o.ApprovalID, desc)
	}
	if derr != nil {
		return s.afterRefused(ctx, o, derr)
	}
	// the approval is spent; from here the write is attempted exactly once
	var receipt string
	var fail *Failure
	ad, _ := AdapterFor(conn.Kind)
	api, afail := s.api(ctx, conn)
	if afail != nil {
		fail = afail
	} else if ad == nil {
		fail = &Failure{Code: CodeOther, Message: "unknown integration " + conn.Kind}
	} else {
		cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
		var serr error
		receipt, serr = ad.Send(cctx, api, OutCall{Endpoint: o.Tool, Args: o.Payload})
		cancel()
		if serr != nil {
			if !errors.As(serr, &fail) {
				fail = Classify(conn.Kind, serr, "")
			}
		}
	}
	now := s.nowMS()
	if fail != nil {
		msg := s.scrub(fail.Message)
		_, _ = s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutExecuting}, To: OutFailed, Error: &msg, SentAt: &now})
		s.emit(ctx, EventOutboundFailed, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "code": fail.Code, "by": by})
		out, _ := s.st.GetOutbox(ctx, o.ID)
		return out, fmt.Errorf("%w: %s", ErrSendFailed, msg)
	}
	receipt = s.scrub(receipt)
	receipt, _ = truncateRunes(strings.Join(strings.Fields(receipt), " "), maxReceiptRunes)
	if _, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutExecuting}, To: OutSent, SentAt: &now, Receipt: &receipt}); err != nil {
		return o, err
	}
	s.emit(ctx, EventOutboundSent, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "op": o.Op, "hash": o.PayloadHash, "by": by,
		"auto": o.AutoClass != "", "class": o.AutoClass})
	return s.st.GetOutbox(ctx, o.ID)
}

// afterRefused maps an approval refusal to the outbox state and a clear error. Nothing was sent.
func (s *Service) afterRefused(ctx context.Context, o Outbox, cause error) (Outbox, error) {
	set := func(to, msg string) {
		m := msg
		_, _ = s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutExecuting}, To: to, Error: &m})
	}
	var err error
	switch {
	case errors.Is(cause, security.ErrHashMismatch):
		set(OutFailed, "the action changed after it was approved; nothing was sent")
		err = fmt.Errorf("%w: %w", ErrPayloadChanged, cause)
		s.emit(ctx, EventOutboundFailed, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "code": "payload_changed"})
	case errors.Is(cause, security.ErrNotApproved):
		set(OutAwaiting, "") // still waiting for a human; it can be executed after approval
		err = fmt.Errorf("%w: %w", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrRejected), errors.Is(cause, security.ErrCancelled):
		set(OutRejected, "the approval was rejected or cancelled")
		err = fmt.Errorf("%w: %w", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrApprovalExpired):
		set(OutExpired, "the approval expired; propose the action again")
		err = fmt.Errorf("%w: %w", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrConsumed):
		set(OutFailed, "the approval was already used")
		err = fmt.Errorf("%w: %w", ErrNotApproved, cause)
	default:
		set(OutAwaiting, "")
		err = cause
	}
	out, _ := s.st.GetOutbox(ctx, o.ID)
	return out, err
}

// ListOutbox lists proposals.
func (s *Service) ListOutbox(ctx context.Context, integrationID, status string, limit int) ([]Outbox, error) {
	return s.st.ListOutbox(ctx, integrationID, status, limit)
}

// LinkBack proposes the comment that links a task's result back to the external item it came from ("Drydock opened a pull
// request ... "). Only the template and the PR URL are in the text; no external text is echoed. Like every outbound write it
// waits for an approval. Linear and Notion support comments; Sentry, Aikido and GitHub have no comment operation here.
func (s *Service) LinkBack(ctx context.Context, by, taskID, prURL string) (Outbox, error) {
	it, err := s.st.ItemByTask(ctx, taskID)
	if err != nil {
		return Outbox{}, err
	}
	u := validHTTPURL(prURL)
	if u == "" {
		return Outbox{}, errf(ErrBadRequest, "the pull request link must be an http(s) URL")
	}
	conn, err := s.st.GetConnection(ctx, it.IntegrationID)
	if err != nil {
		return Outbox{}, err
	}
	switch conn.Kind {
	case KindLinear, KindNotion:
	default:
		return Outbox{}, errf(ErrOpUnsupported, "%s has no comment operation, so there is nothing to post back", vendor(conn.Kind))
	}
	body := fmt.Sprintf("Drydock opened a pull request for this item: %s\n(task %s, sent by Drydock)", u, taskID)
	return s.propose(ctx, by, ProposeReq{IntegrationID: conn.ID, Op: "comment", Target: it.ExternalID, TaskID: taskID, ProjectID: it.ProjectID, Params: map[string]string{"body": body}},
		proposeOpts{class: ClassLinkback})
}

// commentFinder is an adapter that can look for a comment on a target by its text (Linear, Notion).
type commentFinder interface {
	// FindComment returns the vendor's id of a comment on target whose text (whitespace collapsed) hashes to marker, created at
	// or after since (ms); found=false when there is none.
	FindComment(ctx context.Context, a *API, target, marker string, since int64) (id string, found bool, err error)
}

// Retry handles a write that failed or was interrupted: the vendor may have received it even though Drydock recorded a
// failure (a time-out after the vendor accepted the call, the app stopping mid-send). For a comment Drydock first looks for it
// on the target (the comment-marker lookup); when it is there, the write is recorded as sent and nothing is sent again. Any
// other failed write, or a comment that is not there, is proposed again as the same call, which needs a new approval (the old
// approval was spent). Only a person retries.
func (s *Service) Retry(ctx context.Context, by, outboxID string) (Outbox, error) {
	if err := requireHuman(by); err != nil {
		return Outbox{}, err
	}
	o, err := s.st.GetOutbox(ctx, outboxID)
	if err != nil {
		return o, err
	}
	if o.Status != OutFailed {
		return o, errf(ErrBadRequest, "only a failed write can be retried; this one is %s", o.Status)
	}
	conn, err := s.st.GetConnection(ctx, o.IntegrationID)
	if err != nil {
		return o, err
	}
	ad, _ := AdapterFor(conn.Kind)
	if f, ok := ad.(commentFinder); ok && o.Marker != "" && o.Op == "comment" {
		api, fail := s.api(ctx, conn)
		if fail != nil {
			return o, s.scrubFailure(fail)
		}
		cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
		id, found, ferr := f.FindComment(cctx, api, o.Target, o.Marker, o.Created-5*60*1000)
		cancel()
		if ferr != nil {
			var fl *Failure
			if !errors.As(ferr, &fl) {
				fl = Classify(conn.Kind, ferr, "")
			}
			return o, s.scrubFailure(fl)
		}
		if found {
			receipt, _ := truncateRunes("found on "+vendor(conn.Kind)+": comment "+s.scrub(id)+"; not sent again", maxReceiptRunes)
			empty := ""
			if ok, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutFailed}, To: OutSent, Receipt: &receipt, Error: &empty}); err != nil || !ok {
				return o, fmt.Errorf("integrations: could not record the comment found on %s: %v", vendor(conn.Kind), err)
			}
			s.emit(ctx, EventOutboundSent, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "op": o.Op, "hash": o.PayloadHash, "by": by, "found": true})
			return s.st.GetOutbox(ctx, o.ID)
		}
	}
	return s.repropose(ctx, by, o, conn)
}

// repropose stores the same call as a new proposal and asks for a new approval bound to the same hash.
func (s *Service) repropose(ctx context.Context, by string, o Outbox, conn Connection) (Outbox, error) {
	if s.o.Approvals == nil {
		return Outbox{}, errors.New("integrations: no approvals service is configured, so outbound writes are disabled")
	}
	row := Outbox{IntegrationID: o.IntegrationID, ProjectID: o.ProjectID, TaskID: o.TaskID, Op: o.Op, Target: o.Target, Tool: o.Tool, PayloadHash: o.PayloadHash, Preview: o.Preview,
		Status: OutPlanned, IdempotencyKey: o.IdempotencyKey, CreatedBy: by, Marker: o.Marker, Payload: o.Payload}
	row.rawPayload = o.rawPayload
	row, existing, err := s.st.InsertOutbox(ctx, row)
	if err != nil || existing {
		return row, err
	}
	desc, err := descriptorFor(row, conn.Kind)
	if err != nil {
		return row, err
	}
	reason := fmt.Sprintf("Retry of an outbound write to %s that failed: %s on %s. Proposed again by %s. Nothing is sent until you approve and the action is executed.", vendor(conn.Kind), o.Op, o.Target, by)
	apr, err := s.o.Approvals.Request(ctx, desc, reason)
	if err != nil {
		msg := s.scrub(err.Error())
		_, _ = s.st.UpdateOutbox(ctx, row.ID, OutboxUpdate{From: []string{OutPlanned}, To: OutCancelled, Error: &msg})
		return row, fmt.Errorf("integrations: could not request approval: %w", err)
	}
	if _, err := s.st.UpdateOutbox(ctx, row.ID, OutboxUpdate{From: []string{OutPlanned}, To: OutAwaiting, ApprovalID: &apr}); err != nil {
		return row, err
	}
	s.emit(ctx, EventOutboundRequested, o.ProjectID, o.TaskID, map[string]any{"outboxId": row.ID, "integrationId": o.IntegrationID, "op": o.Op, "approvalId": apr, "hash": o.PayloadHash, "by": by, "retryOf": o.ID})
	return s.st.GetOutbox(ctx, row.ID)
}

// RecoverInterrupted marks actions that were executing when the app stopped as failed. Their approval was spent and the write may
// or may not have reached the vendor, so Drydock never re-sends them by itself: the person checks the vendor and proposes the
// action again if it is still needed. It returns how many rows it changed. The core extension calls it at startup.
func (s *Service) RecoverInterrupted(ctx context.Context) int {
	rows, err := s.st.ListOutbox(ctx, "", OutExecuting, 200)
	if err != nil {
		return 0
	}
	n := 0
	for _, o := range rows {
		msg := "Drydock stopped while this action was being sent, so it is not known whether it reached " + vendorOf(s, ctx, o) + ". Check there, then propose it again if it is still needed."
		if ok, _ := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutExecuting}, To: OutFailed, Error: &msg}); ok {
			n++
			s.emit(ctx, EventOutboundFailed, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "code": "interrupted"})
		}
	}
	return n
}

func vendorOf(s *Service, ctx context.Context, o Outbox) string {
	if c, err := s.st.GetConnection(ctx, o.IntegrationID); err == nil {
		return vendor(c.Kind)
	}
	return "the vendor"
}

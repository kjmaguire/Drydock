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
// receive is inside it: the integration, the operation, the target, the tool and the arguments.
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
	call, err := ad.Build(r.Op, target, params, conn.Config)
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
	// pin the tool name now (the approval is bound to it): the server's live list when reachable, else the first candidate
	tool := ""
	for _, n := range ad.Needs() {
		if n.Role == call.Role {
			tool = n.Candidates[0]
			if cl, cerr := s.callerFor(ctx, conn, true); cerr == nil {
				tctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
				offered, terr := cl.Tools(tctx)
				cancel()
				if terr == nil {
					if resolved, _, _ := resolveTools([]ToolNeed{n}, offered); resolved[n.Role] != "" {
						tool = resolved[n.Role]
					} else {
						return Outbox{}, errf(ErrToolMissing, "%s's server does not offer %s, so this write is not possible", vendor(conn.Kind), strings.Join(n.Candidates, " or "))
					}
				}
			}
		}
	}
	if tool == "" {
		return Outbox{}, errf(ErrOpUnsupported, "no tool is known for %q", r.Op)
	}
	call.Tool = tool
	argsRaw, err := json.Marshal(call.Args)
	if err != nil {
		return Outbox{}, err
	}
	_, hash, err := canonicalCall(conn.ID, conn.Kind, r.Op, target, tool, argsRaw)
	if err != nil {
		return Outbox{}, err
	}
	project := r.ProjectID
	if project == "" {
		project = conn.ProjectID
	}
	row := Outbox{IntegrationID: conn.ID, ProjectID: project, TaskID: r.TaskID, Op: r.Op, Target: target, Tool: tool, PayloadHash: hash, Preview: s.scrub(call.Preview),
		Status: OutPlanned, IdempotencyKey: hashHex(conn.ID, r.TaskID, r.Op, target, hash), CreatedBy: by}
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
	return out, nil
}

func (s *Service) callerFor(ctx context.Context, c Connection, write bool) (Caller, error) {
	if s.o.Callers == nil {
		return nil, errors.New("no MCP gateway is configured")
	}
	return s.o.Callers.Caller(ctx, c, write)
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
	caller, cerr := s.callerFor(ctx, conn, true)
	var res Result
	var fail *Failure
	if cerr != nil {
		fail = Classify(conn.Kind, cerr, "")
		if errors.Is(cerr, ErrNoCredential) {
			fail = &Failure{Code: CodeUnauthorized, Message: "No token is stored for " + vendor(conn.Kind) + "."}
		}
	} else {
		cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
		res, cerr = caller.Call(cctx, o.Tool, o.Payload)
		cancel()
		switch {
		case cerr != nil:
			fail = Classify(conn.Kind, cerr, "")
		case res.IsError:
			fail = Classify(conn.Kind, nil, res.Text)
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
	receipt := s.scrub(res.Text)
	receipt, _ = truncateRunes(strings.Join(strings.Fields(receipt), " "), maxReceiptRunes)
	if _, err := s.st.UpdateOutbox(ctx, o.ID, OutboxUpdate{From: []string{OutExecuting}, To: OutSent, SentAt: &now, Receipt: &receipt}); err != nil {
		return o, err
	}
	s.emit(ctx, EventOutboundSent, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "op": o.Op, "hash": o.PayloadHash, "by": by, "auto": false})
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
		err = fmt.Errorf("%w: %v", ErrPayloadChanged, cause)
		s.emit(ctx, EventOutboundFailed, o.ProjectID, o.TaskID, map[string]any{"outboxId": o.ID, "integrationId": o.IntegrationID, "code": "payload_changed"})
	case errors.Is(cause, security.ErrNotApproved):
		set(OutAwaiting, "") // still waiting for a human; it can be executed after approval
		err = fmt.Errorf("%w: %v", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrRejected), errors.Is(cause, security.ErrCancelled):
		set(OutRejected, "the approval was rejected or cancelled")
		err = fmt.Errorf("%w: %v", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrApprovalExpired):
		set(OutExpired, "the approval expired; propose the action again")
		err = fmt.Errorf("%w: %v", ErrNotApproved, cause)
	case errors.Is(cause, security.ErrConsumed):
		set(OutFailed, "the approval was already used")
		err = fmt.Errorf("%w: %v", ErrNotApproved, cause)
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
// waits for an approval. Linear and Notion support comments; Sentry and Aikido do not (their MCP servers offer no comment tool).
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
	body := fmt.Sprintf("Drydock opened a pull request for this item: %s\n(task %s, action posted after human approval)", u, taskID)
	return s.Propose(ctx, by, ProposeReq{IntegrationID: conn.ID, Op: "comment", Target: it.ExternalID, TaskID: taskID, ProjectID: it.ProjectID, Params: map[string]string{"body": body}})
}


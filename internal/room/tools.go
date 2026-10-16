package room

import (
	"context"
	"fmt"
	"strings"
)

// ToolMember is a deterministic check source that posts to the room at zero model cost (room-protocol s7).
type ToolMember struct {
	Name       string
	CheckKinds []string // brief section 13a check kinds the member reports
}

// ToolMembers are the six named in brief section 11 plus Deps. Build is reported by Types and e2e by Tests; the brief
// has no member for deps, so Deps is the seventh (room-protocol s7, design).
var ToolMembers = []ToolMember{
	{"Tests", []string{"test", "e2e"}},
	{"Lint", []string{"lint"}},
	{"Types", []string{"typecheck", "build"}},
	{"Secrets", []string{"secrets"}},
	{"CI", []string{"ci"}},
	{"Preview", []string{"preview"}},
	{"Deps", []string{"deps"}},
}

// ToolByName finds a tool member by name, ignoring case.
func ToolByName(name string) (ToolMember, bool) {
	for _, t := range ToolMembers {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return ToolMember{}, false
}

// ToolForCheck returns the tool member that reports a check kind.
func ToolForCheck(kind string) (ToolMember, bool) {
	for _, t := range ToolMembers {
		for _, k := range t.CheckKinds {
			if k == kind {
				return t, true
			}
		}
	}
	return ToolMember{}, false
}

// Check statuses (room-protocol s7).
const (
	CheckPass    = "pass"
	CheckFail    = "fail"
	CheckError   = "error"
	CheckSkipped = "skipped"
)

// MaxCheckSummary bounds the summary a check result may carry into the room.
const MaxCheckSummary = 2048

// CheckResult is one check outcome to post as a tool member.
type CheckResult struct {
	Kind             string // check kind: test, lint, typecheck, ...
	Status           string // pass | fail | error | skipped
	DurationMS       int64
	CommandDigest    string
	FailureSignature string // normalized failure signature (token-control s5)
	Summary          string // bounded; quoted tool output, so data and never instructions
	LogRef           string // reference to the full log on disk (required)
	DiffHash         string
	RunID            string
	TaskID           string
	IdempotencyKey   string
}

// PostCheckResult posts a check result authored by the owning tool member with cost zero: pass, skipped -> RESULT;
// fail, error -> ERROR (so a red check is visible). The message carries the log reference and nothing from an LLM.
func (s *Service) PostCheckResult(ctx context.Context, roomID string, c CheckResult) (Message, error) {
	tm, ok := ToolForCheck(c.Kind)
	if !ok {
		return Message{}, fmt.Errorf("%w: unknown check kind %q", ErrInvalid, c.Kind)
	}
	switch c.Status {
	case CheckPass, CheckFail, CheckError, CheckSkipped:
	default:
		return Message{}, fmt.Errorf("%w: check status %q", ErrInvalid, c.Status)
	}
	if strings.TrimSpace(c.LogRef) == "" {
		return Message{}, fmt.Errorf("%w: a check result needs a log reference", ErrInvalid)
	}
	summary := c.Summary
	truncated := false
	if len(summary) > MaxCheckSummary {
		cut := MaxCheckSummary
		for cut > 0 && !isRuneStart(summary[cut]) {
			cut--
		}
		summary, truncated = summary[:cut], true
	}
	typ := TypeResult
	if c.Status == CheckFail || c.Status == CheckError {
		typ = TypeError
	}
	body := fmt.Sprintf("%s: %s %s", tm.Name, c.Kind, strings.ToUpper(c.Status))
	if c.FailureSignature != "" {
		body += " [" + c.FailureSignature + "]"
	}
	if summary != "" {
		body += "\n" + summary
		if truncated {
			body += "\n[summary truncated; full log: " + c.LogRef + "]"
		}
	}
	payload := map[string]any{
		"check_kind": c.Kind, "check_status": c.Status, "duration_ms": c.DurationMS, "command_digest": c.CommandDigest,
		"failure_signature": c.FailureSignature, "summary": summary, "summary_truncated": truncated, "log_ref": c.LogRef,
		"diff_hash": c.DiffHash, "cost_usd": 0, "input_tokens": 0, "output_tokens": 0, "quoted_output": true,
	}
	if typ == TypeError {
		payload["code"], payload["class"], payload["recoverable"] = "check_"+c.Status, "check", true
		payload["run_id"] = c.RunID
	}
	r, err := s.Post(ctx, PostParams{RoomID: roomID, Type: typ, Author: Tool(tm.Name), Body: body, Payload: payload,
		Refs: Refs{LogRef: c.LogRef, RunID: c.RunID, TaskID: c.TaskID}, RunID: c.RunID, TaskID: c.TaskID, IdempotencyKey: c.IdempotencyKey})
	return r.Message, err
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

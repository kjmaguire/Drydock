package core

import (
	"context"
	"fmt"

	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/security"
)

// Permission modes that let an agent act without asking (audit 2026-10-16, M1; docs/security.md s4).
//
// bypassPermissions, auto and dontAsk take the approval step out of an agent's work. The docs promise that choosing one needs a
// person's approval of kind permission_mode_escalation and shows a persistent red badge; the kind existed and nothing used it, and
// the template, crew and catalog paths set the mode with no question at all. Every way an agent or a template gets such a mode
// now goes through confirmSkip: the request must carry the person's explicit confirmation (confirmSkipsApprovals), which the UI
// sends only after its warning, and the confirmation is recorded as an approval of that kind, decided by the person and used up
// at once, so the approvals log and the audit log say who allowed it, for which agent and when. A request without the
// confirmation is refused with NeedsConfirmationError.

// NeedsConfirmationError is returned when a permission mode that skips approvals is set without the person's confirmation.
type NeedsConfirmationError struct {
	Mode string
	What string
}

func (e *NeedsConfirmationError) Error() string {
	return fmt.Sprintf("needs your confirmation: the permission mode %q lets %s act without asking you first. Confirm it (confirmSkipsApprovals) to use it", e.Mode, e.What)
}

// confirmSkip returns nil when mode keeps approvals, or when the person confirmed a mode that skips them (and records that as an
// approved permission_mode_escalation approval for the agent, "" for a template). what names the subject for the message.
func (c *Core) confirmSkip(ctx context.Context, project, agentID, what, mode string, confirmed bool) error {
	if !chats.SkipsApprovals(mode) {
		return nil
	}
	if !confirmed {
		return &NeedsConfirmationError{Mode: mode, What: what}
	}
	ap := security.NewApprovals(c.DB, c.Bus, security.NewAudit(c.DB, c.Scanner), security.WithApprovalRedactor(c.Scanner))
	action := security.ActionDescriptor{Kind: security.KindPermissionEscalation, Project: project, AgentID: agentID, Tool: "agent.permissionMode", Command: mode}
	id, err := ap.Request(ctx, action, "The person chose a permission mode that lets "+what+" act without asking: "+mode+".",
		security.WithTitle("Permission mode "+mode+" for "+what))
	if err != nil {
		return err
	}
	if err := ap.Decide(ctx, id, security.DecisionApprove, c.Human()); err != nil {
		return err
	}
	return ap.Consume(ctx, id, action)
}

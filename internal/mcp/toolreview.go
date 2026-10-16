package mcp

import (
	"context"
	"encoding/json"
)

// Tools a person reviewed (audit 2026-10-16, M2; decision P37).
//
// What a model reads of a tool is its name, its description and its input schema, and that text is the server's to write. When a
// person lists a server's tools Drydock scores that text for instructions aimed at the model and marks the tool; a grant by name
// leaves a marked tool out. That is the review. It held only while the grant named tools and the server did not change: a grant of
// "*", a tool the server adds later and a description it rewrites after the review all reached the model unread. Each run now
// checks what the server offers against what was reviewed, which the spec carries (ToolDecl.Digest and Flagged):
//
//   - a tool whose text differs from the reviewed text is withheld until the person lists the tools again and looks;
//   - a tool the review marked is withheld unless the grant names it, which is a person's decision;
//   - a tool that was not in the reviewed list is withheld unless the grant names it.
//
// A server whose spec carries no review is not affected: a catalog entry only declares the tools it offers (so a run can list them
// without starting the server), and a library server listed before reviews were kept has no fingerprints (listing again adds them).

// EventToolWithheld is published when a run keeps a tool away from the model, and why.
const EventToolWithheld = "mcp.tool_withheld"

// hasReview reports whether any declared tool carries what a person reviewed.
func (s *ServerSpec) hasReview() bool {
	for _, t := range s.Tools {
		if t.Digest != "" || t.Flagged {
			return true
		}
	}
	return false
}

// ToolDigest fingerprints what a model reads of a tool: its name, description and input schema.
func ToolDigest(name, description string, schema any) string {
	raw, _ := json.Marshal(map[string]any{"name": name, "description": description, "inputSchema": schema})
	canon, err := canonicalJSON(raw)
	if err != nil {
		canon = raw
	}
	return sha256Hex(canon)
}

// NamesTool reports whether the policy lists the tool of the server by its own name, not through a pattern: a person chose it.
func (p RunPolicy) NamesTool(server, tool string) bool {
	for _, g := range p.Allow[server] {
		if g == tool {
			return true
		}
	}
	return false
}

// reviewed returns "" when the tool may reach the model, else why not. desc and schema are what the server offers now; a caller
// that has only the name (a call) passes known=false and only the marks and the list are checked.
func (r *Run) reviewed(spec *ServerSpec, name, desc string, schema any, known bool) string {
	if !spec.hasReview() {
		return ""
	}
	var decl *ToolDecl
	for i := range spec.Tools {
		if spec.Tools[i].Name == name {
			decl = &spec.Tools[i]
			break
		}
	}
	named := r.cfg.Policy.NamesTool(spec.ID, name)
	switch {
	case decl == nil:
		if named {
			return ""
		}
		return "it was not in the list of tools you looked at"
	case known && decl.Digest != "" && ToolDigest(name, desc, schema) != decl.Digest:
		return "its description or inputs changed since you looked at the server's tools; list them again to review the change"
	case decl.Flagged && !named:
		return "its description reads like instructions to the model; name it in the agent's grant if you want it used"
	}
	return ""
}

// withhold records once per run that a tool was kept from the model.
func (r *Run) withhold(ctx context.Context, server, tool, why string) {
	key := server + "\x00" + tool + "\x00" + why
	if _, seen := r.withheld.LoadOrStore(key, true); seen {
		return
	}
	r.g.publish(ctx, EventToolWithheld, r.cfg.Info, "", map[string]any{"server": server, "tool": tool, "reason": why})
}

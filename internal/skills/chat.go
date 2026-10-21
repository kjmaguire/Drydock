package skills

import (
	"context"
	"strings"
)

// Decision P60: a chat takes skills straight from the workspace library. Choosing a skill for a chat is the person's decision
// for that chat (as adding one to a project is for the project, P48); nothing is copied into a project. The chat's CLI gets an
// index of the chosen skills in its instructions and reads a skill's text through the skills tool of Drydock's MCP gateway
// when the work needs it: the on-demand delivery of P37, without writing files into a folder the person owns.

// LibraryGet returns one library skill and its text.
func (s *Service) LibraryGet(ctx context.Context, registryID, name string) (LibrarySkill, string, error) {
	k, err := s.libraryRow(ctx, registryID, name)
	if err != nil {
		return k, "", err
	}
	return k, k.body, nil
}

// Usable reports whether a library skill may reach an agent: available (not blocked) and still in its registry.
func (k LibrarySkill) Usable() bool { return k.Status == LibraryAvailable && !k.Gone }

// ChatToolName is the skills tool as Claude Code names it in a chat session (the gateway's single "drydock" entry).
const ChatToolName = "mcp__drydock__skills"

// ChatIndex is what a chat's turn adds to the CLI's instructions for the skills chosen for the chat: one line each, with what
// it is for, and how to read it. Empty when there are none.
func ChatIndex(ks []LibrarySkill) string {
	if len(ks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills chosen for this chat\n\nThe person chose these skills for this chat. They are not loaded into your instructions: " +
		"each line names a skill and says what it is for. Before you work on something a skill covers, read it with the skills tool " +
		"of the drydock MCP server (" + ChatToolName + ", action \"get\", name as below) and follow it; skip the ones that have " +
		"nothing to do with the request.\n")
	for _, k := range ks {
		d := oneLineText(k.Description)
		if d == "" {
			d = "no description"
		}
		if len(d) > 300 {
			d = d[:300] + "..."
		}
		if len(k.Globs) > 0 {
			d += " (for " + strings.Join(k.Globs, ", ") + ")"
		}
		b.WriteString("\n- **" + k.Name + "**: " + d)
	}
	return b.String()
}

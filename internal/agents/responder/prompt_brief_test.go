package responder

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/room"
)

// The project brief reaches a room reply after the rules and before the conversation, bounded.
func TestBuildPromptCarriesTheBrief(t *testing.T) {
	in := PromptInput{Agent: Agent{ID: "a1", Name: "Ada", Role: "backend"}, Room: room.Room{Name: "general", Kind: "channel"}, Trigger: room.Message{ID: "m1"}}
	if strings.Contains(BuildPrompt(in), "Project brief") {
		t.Fatal("no brief, no section")
	}
	in.Brief = "A storefront on Workers. " + strings.Repeat("x", 3000)
	p := BuildPrompt(in)
	i, j := strings.Index(p, "Project brief"), strings.Index(p, "Members of this room")
	if i < 0 || j < i || strings.Index(p, "Rules:") > i {
		t.Fatalf("brief placement:\n%s", p)
	}
	if strings.Count(p, "x") > maxBriefPrompt+50 || !strings.Contains(p, "[...]") {
		t.Fatal("the brief must be bounded")
	}
}

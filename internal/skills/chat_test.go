package skills

import (
	"strings"
	"testing"
)

// Decision P60: a chat's skills reach its CLI as an index in the instructions; the text stays behind the skills tool.
func TestChatIndexListsTheChosenSkillsAndHowToReadThem(t *testing.T) {
	if ChatIndex(nil) != "" {
		t.Fatal("no skills, no index")
	}
	long := strings.Repeat("x", 400)
	got := ChatIndex([]LibrarySkill{
		{Name: "wrangler", Description: "Wrangler\nrules.", Globs: []string{"wrangler.*"}, body: "SECRET-BODY"},
		{Name: "empty"},
		{Name: "long", Description: long},
	})
	for _, want := range []string{"# Skills chosen for this chat", ChatToolName, `action "get"`, "- **wrangler**: Wrangler rules. (for wrangler.*)", "- **empty**: no description", strings.Repeat("x", 300) + "..."} {
		if !strings.Contains(got, want) {
			t.Fatalf("index lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SECRET-BODY") || strings.Contains(got, strings.Repeat("x", 301)) {
		t.Fatalf("the index carries no skill text and cuts long descriptions:\n%s", got)
	}
	if !(LibrarySkill{Status: LibraryAvailable}).Usable() || (LibrarySkill{Status: LibraryBlocked}).Usable() || (LibrarySkill{Status: LibraryAvailable, Gone: true}).Usable() {
		t.Fatal("only an available skill still in its registry is usable")
	}
}

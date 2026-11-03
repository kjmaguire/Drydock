package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/skills"
)

// E5: a project's repository skills are found when the project gets its repository, without the Scan button; the skills that
// wait for the person reach #alerts and the Inbox once, and skill.refresh scans again only after 30 seconds.
func TestRepositorySkillsAreFoundWhenTheProjectIsMadeAndOfferedOnce(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, ".aet", "skills", "go-style"), 0o755)
	os.WriteFile(filepath.Join(repo, ".aet", "skills", "go-style", "SKILL.md"), []byte("---\nname: go-style\ndescription: Go style\n---\nKeep functions small.\n"), 0o644)
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)

	var list []skills.Skill
	a.call("skill.list", map[string]string{"projectId": p.ID}, &list)
	if len(list) != 1 || list[0].Name != "go-style" || list[0].Status != skills.StatusOffered {
		t.Fatalf("the skill was not found when the project was made: %+v", list)
	}
	var evs []struct{ Type string }
	a.call("events.replay", map[string]any{"after": 0, "types": []string{core.EventSkillScanned}}, &evs)
	if len(evs) != 1 {
		t.Fatalf("skill.scanned: %+v", evs)
	}
	alerts := func() []room.Message {
		var rooms []room.Room
		a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
		for _, r := range rooms {
			if r.Name == "alerts" {
				var ms []room.Message
				a.call("room.messages", map[string]any{"roomId": r.ID, "limit": 200}, &ms)
				return ms
			}
		}
		t.Fatal("no #alerts")
		return nil
	}
	count := func() int {
		n := 0
		for _, m := range alerts() {
			if strings.Contains(m.Body, "1 skill in the repository waits for your decision") {
				n++
			}
		}
		return n
	}
	if n := count(); n != 2 { // the notice and its Needs-you item
		t.Fatalf("the offer reached #alerts %d times: %+v", n, alerts())
	}
	var needs []struct {
		Title string `json:"title"`
	}
	a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
	found := false
	for _, n := range needs {
		found = found || strings.Contains(n.Title, "skill in the repository waits")
	}
	if !found {
		t.Fatalf("no Inbox item: %+v", needs)
	}

	// the same waiting skill is not told again; a refresh right after a scan does nothing
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	var scanned bool
	a.call("skill.refresh", map[string]string{"projectId": p.ID}, &scanned)
	if scanned || count() != 2 {
		t.Fatalf("refreshed %v, told %d times", scanned, count())
	}
}

// E5 review: deciding on one of several waiting skills does not tell the others again; only a new skill (or a changed one) is
// told, and the notice names only it.
func TestRepositorySkillsDecidingOneDoesNotTellTheRestAgain(t *testing.T) {
	a := open(t, "")
	repo := newRepo(t)
	write := func(name, body string) {
		os.MkdirAll(filepath.Join(repo, ".aet", "skills", name), 0o755)
		os.WriteFile(filepath.Join(repo, ".aet", "skills", name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+name+"\n---\n"+body+"\n"), 0o644)
	}
	write("alpha", "Do alpha.")
	write("beta", "Do beta.")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	notices := func() []string {
		var rooms []room.Room
		a.call("room.list", map[string]any{"projectId": p.ID}, &rooms)
		var out []string
		for _, r := range rooms {
			if r.Name == "alerts" {
				var ms []room.Message
				a.call("room.messages", map[string]any{"roomId": r.ID, "limit": 200}, &ms)
				for _, m := range ms {
					if m.Type == room.TypeSystem && strings.Contains(m.Body, "in the repository") {
						out = append(out, m.Body)
					}
				}
			}
		}
		return out
	}
	if n := notices(); len(n) != 1 || !strings.HasPrefix(n[0], "2 skills in the repository wait") {
		t.Fatalf("first notice: %q", n)
	}
	a.call("skill.accept", map[string]string{"projectId": p.ID, "name": "alpha"}, nil)
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	if n := notices(); len(n) != 1 {
		t.Fatalf("deciding on alpha told beta again: %q", n)
	}
	write("gamma", "Do gamma.")
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	n := notices()
	if len(n) != 2 || !strings.Contains(n[1], "1 skill in the repository waits") || !strings.Contains(n[1], "gamma") || strings.Contains(n[1], "beta") {
		t.Fatalf("a new skill is told alone: %q", n)
	}
	write("beta", "Do beta differently.")
	a.call("skill.scan", map[string]string{"projectId": p.ID}, nil)
	if n := notices(); len(n) != 3 || !strings.Contains(n[2], "beta") || strings.Contains(n[2], "gamma") {
		t.Fatalf("a changed skill is told again, alone: %q", n)
	}
}

package skills

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Decision P48: a project chooses from the library, and choosing is accepting. Decision P76: the skill goes to agents, the
// ones whose role it names unless the person chose others.
func TestAddingFromTheLibraryAcceptsTheSkillAndRefusesWhatCannotBeAdded(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, map[string]string{SkillsDir + "/workers-style/SKILL.md": skillMD("workers-style", "ours", "", "Our own rule.")})
	e.addAgent("agt_be", "backend")
	e.addAgent("agt_fe", "frontend")
	if _, err := e.s.ScanRepo(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Assign(ctx, human, "p1", "workers-style", "agt_be", true); err != nil {
		t.Fatal(err)
	}
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.s.AddFromLibrary(ctx, "agent_1", "p1", "cloudflare-skills", "d1-migrations", "", nil); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("an agent added: %v", err)
	}
	k, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != StatusAccepted || k.Origin != OriginRegistry || k.RegistryID != "cloudflare-skills" || k.PinnedCommit != sha1 || k.DecidedBy != human || k.Audience != AudienceAgents || strings.Join(k.Agents, ",") != "agt_be" ||
		strings.Join(k.Globs, ",") != "migrations/**/*.sql" || k.License != "Apache-2.0" || !k.HasScripts {
		t.Fatalf("%+v", k)
	}
	if got, _ := e.s.Get(ctx, "p1", "d1-migrations"); !strings.Contains(got.Body, d1Skill) {
		t.Fatal("the project copy lacks the text")
	}
	if len(e.events("skill.accepted")) != 1 || len(e.events("skill.imported")) != 1 {
		t.Fatalf("events: accepted %d imported %d", len(e.events("skill.accepted")), len(e.events("skill.imported")))
	}
	// adding again at the same text changes nothing
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil || len(e.events("skill.accepted")) != 1 {
		t.Fatalf("%v, accepted events %d", err, len(e.events("skill.accepted")))
	}
	// what cannot be added says why
	for name, want := range map[string]string{"trap": "cannot be added", "broken": "cannot be added", "workers-style": "repository's own skill wins", "nope": "no skill"} {
		if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", name, "", nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// a rejected skill the person adds again is accepted again: the add is a new decision
	if _, err := e.s.Reject(ctx, human, "p1", "d1-migrations"); err != nil {
		t.Fatal(err)
	}
	if k, err = e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil || k.Status != StatusAccepted {
		t.Fatalf("%v %+v", err, k)
	}
	// every accepted skill given to the agent reaches its plan; the blocked ones never entered the project
	el, _ := e.s.Eligible(ctx, "p1", "agt_be", "backend")
	var names []string
	for _, x := range el {
		names = append(names, x.Name)
	}
	if strings.Join(names, ",") != "d1-migrations,workers-style" {
		t.Fatalf("eligible: %v", names)
	}
	if el, _ := e.s.Eligible(ctx, "p1", "agt_fe", "frontend"); len(el) != 0 {
		t.Fatalf("the frontend agent got skills it was not given: %+v", el)
	}
	// the agents a person chooses replace the ones the roles suggested; the whole team is refused
	if k, err = e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", []string{"agt_fe"}); err != nil || strings.Join(k.Agents, ",") != "agt_fe" {
		t.Fatalf("%v %+v", err, k)
	}
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", AudienceTeam, nil); err == nil {
		t.Fatal("a library skill was added for the whole team")
	}
	// none chosen means nobody receives it
	k, err = e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", AudienceAgents, []string{})
	if err != nil || k.Audience != AudienceAgents || len(k.Agents) != 0 {
		t.Fatalf("%v %+v", err, k)
	}
	if el, _ = e.s.Eligible(ctx, "p1", "agt_be", "backend"); len(el) != 1 || el[0].Name != "workers-style" {
		t.Fatalf("eligible after taking it from everyone: %+v", el)
	}
}

func TestAddAllFromTheLibraryAddsEveryAvailableSkillOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, map[string]string{SkillsDir + "/workers-style/SKILL.md": skillMD("workers-style", "ours", "", "Our own rule.")})
	if _, err := e.s.ScanRepo(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(map[string]string{"skills/queues/SKILL.md": skillMD("queues", "Queues", "", "Ack.")}))
	e.useFixture(gh)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.AddAllFromLibrary(ctx, human, "p1", "cloudflare-skills")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Added, ",") != "d1-migrations,queues" || len(res.Unchanged) != 0 || len(res.Blocked) != 2 || len(res.Skipped) != 1 || res.Skipped[0].Name != "workers-style" {
		t.Fatalf("%+v", res)
	}
	for _, name := range []string{"d1-migrations", "queues"} {
		if k, _ := e.s.Get(ctx, "p1", name); k.Status != StatusAccepted {
			t.Fatalf("%s: %+v", name, k)
		}
	}
	res, err = e.s.AddAllFromLibrary(ctx, human, "p1", "cloudflare-skills")
	if err != nil || len(res.Added) != 0 || strings.Join(res.Unchanged, ",") != "d1-migrations,queues" {
		t.Fatalf("second run: %v %+v", err, res)
	}
	// a skill the registry dropped is skipped, not added
	tree := fixtureTree(nil)
	gh.publish("cloudflare/skills", "HEAD", sha2, tree)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "queues", "", nil); err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.AddAllFromLibrary(ctx, "agent_1", "p1", "cloudflare-skills"); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("%v", err)
	}
}

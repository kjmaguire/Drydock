package skills

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Decision P46: a registry can be imported whole over the network (the Skills page adds from the library since P48).

func TestImportAllOffersEveryValidSkillAndReportsTheRest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)

	if _, err := e.s.ImportAll(ctx, "agent_1", "p1", "cloudflare-skills", ""); !errors.Is(err, ErrNotHuman) {
		t.Fatalf("an agent imported: %v", err)
	}
	res, err := e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != sha1 || strings.Join(res.Imported, ",") != "d1-migrations,workers-style" || len(res.Updated) != 0 || len(res.Unchanged) != 0 {
		t.Fatalf("imported: %+v", res)
	}
	blocked := map[string]string{}
	for _, b := range res.Blocked {
		blocked[b.Name] = b.Reason
	}
	if len(blocked) != 2 || !strings.Contains(blocked["broken"], "not a valid skill") || blocked["trap"] == "" {
		t.Fatalf("the invalid skill and the injection are reported, not imported: %+v", res.Blocked)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped: %+v", res.Skipped)
	}
	// everything imported is offered, nothing accepted; the trap is stored blocked
	for name, want := range map[string]string{"d1-migrations": StatusOffered, "workers-style": StatusOffered, "trap": StatusBlocked} {
		k, err := e.s.Get(ctx, "p1", name)
		if err != nil || k.Status != want || k.PinnedCommit != sha1 || k.RegistryID != "cloudflare-skills" {
			t.Fatalf("%s: %v %+v", name, err, k)
		}
	}
	if _, err := e.s.Get(ctx, "p1", "broken"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an invalid skill was stored: %v", err)
	}
	if n := len(e.events("skill.offered")); n != 2 {
		t.Fatalf("offered events: %d", n)
	}

	// the same commit again: nothing changes, nothing is re-offered
	res, err = e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", "")
	if err != nil || len(res.Imported) != 0 || len(res.Updated) != 0 || strings.Join(res.Unchanged, ",") != "d1-migrations,workers-style" {
		t.Fatalf("second run: %v %+v", err, res)
	}

	// an accepted skill that changes upstream is offered again and reported as updated
	if _, err := e.s.Accept(ctx, human, "p1", "workers-style"); err != nil {
		t.Fatal(err)
	}
	gh.publish("cloudflare/skills", "HEAD", sha2, fixtureTree(map[string]string{
		"skills/workers-style/SKILL.md": skillMD("workers-style", "Workers style", metaLines("src/**/*.ts", ""), "Use modules syntax. Never use service-worker syntax."),
	}))
	res, err = e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", "")
	if err != nil || strings.Join(res.Updated, ",") != "workers-style" || strings.Join(res.Unchanged, ",") != "d1-migrations" {
		t.Fatalf("third run: %v %+v", err, res)
	}
	if k, _ := e.s.Get(ctx, "p1", "workers-style"); k.Status != StatusOffered || k.PinnedCommit != sha2 {
		t.Fatalf("a changed accepted skill is offered again: %+v", k)
	}
}

func TestImportAllKeepsTheRepositorysOwnSkillAndRefusesASelectiveRegistry(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, map[string]string{SkillsDir + "/workers-style/SKILL.md": skillMD("workers-style", "ours", "", "Our own rule.")})
	if _, err := e.s.ScanRepo(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	gh.publish("microsoft/skills", "HEAD", sha1, map[string]string{".github/skills/azure/SKILL.md": skillMD("azure", "Azure", "", "x")})
	e.useFixture(gh)

	res, err := e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Imported, ",") != "d1-migrations" || len(res.Skipped) != 1 || res.Skipped[0].Name != "workers-style" || !strings.Contains(res.Skipped[0].Reason, "repository's own skill wins") {
		t.Fatalf("%+v", res)
	}
	if k, _ := e.s.Get(ctx, "p1", "workers-style"); k.Origin != OriginRepo {
		t.Fatalf("the repository skill was replaced: %+v", k)
	}

	// Microsoft's repository asks for selective import: the whole-registry network import is refused with the reason
	_, err = e.s.ImportAll(ctx, human, "p1", "microsoft-skills", "")
	if err == nil || !strings.Contains(err.Error(), "one at a time") || !strings.Contains(err.Error(), "context rot") {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.Get(ctx, "p1", "azure"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a skill of the refused registry was stored")
	}
}

// Decision P48: the check syncs the library and says what it brought; a project's copies are compared to the library now.
func TestRegistryCheckSyncsTheLibraryAndSaysWhatIsNewOrBehind(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)

	// before any sync: statuses for every registry, nothing checked, an empty library
	sts, err := e.s.RegistryStatuses(ctx)
	if err != nil || len(sts) == 0 {
		t.Fatalf("%v %d", err, len(sts))
	}
	for _, st := range sts {
		if st.CheckedAt != nil || st.Skills != 0 {
			t.Fatalf("checked before any sync: %+v", st)
		}
	}
	st, err := e.s.CheckRegistry(ctx, "cloudflare-skills")
	if err != nil {
		t.Fatal(err)
	}
	// the first sync adds every nameable skill: two available, two blocked (the trap and the invalid one), none changed
	if st.LastCommit != sha1 || st.CheckedAt == nil || st.LastError != "" || st.Skills != 4 || st.Blocked != 2 || len(st.Changed) != 0 || len(st.Projects) != 0 {
		t.Fatalf("%+v", st)
	}
	if n := names(st.Added); n != "broken,d1-migrations,trap,workers-style" {
		t.Fatalf("added: %s", n)
	}
	lib, err := e.s.LibraryList(ctx, "cloudflare-skills")
	if err != nil || len(lib) != 4 {
		t.Fatalf("%v %+v", err, lib)
	}
	byName := map[string]LibrarySkill{}
	for _, l := range lib {
		byName[l.Name] = l
	}
	if byName["d1-migrations"].Status != LibraryAvailable || byName["d1-migrations"].License != "Apache-2.0" || !byName["d1-migrations"].HasScripts || byName["d1-migrations"].Repo != "cloudflare/skills" {
		t.Fatalf("%+v", byName["d1-migrations"])
	}
	if byName["trap"].Status != LibraryBlocked || byName["trap"].BlockReason == "" || byName["broken"].Status != LibraryBlocked || !strings.Contains(byName["broken"].BlockReason, "not a valid skill") {
		t.Fatalf("blocked: %+v %+v", byName["trap"], byName["broken"])
	}
	if n := len(e.events(EventLibrarySynced)); n != 1 {
		t.Fatalf("synced events: %d", n)
	}
	tarHits := gh.hits["/cloudflare/skills/tar.gz/"+sha1]

	// the same commit again: no download, the news of the last sync stays
	st, err = e.s.CheckRegistry(ctx, "cloudflare-skills")
	if err != nil || gh.hits["/cloudflare/skills/tar.gz/"+sha1] != tarHits || len(st.Added) != 4 {
		t.Fatalf("an unchanged registry was downloaded again, or the news was lost: %v %+v", err, st)
	}

	// a project takes two skills from the library
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "workers-style", "", nil); err != nil {
		t.Fatal(err)
	}

	// upstream moves: one skill changes, one is added, one is unchanged, one disappears
	tree := fixtureTree(map[string]string{
		"skills/d1-migrations/SKILL.md": skillMD("d1-migrations", "D1 migration rules", metaLines("migrations/**/*.sql", "Database,Backend"), d1Skill+"\nAlways name migrations."),
		"skills/queues/SKILL.md":        skillMD("queues", "Queues consumer rules", "", "Ack explicitly."),
	})
	delete(tree, "skills/trap/SKILL.md")
	gh.publish("cloudflare/skills", "HEAD", sha2, tree)
	sts, err = e.s.CheckAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st = byRegistry(sts, "cloudflare-skills")
	if st.LastCommit != sha2 || names(st.Added) != "queues" || names(st.Changed) != "d1-migrations" || st.Skills != 4 || st.Blocked != 1 {
		t.Fatalf("%+v", st)
	}
	if up := st.Projects["p1"].Updates; len(up) != 1 || up[0].Name != "d1-migrations" {
		t.Fatalf("the project's copy is behind: %+v", st.Projects)
	}
	if st.NotifiedCommit != "" || !st.HasNews() {
		t.Fatalf("not told yet: %+v", st)
	}
	lib, _ = e.s.LibraryList(ctx, "cloudflare-skills")
	gone := 0
	for _, l := range lib {
		if l.Gone {
			gone++
			if l.Name != "trap" {
				t.Fatalf("gone: %+v", l)
			}
		}
	}
	if gone != 1 {
		t.Fatalf("the skill the registry dropped is marked gone: %+v", lib)
	}
	// the project copy did not move by itself; updating it from the library is the person's act
	if k, _ := e.s.Get(ctx, "p1", "d1-migrations"); k.PinnedCommit != sha1 || k.Status != StatusAccepted {
		t.Fatalf("a sync changed a project copy: %+v", k)
	}
	if err := e.s.MarkNotified(ctx, "cloudflare-skills", sha2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.AddFromLibrary(ctx, human, "p1", "cloudflare-skills", "d1-migrations", "", nil); err != nil {
		t.Fatal(err)
	}
	sts, _ = e.s.RegistryStatuses(ctx)
	st = byRegistry(sts, "cloudflare-skills")
	if len(st.Projects["p1"].Updates) != 0 || st.NotifiedCommit != sha2 {
		t.Fatalf("after the update: %+v", st)
	}
	if k, _ := e.s.Get(ctx, "p1", "d1-migrations"); k.PinnedCommit != sha2 || k.Status != StatusAccepted {
		t.Fatalf("updated copy: %+v", k)
	}

	// a disabled registry is not synced
	if err := e.s.SetRegistryEnabled(ctx, human, "cloudflare-skills", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("%v", err)
	}
	sts, _ = e.s.CheckAll(ctx)
	if byRegistry(sts, "cloudflare-skills").RegistryID != "" {
		t.Fatal("a disabled registry was checked")
	}
}

func TestRegistryCheckRecordsAFailureAndKeepsTheLibrary(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	// the registry becomes unreachable: the failure is in the status, the library and the commit stay
	gh.mu.Lock()
	delete(gh.commits, "cloudflare/skills@HEAD")
	gh.mu.Unlock()
	st, err := e.s.CheckRegistry(ctx, "cloudflare-skills")
	if err != nil {
		t.Fatalf("a failed check is a status, not an error: %v", err)
	}
	if st.LastError == "" || st.LastCommit != sha1 || st.CheckedAt == nil || st.Skills != 4 {
		t.Fatalf("%+v", st)
	}
	// and it clears when the registry answers again
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	if st, err = e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil || st.LastError != "" {
		t.Fatalf("%v %+v", err, st)
	}
}

func byRegistry(sts []RegistryStatus, id string) RegistryStatus {
	for _, st := range sts {
		if st.RegistryID == id {
			return st
		}
	}
	return RegistryStatus{}
}

func names(ch []RegistryChange) string {
	var out []string
	for _, c := range ch {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

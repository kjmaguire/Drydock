package skills

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Decision P46: a registry is imported whole (every valid skill offered, a person still accepts each) and watched afterwards.

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

	// Microsoft's repository asks for selective import: the whole-registry import is refused with the reason
	_, err = e.s.ImportAll(ctx, human, "p1", "microsoft-skills", "")
	if err == nil || !strings.Contains(err.Error(), "one at a time") || !strings.Contains(err.Error(), "context rot") {
		t.Fatalf("%v", err)
	}
	if _, err := e.s.Get(ctx, "p1", "azure"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a skill of the refused registry was stored")
	}
}

func TestRegistryCheckTellsWhatIsNewOrChangedForTheProjectNow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)

	// before any import nothing is watched
	sts, err := e.s.RegistryStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range sts {
		if st.Watched || st.CheckedAt != nil {
			t.Fatalf("watched before any import: %+v", st)
		}
	}
	if sts2, err := e.s.CheckWatched(ctx); err != nil || len(sts2) != 0 {
		t.Fatalf("the scheduled pass checks only registries a project imports from: %v %+v", err, sts2)
	}

	if _, err := e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", ""); err != nil {
		t.Fatal(err)
	}
	st, err := e.s.CheckRegistry(ctx, "cloudflare-skills")
	if err != nil {
		t.Fatal(err)
	}
	ch := st.Projects["p1"]
	if !st.Watched || st.LastCommit != sha1 || st.CheckedAt == nil || st.LastError != "" || len(ch.Additions) != 0 || len(ch.Updates) != 0 {
		t.Fatalf("right after importing everything there is nothing new: %+v", st)
	}
	tarHits := gh.hits["/cloudflare/skills/tar.gz/"+sha1]

	// the same commit again: no tarball is fetched
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	if gh.hits["/cloudflare/skills/tar.gz/"+sha1] != tarHits {
		t.Fatal("an unchanged registry was downloaded again")
	}

	// upstream moves: one skill changes, one is added, one is unchanged
	gh.publish("cloudflare/skills", "HEAD", sha2, fixtureTree(map[string]string{
		"skills/d1-migrations/SKILL.md": skillMD("d1-migrations", "D1 migration rules", metaLines("migrations/**/*.sql", "Database,Backend"), d1Skill+"\nAlways name migrations."),
		"skills/queues/SKILL.md":        skillMD("queues", "Queues consumer rules", "", "Ack explicitly."),
	}))
	sts, err = e.s.CheckWatched(ctx)
	if err != nil || len(sts) != 1 {
		t.Fatalf("%v %+v", err, sts)
	}
	ch = sts[0].Projects["p1"]
	if sts[0].LastCommit != sha2 || len(ch.Additions) != 1 || ch.Additions[0].Name != "queues" || ch.Additions[0].Description != "Queues consumer rules" ||
		len(ch.Updates) != 1 || ch.Updates[0].Name != "d1-migrations" {
		t.Fatalf("%+v", sts[0])
	}
	if sts[0].NotifiedCommit != "" || !sts[0].HasChanges() {
		t.Fatalf("not told yet: %+v", sts[0])
	}
	if err := e.s.MarkNotified(ctx, "cloudflare-skills", sha2); err != nil {
		t.Fatal(err)
	}

	// the status follows the project: importing the addition removes it, importing the update removes that
	if _, err := e.s.Import(ctx, human, ImportReq{ProjectID: "p1", RegistryID: "cloudflare-skills", Name: "queues"}); err != nil {
		t.Fatal(err)
	}
	sts, _ = e.s.RegistryStatuses(ctx)
	st = byRegistry(sts, "cloudflare-skills")
	if ch = st.Projects["p1"]; len(ch.Additions) != 0 || len(ch.Updates) != 1 || st.NotifiedCommit != sha2 {
		t.Fatalf("after importing the addition: %+v", st)
	}
	if _, err := e.s.Import(ctx, human, ImportReq{ProjectID: "p1", RegistryID: "cloudflare-skills", Name: "d1-migrations"}); err != nil {
		t.Fatal(err)
	}
	sts, _ = e.s.RegistryStatuses(ctx)
	if ch = byRegistry(sts, "cloudflare-skills").Projects["p1"]; len(ch.Additions) != 0 || len(ch.Updates) != 0 {
		t.Fatalf("after importing the update: %+v", ch)
	}
	// a disabled registry is not watched and not checked
	if err := e.s.SetRegistryEnabled(ctx, human, "cloudflare-skills", false); err != nil {
		t.Fatal(err)
	}
	if sts, err := e.s.CheckWatched(ctx); err != nil || len(sts) != 0 {
		t.Fatalf("%v %+v", err, sts)
	}
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("%v", err)
	}
}

func TestRegistryCheckRecordsAFailureAndKeepsTheLastGoodSnapshot(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)
	if _, err := e.s.ImportAll(ctx, human, "p1", "cloudflare-skills", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CheckRegistry(ctx, "cloudflare-skills"); err != nil {
		t.Fatal(err)
	}
	// the registry becomes unreachable: the failure is in the status, the previous commit and snapshot stay
	gh.mu.Lock()
	delete(gh.commits, "cloudflare/skills@HEAD")
	gh.mu.Unlock()
	st, err := e.s.CheckRegistry(ctx, "cloudflare-skills")
	if err != nil {
		t.Fatalf("a failed check is a status, not an error: %v", err)
	}
	if st.LastError == "" || st.LastCommit != sha1 || st.CheckedAt == nil {
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

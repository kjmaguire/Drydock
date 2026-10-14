package skills

import (
	"context"
	"strings"
	"testing"
)

// An install made before 2026-10-14 stores the curated react-router registry (remix-run/react-router), its library rows and
// its watch state. At the next start EnsureRegistries brings the stored allowlist in line with registry.yaml: the Remix 3
// registry is there, react-router and everything that claimed to be its current skills are gone, a curated entry whose source
// moved forgets the old source's library, shipped notes reach the stored rows, and a human's choices (a disabled curated
// registry, a registry they added, a project's own copy of a skill) stay.
func TestEnsureRegistriesRetiresReactRouterAndRefreshesCuratedEntries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	db := e.db.Writer()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the state an older install left
	exec(`DELETE FROM skill_registries WHERE id='remix'`)
	exec(`INSERT INTO skill_registries(id,repo,root,license,trust,notes,enabled,curated,added_by,created_at) VALUES('react-router','remix-run/react-router','.agents/skills/react-router','MIT','vendor_official','old',1,1,'curated',1)`)
	exec(`UPDATE skill_registries SET root='old/', notes='old' WHERE id='stripe-ai'`)
	exec(`UPDATE skill_registries SET notes='old', enabled=0 WHERE id='msgraph-community'`)
	for _, l := range [][2]string{{"react-router", "react-router"}, {"stripe-ai", "stripe-old"}, {"cloudflare-skills", "wrangler"}} {
		exec(`INSERT INTO skill_library(id,registry_id,name,body,content_hash,status,created_at,updated_at) VALUES(?,?,?,'x','h','available',1,1)`, "lib-"+l[1], l[0], l[1])
		exec(`INSERT INTO skill_registry_watch(registry_id,last_commit) VALUES(?,?)`, l[0], sha1)
	}
	exec(`INSERT INTO skills(id,project_id,name,body,origin,registry_id,content_hash,status,created_at,updated_at) VALUES('sk1','p1','react-router','x','registry','react-router','h','accepted',1,1)`)
	if _, err := e.s.AddRegistry(ctx, human, Registry{ID: "mine", Repo: "me/skills", License: "MIT"}, true); err != nil {
		t.Fatal(err)
	}
	before := len(e.events(EventRegistryChanged))

	if err := e.s.EnsureRegistries(ctx); err != nil {
		t.Fatal(err)
	}
	regs, err := e.s.Registries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Registry{}
	for _, r := range regs {
		byID[r.ID] = r
	}
	if _, ok := byID["react-router"]; ok {
		t.Fatal("the retired react-router registry is removed")
	}
	if r := byID["remix"]; r.Repo != "remix-run/remix" || r.Root != ".agents/skills/remix" || !r.Enabled || !r.Curated || r.License != "MIT" {
		t.Fatalf("the Remix 3 registry is added: %+v", r)
	}
	if r := byID["stripe-ai"]; r.Root != "skills/" || r.Notes == "old" {
		t.Fatalf("a curated entry takes the shipped source and notes: %+v", r)
	}
	if r := byID["msgraph-community"]; r.Enabled || !strings.Contains(r.Notes, "112 MB") {
		t.Fatalf("shipped notes arrive and a human's disable stays: %+v", r)
	}
	if r, ok := byID["mine"]; !ok || r.Curated {
		t.Fatalf("a registry a human added stays: %+v", r)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := e.db.Reader().QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for id, want := range map[string]int{"react-router": 0, "stripe-ai": 0, "cloudflare-skills": 1} {
		if n := count(`SELECT COUNT(*) FROM skill_library WHERE registry_id=?`, id); n != want {
			t.Errorf("library rows of %s: %d, want %d", id, n, want)
		}
		if n := count(`SELECT COUNT(*) FROM skill_registry_watch WHERE registry_id=?`, id); n != want {
			t.Errorf("watch state of %s: %d, want %d", id, n, want)
		}
	}
	if n := count(`SELECT COUNT(*) FROM skills WHERE project_id='p1' AND registry_id='react-router'`); n != 1 {
		t.Fatal("a project's own copy of a skill is never touched")
	}
	if lib, err := e.s.LibraryList(ctx, ""); err != nil {
		t.Fatal(err)
	} else {
		for _, l := range lib {
			if l.RegistryID == "react-router" {
				t.Fatalf("no library skill of the retired registry is offered: %+v", l)
			}
		}
	}
	evs := e.events(EventRegistryChanged)[before:]
	actions := map[string]string{}
	for _, ev := range evs {
		d := evData(t, ev)
		actions[d["registry"].(string)] = d["action"].(string)
	}
	if len(evs) != 2 || actions["react-router"] != "retired" || actions["stripe-ai"] != "source_changed" {
		t.Fatalf("the changes are told: %v", actions)
	}
	// a second start changes nothing
	if err := e.s.EnsureRegistries(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.events(EventRegistryChanged)); n != before+2 {
		t.Fatalf("idempotent: %d events", n-before)
	}
}

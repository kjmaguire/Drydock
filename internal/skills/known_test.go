package skills

import (
	"strings"
	"testing"
)

// registry.yaml records what the skills Drydock names say about themselves at their pins (decision P43), so a page can show
// a skill's description, source and license before anything is fetched.
func TestKnownSkillsAreRecordedWithDescriptionSourceAndLicense(t *testing.T) {
	known, err := KnownSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(known) < 12 {
		t.Fatalf("%d skills recorded", len(known))
	}
	regs, _ := CuratedRegistries()
	repo := map[string]string{}
	for _, r := range regs {
		repo[r.ID] = r.Repo
	}
	for key, k := range known {
		if repo[k.Registry] == "" || len(k.Description) > MaxKnownDescription || strings.Contains(k.Description, "\n") || k.License == "" || !strings.HasPrefix(k.Path, "skills/") {
			t.Errorf("%s: %+v", key, k)
		}
	}
	fd := known[KnownSkillKey("anthropic-skills", "frontend-design", "2c5958ecac7cee2b583967a2c4981d0f4a75c579")]
	if fd.License != "Apache-2.0" || !strings.HasPrefix(fd.Description, "Guidance for distinctive, intentional visual design") {
		t.Fatalf("frontend-design: %+v", fd)
	}
	// the block scalar of Vercel's composition-patterns is one line here
	cp := known[KnownSkillKey("vercel-agent-skills", "composition-patterns", "1a2c79d4e6c4a2283dc2c614b6538cbe1ed5ca62")]
	if !strings.HasPrefix(cp.Description, "React composition patterns that scale.") {
		t.Fatalf("composition-patterns: %+v", cp)
	}
}

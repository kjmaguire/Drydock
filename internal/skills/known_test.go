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
	fd := known[KnownSkillKey("anthropic-skills", "frontend-design", "dbd4588f9e1033efb41dad4bef2f7947c8993d44")]
	if fd.License != "Apache-2.0" || !strings.HasPrefix(fd.Description, "Guidance for distinctive, intentional visual design") {
		t.Fatalf("frontend-design: %+v", fd)
	}
	// the block scalar of Vercel's composition-patterns is one line here
	cp := known[KnownSkillKey("vercel-agent-skills", "composition-patterns", "063bee94c3f4df8453406c830b0a7df0f2860278")]
	if !strings.HasPrefix(cp.Description, "React composition patterns that scale.") {
		t.Fatalf("composition-patterns: %+v", cp)
	}
}

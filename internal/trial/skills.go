package trial

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kjmaguire/drydock/internal/agentcatalog"
	"github.com/kjmaguire/drydock/internal/skills"
)

// FetchPrebuiltSkills writes the SKILL.md of every skill the prebuilt agents carry (internal/agentcatalog), fetched from its
// registry at the catalog's pinned commit, to dst/<name>/SKILL.md: the skills a person gets by installing the prebuilt
// agents' skills. It returns the names. The files are third-party text under their own licenses; the trial keeps them in
// its scratch directory and never commits them to this repository.
func FetchPrebuiltSkills(ctx context.Context, f skills.RegistryFetcher, dst string) ([]string, error) {
	entries, err := agentcatalog.Builtin()
	if err != nil {
		return nil, err
	}
	regs, err := skills.CuratedRegistries()
	if err != nil {
		return nil, err
	}
	byID := map[string]skills.Registry{}
	for _, r := range regs {
		byID[r.ID] = r
	}
	type key struct{ registry, ref string }
	want := map[key]map[string]bool{}
	for _, e := range entries {
		for _, s := range e.Skills {
			k := key{s.Registry, s.Ref}
			if want[k] == nil {
				want[k] = map[string]bool{}
			}
			want[k][s.Name] = true
		}
	}
	var names []string
	for k, ns := range want {
		reg, ok := byID[k.registry]
		if !ok {
			return nil, fmt.Errorf("the catalog names registry %s, which is not curated", k.registry)
		}
		remote, err := f.Fetch(ctx, reg.Repo, k.ref, reg.Root)
		if err != nil {
			return nil, err
		}
		for _, rs := range remote {
			if !ns[rs.Name] {
				continue
			}
			if err := os.MkdirAll(filepath.Join(dst, rs.Name), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dst, rs.Name, "SKILL.md"), rs.SkillMD, 0o644); err != nil {
				return nil, err
			}
			delete(ns, rs.Name)
			names = append(names, rs.Name)
		}
		for n := range ns {
			return nil, fmt.Errorf("%s has no skill %s at %s", reg.Repo, n, k.ref)
		}
	}
	sort.Strings(names)
	return names, nil
}

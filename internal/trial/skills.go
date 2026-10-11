package trial

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/kjmaguire/drydock/internal/agentcatalog"
	"github.com/kjmaguire/drydock/internal/skills"
)

// PrebuiltSkillRefs lists the skills the prebuilt agents carry (internal/agentcatalog), each once, sorted by name. Two agents
// may carry the same skill at the same pin; the same name at two different pins is refused, because a project can hold a
// skill name once and the trial would otherwise run with whichever the catalog lists first.
func PrebuiltSkillRefs() ([]agentcatalog.SkillRef, error) {
	entries, err := agentcatalog.Builtin()
	if err != nil {
		return nil, err
	}
	seen := map[string]agentcatalog.SkillRef{}
	var out []agentcatalog.SkillRef
	for _, e := range entries {
		for _, s := range e.Skills {
			if prev, ok := seen[s.Name]; ok {
				if prev.Registry != s.Registry || prev.Ref != s.Ref {
					return nil, fmt.Errorf("the catalog carries skill %s at two pins (%s@%s and %s@%s): the trial cannot import both", s.Name, prev.Registry, prev.Ref, s.Registry, s.Ref)
				}
				continue
			}
			seen[s.Name] = s
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CachingFetcher fetches each (repository, commit) once and serves later runs from memory: the runs of a trial import the
// same pinned skills many times. Concurrent runs wait only for the fetch of their own key; a failed fetch is not kept.
type CachingFetcher struct {
	Inner skills.RegistryFetcher
	mu    sync.Mutex
	cache map[string]*fetchEntry
}

type fetchEntry struct {
	once sync.Once
	rs   []skills.RemoteSkill
	err  error
}

// Resolve implements skills.RegistryFetcher.
func (f *CachingFetcher) Resolve(ctx context.Context, repo, ref string) (string, error) {
	return f.Inner.Resolve(ctx, repo, ref)
}

// Fetch implements skills.RegistryFetcher.
func (f *CachingFetcher) Fetch(ctx context.Context, repo, commit, root string) ([]skills.RemoteSkill, error) {
	key := repo + "@" + commit + ":" + root
	f.mu.Lock()
	if f.cache == nil {
		f.cache = map[string]*fetchEntry{}
	}
	e := f.cache[key]
	if e == nil {
		e = &fetchEntry{}
		f.cache[key] = e
	}
	f.mu.Unlock()
	e.once.Do(func() { e.rs, e.err = f.Inner.Fetch(ctx, repo, commit, root) })
	if e.err != nil {
		f.mu.Lock()
		if f.cache[key] == e {
			delete(f.cache, key)
		}
		f.mu.Unlock()
	}
	return e.rs, e.err
}

// FetchPrebuiltSkills writes the SKILL.md of every skill the prebuilt agents carry (internal/agentcatalog), fetched from its
// registry at the catalog's pinned commit, to dst/<name>/SKILL.md: the skills a person gets by installing the prebuilt
// agents' skills. It returns the names. The files are third-party text under their own licenses; the trial keeps them in
// its scratch directory and never commits them to this repository.
func FetchPrebuiltSkills(ctx context.Context, f skills.RegistryFetcher, dst string) ([]string, error) {
	refs, err := PrebuiltSkillRefs()
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
	type pin struct{ registry, ref string }
	want := map[pin]map[string]bool{}
	var pins []pin
	for _, r := range refs {
		k := pin{r.Registry, r.Ref}
		if want[k] == nil {
			want[k] = map[string]bool{}
			pins = append(pins, k)
		}
		want[k][r.Name] = true
	}
	var names []string
	for _, k := range pins {
		ns := want[k]
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

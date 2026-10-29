package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/inuse"
	"github.com/kjmaguire/drydock/internal/proc"
)

// The guard (docs/security.md s3, threat T3). Linked worktrees share the main repository's .git directory, so an agent working
// in a task's worktree can write the repository's own git configuration, hooks and info/attributes. A filter, diff or merge
// driver, an ssh command, a credential helper or an include planted there would run inside Drydock's own git calls, on the
// host and outside any sandbox. Every git call Drydock makes already ignores hooks and fsmonitor (hardening); the guard
// covers the rest: it records what the repository's configuration was when a person last trusted it, and a git call that
// could execute configured programs refuses to start while the configuration differs, until a person approves the change.
//
// The state is kept as one hash per item (a config key, a hook file, info/attributes), never the values: a config value can
// hold a token (a remote URL, http.extraHeader).

// ConfigChangedError is returned instead of running git when the repository's git configuration changed since a person last
// trusted it. Changed names the items (config keys, "hooks/<file>", "info/attributes"), never their values.
type ConfigChangedError struct {
	Root        string
	Changed     []string
	Fingerprint string // of the state approving would trust
}

func (e *ConfigChangedError) Error() string {
	return fmt.Sprintf("git: the git configuration of %s changed since it was last trusted (%s); a person must review and approve the change before Drydock runs git there again",
		e.Root, strings.Join(e.Changed, ", "))
}

// Gate makes the error an approval the workflow engine raises (workflow.GateError): approved, the node runs again once the
// change is trusted; rejected, the run stops.
func (e *ConfigChangedError) Gate() (gate, title string, payload map[string]any) {
	return GateRepoConfigChanged, "The repository's git configuration changed during the task (" + strings.Join(e.Changed, ", ") +
			"). A planted hook, filter or command there would run in Drydock's own git calls. Approve only if you made or expect this change.",
		map[string]any{"root": e.Root, "changed": e.Changed, "fingerprint": e.Fingerprint}
}

// GateRepoConfigChanged is the gate name of the approval a ConfigChangedError raises (the approval kind of docs/security.md s5).
const GateRepoConfigChanged = "repo_config_changed"

// RepoState is the repository's configuration as one hash per item. Equal states have equal fingerprints.
type RepoState map[string]string

// Fingerprint is a hash of the whole state.
func (s RepoState) Fingerprint() string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s\x00%s\x00", k, s[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// diff names the items that differ between two states.
func (s RepoState) diff(o RepoState) []string {
	var out []string
	for k, v := range s {
		if o[k] != v {
			out = append(out, k)
		}
	}
	for k := range o {
		if _, ok := s[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

type guard struct {
	mu        sync.Mutex
	commonDir string
	trusted   RepoState
	raw       string    // hash of the bytes current was computed from
	current   RepoState // the state those bytes parse to
}

// inertCommands are the git subcommands that read the object database or refs and cannot run a configured program (no
// filters, drivers, hooks, editors or network): they run even while the configuration is untrusted, so the app can still show
// a project whose configuration a person has not reviewed yet.
var inertCommands = map[string]bool{"rev-parse": true, "cat-file": true, "merge-base": true, "rev-list": true, "show-ref": true,
	"for-each-ref": true, "symbolic-ref": true, "patch-id": true}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// files lists the paths whose bytes make up the state: the repository's config, worktree configs, info/attributes and every
// file in hooks.
func (g *guard) files() []string {
	out := []string{filepath.Join(g.commonDir, "config"), filepath.Join(g.commonDir, "config.worktree"), filepath.Join(g.commonDir, "info", "attributes")}
	if m, _ := filepath.Glob(filepath.Join(g.commonDir, "worktrees", "*", "config.worktree")); len(m) > 0 {
		out = append(out, m...)
	}
	if ents, err := os.ReadDir(filepath.Join(g.commonDir, "hooks")); err == nil {
		for _, e := range ents {
			out = append(out, filepath.Join(g.commonDir, "hooks", e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// read returns the state now. The files are hashed on every call (they are small); git parses the config files only when
// their bytes changed.
func (g *guard) read(ctx context.Context) (RepoState, error) {
	type file struct {
		rel  string
		path string
		data []byte
	}
	var fs []file
	h := sha256.New()
	for _, p := range g.files() {
		st, err := os.Lstat(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(g.commonDir, p)
		rel = filepath.ToSlash(rel)
		var data []byte
		switch {
		case st.Mode()&os.ModeSymlink != 0:
			t, _ := os.Readlink(p)
			data = []byte("symlink:" + t)
		case st.IsDir():
			data = []byte("dir")
		default:
			// git replaces its config by renaming a lock file over it; on Windows a read at that moment is refused for a moment
			if err = inuse.Do(func() (e error) { data, e = os.ReadFile(p); return e }); err != nil {
				return nil, fmt.Errorf("git guard: read %s: %w", rel, err)
			}
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(data))
		h.Write(data)
		fs = append(fs, file{rel, p, data})
	}
	raw := hex.EncodeToString(h.Sum(nil))
	if raw == g.raw && g.current != nil {
		return g.current, nil
	}
	st := RepoState{}
	for _, f := range fs {
		if strings.HasPrefix(f.rel, "hooks/") || f.rel == "info/attributes" {
			st[f.rel] = sha(f.data)
			continue
		}
		entries, err := configEntries(ctx, f.path)
		if err != nil {
			return nil, err
		}
		for k, v := range entries {
			st[k] = v
		}
	}
	g.raw, g.current = raw, st
	return st, nil
}

// configEntries parses one config file with git itself (an own parser could be fooled by a file git reads differently) and
// returns one hash per key. Keys git writes for its own branch bookkeeping (`push --set-upstream`, `branch -D`) are left out,
// except mergeOptions, which changes how a merge runs.
func configEntries(ctx context.Context, path string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--file", path, "--null", "--list")
	cmd.Dir = filepath.Dir(path)
	if isDistroPath(path) {
		// a repository inside a WSL distro: the file is read through \\wsl.localhost; run outside it, so git does not look for
		// a repository there (and refuse it for its owner)
		cmd.Dir = os.TempDir()
	}
	cmd.Env = append(gitEnv(os.Environ()), "GIT_CONFIG_NOSYSTEM=1")
	proc.Harden(cmd)
	// a configuration file is small; one that prints more than this is not read
	out, stderr := &proc.CapWriter{Max: 8 << 20}, &proc.CapWriter{Max: 1 << 20}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil || out.Exceeded() {
		// an unparseable config is a change too: git would refuse it, and so does the guard
		return map[string]string{"config-unreadable:" + filepath.Base(path): sha(append([]byte(stderr.String()), out.String()...))}, nil
	}
	vals := map[string][]string{}
	for _, rec := range strings.Split(out.String(), "\x00") {
		if rec == "" {
			continue
		}
		k, v, _ := strings.Cut(rec, "\n")
		if bookkeeping(k) {
			continue
		}
		vals[k] = append(vals[k], v)
	}
	res := make(map[string]string, len(vals))
	prefix := ""
	if b := filepath.Base(path); b != "config" {
		prefix = b + ":"
	}
	for k, vs := range vals {
		res[prefix+k] = sha([]byte(strings.Join(vs, "\x00")))
	}
	return res, nil
}

func bookkeeping(key string) bool {
	first, rest, ok := strings.Cut(key, ".")
	if !ok || !strings.EqualFold(first, "branch") {
		return false
	}
	i := strings.LastIndex(rest, ".")
	return i >= 0 && !strings.EqualFold(rest[i+1:], "mergeoptions")
}

// check refuses with ConfigChangedError when the state differs from the trusted one.
func (g *guard) check(ctx context.Context, root string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, err := g.read(ctx)
	if err != nil {
		return err
	}
	if changed := g.trusted.diff(cur); len(changed) > 0 {
		return &ConfigChangedError{Root: root, Changed: changed, Fingerprint: cur.Fingerprint()}
	}
	return nil
}

// TrustedState returns the state Drydock trusts (one hash per item, no values) so a caller can keep it across restarts.
func (r *Repo) TrustedState() RepoState {
	if r.guard == nil {
		return nil
	}
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	out := make(RepoState, len(r.guard.trusted))
	for k, v := range r.guard.trusted {
		out[k] = v
	}
	return out
}

// SetTrustedState makes s the trusted state, for example the one recorded before a restart: a change made while Drydock was
// not running is then caught too.
func (r *Repo) SetTrustedState(s RepoState) {
	if r.guard == nil || s == nil {
		return
	}
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	r.guard.trusted = s
}

// Trust trusts the repository's configuration as it is now, but only when its fingerprint is fp, the state the person was
// shown and approved; if it changed again since, the newer change is returned for review instead.
func (r *Repo) Trust(ctx context.Context, fp string) error {
	if r.guard == nil {
		return nil
	}
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	cur, err := r.guard.read(ctx)
	if err != nil {
		return err
	}
	if cur.Fingerprint() != fp {
		return &ConfigChangedError{Root: r.Root, Changed: r.guard.trusted.diff(cur), Fingerprint: cur.Fingerprint()}
	}
	r.guard.trusted = cur
	return nil
}

// ConfigStatus reports the items that differ from the trusted state now (empty when it is trusted) and the fingerprint of
// the current state, for a page that lets a person review and trust it outside a run.
func (r *Repo) ConfigStatus(ctx context.Context) (changed []string, fingerprint string, err error) {
	if r.guard == nil {
		return nil, "", nil
	}
	r.guard.mu.Lock()
	defer r.guard.mu.Unlock()
	cur, err := r.guard.read(ctx)
	if err != nil {
		return nil, "", err
	}
	return r.guard.trusted.diff(cur), cur.Fingerprint(), nil
}

// startGuard finds the common git directory and trusts its state as it is now.
func (r *Repo) startGuard(ctx context.Context) error {
	out, err := r.gitUnguarded(ctx, r.Root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	g := &guard{commonDir: filepath.Clean(strings.TrimSpace(out))}
	if g.trusted, err = g.read(ctx); err != nil {
		return err
	}
	r.guard = g
	return nil
}

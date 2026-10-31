// Package sandbox runs a task's commands in a container instead of on the human's machine (docs/sandbox.md). Each task gets
// its own workspace container with the task worktree mounted at /workspace, the services the project lists (a database, a
// cache) on a private network, and, by default, a network allowlist enforced by a small proxy container: the agent may
// install, migrate, build, run and test freely inside, while nothing else on the machine is reachable. The engine is Docker
// (Docker Desktop on Windows), the only one supported (decision P135); Drydock talks to it only through the `docker` command.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ConfigPath is where a project defines its sandbox, read from the project's default branch (never from a task worktree: a
// change an agent makes to it takes effect only after a human merged it).
const ConfigPath = ".drydock/sandbox.json"

// Workdir is where the task worktree is mounted inside the workspace container.
const Workdir = "/workspace"

// Network modes.
const (
	// NetworkAllowlist: the workspace reaches only the hosts on the allowlist, through the egress proxy (the default).
	NetworkAllowlist = "allowlist"
	// NetworkOpen: the workspace has ordinary internet access, without a proxy.
	NetworkOpen = "open"
	// NetworkNone: no internet at all; the services stay reachable.
	NetworkNone = "none"
)

// Config is a project's sandbox definition (the JSON file at ConfigPath).
type Config struct {
	// Image is the workspace image, for example "node:24-trixie" or the project's dev container image.
	Image string `json:"image"`
	// Setup commands run once, in order, after the workspace starts (for example "npm ci"). A failure is reported to the agent,
	// it does not stop the sandbox.
	Setup []string `json:"setup,omitempty"`
	// Env is set for every command. Values are visible to the agent: never put secrets here.
	Env map[string]string `json:"env,omitempty"`
	// Services run next to the workspace on the task's private network, reachable by their name as host name.
	Services map[string]Service `json:"services,omitempty"`
	// Network is allowlist (default), open or none.
	Network string `json:"network,omitempty"`
	// AllowHosts are added to the default allowlist (DefaultAllowHosts) unless NoDefaultHosts is set. A name matches itself;
	// "*.example.com" matches every name under example.com.
	AllowHosts     []string `json:"allowHosts,omitempty"`
	NoDefaultHosts bool     `json:"noDefaultHosts,omitempty"`
	// Volumes are container paths kept on a volume of their own for the life of the task, for example
	// "/workspace/node_modules" (fast Linux file system instead of the mounted worktree, and no Linux binaries in the worktree).
	Volumes []string `json:"volumes,omitempty"`
	// User runs commands as this user (a name or uid[:gid] known to the image); empty means the image's default user.
	User string `json:"user,omitempty"`
	// Resource limits of the workspace container.
	CPUs   float64 `json:"cpus,omitempty"`
	Memory string  `json:"memory,omitempty"`
	Pids   int     `json:"pids,omitempty"`
}

// Service is one container next to the workspace.
type Service struct {
	Image   string            `json:"image"`
	Env     map[string]string `json:"env,omitempty"`
	Command []string          `json:"command,omitempty"`
}

// DefaultAllowHosts are the package registries and download hosts most projects need. Projects add their own (an API they
// call in tests) with allowHosts, or start from nothing with noDefaultHosts.
var DefaultAllowHosts = []string{
	// npm, Yarn and Node headers
	"registry.npmjs.org", "registry.yarnpkg.com", "nodejs.org",
	// Go modules
	"proxy.golang.org", "sum.golang.org",
	// Python
	"pypi.org", "files.pythonhosted.org",
	// Rust
	"crates.io", "index.crates.io", "static.crates.io",
	// Debian, Ubuntu and Alpine packages
	"deb.debian.org", "security.debian.org", "archive.ubuntu.com", "security.ubuntu.com", "dl-cdn.alpinelinux.org",
	// dependencies fetched from GitHub (read-only: no credentials are ever put in the sandbox)
	"github.com", "codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com",
	// Playwright browser downloads
	"playwright.azureedge.net", "cdn.playwright.dev",
}

var (
	imageRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
	envKeyRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	serviceRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	hostRe    = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?)*$`)
	memoryRe  = regexp.MustCompile(`^[1-9][0-9]{0,6}[kmg]?$`)
	userRe    = regexp.MustCompile(`^([0-9]{1,7}(:[0-9]{1,7})?|[a-z_][a-z0-9_-]{0,31})$`)
)

// reservedEnv are set by Drydock for every command (the proxy) and cannot be overridden by the project.
var reservedEnv = map[string]bool{"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true}

// ParseConfig reads and validates a sandbox definition.
func ParseConfig(data []byte) (Config, error) {
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	return c, nil
}

// Validate checks the definition and fills the defaults.
func (c *Config) Validate() error {
	c.Image = strings.TrimSpace(c.Image)
	if c.Image == "" {
		return errors.New(`"image" is required (for example "node:24-trixie")`)
	}
	if !imageRe.MatchString(c.Image) {
		return fmt.Errorf("image %q is not a valid image reference", c.Image)
	}
	if len(c.Setup) > 20 {
		return errors.New("at most 20 setup commands")
	}
	for _, s := range c.Setup {
		if strings.TrimSpace(s) == "" || len(s) > 4000 || strings.ContainsRune(s, 0) {
			return errors.New("a setup command is empty, longer than 4000 characters or contains a NUL")
		}
	}
	if err := checkEnv(c.Env); err != nil {
		return err
	}
	if len(c.Services) > 8 {
		return errors.New("at most 8 services")
	}
	for name, s := range c.Services {
		if !serviceRe.MatchString(name) || name == "workspace" || name == "egress" {
			return fmt.Errorf("service name %q: use lower-case letters, digits and dashes (not workspace or egress)", name)
		}
		if !imageRe.MatchString(strings.TrimSpace(s.Image)) {
			return fmt.Errorf("service %s: image %q is not a valid image reference", name, s.Image)
		}
		if err := checkEnv(s.Env); err != nil {
			return fmt.Errorf("service %s: %w", name, err)
		}
		if len(s.Command) > 64 {
			return fmt.Errorf("service %s: a command has at most 64 arguments", name)
		}
		for _, a := range s.Command {
			if strings.ContainsRune(a, 0) {
				return fmt.Errorf("service %s: a command argument contains a NUL", name)
			}
		}
	}
	switch c.Network {
	case "":
		c.Network = NetworkAllowlist
	case NetworkAllowlist, NetworkOpen, NetworkNone:
	default:
		return fmt.Errorf("network %q: use allowlist, open or none", c.Network)
	}
	if len(c.AllowHosts) > 200 {
		return errors.New("at most 200 allowHosts")
	}
	for i, h := range c.AllowHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if !hostRe.MatchString(h) {
			return fmt.Errorf("allowHosts: %q is not a host name (no scheme, port or path; \"*.example.com\" for a whole domain)", c.AllowHosts[i])
		}
		c.AllowHosts[i] = h
	}
	if len(c.Volumes) > 8 {
		return errors.New("at most 8 volumes")
	}
	for _, v := range c.Volumes {
		if !strings.HasPrefix(v, "/") || path.Clean(v) != v || v == "/" || v == Workdir || strings.ContainsAny(v, ",:\x00") {
			return fmt.Errorf("volume %q: give an absolute container path such as /workspace/node_modules", v)
		}
	}
	if c.User != "" && !userRe.MatchString(c.User) {
		return fmt.Errorf("user %q: give a user name or uid[:gid]", c.User)
	}
	if c.CPUs == 0 {
		c.CPUs = 2
	}
	if c.CPUs < 0.1 || c.CPUs > 64 {
		return errors.New("cpus must be between 0.1 and 64")
	}
	if c.Memory == "" {
		c.Memory = "4g"
	}
	c.Memory = strings.ToLower(c.Memory)
	if !memoryRe.MatchString(c.Memory) {
		return fmt.Errorf("memory %q: give a size such as 4g or 2048m", c.Memory)
	}
	if c.Pids == 0 {
		c.Pids = 2048
	}
	if c.Pids < 64 || c.Pids > 65536 {
		return errors.New("pids must be between 64 and 65536")
	}
	return nil
}

func checkEnv(env map[string]string) error {
	if len(env) > 100 {
		return errors.New("at most 100 environment variables")
	}
	for k, v := range env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("environment variable name %q is not valid", k)
		}
		if reservedEnv[k] || strings.HasPrefix(k, "DRYDOCK_") {
			return fmt.Errorf("environment variable %s is set by Drydock and cannot be changed", k)
		}
		if len(v) > 4096 || strings.ContainsRune(v, 0) {
			return fmt.Errorf("environment variable %s: value longer than 4096 characters or contains a NUL", k)
		}
	}
	return nil
}

// Hosts is the allowlist in force: the defaults (unless switched off) plus the project's hosts, sorted and unique.
func (c Config) Hosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if !c.NoDefaultHosts {
		for _, h := range DefaultAllowHosts {
			add(h)
		}
	}
	for _, h := range c.AllowHosts {
		add(h)
	}
	sort.Strings(out)
	return out
}

// ServiceNames are the services' names, sorted.
func (c Config) ServiceNames() []string {
	out := make([]string, 0, len(c.Services))
	for n := range c.Services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Hash identifies a definition: a running sandbox whose hash differs from the current definition is replaced.
func (c Config) Hash() string {
	b, _ := json.Marshal(c) // map keys are marshalled in sorted order
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Fingerprint is the whole SHA-256 of a definition, for deciding whether the definition a person confirmed is the one that is
// about to run; Hash is short, enough to tell sandboxes apart.
func (c Config) Fingerprint() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Diff lists, in plain words, what is different in next from prev: what a person needs to read before they confirm a changed
// definition. A nil prev is a definition that was never confirmed, and next is described instead. Environment values and service
// environments are named, not shown. Every line is cut short; an empty result means the two are the same.
func Diff(prev *Config, next Config) []string {
	var out []string
	add := func(format string, args ...any) {
		l := fmt.Sprintf(format, args...)
		if len(l) > 200 {
			l = l[:200] + "..."
		}
		out = append(out, l)
	}
	if prev == nil {
		add("no definition has been confirmed yet; this one has image %q, network %q", next.Image, next.Network)
		if len(next.AllowHosts) > 0 {
			add("allowHosts: %s", strings.Join(next.AllowHosts, ", "))
		}
		for _, c := range next.Setup {
			add("setup command: %s", c)
		}
		if names := serviceNames(next.Services); len(names) > 0 {
			add("services: %s", strings.Join(names, ", "))
		}
		if len(next.Volumes) > 0 {
			add("volumes: %s", strings.Join(next.Volumes, ", "))
		}
		return out
	}
	p := *prev
	if p.Image != next.Image {
		add("image %q -> %q", p.Image, next.Image)
	}
	if p.Network != next.Network {
		add("network %q -> %q", p.Network, next.Network)
	}
	if p.NoDefaultHosts != next.NoDefaultHosts {
		add("noDefaultHosts %t -> %t", p.NoDefaultHosts, next.NoDefaultHosts)
	}
	if added, removed := setDiff(p.AllowHosts, next.AllowHosts); len(added)+len(removed) > 0 {
		if len(added) > 0 {
			add("allowHosts added: %s", strings.Join(added, ", "))
		}
		if len(removed) > 0 {
			add("allowHosts removed: %s", strings.Join(removed, ", "))
		}
	}
	if added, removed := setDiff(p.Setup, next.Setup); len(added)+len(removed) > 0 {
		for _, c := range added {
			add("setup command added: %s", c)
		}
		for _, c := range removed {
			add("setup command removed: %s", c)
		}
	}
	if added, removed := setDiff(mapKeys(p.Env), mapKeys(next.Env)); len(added)+len(removed) > 0 {
		if len(added) > 0 {
			add("env added: %s", strings.Join(added, ", "))
		}
		if len(removed) > 0 {
			add("env removed: %s", strings.Join(removed, ", "))
		}
	}
	for _, k := range mapKeys(next.Env) {
		if old, ok := p.Env[k]; ok && old != next.Env[k] {
			add("env %s changed", k)
		}
	}
	if added, removed := setDiff(serviceNames(p.Services), serviceNames(next.Services)); len(added)+len(removed) > 0 {
		if len(added) > 0 {
			add("services added: %s", strings.Join(added, ", "))
		}
		if len(removed) > 0 {
			add("services removed: %s", strings.Join(removed, ", "))
		}
	}
	for _, name := range serviceNames(next.Services) {
		o, ok := p.Services[name]
		if !ok {
			continue
		}
		n := next.Services[name]
		if o.Image != n.Image {
			add("service %s image %q -> %q", name, o.Image, n.Image)
		}
		if strings.Join(o.Command, "\x00") != strings.Join(n.Command, "\x00") {
			add("service %s command %q -> %q", name, strings.Join(o.Command, " "), strings.Join(n.Command, " "))
		}
		if !sameMap(o.Env, n.Env) {
			add("service %s env changed", name)
		}
	}
	if added, removed := setDiff(p.Volumes, next.Volumes); len(added)+len(removed) > 0 {
		if len(added) > 0 {
			add("volumes added: %s", strings.Join(added, ", "))
		}
		if len(removed) > 0 {
			add("volumes removed: %s", strings.Join(removed, ", "))
		}
	}
	if p.User != next.User {
		add("user %q -> %q", p.User, next.User)
	}
	if p.CPUs != next.CPUs {
		add("cpus %g -> %g", p.CPUs, next.CPUs)
	}
	if p.Memory != next.Memory {
		add("memory %q -> %q", p.Memory, next.Memory)
	}
	if p.Pids != next.Pids {
		add("pids %d -> %d", p.Pids, next.Pids)
	}
	return out
}

// setDiff returns what is in next and not in prev, and what is in prev and not in next, each sorted.
func setDiff(prev, next []string) (added, removed []string) {
	have := map[string]bool{}
	for _, x := range prev {
		have[x] = true
	}
	now := map[string]bool{}
	for _, x := range next {
		now[x] = true
		if !have[x] {
			added = append(added, x)
		}
	}
	for _, x := range prev {
		if !now[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func serviceNames(m map[string]Service) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// Starter suggests a definition for a project that has none yet, from what the worktree contains. It is a suggestion shown
// to the human, never used without being committed.
func Starter(hasFile func(name string) bool) Config {
	c := Config{Image: "debian:trixie"}
	switch {
	case hasFile("package.json"):
		c.Image = "node:24-trixie"
		switch {
		case hasFile("package-lock.json"):
			c.Setup = []string{"npm ci"}
		case hasFile("pnpm-lock.yaml"):
			c.Setup = []string{"corepack enable && pnpm install --frozen-lockfile"}
		case hasFile("yarn.lock"):
			c.Setup = []string{"corepack enable && yarn install --immutable"}
		default:
			c.Setup = []string{"npm install"}
		}
		c.Volumes = []string{Workdir + "/node_modules"}
	case hasFile("go.mod"):
		c.Image = "golang:trixie"
		c.Setup = []string{"go mod download"}
	case hasFile("requirements.txt") || hasFile("pyproject.toml"):
		c.Image = "python:3-trixie"
	}
	return c
}

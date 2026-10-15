// Package manifests holds the declarative adapter manifests (one YAML file per agent CLI, embedded), their loader and
// validator, compat.yaml (which versions were actually verified, and how), and the Registry that builds agents.Adapter
// instances from them. What cannot be data (the ACP client, headless parsers, the Windows process lifecycle) stays Go;
// see docs/connections.md section 13.
package manifests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the manifest schema this loader understands.
const SchemaVersion = 1

// Transport names.
const (
	TransportACP      = "acp"
	TransportHeadless = "headless"
)

// Manifest declares one agent CLI. Unknown keys are errors.
type Manifest struct {
	Meta         Meta           `yaml:"meta"`
	Binaries     Binaries       `yaml:"binaries"`
	Install      []Install      `yaml:"install"`
	Requires     []Requirement  `yaml:"requires"`
	Detect       Detect         `yaml:"detect"`
	Versions     Versions       `yaml:"versions"`
	Auth         Auth           `yaml:"auth"`
	Transports   Transports     `yaml:"transports"`
	Provider     map[string]any `yaml:"provider_config"` // consumed by the provider-config writers, opaque here
	MCP          map[string]any `yaml:"mcp"`
	Usage        map[string]any `yaml:"usage"`
	Capabilities Declared       `yaml:"capabilities_declared"`
	Instructions map[string]any `yaml:"instructions"`
	Terms        *Terms         `yaml:"terms"`
	Gate         *Gate          `yaml:"gate"`
}

// Meta identifies the CLI.
type Meta struct {
	Schema      int      `yaml:"schema"`
	ID          string   `yaml:"id"`
	DisplayName string   `yaml:"display_name"`
	Kind        string   `yaml:"kind"`
	License     string   `yaml:"license"`
	Tier        string   `yaml:"tier"` // premium | cheap
	RunModes    []string `yaml:"run_modes"`
	Custom      bool     `yaml:"custom"`
}

// Binaries lists executable names in priority order per run mode. Hints are secondary search directories (unverified install
// locations); `~` expands to the home directory.
type Binaries struct {
	Native []string `yaml:"native"`
	WSL    []string `yaml:"wsl"`
	Hints  []string `yaml:"hints"`
}

// Install is display-only guidance. Drydock never executes it.
type Install struct {
	Shell    string        `yaml:"shell"`
	Command  string        `yaml:"command"`
	Note     string        `yaml:"note"`
	Requires []Requirement `yaml:"requires"`
	// RunMode is "wsl" for a line run inside a WSL distro (shown on the distro's Connection); empty for Windows.
	RunMode string `yaml:"run_mode"`
}

// Requirement is a prerequisite tool.
type Requirement struct {
	Tool string `yaml:"tool"`
	Min  string `yaml:"min"`
}

// Detect describes the version probe.
type Detect struct {
	Version VersionProbe `yaml:"version"`
}

// VersionProbe runs `<bin> <argv>` and extracts the version with the first capture group of Regex.
type VersionProbe struct {
	Argv  []string `yaml:"argv"`
	Regex string   `yaml:"regex"`
}

// Versions are the supported range.
type Versions struct {
	Min         string `yaml:"min"`
	Recommended string `yaml:"recommended"`
}

// Auth describes login and the non-interactive probe. Drydock never reads credentials.
type Auth struct {
	Login          *Login            `yaml:"login"`
	Probe          *Probe            `yaml:"probe"`
	WarnEnv        []string          `yaml:"warn_env"`
	ForbiddenFlags []string          `yaml:"forbidden_flags"`
	AllowedKinds   []string          `yaml:"allowed_kinds"`
	BlockedKinds   []string          `yaml:"blocked_kinds"`
	EnvFromKeyring map[string]string `yaml:"env_from_keyring"`
}

// Login is the command run in the embedded terminal.
type Login struct {
	Argv     []string   `yaml:"argv"`
	Variants [][]string `yaml:"variants"`
	Terminal bool       `yaml:"terminal"`
	Note     string     `yaml:"note"`
}

// Probe is a non-interactive auth check. `probe: none` in YAML decodes to a nil *Probe (see Auth.UnmarshalYAML).
type Probe struct {
	Argv         []string `yaml:"argv"`
	Parse        string   `yaml:"parse"`          // json | exit_code | text_providers
	ReadyWhen    string   `yaml:"ready_when"`     // "$.loggedIn == true" or "exit == 0"
	NotReadyWhen string   `yaml:"not_ready_when"` // "exit == 1"
	Unverified   bool     `yaml:"unverified"`
}

// Transports are the ways Drydock can drive the CLI.
type Transports struct {
	Primary  string        `yaml:"primary"`
	ACP      *ACPTransport `yaml:"acp"`
	Headless *Headless     `yaml:"headless"`
}

// ACPTransport spawns an ACP agent. Argv[0] is the command (npx for bridges), exactly as run.
type ACPTransport struct {
	Enabled    *bool             `yaml:"enabled"` // nil means enabled
	Argv       []string          `yaml:"argv"`
	Env        map[string]string `yaml:"env"`
	Requires   []Requirement     `yaml:"requires"`
	EnableWhen string            `yaml:"enable_when"`
	Note       string            `yaml:"note"`
	// ClientMeta is sent as clientCapabilities._meta in initialize: an agent-specific opt-in this CLI's agent reads (OpenCode:
	// "opencode/child-session-updates"; internal/agents/acp/ext.go). Declared per manifest, never for every agent.
	ClientMeta map[string]any `yaml:"client_meta"`
}

// Headless is the prompt-and-exit contract (docs/connections.md section 9). Argv excludes the executable.
type Headless struct {
	Argv          []string       `yaml:"argv"`
	Optional      OptionalArgs   `yaml:"optional"`
	Prompt        any            `yaml:"prompt"`
	Parser        string         `yaml:"parser"` // names a Go parser; custom manifests may not set it
	Interrupt     any            `yaml:"interrupt"`
	ProbeUnknown  []string       `yaml:"probe_unknown"`
	DeniedActions string         `yaml:"denied_actions"`
	ExitCodes     map[int]string `yaml:"exit_codes"`
	Dialect       *Dialect       `yaml:"dialect"`
	Unverified    bool           `yaml:"unverified"`
}

// OptionalArg is a flag group included only when every placeholder in it has a value.
type OptionalArg struct {
	Name string
	Argv []string
}

// OptionalArgs keeps the order the manifest wrote, because flag order is part of the argv.
type OptionalArgs []OptionalArg

// UnmarshalYAML decodes a mapping of name to argv preserving order.
func (o *OptionalArgs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("optional must be a mapping of name to argv")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var argv []string
		if err := n.Content[i+1].Decode(&argv); err != nil {
			return fmt.Errorf("optional.%s: %w", n.Content[i].Value, err)
		}
		*o = append(*o, OptionalArg{Name: n.Content[i].Value, Argv: argv})
	}
	return nil
}

// Dialect maps a custom CLI's JSON lines onto the normalized events.
type Dialect struct {
	Format    string                       `yaml:"format"`
	TypeField string                       `yaml:"type_field"`
	Map       map[string]map[string]string `yaml:"map"`
}

// Declared are the seed capability values. Values are true, false, "unknown" or "estimate".
type Declared map[string]any

// Terms names the terms notice shown for this CLI and carries its summary text.
type Terms struct {
	Notice  string `yaml:"notice"`
	Warning string `yaml:"warning"`
}

// Gate is an acknowledgement the user must give before the Connection is usable.
type Gate struct {
	ID       string `yaml:"id"`
	Required bool   `yaml:"required"`
	Text     string `yaml:"text"`
}

// UnmarshalYAML on Transports handles `headless: none` / `acp: none` by decoding into a shadow type.
func (t *Transports) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Primary  string    `yaml:"primary"`
		ACP      yaml.Node `yaml:"acp"`
		Headless yaml.Node `yaml:"headless"`
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	if err := strictUnmarshal(b, &raw); err != nil {
		return err
	}
	t.Primary = raw.Primary
	if raw.ACP.Kind != 0 && !isNone(&raw.ACP) {
		t.ACP = &ACPTransport{}
		if err := decodeStrict(&raw.ACP, t.ACP); err != nil {
			return fmt.Errorf("transports.acp: %w", err)
		}
	}
	if raw.Headless.Kind != 0 && !isNone(&raw.Headless) {
		t.Headless = &Headless{}
		if err := decodeStrict(&raw.Headless, t.Headless); err != nil {
			return fmt.Errorf("transports.headless: %w", err)
		}
	}
	return nil
}

func isNone(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && (n.Value == "none" || n.Tag == "!!null")
}

// decodeStrict decodes a node into v rejecting unknown keys (yaml.Node.Decode ignores them).
func decodeStrict(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	return strictUnmarshal(b, v)
}

func strictUnmarshal(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// UnmarshalYAML on Auth handles `probe: none` (the Probe type cannot represent absence).
func (a *Auth) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Login          *Login            `yaml:"login"`
		Probe          yaml.Node         `yaml:"probe"`
		WarnEnv        []string          `yaml:"warn_env"`
		ForbiddenFlags []string          `yaml:"forbidden_flags"`
		AllowedKinds   []string          `yaml:"allowed_kinds"`
		BlockedKinds   []string          `yaml:"blocked_kinds"`
		EnvFromKeyring map[string]string `yaml:"env_from_keyring"`
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	if err := strictUnmarshal(b, &raw); err != nil {
		return err
	}
	a.Login, a.WarnEnv, a.ForbiddenFlags = raw.Login, raw.WarnEnv, raw.ForbiddenFlags
	a.AllowedKinds, a.BlockedKinds, a.EnvFromKeyring = raw.AllowedKinds, raw.BlockedKinds, raw.EnvFromKeyring
	if raw.Probe.Kind != 0 && !isNone(&raw.Probe) {
		a.Probe = &Probe{}
		if err := decodeStrict(&raw.Probe, a.Probe); err != nil {
			return fmt.Errorf("auth.probe: %w", err)
		}
	}
	return nil
}

// Parse decodes and validates one manifest document. Unknown keys and multiple documents are errors.
func Parse(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest: more than one YAML document in a file; use one file per CLI")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ValidationError lists every problem found in one manifest.
type ValidationError struct {
	ID       string
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("manifest %q is invalid: %s", e.ID, strings.Join(e.Problems, "; "))
}

var (
	idRe        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pinnedPkgRe = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*@\d+\.\d+\.\d+$`)
	placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_.]+)\s*\}\}`)
	knownTools  = map[string]bool{"node": true, "git": true, "gh": true, "npm": true}
	knownVars   = map[string]bool{"model": true, "effort": true, "session_id": true, "cwd": true, "mode": true, "conservative_mode": true,
		"mcp.config_path": true, "mcp.permission_tool": true, "resolved_binary": true, "system_append": true, "allowed_tools": true}
	probeParses = map[string]bool{"json": true, "exit_code": true, "text_providers": true}
	declaredKey = map[string]bool{"streaming": true, "structured_output": true, "model_selection": true, "effort_control": true, "session_resume": true,
		"interrupt": true, "mcp": true, "token_usage": true, "cost_reporting": true, "headless": true, "permission_modes": true, "image_input": true,
		"tool_call_visibility": true}
)

// Validate checks the manifest against the schema rules. It returns a *ValidationError listing all problems.
func (m *Manifest) Validate() error {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	id := m.Meta.ID
	if m.Meta.Schema != SchemaVersion {
		add("meta.schema must be %d, got %d", SchemaVersion, m.Meta.Schema)
	}
	if !idRe.MatchString(id) {
		add("meta.id %q must match %s", id, idRe)
	}
	if strings.TrimSpace(m.Meta.DisplayName) == "" {
		add("meta.display_name is required")
	}
	if m.Meta.Kind != "agent_cli" {
		add("meta.kind must be agent_cli, got %q", m.Meta.Kind)
	}
	if t := m.Meta.Tier; t != "" && t != "premium" && t != "cheap" {
		add("meta.tier must be premium or cheap, got %q", t)
	}
	for _, rm := range m.Meta.RunModes {
		if rm != "native" && rm != "wsl" {
			add("meta.run_modes: unknown run mode %q", rm)
		}
	}
	if len(m.Binaries.Native) == 0 {
		add("binaries.native must list at least one executable")
	}
	for _, b := range append(append([]string(nil), m.Binaries.Native...), m.Binaries.WSL...) {
		if strings.TrimSpace(b) == "" {
			add("binaries: empty executable name")
		}
	}
	for _, r := range m.Requires {
		p = append(p, checkReq("requires", r)...)
	}
	for i, in := range m.Install {
		if strings.TrimSpace(in.Command) == "" {
			add("install[%d].command is required", i)
		}
		if in.Shell != "powershell" && in.Shell != "cmd" && in.Shell != "sh" {
			add("install[%d].shell must be powershell, cmd or sh, got %q", i, in.Shell)
		}
		switch in.RunMode {
		case "":
		case "wsl":
			if in.Shell != "sh" || !slices.Contains(m.Meta.RunModes, "wsl") {
				add("install[%d]: a run_mode wsl line needs shell sh and wsl in meta.run_modes", i)
			}
		default:
			add("install[%d].run_mode must be empty or wsl, got %q", i, in.RunMode)
		}
		for _, r := range in.Requires {
			p = append(p, checkReq(fmt.Sprintf("install[%d].requires", i), r)...)
		}
	}
	// detection
	if len(m.Detect.Version.Argv) == 0 {
		add("detect.version.argv is required")
	}
	if re, err := regexp.Compile(m.Detect.Version.Regex); err != nil {
		add("detect.version.regex: %v", err)
	} else if re.NumSubexp() < 1 {
		add("detect.version.regex needs one capture group for the version")
	}
	// versions
	if !m.Meta.Custom {
		if _, ok := ParseVersion(m.Versions.Min); !ok {
			add("versions.min must be a semantic version, got %q", m.Versions.Min)
		}
	}
	if m.Versions.Min != "" || m.Versions.Recommended != "" {
		vmin, okMin := ParseVersion(m.Versions.Min)
		if m.Versions.Min != "" && !okMin {
			add("versions.min %q is not a version", m.Versions.Min)
		}
		if m.Versions.Recommended != "" {
			vrec, ok := ParseVersion(m.Versions.Recommended)
			switch {
			case !ok:
				add("versions.recommended %q is not a version", m.Versions.Recommended)
			case okMin && vrec.Less(vmin):
				add("versions.recommended %s is below versions.min %s", m.Versions.Recommended, m.Versions.Min)
			}
		}
	}
	// auth
	if pr := m.Auth.Probe; pr != nil {
		if len(pr.Argv) == 0 {
			add("auth.probe.argv is required")
		}
		if !probeParses[pr.Parse] {
			add("auth.probe.parse must be json, exit_code or text_providers, got %q", pr.Parse)
		}
		if (pr.Parse == "json" || pr.Parse == "exit_code") && pr.ReadyWhen == "" {
			add("auth.probe.ready_when is required for parse=%s", pr.Parse)
		}
		for _, e := range []string{pr.ReadyWhen, pr.NotReadyWhen} {
			if e != "" {
				if _, err := ParseCondition(e); err != nil {
					add("auth.probe condition %q: %v", e, err)
				}
			}
		}
	}
	if l := m.Auth.Login; l != nil && l.Terminal && len(l.Argv) == 0 && !m.Meta.Custom && len(l.Variants) == 0 && l.Note == "" {
		add("auth.login needs argv or a note")
	}
	// transports
	t := m.Transports
	if t.Primary != TransportACP && t.Primary != TransportHeadless {
		add("transports.primary must be %q or %q, got %q", TransportACP, TransportHeadless, t.Primary)
	}
	if t.ACP == nil && t.Headless == nil {
		add("transports: at least one of acp and headless is required")
	}
	if t.Primary == TransportACP && t.ACP == nil {
		add("transports.primary is acp but transports.acp is missing")
	}
	if t.Primary == TransportHeadless && t.Headless == nil {
		add("transports.primary is headless but transports.headless is missing")
	}
	if a := t.ACP; a != nil {
		p = append(p, checkACP(a)...)
	}
	if h := t.Headless; h != nil {
		if len(h.Argv) == 0 && h.Dialect == nil {
			add("transports.headless.argv is required")
		}
		for _, v := range collectVars(h.Argv, h.Optional) {
			if !knownVars[v] {
				add("transports.headless: unknown placeholder {{%s}}", v)
			}
		}
		if h.Dialect != nil {
			p = append(p, checkDialect(h.Dialect)...)
		}
	}
	// forbidden flags must never appear in any argv
	for _, f := range m.Auth.ForbiddenFlags {
		for _, argv := range m.allArgv() {
			for _, a := range argv {
				if a == f {
					add("argv contains forbidden flag %s", f)
				}
			}
		}
	}
	// declared capabilities
	for k, v := range m.Capabilities {
		if !declaredKey[k] {
			add("capabilities_declared: unknown capability %q", k)
			continue
		}
		switch x := v.(type) {
		case bool:
		case string:
			if x != "unknown" && x != "estimate" {
				add("capabilities_declared.%s: %q is not true, false, unknown or estimate", k, x)
			}
		default:
			add("capabilities_declared.%s: unsupported value %v", k, v)
		}
	}
	if g := m.Gate; g != nil && strings.TrimSpace(g.ID) == "" {
		add("gate.id is required")
	}
	if tm := m.Terms; tm != nil && strings.TrimSpace(tm.Notice) == "" {
		add("terms.notice is required when terms is present")
	}
	// custom manifests: no Go parsers, no ungated execution
	if m.Meta.Custom {
		if t.Primary != TransportHeadless || t.Headless == nil {
			add("custom manifests use the headless contract")
		} else {
			if t.Headless.Parser != "" {
				add("custom manifests cannot name a Go parser (%q); describe a dialect instead", t.Headless.Parser)
			}
			if t.Headless.Dialect == nil {
				add("custom manifests need transports.headless.dialect")
			}
		}
		if t.ACP != nil {
			add("custom manifests cannot define an acp transport")
		}
		if m.Gate == nil || !m.Gate.Required {
			add("custom manifests must carry a required gate (they run arbitrary code)")
		}
	}
	if len(p) > 0 {
		return &ValidationError{ID: id, Problems: p}
	}
	return nil
}

func checkReq(where string, r Requirement) []string {
	var p []string
	if !knownTools[r.Tool] {
		p = append(p, fmt.Sprintf("%s: unknown tool %q", where, r.Tool))
	}
	if r.Min != "" {
		if _, ok := ParseVersion(r.Min); !ok {
			p = append(p, fmt.Sprintf("%s: min %q is not a version", where, r.Min))
		}
	}
	return p
}

func checkACP(a *ACPTransport) []string {
	var p []string
	if len(a.Argv) == 0 || strings.TrimSpace(a.Argv[0]) == "" {
		return []string{"transports.acp.argv must start with the command"}
	}
	for _, r := range a.Requires {
		p = append(p, checkReq("transports.acp.requires", r)...)
	}
	for _, v := range collectVars(a.Argv, nil) {
		if !knownVars[v] {
			p = append(p, fmt.Sprintf("transports.acp: unknown placeholder {{%s}}", v))
		}
	}
	for k, v := range a.Env {
		if k == "" || strings.Contains(k, "=") {
			p = append(p, fmt.Sprintf("transports.acp.env: bad variable name %q", k))
		}
		for _, name := range placeholderNames(v) {
			if !knownVars[name] {
				p = append(p, fmt.Sprintf("transports.acp.env.%s: unknown placeholder {{%s}}", k, name))
			}
		}
	}
	for k := range a.ClientMeta {
		// ACP reserves _meta for extensions; an agent's own keys are namespaced ("opencode/child-session-updates")
		if !strings.Contains(k, "/") || strings.TrimSpace(k) != k {
			p = append(p, fmt.Sprintf("transports.acp.client_meta: key %q must be namespaced (vendor/name)", k))
		}
	}
	// bridge packages are pinned exactly (decision D9), and the deprecated @zed-industries/* bridges must not be used
	if base := strings.TrimSuffix(strings.ToLower(path.Base(strings.ReplaceAll(a.Argv[0], `\`, "/"))), ".cmd"); base == "npx" {
		pkg := ""
		for _, arg := range a.Argv[1:] {
			if !strings.HasPrefix(arg, "-") {
				pkg = arg
				break
			}
		}
		switch {
		case pkg == "":
			p = append(p, "transports.acp: npx without a package")
		case !pinnedPkgRe.MatchString(pkg):
			p = append(p, fmt.Sprintf("transports.acp: npx package %q must be pinned to an exact version (name@x.y.z)", pkg))
		case strings.HasPrefix(pkg, "@zed-industries/"):
			p = append(p, fmt.Sprintf("transports.acp: %q is deprecated; use the @agentclientprotocol/* bridge", pkg))
		}
	}
	return p
}

func checkDialect(d *Dialect) []string {
	var p []string
	if d.Format != "jsonl" {
		p = append(p, fmt.Sprintf("dialect.format must be jsonl, got %q", d.Format))
	}
	if d.TypeField == "" {
		p = append(p, "dialect.type_field is required")
	}
	allowed := map[string]bool{"assistant_text": true, "tool_call": true, "tool_result": true, "usage": true, "result": true, "error": true, "session": true}
	for k := range d.Map {
		if !allowed[k] {
			p = append(p, fmt.Sprintf("dialect.map: unknown event %q", k))
		}
	}
	if len(d.Map) == 0 {
		p = append(p, "dialect.map must map at least one event")
	}
	return p
}

func placeholderNames(s string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func collectVars(argv []string, optional OptionalArgs) []string {
	var out []string
	for _, a := range argv {
		out = append(out, placeholderNames(a)...)
	}
	for _, o := range optional {
		for _, a := range o.Argv {
			out = append(out, placeholderNames(a)...)
		}
	}
	return out
}

func (m *Manifest) allArgv() [][]string {
	var out [][]string
	if a := m.Transports.ACP; a != nil {
		out = append(out, a.Argv)
	}
	if h := m.Transports.Headless; h != nil {
		out = append(out, h.Argv)
		for _, o := range h.Optional {
			out = append(out, o.Argv)
		}
	}
	return out
}

// ACPEnabled reports whether the ACP transport exists and is not switched off.
func (m *Manifest) ACPEnabled() bool {
	a := m.Transports.ACP
	return a != nil && (a.Enabled == nil || *a.Enabled)
}

// ID is a shorthand for Meta.ID.
func (m *Manifest) ID() string { return m.Meta.ID }

// Set is a loaded collection of manifests keyed by id.
type Set struct {
	byID map[string]*Manifest
}

// Load reads every *.yaml file directly under dir in fsys.
func Load(fsys fs.FS, dir string) (*Set, error) {
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	s := &Set{byID: map[string]*Manifest{}}
	var errs []error
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m, err := Parse(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		if want := strings.TrimSuffix(e.Name(), ".yaml"); m.Meta.ID != want {
			errs = append(errs, fmt.Errorf("%s: meta.id %q must match the file name", e.Name(), m.Meta.ID))
			continue
		}
		if _, dup := s.byID[m.Meta.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate manifest id %q", e.Name(), m.Meta.ID))
			continue
		}
		s.byID[m.Meta.ID] = m
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

// Get returns the manifest with the given id.
func (s *Set) Get(id string) (*Manifest, bool) { m, ok := s.byID[id]; return m, ok }

// IDs lists manifest ids in sorted order.
func (s *Set) IDs() []string {
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// All returns the manifests in id order.
func (s *Set) All() []*Manifest {
	var out []*Manifest
	for _, id := range s.IDs() {
		out = append(out, s.byID[id])
	}
	return out
}

// HeadlessArgs builds the headless argv (after the executable) from vars: the base argv, then each optional group whose
// placeholders all have non-empty values, in manifest order. Substitution is textual on whole argv elements; nothing is
// ever passed through a shell.
func (m *Manifest) HeadlessArgs(vars map[string]string) []string {
	h := m.Transports.Headless
	if h == nil {
		return nil
	}
	var out []string
	sub := func(a string) (string, bool) {
		ok := true
		r := placeholder.ReplaceAllStringFunc(a, func(tok string) string {
			name := placeholder.FindStringSubmatch(tok)[1]
			v := vars[name]
			if v == "" {
				ok = false
			}
			return v
		})
		return r, ok
	}
	for _, a := range h.Argv {
		r, _ := sub(a)
		out = append(out, r)
	}
	for _, o := range h.Optional {
		var group []string
		complete := true
		for _, a := range o.Argv {
			r, ok := sub(a)
			complete = complete && ok
			group = append(group, r)
		}
		if complete {
			out = append(out, group...)
		}
	}
	return out
}

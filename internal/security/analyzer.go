package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Confidence is how much the analyzer knows about what a command will do. The order matters: combining two results
// takes the minimum (docs/security.md s7 step 8).
type Confidence int

// Confidence levels: ParsedExact > LexedConservative > Opaque.
const (
	Opaque            Confidence = iota // not analyzable: never auto-allowed
	LexedConservative                   // a recognized subset was lexed, not parsed by a real grammar
	ParsedExact                         // parsed by a real grammar with only literal command elements
)

func (c Confidence) String() string {
	switch c {
	case ParsedExact:
		return "parsed_exact"
	case LexedConservative:
		return "lexed_conservative"
	}
	return "opaque"
}

// MarshalText makes the confidence readable in audit details and events.
func (c Confidence) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// ShellKind is the command flavor an adapter reports.
type ShellKind string

// Shell flavors.
const (
	ShellBash       ShellKind = "bash"
	ShellPowerShell ShellKind = "powershell"
	ShellCmd        ShellKind = "cmd"
	ShellUnknown    ShellKind = "unknown"
)

// Analysis limits.
const (
	MaxCommandBytes  = 16 << 10 // longer input is Opaque (design assumption, docs/security.md s7 step 2)
	maxWrapperDepth  = 3
	maxInvocations   = 512
	maxBraceExpand   = 64
	maxScriptNesting = 8 // nested script blocks, command substitutions and sub-expressions
)

// CommandInput is what an adapter submits.
type CommandInput struct {
	Raw   string
	Shell ShellKind
	Cwd   string // working directory of the command; empty means Root
	Root  string // the worktree root every path is checked against
}

// Arg is one evaluated argument. Dynamic means its value is not known statically (variable, substitution, glob
// source text, stdin); Text then holds the source form for display.
type Arg struct {
	Text    string `json:"text"`
	Dynamic bool   `json:"dynamic,omitempty"`
	Glob    bool   `json:"glob,omitempty"`
	Data    bool   `json:"data,omitempty"` // literal data such as a here-string: never a path
}

// Redirect is a redirection of an invocation.
type Redirect struct {
	Op      string `json:"op"`
	Target  string `json:"target"`
	Dynamic bool   `json:"dynamic,omitempty"`
	Write   bool   `json:"write,omitempty"`
}

// Invocation is one program run found in the command, including those reached by unwrapping wrappers.
type Invocation struct {
	Program   string     `json:"program"` // normalized
	Raw       string     `json:"raw"`     // as written
	Args      []Arg      `json:"args"`
	Redirects []Redirect `json:"redirects,omitempty"`
	Edge      string     `json:"edge,omitempty"` // how it connects to the previous invocation: ";", "&&", "||", "|", "&"
	Depth     int        `json:"depth"`          // wrapper nesting
	Shell     ShellKind  `json:"shell"`
	Wrapper   bool       `json:"wrapper,omitempty"`
	StdinArgs bool       `json:"stdinArgs,omitempty"` // receives more arguments at run time (xargs, find -exec, pipeline input)
	Classes   []Class    `json:"classes,omitempty"`
	Rules     []string   `json:"rules,omitempty"`
	Env       []string   `json:"env,omitempty"` // names assigned for this command only

	piped     bool // stdin comes from a pipe
	stdinData bool // stdin is a here-document or here-string
	unix      bool // Unix-style paths on a Windows host (Git Bash, WSL)
	wsl       bool
	function  bool // call of a function defined in the same script
}

// PathRef is a path found in an invocation after the path guard.
type PathRef struct {
	Raw       string `json:"raw"`
	Canonical string `json:"canonical,omitempty"`
	Inside    bool   `json:"inside"`
	Dynamic   bool   `json:"dynamic,omitempty"`
	Source    string `json:"source"` // "arg" or "redirect"
	Err       string `json:"err,omitempty"`
}

// CommandReport is the analyzer's verdict material (docs/security.md s7).
type CommandReport struct {
	Confidence  Confidence   `json:"confidence"`
	Shell       ShellKind    `json:"shell"`
	Invocations []Invocation `json:"invocations"`
	Classes     []Class      `json:"classes"`
	Kinds       []string     `json:"kinds,omitempty"`    // approval kinds implied by matched rules
	Paths       []PathRef    `json:"paths,omitempty"`    // from the path guard
	HardDeny    []string     `json:"hardDeny,omitempty"` // reasons; any entry makes the command not approvable
	RuleIDs     []string     `json:"ruleIds,omitempty"`
	Reasons     []string     `json:"reasons,omitempty"`
	Digest      string       `json:"digest"` // goes into ActionDescriptor.ArgvDigest
}

// AllInside reports whether every path found stays inside the root and none is dynamic.
func (r CommandReport) AllInside() bool {
	for _, p := range r.Paths {
		if !p.Inside {
			return false
		}
	}
	return true
}

// HasClass reports whether c was found.
func (r CommandReport) HasClass(c Class) bool {
	for _, x := range r.Classes {
		if x == c {
			return true
		}
	}
	return false
}

// Analyzer analyzes a command (docs/security.md s7).
type Analyzer interface {
	Analyze(ctx context.Context, in CommandInput) (CommandReport, error)
}

// RuleAnalyzer is the product Analyzer: parsers per shell flavor, class tables from rules/*.yaml, the path guard.
type RuleAnalyzer struct {
	rules *RuleSet
	guard Guard
}

// AnalyzerOption configures NewAnalyzer.
type AnalyzerOption func(*RuleAnalyzer)

// WithGuard replaces the path guard (tests use a Windows-style guard without filesystem access).
func WithGuard(g Guard) AnalyzerOption { return func(a *RuleAnalyzer) { a.guard = g } }

// WithRules replaces the class tables.
func WithRules(rs *RuleSet) AnalyzerOption { return func(a *RuleAnalyzer) { a.rules = rs } }

// NewAnalyzer builds an analyzer over the embedded rule tables and the host path guard.
func NewAnalyzer(opts ...AnalyzerOption) (*RuleAnalyzer, error) {
	a := &RuleAnalyzer{guard: NewGuard()}
	for _, o := range opts {
		o(a)
	}
	if a.rules == nil {
		rs, err := LoadRules()
		if err != nil {
			return nil, err
		}
		a.rules = rs
	}
	return a, nil
}

// Rules returns the loaded tables.
func (a *RuleAnalyzer) Rules() *RuleSet { return a.rules }

var _ Analyzer = (*RuleAnalyzer)(nil)

// run is the state of one Analyze call.
type run struct {
	ctx  context.Context
	a    *RuleAnalyzer
	in   CommandInput
	rep  *CommandReport
	conf Confidence

	cwd      string
	cwdKnown bool
	root     string

	vars   map[string]string
	funcs  map[string]bool
	inCond int
	pipeIn bool
	depth  int // current script nesting (command substitutions, sub-expressions, nested scripts)

	unknownFlavor bool
	nonBashRule   bool
	stop          bool
	classSet      map[Class]bool
	kindSet       map[string]bool
	ruleSet       map[string]bool
	reasonSet     map[string]bool
	pathSeen      map[string]bool
	ctxErr        error
}

func (r *run) opaque(why string) {
	r.conf = Opaque
	r.reason("opaque: " + why)
}

func (r *run) lex(why string) {
	if r.conf > LexedConservative {
		r.conf = LexedConservative
	}
	r.reason(why)
}

func (r *run) reason(s string) {
	if !r.reasonSet[s] {
		r.reasonSet[s] = true
		r.rep.Reasons = append(r.rep.Reasons, s)
	}
}

func (r *run) hardDeny(s string) {
	for _, h := range r.rep.HardDeny {
		if h == s {
			return
		}
	}
	r.rep.HardDeny = append(r.rep.HardDeny, s)
}

// Analyze runs the pipeline of docs/security.md s7. Errors are only context cancellation; every unanalyzable input is
// a successful Opaque report.
func (a *RuleAnalyzer) Analyze(ctx context.Context, in CommandInput) (CommandReport, error) {
	rep := CommandReport{Confidence: ParsedExact, Shell: in.Shell}
	r := &run{ctx: ctx, a: a, in: in, rep: &rep, conf: ParsedExact, vars: map[string]string{}, funcs: map[string]bool{},
		classSet: map[Class]bool{}, kindSet: map[string]bool{}, ruleSet: map[string]bool{}, reasonSet: map[string]bool{}, pathSeen: map[string]bool{}}
	r.root = in.Root
	cwd := in.Cwd
	if cwd == "" {
		cwd = in.Root
	}
	r.cwd, r.cwdKnown = cwd, cwd != ""
	if r.cwdKnown && in.Root != "" {
		if _, err := a.guard.Resolve(in.Root, cwd); err != nil {
			// a working directory outside the root is allowed to exist; relative paths then resolve outside
			r.reason("working directory is outside the worktree")
		}
	}

	switch {
	case err0(ctx) != nil:
		return rep, ctx.Err()
	case len(in.Raw) > MaxCommandBytes:
		r.opaque(fmt.Sprintf("command longer than %d bytes", MaxCommandBytes))
	case strings.ContainsRune(in.Raw, 0):
		r.opaque("command contains a NUL byte")
	case strings.TrimSpace(in.Raw) == "":
		r.reason("empty command")
	default:
		shell := in.Shell
		switch shell {
		case ShellBash, ShellPowerShell, ShellCmd:
		default:
			shell = ShellUnknown
		}
		r.script(in.Raw, shell, 0)
	}
	if r.ctxErr != nil {
		return rep, r.ctxErr
	}
	if r.conf == Opaque {
		// The command could not be analyzed, so a credential file can only be noticed in its source text.
		lower := strings.ToLower(strings.ReplaceAll(in.Raw, `\`, "/"))
		for _, c := range a.rules.CredentialPaths {
			if strings.Contains(lower, c) {
				r.hardDeny("unanalyzable command mentions a known CLI credential file (" + c + ")")
			}
		}
	}
	r.finalize()
	return rep, nil
}

func err0(ctx context.Context) error { return ctx.Err() }

// script dispatches to a flavor. depth is the wrapper depth of the invocations it will add.
func (r *run) script(text string, shell ShellKind, depth int) {
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > maxScriptNesting {
		r.opaque("nesting too deep")
		return
	}
	if len(text) > MaxCommandBytes {
		r.opaque("nested script too long")
		return
	}
	switch shell {
	case ShellBash:
		r.posix(text, depth)
	case ShellPowerShell:
		r.lex("PowerShell is lexed by the phase A subset lexer, not parsed")
		r.powershell(text, depth)
	case ShellCmd:
		r.lex("cmd.exe is lexed, not parsed")
		r.cmdexe(text, depth)
	default:
		r.unknownFlavor = true
		r.posix(text, depth)
	}
}

func (r *run) finalize() {
	rep := r.rep
	if r.unknownFlavor && r.nonBashRule {
		r.lex("shell flavor unknown: PowerShell or cmd rules matched a POSIX parse")
	}
	rep.Confidence = r.conf
	for c := range r.classSet {
		rep.Classes = append(rep.Classes, c)
	}
	sort.Slice(rep.Classes, func(i, j int) bool { return rep.Classes[i] < rep.Classes[j] })
	for k := range r.kindSet {
		rep.Kinds = append(rep.Kinds, k)
	}
	sort.Strings(rep.Kinds)
	for k := range r.ruleSet {
		rep.RuleIDs = append(rep.RuleIDs, k)
	}
	sort.Strings(rep.RuleIDs)
	sort.Strings(rep.HardDeny)
	rep.Digest = digest(rep)
}

type digestInv struct {
	P string     `json:"p"`
	A []Arg      `json:"a"`
	R []Redirect `json:"r"`
	E string     `json:"e"`
	D int        `json:"d"`
	W bool       `json:"w"`
}

func digest(rep *CommandReport) string {
	invs := make([]digestInv, 0, len(rep.Invocations))
	for _, i := range rep.Invocations {
		invs = append(invs, digestInv{i.Program, i.Args, i.Redirects, i.Edge, i.Depth, i.Wrapper})
	}
	b, err := CanonicalJSON(map[string]any{"v": 1, "shell": rep.Shell, "invocations": invs, "classes": rep.Classes, "confidence": rep.Confidence.String()})
	if err != nil {
		panic("security: digest: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- program normalization ---

var versionedInterp = regexp.MustCompile(`^(python|pip|ruby|perl|php|node)[0-9][0-9.]*$`)

// normalizeProgram strips the directory and .exe/.cmd/.bat/.ps1/.com extension and case-folds (docs/security.md s7
// step 4). The PowerShell alias table is applied by the caller.
func normalizeProgram(raw string) string {
	s := raw
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ToLower(s)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1", ".com"} {
		if strings.HasSuffix(s, ext) && len(s) > len(ext) {
			s = strings.TrimSuffix(s, ext)
			break
		}
	}
	if versionedInterp.MatchString(s) && !strings.HasPrefix(s, "pip") {
		s = versionedInterpBase(s)
	}
	return s
}

func versionedInterpBase(s string) string {
	for _, b := range []string{"python", "ruby", "perl", "php", "node"} {
		if strings.HasPrefix(s, b) {
			if b == "python" && strings.HasPrefix(strings.TrimPrefix(s, b), "3") {
				return "python"
			}
			return b
		}
	}
	return s
}

func rawExt(raw string) string {
	s := raw
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndexByte(s, '.'); i > 0 {
		return strings.ToLower(s[i:])
	}
	return ""
}

// --- arguments ---

type parsedArgs struct {
	texts   []string          // every argument text, in order
	pos     []Arg             // non-flag arguments
	flags   map[string]bool   // exact-case flag names
	flagsCI map[string]bool   // lower-case flag names
	flagVal map[string]string // flag -> attached value
}

var cmdSwitch = regexp.MustCompile(`^/[A-Za-z?][A-Za-z0-9:?]*$`)
var shortCluster = regexp.MustCompile(`^-[A-Za-z0-9]{2,}$`)

// parseArgs splits arguments into flags and positionals for the invocation's flavor.
func parseArgs(inv *Invocation) *parsedArgs {
	pa := &parsedArgs{flags: map[string]bool{}, flagsCI: map[string]bool{}, flagVal: map[string]string{}}
	cmdlet := inv.Shell == ShellPowerShell && strings.Contains(inv.Program, "-")
	endOpts := false
	addFlag := func(f string) {
		pa.flags[f] = true
		pa.flagsCI[strings.ToLower(f)] = true
	}
	for _, a := range inv.Args {
		pa.texts = append(pa.texts, a.Text)
		t := a.Text
		if a.Dynamic || endOpts || t == "" {
			pa.pos = append(pa.pos, a)
			continue
		}
		switch {
		case t == "--" && inv.Shell != ShellCmd:
			endOpts = true
			addFlag("--")
		case inv.Shell == ShellCmd && cmdSwitch.MatchString(t):
			name, val, _ := strings.Cut(t, ":")
			addFlag(name)
			if val != "" {
				pa.flagVal[strings.ToLower(name)] = val
			}
		case strings.HasPrefix(t, "--") && len(t) > 2:
			name, val, has := strings.Cut(t, "=")
			addFlag(name)
			if has {
				pa.flagVal[name] = val
			}
		case strings.HasPrefix(t, "-") && len(t) > 1 && !isNumber(t[1:]):
			name := t
			if cmdlet || inv.Shell == ShellPowerShell {
				name, _, _ = strings.Cut(t, ":")
			} else if i := strings.IndexByte(t, '='); i > 0 {
				name = t[:i]
				pa.flagVal[name] = t[i+1:]
			}
			addFlag(name)
			if !cmdlet && shortCluster.MatchString(name) {
				for _, ch := range name[1:] {
					addFlag("-" + string(ch))
				}
			}
		default:
			pa.pos = append(pa.pos, a)
		}
	}
	return pa
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// valueOpts lists, per program, the flags that consume the next argument; they matter for finding the subcommand
// (git -C dir push). A program with an entry is "anchored": its subcommand is the first positional.
var valueOpts = map[string]map[string]bool{
	"git":       set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env", "--super-prefix"),
	"npm":       set("--prefix", "-w", "--workspace", "--userconfig", "--registry", "--cache"),
	"pnpm":      set("-C", "--dir", "--filter", "-F", "--workspace-root"),
	"yarn":      set("--cwd", "--cache-folder"),
	"bun":       set("--cwd"),
	"docker":    set("-H", "--host", "--context", "-c", "--config", "-l", "--log-level"),
	"podman":    set("--connection", "--url", "--root", "--runroot"),
	"kubectl":   set("-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user", "-s", "--server"),
	"gh":        set("-R", "--repo"),
	"terraform": set("-chdir"),
	"wrangler":  set("-c", "--config", "-e", "--env", "--cwd"),
	"cf":        set("-c", "--config", "-e", "--env"),
	"go":        set("-C"),
	"cargo":     set("--manifest-path", "-Z", "--config"),
	"dotnet":    set(),
	"aws":       set("--profile", "--region", "--endpoint-url", "--output", "--query"),
	"gcloud":    set("--project", "--account", "--configuration", "--format"),
	"az":        set("--subscription", "--output", "-o"),
	"helm":      set("-n", "--namespace", "--kube-context", "--kubeconfig"),
	"prisma":    set("--schema"),
	"pip":       set(),
	"pip3":      set(),
	"uv":        set("--directory", "--project"),
	"poetry":    set("-C", "--directory", "-P", "--project"),
	"vault":     set("-address", "-namespace"),
}

func set(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, i := range items {
		m[i] = true
	}
	return m
}

// leadingPositionals returns up to n leading positional words, skipping flags and the values of known value flags.
func leadingPositionals(inv *Invocation, ci bool, n int) []string {
	vo := valueOpts[inv.Program]
	var out []string
	skip := false
	for i, a := range inv.Args {
		if len(out) >= n {
			break
		}
		if skip {
			skip = false
			continue
		}
		t := a.Text
		if a.Dynamic {
			out = append(out, "\x00dynamic")
			continue
		}
		if strings.HasPrefix(t, "-") && len(t) > 1 {
			name := t
			attached := false
			if j := strings.IndexAny(t, "=:"); j > 0 {
				name = t[:j]
				attached = true
			}
			if vo != nil && vo[name] && !attached && i+1 < len(inv.Args) {
				skip = true
			}
			continue
		}
		if ci {
			t = strings.ToLower(t)
		}
		out = append(out, t)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- invocations ---

func (r *run) addInvocation(inv Invocation, depth int) {
	if r.stop {
		return
	}
	if err := r.ctx.Err(); err != nil {
		r.ctxErr = err
		r.stop = true
		return
	}
	if len(r.rep.Invocations) >= maxInvocations {
		r.opaque("too many invocations")
		r.stop = true
		return
	}
	if depth > maxWrapperDepth {
		r.opaque(fmt.Sprintf("wrappers nested deeper than %d", maxWrapperDepth))
		return
	}
	inv.Depth = depth
	inv.Raw = firstNonEmpty(inv.Raw, inv.Program)
	if inv.Shell == "" {
		inv.Shell = ShellBash
	}
	ext := rawExt(inv.Raw)
	inv.Program = normalizeProgram(inv.Raw)
	if inv.Shell == ShellPowerShell {
		if canon, ok := r.a.rules.Aliases[inv.Program]; ok {
			inv.Program = canon
		}
	}
	idx := len(r.rep.Invocations)
	r.rep.Invocations = append(r.rep.Invocations, inv)
	cur := &r.rep.Invocations[idx]

	// Script and batch files: content can change after analysis.
	if inv.Shell == ShellCmd && (ext == ".bat" || ext == ".cmd") {
		r.opaque("batch file invocation: its content can change after analysis")
	}
	if inv.Shell == ShellCmd && inv.Program == "call" {
		for _, a := range inv.Args {
			if strings.HasPrefix(a.Text, ":") {
				r.opaque("call :label")
			}
		}
	}

	if r.a.rules.OpaqueWrappers[inv.Program] {
		cur.Wrapper = true
		r.opaque("unknown wrapper " + inv.Program + ": the command it runs cannot be unwrapped")
	}
	if inv.Shell == ShellPowerShell && r.a.rules.OpaqueCmdlets[inv.Program] {
		r.opaque(inv.Program + " executes code that cannot be analyzed")
	}
	if inv.Shell == ShellPowerShell && inv.Program == "new-object" {
		for _, a := range inv.Args {
			if strings.EqualFold(strings.SplitN(a.Text, ":", 2)[0], "-comobject") {
				r.opaque("New-Object -ComObject creates objects with arbitrary behavior")
			}
		}
	}
	if inv.Shell == ShellCmd && inv.Program == "start" {
		r.opaque("start launches another process")
	}

	pa := parseArgs(cur)
	r.envChecks(cur, pa)
	if inv.Shell == ShellPowerShell {
		r.strictParams(cur)
	}

	// cd and friends change the working directory for later relative paths.
	r.trackCwd(cur, pa)

	// Wrappers: analyze what they run.
	uw := r.unwrap(cur, pa)
	switch uw.kind {
	case uwOpaque:
		cur.Wrapper = true
		r.opaque(uw.note)
	case uwArgv:
		cur.Wrapper = true
		if uw.note != "" {
			r.reason(uw.note)
		}
		for _, argv := range uw.argvs {
			r.runInner(cur, argv, uw, depth)
		}
	case uwScript:
		cur.Wrapper = true
		if uw.note != "" {
			r.reason(uw.note)
		}
		if depth+1 > maxWrapperDepth {
			r.opaque(fmt.Sprintf("wrappers nested deeper than %d", maxWrapperDepth))
		} else {
			saved := r.snapshotState()
			r.script(uw.script, uw.shell, depth+1)
			r.restoreCwd(saved)
		}
	case uwLookup:
		cur.Classes = []Class{ClassReadonly}
		r.classSet[ClassReadonly] = true
		r.collectPaths(cur, pa, nil, "none")
		return
	}

	var res classResult
	if _, launcher := wrapperSpecs[inv.Program]; launcher && uw.kind == uwArgv {
		// a transparent launcher (env, nohup, timeout, xargs, ...): what it runs is classified, not the launcher
		res.pathMode = "none"
	} else {
		res = r.classify(cur, pa)
	}
	cur.Classes = res.classes
	cur.Rules = res.ids
	for _, c := range res.classes {
		r.classSet[c] = true
	}
	for _, k := range res.kinds {
		r.kindSet[k] = true
	}
	for _, id := range res.ids {
		r.ruleSet[id] = true
	}
	for _, h := range res.hard {
		r.hardDeny(h)
	}
	if res.nonBash {
		r.nonBashRule = true
	}
	// Redirections that write.
	for _, rd := range cur.Redirects {
		if rd.Write {
			r.classSet[ClassWorkspaceWrite] = true
			cur.Classes = addClass(cur.Classes, ClassWorkspaceWrite)
		}
	}
	if scriptExts[ext] {
		// a script file run directly: its content is not visible here
		if !hasClass(cur.Classes, ClassExecCode) {
			cur.Classes = append(cur.Classes, ClassExecCode)
		}
		r.classSet[ClassExecCode] = true
	}
	if len(cur.Classes) == 0 && len(res.ids) == 0 && !cur.Wrapper && !cur.function {
		cur.Classes = []Class{ClassUnknown}
		r.classSet[ClassUnknown] = true
		if r.a.rules.IsKnownProgram(cur.Program) {
			r.reason("no rule matches the arguments of " + cur.Program)
		} else {
			r.reason("unclassified program " + cur.Program)
		}
	}
	r.collectPaths(cur, pa, res.matched, res.pathMode)
}

var scriptExts = set(".ps1", ".bat", ".cmd", ".sh", ".vbs", ".wsf", ".js", ".py")

func addClass(cs []Class, c Class) []Class {
	for _, x := range cs {
		if x == c {
			return cs
		}
	}
	return append(cs, c)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// runInner adds the invocation a wrapper runs.
func (r *run) runInner(parent *Invocation, argv []Arg, uw unwrapResult, depth int) {
	if len(argv) == 0 {
		return
	}
	if argv[0].Dynamic || argv[0].Glob {
		r.opaque("the command run by " + parent.Program + " is not a literal word")
		return
	}
	inner := Invocation{Raw: argv[0].Text, Args: argv[1:], Edge: parent.Edge, Shell: parent.Shell, StdinArgs: uw.stdin || parent.StdinArgs,
		unix: parent.unix || uw.unix, wsl: parent.wsl || uw.wsl, piped: parent.piped}
	if uw.shell != "" {
		inner.Shell = uw.shell
	}
	// the inner command is the same program text a shell would see
	r.addInvocation(inner, depth+1)
}

// --- classification ---

type classResult struct {
	classes  []Class
	kinds    []string
	hard     []string
	ids      []string
	matched  []*Rule
	pathMode string
	nonBash  bool
}

func (r *run) classify(inv *Invocation, pa *parsedArgs) classResult {
	var res classResult
	rank := map[string]int{"none": 1, "heuristic": 2, "positional": 3}
	best := ""
	seen := map[Class]bool{}
	for _, rule := range r.a.rules.candidates(inv.Program) {
		if !r.ruleApplies(rule, inv) {
			continue
		}
		if !matchRule(rule, inv, pa) {
			continue
		}
		res.matched = append(res.matched, rule)
		res.ids = append(res.ids, rule.ID)
		if len(rule.Shells) > 0 && !contains(rule.Shells, string(inv.Shell)) {
			res.nonBash = true
		}
		add := func(c Class) {
			if c != classHardDeny && !seen[c] {
				seen[c] = true
				res.classes = append(res.classes, c)
			}
		}
		add(rule.class)
		for _, c := range rule.Also {
			add(c)
		}
		if rule.Kind != "" {
			res.kinds = append(res.kinds, rule.Kind)
		}
		if rule.HardDeny {
			res.hard = append(res.hard, firstNonEmpty(rule.Reason, rule.ID)+" ["+rule.ID+"]")
		}
		p := rule.Paths
		if p == "" {
			p = "heuristic"
		}
		if rank[p] > rank[best] {
			best = p
		}
	}
	if best == "" {
		best = "heuristic"
	}
	res.pathMode = best
	sort.Slice(res.classes, func(i, j int) bool { return res.classes[i] < res.classes[j] })
	return res
}

func (r *run) ruleApplies(rule *Rule, inv *Invocation) bool {
	if len(rule.Shells) == 0 || contains(rule.Shells, string(inv.Shell)) {
		return true
	}
	// Unknown flavor: the command was parsed as POSIX, but a PowerShell or cmd rule for the same program still applies
	// (and caps confidence).
	return r.unknownFlavor && inv.Shell == ShellBash
}

func matchRule(rule *Rule, inv *Invocation, pa *parsedArgs) bool {
	matched := false
	for _, p := range rule.Programs {
		if p == inv.Program || (strings.HasSuffix(p, "*") && strings.HasPrefix(inv.Program, strings.TrimSuffix(p, "*"))) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	flags := pa.flags
	if rule.CI {
		flags = pa.flagsCI
	}
	nsub := 0
	if len(rule.Subcommands) > 0 {
		lead := leadingPositionals(inv, rule.CI, 6)
		_, anchored := valueOpts[inv.Program]
		ok := false
		for _, entry := range rule.Subcommands {
			toks := strings.Fields(entry)
			if matchSub(lead, toks, anchored) {
				ok = true
				nsub = len(toks)
				break
			}
		}
		if !ok {
			return false
		}
	}
	if rule.Bare && (len(pa.flags) > 0 || len(pa.pos) != nsub) {
		return false
	}
	if len(rule.FlagsAny) > 0 {
		ok := false
		for _, f := range rule.FlagsAny {
			if flags[f] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, grp := range rule.FlagsAll {
		ok := false
		for _, f := range grp {
			if flags[f] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, f := range rule.FlagsNone {
		if flags[f] {
			return false
		}
	}
	if len(rule.args) > 0 {
		ok := false
		for _, t := range pa.texts {
			for _, re := range rule.args {
				if re.MatchString(t) {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func matchSub(lead, toks []string, anchored bool) bool {
	if len(toks) == 0 {
		return true
	}
	if anchored {
		if len(lead) < len(toks) {
			return false
		}
		for i, t := range toks {
			if lead[i] != t {
				return false
			}
		}
		return true
	}
	// not anchored: the words appear in order among the leading positionals
	j := 0
	for _, l := range lead {
		if l == toks[j] {
			j++
			if j == len(toks) {
				return true
			}
		}
	}
	return false
}

package security

import (
	"regexp"
	"strings"
)

// hijackVars change what a later command resolves to or runs; assigning one makes the command Opaque.
var hijackVars = set("PATH", "PATHEXT", "LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "BASH_ENV", "ENV", "IFS", "SHELLOPTS", "BASHOPTS",
	"PROMPT_COMMAND", "PS4", "CDPATH", "GLOBIGNORE", "GIT_SSH_COMMAND", "GIT_SSH", "GIT_EXTERNAL_DIFF", "GIT_ASKPASS", "GIT_EXEC_PATH",
	"GIT_PAGER", "GIT_EDITOR", "GIT_PROXY_COMMAND", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "HOME", "XDG_CONFIG_HOME",
	"NODE_OPTIONS", "PYTHONSTARTUP", "PYTHONPATH", "PYTHONHOME", "RUBYOPT", "PERL5OPT", "PERL5LIB", "COMSPEC", "PSMODULEPATH", "EDITOR",
	"VISUAL", "PAGER", "BROWSER", "DOTNET_STARTUP_HOOKS", "JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "NPM_CONFIG_SCRIPT_SHELL")

func isHijackVar(name string) bool {
	u := strings.ToUpper(name)
	return hijackVars[u] || strings.HasPrefix(u, "DYLD_") || strings.HasPrefix(u, "GIT_CONFIG_KEY_") || strings.HasPrefix(u, "GIT_CONFIG_VALUE_")
}

// dangerousGitConfig are `git -c key=value` keys that make git execute a program.
var dangerousGitConfig = []string{"alias.", "core.pager", "core.editor", "core.sshcommand", "core.fsmonitor", "core.hookspath", "core.askpass",
	"diff.external", "credential.helper", "include.path", "includeif.", "filter.", "gpg.program", "gpg.", "sequence.editor", "protocol.ext.allow",
	"url.", "http.proxy", "merge.", "difftool.", "mergetool.", "pager.", "web.browser", "browser.", "man.", "receive.", "uploadpack.", "remote."}

func (r *run) envChecks(inv *Invocation, pa *parsedArgs) {
	for _, n := range inv.Env {
		if isHijackVar(n) {
			r.opaque("assigns " + n + ", which changes what runs")
		}
	}
	if inv.Program != "git" {
		return
	}
	for i := 0; i < len(inv.Args); i++ {
		t := inv.Args[i].Text
		if !strings.HasPrefix(t, "-") {
			break // first positional is the subcommand: later -c belongs to the subcommand
		}
		switch {
		case t == "-c" && i+1 < len(inv.Args):
			key := strings.ToLower(strings.SplitN(inv.Args[i+1].Text, "=", 2)[0])
			if inv.Args[i+1].Dynamic {
				r.opaque("git -c with a dynamic key")
			}
			for _, d := range dangerousGitConfig {
				if strings.HasPrefix(key, d) {
					r.opaque("git -c " + key + " can make git run a program")
				}
			}
			i++
		case t == "--exec-path" || strings.HasPrefix(t, "--exec-path=") || strings.HasPrefix(t, "--config-env"):
			r.opaque("git " + strings.SplitN(t, "=", 2)[0] + " changes what git runs")
		case t == "-C" || t == "--git-dir" || t == "--work-tree" || t == "--namespace":
			i++
		}
	}
}

// strictParams enforces the PowerShell rule that destructive cmdlets must spell their parameters in full.
func (r *run) strictParams(inv *Invocation) {
	for _, a := range inv.Args {
		if a.Text == "--%" {
			r.opaque("PowerShell stop-parsing token --%")
			return
		}
	}
	strict, ok := r.a.rules.StrictParams[inv.Program]
	if !ok {
		return
	}
	for _, a := range inv.Args {
		t := a.Text
		if a.Dynamic || len(t) < 2 || t[0] != '-' || isNumber(t[1:]) || strings.HasPrefix(t, "--") {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(strings.SplitN(t, ":", 2)[0], "-"))
		if strict[name] || r.a.rules.CommonParams[name] {
			continue
		}
		r.opaque("unrecognized or abbreviated parameter " + t + " on " + inv.Program)
		return
	}
}

// psValueParams take a value that is data, not a path; the argument after them is not checked as a path.
var psValueParams = set("value", "encoding", "pattern", "message", "separator", "delimiter", "text", "count", "itemtype", "tail", "first", "last", "skip",
	"depth", "width", "algorithm", "scope", "id", "property", "unique", "inputobject", "object", "milliseconds", "seconds", "uri", "method", "body", "headers", "contenttype")

// psContentSinks receive pipeline input as content, not as paths.
var psContentSinks = set("set-content", "add-content", "out-file", "tee-object", "export-csv", "export-clixml", "out-null")

var psPathParams = set("path", "literalpath", "destination", "filepath", "outfile", "infile", "directory", "workingdirectory")

var cdPrograms = set("cd", "chdir", "pushd", "popd", "set-location", "push-location", "pop-location")

func (r *run) trackCwd(inv *Invocation, pa *parsedArgs) {
	if !cdPrograms[inv.Program] {
		return
	}
	if inv.Program == "popd" || inv.Program == "pop-location" {
		r.cwdKnown = false
		return
	}
	if len(pa.pos) == 0 {
		if inv.Shell != ShellCmd {
			r.cwdKnown = false // cd with no argument goes home
		}
		return
	}
	t := pa.pos[0]
	if t.Dynamic || t.Glob || t.Text == "-" || strings.HasPrefix(t.Text, "~") || !r.cwdKnown {
		r.cwdKnown = false
		return
	}
	p := t.Text
	if inv.unix && r.a.guard.Windows {
		var ok bool
		if p, ok = unixToWindows(p, inv.wsl); !ok {
			r.cwdKnown = false
			return
		}
	}
	if next, ok := r.a.guard.joinClean(r.cwd, p); ok {
		r.cwd = next
	} else {
		r.cwdKnown = false
	}
}

type cwdState struct {
	cwd   string
	known bool
}

func (r *run) snapshotState() cwdState { return cwdState{r.cwd, r.cwdKnown} }
func (r *run) restoreCwd(s cwdState)   { r.cwd, r.cwdKnown = s.cwd, s.known }

// joinClean joins rel onto base lexically (no filesystem access). ok is false for forms whose meaning depends on
// process state (root-relative or drive-relative Windows paths).
func (g Guard) joinClean(base, rel string) (string, bool) {
	bp, ok1 := g.parse(base)
	rp, ok2 := g.parse(rel)
	if !ok1 || !ok2 || !bp.abs {
		return "", false
	}
	if g.Windows {
		n := strings.ReplaceAll(rel, "/", `\`)
		if strings.HasPrefix(n, `\`) || (len(n) >= 2 && n[1] == ':' && !strings.HasPrefix(n[2:], `\`)) {
			return "", false
		}
	}
	full := rp
	if !rp.abs {
		full = parsedPath{vol: bp.vol, abs: true, segs: append(append([]string{}, bp.segs...), rp.segs...)}
	}
	full.segs = g.cleanSegs(full.segs)
	return g.render(full), true
}

var msysDrive = regexp.MustCompile(`^/(?:mnt/|cygdrive/)?([a-zA-Z])(?:/(.*))?$`)

// unixToWindows maps Git Bash (/c/work) and WSL (/mnt/c/work) drive paths to Windows form. Other absolute Unix paths
// live outside any worktree drive mapping.
func unixToWindows(p string, wsl bool) (string, bool) {
	if !strings.HasPrefix(p, "/") {
		return p, true
	}
	m := msysDrive.FindStringSubmatch(p)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]) + `:\` + strings.ReplaceAll(m[2], "/", `\`), true
}

var (
	urlRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)
	scpLike  = regexp.MustCompile(`^[A-Za-z0-9_.\-]+@[A-Za-z0-9_.\-]+:`)
	keyValRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*=(.+)$`)
)

// looksLikePath is the heuristic for arguments of programs whose path arguments are not listed.
func looksLikePath(s string) bool {
	if s == "" || urlRE.MatchString(s) || scpLike.MatchString(s) {
		return false
	}
	if strings.HasPrefix(s, "~") || s == "." || s == ".." {
		return true
	}
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	return len(s) >= 2 && s[1] == ':' && isLetter(s[0])
}

var sinks = set("/dev/fd/procsubst", "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/tty", "/dev/zero", "/dev/urandom", "/dev/random", "nul", "$null", "nul:")

func isSink(s string) bool { return sinks[strings.ToLower(s)] }

// collectPaths sends every path-like argument and redirect target through the path guard.
func (r *run) collectPaths(inv *Invocation, pa *parsedArgs, matched []*Rule, mode string) {
	skip := 0
	if mode != "none" {
		for _, rule := range matched {
			if rule.SkipFirst {
				unless := false
				flags := pa.flags
				if rule.CI {
					flags = pa.flagsCI
				}
				for _, f := range rule.SkipUnless {
					if flags[f] {
						unless = true
					}
				}
				if !unless {
					skip = 1
				}
			}
		}
	}
	if mode != "none" {
		first := true
		prevValueParam := false
		for _, a := range inv.Args {
			t := a.Text
			isFlag := !a.Dynamic && strings.HasPrefix(t, "-") && len(t) > 1 && !isNumber(t[1:])
			if inv.Shell == ShellCmd && !a.Dynamic && cmdSwitch.MatchString(t) {
				isFlag = true
			}
			if isFlag {
				if inv.Shell == ShellPowerShell && !strings.ContainsAny(t, ":=") && psValueParams[strings.ToLower(strings.TrimPrefix(t, "-"))] {
					prevValueParam = true
				}
				// attached value of a flag: --out=/x, -Path:/x
				if _, v, ok := strings.Cut(t, "="); ok && looksLikePath(v) {
					r.addPath(inv, v, false, false, "arg")
				} else if name, v, ok := strings.Cut(t, ":"); ok && inv.Shell == ShellPowerShell {
					if looksLikePath(v) {
						r.addPath(inv, v, false, false, "arg")
					} else if strings.HasPrefix(v, "$") && psPathParams[strings.ToLower(strings.TrimPrefix(name, "-"))] {
						r.addPath(inv, v, true, false, "arg")
					}
				}
				continue
			}
			if a.Data {
				continue
			}
			if prevValueParam {
				prevValueParam = false
				continue
			}
			if first && skip == 1 {
				first = false
				skip = 0
				continue
			}
			first = false
			switch mode {
			case "positional":
				if a.Dynamic || (!urlRE.MatchString(t) && !scpLike.MatchString(t)) {
					r.addPath(inv, t, a.Dynamic, a.Glob, "arg")
				}
			default: // heuristic
				switch {
				case a.Dynamic:
					if looksLikePath(t) || strings.HasPrefix(t, "$HOME") || strings.HasPrefix(t, "${HOME") || strings.HasPrefix(t, "$env:") {
						r.addPath(inv, t, true, a.Glob, "arg")
					}
				default:
					if m := keyValRE.FindStringSubmatch(t); m != nil {
						if looksLikePath(m[1]) {
							r.addPath(inv, m[1], false, a.Glob, "arg")
						}
					} else if looksLikePath(t) {
						r.addPath(inv, t, false, a.Glob, "arg")
					}
				}
			}
		}
	}
	for _, rd := range inv.Redirects {
		if rd.Target == "" || (isFDTarget(rd.Op, rd.Target)) {
			continue
		}
		r.addPath(inv, rd.Target, rd.Dynamic, false, "redirect")
	}
	if inv.StdinArgs && mode == "positional" && (hasClass(inv.Classes, ClassDestructive) || (hasClass(inv.Classes, ClassWorkspaceWrite) && !psContentSinks[inv.Program])) {
		r.addPathRef(PathRef{Raw: "<arguments supplied at run time>", Dynamic: true, Source: "arg", Err: "arguments come from standard input"})
	}
}

func hasClass(cs []Class, c Class) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

func isFDTarget(op, target string) bool {
	if strings.Contains(op, "&") && (isNumber(target) || target == "-") {
		return true
	}
	return false
}

func (r *run) addPath(inv *Invocation, raw string, dyn, glob bool, source string) {
	r.addPathRef(r.resolvePath(inv, raw, dyn, glob, source))
}

func (r *run) addPathRef(ref PathRef) {
	key := ref.Source + "\x00" + ref.Raw
	if r.pathSeen[key] {
		return
	}
	r.pathSeen[key] = true
	r.rep.Paths = append(r.rep.Paths, ref)
}

func (r *run) resolvePath(inv *Invocation, raw string, dyn, glob bool, source string) PathRef {
	ref := PathRef{Raw: raw, Source: source}
	norm := strings.ReplaceAll(raw, `\`, "/")
	lower := strings.ToLower(norm)
	for _, c := range r.a.rules.CredentialPaths {
		if strings.Contains(lower, c) {
			r.hardDeny("access to a known CLI credential file (" + c + ")")
		}
	}
	if r.a.rules.IsSecretPath(norm) {
		r.classSet[ClassSecrets] = true
		r.kindSet[KindSecretsAccess] = true
		r.reason("path looks like secret material: " + norm)
	}
	if dyn {
		ref.Dynamic, ref.Err = true, "dynamic path"
		return ref
	}
	if isSink(raw) {
		ref.Inside, ref.Canonical = true, raw
		return ref
	}
	if r.root == "" {
		ref.Err = "no worktree root to check against"
		return ref
	}
	p := raw
	if glob {
		p = globStaticPrefix(p)
	}
	if strings.HasPrefix(p, "~") {
		ref.Err = "home directory"
		return ref
	}
	g := r.a.guard
	if inv.unix && g.Windows {
		var ok bool
		if p, ok = unixToWindows(p, inv.wsl); !ok {
			ref.Err = "Unix path outside the mapped drives"
			return ref
		}
	}
	abs := isAbsFor(g, p)
	if !abs {
		if !r.cwdKnown {
			ref.Dynamic, ref.Err = true, "working directory unknown"
			return ref
		}
		if r.cwd != r.root {
			joined, ok := g.joinClean(r.cwd, p)
			if !ok {
				ref.Err = "path form depends on process state"
				return ref
			}
			p = joined
		}
	}
	if p == "" {
		p = "."
	}
	canon, err := g.Resolve(r.root, p)
	if err != nil {
		ref.Err = err.Error()
		return ref
	}
	ref.Inside, ref.Canonical = true, canon
	return ref
}

func isAbsFor(g Guard, p string) bool {
	if g.Windows {
		n := strings.ReplaceAll(p, "/", `\`)
		return len(n) >= 3 && n[1] == ':' && n[2] == '\\' && isLetter(n[0])
	}
	return strings.HasPrefix(p, "/")
}

// globStaticPrefix returns the directory part of a glob pattern that precedes the first segment with a wildcard.
func globStaticPrefix(p string) string {
	norm := strings.ReplaceAll(p, `\`, "/")
	segs := strings.Split(norm, "/")
	var keep []string
	for _, s := range segs {
		if strings.ContainsAny(s, "*?[]{}") {
			break
		}
		keep = append(keep, s)
	}
	out := strings.Join(keep, "/")
	if len(out) == 2 && out[1] == ':' {
		out += "/"
	}
	if out == "" && strings.HasPrefix(norm, "/") {
		return "/"
	}
	if out == "" {
		return "."
	}
	return out
}

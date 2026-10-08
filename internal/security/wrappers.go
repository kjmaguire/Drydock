package security

import (
	"strings"
)

type uwKind int

const (
	uwNone   uwKind = iota
	uwArgv          // wrapper runs one or more inner commands given as argv
	uwScript        // wrapper runs script text in a shell flavor
	uwOpaque        // wrapper runs something that cannot be determined
	uwLookup        // command -v: only a lookup, nothing runs
)

type unwrapResult struct {
	kind   uwKind
	argvs  [][]Arg
	script string
	shell  ShellKind
	note   string
	stdin  bool // inner commands receive additional arguments at run time
	unix   bool
	wsl    bool
}

func opaqueWrap(note string) unwrapResult { return unwrapResult{kind: uwOpaque, note: note} }

// wrapperSpec describes a transparent launcher: flags that take a value, and positionals consumed before the command.
type wrapperSpec struct {
	valued      map[string]bool
	skipPos     int
	assigns     bool   // NAME=value words before the command (env)
	note        string // reason recorded when this wrapper is used
	stdinArgs   bool
	noCmdOK     bool // running with no command is harmless (env prints the environment)
	shortAttach bool // -nN style attached values (nice -n5)
}

var wrapperSpecs = map[string]wrapperSpec{
	"env":     {valued: set("-u", "--unset", "-C", "--chdir", "-S", "--split-string", "-a", "--argv0"), assigns: true, noCmdOK: true},
	"nohup":   {},
	"setsid":  {},
	"time":    {valued: set("-o", "--output", "-f", "--format")},
	"nice":    {valued: set("-n", "--adjustment")},
	"ionice":  {valued: set("-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid")},
	"stdbuf":  {valued: set("-i", "-o", "-e", "--input", "--output", "--error")},
	"timeout": {valued: set("-s", "--signal", "-k", "--kill-after"), skipPos: 1},
	"sudo":    {valued: set("-u", "-g", "-h", "-p", "-C", "-D", "-r", "-t", "-U", "-T", "-R", "--user", "--group", "--host", "--prompt", "--chdir"), note: "privilege escalation (sudo)"},
	"doas":    {valued: set("-u", "-C"), note: "privilege escalation (doas)"},
	"exec":    {valued: set("-a")},
	"builtin": {},
	"chronic": {},
	"xargs":   {valued: set("-I", "-n", "-P", "-L", "-s", "-d", "-E", "-a", "-J", "-R", "-S", "--max-args", "--max-procs", "--max-lines", "--max-chars", "--delimiter", "--arg-file", "--eof", "--replace-str"), stdinArgs: true},
}

// unwrap recognizes the known wrappers (docs/security.md s7 step 5) and returns what they run.
func (r *run) unwrap(inv *Invocation, pa *parsedArgs) unwrapResult {
	p := inv.Program
	switch p {
	case "command":
		for _, a := range inv.Args {
			if a.Text == "-v" || a.Text == "-V" {
				return unwrapResult{kind: uwLookup}
			}
		}
		return r.unwrapSpec(inv, wrapperSpec{valued: set()})
	case "env", "nohup", "setsid", "time", "nice", "ionice", "stdbuf", "timeout", "sudo", "doas", "exec", "builtin", "chronic", "xargs":
		return r.unwrapSpec(inv, wrapperSpecs[p])
	case "call":
		if inv.Shell == ShellCmd {
			return r.unwrapSpec(inv, wrapperSpec{valued: set()})
		}
	case "find":
		return unwrapFind(inv)
	case "trap":
		if len(inv.Args) > 0 && !strings.HasPrefix(inv.Args[0].Text, "-") {
			if inv.Args[0].Dynamic {
				return opaqueWrap("trap handler is not a literal string")
			}
			if inv.Args[0].Text == "" {
				return unwrapResult{}
			}
			return unwrapResult{kind: uwScript, script: inv.Args[0].Text, shell: ShellBash, note: "trap handler analyzed as a script"}
		}
	case "npx", "bunx", "uvx":
		return r.unwrapRunner(inv, 0, set("-p", "--package", "--registry", "--node-arg", "--from", "--with", "-w", "--workspace", "--python", "-c"), p == "npx")
	case "npm", "pnpm", "yarn", "bun", "uv", "poetry", "pipenv", "pipx":
		return r.unwrapSubcommandRunner(inv)
	case "cmd":
		return unwrapCmd(inv)
	case "powershell", "pwsh":
		return r.unwrapPowerShell(inv)
	case "bash", "sh", "zsh", "dash", "ksh", "ash", "fish", "tcsh", "csh":
		return r.unwrapShell(inv)
	case "wsl":
		return r.unwrapWSL(inv)
	}
	return unwrapResult{}
}

// unwrapSpec handles launchers of the form `wrapper [flags] [NAME=v ...] [positional ...] command args`.
func (r *run) unwrapSpec(inv *Invocation, sp wrapperSpec) unwrapResult {
	args := inv.Args
	i := 0
	skipPos := sp.skipPos
	var replace string
	for i < len(args) {
		t := args[i].Text
		if args[i].Dynamic {
			break
		}
		if t == "--" {
			i++
			break
		}
		if strings.HasPrefix(t, "-") && len(t) > 1 {
			name := t
			if j := strings.IndexByte(t, '='); j > 0 {
				name = t[:j]
			}
			if inv.Program == "env" && (name == "-S" || name == "--split-string") {
				return opaqueWrap("env -S splits a string into a command")
			}
			if inv.Program == "xargs" {
				switch {
				case strings.HasPrefix(t, "-I") && len(t) > 2:
					replace = t[2:]
				case t == "-I" || t == "--replace-str" || t == "-J":
					if i+1 < len(args) {
						replace = args[i+1].Text
					}
				case t == "-i" || t == "--replace":
					replace = "{}"
				case strings.HasPrefix(t, "-i") && len(t) > 2 && !strings.HasPrefix(t, "-in"):
					replace = t[2:]
				case strings.HasPrefix(t, "--replace="):
					replace = strings.TrimPrefix(t, "--replace=")
				}
			}
			if sp.valued[name] && !strings.Contains(t, "=") && i+1 < len(args) {
				i += 2
				continue
			}
			i++
			continue
		}
		if sp.assigns && isAssignWord(t) {
			name := t[:strings.IndexByte(t, '=')]
			if isHijackVar(name) {
				r.opaque("env assigns " + name + ", which changes what runs")
			}
			i++
			continue
		}
		if skipPos > 0 {
			skipPos--
			i++
			continue
		}
		break
	}
	rest := args[i:]
	if len(rest) == 0 {
		if sp.noCmdOK || inv.Program == "xargs" || inv.Program == "exec" || inv.Program == "nohup" {
			return unwrapResult{}
		}
		return opaqueWrap(inv.Program + " without a command")
	}
	res := unwrapResult{kind: uwArgv, argvs: [][]Arg{markReplace(rest, replace)}, note: sp.note, stdin: sp.stdinArgs}
	return res
}

func markReplace(argv []Arg, replace string) []Arg {
	if replace == "" {
		return argv
	}
	out := make([]Arg, len(argv))
	for i, a := range argv {
		if strings.Contains(a.Text, replace) {
			a.Dynamic = true
		}
		out[i] = a
	}
	return out
}

func isAssignWord(t string) bool {
	i := strings.IndexByte(t, '=')
	if i <= 0 {
		return false
	}
	for j, c := range t[:i] {
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (j > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// unwrapFind unwraps every -exec/-execdir/-ok/-okdir clause of find.
func unwrapFind(inv *Invocation) unwrapResult {
	var argvs [][]Arg
	args := inv.Args
	for i := 0; i < len(args); i++ {
		switch args[i].Text {
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			var clause []Arg
			for ; j < len(args); j++ {
				if !args[j].Dynamic && (args[j].Text == ";" || args[j].Text == "+") {
					break
				}
				a := args[j]
				if a.Text == "{}" {
					a.Dynamic = true
				}
				clause = append(clause, a)
			}
			if j >= len(args) {
				return opaqueWrap("find -exec clause is not terminated")
			}
			if len(clause) == 0 {
				return opaqueWrap("find -exec without a command")
			}
			argvs = append(argvs, clause)
			i = j
		}
	}
	if len(argvs) == 0 {
		return unwrapResult{}
	}
	return unwrapResult{kind: uwArgv, argvs: argvs, stdin: true, note: "find -exec commands analyzed"}
}

// unwrapRunner handles npx-style launchers: flags, then the package/command, then its args.
func (r *run) unwrapRunner(inv *Invocation, from int, valued map[string]bool, isNPX bool) unwrapResult {
	args := inv.Args[from:]
	i := 0
	for i < len(args) {
		t := args[i].Text
		if args[i].Dynamic {
			break
		}
		if t == "--" {
			i++
			break
		}
		if strings.HasPrefix(t, "-") && len(t) > 1 {
			if isNPX && (t == "-c" || t == "--call") && i+1 < len(args) {
				if args[i+1].Dynamic {
					return opaqueWrap("npx -c command is not literal")
				}
				return unwrapResult{kind: uwScript, script: args[i+1].Text, shell: platformShell(inv), note: "npx -c analyzed as a script"}
			}
			if valued[t] && !strings.Contains(t, "=") && i+1 < len(args) {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
	rest := args[i:]
	if len(rest) == 0 {
		return opaqueWrap(inv.Program + " without a command")
	}
	rest = append([]Arg{}, rest...)
	rest[0].Text = stripPackageVersion(rest[0].Text)
	return unwrapResult{kind: uwArgv, argvs: [][]Arg{rest}, note: inv.Program + " package executable analyzed"}
}

func platformShell(inv *Invocation) ShellKind {
	if inv.Shell == ShellPowerShell || inv.Shell == ShellCmd {
		return inv.Shell
	}
	return ShellBash
}

// stripPackageVersion turns "wrangler@latest" into "wrangler" and "@scope/pkg@1" into "pkg".
func stripPackageVersion(s string) string {
	if i := strings.LastIndexByte(s, '@'); i > 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "@") {
		if i := strings.IndexByte(s, '/'); i > 0 {
			s = s[i+1:]
		}
	}
	return s
}

var runnerSubcommands = map[string][]string{
	"npm":    {"exec", "x"},
	"pnpm":   {"dlx", "exec"},
	"yarn":   {"dlx", "exec"},
	"bun":    {"x"},
	"uv":     {"run", "tool run"},
	"poetry": {"run"},
	"pipenv": {"run"},
	"pipx":   {"run"},
}

func (r *run) unwrapSubcommandRunner(inv *Invocation) unwrapResult {
	subs := runnerSubcommands[inv.Program]
	lead := leadingPositionals(inv, false, 2)
	for _, s := range subs {
		toks := strings.Fields(s)
		if matchSub(lead, toks, true) {
			// drop everything up to and including the subcommand words
			vo := valueOpts[inv.Program]
			idx, seen, skip := 0, 0, false
			for i, a := range inv.Args {
				if skip {
					skip = false
					continue
				}
				if strings.HasPrefix(a.Text, "-") && len(a.Text) > 1 {
					if vo[a.Text] && i+1 < len(inv.Args) {
						skip = true
					}
					continue
				}
				seen++
				if seen == len(toks) {
					idx = i + 1
					break
				}
			}
			sub := *inv
			sub.Args = inv.Args[idx:]
			return r.unwrapRunner(&sub, 0, set("-p", "--package", "--from", "--with", "-w", "--workspace", "--python", "--registry", "-C", "--dir", "--cwd", "--directory"), false)
		}
	}
	return unwrapResult{}
}

// unwrapCmd: cmd [/d /s /q ...] /c <command line>.
func unwrapCmd(inv *Invocation) unwrapResult {
	for i, a := range inv.Args {
		if a.Dynamic {
			return opaqueWrap("cmd argument is not literal")
		}
		low := strings.ToLower(a.Text)
		if low == "/c" || low == "/k" || low == "/r" || (strings.HasPrefix(low, "/c") && len(low) > 2 && !strings.Contains(low, ":")) {
			var parts []string
			if len(low) > 2 {
				parts = append(parts, a.Text[2:])
			}
			for _, b := range inv.Args[i+1:] {
				if b.Dynamic {
					return opaqueWrap("cmd command line is not literal")
				}
				parts = append(parts, b.Text)
			}
			line := strings.Join(parts, " ")
			if strings.TrimSpace(line) == "" {
				return opaqueWrap("cmd without a command")
			}
			return unwrapResult{kind: uwScript, script: line, shell: ShellCmd, note: "cmd /c command line analyzed", unix: false}
		}
	}
	return opaqueWrap("cmd without /c: interactive or script from stdin")
}

var psExeParams = []string{"command", "file", "encodedcommand", "noprofile", "noninteractive", "nologo", "noexit", "executionpolicy", "windowstyle",
	"version", "sta", "mta", "inputformat", "outputformat", "configurationname", "workingdirectory", "settingsfile", "custompipename", "encodedarguments", "help", "login", "mta"}

var psValuedParams = set("executionpolicy", "windowstyle", "version", "inputformat", "outputformat", "configurationname", "workingdirectory", "settingsfile", "custompipename", "encodedarguments")

// resolvePSParam resolves a PowerShell.exe/pwsh parameter abbreviation. -c, -f, -e, -ec, -enc and the usual short
// spellings are honored the way the host honors them.
func resolvePSParam(name string) string {
	name = strings.ToLower(name)
	switch name {
	case "c":
		return "command"
	case "f":
		return "file"
	case "e", "ec", "en", "enc", "enco", "encod", "encode", "encoded", "encodedc", "encodedco", "encodedcom", "encodedcomm", "encodedcomma", "encodedcomman":
		return "encodedcommand"
	case "ep":
		return "executionpolicy"
	case "w":
		return "windowstyle"
	case "wd", "wo":
		return "workingdirectory"
	case "nop":
		return "noprofile"
	case "noni":
		return "noninteractive"
	case "nol":
		return "nologo"
	case "noe":
		return "noexit"
	case "if", "in":
		return "inputformat"
	case "of", "o":
		return "outputformat"
	}
	var hit string
	for _, p := range psExeParams {
		if strings.HasPrefix(p, name) {
			if hit != "" && hit != p {
				return ""
			}
			hit = p
		}
	}
	return hit
}

func (r *run) unwrapPowerShell(inv *Invocation) unwrapResult {
	args := inv.Args
	note := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.Text
		if a.Dynamic {
			return opaqueWrap("powershell argument is not literal")
		}
		if !(strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/")) || len(t) < 2 {
			// first non-parameter argument starts the command text
			return r.psPayload(inv, args[i:], note)
		}
		name, val, attached := strings.Cut(t[1:], ":")
		p := resolvePSParam(name)
		switch p {
		case "":
			return opaqueWrap("unrecognized powershell parameter " + t)
		case "encodedcommand":
			if attached && val != "" && !a.Dynamic {
				r.opaque("-EncodedCommand payload cannot be analyzed")
				return unwrapResult{kind: uwOpaque, note: "-EncodedCommand payload"}
			}
			if i+1 >= len(args) || args[i+1].Dynamic {
				r.hardDeny("-EncodedCommand with a non-literal payload")
			}
			return opaqueWrap("-EncodedCommand payload cannot be analyzed")
		case "command":
			if attached {
				return r.psPayload(inv, append([]Arg{{Text: val}}, args[i+1:]...), note)
			}
			if i+1 >= len(args) {
				return opaqueWrap("powershell -Command without a command")
			}
			if args[i+1].Text == "-" {
				return opaqueWrap("powershell reads the command from stdin")
			}
			return r.psPayload(inv, args[i+1:], note)
		case "file":
			// a script file: exec_code, content unknown to us
			return unwrapResult{}
		case "executionpolicy":
			v := val
			if !attached && i+1 < len(args) {
				v = args[i+1].Text
				i++
			}
			note = "execution policy override: " + v
			r.reason(note)
		default:
			if psValuedParams[p] && !attached && i+1 < len(args) {
				i++
			}
		}
	}
	return opaqueWrap("powershell without a command: interactive or stdin")
}

func (r *run) psPayload(inv *Invocation, rest []Arg, note string) unwrapResult {
	var parts []string
	for _, b := range rest {
		if b.Dynamic {
			return opaqueWrap("powershell command text is not literal")
		}
		parts = append(parts, b.Text)
	}
	text := strings.Join(parts, " ")
	if strings.TrimSpace(text) == "" {
		return opaqueWrap("powershell without a command")
	}
	return unwrapResult{kind: uwScript, script: text, shell: ShellPowerShell, note: firstNonEmpty(note, "powershell -Command text analyzed")}
}

// unwrapShell: bash -c 'script', bash -lc, sh script.sh, bash -s, ...
func (r *run) unwrapShell(inv *Invocation) unwrapResult {
	args := inv.Args
	c := false
	stdin := false
	i := 0
	for ; i < len(args); i++ {
		t := args[i].Text
		if args[i].Dynamic {
			return opaqueWrap(inv.Program + " argument is not literal")
		}
		if t == "--" {
			i++
			break
		}
		if t == "-o" || t == "+o" || t == "-O" || t == "+O" || t == "--rcfile" || t == "--init-file" {
			i++
			continue
		}
		if strings.HasPrefix(t, "--") {
			continue
		}
		if len(t) > 1 && (t[0] == '-' || t[0] == '+') {
			if strings.ContainsRune(t, 'c') {
				c = true
			}
			if strings.ContainsRune(t, 's') {
				stdin = true
			}
			continue
		}
		break
	}
	rest := args[i:]
	switch {
	case c:
		if len(rest) == 0 {
			return opaqueWrap(inv.Program + " -c without a command string")
		}
		if rest[0].Dynamic {
			return opaqueWrap(inv.Program + " -c command string is not literal")
		}
		return unwrapResult{kind: uwScript, script: rest[0].Text, shell: ShellBash, note: inv.Program + " -c script analyzed", unix: true}
	case stdin || (len(rest) == 0 && (inv.piped || inv.stdinData)):
		return opaqueWrap("script supplied on standard input to " + inv.Program)
	case len(rest) == 0:
		return opaqueWrap("interactive " + inv.Program)
	}
	return unwrapResult{} // runs a script file: exec_code
}

func (r *run) unwrapWSL(inv *Invocation) unwrapResult {
	args := inv.Args
	valued := set("-d", "--distribution", "-u", "--user", "--cd", "--shell-type", "--distribution-id")
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		t := a.Text
		if a.Dynamic {
			return opaqueWrap("wsl argument is not literal")
		}
		switch t {
		case "-l", "--list", "--status", "-v", "--version", "--help", "--set-default-version":
			return unwrapResult{kind: uwLookup}
		case "--unregister", "--shutdown", "--terminate", "-t", "--import", "--export", "--install", "--update", "--mount", "--unmount", "--set-version", "--manage", "--import-in-place":
			return opaqueWrap("wsl management command " + t)
		case "-e", "--exec", "--":
			i++
			goto done
		}
		if valued[t] {
			i++
			continue
		}
		if strings.HasPrefix(t, "-") && len(t) > 1 {
			continue
		}
		break
	}
done:
	rest := args[i:]
	if len(rest) == 0 {
		return opaqueWrap("interactive wsl")
	}
	return unwrapResult{kind: uwArgv, argvs: [][]Arg{rest}, unix: true, wsl: true, shell: ShellBash, note: "wsl command analyzed with Unix semantics"}
}

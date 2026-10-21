package security

import (
	"regexp"
	"strings"
)

// cmd.exe lexer (docs/security.md s7, "cmd.exe"): & && || | grouping, ^ escapes, quotes and redirections. Variable
// expansion (%VAR%, !VAR!), for /f, call :label and batch files make the statement Opaque.

var (
	cmdPercent = regexp.MustCompile(`%[^%]*%|%[0-9*~]`)
	cmdBang    = regexp.MustCompile(`![^!]+!`)
)

type cmdTok struct {
	kind  byte // 'w' word, 'o' operator (& && || |), '(' ')' , 'r' redirect operator
	text  string
	quote bool // contained a quoted segment
	mixed bool // quoted and unquoted text in one word, or a caret inside the word
}

func (r *run) cmdexe(text string, depth int) {
	if cmdPercent.MatchString(text) {
		r.opaque("cmd %VAR% expansion")
		return
	}
	if cmdBang.MatchString(text) {
		r.opaque("cmd !VAR! delayed expansion")
		return
	}
	toks, ok := r.cmdTokens(text)
	if !ok {
		return
	}
	p := &cmdParser{r: r, toks: toks, depth: depth}
	p.list(0, ";")
}

func (r *run) cmdTokens(text string) ([]cmdTok, bool) {
	var toks []cmdTok
	var cur strings.Builder
	var curTok cmdTok
	inWord := false
	groupDepth := 0
	stmtHead := ""
	prevWord := ""
	flush := func() {
		if inWord {
			curTok.kind, curTok.text = 'w', cur.String()
			if stmtHead == "" {
				stmtHead = strings.ToLower(curTok.text)
			}
			prevWord = strings.ToLower(curTok.text)
			toks = append(toks, curTok)
		}
		cur.Reset()
		curTok = cmdTok{}
		inWord = false
	}
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\n' || c == '\r':
			flush()
			toks = append(toks, cmdTok{kind: 'o', text: "&"})
			stmtHead, prevWord = "", ""
		case c == ' ' || c == '\t':
			flush()
		case c == '"':
			if inWord && !curTok.quote && cur.Len() > 0 {
				curTok.mixed = true
			}
			if inWord && curTok.quote {
				curTok.mixed = true
			}
			inWord = true
			curTok.quote = true
			i++
			for i < len(rs) && rs[i] != '"' {
				cur.WriteRune(rs[i])
				i++
			}
			// at the end of the input: cmd tolerates an unterminated quote, so the rest is quoted text
		case c == '^':
			if i+1 >= len(rs) {
				r.opaque("cmd trailing caret")
				return nil, false
			}
			if rs[i+1] == '\n' || rs[i+1] == '\r' {
				r.opaque("cmd caret line continuation")
				return nil, false
			}
			inWord = true
			curTok.mixed = curTok.mixed || cur.Len() == 0 || true
			i++
			cur.WriteRune(rs[i])
		case c == '&':
			flush()
			stmtHead, prevWord = "", ""
			if i+1 < len(rs) && rs[i+1] == '&' {
				toks = append(toks, cmdTok{kind: 'o', text: "&&"})
				i++
			} else {
				toks = append(toks, cmdTok{kind: 'o', text: "&"})
			}
		case c == '|':
			flush()
			stmtHead, prevWord = "", ""
			if i+1 < len(rs) && rs[i+1] == '|' {
				toks = append(toks, cmdTok{kind: 'o', text: "||"})
				i++
			} else {
				toks = append(toks, cmdTok{kind: 'o', text: "|"})
			}
		case c == '(' && !inWord && (stmtHead == "" || stmtHead == "if" || prevWord == "else" || prevWord == "do"):
			// a group: at the start of a command, or the command part of if/else/for
			toks = append(toks, cmdTok{kind: '('})
			groupDepth++
			stmtHead, prevWord = "", ""
		case c == ')' && groupDepth > 0:
			// inside a group an unquoted ) always closes it, even directly after a word
			flush()
			toks = append(toks, cmdTok{kind: ')'})
			groupDepth--
			stmtHead, prevWord = "", ""
		case c == '>' || c == '<':
			// a lone digit directly before the operator is the file descriptor
			fd := ""
			if inWord && !curTok.quote && len(cur.String()) == 1 && cur.String()[0] >= '0' && cur.String()[0] <= '9' {
				fd = cur.String()
				cur.Reset()
				curTok = cmdTok{}
				inWord = false
			} else {
				flush()
			}
			op := fd + string(c)
			for i+1 < len(rs) && (rs[i+1] == '>' || rs[i+1] == '&') && (c == '>') {
				op += string(rs[i+1])
				i++
				if rs[i] == '&' {
					break
				}
			}
			toks = append(toks, cmdTok{kind: 'r', text: op})
		default:
			if !inWord {
				inWord = true
			}
			cur.WriteRune(c)
		}
	}
	flush()
	return toks, true
}

type cmdParser struct {
	r     *run
	toks  []cmdTok
	i     int
	depth int
}

func (p *cmdParser) more() bool { return p.i < len(p.toks) }

// list parses commands separated by & && || until ')' (when grouped) or the end.
func (p *cmdParser) list(group int, edge string) {
	for p.more() && !p.r.stop {
		if p.toks[p.i].kind == ')' {
			if group > 0 {
				return
			}
			p.i++
			continue
		}
		if p.toks[p.i].kind == 'o' {
			op := p.toks[p.i].text
			p.i++
			if op == "&" {
				edge = ";"
			} else {
				edge = op
			}
			continue
		}
		p.pipeline(group, edge)
		edge = ";"
	}
}

func (p *cmdParser) pipeline(group int, edge string) {
	piped := false
	for {
		p.command(group, edge, piped)
		if p.more() && p.toks[p.i].kind == 'o' && p.toks[p.i].text == "|" {
			p.i++
			piped = true
			edge = "|"
			continue
		}
		return
	}
}

var cmdIfConds = set("exist", "defined", "errorlevel", "cmdextversion")

func (p *cmdParser) command(group int, edge string, piped bool) {
	r := p.r
	if !p.more() {
		return
	}
	t := p.toks[p.i]
	switch t.kind {
	case '(':
		p.i++
		r.cond(func() { p.list(group+1, edge) })
		if p.more() && p.toks[p.i].kind == ')' {
			p.i++
		}
		// else (...)
		if p.more() && p.toks[p.i].kind == 'w' && strings.EqualFold(p.toks[p.i].text, "else") {
			p.i++
			r.cond(func() { p.command(group, edge, false) })
		}
		return
	case 'o', ')':
		return
	case 'r':
		p.redirOnly(edge)
		return
	}
	// word: program
	p.i++
	prog := t.text
	if t.mixed {
		r.opaque("cmd quote or caret insertion inside the command name")
		p.skipToEnd()
		return
	}
	low := strings.ToLower(prog)
	switch low {
	case "rem", "::":
		p.skipToEnd()
		return
	case "goto":
		r.opaque("goto")
		p.skipToEnd()
		return
	case "for":
		r.opaque("for loops run commands over expanded values")
		p.skipToEnd()
		return
	case "if":
		p.ifCommand(group, edge)
		return
	case "@echo", "@":
	}
	if strings.HasPrefix(prog, ":") {
		r.opaque("batch label")
		p.skipToEnd()
		return
	}
	prog = strings.TrimPrefix(prog, "@")
	inv := Invocation{Raw: prog, Edge: edge, Shell: ShellCmd, piped: piped}
	p.args(&inv)
	if low == "set" {
		// set NAME=value only affects later %NAME% expansions, which are Opaque anyway
		if len(inv.Args) > 0 && !strings.HasPrefix(strings.ToLower(inv.Args[0].Text), "/") {
			if name, _, ok := strings.Cut(inv.Args[0].Text, "="); ok && isHijackVar(name) {
				r.opaque("set " + name + " changes what runs")
			}
		}
	}
	r.addInvocation(inv, p.depth)
}

func (p *cmdParser) skipToEnd() {
	for p.more() && p.toks[p.i].kind != 'o' && p.toks[p.i].kind != ')' {
		p.i++
	}
}

// redirOnly handles a statement that starts with a redirection (creates or truncates a file).
func (p *cmdParser) redirOnly(edge string) {
	inv := Invocation{Raw: ":", Edge: edge, Shell: ShellCmd}
	p.args(&inv)
	p.r.addInvocation(inv, p.depth)
}

// ifCommand: if [/i] [not] (exist X | defined X | errorlevel N | A==B | A op B) command
func (p *cmdParser) ifCommand(group int, edge string) {
	r := p.r
	next := func() (cmdTok, bool) {
		if !p.more() {
			return cmdTok{}, false
		}
		t := p.toks[p.i]
		p.i++
		return t, true
	}
	t, ok := next()
	if ok && t.kind == 'w' && strings.EqualFold(t.text, "/i") {
		t, ok = next()
	}
	if ok && t.kind == 'w' && strings.EqualFold(t.text, "not") {
		t, ok = next()
	}
	if !ok || t.kind != 'w' {
		r.opaque("cmd if condition")
		p.skipToEnd()
		return
	}
	lowc := strings.ToLower(t.text)
	switch {
	case cmdIfConds[lowc]:
		if _, ok := next(); !ok {
			r.opaque("cmd if condition")
			return
		}
		if lowc == "exist" {
			// the tested path is read-only access; record it as a path
			prev := p.toks[p.i-1]
			r.addInvocationPathOnly(prev.text, p.depth)
		}
	case strings.Contains(t.text, "=="):
		// A==B in one word
	default:
		// A == B or A EQU B
		op, ok1 := next()
		_, ok2 := next()
		if !ok1 || !ok2 || (op.text != "==" && !cmdCompare[strings.ToLower(op.text)]) {
			r.opaque("cmd if condition")
			p.skipToEnd()
			return
		}
	}
	r.cond(func() { p.command(group, edge, false) })
	if p.more() && p.toks[p.i].kind == 'w' && strings.EqualFold(p.toks[p.i].text, "else") {
		p.i++
		r.cond(func() { p.command(group, edge, false) })
	}
}

var cmdCompare = set("equ", "neq", "lss", "leq", "gtr", "geq")

func (r *run) addInvocationPathOnly(path string, depth int) {
	inv := Invocation{Raw: "if-exist", Program: "if-exist", Shell: ShellCmd, Args: []Arg{{Text: path}}}
	r.addInvocation(inv, depth)
}

// args reads arguments and redirections until an operator or ')' of a group.
func (p *cmdParser) args(inv *Invocation) {
	for p.more() {
		t := p.toks[p.i]
		switch t.kind {
		case 'o', ')':
			return
		case '(':
			// a '(' inside arguments (after the program) is plain text for tools like echo; treat as word
			p.i++
			inv.Args = append(inv.Args, Arg{Text: "("})
		case 'r':
			p.i++
			op := t.text
			if strings.HasSuffix(op, "&") {
				if p.more() && p.toks[p.i].kind == 'w' && isNumber(p.toks[p.i].text) {
					inv.Redirects = append(inv.Redirects, Redirect{Op: op, Target: p.toks[p.i].text})
					p.i++
					continue
				}
			}
			if !p.more() || p.toks[p.i].kind != 'w' {
				p.r.opaque("cmd redirection without a target")
				return
			}
			target := p.toks[p.i].text
			p.i++
			write := strings.Contains(op, ">")
			inv.Redirects = append(inv.Redirects, Redirect{Op: op, Target: target, Write: write && !isSink(target)})
		default:
			p.i++
			text := t.text
			// split /s/q style switch runs
			if !t.quote && strings.HasPrefix(text, "/") && strings.Count(text, "/") > 1 && cmdSwitchRun.MatchString(text) {
				for _, part := range strings.Split(strings.TrimPrefix(text, "/"), "/") {
					inv.Args = append(inv.Args, Arg{Text: "/" + part})
				}
				continue
			}
			inv.Args = append(inv.Args, Arg{Text: text, Glob: !t.quote && strings.ContainsAny(text, "*?")})
		}
	}
}

var cmdSwitchRun = regexp.MustCompile(`^(/[A-Za-z?][A-Za-z0-9:?]*){2,}$`)

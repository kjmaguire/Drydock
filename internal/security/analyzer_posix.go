package security

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// posix analyzes POSIX shell text (Git Bash, WSL, sh) with mvdan.cc/sh/v3 (docs/security.md s7, "POSIX").
func (r *run) posix(text string, depth int) {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	f, err := p.Parse(strings.NewReader(text), "")
	if err != nil {
		var pe syntax.ParseError
		if errors.As(err, &pe) {
			r.opaque(fmt.Sprintf("shell parse error at %d:%d", pe.Pos.Line(), pe.Pos.Col()))
		} else {
			r.opaque("shell parse error")
		}
		return
	}
	r.stmts(f.Stmts, depth, ";")
}

func (r *run) stmts(list []*syntax.Stmt, depth int, edge string) {
	for i, st := range list {
		if r.stop {
			return
		}
		e := edge
		if i > 0 {
			e = ";"
		}
		r.stmt(st, depth, e)
	}
}

// sub runs fn as a subshell: working directory and variables do not leak out.
func (r *run) sub(fn func()) {
	saved := r.snapshotState()
	vars := copyVars(r.vars)
	fn()
	r.restoreCwd(saved)
	r.vars = vars
}

// cond runs fn for code that may or may not execute (a branch, a loop body, a function body): any change it makes to
// the working directory or to variables is unknown afterwards.
func (r *run) cond(fn func()) {
	saved := r.snapshotState()
	before := copyVars(r.vars)
	r.inCond++
	fn()
	r.inCond--
	if r.cwd != saved.cwd || r.cwdKnown != saved.known {
		r.cwdKnown = false
	}
	for k, v := range r.vars {
		if bv, ok := before[k]; !ok || bv != v {
			delete(r.vars, k)
		}
	}
	for k := range before {
		if _, ok := r.vars[k]; !ok {
			delete(r.vars, k)
		}
	}
}

func copyVars(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r *run) stmt(st *syntax.Stmt, depth int, edge string) {
	if st == nil || r.stop {
		return
	}
	if err := r.ctx.Err(); err != nil {
		r.ctxErr = err
		r.stop = true
		return
	}
	if st.Background {
		r.reason("background execution")
	}
	redirs := r.redirects(st.Redirs, depth)
	switch c := st.Cmd.(type) {
	case nil:
		if len(redirs) > 0 {
			r.addInvocation(Invocation{Raw: ":", Program: ":", Redirects: redirs, Edge: edge, Shell: ShellBash, unix: r.a.guard.Windows}, depth)
		}
	case *syntax.CallExpr:
		r.call(c, redirs, depth, edge)
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt:
			r.stmt(c.X, depth, edge)
			r.cond(func() { r.stmt(c.Y, depth, "&&") })
		case syntax.OrStmt:
			r.stmt(c.X, depth, edge)
			r.cond(func() { r.stmt(c.Y, depth, "||") })
		default: // | and |&
			r.sub(func() { r.stmt(c.X, depth, edge) })
			r.sub(func() {
				r.pipeIn = true
				r.stmt(c.Y, depth, "|")
				r.pipeIn = false
			})
		}
		r.attachRedirects(redirs, depth, edge)
	case *syntax.Subshell:
		r.sub(func() { r.stmts(c.Stmts, depth, edge) })
		r.attachRedirects(redirs, depth, edge)
	case *syntax.Block:
		r.stmts(c.Stmts, depth, edge)
		r.attachRedirects(redirs, depth, edge)
	case *syntax.IfClause:
		r.ifClause(c, depth, edge)
		r.attachRedirects(redirs, depth, edge)
	case *syntax.WhileClause:
		r.cond(func() {
			r.stmts(c.Cond, depth, edge)
			r.stmts(c.Do, depth, ";")
		})
		r.attachRedirects(redirs, depth, edge)
	case *syntax.ForClause:
		r.cond(func() {
			switch l := c.Loop.(type) {
			case *syntax.WordIter:
				for _, w := range l.Items {
					r.scanWord(w, depth)
				}
				delete(r.vars, l.Name.Value)
			case *syntax.CStyleLoop:
				r.scanNode(l, depth)
			}
			r.stmts(c.Do, depth, edge)
		})
		r.attachRedirects(redirs, depth, edge)
	case *syntax.CaseClause:
		r.scanWord(c.Word, depth)
		r.cond(func() {
			for _, it := range c.Items {
				for _, p := range it.Patterns {
					r.scanWord(p, depth)
				}
				r.stmts(it.Stmts, depth, edge)
			}
		})
		r.attachRedirects(redirs, depth, edge)
	case *syntax.FuncDecl:
		names := []*syntax.Lit{c.Name}
		names = append(names, c.Names...)
		for _, n := range names {
			if n != nil {
				r.funcs[n.Value] = true
			}
		}
		r.cond(func() { r.stmt(c.Body, depth, edge) })
	case *syntax.DeclClause:
		r.decl(c, depth)
	case *syntax.TimeClause:
		r.stmt(c.Stmt, depth, edge)
	case *syntax.CoprocClause:
		r.reason("coprocess")
		r.stmt(c.Stmt, depth, edge)
	case *syntax.LetClause, *syntax.ArithmCmd, *syntax.TestClause:
		r.scanNode(c, depth)
	default:
		r.opaque(fmt.Sprintf("unsupported shell construct %T", c))
	}
}

func (r *run) ifClause(c *syntax.IfClause, depth int, edge string) {
	r.stmts(c.Cond, depth, edge)
	r.cond(func() { r.stmts(c.Then, depth, ";") })
	if c.Else != nil {
		r.cond(func() { r.ifClause(c.Else, depth, ";") })
	}
}

// attachRedirects records the redirections of a compound command as an invocation of ":" so that their paths are
// checked and their writes classified.
func (r *run) attachRedirects(redirs []Redirect, depth int, edge string) {
	if len(redirs) == 0 {
		return
	}
	r.addInvocation(Invocation{Raw: ":", Program: ":", Redirects: redirs, Edge: edge, Shell: ShellBash, unix: r.a.guard.Windows}, depth)
}

func (r *run) decl(c *syntax.DeclClause, depth int) {
	for _, a := range c.Args {
		if a.Name != nil && isHijackVar(a.Name.Value) {
			r.opaque("assigns " + a.Name.Value + ", which changes what runs")
		}
		if a.Value != nil {
			r.scanWord(a.Value, depth)
		}
		if a.Array != nil {
			r.scanNode(a.Array, depth)
		}
	}
}

// call analyzes a simple command.
func (r *run) call(c *syntax.CallExpr, redirs []Redirect, depth int, edge string) {
	if len(c.Args) == 0 {
		// bare assignments
		for _, a := range c.Assigns {
			r.assign(a, depth, true)
		}
		if len(redirs) > 0 {
			r.addInvocation(Invocation{Raw: ":", Program: ":", Redirects: redirs, Edge: edge, Shell: ShellBash, unix: r.a.guard.Windows}, depth)
		}
		return
	}
	var env []string
	for _, a := range c.Assigns {
		r.assign(a, depth, false)
		if a.Name != nil {
			env = append(env, a.Name.Value)
		}
	}
	var args []Arg
	for i, w := range c.Args {
		args = append(args, r.evalWord(w, i == 0, depth)...)
	}
	if len(args) == 0 {
		return
	}
	piped := r.pipeIn
	r.pipeIn = false
	head := args[0]
	if head.Dynamic {
		r.opaque("command name is not a literal word: " + truncate(head.Text, 60))
		return
	}
	if head.Glob {
		r.opaque("command name contains a glob")
		return
	}
	switch head.Text {
	case "eval":
		r.opaque("eval runs constructed text")
		return
	case "source", ".":
		r.opaque(head.Text + " runs a script whose content is not known")
		return
	}
	stdinData := false
	for _, rd := range redirs {
		if rd.Op == "<<" || rd.Op == "<<-" || rd.Op == "<<<" {
			stdinData = true
		}
	}
	inv := Invocation{Raw: head.Text, Args: args[1:], Redirects: redirs, Edge: edge, Shell: ShellBash, Env: env, piped: piped, stdinData: stdinData,
		unix: r.a.guard.Windows}
	if r.funcs[head.Text] {
		inv.function = true
	}
	r.addInvocation(inv, depth)
}

// assign records NAME=value when the value is a literal known now and the assignment is unconditional.
func (r *run) assign(a *syntax.Assign, depth int, persistent bool) {
	if a.Name == nil {
		return
	}
	name := a.Name.Value
	if isHijackVar(name) {
		r.opaque("assigns " + name + ", which changes what runs")
	}
	if a.Array != nil {
		r.scanNode(a.Array, depth)
		delete(r.vars, name)
		return
	}
	if a.Value == nil {
		delete(r.vars, name)
		return
	}
	text, dyn, _ := r.evalPlain(a.Value, false, depth)
	if !persistent {
		return
	}
	if dyn || a.Append || a.Index != nil || r.inCond > 0 {
		delete(r.vars, name)
		return
	}
	r.vars[name] = text
}

// redirects evaluates redirections.
func (r *run) redirects(list []*syntax.Redirect, depth int) []Redirect {
	var out []Redirect
	for _, rd := range list {
		op := rd.Op.String()
		if rd.Hdoc != nil {
			r.scanWord(rd.Hdoc, depth)
		}
		if rd.Word == nil {
			if rd.Op == syntax.Hdoc || rd.Op == syntax.DashHdoc {
				out = append(out, Redirect{Op: op})
			}
			continue
		}
		args := r.evalWord(rd.Word, false, depth)
		if rd.Op == syntax.Hdoc || rd.Op == syntax.DashHdoc || rd.Op == syntax.WordHdoc {
			out = append(out, Redirect{Op: op})
			continue
		}
		write := false
		switch rd.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob, syntax.AppClob, syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
			write = true
		}
		if len(args) != 1 {
			out = append(out, Redirect{Op: op, Target: "<ambiguous redirect>", Dynamic: true, Write: write})
			continue
		}
		a := args[0]
		if (rd.Op == syntax.DplOut || rd.Op == syntax.DplIn) && (isNumber(a.Text) || a.Text == "-") {
			out = append(out, Redirect{Op: op, Target: a.Text})
			continue
		}
		if rd.Op == syntax.DplOut || rd.Op == syntax.DplIn {
			write = rd.Op == syntax.DplOut
		}
		out = append(out, Redirect{Op: op, Target: a.Text, Dynamic: a.Dynamic || a.Glob, Write: write && !isSink(a.Text)})
	}
	return out
}

// evalWord evaluates a word into arguments (brace expansion may produce several). cmdName words never substitute
// variables, so a variable in command position is always dynamic.
func (r *run) evalWord(w *syntax.Word, cmdName bool, depth int) []Arg {
	words := []*syntax.Word{w}
	if wordHasBrace(w) {
		syntax.SplitBraces(w)
		words = words[:0]
		n := 0
		for bw, err := range expand.BracesSeq(&expand.Config{}, w) {
			if err != nil || n >= maxBraceExpand {
				d := r.printWord(w)
				r.scanWord(w, depth)
				return []Arg{{Text: d, Dynamic: true, Glob: true}}
			}
			words = append(words, bw)
			n++
		}
	}
	var out []Arg
	for _, x := range words {
		text, dyn, glob := r.evalPlain(x, cmdName, depth)
		out = append(out, Arg{Text: text, Dynamic: dyn, Glob: glob && !dyn})
	}
	return out
}

func wordHasBrace(w *syntax.Word) bool {
	for _, p := range w.Parts {
		if l, ok := p.(*syntax.Lit); ok && strings.ContainsRune(l.Value, '{') {
			return true
		}
	}
	return false
}

func (r *run) printWord(n syntax.Node) string {
	var sb strings.Builder
	_ = syntax.NewPrinter().Print(&sb, n)
	return sb.String()
}

// evalPlain evaluates one word without brace expansion.
func (r *run) evalPlain(w *syntax.Word, cmdName bool, depth int) (text string, dynamic, glob bool) {
	if len(w.Parts) == 1 {
		if ps, ok := w.Parts[0].(*syntax.ProcSubst); ok {
			// <(cmd) is a /dev/fd path; the command inside is analyzed
			r.scanNode(ps, depth)
			return "/dev/fd/procsubst", false, false
		}
	}
	var sb strings.Builder
	for i, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			s, g := unescapeUnquoted(x.Value)
			if i == 0 || true {
				sb.WriteString(s)
			}
			glob = glob || g
		case *syntax.SglQuoted:
			if x.Dollar {
				s, ok := decodeANSIC(x.Value)
				if !ok {
					dynamic = true
				}
				sb.WriteString(s)
			} else {
				sb.WriteString(x.Value)
			}
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				switch y := q.(type) {
				case *syntax.Lit:
					sb.WriteString(unescapeDouble(y.Value))
				case *syntax.ParamExp:
					if v, ok := r.literalParam(y, cmdName); ok {
						sb.WriteString(v)
					} else {
						dynamic = true
						r.scanNode(y, depth)
					}
				default:
					dynamic = true
					r.scanNode(q, depth)
				}
			}
		case *syntax.ParamExp:
			if v, ok := r.literalParam(x, cmdName); ok {
				sb.WriteString(v)
			} else {
				dynamic = true
				r.scanNode(x, depth)
			}
		case *syntax.ExtGlob:
			glob = true
			sb.WriteString(r.printWord(x))
		default:
			dynamic = true
			r.scanNode(p, depth)
		}
	}
	if dynamic {
		return r.printWord(w), true, false
	}
	return sb.String(), false, glob
}

// literalParam substitutes $name / ${name} when name was assigned a literal earlier (arguments only).
func (r *run) literalParam(p *syntax.ParamExp, cmdName bool) (string, bool) {
	if cmdName || p.Param == nil || p.Excl || p.Length || p.Width || p.IsSet || p.Index != nil || p.Slice != nil || p.Repl != nil || p.Exp != nil || p.NestedParam != nil {
		return "", false
	}
	v, ok := r.vars[p.Param.Value]
	return v, ok
}

// scanWord looks inside a word for command and process substitutions.
func (r *run) scanWord(w *syntax.Word, depth int) {
	if w == nil {
		return
	}
	r.scanNode(w, depth)
}

// scanNode finds substitutions anywhere under n and analyzes the commands they run.
func (r *run) scanNode(n syntax.Node, depth int) {
	if n == nil {
		return
	}
	syntax.Walk(n, func(x syntax.Node) bool {
		switch s := x.(type) {
		case *syntax.CmdSubst:
			r.sub(func() { r.stmts(s.Stmts, depth, ";") })
			return false
		case *syntax.ProcSubst:
			r.sub(func() { r.stmts(s.Stmts, depth, ";") })
			return false
		}
		return true
	})
}

// unescapeUnquoted removes backslash escapes from unquoted literal text and reports whether an unescaped glob
// character is present.
func unescapeUnquoted(s string) (string, bool) {
	var sb strings.Builder
	glob := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			if s[i] != '\n' {
				sb.WriteByte(s[i])
			}
		case c == '\\':
		default:
			if c == '*' || c == '?' || c == '[' {
				glob = true
			}
			sb.WriteByte(c)
		}
	}
	return sb.String(), glob
}

// unescapeDouble removes the escapes that are special inside double quotes.
func unescapeDouble(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '$', '`', '"', '\\':
				i++
				sb.WriteByte(s[i])
				continue
			case '\n':
				i++
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// decodeANSIC decodes the common escapes of $'...'. Unsupported escapes make the word dynamic.
func decodeANSIC(s string) (string, bool) {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			sb.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case 'a':
			sb.WriteByte(7)
		case 'b':
			sb.WriteByte(8)
		case 'f':
			sb.WriteByte(12)
		case 'v':
			sb.WriteByte(11)
		case 'e', 'E':
			sb.WriteByte(27)
		case '\\', '\'', '"', '?':
			sb.WriteByte(s[i])
		case 'x':
			j := i + 1
			for j < len(s) && j < i+3 && isHex(s[j]) {
				j++
			}
			if j == i+1 {
				return s, false
			}
			v, _ := strconv.ParseUint(s[i+1:j], 16, 8)
			sb.WriteByte(byte(v))
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			v, _ := strconv.ParseUint(s[i:j], 8, 16)
			sb.WriteByte(byte(v))
			i = j - 1
		default:
			return s, false
		}
	}
	return sb.String(), true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

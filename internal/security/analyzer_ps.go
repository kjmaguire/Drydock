package security

import (
	"regexp"
	"strings"
	"unicode"
)

// PowerShell, phase A (docs/security.md s7): a conservative lexer for a small subset. It returns LexedConservative only
// for statements wholly inside the subset; anything else makes the whole command Opaque. There is no PowerShell parser
// in Go; phase B (out-of-process Parser.ParseInput) is not implemented here.

var psKeywords = set("if", "elseif", "else", "foreach", "for", "while", "do", "switch", "function", "filter", "param", "try", "catch", "finally", "trap",
	"return", "break", "continue", "throw", "exit", "class", "enum", "using", "workflow", "data", "begin", "process", "end", "until", "parallel",
	"sequence", "inlinescript", "configuration", "dynamicparam")

var psStaticAllow = regexp.MustCompile(`(?i)^\[(system\.)?io\.path\]::(combine|getfilename|getfilenamewithoutextension|getdirectoryname|getextension)\(`)

type psLexer struct {
	r      *run
	s      []rune
	i      int
	depth  int
	failed bool
}

func (r *run) powershell(text string, depth int) {
	l := &psLexer{r: r, s: []rune(text), depth: depth}
	l.statements()
}

func (l *psLexer) eof() bool { return l.i >= len(l.s) }

func (l *psLexer) peek(k int) rune {
	if l.i+k < len(l.s) {
		return l.s[l.i+k]
	}
	return 0
}

func (l *psLexer) fail(why string) {
	if !l.failed {
		l.r.opaque("PowerShell: " + why)
	}
	l.failed = true
}

func isPSSpace(c rune) bool { return c == ' ' || c == '\t' || c == ' ' || c == '\f' || c == '\v' }

// skipSpaces skips blanks, comments and (reporting Opaque) backtick line continuations. It stops at newlines.
func (l *psLexer) skipSpaces() {
	for !l.eof() {
		c := l.peek(0)
		switch {
		case isPSSpace(c):
			l.i++
		case c == '`':
			if n := l.peek(1); n == '\n' || n == '\r' {
				l.fail("backtick line continuation")
				l.i += 2
			} else {
				return
			}
		case c == '#':
			for !l.eof() && l.peek(0) != '\n' {
				l.i++
			}
		case c == '<' && l.peek(1) == '#':
			end := strings.Index(string(l.s[l.i+2:]), "#>")
			if end < 0 {
				l.fail("unterminated block comment")
				l.i = len(l.s)
				return
			}
			l.i += 2 + len([]rune(string(l.s[l.i+2:])[:end])) + 2
		default:
			return
		}
	}
}

func (l *psLexer) skipNewlinesAndSpaces() {
	for {
		l.skipSpaces()
		if !l.eof() && (l.peek(0) == '\n' || l.peek(0) == '\r') {
			l.i++
			continue
		}
		return
	}
}

// statements parses statements until the end of the text.
func (l *psLexer) statements() {
	edge := ";"
	for {
		if l.r.stop || l.failed {
			return
		}
		l.skipNewlinesAndSpaces()
		if l.eof() {
			return
		}
		if l.peek(0) == ';' {
			l.i++
			continue
		}
		l.pipeline(edge)
		if l.failed {
			return
		}
		l.skipSpaces()
		if l.eof() {
			return
		}
		switch c := l.peek(0); {
		case c == '\n' || c == '\r' || c == ';':
			edge = ";"
		case c == '&' && l.peek(1) == '&':
			l.i += 2
			edge = "&&"
		case c == '|' && l.peek(1) == '|':
			l.i += 2
			edge = "||"
		case c == '}' || c == ')':
			l.fail("unbalanced " + string(c))
			return
		default:
			l.fail("unexpected character " + string(c))
			return
		}
	}
}

// pipeline parses command ('|' command)*.
func (l *psLexer) pipeline(edge string) {
	piped := false
	for {
		l.command(piped, edge)
		if l.r.stop || l.failed {
			return
		}
		l.skipSpaces()
		if !l.eof() && l.peek(0) == '|' && l.peek(1) != '|' {
			l.i++
			piped = true
			edge = "|"
			l.skipNewlinesAndSpaces()
			continue
		}
		return
	}
}

// command parses one command: its program and arguments.
func (l *psLexer) command(piped bool, edge string) {
	l.skipSpaces()
	if l.eof() {
		return
	}
	c := l.peek(0)
	var prog string
	switch {
	case c == '&' && l.peek(1) != '&':
		l.i++
		l.skipSpaces()
		if l.peek(0) == '{' {
			inner, ok := l.capture('{', '}')
			if !ok {
				l.fail("unterminated script block")
				return
			}
			l.r.script(inner, ShellPowerShell, l.depth)
			return
		}
		p, ok := l.commandName(true)
		if !ok {
			return
		}
		prog = p
	case c == '.' && isPSSpace(l.peek(1)):
		l.i++
		l.skipSpaces()
		p, ok := l.commandName(true)
		if !ok {
			return
		}
		prog = p
		l.r.reason("dot-sourced script: " + p)
	case c == '$':
		l.fail("variable in command position (assignment or $x invocation)")
		return
	case c == '[' && psStaticAllow.MatchString(string(l.s[l.i:])):
		loc := psStaticAllow.FindStringIndex(string(l.s[l.i:]))
		l.i += len([]rune(string(l.s[l.i:])[:loc[1]-1]))
		inner, ok := l.capture('(', ')')
		if !ok {
			l.fail("unterminated static call")
			return
		}
		l.r.script(inner, ShellPowerShell, l.depth)
		return
	case c == '(' || c == '[' || c == '{' || c == '@' || c == '<' || c == '>':
		l.fail("expression in command position")
		return
	case c == '|' || c == ')' || c == '}' || c == ',':
		l.fail("unexpected " + string(c))
		return
	default:
		p, ok := l.commandName(false)
		if !ok {
			return
		}
		prog = p
		if !piped && psKeywords[strings.ToLower(prog)] {
			l.fail("language construct '" + prog + "' is outside the analyzed subset")
			return
		}
	}

	inv := Invocation{Raw: prog, Edge: edge, Shell: ShellPowerShell, piped: piped, StdinArgs: piped}
	l.args(&inv)
	if l.r.stop || l.failed {
		return
	}
	// pipe targets must be known commands
	if piped {
		canon := normalizeProgram(prog)
		if a, ok := l.r.a.rules.Aliases[canon]; ok {
			canon = a
		}
		if !l.r.a.rules.PipelineCmdlets[canon] && !l.r.a.rules.IsKnownProgram(canon) {
			l.fail("pipe into unknown command " + canon)
		}
	}
	l.r.addInvocation(inv, l.depth)
}

// commandName reads the program word. Quote insertion, backticks, variables and wildcards make it unanalyzable.
func (l *psLexer) commandName(afterCall bool) (string, bool) {
	c := l.peek(0)
	if isPSQuote(c) {
		if !afterCall {
			l.fail("quoted string in command position")
			return "", false
		}
	}
	w, ok := l.word(false)
	if !ok {
		return "", false
	}
	if w.dyn || w.backtick {
		l.fail("command name is not a literal word")
		return "", false
	}
	if w.mixedQuote {
		l.fail("quote insertion inside the command name")
		return "", false
	}
	if strings.ContainsAny(w.text, "*?") && !afterCall {
		l.fail("wildcard in command name")
		return "", false
	}
	if strings.ContainsAny(w.text, "*?") {
		l.fail("wildcard in command name")
		return "", false
	}
	if w.text == "" {
		l.fail("empty command name")
		return "", false
	}
	return w.text, true
}

type psWord struct {
	text       string
	dyn        bool
	glob       bool
	quoted     bool
	mixedQuote bool
	backtick   bool
}

func isPSQuote(c rune) bool {
	switch c {
	case '\'', '"', '‘', '’', '‚', '‛', '“', '”', '„':
		return true
	}
	return false
}

func isPSSingleQuote(c rune) bool {
	return c == '\'' || c == '‘' || c == '’' || c == '‚' || c == '‛'
}

func isPSWordDelim(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '<', '>', '(', ')', '{', '}', ',', 0, ' ':
		return true
	}
	return unicode.IsSpace(c)
}

// word reads one bare word, merging adjacent quoted segments. argPos allows wildcard characters to set glob.
func (l *psLexer) word(argPos bool) (psWord, bool) {
	var sb strings.Builder
	var w psWord
	segments := 0
	bare := false
	for !l.eof() {
		c := l.peek(0)
		switch {
		case isPSQuote(c):
			txt, dyn, ok := l.quoted()
			if !ok {
				return w, false
			}
			sb.WriteString(txt)
			w.quoted = true
			segments++
			if dyn {
				w.dyn = true
			}
			if bare || segments > 1 {
				w.mixedQuote = true
			}
			continue
		case c == '`':
			if n := l.peek(1); n == '\n' || n == '\r' || n == 0 {
				l.fail("backtick line continuation")
				return w, false
			}
			w.backtick, w.dyn = true, true
			l.i += 2
			bare = true
		case c == '$':
			w.dyn = true
			bare = true
			if !l.variable(&sb) {
				return w, false
			}
		case c == '@' && sb.Len() == 0 && (l.peek(1) == '(' || l.peek(1) == '{'):
			// handled by the caller before calling word
			return w, true
		case isPSWordDelim(c):
			return finishWord(&w, &sb), true
		default:
			if c == '*' || c == '?' || c == '[' {
				w.glob = true
			}
			sb.WriteRune(c)
			bare = true
			l.i++
		}
	}
	return finishWord(&w, &sb), true
}

func finishWord(w *psWord, sb *strings.Builder) psWord {
	w.text = sb.String()
	if w.quoted && w.mixedQuote {
		// bare and quoted segments are merged by PowerShell in argument mode
	}
	return *w
}

// variable consumes $name, ${name}, $(...), $env:NAME, $_, $?, $$ and appends the source form.
func (l *psLexer) variable(sb *strings.Builder) bool {
	start := l.i
	l.i++ // $
	switch c := l.peek(0); {
	case c == '(':
		inner, ok := l.capture('(', ')')
		if !ok {
			l.fail("unterminated $( )")
			return false
		}
		l.r.script(inner, ShellPowerShell, l.depth)
		sb.WriteString(string(l.s[start:l.i]))
	case c == '{':
		_, ok := l.capture('{', '}')
		if !ok {
			l.fail("unterminated ${ }")
			return false
		}
		sb.WriteString(string(l.s[start:l.i]))
	default:
		for !l.eof() {
			c := l.peek(0)
			if c == '_' || c == ':' || c == '?' || c == '$' || c == '^' || unicode.IsLetter(c) || unicode.IsDigit(c) {
				l.i++
				continue
			}
			break
		}
		sb.WriteString(string(l.s[start:l.i]))
	}
	return true
}

// quoted reads a quoted string starting at the opening quote.
func (l *psLexer) quoted() (text string, dyn bool, ok bool) {
	open := l.peek(0)
	l.i++
	var sb strings.Builder
	if isPSSingleQuote(open) {
		for !l.eof() {
			c := l.peek(0)
			if isPSSingleQuote(c) {
				if isPSSingleQuote(l.peek(1)) {
					sb.WriteRune(c)
					l.i += 2
					continue
				}
				l.i++
				return sb.String(), false, true
			}
			sb.WriteRune(c)
			l.i++
		}
		l.fail("unterminated string")
		return "", false, false
	}
	for !l.eof() {
		c := l.peek(0)
		switch {
		case c == '"' || c == '“' || c == '”' || c == '„':
			if l.peek(1) == '"' || l.peek(1) == '“' || l.peek(1) == '”' {
				sb.WriteRune(c)
				l.i += 2
				continue
			}
			l.i++
			return sb.String(), dyn, true
		case c == '`':
			dyn = true
			l.i += 2
		case c == '$':
			n := l.peek(1)
			if n == '(' || n == '{' || n == '_' || n == '?' || n == ':' || n == '$' || unicode.IsLetter(n) {
				dyn = true
				if !l.variable(&sb) {
					return "", false, false
				}
				continue
			}
			sb.WriteRune(c)
			l.i++
		default:
			sb.WriteRune(c)
			l.i++
		}
	}
	l.fail("unterminated string")
	return "", false, false
}

// capture returns the text between a balanced pair starting at the opener, skipping over quoted strings.
func (l *psLexer) capture(open, close rune) (string, bool) {
	if l.peek(0) != open {
		return "", false
	}
	start := l.i + 1
	depth := 0
	for i := l.i; i < len(l.s); i++ {
		c := l.s[i]
		switch {
		case c == '`':
			i++
		case isPSQuote(c):
			dq := !isPSSingleQuote(c)
			for i++; i < len(l.s); i++ {
				d := l.s[i]
				if dq && d == '`' {
					i++
					continue
				}
				if dq && (d == '"' || d == '“' || d == '”') || !dq && isPSSingleQuote(d) {
					break
				}
			}
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				inner := string(l.s[start:i])
				l.i = i + 1
				return inner, true
			}
		}
	}
	return "", false
}

// args reads the arguments and redirections of a command.
func (l *psLexer) args(inv *Invocation) {
	for {
		if l.r.stop || l.failed {
			return
		}
		l.skipSpaces()
		if l.eof() {
			return
		}
		c := l.peek(0)
		switch {
		case c == '\n' || c == '\r' || c == ';' || c == '|':
			return
		case c == '&':
			if l.peek(1) == '&' {
				return
			}
			l.fail("background job operator &")
			return
		case c == ')' || c == '}':
			return
		case c == ',':
			l.i++
			continue
		case c == '>' || (c == '<' && l.peek(1) != '#'):
			if !l.redirect(inv) {
				return
			}
		case (isPSDigit(c) || c == '*') && l.isNumberedRedirect():
			if !l.redirect(inv) {
				return
			}
		case c == '{':
			inner, ok := l.capture('{', '}')
			if !ok {
				l.fail("unterminated script block")
				return
			}
			l.r.script(inner, ShellPowerShell, l.depth)
			inv.Args = append(inv.Args, Arg{Text: "{" + truncate(inner, 80) + "}", Dynamic: true})
		case c == '(':
			inner, ok := l.capture('(', ')')
			if !ok {
				l.fail("unterminated parenthesis")
				return
			}
			l.r.script(inner, ShellPowerShell, l.depth)
			inv.Args = append(inv.Args, Arg{Text: "(" + truncate(inner, 80) + ")", Dynamic: true})
		case c == '@':
			switch n := l.peek(1); {
			case n == '(':
				inner, ok := l.capture2('(', ')', 1)
				if !ok {
					l.fail("unterminated @( )")
					return
				}
				l.r.script(inner, ShellPowerShell, l.depth)
				inv.Args = append(inv.Args, Arg{Text: "@(" + truncate(inner, 80) + ")", Dynamic: true})
			case n == '{':
				inner, ok := l.capture2('{', '}', 1)
				if !ok {
					l.fail("unterminated @{ }")
					return
				}
				l.scanSubexprs(inner)
				inv.Args = append(inv.Args, Arg{Text: "@{" + truncate(inner, 80) + "}", Dynamic: true})
			case n == '\'' || n == '"':
				txt, dyn, ok := l.hereString()
				if !ok {
					return
				}
				inv.Args = append(inv.Args, Arg{Text: txt, Dynamic: dyn})
			default:
				l.fail("splatting or array operator")
				return
			}
		case c == '[':
			rest := string(l.s[l.i:])
			if loc := psStaticAllow.FindStringIndex(rest); loc != nil {
				l.i += len([]rune(rest[:loc[1]-1]))
				inner, ok := l.capture('(', ')')
				if !ok {
					l.fail("unterminated static call")
					return
				}
				l.r.script(inner, ShellPowerShell, l.depth)
				inv.Args = append(inv.Args, Arg{Text: "[IO.Path]::...(" + truncate(inner, 60) + ")", Dynamic: true})
				continue
			}
			l.fail("type literal or static member access")
			return
		default:
			w, ok := l.word(true)
			if !ok {
				return
			}
			if l.i < len(l.s) && l.peek(0) == '@' && w.text == "" {
				continue
			}
			if w.text == "" && !w.quoted && !w.dyn {
				l.fail("unparseable argument")
				return
			}
			dyn := w.dyn
			if dyn && strings.HasPrefix(w.text, "-") && strings.Contains(w.text, ":") && !strings.HasPrefix(w.text, "-$") {
				dyn = false // -Param:$value: the parameter name is literal, its value is checked as a path argument
			}
			inv.Args = append(inv.Args, Arg{Text: w.text, Dynamic: dyn, Glob: w.glob && !w.quoted && !dyn && !strings.HasPrefix(w.text, "-")})
		}
	}
}

// capture2 is capture for an opener that follows `skip` prefix characters.
func (l *psLexer) capture2(open, close rune, skip int) (string, bool) {
	l.i += skip
	inner, ok := l.capture(open, close)
	return inner, ok
}

func isPSDigit(c rune) bool { return c >= '0' && c <= '9' }

// isNumberedRedirect: 2> , 2>> , 2>&1 , *> , *>> at the current position.
func (l *psLexer) isNumberedRedirect() bool {
	j := l.i
	for j < len(l.s) && (isPSDigit(l.s[j]) || l.s[j] == '*') {
		j++
	}
	return j > l.i && j < len(l.s) && l.s[j] == '>'
}

func (l *psLexer) redirect(inv *Invocation) bool {
	start := l.i
	for !l.eof() && (isPSDigit(l.peek(0)) || l.peek(0) == '*') {
		l.i++
	}
	op := string(l.s[start:l.i])
	if l.peek(0) == '<' {
		l.fail("the < operator is reserved in PowerShell")
		return false
	}
	for !l.eof() && l.peek(0) == '>' {
		op += ">"
		l.i++
	}
	if l.peek(0) == '&' {
		l.i++
		d := l.i
		for !l.eof() && isPSDigit(l.peek(0)) {
			l.i++
		}
		inv.Redirects = append(inv.Redirects, Redirect{Op: op + "&", Target: string(l.s[d:l.i])})
		return true
	}
	l.skipSpaces()
	w, ok := l.word(true)
	if !ok {
		return false
	}
	if w.text == "" && !w.dyn {
		l.fail("redirection without a target")
		return false
	}
	target := w.text
	dyn := w.dyn && !strings.EqualFold(target, "$null")
	inv.Redirects = append(inv.Redirects, Redirect{Op: op, Target: target, Dynamic: dyn || w.glob, Write: !isSink(target)})
	return true
}

// hereString reads @'...'@ or @"..."@ (the body starts on the next line).
func (l *psLexer) hereString() (string, bool, bool) {
	q := l.peek(1)
	j := l.i + 2
	for j < len(l.s) && (l.s[j] == ' ' || l.s[j] == '\t') {
		j++
	}
	if j >= len(l.s) || (l.s[j] != '\n' && l.s[j] != '\r') {
		l.fail("here-string header must end the line")
		return "", false, false
	}
	rest := l.s[j:]
	end := -1
	for k := 0; k+2 < len(rest); k++ {
		if rest[k] == '\n' && rest[k+1] == q && rest[k+2] == '@' {
			end = k
			break
		}
	}
	if end < 0 {
		l.fail("unterminated here-string")
		l.i = len(l.s)
		return "", false, false
	}
	text := strings.ReplaceAll(string(rest[:end]), "\r\n", "\n")
	text = strings.TrimPrefix(strings.TrimPrefix(text, "\r"), "\n")
	l.i = j + end + 3
	dyn := false
	if q == '"' {
		for k, ch := range text {
			if ch == '$' && k+1 < len(text) {
				n := rune(text[k+1])
				if n == '(' || n == '{' || n == '_' || n == '?' || unicode.IsLetter(n) {
					dyn = true
				}
			}
			if ch == '`' {
				dyn = true
			}
		}
		if dyn {
			l.scanSubexprs(text)
		}
	}
	return text, dyn, true
}

// scanSubexprs finds $( ... ) sub-expressions inside text and analyzes them.
func (l *psLexer) scanSubexprs(text string) {
	rs := []rune(text)
	for i := 0; i+1 < len(rs); i++ {
		if rs[i] == '$' && rs[i+1] == '(' {
			sub := &psLexer{r: l.r, s: rs, i: i + 1, depth: l.depth}
			inner, ok := sub.capture('(', ')')
			if !ok {
				l.fail("unterminated $( ) in text")
				return
			}
			l.r.script(inner, ShellPowerShell, l.depth)
			i = sub.i - 1
		}
	}
}

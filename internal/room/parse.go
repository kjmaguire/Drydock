package room

import (
	"regexp"
	"strings"
)

// maskIgnored returns body with the regions that must not be parsed replaced by spaces, preserving byte offsets and
// newlines: fenced code blocks, inline code, block-quote lines and <untrusted ...> blocks (room-protocol s3).
func maskIgnored(body string) string {
	lines := strings.Split(body, "\n")
	fence := ""
	untrusted := false
	for i, ln := range lines {
		trim := strings.TrimLeft(ln, " \t")
		ignore := false
		switch {
		case fence != "":
			ignore = true
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			fence = trim[:3]
			ignore = true
		case untrusted:
			ignore = true
			if strings.Contains(ln, "</untrusted>") {
				untrusted = false
			}
		case strings.HasPrefix(trim, "<untrusted"):
			ignore = true
			if !strings.Contains(ln, "</untrusted>") {
				untrusted = true
			}
		case strings.HasPrefix(trim, ">"):
			ignore = true
		}
		if ignore {
			lines[i] = blank(ln)
			continue
		}
		lines[i] = maskInlineCode(ln)
	}
	return strings.Join(lines, "\n")
}

func blank(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\r' {
			b[i] = ' '
		}
	}
	return string(b)
}

func maskInlineCode(ln string) string {
	if !strings.Contains(ln, "`") {
		return ln
	}
	b := []byte(ln)
	in := false
	for i := range b {
		if b[i] == '`' {
			in = !in
			b[i] = ' '
			continue
		}
		if in {
			b[i] = ' '
		}
	}
	return string(b) // an unterminated span masks the rest of the line, which is the safe direction
}

// markerNames are the four line-start markers.
var markerNames = []string{"DECISION", "TODO", "STATUS", "RESULT"}

// maxMarkers bounds how many markers one message may carry (and so how many structured messages it can spawn).
const maxMarkers = 10

// ParseMarkers finds [DECISION] [TODO] [STATUS] [RESULT] at the very start of a line. It is deterministic and ignores
// markers inside code, quotes and untrusted blocks. Text is the rest of the line.
func ParseMarkers(body string) []Marker {
	var out []Marker
	masked := strings.Split(maskIgnored(body), "\n")
	orig := strings.Split(body, "\n")
	for i, ln := range masked {
		if len(out) >= maxMarkers {
			break
		}
		for _, name := range markerNames {
			tag := "[" + name + "]"
			if strings.HasPrefix(ln, tag) {
				text := strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(orig[i], tag), "\r"))
				text = strings.TrimSpace(strings.TrimPrefix(text, ":"))
				out = append(out, Marker{Marker: name, Line: i + 1, Text: text})
				break
			}
		}
	}
	return out
}

// RawMention is a syntactic mention before resolution.
type RawMention struct {
	Sigil string // "@", "@role:", "#", "^"
	Name  string // text after the sigil
	Span  [2]int // byte offsets of the whole token in the body
}

var mentionRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_@#^/.:+=-])(@role:[A-Za-z][A-Za-z0-9_-]*|@[A-Za-z0-9_][A-Za-z0-9_.-]*|#[a-z0-9][a-z0-9_-]*|\^msg_[A-Za-z0-9]+)`)

// ParseMentions finds @handle, @role:Name, #room-name and ^msg_id tokens outside code, quotes and untrusted blocks.
// A handle never ends in "." or "-" (sentence punctuation). Each token is returned once, in order of first appearance.
func ParseMentions(body string) []RawMention {
	masked := maskIgnored(body)
	var out []RawMention
	seen := map[string]bool{}
	for _, m := range mentionRE.FindAllStringSubmatchIndex(masked, -1) {
		start, end := m[2], m[3]
		tok := masked[start:end]
		tok = strings.TrimRight(tok, ".-")
		end = start + len(tok)
		if len(tok) < 2 || seen[strings.ToLower(tok)] {
			continue
		}
		seen[strings.ToLower(tok)] = true
		rm := RawMention{Span: [2]int{start, end}}
		switch {
		case strings.HasPrefix(tok, "@role:"):
			rm.Sigil, rm.Name = "@role:", strings.TrimPrefix(tok, "@role:")
		case tok[0] == '@':
			rm.Sigil, rm.Name = "@", tok[1:]
		case tok[0] == '#':
			rm.Sigil, rm.Name = "#", tok[1:]
		default:
			rm.Sigil, rm.Name = "^", tok[1:]
		}
		out = append(out, rm)
	}
	return out
}

// handleOf normalizes a display name or id to the form used after "@": lower case, spaces become "-".
func handleOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), "-")
}

// lineAfter returns the rest of the line following byte offset end in body, trimmed.
func lineAfter(body string, end int) string {
	if end > len(body) {
		return ""
	}
	rest := body[end:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// slug makes a short lower-case dash-separated name part.
func slug(s string, max int) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	return out
}

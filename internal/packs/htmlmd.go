package packs

import (
	"html"
	"regexp"
	"strings"
)

// HTMLToMarkdown is a small, dependency-free converter for documentation pages. It keeps headings, paragraphs, lists,
// inline code, code blocks and table cells, drops scripts, styles, navigation and other chrome, prefers the <main> or
// <article> element when there is one, and returns the page title. It is not a general HTML parser: malformed or exotic
// markup degrades to plain text rather than failing, and nothing in the page is ever executed.
func HTMLToMarkdown(src string) (title, md string) {
	if m := titleRe.FindStringSubmatch(src); m != nil {
		title = strings.TrimSpace(html.UnescapeString(stripTags(m[1])))
	}
	for _, tag := range []string{"main", "article"} {
		if inner, ok := innerOf(src, tag); ok {
			src = inner
			break
		}
	}
	src = commentRe.ReplaceAllString(src, "")
	for _, tag := range []string{"script", "style", "noscript", "svg", "template", "nav", "header", "footer", "aside", "form", "head", "iframe", "button"} {
		src = removeElement(src, tag)
	}
	var out strings.Builder
	inPre := false
	afterOpenCode := false
	atLineStart := func() bool {
		cur := out.String()
		return cur == "" || strings.HasSuffix(cur, "\n")
	}
	pos := 0
	text := func(s string) {
		s = html.UnescapeString(s)
		if inPre {
			out.WriteString(s)
			return
		}
		s = strings.Join(strings.Fields(s), " ")
		if s != "" {
			if out.Len() > 0 && !afterOpenCode {
				last := out.String()[out.Len()-1]
				if last != '\n' && last != ' ' && last != '(' && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, ",") && !strings.HasPrefix(s, ")") && !strings.HasPrefix(s, ";") && !strings.HasPrefix(s, ":") {
					out.WriteByte(' ')
				}
			}
			afterOpenCode = false
			out.WriteString(s)
		}
	}
	for _, loc := range tagRe.FindAllStringSubmatchIndex(src, -1) {
		text(src[pos:loc[0]])
		pos = loc[1]
		closing := loc[2] != loc[3]
		name := strings.ToLower(src[loc[4]:loc[5]])
		switch name {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			if closing {
				out.WriteString("\n\n")
			} else {
				out.WriteString("\n\n" + strings.Repeat("#", int(name[1]-'0')) + " ")
			}
		case "p", "div", "section", "ul", "ol", "table", "blockquote", "dl", "details", "summary", "figure":
			out.WriteString("\n\n")
		case "br", "hr":
			out.WriteString("\n")
		case "tr":
			if !closing && !atLineStart() {
				out.WriteString("\n")
			}
		case "td", "th":
			if closing {
				out.WriteString(" |")
			} else {
				if atLineStart() {
					out.WriteString("|")
				}
				out.WriteString(" ")
				afterOpenCode = true
			}
		case "li", "dt", "dd":
			if !closing {
				out.WriteString("\n- ")
			}
		case "pre":
			if closing {
				inPre = false
				out.WriteString("\n```\n\n")
			} else {
				inPre = true
				out.WriteString("\n\n```\n")
			}
		case "code", "kbd", "samp", "tt":
			if !inPre {
				if !closing {
					if cur := out.String(); cur != "" && !strings.HasSuffix(cur, "\n") && !strings.HasSuffix(cur, " ") && !strings.HasSuffix(cur, "(") {
						out.WriteString(" ")
					}
					afterOpenCode = true
				}
				out.WriteString("`")
			}
		}
	}
	text(src[pos:])
	md = out.String()
	md = strings.ReplaceAll(md, "`\n```", "\n```")
	md = regexp.MustCompile("\n{3,}").ReplaceAllString(md, "\n\n")
	return title, strings.TrimSpace(md)
}

var (
	tagRe     = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	titleRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

func removeElement(s, tag string) string {
	re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`)
	return re.ReplaceAllString(s, "")
}

func innerOf(s, tag string) (string, bool) {
	re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>(.*)</` + tag + `\s*>`)
	if m := re.FindStringSubmatch(s); m != nil && strings.TrimSpace(m[1]) != "" {
		return m[1], true
	}
	return "", false
}

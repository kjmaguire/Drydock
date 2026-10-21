package requirements

import (
	"errors"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/packs"
)

// Formats of incoming text.
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
	// FormatPDFText is text pasted from a PDF reader: hard-wrapped lines, page numbers and running headers. Binary PDF
	// files are not parsed (the design does not specify conversion); paste the text instead.
	FormatPDFText = "pdftext"
)

// MaxTextBytes bounds one document version.
const MaxTextBytes = 1 << 20

// ErrBinary is returned for input that is not text.
var ErrBinary = errors.New("requirements: this is not text. Binary PDF and DOCX files are not parsed; paste the text or upload Markdown")

var (
	numberedHeading = regexp.MustCompile(`^(\d+(?:\.\d+){0,3})[.)]?\s+([A-Z][^\n]{1,100})$`)
	pageNumber      = regexp.MustCompile(`^(?i)\s*(page\s+)?\d{1,4}(\s*(/|of)\s*\d{1,4})?\s*$`)
	listItem        = regexp.MustCompile(`^\s*([-*•●▪]|\d+[.)]|[a-z][.)])\s+`)
	atxLine         = regexp.MustCompile(`^#{1,6}\s`)
)

// Normalize prepares a document for chunking: validates it is text, applies the format's conversion and the common
// normalization of packs.Normalize (line endings, hidden characters, blank lines). The result is Markdown.
func Normalize(format, text string) (string, error) {
	if strings.HasPrefix(text, "%PDF-") || strings.ContainsRune(text, 0) {
		return "", ErrBinary
	}
	if len(text) > MaxTextBytes {
		return "", errors.New("requirements: the document is larger than 1 MiB")
	}
	switch format {
	case FormatMarkdown, "":
		out, _ := packs.Normalize(text)
		return out, nil
	case FormatText, FormatPDFText:
		pre, _ := packs.Normalize(strings.ReplaceAll(text, "\f", "\n\n")) // form feeds are page breaks, not hidden characters
		lines := strings.Split(pre, "\n")
		if format == FormatPDFText {
			lines = dropPDFNoise(lines)
		}
		lines = detectHeadings(lines)
		if format == FormatPDFText {
			lines = unwrap(lines)
		}
		out, _ := packs.Normalize(strings.Join(lines, "\n"))
		return out, nil
	}
	return "", errors.New("requirements: format must be markdown, text or pdftext")
}

// dropPDFNoise removes bare page numbers and short lines that repeat on three or more pages (running headers and footers).
func dropPDFNoise(lines []string) []string {
	count := map[string]int{}
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" && len(t) <= 80 {
			count[t]++
		}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if pageNumber.MatchString(l) && t != "" {
			continue
		}
		if t != "" && len(t) <= 80 && count[t] >= 3 && !numberedHeading.MatchString(t) && !listItem.MatchString(t) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// detectHeadings turns "1.2 Scope" and standalone ALL-CAPS lines into Markdown headings (depth from the number of parts).
func detectHeadings(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l
		t := strings.TrimSpace(l)
		if t == "" || atxLine.MatchString(t) {
			continue
		}
		prevBlank := i == 0 || strings.TrimSpace(lines[i-1]) == ""
		nextBlank := i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == ""
		nextText := !nextBlank
		// "1.2 Scope" is a heading when it stands alone as a paragraph (or is clearly multi-level); "1. Install" inside a
		// list is not
		if m := numberedHeading.FindStringSubmatch(t); m != nil && !strings.HasSuffix(t, ".") && !strings.HasSuffix(t, ",") && prevBlank {
			depth := strings.Count(m[1], ".") + 1
			if depth >= 2 || nextBlank || !listItem.MatchString(t) {
				out[i] = strings.Repeat("#", min(depth, 4)) + " " + m[1] + " " + m[2]
				continue
			}
		}
		if listItem.MatchString(t) {
			continue
		}
		if prevBlank && nextText && isAllCaps(t) {
			out[i] = "# " + strings.Title(strings.ToLower(t)) //nolint:staticcheck // a display title for ASCII headings
		}
	}
	return out
}

func isAllCaps(s string) bool {
	letters := 0
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			letters++
		case r >= 'a' && r <= 'z', r == '.', r == ',', r == ';', r == ':', r == '!', r == '?':
			return false
		}
	}
	return letters >= 3 && len(s) <= 80
}

// unwrap joins hard-wrapped lines of a paragraph: a line continues into the next unless it ends a sentence, the next line
// starts a list item or heading, or a blank line separates them.
func unwrap(lines []string) []string {
	var out []string
	for _, l := range lines {
		t := strings.TrimRight(l, " \t")
		if len(out) > 0 && strings.TrimSpace(t) != "" {
			prev := out[len(out)-1]
			pt := strings.TrimSpace(prev)
			if pt != "" && !atxLine.MatchString(pt) && !strings.HasSuffix(pt, ".") && !strings.HasSuffix(pt, ":") && !strings.HasSuffix(pt, ";") &&
				!strings.HasSuffix(pt, "!") && !strings.HasSuffix(pt, "?") && !listItem.MatchString(t) && !atxLine.MatchString(strings.TrimSpace(t)) {
				if strings.HasSuffix(pt, "-") && len(pt) > 1 {
					out[len(out)-1] = strings.TrimSuffix(prev, "-") + strings.TrimLeft(t, " \t") // hyphenated line break
				} else {
					out[len(out)-1] = prev + " " + strings.TrimLeft(t, " \t")
				}
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

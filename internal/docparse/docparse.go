// Package docparse reads the text out of PDF and DOCX files a person gives Drydock (expert-pack sources, docs/expert-packs.md
// "As built: PDF and DOCX"). Every file is untrusted: it may be cut, malformed or made to exhaust the reader, so each one is
// read within limits (its size, its pages, how much a DOCX unpacks to, a time limit) and the text that comes out is only text,
// never anything that is run. What comes back is Markdown: a PDF as one section per page (so a citation can name the page), a
// DOCX with its headings, lists and tables.
package docparse

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Kinds of document.
const (
	KindPDF  = "pdf"
	KindDOCX = "docx"
)

// Limits bound the reading of one file. The zero value of a field means its default.
type Limits struct {
	// MaxBytes is the largest file read at all.
	MaxBytes int64
	// MaxPages is the most pages of a PDF; a longer one is refused with the count rather than read in part.
	MaxPages int
	// MaxUnpacked is the most a DOCX's parts may unpack to, together (the zip-bomb guard).
	MaxUnpacked int64
	// MaxText is the most text kept from one file.
	MaxText int
	// Timeout is how long one file may take.
	Timeout time.Duration
}

// Defaults for Limits.
const (
	DefaultMaxBytes    = 32 << 20
	DefaultMaxPages    = 1000
	DefaultMaxUnpacked = 128 << 20
	DefaultMaxText     = 16 << 20
	DefaultTimeout     = 60 * time.Second
)

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxPages <= 0 {
		l.MaxPages = DefaultMaxPages
	}
	if l.MaxUnpacked <= 0 {
		l.MaxUnpacked = DefaultMaxUnpacked
	}
	if l.MaxText <= 0 {
		l.MaxText = DefaultMaxText
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	return l
}

// Errors a caller may tell apart. Each carries a message a person can act on.
var (
	ErrTooLarge    = errors.New("the file is too large")
	ErrTooManyPage = errors.New("the PDF has too many pages")
	ErrNoText      = errors.New("this PDF has no text layer (it looks like a scan): Drydock reads text, not pictures of text, so run it through OCR or export it with its text first")
	ErrEncrypted   = errors.New("the PDF is encrypted; save a copy without a password first")
	ErrTimeout     = errors.New("reading the file took too long")
	ErrMalformed   = errors.New("the file is damaged or not what its name says")
	ErrUnsupported = errors.New("only PDF and DOCX files are read")
)

// Page is one page of a PDF: its number (from 1) and its text.
type Page struct {
	N    int
	Text string
}

// Document is what a file holds, as text.
type Document struct {
	Kind  string
	Title string // the document's own title when it names one, else ""
	// Pages are a PDF's pages with text (a page with none is left out). Empty for a DOCX.
	Pages []Page
	// Markdown is the whole document: for a PDF, a "## Page N" section per page under a "# Title" heading; for a DOCX, its
	// paragraphs with headings, lists and tables. Lines of the file's own text that Markdown would read as a heading or a code
	// fence are escaped, so the only headings are the document's.
	Markdown string
}

// KindOf names the kind of a file from its name and its first bytes: "" when it is neither a PDF nor a DOCX. A name and
// content that disagree (a .pdf that is a zip) are taken by the content, which is what the reader would see.
func KindOf(name string, head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return KindPDF
	case bytes.HasPrefix(head, []byte("PK\x03\x04")) && strings.EqualFold(filepath.Ext(name), ".docx"):
		return KindDOCX
	}
	return ""
}

// IsDocumentName reports whether a file name has an extension this package reads.
func IsDocumentName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".docx":
		return true
	}
	return false
}

// PageMarker is the heading line that starts page n in a PDF's Markdown.
func PageMarker(n int) string { return fmt.Sprintf("## Page %d", n) }

// escapeLine keeps a line of the file's own text from being read as Markdown structure: a heading, a code fence, a
// front-matter rule or (in a table) a cell separator.
func escapeLine(l string) string {
	t := strings.TrimLeft(l, " \t")
	switch {
	case strings.HasPrefix(t, "#"), strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"), t == "---" && t == l:
		return `\` + t
	}
	return l
}

func escapeText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = escapeLine(l)
	}
	return strings.Join(lines, "\n")
}

// clean collapses runs of spaces, trims each line and runs of blank lines.
func clean(s string) string {
	var out []string
	blank := 0
	for _, l := range strings.Split(s, "\n") {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			blank++
			if blank > 1 || len(out) == 0 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

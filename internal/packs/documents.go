package packs

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/docparse"
)

// Page formats of documents read with internal/docparse (decision P110). A PDF is chunked page by page, so every passage
// knows its page; a DOCX becomes Markdown with its own headings and is chunked like any other page.
const (
	FormatPDF  = docparse.KindPDF
	FormatDOCX = docparse.KindDOCX
)

// documentLimits are the bounds of one PDF or DOCX source. Its own size bound is docparse's, not crawl.max_page_bytes: a
// manual is often larger than a web page, and what is kept is its text.
var documentLimits = docparse.Limits{}

// maxSourceBytes is the largest local file of a format that discovery lists.
func maxSourceBytes(format string, m *Manifest) int64 {
	if format == FormatPDF || format == FormatDOCX {
		return docparse.DefaultMaxBytes
	}
	return m.Crawl.MaxPageBytes
}

func readDocument(ctx context.Context, format string, data []byte) (docparse.Document, error) {
	if format == FormatPDF {
		return docparse.PDF(ctx, data, documentLimits)
	}
	return docparse.DOCX(ctx, data, documentLimits)
}

// documentReason is the refresh's word for why a document was not indexed. The reasons are counted across pages, so they
// name the kind of trouble, never the file.
func documentReason(err error) string {
	switch {
	case errors.Is(err, docparse.ErrNoText):
		return "PDF with no text layer (a scan: run it through OCR first)"
	case errors.Is(err, docparse.ErrEncrypted):
		return "PDF is password protected"
	case errors.Is(err, docparse.ErrTooManyPage):
		return "PDF has more pages than are read"
	case errors.Is(err, docparse.ErrTooLarge):
		return "document too large"
	case errors.Is(err, docparse.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return "document took too long to read"
	case errors.Is(err, docparse.ErrBusy):
		return "PDF reader busy"
	}
	return "damaged document"
}

var pageMarker = regexp.MustCompile(`^## Page ([0-9]+)$`)

// chunkPDF chunks the Markdown of a PDF (docparse: "## Page N" sections, the document's own lines escaped so they are never
// headings) one page at a time. Each chunk's heading path is "<title> > Page N" and it carries N, so a citation names the
// page and a long page is still split toward the target size.
func chunkPDF(text, title string, co ChunkOptions) (string, []Chunk) {
	if t, rest := SplitFrontMatter(text); t != "" {
		title, text = t, rest
	}
	var out []Chunk
	page := 0
	var body []string
	flush := func() {
		if page == 0 || strings.TrimSpace(strings.Join(body, "\n")) == "" {
			body = body[:0]
			return
		}
		_, cs := ChunkMarkdown(strings.Join(body, "\n"), title, co)
		path := strings.TrimSpace(title)
		if path != "" {
			path += PathSep
		}
		path += "Page " + strconv.Itoa(page)
		for _, c := range cs {
			c.Ordinal, c.HeadingPath, c.Page = len(out), path, page
			c.Hash = HashHex(c.HeadingPath, c.Body)
			out = append(out, c)
		}
		body = body[:0]
	}
	for _, l := range strings.Split(text, "\n") {
		if m := pageMarker.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			flush()
			page, _ = strconv.Atoi(m[1])
			continue
		}
		if page == 0 {
			if h, ok := strings.CutPrefix(l, "# "); ok && strings.TrimSpace(title) == "" {
				title = strings.TrimSpace(h)
			}
			continue
		}
		body = append(body, l)
	}
	flush()
	return title, out
}

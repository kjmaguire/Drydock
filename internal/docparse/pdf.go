package docparse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"

	"github.com/kjmaguire/drydock/internal/safe"
)

// pdfSlots bounds how many PDFs are read at once. A slot is given back when the reading really ends, not when the caller
// stops waiting: the reader cannot be interrupted part-way (a hostile page tree can keep it busy), so a file that ran out of
// time still holds its slot until it finishes, and two such files leave PDFs refused as busy instead of piling up work in the
// background.
var pdfSlots = make(chan struct{}, 2)

// ErrBusy is returned when every PDF slot is held by a reading that has not ended.
var ErrBusy = errors.New("two PDFs are still being read; try again in a minute")

// PDF reads the text of a PDF, page by page. A page's text follows the order of its content stream (which keeps columns
// apart), with a space where the glyphs leave a gap and a new line where the baseline moves. A PDF with no text on any page
// is ErrNoText: it is a scan, and no OCR is done.
func PDF(ctx context.Context, data []byte, lim Limits) (Document, error) {
	lim = lim.withDefaults()
	if int64(len(data)) > lim.MaxBytes {
		return Document{}, fmt.Errorf("%w: %d MB, the most read is %d MB", ErrTooLarge, len(data)>>20, lim.MaxBytes>>20)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return Document{}, fmt.Errorf("%w: it does not start like a PDF", ErrMalformed)
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	select {
	case pdfSlots <- struct{}{}:
	case <-ctx.Done():
		return Document{}, ErrBusy
	}
	type result struct {
		doc Document
		err error
	}
	done := make(chan result, 1)
	safe.Go("docparse.PDF", func() {
		defer func() { <-pdfSlots }()
		var r result
		func() {
			// the reader reports damage by panicking; a damaged file is an error of that file, not of the app
			defer func() {
				if v := recover(); v != nil {
					r = result{err: fmt.Errorf("%w: %v", ErrMalformed, v)}
				}
			}()
			r.doc, r.err = readPDF(ctx, data, lim)
		}()
		done <- r
	})
	select {
	case r := <-done:
		return r.doc, r.err
	case <-ctx.Done():
		return Document{}, fmt.Errorf("%w (more than %s)", ErrTimeout, lim.Timeout)
	}
}

func readPDF(ctx context.Context, data []byte, lim Limits) (Document, error) {
	r, err := pdf.NewReader(pdf2Compat{data}, int64(len(data)))
	if err != nil {
		if errors.Is(err, pdf.ErrInvalidPassword) || strings.Contains(strings.ToLower(err.Error()), "encrypt") {
			return Document{}, ErrEncrypted
		}
		return Document{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	n := r.NumPage()
	if n <= 0 {
		return Document{}, fmt.Errorf("%w: it has no pages", ErrMalformed)
	}
	if n > lim.MaxPages {
		return Document{}, fmt.Errorf("%w: %d pages, the most read is %d", ErrTooManyPage, n, lim.MaxPages)
	}
	doc := Document{Kind: KindPDF}
	if info := r.Trailer().Key("Info"); !info.IsNull() {
		doc.Title = clean(info.Key("Title").Text())
	}
	total := 0
	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text := clean(pageText(p.Content().Text))
		if text == "" {
			continue
		}
		total += len(text)
		if total > lim.MaxText {
			return Document{}, fmt.Errorf("%w: more than %d MB of text", ErrTooLarge, lim.MaxText>>20)
		}
		doc.Pages = append(doc.Pages, Page{N: i, Text: text})
	}
	if !hasWords(doc.Pages) {
		return Document{}, ErrNoText
	}
	var b strings.Builder
	if doc.Title != "" {
		b.WriteString("# " + doc.Title + "\n\n")
	}
	for _, p := range doc.Pages {
		fmt.Fprintf(&b, "%s\n\n%s\n\n", PageMarker(p.N), escapeText(p.Text))
	}
	doc.Markdown = strings.TrimSpace(b.String()) + "\n"
	return doc, nil
}

// hasWords reports whether the pages hold readable text: at least a few letters or digits. A scan often carries a stray
// glyph or a page number from its producer, which is not a text layer.
func hasWords(pages []Page) bool {
	n := 0
	for _, p := range pages {
		for _, r := range p.Text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				n++
				if n >= 16 {
					return true
				}
			}
		}
	}
	return false
}

// pageText lays a page's glyphs out as lines in content-stream order: a new line when the baseline moves by more than half
// the glyph size, a space when the gap to the previous glyph is wider than a sixth of it.
func pageText(glyphs []pdf.Text) string {
	var b strings.Builder
	var px, py, pw, ps float64
	first := true
	for _, g := range glyphs {
		if g.S == "" || g.S == "\n" || g.S == "\r" {
			continue
		}
		size := math.Abs(g.FontSize)
		if size == 0 {
			size = 10
		}
		if !first {
			ref := math.Max(size, ps)
			switch {
			case math.Abs(g.Y-py) > ref/2:
				b.WriteByte('\n')
			case g.X-(px+pw) > ref/6 || g.X < px-ref: // a gap, or a jump back on the same line
				b.WriteByte(' ')
			}
		}
		for _, r := range g.S {
			if r == '\t' || unicode.IsSpace(r) {
				b.WriteByte(' ')
			} else if unicode.IsPrint(r) {
				b.WriteRune(r)
			}
		}
		px, py, pw, ps, first = g.X, g.Y, g.W, size, false
	}
	return b.String()
}

// pdf2Compat shows the reader a PDF 2.0 file as 1.7: the reader refuses any header but %PDF-1.x, and what it reads (the
// cross-reference table, the page tree, fonts and content streams) is the same in 2.0.
type pdf2Compat struct{ b []byte }

func (p pdf2Compat) ReadAt(out []byte, off int64) (int, error) {
	if off >= int64(len(p.b)) {
		return 0, io.EOF
	}
	n := copy(out, p.b[off:])
	if off < 8 && bytes.HasPrefix(p.b, []byte("%PDF-2.")) {
		fake := []byte("%PDF-1.7")
		for i := off; i < 8 && int(i-off) < n; i++ {
			out[i-off] = fake[i]
		}
	}
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

// Package docfixture writes small, valid PDF and DOCX files for tests: TEST FIXTURES, built byte by byte the way the
// formats are specified, so readers are tested on real files without shipping binary test data. They prove nothing about
// files from real producers (Word, Acrobat, browsers' print to PDF).
package docfixture

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// PDF writes a small, valid PDF: one page per entry of pages, each a content stream as given. The font is Helvetica
// with a width for every printable ASCII character, so glyph positions advance the way a real producer's do.
func PDF(header string, title string, pages []string) []byte {
	var objs []string
	n := len(pages)
	// 1 catalog, 2 pages, 3 font, then a page and a content object per page, then the info dictionary
	kids := []string{}
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+2*i))
	}
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>")
	objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	widths := strings.TrimSpace(strings.Repeat("500 ", 95))
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /FirstChar 32 /LastChar 126 /Widths ["+widths+"] >>")
	for i, c := range pages {
		objs = append(objs, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(c), c))
	}
	info := len(objs) + 1
	objs = append(objs, fmt.Sprintf("<< /Title (%s) >>", title))
	var b bytes.Buffer
	b.WriteString(header + "\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, info, xref)
	return b.Bytes()
}

// TextPage is a content stream that writes each line at 72 pt from the left, 14 pt apart.
func TextPage(lines ...string) string {
	var b strings.Builder
	b.WriteString("BT /F1 12 Tf 72 720 Td")
	for i, l := range lines {
		if i > 0 {
			b.WriteString(" 0 -14 Td")
		}
		fmt.Fprintf(&b, " (%s) Tj", l)
	}
	b.WriteString(" ET")
	return b.String()
}

// Part is one file inside a DOCX.
type Part struct{ Name, Body string }

// DOCX writes a zip with the given parts (word/document.xml and the rest), deflated as Word does.
func DOCX(parts ...Part) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, p := range parts {
		w, err := zw.Create(p.Name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(p.Body)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// NS declares the WordprocessingML and markup-compatibility namespaces.
const NS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"`

// Body is a word/document.xml holding inner.
func Body(inner string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document ` + NS + `><w:body>` + inner + `</w:body></w:document>`
}

// Para is a paragraph of one run, with a style id when style is not empty.
func Para(style, text string) string {
	ppr := ""
	if style != "" {
		ppr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + ppr + `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

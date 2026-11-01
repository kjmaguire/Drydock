package docparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// maxZipEntries bounds the entries a DOCX may list (a real one has a few dozen).
const maxZipEntries = 10000

// DOCX reads a Word document with the standard library alone: the paragraphs of word/document.xml, the headings its styles
// name (word/styles.xml: "heading 1" to "heading 9" and "Title", in any language, or an outline level), list items, and
// tables as Markdown tables. Only the characters of w:t are text: deleted tracked changes (w:delText), field codes
// (w:instrText) and drawing positions are left out, and so is mc:Fallback, a second copy of a text box. Only those parts (and the title in
// docProps/core.xml) are unpacked, each through a bound on what it unpacks to, whatever the zip claims.
func DOCX(ctx context.Context, data []byte, lim Limits) (Document, error) {
	lim = lim.withDefaults()
	if int64(len(data)) > lim.MaxBytes {
		return Document{}, fmt.Errorf("%w: %d MB, the most read is %d MB", ErrTooLarge, len(data)>>20, lim.MaxBytes>>20)
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Document{}, fmt.Errorf("%w: it is not a Word document (%v)", ErrMalformed, err)
	}
	if len(zr.File) > maxZipEntries {
		return Document{}, fmt.Errorf("%w: it lists %d parts", ErrMalformed, len(zr.File))
	}
	budget := &unpackBudget{left: lim.MaxUnpacked}
	parts := map[string]*zip.File{}
	for _, f := range zr.File {
		parts[f.Name] = f
	}
	body, ok := parts["word/document.xml"]
	if !ok {
		return Document{}, fmt.Errorf("%w: it has no word/document.xml", ErrMalformed)
	}
	headings := map[string]int{}
	if st, ok := parts["word/styles.xml"]; ok {
		b, err := budget.read(st)
		if err != nil {
			return Document{}, err
		}
		headings = headingStyles(b)
	}
	doc := Document{Kind: KindDOCX}
	if core, ok := parts["docProps/core.xml"]; ok {
		if b, err := budget.read(core); err == nil {
			doc.Title = coreTitle(b)
		} else if errors.Is(err, ErrTooLarge) {
			return Document{}, err
		}
	}
	b, err := budget.read(body)
	if err != nil {
		return Document{}, err
	}
	md, err := docxMarkdown(ctx, b, headings, lim.MaxText)
	if err != nil {
		return Document{}, err
	}
	if doc.Title != "" && !strings.HasPrefix(md, "# ") {
		md = "# " + doc.Title + "\n\n" + md
	}
	if strings.TrimSpace(md) == "" {
		return Document{}, fmt.Errorf("%w: the document has no text", ErrMalformed)
	}
	doc.Markdown = strings.TrimSpace(md) + "\n"
	return doc, nil
}

// unpackBudget is what the parts read so far may still unpack to. A part is read through a limit one byte above what is
// left, so a zip that understates a part's size cannot get past it.
type unpackBudget struct{ left int64 }

func (u *unpackBudget) read(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > uint64(u.left) {
		return nil, fmt.Errorf("%w: it unpacks to more than %d MB (a zip bomb?)", ErrTooLarge, (u.left+(1<<20)-1)>>20)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, u.left+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if int64(len(b)) > u.left {
		return nil, fmt.Errorf("%w: it unpacks to more than its parts say (a zip bomb?)", ErrTooLarge)
	}
	u.left -= int64(len(b))
	return b, nil
}

var headingName = regexp.MustCompile(`^heading ([1-9])$`)

// headingStyles maps the style ids of word/styles.xml that are headings to their level. Word names its built-in styles in
// English in w:name whatever the language of the document ("heading 1"), while the id is localized ("berschrift1"), so the
// name is what is read; an outline level on a style makes it a heading too.
func headingStyles(b []byte) map[string]int {
	out := map[string]int{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var id string
	level := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				id, level = attr(t, "styleId"), 0
			case "name":
				n := strings.ToLower(strings.TrimSpace(attr(t, "val")))
				if m := headingName.FindStringSubmatch(n); m != nil {
					level, _ = strconv.Atoi(m[1])
				} else if n == "title" {
					level = 1
				}
			case "outlineLvl":
				if v, err := strconv.Atoi(attr(t, "val")); err == nil && v >= 0 && v < 9 && level == 0 {
					level = v + 1
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" && id != "" && level > 0 {
				out[id] = level
			}
		}
	}
}

func coreTitle(b []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	in := false
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			in = t.Name.Local == "title"
		case xml.EndElement:
			if t.Name.Local == "title" {
				return clean(sb.String())
			}
		case xml.CharData:
			if in && sb.Len() < 400 {
				sb.Write(t)
			}
		}
	}
	return ""
}

func attr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

var builtinHeading = regexp.MustCompile(`^(?i)heading([1-9])$`)

// docxMarkdown walks the body. A paragraph's style and numbering come before its runs (w:pPr is its first child), text is in
// w:t, and a table is rows of cells of paragraphs; a table inside a cell is flattened into that cell's text.
func docxMarkdown(ctx context.Context, b []byte, headings map[string]int, maxText int) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var out strings.Builder
	var para strings.Builder
	style, outline, listItem := "", -1, false
	skip := 0 // inside w:del, mc:Fallback (a copy of what mc:Choice holds) or another part whose text is not the document's
	inT := 0  // inside w:t: the only element whose characters are the document's text
	tableDepth := 0
	var row []string
	rowsInTable := 0
	var cell strings.Builder
	n := 0

	level := func() int {
		if l, ok := headings[style]; ok {
			return l
		}
		if m := builtinHeading.FindStringSubmatch(style); m != nil {
			l, _ := strconv.Atoi(m[1])
			return l
		}
		if strings.EqualFold(style, "Title") {
			return 1
		}
		if outline >= 0 && outline < 9 {
			return outline + 1
		}
		return 0
	}
	endPara := func() {
		text := strings.Join(strings.Fields(para.String()), " ")
		para.Reset()
		lvl := level()
		style, outline = "", -1
		li := listItem
		listItem = false
		if text == "" {
			return
		}
		if tableDepth > 0 {
			if cell.Len() > 0 {
				cell.WriteString(" ")
			}
			cell.WriteString(text)
			return
		}
		switch {
		case lvl > 0:
			fmt.Fprintf(&out, "%s %s\n\n", strings.Repeat("#", min(lvl, 6)), text)
		case li:
			fmt.Fprintf(&out, "- %s\n", escapeLine(text))
		default:
			fmt.Fprintf(&out, "%s\n\n", escapeLine(text))
		}
	}
	for {
		if n++; n%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return "", ErrTimeout
			}
			if out.Len() > maxText {
				return "", fmt.Errorf("%w: more than %d MB of text", ErrTooLarge, maxText>>20)
			}
		}
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "del", "Fallback":
				skip++
			case "t":
				inT++
			case "pStyle":
				style = attr(t, "val")
			case "outlineLvl":
				if v, err := strconv.Atoi(attr(t, "val")); err == nil {
					outline = v
				}
			case "numPr":
				listItem = true
			case "tab":
				para.WriteString(" ")
			case "br", "cr":
				para.WriteString(" ")
			case "tbl":
				tableDepth++
				if tableDepth == 1 {
					endPara()
					rowsInTable = 0
				}
			case "tr":
				if tableDepth == 1 {
					row = row[:0]
				}
			case "tc":
				if tableDepth == 1 {
					cell.Reset()
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "del", "Fallback":
				if skip > 0 {
					skip--
				}
			case "t":
				if inT > 0 {
					inT--
				}
			case "p":
				endPara()
			case "tc":
				if tableDepth == 1 {
					row = append(row, strings.ReplaceAll(cell.String(), "|", `\|`))
				}
			case "tr":
				if tableDepth == 1 && len(row) > 0 {
					fmt.Fprintf(&out, "| %s |\n", strings.Join(row, " | "))
					if rowsInTable == 0 {
						out.WriteString("|" + strings.Repeat(" --- |", len(row)) + "\n")
					}
					rowsInTable++
				}
			case "tbl":
				if tableDepth > 0 {
					tableDepth--
				}
				if tableDepth == 0 {
					out.WriteString("\n")
				}
			}
		case xml.CharData:
			if skip == 0 && inT > 0 {
				para.Write(t)
			}
		}
	}
	endPara()
	return out.String(), nil
}

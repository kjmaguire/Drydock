package docparse

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/docparse/docfixture"
)

func TestPDFReadsTheTextOfEveryPageWithItsNumber(t *testing.T) {
	data := docfixture.PDF("%PDF-1.4", "Install Guide", []string{
		docfixture.TextPage("Installing the agent", "Run the installer as yourself."),
		"", // a page with no text is left out, the numbering is kept
		docfixture.TextPage("# Not a heading", "Rotate the keys every 90 days."),
	})
	doc, err := PDF(context.Background(), data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindPDF || doc.Title != "Install Guide" {
		t.Fatalf("%+v", doc)
	}
	if len(doc.Pages) != 2 || doc.Pages[0].N != 1 || doc.Pages[1].N != 3 {
		t.Fatalf("pages %+v", doc.Pages)
	}
	if doc.Pages[0].Text != "Installing the agent\nRun the installer as yourself." {
		t.Fatalf("page 1 %q", doc.Pages[0].Text)
	}
	for _, want := range []string{"# Install Guide\n", "## Page 1\n", "## Page 3\n", `\# Not a heading`, "Rotate the keys every 90 days."} {
		if !strings.Contains(doc.Markdown, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, doc.Markdown)
		}
	}
	if strings.Contains(doc.Markdown, "## Page 2") {
		t.Fatal("an empty page has no section")
	}
}

func TestPDFPutsASpaceWhereTheGlyphsLeaveAGap(t *testing.T) {
	// TJ with a large negative adjustment moves the next glyph right: the producer's way of writing a space
	data := docfixture.PDF("%PDF-1.7", "", []string{"BT /F1 12 Tf 72 720 Td [(Hello) -2000 (World)] TJ 0 -14 Td [(tight) 0 (ly)] TJ ET " +
		"BT /F1 12 Tf 72 600 Td (and a long enough line of text to count as words) Tj ET"})
	doc, err := PDF(context.Background(), data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Pages[0].Text; !strings.HasPrefix(got, "Hello World\ntightly\nand a long") {
		t.Fatalf("%q", got)
	}
}

func TestPDF2IsRead(t *testing.T) {
	data := docfixture.PDF("%PDF-2.0", "", []string{docfixture.TextPage("A PDF 2.0 file is read like any other one.")})
	doc, err := PDF(context.Background(), data, Limits{})
	if err != nil || len(doc.Pages) != 1 {
		t.Fatalf("%v %+v", err, doc)
	}
}

func TestAScannedPDFSaysItHasNoTextLayer(t *testing.T) {
	// a page that only paints a picture (here a rectangle) and a stray page number
	data := docfixture.PDF("%PDF-1.4", "", []string{"0 0 612 792 re f", docfixture.TextPage("1")})
	_, err := PDF(context.Background(), data, Limits{})
	if !errors.Is(err, ErrNoText) || !strings.Contains(err.Error(), "OCR") {
		t.Fatalf("%v", err)
	}
}

func TestPDFLimits(t *testing.T) {
	ctx := context.Background()
	data := docfixture.PDF("%PDF-1.4", "", []string{docfixture.TextPage("one page of text, with enough words"), docfixture.TextPage("two pages of text, with enough words")})
	if _, err := PDF(ctx, data, Limits{MaxBytes: 100}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
	if _, err := PDF(ctx, data, Limits{MaxPages: 1}); !errors.Is(err, ErrTooManyPage) || !strings.Contains(err.Error(), "2 pages") {
		t.Fatalf("page cap: %v", err)
	}
	if _, err := PDF(ctx, data, Limits{MaxText: 20}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("text cap: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PDF(cancelled, data, Limits{}); err == nil {
		t.Fatal("a cancelled read must fail")
	}
}

func TestADamagedPDFIsAnErrorNotAPanic(t *testing.T) {
	ctx := context.Background()
	for name, data := range map[string][]byte{
		"not a pdf":     []byte("hello"),
		"cut":           docfixture.PDF("%PDF-1.4", "", []string{docfixture.TextPage("x")})[:200],
		"no xref":       []byte("%PDF-1.4\n1 0 obj << >> endobj\n%%EOF\n"),
		"bad operators": docfixture.PDF("%PDF-1.4", "", []string{"BT /F1 Tf (one argument short) Tj ET"}),
		"garbage tail":  append(docfixture.PDF("%PDF-1.4", "", []string{docfixture.TextPage("x")}), bytes.Repeat([]byte{0xff}, 300)...),
	} {
		_, err := PDF(ctx, data, Limits{})
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}

func TestPDFTimesOut(t *testing.T) {
	// many pages with text and a time limit too short to read them: the caller gets ErrTimeout, and the slot comes back
	var pages []string
	for i := 0; i < 400; i++ {
		pages = append(pages, docfixture.TextPage(strings.Repeat("words on a page ", 60)))
	}
	data := docfixture.PDF("%PDF-1.4", "", pages)
	_, err := PDF(context.Background(), data, Limits{Timeout: time.Millisecond})
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(pdfSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(pdfSlots) != 0 {
		t.Fatal("the slot of a finished reading must be free")
	}
}

func TestDOCXHeadingsListsTablesAndWhatIsLeftOut(t *testing.T) {
	styles := `<?xml version="1.0"?><w:styles ` + docfixture.NS + `>` +
		`<w:style w:type="paragraph" w:styleId="berschrift1"><w:name w:val="heading 1"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="berschrift2"><w:name w:val="heading 2"/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Kapitel"><w:name w:val="Kapitel"/><w:pPr><w:outlineLvl w:val="2"/></w:pPr></w:style>` +
		`</w:styles>`
	body := docfixture.Body(
		docfixture.Para("Title", "Security Handbook") +
			docfixture.Para("berschrift1", "Access") +
			docfixture.Para("", "Every account has an owner.") +
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>Review access quarterly</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Kept </w:t></w:r><w:del><w:r><w:delText>deleted words</w:delText></w:r></w:del><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:t>text</w:t></w:r></w:p>` +
			docfixture.Para("berschrift2", "Keys") +
			`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Key</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Rotation</w:t></w:r></w:p></w:tc></w:tr>` +
			`<w:tr><w:tc><w:p><w:r><w:t>API|token</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>90</w:t></w:r></w:p><w:p><w:r><w:t>days</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
			docfixture.Para("Kapitel", "Incident response") +
			docfixture.Para("", "# looks like a heading but is text") +
			`<w:p><mc:AlternateContent><mc:Choice><w:r><w:t>Box text</w:t></w:r></mc:Choice><mc:Fallback><w:r><w:t>Box text</w:t></w:r></mc:Fallback></mc:AlternateContent></w:p>`)
	data := docfixture.DOCX(docfixture.Part{"[Content_Types].xml", "<Types/>"}, docfixture.Part{"word/styles.xml", styles}, docfixture.Part{"word/document.xml", body})
	doc, err := DOCX(context.Background(), data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Security Handbook\n\n# Access\n\nEvery account has an owner.\n\n- Review access quarterly\nKept text\n\n## Keys\n\n" +
		"| Key | Rotation |\n| --- | --- |\n| API\\|token | 90 days |\n\n### Incident response\n\n\\# looks like a heading but is text\n\nBox text\n"
	if doc.Markdown != want {
		t.Fatalf("got:\n%s\nwant:\n%s", doc.Markdown, want)
	}
}

func TestDOCXTitleFromCoreProperties(t *testing.T) {
	core := `<?xml version="1.0"?><cp:coreProperties xmlns:cp="x" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Runbook</dc:title></cp:coreProperties>`
	data := docfixture.DOCX(docfixture.Part{"docProps/core.xml", core}, docfixture.Part{"word/document.xml", docfixture.Body(docfixture.Para("", "Restart the service."))})
	doc, err := DOCX(context.Background(), data, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Runbook" || !strings.HasPrefix(doc.Markdown, "# Runbook\n\nRestart the service.") {
		t.Fatalf("%+v", doc)
	}
}

func TestDOCXZipBombAndDamage(t *testing.T) {
	ctx := context.Background()
	// a document.xml that unpacks to 4 MB from a few KB: refused at a 1 MB bound
	bomb := docfixture.DOCX(docfixture.Part{"word/document.xml", docfixture.Body(docfixture.Para("", strings.Repeat("a", 4<<20)))})
	if len(bomb) > 64<<10 {
		t.Fatalf("the fixture should compress well, it is %d bytes", len(bomb))
	}
	if _, err := DOCX(ctx, bomb, Limits{MaxUnpacked: 1 << 20}); !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "zip bomb") {
		t.Fatalf("bomb: %v", err)
	}
	if _, err := DOCX(ctx, []byte("PK\x03\x04 not really"), Limits{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("damaged: %v", err)
	}
	if _, err := DOCX(ctx, docfixture.DOCX(docfixture.Part{"word/other.xml", "x"}), Limits{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("no body: %v", err)
	}
	if _, err := DOCX(ctx, docfixture.DOCX(docfixture.Part{"word/document.xml", "<w:document><w:body><w:p>"}), Limits{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("cut xml: %v", err)
	}
	if _, err := DOCX(ctx, docfixture.DOCX(docfixture.Part{"word/document.xml", docfixture.Body("")}), Limits{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := DOCX(ctx, bomb, Limits{MaxBytes: 100}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
}

func TestKindOf(t *testing.T) {
	for _, c := range []struct {
		name string
		head string
		want string
	}{
		{"a.pdf", "%PDF-1.7", KindPDF},
		{"a.txt", "%PDF-1.7", KindPDF},
		{"a.docx", "PK\x03\x04", KindDOCX},
		{"a.zip", "PK\x03\x04", ""},
		{"a.pdf", "hello", ""},
	} {
		if got := KindOf(c.name, []byte(c.head)); got != c.want {
			t.Fatalf("%s %q: %q", c.name, c.head, got)
		}
	}
}

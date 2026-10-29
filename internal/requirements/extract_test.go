package requirements

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/docparse"
	"github.com/kjmaguire/drydock/internal/docparse/docfixture"
)

func TestARequirementFromAPDFCitesItsPages(t *testing.T) {
	ctx := context.Background()
	pdf := docfixture.PDF("%PDF-1.4", "Checkout PRD", []string{
		docfixture.TextPage("Scope", "The checkout covers carts paid by card."),
		docfixture.TextPage("Acceptance", "Given a paid cart, when the order is placed, then an order record exists."),
	})
	x, err := Extract(ctx, `C:\specs\checkout.pdf`, pdf)
	if err != nil {
		t.Fatal(err)
	}
	if x.Title != "Checkout PRD" || !strings.Contains(x.Text, "## Page 2\n\nAcceptance") {
		t.Fatalf("%+v", x)
	}
	e := newEnv(t)
	r, err := e.s.Add(ctx, AddReq{ProjectID: "p1", Title: x.Title, Text: x.Text, By: human})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := e.s.Search(ctx, "p1", "order record exists", "", 3)
	if err != nil || len(hits) == 0 || !strings.HasSuffix(hits[0].HeadingPath, "Page 2") || hits[0].DocID != r.ID {
		t.Fatalf("the requirement's citation names the page: %v %+v", err, hits)
	}

	// a DOCX keeps its headings; its title comes from the file name when it names none
	docx := docfixture.DOCX(docfixture.Part{Name: "word/document.xml", Body: docfixture.Body(docfixture.Para("Heading1", "Refunds") + docfixture.Para("", "A refund is issued within five days."))})
	x, err = Extract(ctx, "refunds.docx", docx)
	if err != nil || x.Title != "refunds" || x.Text != "# Refunds\n\nA refund is issued within five days.\n" {
		t.Fatalf("%v %+v", err, x)
	}

	// a scan, a text file and a damaged file are refused with their reasons
	if _, err := Extract(ctx, "scan.pdf", docfixture.PDF("%PDF-1.4", "", []string{"0 0 612 792 re f"})); !errors.Is(err, docparse.ErrNoText) {
		t.Fatalf("%v", err)
	}
	if _, err := Extract(ctx, "notes.md", []byte("# Notes")); err == nil {
		t.Fatal("a text file is opened as it is")
	}
	if _, err := Extract(ctx, "x.docx", []byte("PK\x03\x04 cut")); !errors.Is(err, docparse.ErrMalformed) {
		t.Fatalf("%v", err)
	}
}

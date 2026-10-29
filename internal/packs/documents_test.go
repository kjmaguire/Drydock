package packs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/docparse/docfixture"
)

// a PDF manual of three pages (page 2 has no text) and a DOCX handbook, both written as real files (docfixture)
func manualPDF() []byte {
	return docfixture.PDF("%PDF-1.4", "Operator Manual", []string{
		docfixture.TextPage("Starting the gateway", "Run the gateway with the service account, never as root."),
		"",
		docfixture.TextPage("Rotating credentials", "Rotate the gateway token every ninety days with rotate-token."),
	})
}

func handbookDOCX() []byte {
	return docfixture.DOCX(docfixture.Part{Name: "word/document.xml", Body: docfixture.Body(
		docfixture.Para("Heading1", "Escalation") + docfixture.Para("", "Page the on-call engineer through the escalation bridge.") +
			docfixture.Para("Heading2", "Severity levels") + docfixture.Para("", "A severity one incident wakes the incident commander."))})
}

func TestPDFAndDOCXInALocalDirAreIndexedWithPagesAndHeadings(t *testing.T) {
	e := newEnv(t)
	docs := filepath.Join(e.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"manual.pdf": manualPDF(), "handbook.docx": handbookDOCX()} {
		if err := os.WriteFile(filepath.Join(docs, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.install(localManifest("ops", docs, ""))
	r := e.refresh("ops")
	if r.Added != 2 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	res := e.search("rotate the gateway token", "ops")
	if !res.Found {
		t.Fatalf("%+v", res)
	}
	top := res.Hits[0].Citation
	if top.URL != "local:manual.pdf" || top.Page != 3 || top.HeadingPath != "Operator Manual > Page 3" {
		t.Fatalf("a PDF passage cites its page: %+v", top)
	}
	// the page survives in a stored citation too
	h, err := e.s.ResolveCitation(context.Background(), "ops", top.Version, top.ChunkID)
	if err != nil || h.Citation.Page != 3 {
		t.Fatalf("%v %+v", err, h.Citation)
	}
	res = e.search("severity one incident commander", "ops")
	if !res.Found || res.Hits[0].Citation.URL != "local:handbook.docx" || res.Hits[0].Citation.HeadingPath != "Escalation > Severity levels" || res.Hits[0].Citation.Page != 0 {
		t.Fatalf("a DOCX passage cites its headings: %+v", res.Hits)
	}
	c, err := e.s.Consult(context.Background(), ConsultRequest{PackIDs: []string{"ops"}, Question: "how often is the gateway token rotated"})
	if err != nil || !c.Found || c.Sources[0].Citation.Page != 3 {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestAScannedOrDamagedDocumentFailsWithAClearReason(t *testing.T) {
	e := newEnv(t)
	docs := filepath.Join(e.dir, "docs")
	writeFiles(t, docs, map[string]string{"notes.md": "# Notes\n\nPlain notes are still indexed.\n"})
	scan := docfixture.PDF("%PDF-1.4", "", []string{"0 0 612 792 re f"})
	if err := os.WriteFile(filepath.Join(docs, "scan.pdf"), scan, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "broken.docx"), []byte("PK\x03\x04 cut short"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.install(localManifest("mixed", docs, ""))
	r, err := e.s.Refresh(context.Background(), human, "mixed", RefreshOptions{})
	// two of three pages failed: more than half, so the refresh keeps the previous (empty) version and says why
	if err == nil || r.Failed != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	if r.Reasons["PDF with no text layer (a scan: run it through OCR first)"] != 1 || r.Reasons["damaged document"] != 1 {
		t.Fatalf("reasons %+v", r.Reasons)
	}
	// with a second good page the scan is a failed page and the rest is indexed
	writeFiles(t, docs, map[string]string{"more.md": "# More\n\nMore notes.\n", "extra.md": "# Extra\n\nExtra notes.\n"})
	r = e.refresh("mixed")
	if r.Added != 3 || r.Failed != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestAPackMadeFromFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.s.CreateFromFiles(ctx, "agent:x", NewFilesPack{ID: "ops", Title: "Ops"}, []UploadFile{{Name: "a.md", Data: []byte("# A\n")}}); err == nil {
		t.Fatal("only a human makes a pack")
	}
	p, err := e.s.CreateFromFiles(ctx, human, NewFilesPack{ID: "ops", Title: "Ops handbook"}, []UploadFile{
		{Name: `C:\Users\kyle\Downloads\manual.pdf`, Data: manualPDF()},
		{Name: "handbook.docx", Data: handbookDOCX()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Visibility != VisibilityPrivate || p.Manifest.Consult.Route != RoutePrivateOnly || len(p.Manifest.Sources) != 1 || p.Manifest.Sources[0].Path != FilesDir {
		t.Fatalf("a pack of a person's files is private and reads its files folder: %+v", p.Manifest)
	}
	files, err := e.s.Files(ctx, "ops")
	if err != nil || len(files) != 2 || files[0].Name != "handbook.docx" || files[1].Name != "manual.pdf" {
		t.Fatalf("only the last element of a name is kept: %v %+v", err, files)
	}
	r := e.refresh("ops")
	if r.Added != 2 {
		t.Fatalf("%+v", r)
	}
	// replacing a file of the same name, adding one, removing one
	if _, err := e.s.AddFiles(ctx, human, "ops", []UploadFile{{Name: "handbook.docx", Data: docfixture.DOCX(docfixture.Part{Name: "word/document.xml",
		Body: docfixture.Body(docfixture.Para("Heading1", "Escalation") + docfixture.Para("", "Call the duty manager first."))})}, {Name: "faq.txt", Data: []byte("Where are the logs? In the logs folder.\n")}}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RemoveFile(ctx, human, "ops", "manual.pdf"); err != nil {
		t.Fatal(err)
	}
	r = e.refresh("ops")
	if r.Added != 1 || r.Changed != 1 || r.Removed != 1 {
		t.Fatalf("%+v", r)
	}
	if res := e.search("duty manager", "ops"); !res.Found {
		t.Fatal("the replaced file is indexed")
	}
	if res := e.search("rotate gateway token", "ops"); res.Found {
		t.Fatal("a removed file's passages are retired")
	}
	if len(e.events(EventFilesChanged)) != 3 {
		t.Fatalf("events %d", len(e.events(EventFilesChanged)))
	}
}

func TestPackFilesAreChecked(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.s.CreateFromFiles(ctx, human, NewFilesPack{ID: "ops", Title: "Ops"}, []UploadFile{{Name: "a.md", Data: []byte("# A\n\nText.\n")}}); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]UploadFile{
		"wrong extension":  {Name: "tool.exe", Data: []byte("MZ")},
		"a zip named pdf":  {Name: "x.pdf", Data: []byte("PK\x03\x04")},
		"a pdf named docx": {Name: "x.docx", Data: manualPDF()},
		"binary as text":   {Name: "x.md", Data: []byte{0, 1, 2}},
		"empty":            {Name: "x.txt"},
	} {
		if _, err := e.s.AddFiles(ctx, human, "ops", []UploadFile{f}); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := e.s.AddFiles(ctx, human, "ops", []UploadFile{{Name: "a.md", Data: []byte("x")}, {Name: "A.md", Data: []byte("y")}}); err == nil {
		t.Fatal("the same name twice (Windows names ignore case)")
	}
	got, err := e.s.AddFiles(ctx, human, "ops", []UploadFile{{Name: "../../escape/CON.md", Data: []byte("# Con\n")}})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range got {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "_CON.md,a.md" {
		t.Fatalf("a name keeps its last element and is never a device name: %v", names)
	}
	if err := e.s.RemoveFile(ctx, human, "ops", "../pack.yaml"); err == nil {
		t.Fatal("only the pack's files can be removed")
	}
	// a pack whose manifest reads another folder has no files to add to
	docs := filepath.Join(e.dir, "docs")
	writeFiles(t, docs, map[string]string{"x.md": "# X\n"})
	e.install(localManifest("other", docs, ""))
	if _, err := e.s.AddFiles(ctx, human, "other", []UploadFile{{Name: "a.md", Data: []byte("# A\n")}}); err == nil {
		t.Fatal("a pack without its own files folder")
	}
	if _, err := e.s.CreateFromFiles(ctx, human, NewFilesPack{ID: "ops", Title: "Again"}, []UploadFile{{Name: "b.md", Data: []byte("# B\n")}}); err == nil {
		t.Fatal("making a pack over an existing one")
	}
}

package requirements

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/docparse"
)

// Extracted is the text of a PDF or DOCX a person chose for a requirement (decision P110): Markdown, with a "## Page N"
// section per page of a PDF, so a citation of the requirement names the page. The page shows it in the text box to be read
// and edited before it is added, like a Markdown file.
type Extracted struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// Extract reads a PDF or DOCX with internal/docparse (its size, page, unpacking and time limits). The text must fit a
// requirement version (MaxTextBytes). Nothing is stored.
func Extract(ctx context.Context, name string, data []byte) (Extracted, error) {
	var doc docparse.Document
	var err error
	switch docparse.KindOf(name, data) {
	case docparse.KindPDF:
		doc, err = docparse.PDF(ctx, data, docparse.Limits{})
	case docparse.KindDOCX:
		doc, err = docparse.DOCX(ctx, data, docparse.Limits{})
	default:
		return Extracted{}, errors.New("requirements: only a PDF or a DOCX file is read here; open a text or Markdown file as it is")
	}
	if err != nil {
		return Extracted{}, fmt.Errorf("requirements: %s: %w", filepath.Base(name), err)
	}
	if len(doc.Markdown) > MaxTextBytes {
		return Extracted{}, fmt.Errorf("requirements: %s holds %d KB of text; a requirement holds at most 1 MiB, so split it", filepath.Base(name), len(doc.Markdown)>>10)
	}
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(strings.ReplaceAll(name, `\`, "/")), filepath.Ext(name))
	}
	return Extracted{Title: title, Text: doc.Markdown}, nil
}

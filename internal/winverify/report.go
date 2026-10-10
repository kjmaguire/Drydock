package winverify

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Report is the whole verification result, written as drydock-verify-report.json and .md.
type Report struct {
	Tool     string       `json:"tool"`
	Started  time.Time    `json:"started"`
	Finished time.Time    `json:"finished"`
	Host     string       `json:"host"` // goos/goarch the verifier ran on
	Bundle   *Manifest    `json:"bundle,omitempty"`
	Options  []string     `json:"options"` // the flags given
	Summary  Summary      `json:"summary"`
	Steps    []StepResult `json:"steps"`
}

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Markdown renders the report for a person: the machine, the result, a table of steps with what each proves, then the
// details of every step that did not pass and a short section for the rest.
func (r *Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("# Drydock verification report\n\n")
	if f := r.facts(); f != nil {
		w("- **Machine:** %s\n", mdInline(f.OSLine()))
		w("- **Hardware:** %s\n", mdInline(f.HardwareLine()))
	}
	w("- **Ran:** %s to %s (%s), on %s\n", r.Started.Format("2006-01-02 15:04:05 MST"), r.Finished.Format("15:04:05"),
		r.Finished.Sub(r.Started).Round(time.Second), r.Host)
	if m := r.Bundle; m != nil {
		dirty := ""
		if m.Dirty {
			dirty = " (with uncommitted changes)"
		}
		w("- **Bundle:** commit `%s`%s, built %s for %s/%s with %s\n", m.Commit, dirty, m.Built.Format("2006-01-02 15:04 MST"), m.GOOS, m.GOARCH, m.GoVersion)
	}
	opts := "none"
	if len(r.Options) > 0 {
		opts = "`" + strings.Join(r.Options, " ") + "`"
	}
	w("- **Options:** %s\n", opts)
	w("- **Result:** %d passed, %d failed, %d skipped\n\n", r.Summary.Passed, r.Summary.Failed, r.Summary.Skipped)

	w("| Step | Result | Time | Tests | What it proves |\n|---|---|---|---|---|\n")
	for _, s := range r.Steps {
		tests := ""
		if s.Counts != nil {
			tests = fmt.Sprintf("%d / %d / %d", s.Counts.Passed, s.Counts.Failed, s.Counts.Skipped)
		}
		w("| %s | %s | %s | %s | %s |\n", mdCell(s.ID), strings.ToUpper(string(s.Status)), fmtDur(s.Duration()), tests, mdCell(s.Proves))
	}
	w("\nTests are counted as passed / failed / skipped (top-level tests).\n")

	if f := r.facts(); f != nil {
		w("\n## Machine facts\n\n")
		for _, kv := range f.Lines() {
			w("- %s: %s\n", kv[0], mdInline(kv[1]))
		}
	}
	var notPassed, passed []StepResult
	for _, s := range r.Steps {
		if s.Status == Pass {
			passed = append(passed, s)
		} else {
			notPassed = append(notPassed, s)
		}
	}
	if len(notPassed) > 0 {
		w("\n## Steps that did not pass\n")
		for _, s := range notPassed {
			r.stepSection(&b, s)
		}
	}
	if len(passed) > 0 {
		w("\n## Steps that passed\n")
		for _, s := range passed {
			r.stepSection(&b, s)
		}
	}
	w("\nEvery step's full output is in the `logs` folder next to this report.\n")
	return b.String()
}

func (r *Report) stepSection(b *strings.Builder, s StepResult) {
	fmt.Fprintf(b, "\n### %s: %s (%s)\n\n", s.ID, strings.ToUpper(string(s.Status)), fmtDur(s.Duration()))
	fmt.Fprintf(b, "%s\n\n", s.Proves)
	if s.Reason != "" {
		label := "Reason"
		if s.Status == Fail {
			label = "Why it failed"
		}
		fmt.Fprintf(b, "**%s:** %s\n\n", label, mdInline(s.Reason))
	}
	if s.Excerpt != "" {
		fmt.Fprintf(b, "```text\n%s\n```\n\n", strings.ReplaceAll(s.Excerpt, "```", "'''"))
	}
	fmt.Fprintf(b, "Log: `%s`\n", s.Log)
}

func (r *Report) facts() *Facts {
	for _, s := range r.Steps {
		if s.Facts != nil {
			return s.Facts
		}
	}
	return nil
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

func mdInline(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}

// WriteMarkdown writes the Markdown report.
func (r *Report) WriteMarkdown(path string) error {
	return os.WriteFile(path, []byte(r.Markdown()), 0o644)
}

// ZipDir writes every file under dir into a zip at zipPath, with slash-separated paths relative to dir (no top folder, so
// "Extract All" on Windows gives one folder, not two). Unix permissions are kept, so a Linux bundle's programs stay
// executable after unzip.
func ZipDir(dir, zipPath string) error {
	absZip, err := filepath.Abs(zipPath)
	if err != nil {
		return err
	}
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if abs, _ := filepath.Abs(p); abs == absZip { // the zip may be written inside the folder it packs
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		in.Close()
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(zipPath)
	}
	return err
}

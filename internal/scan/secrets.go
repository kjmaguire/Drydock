package scan

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// Folders the secrets engine does not enter: the repository's own database, other tools' trees, and Drydock's worktrees.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".drydock-worktrees": true}

// maxSecretFile is the largest file the secrets engine reads (gitleaks' rules are line-based; a bigger file is a bundle or
// data).
const maxSecretFile = 2 << 20

// scanSecrets walks dir and runs the gitleaks default rules over every text file, calling progress(done, total) as it goes.
// A finding names the rule, the file and the line; the matched value is dropped before it leaves this function. unread
// counts the folders and files that could not be read (permissions): they were not scanned and the caller says so. A root
// that cannot be read is an error.
func scanSecrets(ctx context.Context, dir string, redact func(string) string, progress func(done, total int)) (out []Finding, unread int, err error) {
	det, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, 0, err
	}
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err // the tree itself cannot be read
			}
			unread++
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // symlinks, devices
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxSecretFile {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, unread, err
	}
	total := len(files)
	progress(0, total)
	for i, p := range files {
		if ctx.Err() != nil {
			return out, unread, ctx.Err()
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, dir), string(filepath.Separator)))
		b, ok, readErr := readText(p)
		if readErr {
			unread++
		}
		if ok {
			for _, f := range detectIn(ctx, det, string(b), rel) {
				fd := Finding{Engine: Secrets, Severity: High, Rule: cleanLine(f.RuleID, 120), Title: cleanLine(redact(f.Description), 200),
					File: cleanLine(rel, 300), Line: f.StartLine, EndLine: endLine(f.StartLine, f.EndLine)}
				// the line with the secret masked is what the fingerprint hashes; the value itself never leaves this loop
				text, _ := maskSecret(f.Line, f.Secret, f.RuleID, redact)
				fd.Fingerprint = Fingerprint(Secrets, fd.Rule, fd.File, text, fd.Line)
				out = append(out, fd)
			}
		}
		if (i+1)%25 == 0 || i+1 == total {
			progress(i+1, total)
		}
	}
	return out, unread, nil
}

// detectIn runs the gitleaks rules over one file's text (rel names it), lines counted from 1.
func detectIn(ctx context.Context, det *detect.Detector, raw, rel string) []report.Finding {
	return det.DetectContext(ctx, detect.Fragment{Raw: raw, FilePath: rel, StartLine: 1})
}

func endLine(start, end int) int {
	if end > start {
		return end
	}
	return 0
}

// readText reads a file that looks like text: a NUL byte in its first 8 KiB says it is binary and it is skipped (ok false,
// readErr false); a file that cannot be opened or read is reported (readErr true).
func readText(p string) (b []byte, ok bool, readErr bool) {
	f, err := os.Open(p)
	if err != nil {
		return nil, false, true
	}
	defer f.Close()
	b, err = io.ReadAll(io.LimitReader(f, maxSecretFile+1))
	if err != nil {
		return nil, false, true
	}
	if len(b) > maxSecretFile {
		return nil, false, false
	}
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, false, false
	}
	return b, true, false
}

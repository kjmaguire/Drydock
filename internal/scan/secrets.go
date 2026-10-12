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
)

// Folders the secrets engine does not enter: the repository's own database, other tools' trees, and Drydock's worktrees.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".drydock-worktrees": true}

// maxSecretFile is the largest file the secrets engine reads (gitleaks' rules are line-based; a bigger file is a bundle or
// data).
const maxSecretFile = 2 << 20

// scanSecrets walks dir and runs the gitleaks default rules over every text file, calling progress(done, total) as it goes.
// A finding names the rule, the file and the line; the matched value is dropped before it leaves this function.
func scanSecrets(ctx context.Context, dir string, redact func(string) string, progress func(done, total int)) ([]Finding, error) {
	det, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
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
		return nil, err
	}
	total := len(files)
	progress(0, total)
	var out []Finding
	for i, p := range files {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, dir), string(filepath.Separator)))
		if b, ok := readText(p); ok {
			for _, f := range det.DetectContext(ctx, detect.Fragment{Raw: string(b), FilePath: rel, StartLine: 1}) {
				out = append(out, Finding{Engine: Secrets, Severity: High, Rule: cleanLine(f.RuleID, 120), Title: cleanLine(redact(f.Description), 200),
					File: cleanLine(rel, 300), Line: f.StartLine, EndLine: endLine(f.StartLine, f.EndLine)})
			}
		}
		if (i+1)%25 == 0 || i+1 == total {
			progress(i+1, total)
		}
	}
	return out, nil
}

func endLine(start, end int) int {
	if end > start {
		return end
	}
	return 0
}

// readText reads a file that looks like text: a NUL byte in its first 8 KiB says it is binary and it is skipped.
func readText(p string) ([]byte, bool) {
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSecretFile+1))
	if err != nil || len(b) > maxSecretFile {
		return nil, false
	}
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, false
	}
	return b, true
}

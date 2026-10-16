package checks

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/security"
)

// AddedLine is a line a diff adds, with its line number in the new file.
type AddedLine struct {
	No   int
	Text string
}

// AddedFile is the added lines of one file of a unified diff.
type AddedFile struct {
	Path  string
	Lines []AddedLine
}

// ParseAddedLines extracts the added lines of every file in a unified diff (git diff output). Only added lines are
// returned: context and removed lines are not the task's change and a secret that was already in the repository is not
// this task's finding. Binary files and deletions have no added lines.
func ParseAddedLines(diff string) []AddedFile {
	var out []AddedFile
	var cur *AddedFile
	newNo := 0
	inHunk := false
	for _, l := range strings.Split(diff, "\n") {
		l = strings.TrimSuffix(l, "\r")
		switch {
		case strings.HasPrefix(l, "diff --git "):
			cur, inHunk = nil, false
		case !inHunk && strings.HasPrefix(l, "+++ "):
			p := strings.TrimSpace(strings.TrimPrefix(l, "+++ "))
			if i := strings.IndexByte(p, '\t'); i >= 0 {
				p = p[:i]
			}
			if strings.HasPrefix(p, `"`) {
				if u, err := strconv.Unquote(p); err == nil {
					p = u
				}
			}
			if p == "/dev/null" {
				cur = nil
				continue
			}
			p = strings.TrimPrefix(p, "b/")
			out = append(out, AddedFile{Path: p})
			cur = &out[len(out)-1]
		case strings.HasPrefix(l, "@@"):
			inHunk = true
			// @@ -a,b +c,d @@
			if i := strings.Index(l, "+"); i >= 0 {
				rest := l[i+1:]
				if j := strings.IndexAny(rest, ", "); j >= 0 {
					rest = rest[:j]
				}
				if n, err := strconv.Atoi(rest); err == nil {
					newNo = n
				}
			}
		case !inHunk || cur == nil:
		case strings.HasPrefix(l, "+"):
			cur.Lines = append(cur.Lines, AddedLine{No: newNo, Text: l[1:]})
			newNo++
		case strings.HasPrefix(l, "-"), strings.HasPrefix(l, `\`):
		default:
			newNo++ // context line
		}
	}
	var kept []AddedFile
	for _, f := range out {
		if len(f.Lines) > 0 {
			kept = append(kept, f)
		}
	}
	return kept
}

// SecretFinding locates a secret without carrying it: the rule, the file and the line in the new file.
type SecretFinding struct {
	Rule string `json:"rule"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// ScanDiff scans the added lines of every file with the scanner. A finding never carries the secret text.
func ScanDiff(ctx context.Context, sc security.SecretScanner, diff string) ([]SecretFinding, error) {
	var out []SecretFinding
	for _, f := range ParseAddedLines(diff) {
		var sb strings.Builder
		for _, l := range f.Lines {
			sb.WriteString(l.Text)
			sb.WriteByte('\n')
		}
		fs, err := sc.Scan(ctx, sb.String())
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", f.Path, err)
		}
		for _, x := range fs {
			line := 0
			if i := x.StartLine - 1; i >= 0 && i < len(f.Lines) {
				line = f.Lines[i].No
			}
			out = append(out, SecretFinding{Rule: x.RuleID, File: f.Path, Line: line})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// SecretsSpec is the `secrets` check: it scans the lines the task added with the gitleaks-backed scanner. A finding fails
// the check. The output names rule, file and line and nothing else: the secret value is never written to the result, the
// log, the room or the evidence. diff supplies the task's diff against its base (git.Repo.Diff).
func SecretsSpec(dir string, sc security.SecretScanner, diff func(ctx context.Context) (string, error)) Spec {
	return Spec{Name: "secrets", Argv: []string{"drydock", "scan-added-lines"}, Dir: dir, InProcess: func(ctx context.Context, _ proc.Manager, _ Spec) (Result, error) {
		var res Result
		d, err := diff(ctx)
		if err != nil {
			return res, fmt.Errorf("secrets check: read diff: %w", err)
		}
		fs, err := ScanDiff(ctx, sc, d)
		if err != nil {
			// a scanner that cannot run (size cap, cancelled) must not read as a pass
			res.Errored = true
			res.Output = "secrets check could not run: " + err.Error()
			res.ExitCode = 2
			return res, nil
		}
		if len(fs) == 0 {
			res.Passed = true
			res.Output = "no secrets found in the lines this task added"
			return res, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d possible secret(s) in the lines this task added. Remove them (use environment variables or the credential store); the values are not shown here.\n", len(fs))
		for _, f := range fs {
			fmt.Fprintf(&sb, "%s %s:%d\n", f.Rule, f.File, f.Line)
			res.FailingTests = append(res.FailingTests, fmt.Sprintf("%s %s:%d", f.Rule, f.File, f.Line))
		}
		res.ExitCode = 1
		res.Output = sc.Redact(sb.String()) // defense in depth: nothing that looks like a secret survives, even in a file name
		return res, nil
	}}
}

package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/safefs"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Files in the package as ranges (docs/token-control.md rule 11, decision P119). The files a task names (its focus paths) and
// the files a failing check points at (path:line in its output) enter a work turn's package: a short file whole, a longer one
// only as line ranges around the part that matters (the lines the check output names, else the lines that mention the task's
// identifiers, else its head), each with a read_range pointer for the rest. Never a long file whole.

const (
	// FileLineCap is the longest file that enters a package whole; a longer one enters only as ranges.
	FileLineCap = 150
	// fileRangeContext is how many lines either side of a relevant line a range takes.
	fileRangeContext = 20
	// fileHeadLines is the range of a long file with no relevant line: its first lines.
	fileHeadLines = 40
	// maxFileRanges and maxRangeLines bound the ranges of one file.
	maxFileRanges = 2
	maxRangeLines = 100
	// maxPackageFiles and maxPackageFileBytes bound the excerpts of one package together.
	maxPackageFiles     = 6
	maxPackageFileBytes = 24 << 10
	// maxWholeFileBytes: a short file of long lines enters as ranges too.
	maxWholeFileBytes = 12 << 10
	// maxExcerptLine cuts a single long line (minified code, data).
	maxExcerptLine = 400
	// maxFileRead is the most read of one file to excerpt it.
	maxFileRead = 4 << 20
)

// FileExcerpt is one file of a package: the whole file or the ranges of it that matter, with line numbers.
type FileExcerpt struct {
	Path   string
	Lines  int      // lines in the file
	Ranges [][2]int // 1-based, inclusive, in order
	Whole  bool
	Why    string // why these lines
	Text   string // the excerpt as the agent reads it (redacted)
	// Skip is why a named file did not enter the package (not in the worktree, binary, over the excerpts' budget).
	Skip string
}

// filePieces renders the excerpts as package pieces: file_whole or file_range, cut or left out first when a package is over its
// ceiling (the agent reads the file itself), and a skipped file as a manifest line.
func filePieces(fx []FileExcerpt) []ctxeng.Piece {
	var ps []ctxeng.Piece
	for _, f := range fx {
		if f.Skip != "" {
			ps = append(ps, ctxeng.Piece{Kind: ctxeng.KindFileRange, Source: "file:" + f.Path, Skip: f.Skip})
			continue
		}
		pointer := "read_range on " + f.Path + " (or read the file) for the rest"
		if f.Whole {
			ps = append(ps, quotedPiece(ctxeng.KindFileWhole, "file:"+f.Path, fmt.Sprintf("%d lines: a file of at most %d enters whole", f.Lines, FileLineCap), pointer,
				"\n", fmt.Sprintf("File %s (all %d lines)", f.Path, f.Lines), f.Text, ""))
			continue
		}
		var spans []string
		for _, r := range f.Ranges {
			spans = append(spans, fmt.Sprintf("%d-%d", r[0], r[1]))
		}
		ps = append(ps, quotedPiece(ctxeng.KindFileRange, "file:"+f.Path+" ["+strings.Join(spans, ",")+"]",
			fmt.Sprintf("%s; %d lines, more than %d, so ranges only", f.Why, f.Lines, FileLineCap), pointer,
			"\n", fmt.Sprintf("File %s, lines %s of %d", f.Path, strings.Join(spans, ", "), f.Lines), f.Text,
			fmt.Sprintf("The rest of %s: read_range(path %q, start_line, end_line).\n", f.Path, f.Path)))
	}
	return ps
}

// fileRefRE finds path:line references in check output ("calc_test.go:42:", "./internal/x/y.go:7:3").
var fileRefRE = regexp.MustCompile(`([A-Za-z0-9_.\-/\\]+\.[A-Za-z0-9]+):(\d+)`)

// identRE finds code-shaped words in task text: CamelCase, mixedCase, snake_case or a call.
var identRE = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]{3,}\b`)

// fileExcerpts picks and reads the files for a work turn's package: on a first pass the task's focus paths, then the files a
// failing check in the inbox points at, at most maxPackageFiles and maxPackageFileBytes in all. A later pass (the inbox carries
// a failure or feedback) gets only the files the failing check points at: the focus files were in the first pass, and a later
// pass carries only what changed (token rule 16).
func (d Deps) fileExcerpts(ctx context.Context, t tasks.Task, in workflow.NodeInput) []FileExcerpt {
	if t.Worktree == "" {
		return nil
	}
	refs := map[string][]int{} // path -> lines the check output names
	var order []string
	seen := map[string]bool{}
	addPath := func(p string) {
		p = cleanRel(p)
		if p != "" && !seen[p] {
			seen[p] = true
			order = append(order, p)
		}
	}
	if len(inboxFeedback(in)) == 0 {
		for _, f := range t.Files {
			if !strings.ContainsAny(f, "*?[") { // a glob names no one file
				addPath(f)
			}
		}
	}
	var failing []string
	for _, m := range in.Inbox {
		var cr workflow.CheckResult
		if json.Unmarshal(m.Value, &cr) != nil || cr.Name == "" || cr.Passed {
			continue
		}
		failing = append(failing, cr.FailingTests...)
		for _, mm := range fileRefRE.FindAllStringSubmatch(cr.Output, 200) {
			p := cleanRel(mm[1])
			n, err := strconv.Atoi(mm[2])
			if p == "" || err != nil || n <= 0 {
				continue
			}
			if _, err := safefs.Lstat(t.Worktree, p); err != nil {
				continue // a path of the toolchain or the standard library, not of the worktree
			}
			refs[p] = append(refs[p], n)
			addPath(p)
		}
	}
	idents := taskIdents(t, failing)
	var out []FileExcerpt
	used, taken := 0, 0
	for _, p := range order {
		if taken == maxPackageFiles {
			out = append(out, FileExcerpt{Path: p, Skip: fmt.Sprintf("not included: at most %d files enter a package", maxPackageFiles)})
			continue
		}
		fx := excerptFile(t.Worktree, p, refs[p], idents)
		if fx.Skip == "" {
			fx.Text = d.redact(fx.Text)
			if used+len(fx.Text) > maxPackageFileBytes {
				fx = FileExcerpt{Path: p, Skip: fmt.Sprintf("not included: the file excerpts of a package take at most %d KB", maxPackageFileBytes>>10)}
			} else {
				used += len(fx.Text)
				taken++
			}
		}
		out = append(out, fx)
	}
	return out
}

// cleanRel turns a path from a task or a check's output into a worktree-relative slash path; "" when it is not one.
func cleanRel(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	p = strings.TrimPrefix(p, "./")
	if p == "" || strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return ""
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// taskIdents are the code-shaped words of the task and its failing tests: what a relevant line of a long file mentions.
func taskIdents(t tasks.Task, failing []string) []string {
	text := t.Title + "\n" + t.Description + "\n" + strings.Join(t.Acceptance, "\n") + "\n" + strings.Join(failing, "\n")
	seen := map[string]bool{}
	var out []string
	for _, w := range identRE.FindAllString(text, -1) {
		codey := strings.Contains(w, "_") || strings.ContainsAny(w[1:], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || strings.Contains(text, w+"(")
		if codey && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// excerptFile reads one file and takes it whole (at most FileLineCap lines) or as ranges: around the lines the check output
// names, else around the first lines that mention the task's identifiers, else its head.
func excerptFile(root, rel string, refLines []int, idents []string) FileExcerpt {
	fx := FileExcerpt{Path: rel}
	b, err := safefs.ReadFile(root, rel, maxFileRead)
	if err != nil {
		fx.Skip = "not included: not a file in the worktree"
		return fx
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		fx.Skip = "not included: a binary file"
		return fx
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	fx.Lines = len(lines)
	if fx.Lines <= FileLineCap && len(b) <= maxWholeFileBytes {
		fx.Whole, fx.Ranges, fx.Why = true, [][2]int{{1, fx.Lines}}, "the whole file"
		fx.Text = numbered(lines, fx.Ranges)
		return fx
	}
	var hits []int
	switch {
	case len(refLines) > 0:
		hits, fx.Why = refLines, "around the lines a failing check names"
	default:
		for i, l := range lines {
			for _, w := range idents {
				if strings.Contains(l, w) {
					hits = append(hits, i+1)
					break
				}
			}
			if len(hits) == maxFileRanges {
				break
			}
		}
		if len(hits) > 0 {
			fx.Why = "around the first lines that mention the task's identifiers"
		}
	}
	if len(hits) == 0 {
		fx.Ranges, fx.Why = [][2]int{{1, fileHeadLines}}, "its first lines (no line of it is named by a check or mentions the task's identifiers)"
	} else {
		fx.Ranges = ranges(hits, fx.Lines)
	}
	fx.Text = numbered(lines, fx.Ranges)
	return fx
}

// ranges merges the windows around the hit lines into at most maxFileRanges ranges of at most maxRangeLines lines each.
func ranges(hits []int, n int) [][2]int {
	sort.Ints(hits)
	var out [][2]int
	for _, h := range hits {
		if h > n {
			continue
		}
		lo, hi := max(1, h-fileRangeContext), min(n, h+fileRangeContext)
		if k := len(out) - 1; k >= 0 && lo <= out[k][1]+1 {
			out[k][1] = min(max(out[k][1], hi), out[k][0]+maxRangeLines-1)
			continue
		}
		if len(out) == maxFileRanges {
			break
		}
		out = append(out, [2]int{lo, hi})
	}
	if len(out) == 0 {
		out = [][2]int{{1, min(n, fileHeadLines)}}
	}
	return out
}

// numbered renders the ranges of lines with their numbers, long lines cut.
func numbered(lines []string, rs [][2]int) string {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteString("...\n")
		}
		for n := r[0]; n <= r[1] && n <= len(lines); n++ {
			l := lines[n-1]
			if len(l) > maxExcerptLine {
				l = strings.ToValidUTF8(l[:maxExcerptLine], "") + " [line cut]"
			}
			fmt.Fprintf(&b, "%d| %s\n", n, l)
		}
	}
	return b.String()
}

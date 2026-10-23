package scan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zricethezav/gitleaks/v8/detect"
)

// The code around a finding (decision P97), for the Security page's detail pane: a few lines before and after the finding,
// read from the scanned folder when they are asked for (a report never quotes a line). Every secret the gitleaks rules find
// in the file is masked in them, the finding's own secret too, and the app's scanner redacts every line; a secrets finding
// whose secret is no longer found where it was has its lines masked whole. Only a regular text file inside the scanned
// folder is read: its path is resolved (links followed) and must stay inside the folder, the same folder rules as the scan's.

var isWindows = runtime.GOOS == "windows"

// ErrOutsideFolder is returned for a finding whose file is not inside the scanned folder.
var ErrOutsideFolder = errors.New("the finding's file is not inside the scanned folder, so it is not read")

var errNotText = errors.New("not a text file")

// Context lines: the default before and after, the most a caller may ask for, the most lines in all, and a line's longest text.
const (
	ContextAround    = 2
	maxContextAround = 10
	maxContextLines  = 40
	maxContextLine   = 400
	// MaxFileLines is the most lines ReadFile answers with.
	MaxFileLines = 2000
)

// ContextLine is one line of the code around a finding.
type ContextLine struct {
	N    int    `json:"n"`
	Text string `json:"text"`
	// Hit marks the finding's own lines; Masked a line where a secret was masked; Cut a line longer than the page shows.
	Hit    bool `json:"hit,omitempty"`
	Masked bool `json:"masked,omitempty"`
	Cut    bool `json:"cut,omitempty"`
}

// FindingContext is a finding with the code around it and what it means.
type FindingContext struct {
	ScanID  string        `json:"scanId"`
	Finding Finding       `json:"finding"`
	Lines   []ContextLine `json:"lines"`
	// Changed says the finding's line no longer reads as it did when the scan ran (the file was edited since): the lines are
	// what the file holds now.
	Changed bool `json:"changed,omitempty"`
	// Note says why there are no lines (a dependency finding, a file that is gone, a binary or very large file).
	Note string `json:"note,omitempty"`
	// More says the file has more lines than ReadFile answered with (MaxFileLines).
	More   bool   `json:"more,omitempty"`
	Advice Advice `json:"advice"`
}

// resolveInside is the file rel (forward slashes, relative to dir) with links followed, refused unless it is inside dir
// (also with dir's links followed). A file that is not there is an error that is not ErrOutsideFolder.
func resolveInside(dir, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("the finding names no file")
	}
	p := filepath.FromSlash(rel)
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || (isWindows && strings.Contains(rel, ":")) {
		return "", ErrOutsideFolder
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrOutsideFolder
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("the scanned folder %s is not there any more", dir)
	}
	full := filepath.Join(dir, clean)
	if !within(full, dir) {
		return "", ErrOutsideFolder
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("%s is not there any more", rel)
	}
	if !within(real, realDir) {
		return "", ErrOutsideFolder
	}
	return real, nil
}

// ReadContext reads the lines around a finding from dir, the folder its scan read: around lines before and after
// (ContextAround when 0, at most maxContextAround), secrets masked, every line redacted. A finding outside dir is refused
// with ErrOutsideFolder; a file that cannot be shown (gone, binary, too big) and a finding without a line answer with a Note
// and no lines.
func ReadContext(ctx context.Context, dir string, f Finding, around int, redact func(string) string) (FindingContext, error) {
	if around <= 0 {
		around = ContextAround
	}
	return readFinding(ctx, dir, f, min(around, maxContextAround), false, redact)
}

// ReadFile reads the whole file of a finding from dir (the Security page's "Open the file", decision P97), with the same
// masking, redaction and folder rules as ReadContext: its first MaxFileLines lines (More says the file has more), the
// finding's own lines marked. A dependency finding's manifest is read whole too; a finding that names no file, and a file that
// cannot be shown, answer with a Note and no lines.
func ReadFile(ctx context.Context, dir string, f Finding, redact func(string) string) (FindingContext, error) {
	return readFinding(ctx, dir, f, 0, true, redact)
}

// readFinding is ReadContext (around lines about the finding) and, with entire, ReadFile (the file from its first line).
func readFinding(ctx context.Context, dir string, f Finding, around int, entire bool, redact func(string) string) (FindingContext, error) {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	out := FindingContext{Finding: f, Lines: []ContextLine{}, Advice: AdviceFor(f)}
	if f.File == "" {
		out.Note = "This finding names no file."
		return out, nil
	}
	p, err := resolveInside(dir, f.File)
	if errors.Is(err, ErrOutsideFolder) {
		return out, err
	}
	if f.Line <= 0 && !entire {
		if f.Engine == Deps {
			out.Note = "A dependency finding names a package in " + f.File + ", not a line of code."
		} else {
			out.Note = "This finding names a file, not a line."
		}
		return out, nil
	}
	if err != nil {
		out.Note = cleanLine(redact(err.Error()), 200) + "."
		return out, nil
	}
	b, why, err := readSafeText(p)
	if err != nil {
		out.Note = "The lines are not shown: " + why + "."
		return out, nil
	}
	text := string(b)
	lines := splitLines(text)
	if f.Line > len(lines) {
		out.Changed = true
		out.Note = fmt.Sprintf("The file has %d lines now; the finding was on line %d when the scan ran.", len(lines), f.Line)
		if !entire {
			return out, nil
		}
	}
	// lineNow: the finding's line is in the file as it reads now
	lineNow := f.Line >= 1 && f.Line <= len(lines)
	last := max(f.Line, f.EndLine)
	var from, to int
	if entire {
		from, to = 1, min(len(lines), MaxFileLines)
		out.More = len(lines) > to
	} else {
		from, to = max(1, f.Line-around), min(len(lines), last+around)
		if to-from+1 > maxContextLines {
			to = from + maxContextLines - 1
		}
	}

	// every secret in the file, by line: the value to mask on single-line matches, whole lines for a match over several
	masks := map[int][]string{}
	whole := map[int]bool{}
	det, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return out, err
	}
	ownFound := false
	ownText := ""
	for _, d := range detectIn(ctx, det, text, f.File) {
		if d.EndLine > d.StartLine || strings.Contains(d.Secret, "\n") {
			for n := d.StartLine; n <= d.EndLine; n++ {
				whole[n] = true
			}
		} else if d.Secret != "" {
			masks[d.StartLine] = append(masks[d.StartLine], d.Secret)
		} else {
			whole[d.StartLine] = true
		}
		if f.Engine == Secrets && d.RuleID == f.Rule && d.StartLine == f.Line {
			ownFound = true
			if t, ok := maskSecret(d.Line, d.Secret, d.RuleID, redact); ok {
				ownText = t
			}
		}
	}
	// the finding's own text as the scan hashed it, to tell whether the line changed since
	var now string
	switch f.Engine {
	case Secrets:
		if !ownFound {
			for n := f.Line; n <= last; n++ {
				whole[n] = true // the secret is not found where it was: nothing of those lines is shown
			}
		}
		now = ownText
	case Deps:
		now = depsText(f)
	default:
		if lineNow {
			now = redact(strings.Join(lines[f.Line-1:min(len(lines), max(f.EndLine, f.Line), f.Line+maxHashedLines-1)], "\n"))
		}
	}
	if f.Fingerprint != "" && (lineNow || f.Engine == Deps) {
		out.Changed = Fingerprint(f.Engine, f.Rule, f.File, now, f.Line) != BaseFingerprint(f.Fingerprint)
	}
	for n := from; n <= to; n++ {
		l := ContextLine{N: n, Hit: n >= f.Line && n <= last}
		raw := lines[n-1]
		if whole[n] {
			l.Text, l.Masked = secretMask(secretRuleOn(f, n)), true
		} else {
			for _, sec := range masks[n] {
				if strings.Contains(raw, sec) {
					raw, l.Masked = strings.ReplaceAll(raw, sec, secretMask("secret")), true
				}
			}
			red := redact(raw)
			if red != raw {
				l.Masked = true
			}
			l.Text, l.Cut = capLine(red, maxContextLine)
		}
		out.Lines = append(out.Lines, l)
	}
	return out, nil
}

// secretRuleOn names the mask of a line masked whole: the finding's rule on its own lines, else "secret".
func secretRuleOn(f Finding, n int) string {
	if f.Engine == Secrets && n >= f.Line && n <= max(f.Line, f.EndLine) {
		return cleanLine(f.Rule, 80)
	}
	return "secret"
}

// capLine drops control characters but tabs and caps a line at max bytes on a rune boundary, with an ellipsis.
func capLine(s string, max int) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimRight(b.String(), " \t")
	if len(out) <= max {
		return out, false
	}
	cut := max - 1
	for cut > 0 && !isRuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + "…", true
}

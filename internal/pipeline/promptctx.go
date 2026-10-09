package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// What every agent turn of a task is told besides its own instructions: the project brief, the accepted spec, the architect's
// design notes, the worker's own definition of done. Each piece is bounded, because every byte here is paid for on every turn.

// Bounds on what goes into a prompt.
const (
	maxBriefPrompt = 2000     // the project brief in a turn prompt (AGENTS.md carries up to maxBriefFile)
	maxBriefFile   = 4000     // the project brief in the generated AGENTS.md
	maxSpecPrompt  = 4000     // the accepted spec (problem, scope, non-goals) in a builder's prompt
	maxDesignNotes = 3000     // the architect's notes in a builder's prompt
	maxDescFirst   = 4000     // the task description on a first pass
	maxDescRework  = 1500     // the task description on a later pass (compact)
	maxGuidance    = 4000     // a human's or the team lead's guidance
	reviewDiffMax  = 48 << 10 // the diff a reviewer sees in full
	maxStatFiles   = 200      // files listed in the per-file stat of a truncated diff
	maxDoD         = 600      // a worker's stated definition of done
)

// PromptContext is what a turn prompt carries besides the task itself. Empty fields are left out.
type PromptContext struct {
	Brief  string // the project brief as the human wrote it (bounded by the caller)
	Spec   string // the accepted spec without its criteria section (feature templates)
	Design string // the architect's design notes (full-stack template)
	DoD    string // the definition of done the worker stated for a task without acceptance criteria
}

// brief returns the project's brief for a prompt, bounded; empty when the hook is not wired or the project has none.
func (d Deps) brief(ctx context.Context, projectID string) string {
	if d.ProjectBrief == nil || projectID == "" {
		return ""
	}
	return clip(strings.TrimSpace(cleanText(d.ProjectBrief(ctx, projectID))), maxBriefPrompt)
}

// briefBlock renders the project brief for a prompt: it is the human's own text, so it is background, not quoted agent data.
func briefBlock(brief string) string {
	if strings.TrimSpace(brief) == "" {
		return ""
	}
	return "Project brief, written by the human who owns this project (background for every task here):\n<<<BRIEF\n" +
		strings.ReplaceAll(strings.TrimSpace(brief), "BRIEF>>>", "BRIEF>> >") + "\nBRIEF>>>\n\n"
}

// BriefForInstructions renders the project brief for the generated AGENTS.md (ctxeng.Engine.Standing): bounded, labelled as the
// human's. Empty input gives empty output, so a project without a brief gets no section.
func BriefForInstructions(brief string) string {
	brief = strings.TrimSpace(cleanText(brief))
	if brief == "" {
		return ""
	}
	return "Project brief, written by the human who owns this project:\n\n" + clip(brief, maxBriefFile)
}

// taskBlock is the task as every turn sees it: title, description (compact on a later pass) and the acceptance criteria.
func taskBlock(t tasks.Task, compact bool) string {
	var b strings.Builder
	n := maxDescFirst
	if compact {
		n = maxDescRework
	}
	fmt.Fprintf(&b, "Task: %s\n", t.Title)
	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString("\n" + clip(d, n) + "\n")
	}
	if len(t.Acceptance) > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for _, a := range t.Acceptance {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	return b.String()
}

// dodAsk is what a worker on a task without acceptance criteria is asked to do first. Short on purpose.
const dodAsk = "No acceptance criteria were given. Before you change anything, write \"Definition of done:\" followed by 1 to 3 short bullet points: what you will deliver and how it can be checked. Work to it, and start your final summary with the same \"Definition of done:\" lines.\n"

var dodRE = regexp.MustCompile(`(?i)definition of done\s*:?`)

// ExtractDoD finds the "Definition of done:" bullets in a worker's text (at most three, bounded). Empty when there are none.
func ExtractDoD(text string) string {
	loc := dodRE.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	var out []string
	rest := text[loc[1]:]
	for _, l := range strings.Split(rest, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			if len(out) > 0 {
				break
			}
			continue
		}
		if !(strings.HasPrefix(l, "-") || strings.HasPrefix(l, "*") || strings.HasPrefix(l, "•") || (len(l) > 1 && l[0] >= '1' && l[0] <= '9' && (l[1] == '.' || l[1] == ')'))) {
			if len(out) > 0 {
				break
			}
			continue
		}
		out = append(out, "- "+strings.TrimSpace(strings.TrimLeft(l, "-*•0123456789.) ")))
		if len(out) == 3 {
			break
		}
	}
	return clip(strings.Join(out, "\n"), maxDoD)
}

// dodKind is the evidence kind that keeps a worker's stated definition of done for the reviewer and the human.
const dodKind = "definition_of_done"

// recordDoD stores the worker's definition of done for a task that has no acceptance criteria (once per distinct text).
func (d Deps) recordDoD(ctx context.Context, t tasks.Task, text string) {
	if len(t.Acceptance) > 0 {
		return
	}
	dod := ExtractDoD(text)
	if dod == "" {
		return
	}
	_ = d.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: dodKind, Summary: dod})
}

// statedDoD returns the latest definition of done the worker stated, if any.
func statedDoD(ev []tasks.Evidence) string {
	if e, ok := latestEvidence(ev, dodKind); ok {
		return e.Summary
	}
	return ""
}

// addEvidenceOnce records evidence unless an identical item (same kind, summary and ref) is already there: nodes may run
// again after a crash, and evidence must not double up.
func (d Deps) addEvidenceOnce(ctx context.Context, taskID string, e tasks.Evidence) error {
	have, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return err
	}
	for _, x := range have {
		if x.Kind == e.Kind && x.Summary == e.Summary && x.Ref == e.Ref {
			return nil
		}
	}
	_, err = d.Tasks.AddEvidence(ctx, taskID, e)
	return err
}

// SpecContext is the accepted spec as builders see it: problem, scope, non-goals and open questions. The criteria section is
// dropped because the criteria are listed with their ids next to it.
func SpecContext(body string) string {
	var keep []string
	skip := false
	for _, l := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "## ") || t == "##" {
			skip = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(t, "##")), "acceptance criteria")
		}
		if !skip {
			keep = append(keep, l)
		}
	}
	return clip(strings.TrimSpace(strings.Join(keep, "\n")), maxSpecPrompt)
}

// acceptedSpec reads the spec a human accepted for the task (spec_accept evidence), bounded; empty when there is none.
func (d Deps) acceptedSpec(ctx context.Context, taskID string) string {
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return ""
	}
	e, ok := latestEvidence(ev, "spec_accept")
	if !ok || e.Passed == nil || !*e.Passed || e.Ref == "" {
		return ""
	}
	b, err := os.ReadFile(e.Ref)
	if err != nil {
		return ""
	}
	return SpecContext(string(b))
}

// designNotes reads the architect's latest design notes for the task, bounded.
func (d Deps) designNotes(ctx context.Context, taskID string) string {
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return ""
	}
	if e, ok := latestEvidence(ev, "design"); ok && e.Ref != "" {
		if b, err := os.ReadFile(e.Ref); err == nil {
			return clip(strings.TrimSpace(string(b)), maxDesignNotes)
		}
	}
	return ""
}

// HumanText renders a human's answer data as readable text for an agent: a plain string, the text/feedback/guidance/message
// field of an object, or the object's fields as "key: value" lines. Never raw JSON.
func HumanText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "{}" || s == `""` {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return clip(strings.TrimSpace(str), maxGuidance)
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return clip(s, maxGuidance)
	}
	for _, k := range []string{"text", "guidance", "feedback", "message", "comment", "answer"} {
		if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
			return clip(strings.TrimSpace(v), maxGuidance)
		}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k == "action" {
			continue // a control field (continue/stop), not guidance
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		switch v := obj[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				lines = append(lines, k+": "+strings.TrimSpace(v))
			}
		case nil:
		default:
			b, _ := json.Marshal(v)
			lines = append(lines, k+": "+string(b))
		}
	}
	return clip(strings.Join(lines, "\n"), maxGuidance)
}

// FileStat is one file of a diff.
type FileStat struct {
	Path       string
	Insertions int
	Deletions  int
}

// DiffFileStats counts added and removed lines per file in a unified diff.
func DiffFileStats(diff string) []FileStat {
	var out []FileStat
	cur := -1
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			path := l[len("diff --git "):]
			if i := strings.Index(path, " b/"); i >= 0 {
				path = path[i+3:]
			}
			out = append(out, FileStat{Path: path})
			cur = len(out) - 1
		case cur < 0, strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			out[cur].Insertions++
		case strings.HasPrefix(l, "-"):
			out[cur].Deletions++
		}
	}
	return out
}

// diffSection is the diff as a reviewer sees it: in full up to reviewDiffMax; beyond that, the first part plus an explicit note
// and the per-file stat of the whole diff, so the reviewer knows what it did not see and reads those files itself.
func diffSection(diff string) string {
	if len(diff) <= reviewDiffMax {
		return fmt.Sprintf("\nDiff of the change:\n```diff\n%s\n```\n", diff)
	}
	var b strings.Builder
	stats := DiffFileStats(diff)
	fmt.Fprintf(&b, "\nThe diff is %d KB, more than the %d KB shown below: it is TRUNCATED. Every file it changes (whole diff):\n", len(diff)>>10, reviewDiffMax>>10)
	for i, s := range stats {
		if i == maxStatFiles {
			fmt.Fprintf(&b, "- ... and %d more files\n", len(stats)-maxStatFiles)
			break
		}
		fmt.Fprintf(&b, "- %s +%d -%d\n", s.Path, s.Insertions, s.Deletions)
	}
	b.WriteString("Read the files that are not shown in full in the working directory before you decide.\n")
	fmt.Fprintf(&b, "\nDiff of the change (first %d KB):\n```diff\n%s\n```\n", reviewDiffMax>>10, clip(diff, reviewDiffMax))
	return b.String()
}

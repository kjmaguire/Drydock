package context

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// A ceiling on the whole initial package of a turn (docs/token-control.md rule 9, decision P119). What an agent turn starts with
// is assembled from pieces: the task and the instructions, which are never cut, and pieces the agent can do without or fetch
// itself (file excerpts, the repo map, memory, design notes, the spec, the diff, the handoff, check output). When the package is
// over its ceiling, the cuttable pieces are cut or left out in one fixed order (CutOrder), and every piece is listed in the
// manifest with what happened to it and why. Sizes are the chars/4 estimate used everywhere in Drydock, not a tokenizer.

// Kinds of package pieces (the manifest's source_type, token-control section 2).
const (
	KindTask                = "task"
	KindRoleInstructions    = "role_instructions"
	KindProjectInstructions = "project_instructions"
	KindPackInstructions    = "pack_instructions"
	KindWorkflowState       = "workflow_state"
	KindFileRange           = "file_range"
	KindFileWhole           = "file_whole"
	KindRepoMap             = "repo_map"
	KindMemory              = "memory_hit"
	KindSkill               = "skill"
	KindDesign              = "design"
	KindSpec                = "spec"
	KindDiff                = "diff"
	KindHandoff             = "handoff"
	KindFeedback            = "previous_result"
)

// CutOrder is the fixed order in which pieces are cut or left out of a package over its ceiling, first to last: first what the
// agent can read itself with a tool (files, the repo map, memory, the skill list), then background (design notes, the spec, the
// diff), last what says where the work stands and what to fix now (the handoff, then the failing check or feedback). A kind not
// in the list (the task, role, project and pack instructions, workflow state) is never cut.
var CutOrder = []string{KindFileRange, KindFileWhole, KindRepoMap, KindMemory, KindSkill, KindDesign, KindSpec, KindDiff, KindHandoff, KindFeedback}

// DefaultCeiling is the package ceiling in tokens when a project sets none: well above a usual first turn (instructions within
// their 8,000-token budget plus a task, a handoff and a few file excerpts), so it binds only when something is unusually large.
const DefaultCeiling = 20000

// Ceiling bounds (the project setting).
const (
	MinCeiling = 4000
	MaxCeiling = 200000
)

// ExclusionOverBudget is the manifest's exclusion reason for a piece cut or left out by the ceiling.
const ExclusionOverBudget = "over_budget"

// minCutBytes is the smallest cut worth keeping: below it a piece is left out whole instead of being cut to a stub.
const minCutBytes = 320

// Cuttable reports whether pieces of a kind may be cut or left out by the ceiling.
func Cuttable(kind string) bool {
	for _, k := range CutOrder {
		if k == kind {
			return true
		}
	}
	return false
}

// Piece is one part of a package.
type Piece struct {
	Kind   string
	Source string // manifest source, for example "task", "file:internal/x.go [40-100]", "handoff:hnd_..."
	Text   string // the text as it goes into the prompt, with its own separators
	// Counted pieces are part of the package but are not rendered here: the instruction files a CLI reads from the worktree.
	// Bytes is their size; they are never cut by Assemble.
	Counted bool
	Bytes   int
	Reason  string // why the piece was selected
	// Pointer says how the agent gets what a cut left out ("read_range on the file", "search_memory").
	Pointer string
	// Cut, if set, shortens the piece's text to at most max bytes (a diff keeps its per-file stat, say); nil cuts at a line
	// boundary and appends a note with the pointer.
	Cut      func(max int) string
	Priority int
	// Skip, when set, makes the piece a manifest line only: something the package named but did not take (a file that is not
	// in the worktree, say), with this reason. It has no text and no size.
	Skip string
}

func (p Piece) size() int {
	if p.Skip != "" {
		return 0
	}
	if p.Counted {
		return p.Bytes
	}
	return len(p.Text)
}

func (p Piece) tokens() int {
	if p.Skip != "" {
		return 0
	}
	if p.Counted {
		return (p.Bytes + 3) / 4
	}
	return EstimateTokens(p.Text)
}

// EstimateTokens is the chars/4 estimate used everywhere in Drydock (an assumption, not a tokenizer).
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + 3) / 4
}

// Assembly is an assembled package.
type Assembly struct {
	Text    string // the rendered pieces, in order
	Ceiling int    // tokens; 0: no ceiling
	Tokens  int    // estimated tokens of everything included, counted pieces too
	// Fixed is the estimated tokens of the pieces that are never cut; Over is true when they alone are above the ceiling.
	Fixed int
	Over  bool
	// Pieces are the pieces as included (Text cut where it was cut); Items the manifest lines, one per piece plus the package
	// line (source "budget:package").
	Pieces []Piece
	Items  []Item
}

// LeftOut lists the pieces the ceiling left out or cut (their manifest items).
func (a Assembly) LeftOut() []Item {
	var out []Item
	for _, it := range a.Items {
		if it.Exclusion == ExclusionOverBudget {
			out = append(out, it)
		}
	}
	return out
}

// Assemble renders pieces under a ceiling in tokens (0 or less: none). When the package is over it, the pieces whose kind is in
// CutOrder are cut or left out kind by kind in that order, the latest piece of a kind first, until it fits; the others are never
// touched, so a package whose fixed pieces alone are over the ceiling is assembled with every cuttable piece left out and Over set.
func Assemble(ceiling int, pieces []Piece) Assembly {
	ps := append([]Piece(nil), pieces...)
	orig := make([]int, len(ps))
	total := 0
	for i, p := range ps {
		orig[i] = p.size()
		total += p.tokens()
	}
	what := make([]string, len(ps)) // "", "cut", "dropped"
	if ceiling > 0 && total > ceiling {
		for _, kind := range CutOrder {
			for i := len(ps) - 1; i >= 0 && total > ceiling; i-- {
				p := ps[i]
				if p.Kind != kind || p.Counted || p.Text == "" {
					continue
				}
				excess := total - ceiling
				keep := (p.tokens()-excess)*4 - 8 // bytes; a little room for multi-byte runes and the note
				var cut string
				if keep >= minCutBytes {
					cut = cutPiece(p, keep)
				}
				if cut == "" || EstimateTokens(cut) >= p.tokens() {
					total -= p.tokens()
					ps[i].Text, what[i] = "", "dropped"
					continue
				}
				total -= p.tokens() - EstimateTokens(cut)
				ps[i].Text, what[i] = cut, "cut"
			}
		}
	}
	a := Assembly{Ceiling: max(ceiling, 0), Tokens: total}
	var sb strings.Builder
	for i, p := range ps {
		if !Cuttable(p.Kind) {
			a.Fixed += p.tokens()
		}
		if p.Skip != "" {
			a.Items = append(a.Items, Item{Source: p.Source, Kind: p.Kind, Reason: p.Skip, Priority: p.Priority})
			continue
		}
		it := Item{Source: p.Source, Kind: p.Kind, Included: what[i] != "dropped", Bytes: p.size(), Reason: p.Reason, Priority: p.Priority, Tokens: p.tokens()}
		switch what[i] {
		case "dropped":
			it.Bytes, it.Tokens, it.Exclusion = orig[i], 0, ExclusionOverBudget
			it.Reason = fmt.Sprintf("left out: over the package ceiling of %d tokens (%d bytes)%s", ceiling, orig[i], pointerNote(p.Pointer))
		case "cut":
			it.Exclusion = ExclusionOverBudget
			it.Reason = fmt.Sprintf("cut from %d to %d bytes: over the package ceiling of %d tokens%s", orig[i], len(p.Text), ceiling, pointerNote(p.Pointer))
		}
		a.Items = append(a.Items, it)
		if what[i] != "dropped" {
			a.Pieces = append(a.Pieces, p)
			if !p.Counted {
				sb.WriteString(p.Text)
			}
		}
	}
	a.Text = sb.String()
	a.Over = ceiling > 0 && a.Fixed > ceiling
	line := Item{Source: "budget:package", Kind: "budget", Included: true, Tokens: a.Tokens, Priority: 0}
	switch {
	case ceiling <= 0:
		line.Reason = fmt.Sprintf("about %d tokens (estimated); no package ceiling", a.Tokens)
	case a.Over:
		line.Reason = fmt.Sprintf("OVER THE CEILING: the task and instructions alone are about %d tokens, more than the package ceiling of %d; every piece that may be cut was left out and nothing that may not was touched", a.Fixed, ceiling)
	case len(a.LeftOut()) > 0:
		line.Reason = fmt.Sprintf("about %d tokens of a package ceiling of %d (estimated): %d piece(s) cut or left out to fit, in the fixed order", a.Tokens, ceiling, len(a.LeftOut()))
	default:
		line.Reason = fmt.Sprintf("about %d tokens of a package ceiling of %d (estimated): nothing cut", a.Tokens, ceiling)
	}
	a.Items = append(a.Items, line)
	return a
}

func pointerNote(p string) string {
	if p == "" {
		return ""
	}
	return "; " + p
}

// cutPiece shortens a piece to at most max bytes with its own Cut, or at a line boundary with a note.
func cutPiece(p Piece, max int) string {
	if p.Cut != nil {
		s := p.Cut(max)
		if len(s) > max {
			return ""
		}
		return s
	}
	note := "\n[cut to fit the context ceiling"
	if p.Pointer != "" {
		note += "; " + p.Pointer
	}
	note += "]\n"
	room := max - len(note)
	if room <= 0 {
		return ""
	}
	head := p.Text[:min(room, len(p.Text))]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	if i := strings.LastIndexByte(head, '\n'); i > len(head)/2 {
		head = head[:i]
	}
	return head + note
}

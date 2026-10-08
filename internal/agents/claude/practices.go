package claude

import "strings"

// PracticesVersion changes whenever the text below changes, so a run records which wording it was given.
const PracticesVersion = 1

// Role families for the practices text. A role the planner or the human typed is mapped to one of them.
const (
	FamilyWorker   = "worker"   // writes code
	FamilyReviewer = "reviewer" // reads and judges; never modifies
	FamilyPlanner  = "planner"  // writes specs, designs and plans; does not write code
)

// FamilyOf maps a role name to its family. Unknown roles are workers: they are the ones that change files.
func FamilyOf(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "reviewer", "security", "security reviewer", "code reviewer":
		return FamilyReviewer
	case "pm", "product manager", "architect", "ux designer", "ux", "planner":
		return FamilyPlanner
	}
	return FamilyWorker
}

const practicesCommon = `Working practices for this team (Claude Code best practices, applied by Drydock):
- You work in your own copy of the repository (a Git worktree), next to other agents working on other tasks in parallel. Change only what your task needs.
- Keep your context small: read the files the task names or that you need, not the whole repository. Follow the patterns already in the code and do not add dependencies.`

const practicesWorker = `
- Read before you edit. For a change that touches several files, look at the existing code and its tests first and settle the approach, then make the change.
- Drydock runs the project's checks after your turn and sends you the output if they fail. Fix the root cause. Never skip, delete or weaken a test, or suppress an error, to make a check pass.
- If the task needs a change outside its scope (shared configuration, lockfiles, generated files, public interfaces, files that belong to another task), say so in your summary instead of making it.
- If the requirements are ambiguous in a way that changes the design, take the simplest reading, state your assumption and continue.
- Finish with a short summary: the files you changed, what you verified and how, and anything you could not do. Give evidence, not claims.`

const practicesReviewer = `
- You did not write this change and you start without its history. Judge it against the task and its acceptance criteria only. You do not modify files.
- Ask for a revision only for problems that affect correctness, the stated requirements, security, or changes nobody asked for. Do not ask for style changes, extra abstraction, extra defensive code, or tests for cases that cannot happen; mention those as optional notes, if at all.
- For each problem give the file and line and say what would fix it.`

const practicesPlanner = `
- Your output is words, not code: do not write or change code unless your task says to.
- Be specific: name the files, interfaces and edge cases involved, say what is out of scope, and state how the result will be verified.
- Split work so that parallel tasks own different files. Say which files each piece touches.`

// Practices returns the working-practices text for a role. It is short on purpose: every line is paid for in tokens on every run,
// and a long list of rules makes the model ignore the ones that matter (Anthropic's CLAUDE.md guidance).
func Practices(role string) string {
	switch FamilyOf(role) {
	case FamilyReviewer:
		return practicesCommon + practicesReviewer
	case FamilyPlanner:
		return practicesCommon + practicesPlanner
	}
	return practicesCommon + practicesWorker
}

// Preset is a starting model and effort for a role, from Anthropic's model-selection and effort guidance.
type Preset struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Why    string `json:"why"`
}

// PresetFor suggests a model and effort for a role. It is a suggestion shown to the human, never applied silently.
func PresetFor(role string) Preset {
	switch FamilyOf(role) {
	case FamilyReviewer:
		return Preset{"sonnet", "high", "Review is where verification matters and edge cases hide, so a higher effort pays off; Sonnet handles most review well."}
	case FamilyPlanner:
		if strings.Contains(strings.ToLower(role), "architect") {
			return Preset{"opus", "high", "Architecture decisions are the work Anthropic reserves Opus for: complex, multi-step reasoning."}
		}
		return Preset{"sonnet", "medium", "Specs and plans for a clear scope do not need the most expensive model."}
	}
	return Preset{"sonnet", "medium", "Sonnet handles most coding tasks well and costs less than Opus; medium effort fits day-to-day work with a clear scope. Raise it for bug fixes in unfamiliar code."}
}

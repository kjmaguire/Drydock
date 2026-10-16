package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CLIInfo is what the runner found for one CLI.
type CLIInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	// Source says where it came from: "path" (found on PATH), "latest" (installed by --latest), "override" (DRYDOCK_ACP_*).
	Source string `json:"source,omitempty"`
	Status string `json:"status,omitempty"` // the Connections status for a manifest CLI (ready, unauthenticated, ...)
	Detail string `json:"detail,omitempty"`
}

// Update is one line of the version watch.
type Update struct {
	CLI       string `json:"cli"`
	Package   string `json:"package"`
	Kind      string `json:"kind"`             // cli or bridge
	Pinned    string `json:"pinned"`           // what the manifest recommends (CLI) or runs (bridge)
	Installed string `json:"installed"`        // what is on PATH here ("" when not installed)
	Latest    string `json:"latest"`           // newest on npm ("" when the lookup failed)
	Change    string `json:"change"`           // what changed, in words
	Newer     bool   `json:"newer"`            // the registry has a version newer than both the pin and the installed one
	Error     string `json:"error,omitempty"`  // why the lookup failed
	Tested    bool   `json:"tested,omitempty"` // the suite ran against the latest version in this run (--latest)
}

// RowTest is one test behind a contract row.
type RowTest struct {
	Package  string   `json:"package"`
	Test     string   `json:"test"`
	Status   string   `json:"status"`
	Elapsed  float64  `json:"elapsedSeconds"`
	Reason   string   `json:"reason,omitempty"`
	SpendUSD *float64 `json:"spendUsd,omitempty"`
}

// Row is one contract (one id for one CLI) in the report.
type Row struct {
	Contract     string    `json:"contract"`
	CLI          string    `json:"cli"`
	CLIVersion   string    `json:"cliVersion,omitempty"`
	Status       string    `json:"status"` // pass, fail or skip
	Duration     float64   `json:"durationSeconds"`
	SpendUSD     *float64  `json:"spendUsd,omitempty"`     // what its paid checks reported
	EstimatedUSD float64   `json:"estimatedUsd,omitempty"` // paid checks that ran without reporting a spend, at their estimate
	Reason       string    `json:"reason,omitempty"`       // why it skipped, or which of its tests skipped
	Failure      string    `json:"failure,omitempty"`
	Notes        []string  `json:"notes,omitempty"`
	Warnings     []string  `json:"warnings,omitempty"`
	Assumption   string    `json:"assumption"`
	Tests        []RowTest `json:"tests"`
}

// Report is contract-report.json.
type Report struct {
	Generated    string         `json:"generated"`
	Commit       string         `json:"commit,omitempty"`
	Platform     string         `json:"platform"`
	Command      string         `json:"command,omitempty"`
	BudgetUSD    float64        `json:"budgetUsd"`
	SpentUSD     float64        `json:"spentUsd"`     // measured: what the paid checks reported
	EstimatedUSD float64        `json:"estimatedUsd"` // paid tests that ran without reporting their spend, at their estimate
	Summary      map[string]int `json:"summary"`
	CLIs         []CLIInfo      `json:"clis"`
	Updates      []Update       `json:"updates,omitempty"`
	Rows         []Row          `json:"contracts"`
}

// BuildRow combines the outcomes of a contract's tests: it fails when any test failed or did not run, passes when at least one
// passed, and is skipped when all were skipped.
func BuildRow(c Contract, version string, outcomes []Outcome) Row {
	row := Row{Contract: c.ID, CLI: c.CLI, CLIVersion: version, Assumption: c.Assumption}
	var failures, skipped []string
	var passed bool
	for i, o := range outcomes {
		ref := c.Tests[i]
		rt := RowTest{Package: ref.Package, Test: ref.Name(), Status: o.Status, Elapsed: o.Elapsed, Reason: o.Reason, SpendUSD: o.SpendUSD}
		row.Tests = append(row.Tests, rt)
		row.Duration += o.Elapsed
		if o.SpendUSD != nil {
			s := *o.SpendUSD
			if row.SpendUSD != nil {
				s += *row.SpendUSD
			}
			row.SpendUSD = &s
		} else if o.Ran && ref.Paid() {
			row.EstimatedUSD += ref.EstUSD
		}
		row.Notes = appendNew(row.Notes, o.Notes...)
		row.Warnings = appendNew(row.Warnings, o.Warnings...)
		switch o.Status {
		case StatusPass:
			passed = true
		case StatusSkip:
			skipped = append(skipped, ref.Name()+": "+o.Reason)
		default: // fail, missing
			msg := o.Failure
			if msg == "" {
				msg = o.Reason
			}
			failures = append(failures, ref.Name()+":\n"+msg)
		}
	}
	switch {
	case len(failures) > 0:
		row.Status = StatusFail
		row.Failure = strings.Join(failures, "\n\n")
	case passed:
		row.Status = StatusPass
		if len(skipped) > 0 {
			row.Reason = "also skipped: " + strings.Join(skipped, "; ")
		}
	default:
		row.Status = StatusSkip
		row.Reason = strings.Join(skipped, "; ")
	}
	return row
}

func appendNew(dst []string, src ...string) []string {
	for _, s := range src {
		dup := false
		for _, d := range dst {
			dup = dup || d == s
		}
		if !dup {
			dst = append(dst, s)
		}
	}
	return dst
}

// Finish fills the summary.
func (r *Report) Finish() {
	r.Summary = map[string]int{StatusPass: 0, StatusFail: 0, StatusSkip: 0, "warnings": 0}
	for _, row := range r.Rows {
		r.Summary[row.Status]++
		if len(row.Warnings) > 0 {
			r.Summary["warnings"]++
		}
	}
}

// Failed reports whether any contract failed.
func (r *Report) Failed() bool { return r.Summary[StatusFail] > 0 }

// Write writes contract-report.json and contract-report.md into dir.
func (r *Report) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "contract-report.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "contract-report.md"), []byte(r.Markdown()), 0o644)
}

// Markdown renders the report for people: a table of contracts, then failures, warnings and notes, the version watch and the
// CLIs found.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Drydock contract report\n\n")
	fmt.Fprintf(&b, "Generated %s on %s", r.Generated, r.Platform)
	if r.Commit != "" {
		fmt.Fprintf(&b, " at commit %s", r.Commit)
	}
	fmt.Fprintf(&b, ". %d passed, %d failed, %d skipped", r.Summary[StatusPass], r.Summary[StatusFail], r.Summary[StatusSkip])
	if n := r.Summary["warnings"]; n > 0 {
		fmt.Fprintf(&b, ", %d with a warning", n)
	}
	fmt.Fprintf(&b, ".\n\nSpend: $%.4f reported by the paid checks", r.SpentUSD)
	if r.EstimatedUSD > 0 {
		fmt.Fprintf(&b, ", plus about $%.2f estimated for tests that do not report their cost", r.EstimatedUSD)
	}
	fmt.Fprintf(&b, " (budget $%.2f).\n", r.BudgetUSD)
	if r.Command != "" {
		fmt.Fprintf(&b, "\nCommand: `%s`\n", r.Command)
	}

	fmt.Fprintf(&b, "\n| Contract | CLI | Version | Result | Time | Spend | Detail |\n|---|---|---|---|---|---|---|\n")
	for _, row := range r.Rows {
		spend := ""
		if row.SpendUSD != nil {
			spend = fmt.Sprintf("$%.4f", *row.SpendUSD)
		}
		if row.EstimatedUSD > 0 {
			spend = strings.TrimSpace(spend + fmt.Sprintf(" ~$%.2f est.", row.EstimatedUSD))
		}
		detail := row.Reason
		if row.Status == StatusFail {
			detail = firstLine(lastNonEmpty(strings.Split(row.Failure, "\n")))
		}
		if len(row.Warnings) > 0 {
			detail = strings.TrimSpace("warning: " + row.Warnings[0] + " " + detail)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %.1fs | %s | %s |\n", row.Contract, row.CLI, cell(row.CLIVersion), row.Status, row.Duration, spend, cell(clip(detail, 160)))
	}

	var fails, notes []Row
	for _, row := range r.Rows {
		if row.Status == StatusFail {
			fails = append(fails, row)
		}
		if len(row.Notes) > 0 || len(row.Warnings) > 0 {
			notes = append(notes, row)
		}
	}
	if len(fails) > 0 {
		fmt.Fprintf(&b, "\n## Failures\n")
		for _, row := range fails {
			fmt.Fprintf(&b, "\n### %s (%s %s)\n\n%s\n\n```text\n%s\n```\n", row.Contract, row.CLI, row.CLIVersion, row.Assumption, strings.ReplaceAll(row.Failure, "```", "'''"))
		}
	}
	if len(notes) > 0 {
		fmt.Fprintf(&b, "\n## Warnings and observations\n\n")
		for _, row := range notes {
			for _, w := range row.Warnings {
				fmt.Fprintf(&b, "- **%s (%s), warning:** %s\n", row.Contract, row.CLI, w)
			}
			for _, n := range row.Notes {
				fmt.Fprintf(&b, "- %s (%s): %s\n", row.Contract, row.CLI, n)
			}
		}
	}
	if len(r.Updates) > 0 {
		fmt.Fprintf(&b, "\n## Version watch\n\n| CLI | Package | Pinned | Installed | Latest | What changed |\n|---|---|---|---|---|---|\n")
		for _, u := range r.Updates {
			change := u.Change
			if u.Tested {
				change = strings.Replace(change, "run the suite with --latest before adopting it", "this run tested it", 1)
			}
			if u.Error != "" {
				change = "lookup failed: " + u.Error
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", u.CLI, u.Package, cell(u.Pinned), cell(u.Installed), cell(u.Latest), cell(clip(change, 200)))
		}
	}
	if len(r.CLIs) > 0 {
		fmt.Fprintf(&b, "\n## CLIs\n\n| CLI | Version | Where | Status |\n|---|---|---|---|\n")
		for _, c := range r.CLIs {
			where := c.Path
			if c.Source != "" && c.Path != "" {
				where = c.Path + " (" + c.Source + ")"
			}
			status := c.Status
			if c.Detail != "" {
				status = strings.TrimSpace(status + ": " + c.Detail)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Name, cell(c.Version), cell(where), cell(clip(status, 160)))
		}
	}
	return b.String()
}

func cell(s string) string {
	if s == "" {
		return "-"
	}
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasSuffix(t, ":") {
			return t
		}
	}
	return ""
}

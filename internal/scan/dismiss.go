package scan

import "fmt"

// Dismissed findings (decision P97): a person says a finding is not a real problem ("Not a real key"), an accepted risk, or
// a false positive of its rule. The core keeps the dismissals per project or folder; a report marks the findings it has
// with them and leaves them out of its counts, its engines' counts and its summary.

// Dismissal reasons.
const (
	ReasonNotReal       = "not_real"
	ReasonAcceptedRisk  = "accepted_risk"
	ReasonFalsePositive = "false_positive"
)

// ValidReason reports whether r is a dismissal reason.
func ValidReason(r string) bool {
	return r == ReasonNotReal || r == ReasonAcceptedRisk || r == ReasonFalsePositive
}

// Dismissal is one dismissed finding of a project or folder, with what named the finding when it was dismissed.
type Dismissal struct {
	Fingerprint string   `json:"fingerprint"`
	Reason      string   `json:"reason"`
	By          string   `json:"by"`
	At          int64    `json:"at"` // unix milliseconds
	Engine      EngineID `json:"engine"`
	Rule        string   `json:"rule"`
	Title       string   `json:"title"`
	File        string   `json:"file,omitempty"`
	Line        int      `json:"line,omitempty"`
	// ScanID is the scan whose report the person dismissed it in.
	ScanID string `json:"scanId,omitempty"`
}

// ApplyDismissals marks the report's findings with the dismissals (by fingerprint) and clears the marks of findings no
// longer dismissed, keeping the counts, the engines' counts and the summary of what is not dismissed. Counts of findings past
// MaxFindings (not kept) are not touched.
func ApplyDismissals(rep *Report, d map[string]Dismissal) {
	changed := false
	for i := range rep.Findings {
		f := &rep.Findings[i]
		dm, now := d[f.Fingerprint]
		if f.Fingerprint == "" {
			now = false
		}
		if now == f.Dismissed {
			if now {
				f.DismissedBy, f.DismissedAt, f.DismissReason = dm.By, dm.At, dm.Reason
			}
			continue
		}
		changed = true
		delta := 1 // a finding no longer dismissed counts again
		if now {
			delta = -1
			f.Dismissed, f.DismissedBy, f.DismissedAt, f.DismissReason = true, dm.By, dm.At, dm.Reason
		} else {
			f.Dismissed, f.DismissedBy, f.DismissedAt, f.DismissReason = false, "", 0, ""
		}
		addCount(&rep.Counts, f.Severity, delta)
		rep.Dismissed -= delta
		for j := range rep.Engines {
			if rep.Engines[j].ID == f.Engine {
				rep.Engines[j].Findings += delta
				rep.Engines[j].Dismissed -= delta
			}
		}
	}
	if changed && rep.Status != StatusRunning && rep.FinishedAt > 0 {
		rep.Summary = summarize(*rep)
	}
}

func addCount(c *Counts, s Severity, delta int) {
	c.Total += delta
	switch s {
	case Critical:
		c.Critical += delta
	case High:
		c.High += delta
	case Medium:
		c.Medium += delta
	case Low:
		c.Low += delta
	default:
		c.Info += delta
	}
}

// dismissedNote is the summary's part about dismissed findings ("" for none).
func dismissedNote(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 finding you dismissed is not counted"
	}
	return fmt.Sprintf("%d findings you dismissed are not counted", n)
}

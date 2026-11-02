package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// InterruptedCrash is the crash report of the moment an agent turn was cut off (a request of gate workflow.InterruptedGate):
// the newest report kept on this computer from between the run's start and the time the request was raised. A crash after the
// request was raised (while it waited) did not cut the turn off and is not shown. Its summary is the redacted line that says why.
type InterruptedCrash struct {
	Name    string `json:"name"`
	Time    int64  `json:"time"` // Unix milliseconds
	Summary string `json:"summary"`
}

// withInterruptedCrash adds, to each interrupted-turn request of a run that started at runCreated (Unix ms), the crash report
// of that time as "crashReport" in its payload, so the task page shows what cut the turn off beside the question. Nothing is
// added when crash reports are off, the request's raise time is unknown, or no report was kept between the run's start and it.
func (c *Core) withInterruptedCrash(ctx context.Context, pending []workflow.PendingRequest, runCreated int64) []workflow.PendingRequest {
	var reports []crashreport.Report
	loaded := false
	for i, p := range pending {
		if !isInterrupted(p) {
			continue
		}
		if !loaded {
			loaded = true
			reports = c.keptCrashReports(ctx)
		}
		if len(reports) == 0 {
			return pending
		}
		var raised int64
		if err := c.DB.Reader().QueryRowContext(ctx, `SELECT created_at FROM approvals WHERE id=?`, string(p.ID)).Scan(&raised); err != nil || raised == 0 {
			continue
		}
		pending[i] = withCrash(p, newestCrashBetween(reports, runCreated, raised))
	}
	return pending
}

func isInterrupted(p workflow.PendingRequest) bool {
	var pl struct {
		Gate string `json:"gate"`
	}
	return json.Unmarshal(p.Spec.Payload, &pl) == nil && pl.Gate == workflow.InterruptedGate
}

// withCrash puts crash in the payload of an interrupted-turn request; a nil crash, or another request, changes nothing.
func withCrash(p workflow.PendingRequest, crash *InterruptedCrash) workflow.PendingRequest {
	var pl map[string]any
	if crash == nil || !isInterrupted(p) || json.Unmarshal(p.Spec.Payload, &pl) != nil {
		return p
	}
	pl["crashReport"] = crash
	if b, err := json.Marshal(pl); err == nil {
		p.Spec.Payload = b
	}
	return p
}

// keptCrashReports lists the crash reports kept on this computer (newest first), or none when they are off.
func (c *Core) keptCrashReports(ctx context.Context) []crashreport.Report {
	if c.cfg.DataDir == "" || !c.crashReportsKept(ctx) {
		return nil
	}
	rs, err := crashreport.List(c.cfg.DataDir)
	if err != nil {
		return nil
	}
	return rs
}

// newestCrashBetween picks from reports (newest first, as crashreport.List gives them) the newest written at or after since and
// at or before until (Unix ms): the crash that cut off the turn whose request was raised at until, not one from while it waited.
func newestCrashBetween(rs []crashreport.Report, since, until int64) *InterruptedCrash {
	from := time.UnixMilli(since).Truncate(time.Second) // a report's time is kept to the second
	to := time.UnixMilli(until)
	for _, r := range rs {
		if r.Time.IsZero() || r.Time.After(to) {
			continue
		}
		if r.Time.Before(from) {
			return nil
		}
		return &InterruptedCrash{Name: r.Name, Time: r.Time.UnixMilli(), Summary: r.Summary}
	}
	return nil
}

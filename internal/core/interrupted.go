package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// InterruptedCrash is the crash report of the moment an agent turn was cut off (a request of gate workflow.InterruptedGate):
// the newest report kept on this computer from since the run started. Its summary is the redacted line that says why.
type InterruptedCrash struct {
	Name    string `json:"name"`
	Time    int64  `json:"time"` // Unix milliseconds
	Summary string `json:"summary"`
}

// withInterruptedCrash adds, to each interrupted-turn request of a run that started at runCreated (Unix ms), the crash report
// of that time as "crashReport" in its payload, so the task page shows what cut the turn off beside the question. Nothing is
// added when crash reports are off or none was kept since the run started.
func (c *Core) withInterruptedCrash(ctx context.Context, pending []workflow.PendingRequest, runCreated int64) []workflow.PendingRequest {
	for _, p := range pending {
		if isInterrupted(p) {
			return withCrash(pending, c.crashSince(ctx, runCreated))
		}
	}
	return pending
}

func isInterrupted(p workflow.PendingRequest) bool {
	var pl struct {
		Gate string `json:"gate"`
	}
	return json.Unmarshal(p.Spec.Payload, &pl) == nil && pl.Gate == workflow.InterruptedGate
}

// withCrash puts crash in the payload of each interrupted-turn request; a nil crash changes nothing.
func withCrash(pending []workflow.PendingRequest, crash *InterruptedCrash) []workflow.PendingRequest {
	if crash == nil {
		return pending
	}
	for i, p := range pending {
		var pl map[string]any
		if !isInterrupted(p) || json.Unmarshal(p.Spec.Payload, &pl) != nil {
			continue
		}
		pl["crashReport"] = crash
		if b, err := json.Marshal(pl); err == nil {
			pending[i].Spec.Payload = b
		}
	}
	return pending
}

// crashSince is the newest kept crash report written at or after since (Unix ms), or nil.
func (c *Core) crashSince(ctx context.Context, since int64) *InterruptedCrash {
	if c.cfg.DataDir == "" || !c.crashReportsKept(ctx) {
		return nil
	}
	rs, err := crashreport.List(c.cfg.DataDir)
	if err != nil {
		return nil
	}
	return newestCrashSince(rs, since)
}

// newestCrashSince picks from reports (newest first, as crashreport.List gives them) the first written at or after since.
func newestCrashSince(rs []crashreport.Report, since int64) *InterruptedCrash {
	from := time.UnixMilli(since).Truncate(time.Second) // a report's time is kept to the second
	for _, r := range rs {
		if !r.Time.IsZero() && !r.Time.Before(from) {
			return &InterruptedCrash{Name: r.Name, Time: r.Time.UnixMilli(), Summary: r.Summary}
		}
	}
	return nil
}

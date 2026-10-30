package core

import (
	"context"
	"fmt"

	"github.com/kjmaguire/drydock/internal/crashreport"
)

// Crash reports (docs/packaging.md section 9; review 2026-10-27). The desktop app points Go's crash output at a file of its own
// (Config.CrashOutput); when a run of Drydock dies anyway (a panic nothing could recover, a runtime fatal error), the next start
// finds that file, turns it into a report with its secrets redacted, says so in the log and with an app.crashed event, and the
// Support page lists the reports. A support bundle includes them, scrubbed again. They never leave the machine on their own.
// Since decision P121 crash reports are a diagnostics category a person can turn off (ext_diagnostics.go): then this process's
// crash output is paused, what an earlier crash left is removed unread, and no report is made.

func init() {
	RegisterExtension(Extension{Name: "crashes", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		redact := func(s string) string { return s }
		if c.Scanner != nil {
			redact = c.Scanner.Redact
		}
		var made []crashreport.Report
		if c.crashReportsKept(ctx) {
			var err error
			if made, err = crashreport.Collect(c.cfg.DataDir, c.cfg.CrashOutput, redact, AppVersion); err != nil {
				c.logf("crash reports: %v", err)
			}
		} else {
			c.applyCrashReports(CrashReportsOff)
			if n, err := crashreport.Discard(c.cfg.DataDir, c.cfg.CrashOutput); err != nil {
				c.logf("crash reports: %v", err)
			} else if n > 0 {
				c.logf("Drydock stopped unexpectedly %d time(s) before this start; crash reports are off, so nothing was kept", n)
			}
		}
		for _, r := range made {
			c.logf("Drydock stopped unexpectedly (started %s): %s. The crash report %s is on the Support page.", r.Time.Format("2006-01-02 15:04 UTC"), r.Summary, r.Name)
		}
		if len(made) > 0 {
			c.publish(ctx, "app.crashed", "", map[string]any{"count": len(made), "summary": made[len(made)-1].Summary, "name": made[len(made)-1].Name})
		}
		return map[string]Method{
			"crash.list": bind(func(ctx context.Context, _ struct{}) (any, error) {
				rs, err := crashreport.List(c.cfg.DataDir)
				if rs == nil {
					rs = []crashreport.Report{}
				}
				return map[string]any{"reports": rs}, err
			}),
			"crash.read": bind(func(ctx context.Context, p struct {
				Name string `json:"name"`
			}) (any, error) {
				text, err := crashreport.Read(c.cfg.DataDir, p.Name)
				if err != nil {
					return nil, err
				}
				return map[string]string{"name": p.Name, "text": text}, nil
			}),
			"crash.delete": bind(func(ctx context.Context, p struct {
				Name string `json:"name"`
			}) (any, error) {
				if err := crashreport.Delete(c.cfg.DataDir, p.Name); err != nil {
					return nil, fmt.Errorf("delete the crash report: %w", err)
				}
				return map[string]bool{"deleted": true}, nil
			}),
		}, nil, nil
	}})
}

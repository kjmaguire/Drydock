package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kjmaguire/drydock/internal/crashreport"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

// The diagnostics categories (docs/packaging.md section 9, decision P121), each a person's switch of its own, stored with who set
// it and when, shown on the Settings page and honoured where it is read:
//
//   - Crash reports: "local" (the default, as since 2026-10-27: a crash leaves a report on this computer that only a support
//     bundle the person sends carries anywhere) or "off". Off, this process's crash output is paused at once, the next start
//     removes what a crash left without making a report, and a support bundle leaves the kept reports out.
//   - Usage counters: off by default. On, the name of every method the window calls is counted (internal/telemetry, CountUse)
//     and written to the local telemetry sink every five minutes; OTLP export sends them like anything else it records.
//   - Content export (telemetry.content.set): the LLM calls' prompts and completions in the telemetry, redacted; off by default
//     and turned on only with an acknowledgement of what it sends.
//
// OpenTelemetry export, the third category of section 9, is the exporter's own switch (telemetry.otlp.set).

const (
	settingCrashReports   = "diagnostics.crash_reports" // local | off
	settingCrashReportsBy = "diagnostics.crash_reports.changed_by"
	settingCrashReportsAt = "diagnostics.crash_reports.changed_at"
)

// Crash report modes.
const (
	CrashReportsLocal = "local"
	CrashReportsOff   = "off"
)

// usageFlushEvery is how often counted uses are written to the local sink.
const usageFlushEvery = 5 * time.Minute

func readSetting(ctx context.Context, c *Core, key string) string {
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// crashReportsKept reports whether crash reports are kept (anything but an explicit "off": the default is local).
func (c *Core) crashReportsKept(ctx context.Context) bool {
	return readSetting(ctx, c, settingCrashReports) != CrashReportsOff
}

// CrashConsent is the crash reports' category.
type CrashConsent struct {
	Mode      string `json:"mode"` // local | off
	ChangedBy string `json:"changedBy,omitempty"`
	ChangedAt int64  `json:"changedAt,omitempty"`
	// Kept is how many reports are on this computer (made before the mode changed, they stay until deleted).
	Kept int `json:"kept"`
}

// DiagnosticsStatus is the diagnostics categories for the Settings page.
type DiagnosticsStatus struct {
	CrashReports CrashConsent `json:"crashReports"`
	// UsageCounters is nil when the telemetry sink did not start (the log says why).
	UsageCounters *telemetry.UsageStatus `json:"usageCounters,omitempty"`
	// Content is content export's switch (also in telemetry.status).
	Content *telemetry.Consent `json:"content,omitempty"`
	// Telemetry and OTLP say whether the local sink records and whether anything is being sent.
	Telemetry bool `json:"telemetry"`
	OTLP      bool `json:"otlp"`
}

func diagnosticsSink(c *Core) *telemetry.Sink {
	econMu.Lock()
	defer econMu.Unlock()
	if e := econByCore[c]; e != nil {
		return e.sink
	}
	return nil
}

func (c *Core) diagnosticsStatus(ctx context.Context) (DiagnosticsStatus, error) {
	st := DiagnosticsStatus{CrashReports: CrashConsent{Mode: CrashReportsLocal, ChangedBy: readSetting(ctx, c, settingCrashReportsBy)}}
	if !c.crashReportsKept(ctx) {
		st.CrashReports.Mode = CrashReportsOff
	}
	st.CrashReports.ChangedAt, _ = strconv.ParseInt(readSetting(ctx, c, settingCrashReportsAt), 10, 64)
	if rs, err := crashreport.List(c.cfg.DataDir); err == nil {
		st.CrashReports.Kept = len(rs)
	}
	if s := diagnosticsSink(c); s != nil {
		ts, err := s.Status(ctx)
		if err != nil {
			return st, err
		}
		st.UsageCounters, st.Content, st.Telemetry = &ts.UsageCounters, &ts.Content, ts.Enabled
		st.OTLP = ts.OTLP != nil && ts.OTLP.Active
	}
	return st, nil
}

// setCrashReports stores a person's choice and applies it to this process at once.
func (c *Core) setCrashReports(ctx context.Context, mode string) error {
	if mode != CrashReportsLocal && mode != CrashReportsOff {
		return fmt.Errorf("crash reports are %s or %s, not %q", CrashReportsLocal, CrashReportsOff, mode)
	}
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for k, v := range map[string]string{settingCrashReports: mode, settingCrashReportsBy: c.Human(), settingCrashReportsAt: strconv.FormatInt(time.Now().UnixMilli(), 10)} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.applyCrashReports(mode)
	c.publish(ctx, "diagnostics.changed", "", map[string]any{"category": "crash_reports", "mode": mode, "by": c.Human()})
	return nil
}

// applyCrashReports points this process's crash output at its file, or away from it. A core without a crash output file (tests,
// the dev transport) has nothing to point.
func (c *Core) applyCrashReports(mode string) {
	if c.cfg.CrashOutput == "" {
		return
	}
	var err error
	if mode == CrashReportsOff {
		err = crashreport.Pause()
	} else {
		err = crashreport.Resume(c.cfg.CrashOutput)
	}
	if err != nil {
		c.logf("crash reports: %v", err)
	}
}

func init() {
	RegisterExtension(Extension{Name: "diagnostics", After: []string{"economics", "crashes"}, Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		sink := diagnosticsSink(c)
		var stop chan struct{}
		var done chan struct{}
		if sink != nil {
			count := sink.CountUse
			c.onCall.Store(&count)
			stop, done = make(chan struct{}), make(chan struct{})
			safe.Go("core.diagnostics.usage", func() {
				defer close(done)
				t := time.NewTicker(usageFlushEvery)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case <-t.C:
						if _, err := sink.FlushUsage(context.Background()); err != nil {
							c.logf("usage counters: %v", err)
						}
					}
				}
			})
		}
		closeFn := func() {
			c.onCall.Store(nil)
			if stop != nil {
				close(stop)
				<-done
				if _, err := sink.FlushUsage(context.Background()); err != nil {
					c.logf("usage counters: %v", err)
				}
			}
		}
		needSink := func() (*telemetry.Sink, error) {
			if s := diagnosticsSink(c); s != nil {
				return s, nil
			}
			return nil, errors.New("telemetry is unavailable (see the log)")
		}
		return map[string]Method{
			"diagnostics.status": bind(func(ctx context.Context, _ struct{}) (any, error) {
				if s := diagnosticsSink(c); s != nil {
					_, _ = s.FlushUsage(ctx) // the counts shown are the ones written
				}
				return c.diagnosticsStatus(ctx)
			}),
			// a person's choice per category; a nil field is left as it is
			"diagnostics.set": bind(func(ctx context.Context, p struct {
				CrashReports  *string `json:"crashReports"`
				UsageCounters *bool   `json:"usageCounters"`
			}) (any, error) {
				if p.CrashReports != nil {
					if err := c.setCrashReports(ctx, *p.CrashReports); err != nil {
						return nil, err
					}
				}
				if p.UsageCounters != nil {
					s, err := needSink()
					if err != nil {
						return nil, err
					}
					if err := s.SetUsageCounters(ctx, *p.UsageCounters, c.Human()); err != nil {
						return nil, err
					}
					c.publish(ctx, "diagnostics.changed", "", map[string]any{"category": "usage_counters", "enabled": *p.UsageCounters, "by": c.Human()})
				}
				return c.diagnosticsStatus(ctx)
			}),
			// content export: the LLM calls' prompts and completions in the telemetry (redacted), with the acknowledgement that the
			// person was shown what it sends
			"telemetry.content.set": bind(func(ctx context.Context, p struct {
				Enabled      bool `json:"enabled"`
				Acknowledged bool `json:"acknowledged"`
			}) (any, error) {
				s, err := needSink()
				if err != nil {
					return nil, err
				}
				if err := s.SetContent(ctx, p.Enabled, p.Acknowledged, c.Human()); err != nil {
					return nil, err
				}
				c.publish(ctx, "telemetry.content_changed", "", map[string]any{"enabled": p.Enabled, "by": c.Human()})
				return s.Status(ctx)
			}),
		}, closeFn, nil
	}})
}

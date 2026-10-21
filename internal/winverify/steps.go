package winverify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

// Step is one verification step: what it proves in one plain sentence, and how to run it.
type Step struct {
	ID     string
	Title  string
	Proves string
	Run    func(ctx context.Context, sc *StepContext) Outcome
}

// StepContext is what a running step gets: its own log file and a scratch folder that is removed afterwards.
type StepContext struct {
	Log  io.Writer
	Work string
	// Env is shared by all steps of one run (options, the bundle, the core session the smoke steps share).
	Env *Env
}

// Logf writes one line to the step's log.
func (sc *StepContext) Logf(format string, a ...any) {
	fmt.Fprintf(sc.Log, format+"\n", a...)
}

// Outcome is what a step reports. A skip and a fail always carry a reason.
type Outcome struct {
	Status  Status
	Reason  string
	Excerpt string
	Tests   []TestResult
	// Facts is set by the facts step only.
	Facts *Facts
}

// StepResult is a step as it appears in the report.
type StepResult struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Proves     string       `json:"proves"`
	Status     Status       `json:"status"`
	Reason     string       `json:"reason,omitempty"`
	Started    time.Time    `json:"started"`
	DurationMs int64        `json:"durationMs"`
	Excerpt    string       `json:"excerpt,omitempty"`
	Log        string       `json:"log"`
	Counts     *TestCounts  `json:"counts,omitempty"`
	Tests      []TestResult `json:"tests,omitempty"`
	Facts      *Facts       `json:"facts,omitempty"`
}

// Duration is the step's run time.
func (r StepResult) Duration() time.Duration { return time.Duration(r.DurationMs) * time.Millisecond }

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// logName is the file name of a step's log (step ids contain "/" and ":").
func logName(id string) string {
	return strings.Trim(unsafeName.ReplaceAllString(id, "_"), "_") + ".log"
}

// RunSteps runs the steps in order. Each step writes its log to logDir; progress lines go to progress (may be nil). A step
// that panics fails with the panic as its reason; a skip or a fail without a reason is turned into a fail that says so,
// because a result nobody can explain is worth nothing. Cancelling ctx marks the remaining steps as skipped.
func RunSteps(ctx context.Context, steps []Step, env *Env, logDir string, progress io.Writer) []StepResult {
	red := newRedactor()
	if progress == nil {
		progress = io.Discard
	}
	results := make([]StepResult, 0, len(steps))
	width := 0
	for _, s := range steps {
		width = max(width, len(s.ID))
	}
	for i, s := range steps {
		res := StepResult{ID: s.ID, Title: s.Title, Proves: s.Proves, Started: time.Now(), Log: filepath.ToSlash(filepath.Join("logs", logName(s.ID)))}
		fmt.Fprintf(progress, "[%2d/%d] %-*s ", i+1, len(steps), width, s.ID)
		var out Outcome
		if ctx.Err() != nil {
			out = Outcome{Status: Skip, Reason: "not run: the run was stopped before this step"}
		} else {
			out = runOne(ctx, s, env, filepath.Join(logDir, logName(s.ID)))
		}
		res.DurationMs = time.Since(res.Started).Milliseconds()
		switch out.Status {
		case Pass:
		case Skip, Fail:
			if strings.TrimSpace(out.Reason) == "" {
				out.Reason = "the step reported " + string(out.Status) + " without a reason (a bug in drydock-verify)"
				out.Status = Fail
			}
		default:
			out.Reason = fmt.Sprintf("the step reported an unknown result %q (a bug in drydock-verify)", out.Status)
			out.Status = Fail
		}
		res.Status, res.Reason = out.Status, red.text(out.Reason)
		res.Excerpt = red.text(out.Excerpt)
		res.Facts = out.Facts
		if len(out.Tests) > 0 {
			c := Count(out.Tests)
			res.Counts = &c
			res.Tests = make([]TestResult, len(out.Tests))
			for j, t := range out.Tests {
				t.Reason = red.text(t.Reason)
				for k := range t.Output {
					t.Output[k] = red.text(t.Output[k])
				}
				res.Tests[j] = t
			}
		}
		line := fmt.Sprintf("%-4s %8s", res.Status, fmtDur(res.Duration()))
		if res.Counts != nil {
			line += "  (" + res.Counts.String() + ")"
		}
		if res.Status != Pass {
			line += "\n         " + firstLine(res.Reason)
		}
		fmt.Fprintln(progress, line)
		results = append(results, res)
	}
	return results
}

// runOne runs a step with its own log and scratch folder and turns a panic into a failure.
func runOne(ctx context.Context, s Step, env *Env, logPath string) (out Outcome) {
	f, err := os.Create(logPath)
	if err != nil {
		return Outcome{Status: Fail, Reason: "cannot create the step's log: " + err.Error()}
	}
	defer f.Close()
	lw := &redactingWriter{w: f, red: newRedactor()}
	defer lw.Flush()
	fmt.Fprintf(lw, "step %s\nproves: %s\nstarted: %s\n\n", s.ID, s.Proves, time.Now().Format(time.RFC3339))
	work, err := os.MkdirTemp("", "drydock-verify-step-")
	if err != nil {
		return Outcome{Status: Fail, Reason: "cannot create a scratch folder: " + err.Error()}
	}
	keep := env != nil && env.Keep
	defer func() {
		if !keep {
			removeAllRetry(work)
		}
	}()
	defer func() {
		if p := recover(); p != nil {
			fmt.Fprintf(lw, "\nPANIC: %v\n%s\n", p, debug.Stack())
			out = Outcome{Status: Fail, Reason: fmt.Sprintf("drydock-verify crashed in this step: %v", p)}
		}
		fmt.Fprintf(lw, "\nresult: %s %s\n", out.Status, out.Reason)
	}()
	sc := &StepContext{Log: lw, Work: work, Env: env}
	return s.Run(ctx, sc)
}

// redactingWriter redacts whole lines before they reach the log file.
type redactingWriter struct {
	w   io.Writer
	red redactor
	buf []byte
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		if _, err := io.WriteString(r.w, r.red.text(string(r.buf[:i+1]))); err != nil {
			return 0, err
		}
		r.buf = r.buf[i+1:]
	}
	if len(r.buf) > 64<<10 { // a very long line without a newline: write what there is
		r.Flush()
	}
	return len(p), nil
}

// Flush writes a partial last line.
func (r *redactingWriter) Flush() {
	if len(r.buf) > 0 {
		_, _ = io.WriteString(r.w, r.red.text(string(r.buf)))
		r.buf = nil
	}
}

// removeAllRetry removes a folder, retrying briefly: on Windows a file can stay locked for a moment after the process that
// held it exited (and antivirus scanners open new files).
func removeAllRetry(dir string) {
	for i := 0; i < 10; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// fmtDur shows short steps in milliseconds and longer ones to a tenth of a second.
func fmtDur(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// Summary counts step results.
type Summary struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Summarize counts the steps by status.
func Summarize(rs []StepResult) Summary {
	var s Summary
	for _, r := range rs {
		switch r.Status {
		case Pass:
			s.Passed++
		case Fail:
			s.Failed++
		default:
			s.Skipped++
		}
	}
	return s
}

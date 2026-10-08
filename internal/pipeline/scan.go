package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// ScanFactory builds the check for one named scan on a task's worktree.
type ScanFactory func(ctx context.Context, td TemplateDeps, t tasks.Task) (checks.Spec, error)

// ScanRegistry resolves scan check names, so the Security template does not hard-code which checks exist: a name that is
// registered here wins, a name that matches a detected project check (build, test, lint, typecheck) is run as that check,
// and any other name is reported as "not available" (skipped, never a pass).
type ScanRegistry struct {
	mu sync.RWMutex
	m  map[string]ScanFactory
}

// NewScanRegistry returns an empty registry.
func NewScanRegistry() *ScanRegistry { return &ScanRegistry{m: map[string]ScanFactory{}} }

// Register adds or replaces a scan by name.
func (r *ScanRegistry) Register(name string, f ScanFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = f
}

// Names lists the registered scans.
func (r *ScanRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *ScanRegistry) get(name string) (ScanFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.m[name]
	return f, ok
}

// DefaultScanNames are the scans the Security template runs unless the run input names others.
var DefaultScanNames = []string{"secrets", "deps"}

// DefaultScans is the registry the templates use unless TemplateDeps.Scans is set.
var DefaultScans = func() *ScanRegistry {
	r := NewScanRegistry()
	r.Register("secrets", secretsScan)
	r.Register("deps", depsScan)
	return r
}()

// gitEmptyTree is the well-known id of the empty tree: diffing the worktree against it lists every file as added, so the
// added-lines secrets scanner covers the whole tree instead of one task's change.
const gitEmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func secretsScan(ctx context.Context, td TemplateDeps, t tasks.Task) (checks.Spec, error) {
	repo, err := td.Repo(ctx, t.ProjectID)
	if err != nil {
		return checks.Spec{}, err
	}
	sc := td.Scanner
	if sc == nil {
		if sc, err = checks.DefaultScanner(); err != nil {
			return checks.Spec{}, fmt.Errorf("secrets scanner unavailable: %w", err)
		}
	}
	return checks.SecretsSpec(t.Worktree, sc, func(ctx context.Context) (string, error) {
		return repo.Diff(ctx, worktreeOf(t), gitEmptyTree)
	}), nil
}

func depsScan(ctx context.Context, td TemplateDeps, t tasks.Task) (checks.Spec, error) {
	set := checks.LoadSettings(ctx, td.Tasks.DB, t.ProjectID)
	return checks.DepsSpec(t.Worktree, checks.DepsOptions{Binary: td.DepsBinary, Offline: set.DepsOffline}), nil
}

// resolve returns the spec for a scan name, or a skipped result when the name is unknown in this build.
func (r *ScanRegistry) resolve(ctx context.Context, td TemplateDeps, t tasks.Task, name string) (checks.Spec, *checks.Result, error) {
	if f, ok := r.get(name); ok {
		spec, err := f(ctx, td, t)
		if err != nil {
			res := checks.Result{}
			res.Name, res.Errored, res.ExitCode, res.Output = name, true, 2, name+" could not start: "+err.Error()
			return checks.Spec{}, &res, nil
		}
		return spec, nil, nil
	}
	detect := td.Checks
	if detect == nil {
		detect = checks.Detect
	}
	for _, s := range detect(t.Worktree) {
		if s.Name == name {
			return s, nil, nil
		}
	}
	res := checks.SkippedResult(name, "the scan check "+name+" is not available in this build (register it with ScanRegistry.Register)")
	return checks.Spec{}, &res, nil
}

// scanNode runs the named scans over the task's worktree. With informational true (the first scan of the Security template)
// it only gathers findings for the worker and always continues; otherwise (the gate) any failing scan sends the work back.
// Skipped and errored scans are recorded as evidence without a verdict and never count as a pass.
func (td TemplateDeps) scanNode(id string, informational bool) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		inp := td.templateInput(in)
		names := inp.Scans
		if len(names) == 0 {
			names = DefaultScanNames
		}
		logDir := filepath.Join(td.DataDir, "runs", string(in.RunID), "checks", id)
		var failing []checks.Result
		var summary strings.Builder
		var last checks.Result
		for _, name := range names {
			spec, pre, err := td.scans().resolve(ctx, td, t, name)
			if err != nil {
				return workflow.NodeOutput{}, err
			}
			var res checks.Result
			if pre != nil {
				res = *pre
			} else {
				spec.LogDir = logDir
				if res, err = checks.Run(ctx, td.PM, spec, func(line string) {
					in.Emit(workflow.NewChunk(workflow.ChunkLog, workflow.TextDelta{Text: line}))
				}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
			data, _ := json.Marshal(map[string]any{"argv": res.Argv, "exitCode": res.ExitCode, "failing": res.FailingTests, "skipped": res.Skipped, "errored": res.Errored, "scan": id})
			if res.Inconclusive() {
				status := "skipped"
				if res.Errored {
					status = "error"
				}
				reason := nonEmptyStr(res.SkipReason, res.Output)
				if _, err := td.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: name, Summary: truncateStr(fmt.Sprintf("%s %s: %s", name, status, reason), 300), Ref: res.LogRef, Data: data}); err != nil {
					return workflow.NodeOutput{}, err
				}
				td.emit(ctx, "task.check_inconclusive", t, map[string]any{"kind": name, "status": status, "reason": truncateStr(reason, 300), "scan": id})
				if td.OnInconclusive != nil {
					td.OnInconclusive(ctx, t, res)
				}
				fmt.Fprintf(&summary, "- %s: %s (%s)\n", name, status, truncateStr(reason, 200))
				continue
			}
			passed := res.Passed
			if _, err := td.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: name, Passed: &passed, Summary: summarize(res), Ref: res.LogRef, Data: data}); err != nil {
				return workflow.NodeOutput{}, err
			}
			if td.OnCheck != nil {
				td.OnCheck(ctx, t, res)
			}
			last = res
			if !res.Passed {
				failing = append(failing, res)
				fmt.Fprintf(&summary, "- %s: FAILED\n%s\n", name, truncateStr(res.Output, 1500))
			} else {
				fmt.Fprintf(&summary, "- %s: clean\n", name)
			}
		}
		if informational {
			b, _ := json.Marshal(map[string]any{"scanSummary": summary.String()})
			return workflow.NodeOutput{Value: b, Route: "done"}, nil
		}
		if len(failing) > 0 {
			first := failing[0]
			if len(failing) > 1 {
				var all []string
				for _, f := range failing {
					all = append(all, f.Name+":\n"+f.Output)
				}
				first.Output = truncateStr(strings.Join(all, "\n\n"), 6000)
			}
			b, _ := json.Marshal(first.CheckResult)
			return workflow.NodeOutput{Value: b, Route: "fail"}, nil
		}
		b, _ := json.Marshal(last.CheckResult)
		return workflow.NodeOutput{Value: b, Route: "pass"}, nil
	}
}

func (td TemplateDeps) templateInput(in workflow.NodeInput) TemplateInput {
	var ti TemplateInput
	_ = json.Unmarshal(in.Input, &ti)
	return ti
}

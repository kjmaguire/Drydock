package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/tasks"
)

// A push that changes how CI runs (audit 2026-10-16, H5).
//
// PushPR publishes the task branch after the LLM reviewer's verdict, with no approval (docs/github.md section 5). What the
// branch contains is code an agent wrote, and pushing it makes GitHub run the workflows in it: on GitHub's machines, and, where
// the person registered a self-hosted runner (internal/runners), on their own PC, as them, outside the per-task container and
// every approval. A workflow an agent added or changed is the one place where a push becomes code execution on the host, so a
// push whose diff touches a CI definition waits for a person, who sees the paths.

// GateCIPush is the gate name of the approval a push that changes CI definitions raises (security.KindGitPush).
const GateCIPush = "ci_push"

// EvidenceCIPushApproved is the task evidence a person's approval of such a push leaves: Data holds the fingerprint of the
// head and the paths they were shown, so the approval covers that push and not a later one.
const EvidenceCIPushApproved = "ci_push_approved"

// ciDefinitionDirs and ciDefinitionFiles name what CI services read to decide what to run. Matching is by path, case-insensitive,
// from the repository root.
var (
	ciDefinitionDirs = []string{".github/workflows/", ".github/actions/", ".circleci/", ".buildkite/", ".woodpecker/", ".azure-pipelines/", ".teamcity/", ".gitlab/ci/"}
	// a top-level file, or the same name in a folder for the composite-action ones
	ciDefinitionFiles = []string{".gitlab-ci.yml", ".gitlab-ci.yaml", "jenkinsfile", "azure-pipelines.yml", "azure-pipelines.yaml", ".drone.yml", ".travis.yml",
		"appveyor.yml", ".appveyor.yml", "bitbucket-pipelines.yml", "cloudbuild.yaml", "cloudbuild.yml", "action.yml", "action.yaml", "buildspec.yml", "codemagic.yaml"}
)

// CIDefinitionPaths returns the paths among changed (relative, slash-separated) that define what a CI service runs.
func CIDefinitionPaths(changed []string) []string {
	var out []string
	for _, p := range changed {
		q := strings.ToLower(strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, `\`, "/")), "./"))
		hit := false
		for _, d := range ciDefinitionDirs {
			if strings.HasPrefix(q, d) {
				hit = true
				break
			}
		}
		if !hit {
			base := path.Base(q)
			for _, f := range ciDefinitionFiles {
				if base == f {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// ciPushFingerprint binds an approval to one head and the paths that were shown.
func ciPushFingerprint(head string, paths []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", head)
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00", p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ciPushGate is the error PushPR returns until a person has approved the push: the engine raises an approval for it
// (workflow.GateError) and runs the node again once it is answered.
type ciPushGate struct {
	Branch      string
	Head        string
	Paths       []string
	Fingerprint string
}

func (g *ciPushGate) Error() string {
	return fmt.Sprintf("pipeline: pushing %s changes CI definitions (%s); a person must approve the push", g.Branch, strings.Join(g.Paths, ", "))
}

// Gate implements workflow.GateError.
func (g *ciPushGate) Gate() (gate, title string, payload map[string]any) {
	shown := g.Paths
	more := ""
	if len(shown) > 12 {
		shown, more = shown[:12], fmt.Sprintf(" and %d more", len(g.Paths)-12)
	}
	return GateCIPush, "The task's branch " + g.Branch + " changes how CI runs (" + strings.Join(shown, ", ") + more + "). Pushing it makes GitHub run those workflows, " +
			"and on a self-hosted runner of yours they run on your PC as you, outside the sandbox and without another approval. Approve only if you want this change in CI.",
		map[string]any{"kind": "git_push", "branch": g.Branch, "head": g.Head, "paths": g.Paths, "fingerprint": g.Fingerprint}
}

// ciPushApproved reports whether a person approved exactly this push (the fingerprint of the head and the paths).
func ciPushApproved(ctx context.Context, d Deps, taskID, fingerprint string) bool {
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return false
	}
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind != EvidenceCIPushApproved {
			continue
		}
		var data struct {
			Fingerprint string `json:"fingerprint"`
		}
		if json.Unmarshal(ev[i].Data, &data) == nil && data.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}

// RecordGateApproval is what the core calls as it records a person's "approved" for a pending request of a task's run:
// payload is the request's payload. For the CI push gate it stores the approval (evidence with the fingerprint of the head and
// paths the person was shown), so the node, run again, pushes exactly that. handled is false for any other gate.
func RecordGateApproval(ctx context.Context, ts *tasks.Service, taskID string, payload []byte, by string) (handled bool, err error) {
	var g struct {
		Gate        string `json:"gate"`
		Fingerprint string `json:"fingerprint"`
	}
	if json.Unmarshal(payload, &g) != nil || g.Gate != GateCIPush || g.Fingerprint == "" || taskID == "" {
		return false, nil
	}
	data, _ := json.Marshal(map[string]any{"fingerprint": g.Fingerprint, "by": by})
	passed := true
	_, err = ts.AddEvidence(ctx, taskID, tasks.Evidence{Kind: EvidenceCIPushApproved, Passed: &passed, Summary: "push of CI definitions approved by " + by, Data: data})
	return true, err
}

// checkCIPush returns the gate error when the branch changes CI definitions and no person approved this exact push; nil
// otherwise. A branch whose changes cannot be listed is treated as if it changed them: the push waits rather than guesses.
func checkCIPush(ctx context.Context, d Deps, t tasks.Task, repoChanged func() ([]string, error), head, base string) error {
	changed, err := repoChanged()
	var paths []string
	if err != nil {
		paths = []string{"(the changes against " + base + " could not be listed: " + oneLine(err.Error()) + ")"}
	} else {
		paths = CIDefinitionPaths(changed)
	}
	if len(paths) == 0 {
		return nil
	}
	fp := ciPushFingerprint(head, paths)
	if ciPushApproved(ctx, d, t.ID, fp) {
		return nil
	}
	return &ciPushGate{Branch: t.Branch, Head: head, Paths: paths, Fingerprint: fp}
}

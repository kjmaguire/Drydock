package host

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"
)

// ErrCIStillRunning ends WaitCI when checks were still running at CIWait.MaxWait.
var ErrCIStillRunning = errors.New("host: CI is still running")

// CIWait says how WaitCI waits for a commit's CI.
type CIWait struct {
	Poll    time.Duration // between looks (default 15s)
	Grace   time.Duration // how long a commit may show no CI result, or not every required check, before the wait ends (default 2m)
	MaxWait time.Duration // how long checks may keep running before the wait ends with ErrCIStillRunning (default 30m)
	// Required are the check names branch protection requires (RequiredChecks). Each is waited for by name: one that
	// reported and runs is waited for, one that has not reported when the grace period ends is listed in CIState.Missing.
	Required []string
}

func (w CIWait) withDefaults() CIWait {
	if w.Poll <= 0 {
		w.Poll = 15 * time.Second
	}
	if w.Grace <= 0 {
		w.Grace = 2 * time.Minute
	}
	if w.MaxWait <= 0 {
		w.MaxWait = 30 * time.Minute
	}
	return w
}

// WaitCI waits for CI on a commit (check runs and commit statuses) and returns what it reported. The wait ends when every
// check that reported has finished and every required check has reported, when a check failed and nothing else runs, or
// when the grace period is over with nothing (or not every required check) reported: CIState.Missing then names the
// required checks that never did. Checks still running at MaxWait end it with ErrCIStillRunning and the state so far. A
// rate limit makes it wait longer, never fail. WaitCI does not decide whether a commit without CI may be merged; the
// caller does (the pull-request flow asks the human, unless a human declared that the repository has no CI).
func (g GitHub) WaitCI(ctx context.Context, sha string, w CIWait) (CIState, error) {
	w = w.withDefaults()
	start := time.Now()
	var cache CICache // unchanged answers come back as 304s, which do not count against the rate limit
	for {
		st, err := g.ChecksCached(ctx, sha, &cache)
		if errors.Is(err, ErrRateLimited) {
			// back off as GitHub asked (Retry-After, or until the limit resets) with some jitter, and keep waiting; never
			// block local work
			wait := w.Poll * 4
			var rl *RateLimitError
			if errors.As(err, &rl) {
				wait = rl.Wait(time.Now(), wait, 15*time.Minute)
			}
			wait += time.Duration(rand.Int64N(int64(wait/10) + 1))
			if err := sleep(ctx, wait); err != nil {
				return st, err
			}
			continue
		}
		if err != nil {
			return st, err
		}
		st.Missing = missingChecks(st, w.Required)
		waited := time.Since(start)
		if !st.Pending {
			switch {
			case len(st.Failed) > 0, len(st.Runs) > 0 && len(st.Missing) == 0:
				return st, nil
			case waited > w.Grace:
				return st, nil
			}
		}
		if waited > w.MaxWait {
			return st, fmt.Errorf("%w after %s", ErrCIStillRunning, w.MaxWait)
		}
		if err := sleep(ctx, w.Poll); err != nil {
			return st, err
		}
	}
}

// missingChecks lists the required check names that no check run or status on the commit carries.
func missingChecks(st CIState, required []string) []string {
	have := map[string]bool{}
	for _, r := range st.Runs {
		have[r.Name] = true
	}
	var out []string
	for _, n := range required {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}

// requiredStatusChecks is the required-checks part of classic branch protection.
type requiredStatusChecks struct {
	Contexts []string `json:"contexts"`
	Checks   []struct {
		Context string `json:"context"`
	} `json:"checks"`
}

// RequiredChecks returns the names of the checks that must pass before the branch takes a merge: the required status checks
// of classic branch protection and of the repository rulesets that apply to the branch. Whether the branch is protected,
// and the rulesets, can be read by anyone who can read the repository; when the branch view does not list the required
// checks, the protection endpoint does, but GitHub answers it only to administrators (403 or 404 for anyone else). What
// could not be read is described by err, next to the names that could: the caller says so and waits for every check that
// reports instead.
func (g GitHub) RequiredChecks(ctx context.Context, branch string) ([]string, error) {
	b := url.PathEscape(branch)
	seen := map[string]bool{}
	var names []string
	add := func(rc *requiredStatusChecks) {
		if rc == nil {
			return
		}
		all := append([]string(nil), rc.Contexts...)
		for _, c := range rc.Checks {
			all = append(all, c.Context)
		}
		for _, n := range all {
			if n = strings.TrimSpace(n); n != "" && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	var problems []error
	var br struct {
		Protected  bool `json:"protected"`
		Protection struct {
			RequiredStatusChecks *requiredStatusChecks `json:"required_status_checks"`
		} `json:"protection"`
	}
	if _, err := g.API.Do(ctx, "GET", g.p("/branches/%s", b), nil, &br, ""); err != nil {
		problems = append(problems, fmt.Errorf("branch %s: %w", branch, err))
	} else if br.Protected {
		rc := br.Protection.RequiredStatusChecks
		if rc == nil {
			var full requiredStatusChecks
			if _, err := g.API.Do(ctx, "GET", g.p("/branches/%s/protection/required_status_checks", b), nil, &full, ""); err != nil {
				problems = append(problems, fmt.Errorf("the protection of %s: %w", branch, err))
			} else {
				rc = &full
			}
		}
		add(rc)
	}
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if _, err := g.API.Do(ctx, "GET", g.p("/rules/branches/%s?per_page=100", b), nil, &rules, ""); err != nil {
		problems = append(problems, fmt.Errorf("the rulesets of %s: %w", branch, err))
	} else {
		for _, r := range rules {
			if r.Type != "required_status_checks" {
				continue
			}
			var rc requiredStatusChecks
			for _, c := range r.Parameters.RequiredStatusChecks {
				rc.Contexts = append(rc.Contexts, c.Context)
			}
			add(&rc)
		}
	}
	return names, errors.Join(problems...)
}

// MergeWait says how MergeState waits for GitHub to work out whether a pull request can be merged.
type MergeWait struct {
	Tries int           // reads in all (default 4)
	Delay time.Duration // between reads (default 2s)
}

// MergeState reads the pull request until GitHub has worked out whether it can be merged: after a push GitHub computes that
// in the background and answers "unknown" meanwhile. When the tries run out the state stays unknown. A merged pull request
// is returned as it is. PR.MergeStatus reads the state.
func (g GitHub) MergeState(ctx context.Context, n int, w MergeWait) (PR, error) {
	if w.Tries <= 0 {
		w.Tries = 4
	}
	if w.Delay <= 0 {
		w.Delay = 2 * time.Second
	}
	for i := 1; ; i++ {
		pr, _, err := g.GetPR(ctx, n, "")
		if err != nil || pr.Merged || pr.MergeStatus() != "unknown" || i >= w.Tries {
			return pr, err
		}
		if err := sleep(ctx, w.Delay); err != nil {
			return pr, err
		}
	}
}

// MergeStatus is GitHub's view of whether the pull request can be merged now (REST mergeable_state; GraphQL calls it
// mergeStateStatus), in lower case: clean, unstable or has_hooks (it can be merged), blocked (branch protection: a required
// review or check is missing), behind (the protection wants the branch up to date first), dirty (merge conflicts), draft,
// or unknown (not worked out yet).
func (p PR) MergeStatus() string {
	s := strings.ToLower(strings.TrimSpace(p.MergeableState))
	switch {
	case p.Draft:
		return "draft"
	case s == "":
		return "unknown"
	case p.Mergeable != nil && !*p.Mergeable && (s == "clean" || s == "unstable" || s == "has_hooks"):
		return "dirty" // GitHub says the branch cannot be merged
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

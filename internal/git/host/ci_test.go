package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// GhCLI tests run this test binary as `gh` (a LABELED TEST DOUBLE): it records its arguments and stdin and answers like
	// `gh api -i`, so the exact command line Drydock runs can be checked on every platform
	if log := os.Getenv("DRYDOCK_FAKE_GH_LOG"); log != "" {
		os.Exit(fakeGh(log))
	}
	os.Exit(m.Run())
}

type ghCall struct {
	Args  []string        `json:"args"`
	Stdin json.RawMessage `json:"stdin,omitempty"`
}

func fakeGh(log string) int {
	in, _ := io.ReadAll(os.Stdin)
	c := ghCall{Args: os.Args[1:]}
	if json.Valid(in) {
		c.Stdin = in
	}
	b, _ := json.Marshal(c)
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 2
	}
	fmt.Fprintf(f, "%s\n", b)
	f.Close()
	body := `{}`
	if strings.HasSuffix(os.Args[len(os.Args)-1], "/merge") {
		body = `{"sha":"mergedsha","merged":true}`
	}
	fmt.Printf("HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n\r\n%s", body)
	return 0
}

func readGhCalls(t *testing.T, path string) []ghCall {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []ghCall
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var c ghCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// The merge is the REST merge through `gh api`, never `gh pr merge` (which has --admin): every argument and every field of
// the request body is checked, for every merge method, so nothing asks GitHub for administrator rights or a bypass.
func TestMergeNeverAsksForAdministratorRights(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gh.jsonl")
	t.Setenv("DRYDOCK_FAKE_GH_LOG", log)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	g := GitHub{API: GhCLI{Bin: exe}, Owner: "o", Repo: "r"}
	for _, m := range []MergeStrategy{MergeMerge, MergeSquash, MergeRebase} {
		sha, err := g.Merge(context.Background(), 9, m, "reviewedsha", "a title")
		if err != nil || sha != "mergedsha" {
			t.Fatalf("%s: %q %v", m, sha, err)
		}
	}
	calls := readGhCalls(t, log)
	if len(calls) != 3 {
		t.Fatalf("gh ran %d times, want 3", len(calls))
	}
	for _, c := range calls {
		if len(c.Args) == 0 || c.Args[0] != "api" || c.Args[len(c.Args)-1] != "/repos/o/r/pulls/9/merge" || !strings.Contains(strings.Join(c.Args, " "), "-X PUT") {
			t.Fatalf("the merge must be PUT /repos/o/r/pulls/9/merge through gh api: %q", c.Args)
		}
		for _, a := range c.Args {
			if l := strings.ToLower(a); strings.Contains(l, "admin") || strings.Contains(l, "bypass") || l == "--auto" {
				t.Fatalf("merge argument %q asks for more than a plain merge: %q", a, c.Args)
			}
		}
		var body map[string]any
		if err := json.Unmarshal(c.Stdin, &body); err != nil {
			t.Fatalf("merge body %s: %v", c.Stdin, err)
		}
		for k := range body {
			switch k {
			case "merge_method", "sha", "commit_title":
			default:
				t.Fatalf("merge body field %q is not a plain merge: %s", k, c.Stdin)
			}
		}
		if body["sha"] != "reviewedsha" {
			t.Fatalf("the reviewed head guard is missing: %s", c.Stdin)
		}
	}
}

// statusFixture serves check runs and the combined commit status of commit abc.
func statusFixture(t *testing.T, runs, statuses string) GitHub {
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits/abc/check-runs"):
			fmt.Fprintf(w, `{"total_count":0,"check_runs":%s}`, runs)
		case strings.HasSuffix(r.URL.Path, "/commits/abc/status"):
			// GitHub's combined state is "pending" when there is no status at all: it must not be read as CI
			fmt.Fprintf(w, `{"state":"pending","statuses":%s}`, statuses)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
}

// Commit statuses (the older status API many outside CI services use) count as CI next to check runs.
func TestChecksCountCommitStatuses(t *testing.T) {
	cases := []struct {
		name, runs, statuses     string
		total, failed            int
		pending, passed          bool
		firstKind, firstConclude string
	}{
		{"statuses only, green", `[]`, `[{"id":7,"context":"ci/circleci","state":"success","target_url":"https://ci.test/7","description":"ok"}]`, 1, 0, false, true, KindStatus, "success"},
		{"a pending status keeps CI pending", `[]`, `[{"id":7,"context":"deploy","state":"pending"}]`, 1, 0, true, false, KindStatus, ""},
		{"failure and error statuses fail", `[]`, `[{"id":7,"context":"a","state":"failure"},{"id":8,"context":"b","state":"error"}]`, 2, 2, false, false, KindStatus, "failure"},
		{"runs and statuses together", `[{"id":1,"name":"build","status":"completed","conclusion":"success"}]`, `[{"id":7,"context":"deploy","state":"success"}]`, 2, 0, false, true, KindCheckRun, "success"},
		{"nothing at all is not a pass", `[]`, `[]`, 0, 0, false, false, "", ""},
	}
	for _, c := range cases {
		s, err := statusFixture(t, c.runs, c.statuses).Checks(context.Background(), "abc")
		if err != nil {
			t.Fatal(c.name, err)
		}
		if len(s.Runs) != c.total || len(s.Failed) != c.failed || s.Pending != c.pending || s.Passed != c.passed {
			t.Errorf("%s: %+v", c.name, s)
			continue
		}
		if c.total > 0 && (s.Runs[0].Kind != c.firstKind || s.Runs[0].Conclusion != c.firstConclude) {
			t.Errorf("%s: first result %+v", c.name, s.Runs[0])
		}
	}
	s, _ := statusFixture(t, `[]`, `[{"id":7,"context":"ci/circleci","state":"success","target_url":"https://ci.test/7","description":"ok"}]`).Checks(context.Background(), "abc")
	if r := s.Runs[0]; r.Name != "ci/circleci" || r.URL != "https://ci.test/7" || r.Description != "ok" || r.Status != "completed" {
		t.Fatalf("a status maps to a result with its context, link and description: %+v", r)
	}
}

// ciScript is a commit whose CI changes as it is polled: step i of the script is what GitHub shows on the i-th look (the
// last step repeats). Each step lists check runs as name=status[/conclusion].
type ciScript struct {
	mu    sync.Mutex
	looks int
	steps [][]string
}

func (c *ciScript) serve(t *testing.T) GitHub {
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			step := c.steps[min(c.looks, len(c.steps)-1)]
			c.looks++
			var runs []map[string]any
			for i, s := range step {
				name, state, _ := strings.Cut(s, "=")
				status, conclusion, _ := strings.Cut(state, "/")
				runs = append(runs, map[string]any{"id": i + 1, "name": name, "status": status, "conclusion": conclusion})
			}
			json.NewEncoder(w).Encode(map[string]any{"total_count": len(runs), "check_runs": runs})
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprint(w, `{"state":"pending","statuses":[]}`)
		}
	})
}

func (c *ciScript) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.looks
}

func TestWaitCI(t *testing.T) {
	ctx := context.Background()
	fast := CIWait{Poll: 5 * time.Millisecond, Grace: 40 * time.Millisecond, MaxWait: 2 * time.Second}

	// nothing ever reports: the wait ends after the grace period with nothing, and does not call that a pass
	none := &ciScript{steps: [][]string{{}}}
	start := time.Now()
	st, err := none.serve(t).WaitCI(ctx, "abc", fast)
	if err != nil || len(st.Runs) != 0 || st.Passed || time.Since(start) < fast.Grace {
		t.Fatalf("no CI: %+v %v after %s", st, err, time.Since(start))
	}

	// running checks are waited for until they finish
	slow := &ciScript{steps: [][]string{{}, {"build=in_progress"}, {"build=in_progress"}, {"build=completed/success"}}}
	if st, err = slow.serve(t).WaitCI(ctx, "abc", CIWait{Poll: 5 * time.Millisecond, Grace: 5 * time.Second, MaxWait: 10 * time.Second}); err != nil || !st.Passed || slow.count() != 4 {
		t.Fatalf("running checks: %+v %v after %d looks", st, err, slow.count())
	}

	// a failure ends the wait as soon as nothing else runs
	red := &ciScript{steps: [][]string{{"lint=completed/failure", "test=completed/success"}}}
	if st, err = red.serve(t).WaitCI(ctx, "abc", CIWait{Poll: time.Millisecond, Grace: time.Hour, MaxWait: time.Hour, Required: []string{"deploy"}}); err != nil || len(st.Failed) != 1 {
		t.Fatalf("failure: %+v %v", st, err)
	}

	// a required check that is still pending well past the grace period is waited for; one that never reports is listed
	// missing once the grace period is over
	required := &ciScript{}
	for i := 0; i < 20; i++ {
		required.steps = append(required.steps, []string{"build=completed/success", "deploy=in_progress"})
	}
	required.steps = append(required.steps, []string{"build=completed/success", "deploy=completed/success"})
	start = time.Now()
	if st, err = required.serve(t).WaitCI(ctx, "abc", CIWait{Poll: 5 * time.Millisecond, Grace: 20 * time.Millisecond, MaxWait: 10 * time.Second, Required: []string{"deploy"}}); err != nil || !st.Passed || len(st.Missing) != 0 || required.count() != 21 || time.Since(start) < 20*time.Millisecond {
		t.Fatalf("a pending required check must be waited for: %+v %v after %d looks", st, err, required.count())
	}
	missing := &ciScript{steps: [][]string{{"build=completed/success"}}}
	if st, err = missing.serve(t).WaitCI(ctx, "abc", CIWait{Poll: 5 * time.Millisecond, Grace: 30 * time.Millisecond, MaxWait: 10 * time.Second, Required: []string{"build", "deploy"}}); err != nil || len(st.Missing) != 1 || st.Missing[0] != "deploy" || missing.count() < 2 {
		t.Fatalf("a required check that never reports: %+v %v after %d looks", st, err, missing.count())
	}

	// checks still running at the deadline end the wait with ErrCIStillRunning and the state so far
	stuck := &ciScript{steps: [][]string{{"e2e=queued"}}}
	if st, err = stuck.serve(t).WaitCI(ctx, "abc", CIWait{Poll: 5 * time.Millisecond, Grace: 10 * time.Millisecond, MaxWait: 50 * time.Millisecond}); !errors.Is(err, ErrCIStillRunning) || !st.Pending || len(st.Runs) != 1 {
		t.Fatalf("stuck checks: %+v %v", st, err)
	}
}

func TestWaitCIBacksOffOnARateLimit(t *testing.T) {
	var looks atomic.Int32
	g := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/check-runs") {
			if looks.Add(1) == 1 {
				w.Header().Set("X-Ratelimit-Remaining", "0")
				w.WriteHeader(403)
				fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
				return
			}
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":1,"name":"build","status":"completed","conclusion":"success"}]}`)
			return
		}
		fmt.Fprint(w, `{"state":"pending","statuses":[]}`)
	})
	st, err := g.WaitCI(context.Background(), "abc", CIWait{Poll: time.Millisecond, Grace: time.Second, MaxWait: 5 * time.Second})
	if err != nil || !st.Passed || looks.Load() != 2 {
		t.Fatalf("%+v %v after %d looks", st, err, looks.Load())
	}
}

func TestRequiredChecks(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	serve := func(routes map[string]answer) GitHub {
		return fixture(t, func(w http.ResponseWriter, r *http.Request) {
			a, ok := routes[strings.TrimPrefix(r.URL.EscapedPath(), "/repos/o/r")]
			if !ok {
				t.Errorf("unexpected path %s", r.URL.Path)
				a = answer{404, `{"message":"Not Found"}`}
			}
			w.WriteHeader(a.status)
			fmt.Fprint(w, a.body)
		})
	}
	const unprotected = `{"name":"main","protected":false}`
	noRules := answer{200, `[]`}
	ctx := context.Background()

	// not protected, no rulesets: nothing required, nothing unread
	names, err := serve(map[string]answer{"/branches/main": {200, unprotected}, "/rules/branches/main": noRules}).RequiredChecks(ctx, "main")
	if err != nil || len(names) != 0 {
		t.Fatalf("unprotected: %v %v", names, err)
	}
	// the branch view lists the required checks (anyone who can read the repository sees it); a ruleset adds one
	names, err = serve(map[string]answer{
		"/branches/main":       {200, `{"protected":true,"protection":{"enabled":true,"required_status_checks":{"enforcement_level":"everyone","contexts":["build"],"checks":[{"context":"build","app_id":15368},{"context":"lint"}]}}}`},
		"/rules/branches/main": {200, `[{"type":"pull_request"},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"deploy","integration_id":1},{"context":"lint"}]}}]`},
	}).RequiredChecks(ctx, "main")
	if err != nil || strings.Join(names, ",") != "build,lint,deploy" {
		t.Fatalf("branch view and rulesets: %v %v", names, err)
	}
	// protected, and only the protection endpoint lists the checks: an administrator reads them there
	names, err = serve(map[string]answer{
		"/branches/main": {200, `{"protected":true,"protection":{"enabled":true}}`},
		"/branches/main/protection/required_status_checks": {200, `{"strict":true,"contexts":["test"]}`},
		"/rules/branches/main":                             noRules,
	}).RequiredChecks(ctx, "main")
	if err != nil || strings.Join(names, ",") != "test" {
		t.Fatalf("protection endpoint: %v %v", names, err)
	}
	// anyone else gets 403 (or 404) there: the names that could be read come back with an error that says what was not
	for _, refused := range []answer{{403, `{"message":"Must have admin rights to Repository."}`}, {404, `{"message":"Not Found"}`}} {
		names, err = serve(map[string]answer{
			"/branches/main": {200, `{"protected":true,"protection":{"enabled":true}}`},
			"/branches/main/protection/required_status_checks": refused,
			"/rules/branches/main":                             {200, `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"deploy"}]}}]`},
		}).RequiredChecks(ctx, "main")
		if err == nil || !strings.Contains(err.Error(), "the protection of main") || strings.Join(names, ",") != "deploy" {
			t.Fatalf("protection unreadable (%d): %v %v", refused.status, names, err)
		}
	}
	// a branch name with a slash is one path segment
	names, err = serve(map[string]answer{"/branches/release%2F1.0": {200, unprotected}, "/rules/branches/release%2F1.0": noRules}).RequiredChecks(ctx, "release/1.0")
	if err != nil || len(names) != 0 {
		t.Fatalf("release/1.0: %v %v", names, err)
	}
}

func TestMergeState(t *testing.T) {
	var mu sync.Mutex
	reads := 0
	states := []string{`"mergeable":null,"mergeable_state":"unknown"`, `"mergeable":null,"mergeable_state":"unknown"`, `"mergeable":true,"mergeable_state":"blocked"`}
	g := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		s := states[min(reads, len(states)-1)]
		reads++
		fmt.Fprintf(w, `{"number":5,"state":"open","head":{"ref":"b","sha":"h"},"base":{"ref":"main"},%s}`, s)
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return reads
	}
	pr, err := g.MergeState(context.Background(), 5, MergeWait{Tries: 5, Delay: time.Millisecond})
	if err != nil || pr.MergeStatus() != "blocked" || count() != 3 {
		t.Fatalf("GitHub works the state out in the background; MergeState reads again until it has: %+v %v after %d reads", pr, err, count())
	}
	mu.Lock()
	reads, states = 0, []string{`"mergeable":null,"mergeable_state":"unknown"`}
	mu.Unlock()
	if pr, err = g.MergeState(context.Background(), 5, MergeWait{Tries: 3, Delay: time.Millisecond}); err != nil || pr.MergeStatus() != "unknown" || count() != 3 {
		t.Fatalf("still unknown after the tries: %+v %v after %d reads", pr, err, count())
	}

	yes, no := true, false
	for _, c := range []struct {
		pr   PR
		want string
	}{
		{PR{MergeableState: "clean", Mergeable: &yes}, "clean"},
		{PR{MergeableState: "BEHIND", Mergeable: &yes}, "behind"},
		{PR{MergeableState: "dirty", Mergeable: &no}, "dirty"},
		{PR{MergeableState: "clean", Mergeable: &no}, "dirty"},
		{PR{MergeableState: "clean", Mergeable: &yes, Draft: true}, "draft"},
		{PR{}, "unknown"},
	} {
		if got := c.pr.MergeStatus(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.pr, got, c.want)
		}
	}
}

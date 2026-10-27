package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// stallingAPI is a labeled test double of the GitHub API: it never answers (an outage, a black-holed network).
type stallingAPI struct{}

func (stallingAPI) Do(ctx context.Context, _, _ string, _ any, _ any, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (stallingAPI) Raw(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type authFailAPI struct{}

func (authFailAPI) Do(context.Context, string, string, any, any, string) (string, error) {
	return "", errors.Join(host.ErrAuth, errors.New("gh: not logged in"))
}
func (authFailAPI) Raw(context.Context, string) (string, error) { return "", host.ErrAuth }

// GitHub that does not answer is reported as such, never as "not signed in" (gh may well be signed in); a real sign-in
// failure still says to sign in.
func TestGitHubStatusTellsATimeoutFromASignedOutGh(t *testing.T) {
	old := githubStatusTimeout
	githubStatusTimeout = 50 * time.Millisecond
	defer func() { githubStatusTimeout = old }()
	st := (&Core{cfg: Config{GitHubAPI: stallingAPI{}}}).GitHubAccountStatus(context.Background())
	if !st.Installed || st.LoggedIn || !st.Unreachable || strings.Contains(st.Error, "not signed in") || !strings.Contains(st.Error, "did not answer") {
		t.Fatalf("timeout: %+v", st)
	}
	st = (&Core{cfg: Config{GitHubAPI: authFailAPI{}}}).GitHubAccountStatus(context.Background())
	if st.Unreachable || !strings.Contains(st.Error, "gh is not signed in") {
		t.Fatalf("signed out: %+v", st)
	}
}

// The version is the first line of `gh --version`; anything else is no version rather than a guess.
func TestParseGhVersion(t *testing.T) {
	for in, want := range map[string]string{
		"gh version 2.83.1 (2025-11-13)\nhttps://example.invalid/releases/tag/v2.83.1\n": "2.83.1",
		"gh version 2.90.0-rc.1 (2026-09-01)":                                            "2.90.0-rc.1",
		"  gh version 2.4.0\n":                                                           "2.4.0",
		"":                                                                               "",
		"GitHub CLI, unknown":                                                            "",
		"gh version DEV":                                                                 "",
	} {
		if got := parseGhVersion(in); got != want {
			t.Errorf("parseGhVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// The GitHub API the core talks through: a configured API (what tests give the core) is used as it is, with no gh path to
// ask a version of; otherwise the user's own gh is looked up on PATH, and GitHub cannot be used when there is none.
func TestGithubCLIResolvesTheUsersGhOnlyWhenNoAPIIsConfigured(t *testing.T) {
	api, path, ok := (&Core{cfg: Config{GitHubAPI: authFailAPI{}}}).githubCLI()
	if !ok || path != "" || api == nil {
		t.Fatalf("a configured API: %v %q %v", api, path, ok)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if api, path, ok := (&Core{}).githubCLI(); ok || path != "" || api != nil {
		t.Fatalf("no gh on PATH: %v %q %v", api, path, ok)
	}
	gh := filepath.Join(bin, "gh")
	if runtime.GOOS == "windows" {
		gh += ".exe"
	}
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	api, path, ok = (&Core{}).githubCLI()
	if _, isGh := api.(host.GhCLI); !ok || !isGh || path != gh {
		t.Fatalf("gh on PATH: %T %q %v, want its path %q", api, path, ok, gh)
	}
}

// keepGhVersions starts a test with no kept gh version and a clock it may set, and gives both back when the test ends, so the
// rest of the package run is not left trusting a version read from a stand-in that no longer exists.
func keepGhVersions(t *testing.T) {
	t.Helper()
	ghVersions.Lock()
	key, v, at := ghVersions.key, ghVersions.v, ghVersions.at
	ghVersions.key, ghVersions.v, ghVersions.at = ghBinary{}, "", time.Time{}
	ghVersions.Unlock()
	now := ghVersionNow
	t.Cleanup(func() {
		ghVersions.Lock()
		ghVersions.key, ghVersions.v, ghVersions.at = key, v, at
		ghVersions.Unlock()
		ghVersionNow = now
	})
}

// ghStandIn writes a shell script at path that prints "gh version <v>" and notes each run in the count file. The stand-in is a
// shell script, so the tests that use it do not run on Windows (the test binary is the stand-in there, in ghOnPath).
func ghStandIn(t *testing.T, path, count, v string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho run >> %q\necho \"gh version %s (2026-10-01)\"\n", count, v)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ghRuns is how many times a stand-in gh noted a run in its count file.
func ghRuns(count string) int {
	b, _ := os.ReadFile(count)
	return strings.Count(string(b), "run")
}

// `gh --version` runs once per gh binary: a second check reuses the answer, and a gh that changed in place (an update that
// rewrote the file) is asked again at once.
func TestGhVersionIsReadOncePerBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh here is a shell script")
	}
	keepGhVersions(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ghVersionNow = func() time.Time { return now }
	count := filepath.Join(t.TempDir(), "runs")
	gh := filepath.Join(t.TempDir(), "gh")
	ghStandIn(t, gh, count, "2.83.1")
	if v := ghVersion(context.Background(), gh); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("first read %q, %d runs", v, ghRuns(count))
	}
	if v := ghVersion(context.Background(), gh); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("the second read must reuse the answer: %q, %d runs", v, ghRuns(count))
	}
	ghStandIn(t, gh, count, "2.90.0")
	if err := os.Chtimes(gh, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if v := ghVersion(context.Background(), gh); v != "2.90.0" || ghRuns(count) != 2 {
		t.Fatalf("an updated gh is asked again: %q, %d runs", v, ghRuns(count))
	}
}

// A status check whose budget GitHub used up (the context is done) still reports a kept version, and does not start gh for
// one it does not have: the check is never longer for the version than for the sign-in.
func TestGhVersionDoesNotWaitOnGhOnceTheBudgetIsSpent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh here is a shell script")
	}
	keepGhVersions(t)
	count := filepath.Join(t.TempDir(), "runs")
	gh := filepath.Join(t.TempDir(), "gh")
	ghStandIn(t, gh, count, "2.83.1")
	spent, cancel := context.WithCancel(context.Background())
	cancel()
	if v := ghVersion(spent, gh); v != "" || ghRuns(count) != 0 {
		t.Fatalf("no kept version and no budget: %q, %d runs", v, ghRuns(count))
	}
	if v := ghVersion(context.Background(), gh); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("first read %q, %d runs", v, ghRuns(count))
	}
	if v := ghVersion(spent, gh); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("the kept version stands when the budget is spent: %q, %d runs", v, ghRuns(count))
	}
}

// A launcher or shim (scoop, chocolatey, a wrapper .cmd) does not change when the gh behind it does, so a kept version is
// trusted for a minute at most: an update behind an unchanged shim shows on the first read after the minute, and gh runs once
// more for it, then the new answer is kept in turn.
func TestGhVersionIsAskedAgainAfterAMinuteBehindAnUnchangedShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh here is a shell script")
	}
	keepGhVersions(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ghVersionNow = func() time.Time { return now }
	dir := t.TempDir()
	count := filepath.Join(dir, "runs")
	behind := filepath.Join(dir, "behind") // the version of the gh the shim launches; the shim itself never changes
	shim := filepath.Join(dir, "gh")
	setBehind := func(v string) {
		t.Helper()
		if err := os.WriteFile(behind, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setBehind("2.83.1")
	script := fmt.Sprintf("#!/bin/sh\necho run >> %q\necho \"gh version $(cat %q) (2026-10-01)\"\n", count, behind)
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(shim)
	if err != nil {
		t.Fatal(err)
	}
	if v := ghVersion(context.Background(), shim); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("first read %q, %d runs", v, ghRuns(count))
	}
	setBehind("2.90.0")
	if v := ghVersion(context.Background(), shim); v != "2.83.1" || ghRuns(count) != 1 {
		t.Fatalf("within the minute the kept answer stands: %q, %d runs", v, ghRuns(count))
	}
	now = now.Add(ghVersionTTL)
	if v := ghVersion(context.Background(), shim); v != "2.90.0" || ghRuns(count) != 2 {
		t.Fatalf("after the minute gh is asked again: %q, %d runs", v, ghRuns(count))
	}
	if v := ghVersion(context.Background(), shim); v != "2.90.0" || ghRuns(count) != 2 {
		t.Fatalf("the new answer is kept in turn: %q, %d runs", v, ghRuns(count))
	}
	if after, err := os.Stat(shim); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the shim must not have changed for this test to mean anything: %v", err)
	}
}

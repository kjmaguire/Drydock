package core

import (
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// One rule for both caches of the Repositories table: an answer is reused until it is repoOverviewTTL old, exactly that old
// included, and never when a refresh was asked for; a repository's answer and a project's origin remote alike.
func TestFreshKeepsAnAnswerUntilItIsATTLOld(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	r := &repoOverviews{cache: map[string]repoOverviewEntry{}, remoteCache: map[string]projectRemote{}, now: func() time.Time { return t0 }}
	ages := map[string]time.Duration{"young": time.Second, "at-ttl": repoOverviewTTL, "stale": repoOverviewTTL + time.Nanosecond}
	for name, age := range ages {
		r.cache[name] = repoOverviewEntry{at: t0.Add(-age), ov: host.RepoOverview{FullName: name}}
		r.remoteCache[name] = projectRemote{at: t0.Add(-age), state: RepoStateGitHub, full: name}
	}
	for name, want := range map[string]bool{"young": true, "at-ttl": true, "stale": false, "missing": false} {
		if e, ok := fresh(r, r.cache, name, false); ok != want || (ok && e.ov.FullName != name) {
			t.Errorf("GitHub's answer %s: kept %v, want %v", name, ok, want)
		}
		if e, ok := fresh(r, r.remoteCache, name, false); ok != want || (ok && e.full != name) {
			t.Errorf("origin remote %s: kept %v, want %v", name, ok, want)
		}
		if _, ok := fresh(r, r.cache, name, true); ok {
			t.Errorf("%s: a refresh must not reuse an answer", name)
		}
	}
}

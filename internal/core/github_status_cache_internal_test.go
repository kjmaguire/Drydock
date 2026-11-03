package core

import (
	"testing"
	"time"
)

// F4: a github.status read that started before the sign-in's end (drop) finishes after the sign-in's own refresh: its stale
// "not signed in" answers its caller but is not kept over the refresh's "signed in".
func TestGitHubStatusCacheKeepsNoReadOlderThanADrop(t *testing.T) {
	var k ghStatusCache
	started, release := make(chan struct{}), make(chan struct{})
	staleDone := make(chan GitHubStatus, 1)
	go func() {
		staleDone <- k.get(false, func() (GitHubStatus, bool) {
			close(started)
			<-release
			return GitHubStatus{Installed: true, LoggedIn: false}, true
		})
	}()
	<-started
	k.drop() // gh auth login exited
	fresh := k.get(true, func() (GitHubStatus, bool) { return GitHubStatus{Installed: true, LoggedIn: true, Login: "kyle"}, true })
	if !fresh.LoggedIn {
		t.Fatalf("refresh: %+v", fresh)
	}
	close(release)
	if st := <-staleDone; st.LoggedIn {
		t.Fatalf("the stale read answers its own caller with what it read: %+v", st)
	}
	got := k.get(false, func() (GitHubStatus, bool) { t.Fatal("the kept answer was not used"); return GitHubStatus{}, false })
	if !got.LoggedIn || got.Login != "kyle" {
		t.Fatalf("the stale read replaced the newer answer: %+v", got)
	}
}

// F4: of two reads in one generation, the one that started later is kept, whichever finishes last.
func TestGitHubStatusCacheKeepsTheLaterRead(t *testing.T) {
	var k ghStatusCache
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		k.get(true, func() (GitHubStatus, bool) {
			close(started)
			<-release
			return GitHubStatus{Login: "old"}, true
		})
	}()
	<-started
	time.Sleep(2 * time.Millisecond) // the second read starts measurably later
	k.get(true, func() (GitHubStatus, bool) { return GitHubStatus{Login: "new"}, true })
	close(release)
	<-done
	if got := k.get(false, func() (GitHubStatus, bool) { return GitHubStatus{Login: "read"}, true }); got.Login != "new" {
		t.Fatalf("kept %+v", got)
	}
	// an answer the read says not to keep (unreachable) is never kept
	k.drop()
	k.get(true, func() (GitHubStatus, bool) { return GitHubStatus{Login: "unreachable"}, false })
	if got := k.get(false, func() (GitHubStatus, bool) { return GitHubStatus{Login: "read"}, true }); got.Login != "read" {
		t.Fatalf("an unkept answer was used: %+v", got)
	}
}

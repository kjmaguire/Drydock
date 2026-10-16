package trial

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agentcatalog"
	"github.com/kjmaguire/drydock/internal/skills"
)

// A skill two agents carry at the same pin is listed once; the same name at two pins is refused with both pins named.
func TestPrebuiltSkillRefsAreUniqueAndRefuseTwoPinsOfOneName(t *testing.T) {
	same := []agentcatalog.Entry{
		{Skills: []agentcatalog.SkillRef{{Registry: "r1", Name: "b", Ref: "aaa"}, {Registry: "r1", Name: "a", Ref: "aaa"}}},
		{Skills: []agentcatalog.SkillRef{{Registry: "r1", Name: "b", Ref: "aaa"}}},
	}
	refs, err := skillRefsOf(same)
	if err != nil || len(refs) != 2 || refs[0].Name != "a" || refs[1].Name != "b" {
		t.Fatalf("%v %v", refs, err)
	}
	twoPins := append(same, agentcatalog.Entry{Skills: []agentcatalog.SkillRef{{Registry: "r1", Name: "b", Ref: "bbb"}}})
	if _, err := skillRefsOf(twoPins); err == nil || !strings.Contains(err.Error(), "skill b at two pins (r1@aaa and r1@bbb)") {
		t.Fatalf("%v", err)
	}
	if refs, err := PrebuiltSkillRefs(); err != nil || len(refs) == 0 {
		t.Fatalf("the shipped catalog: %v %v", refs, err)
	}
}

// slowRegistry is a labelled test double: each Fetch waits for release (or its context) and counts the calls per key.
type slowRegistry struct {
	release chan struct{}
	calls   sync.Map // key -> *int32
	fail    atomic.Bool
}

func (s *slowRegistry) Resolve(_ context.Context, _, ref string) (string, error) { return ref, nil }

func (s *slowRegistry) Fetch(ctx context.Context, repo, commit, root string) ([]skills.RemoteSkill, error) {
	n, _ := s.calls.LoadOrStore(repo+"@"+commit, new(int32))
	atomic.AddInt32(n.(*int32), 1)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.fail.Load() {
		return nil, errors.New("registry down")
	}
	return []skills.RemoteSkill{{Name: repo}}, nil
}

func (s *slowRegistry) count(key string) int32 {
	n, ok := s.calls.Load(key)
	if !ok {
		return 0
	}
	return atomic.LoadInt32(n.(*int32))
}

// One fetch per key, shared by concurrent callers; a fetch of one key does not hold up another key; a failed fetch is not
// kept; and a waiter whose own context is live when the first caller's context cancels the shared fetch gets its own fetch.
func TestCachingFetcherFetchesEachKeyOnceWithoutBlockingOtherKeys(t *testing.T) {
	inner := &slowRegistry{release: make(chan struct{})}
	f := &CachingFetcher{Inner: inner}
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 2; i++ { // two callers of key A wait on one fetch
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.Fetch(ctx, "A", "c1", ""); results <- err }()
	}
	waitFor(t, func() bool { return inner.count("A@c1") == 1 })
	// key B is not behind A's download
	done := make(chan error, 1)
	go func() { _, err := f.Fetch(ctx, "B", "c1", ""); done <- err }()
	waitFor(t, func() bool { return inner.count("B@c1") == 1 })
	close(inner.release)
	for _, c := range []chan error{done, results, results} {
		select {
		case err := <-c:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a fetch did not finish")
		}
	}
	wg.Wait()
	if _, err := f.Fetch(ctx, "A", "c1", ""); err != nil || inner.count("A@c1") != 1 {
		t.Fatalf("a later call is served from memory: %v, %d fetches", err, inner.count("A@c1"))
	}
	// a failed fetch is not kept
	inner.fail.Store(true)
	if _, err := f.Fetch(ctx, "C", "c1", ""); err == nil {
		t.Fatal("the failure reaches the caller")
	}
	inner.fail.Store(false)
	if rs, err := f.Fetch(ctx, "C", "c1", ""); err != nil || len(rs) != 1 || inner.count("C@c1") != 2 {
		t.Fatalf("the next call fetches again: %v %v %d", rs, err, inner.count("C@c1"))
	}
	// the first caller's cancellation does not fail a waiter whose context is live
	blocked := &slowRegistry{release: make(chan struct{})}
	g := &CachingFetcher{Inner: blocked}
	first, cancel := context.WithCancel(ctx)
	firstErr := make(chan error, 1)
	go func() { _, err := g.Fetch(first, "D", "c1", ""); firstErr <- err }()
	waitFor(t, func() bool { return blocked.count("D@c1") == 1 })
	second := make(chan error, 1)
	go func() { _, err := g.Fetch(ctx, "D", "c1", ""); second <- err }()
	time.Sleep(50 * time.Millisecond) // the second caller joins the first's fetch
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("the canceller gets its cancellation: %v", err)
	}
	waitFor(t, func() bool { return blocked.count("D@c1") == 2 })
	close(blocked.release)
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the waiter's own fetch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never finished")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

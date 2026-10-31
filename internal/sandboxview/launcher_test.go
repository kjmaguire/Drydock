package sandboxview

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProcess is a labelled test double of a started viewer: it ends when end is closed.
type fakeProcess struct{ end chan struct{} }

func (p *fakeProcess) Wait() error { <-p.end; return nil }

type fakeStarter struct {
	mu    sync.Mutex
	args  [][]string
	procs []*fakeProcess
	fail  error
}

func (s *fakeStarter) start(args []string) (Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	p := &fakeProcess{end: make(chan struct{})}
	s.args, s.procs = append(s.args, args), append(s.procs, p)
	return p, nil
}

func newLauncher(t *testing.T, max int, remove func(string) error) (*Launcher, *fakeStarter, string) {
	t.Helper()
	dir := t.TempDir()
	st := &fakeStarter{}
	return &Launcher{DataDir: dir, Max: max, Start: st.start, RemoveTries: 3, RemoveWait: time.Millisecond, Remove: remove}, st, dir
}

const okURL = "http://127.0.0.1:49153/"

func TestTheLauncherHandsTheViewerItsAddressTitleAndProfile(t *testing.T) {
	l, st, dir := newLauncher(t, 2, nil)
	if err := l.Open(Request{URL: okURL, Title: "Port 3000 of the task's sandbox"}); err != nil {
		t.Fatal(err)
	}
	r, ok, err := Parse(append([]string{"drydock.exe"}, st.args[0]...))
	if err != nil || !ok || r.URL != okURL || Title(r.Title) != "Port 3000 of the task's sandbox - Drydock isolated window" {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	if err := ValidProfile(dir, r.Profile); err != nil {
		t.Fatalf("the profile the viewer is given is the app's: %v", err)
	}
	close(st.procs[0].end)
	l.Wait()
	if _, err := os.Stat(r.Profile); !os.IsNotExist(err) {
		t.Fatalf("the profile goes when the viewer ends: %v", err)
	}
}

func TestTheLauncherOpensAtMostMaxAndGivesTheSlotBackWhenTheProcessEnds(t *testing.T) {
	// removing the profile fails for a while, as it does while WebView2 lets go of its files
	var mu sync.Mutex
	gate := make(chan struct{})
	remove := func(dir string) error {
		<-gate
		mu.Lock()
		defer mu.Unlock()
		return os.RemoveAll(dir)
	}
	l, st, _ := newLauncher(t, 2, remove)
	for i := 0; i < 2; i++ {
		if err := l.Open(Request{URL: okURL}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Open(Request{URL: okURL}); err == nil || !strings.Contains(err.Error(), "close one first") {
		t.Fatalf("the third is refused: %v", err)
	}
	if n := len(st.procs); n != 2 {
		t.Fatalf("%d started", n)
	}
	close(st.procs[0].end)
	deadline := time.Now().Add(5 * time.Second)
	for l.Running() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// the first viewer's profile is still being removed (the gate is shut), and its slot is free
	if err := l.Open(Request{URL: okURL}); err != nil {
		t.Fatalf("a slot is free once the process ended, not once the cleanup is done: %v", err)
	}
	close(gate)
	close(st.procs[1].end)
	close(st.procs[2].end)
	l.Wait()
	if l.Running() != 0 {
		t.Fatalf("%d still counted", l.Running())
	}
}

func TestTheLauncherRefusesAnAddressThatIsNotAForwardAndStartsNothing(t *testing.T) {
	l, st, dir := newLauncher(t, 2, nil)
	if err := l.Open(Request{URL: "http://example.com/"}); err == nil {
		t.Fatal("refused")
	}
	if len(st.procs) != 0 || l.Running() != 0 {
		t.Fatalf("%d started, %d counted", len(st.procs), l.Running())
	}
	if _, err := os.Stat(dir + "/" + DirName); err == nil {
		if es, _ := os.ReadDir(dir + "/" + DirName); len(es) != 0 {
			t.Fatalf("a profile was left: %v", es)
		}
	}
}

func TestTheLauncherGivesBackWhatAFailedStartTook(t *testing.T) {
	l, st, dir := newLauncher(t, 1, nil)
	st.fail = errors.New("cannot start")
	if err := l.Open(Request{URL: okURL}); err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Fatalf("%v", err)
	}
	if l.Running() != 0 {
		t.Fatalf("%d counted", l.Running())
	}
	if es, _ := os.ReadDir(dir + "/" + DirName); len(es) != 0 {
		t.Fatalf("a profile was left: %v", es)
	}
	st.fail = nil
	if err := l.Open(Request{URL: okURL}); err != nil {
		t.Fatalf("the slot is free again: %v", err)
	}
	close(st.procs[0].end)
	l.Wait()
}

package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	wf "github.com/kjmaguire/drydock/internal/workflow"
)

func regGraph(name, mid string, handler wf.NodeHandler) *wf.Graph {
	return wf.NewGraph(name).
		Add(wf.Route("start", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			return wf.NodeOutput{Value: val("go")}, nil
		}),
			wf.Request("gate", wf.RequestSpec{Kind: "review", Title: "wait for the human"}),
			wf.Route(wf.NodeID(mid), handler)).
		Edge("start", "gate").EdgeWhen("gate", wf.NodeID(mid), "approved").Start("start")
}

// Extensions register templates after the engine is running. A run that is waiting for the human on the old graph must
// still resume; registering a different topology under the same name would strand it, so it is refused.
func TestRegisterNewIsSafeWhileRunsAreInFlight(t *testing.T) {
	e := openEnv(t, t.TempDir())
	var mu sync.Mutex
	var hits []string
	mk := func(tag string) wf.NodeHandler {
		return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			mu.Lock()
			hits = append(hits, tag)
			mu.Unlock()
			return wf.NodeOutput{Value: val(tag)}, nil
		}
	}
	if err := e.eng.RegisterNew(regGraph("tpl", "after", mk("v1"))); err != nil {
		t.Fatal(err)
	}
	run, err := e.eng.Start(context.Background(), "tpl", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusWaitingHuman)

	// a different topology under the same name is refused: the waiting run's checkpoint carries the old hash
	err = e.eng.RegisterNew(regGraph("tpl", "other-node", mk("v2")))
	if !errors.Is(err, wf.ErrGraphChanged) {
		t.Fatalf("want ErrGraphChanged, got %v", err)
	}
	// the identical topology may be re-registered (fresh closures over fresh dependencies)
	if err := e.eng.RegisterNew(regGraph("tpl", "after", mk("v1b"))); err != nil {
		t.Fatalf("same topology must be accepted: %v", err)
	}
	// a new name is added next to the running one and can start while the first run waits
	if err := e.eng.RegisterNew(regGraph("tpl2", "after", mk("t2"))); err != nil {
		t.Fatal(err)
	}
	if !e.eng.Registered("tpl2") || e.eng.Registered("nope") {
		t.Fatal("Registered is wrong")
	}
	run2, err := e.eng.Start(context.Background(), "tpl2", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run2, wf.StatusWaitingHuman)

	// the original run resumes on the graph registered later (same topology) and completes
	time.Sleep(60 * time.Millisecond)
	pend, _ := e.eng.Pending(context.Background(), run)
	if len(pend) != 1 {
		t.Fatalf("pending %+v", pend)
	}
	if err := e.eng.Resume(context.Background(), run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:test"}); err != nil {
		t.Fatalf("resume after post-start registration: %v", err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != "v1b" {
		t.Fatalf("the swapped handler must run exactly once: %v", hits)
	}
}

// Registration races with Start and Resume lookups under the race detector.
func TestRegisterNewConcurrentWithStart(t *testing.T) {
	e := openEnv(t, t.TempDir())
	noop := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		return wf.NodeOutput{Value: val("x")}, nil
	}
	if err := e.eng.RegisterNew(regGraph("base", "after", noop)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if err := e.eng.RegisterNew(regGraph(fmt.Sprintf("g%d", i), "after", noop)); err != nil {
				errs <- err
			}
		}(i)
		go func() {
			defer wg.Done()
			if _, err := e.eng.Start(context.Background(), "base", nil, wf.StartOptions{}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestUseOutermostWrapsEveryEarlierMiddleware(t *testing.T) {
	e := openEnv(t, t.TempDir())
	var mu sync.Mutex
	var order []string
	mk := func(name string) wf.Middleware {
		return func(next wf.NodeHandler) wf.NodeHandler {
			return func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return next(ctx, in)
			}
		}
	}
	e.eng.Use(wf.KindRoute, mk("first-registered"))
	e.eng.Use(wf.KindRoute, mk("second-registered"))
	e.eng.UseOutermost(wf.KindRoute, mk("outermost"))
	g := wf.NewGraph("mw").Add(wf.Route("only", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		mu.Lock()
		order = append(order, "handler")
		mu.Unlock()
		return wf.NodeOutput{Value: val("x")}, nil
	})).Start("only")
	if err := e.eng.RegisterNew(g); err != nil {
		t.Fatal(err)
	}
	run, _ := e.eng.Start(context.Background(), "mw", nil, wf.StartOptions{})
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	mu.Lock()
	defer mu.Unlock()
	if got := fmt.Sprint(order); got != "[outermost first-registered second-registered handler]" {
		t.Fatalf("order %s", got)
	}
}

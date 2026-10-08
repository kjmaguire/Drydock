package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type Kick struct{}
type Work struct{ From string }
type Done struct{}

var aRuns, bRuns, cRuns, joinRuns atomic.Int32
var bShouldFail atomic.Bool

type starter struct{ _ workflow.AttrSendsMessage[Kick] }

func (s *starter) Handle(ctx *workflow.Context, m string) error { return ctx.SendMessage("", Kick{}) }

type nodeA struct{ _ workflow.AttrSendsMessage[Work] }

func (n *nodeA) Handle(ctx *workflow.Context, m Kick) error {
	aRuns.Add(1) // side effect e.g. file write / API call / billing
	return ctx.SendMessage("", Work{From: "A"})
}

type nodeB struct{ _ workflow.AttrSendsMessage[Work] }

func (n *nodeB) Handle(ctx *workflow.Context, m Kick) error {
	bRuns.Add(1)
	time.Sleep(50 * time.Millisecond)
	if bShouldFail.Load() {
		return errors.New("B crashed (simulated)")
	}
	return ctx.SendMessage("", Work{From: "B"})
}

type nodeC struct{ _ workflow.AttrSendsMessage[Work] }

func (n *nodeC) Handle(ctx *workflow.Context, m Kick) error {
	cRuns.Add(1)
	return ctx.SendMessage("", Work{From: "C"})
}

type join struct{ _ workflow.AttrYieldsOutput[string] }

func (j *join) Handle(ctx *workflow.Context, m Work) error {
	n := joinRuns.Add(1)
	if n%3 == 0 {
		return ctx.YieldOutput(fmt.Sprintf("joined 3 inputs (last from %s)", m.From))
	}
	return nil
}

func build() *workflow.Workflow {
	st := workflow.NewExecutor("start", &starter{}).Bind()
	a := workflow.NewExecutor("A", &nodeA{}).Bind()
	b := workflow.NewExecutor("B", &nodeB{}).Bind()
	c := workflow.NewExecutor("C", &nodeC{}).Bind()
	jn := workflow.NewExecutor("join", &join{}).Bind()
	wf, err := workflow.NewBuilder(st).
		AddFanOutEdge(st, []workflow.ExecutorBinding{a, b, c}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{a, b, c}, jn).
		WithOutputFrom(jn).
		Build()
	if err != nil {
		panic(err)
	}
	return wf
}

func drain(ctx context.Context, run *inproc.StreamingRun, label string) []workflow.CheckpointInfo {
	var cps []workflow.CheckpointInfo
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			fmt.Println(label, "stream err:", err)
			break
		}
		switch e := evt.(type) {
		case workflow.SuperStepCompletedEvent:
			if e.CompletionInfo != nil && e.CompletionInfo.CheckpointInfo != nil {
				cps = append(cps, *e.CompletionInfo.CheckpointInfo)
				fmt.Printf("%s: superstep completed, checkpoint %s\n", label, e.CompletionInfo.CheckpointInfo.CheckpointID[:8])
			}
		case workflow.ExecutorFailedEvent:
			fmt.Printf("%s: executor %s FAILED: %v\n", label, e.ExecutorID, e.Error)
		case workflow.ExecutorCompletedEvent:
			fmt.Printf("%s: executor %s completed\n", label, e.ExecutorID)
		case workflow.OutputEvent:
			fmt.Printf("%s: OUTPUT %v\n", label, e.Output)
		case workflow.ErrorEvent:
			fmt.Printf("%s: ErrorEvent %v\n", label, e.Error)
		}
	}
	return cps
}

func main() {
	ctx := context.Background()
	mgr := checkpoint.NewInMemoryManager()
	env := inproc.Default.WithCheckpointing(mgr)

	bShouldFail.Store(true)
	run, err := env.RunStreaming(ctx, build(), "go")
	if err != nil {
		panic(err)
	}
	cps := drain(ctx, run, "run1")
	run.Close(ctx)
	fmt.Printf("after run1 (B failed in the parallel superstep): A=%d B=%d C=%d join=%d, checkpoints=%d\n", aRuns.Load(), bRuns.Load(), cRuns.Load(), joinRuns.Load(), len(cps))

	// Simulate "manual resume" from last good checkpoint in a new run (same session)
	bShouldFail.Store(false)
	last := cps[len(cps)-1]
	run2, err := env.ResumeStreaming(ctx, build(), last)
	if err != nil {
		panic(err)
	}
	drain(ctx, run2, "resume1")
	run2.Close(ctx)
	fmt.Printf("after resume #1 from checkpoint %s: A=%d B=%d C=%d join=%d\n", last.CheckpointID[:8], aRuns.Load(), bRuns.Load(), cRuns.Load(), joinRuns.Load())

	// Duplicate resume from the same checkpoint (e.g. user double-clicks Resume / two processes)
	run3, err := env.ResumeStreaming(ctx, build(), last)
	if err != nil {
		panic(err)
	}
	drain(ctx, run3, "resume2(dup)")
	run3.Close(ctx)
	fmt.Printf("after duplicate resume #2 from same checkpoint: A=%d B=%d C=%d join=%d\n", aRuns.Load(), bRuns.Load(), cRuns.Load(), joinRuns.Load())
}

package security_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// A workflow request port whose approval is bound to the descriptor hash, answered by the workflow engine and spent
// by the executor through the security service.
func TestWorkflowBoundApprovalConsume(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present func(a security.ActionDescriptor) security.ActionDescriptor
		wantErr error
	}{
		{"same action", func(a security.ActionDescriptor) security.ActionDescriptor { return a }, nil},
		{"different action", func(a security.ActionDescriptor) security.ActionDescriptor { a.Command = "rm -rf /"; return a }, security.ErrHashMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.OpenTemp(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			bus := events.NewSQLBus(db, 256)
			eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: t.TempDir(), LeaseTTL: 300 * time.Millisecond})
			t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
			apr := security.NewApprovals(db, bus, security.NewAudit(db, nil))

			action := security.ActionDescriptor{Kind: security.KindShellDestructive, Project: "p", RunID: "run-x", NodeID: "R", Step: 1, Attempt: 1,
				Tool: "shell", Shell: "bash", Command: "rm -rf build", Cwd: "/work/repo"}
			var mu sync.Mutex
			var consumeErr error
			done := make(chan struct{})
			g := wf.NewGraph("secflow").
				Add(
					wf.AgentTask("A", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
						return wf.NodeOutput{Value: json.RawMessage(`"a"`)}, nil
					}),
					wf.Request("R", wf.RequestSpec{Kind: "approval", Title: "run rm?", ActionHash: action.Hash()}),
					wf.AgentTask("B", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
						var out struct {
							Request string `json:"request"`
						}
						if err := json.Unmarshal(in.Inbox[0].Value, &out); err != nil {
							return wf.NodeOutput{}, err
						}
						err := apr.Consume(ctx, out.Request, tc.present(action))
						mu.Lock()
						consumeErr = err
						mu.Unlock()
						close(done)
						return wf.NodeOutput{Value: json.RawMessage(`"b"`)}, nil
					}),
				).Edge("A", "R").EdgeWhen("R", "B", "approved").Start("A")
			if err := eng.Register(g); err != nil {
				t.Fatal(err)
			}
			run, err := eng.Start(ctx, "secflow", "go", wf.StartOptions{})
			if err != nil {
				t.Fatal(err)
			}
			awaitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if _, err := eng.Await(awaitCtx, run, func(r wf.RunInfo) bool { return r.Status == wf.StatusWaitingHuman }); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			pend, err := eng.Pending(ctx, run)
			if err != nil || len(pend) != 1 {
				t.Fatalf("pending %v %v", pend, err)
			}
			id := string(pend[0].ID)

			// the security service refuses to answer a workflow-bound request; the engine owns that path
			if err := apr.Decide(ctx, id, security.DecisionApprove, "human:kyle"); !errors.Is(err, security.ErrWorkflowBound) {
				t.Fatalf("Decide on a workflow-bound request: %v", err)
			}
			// nothing to consume before the human answered
			if err := apr.Consume(ctx, id, action); !errors.Is(err, security.ErrNotApproved) {
				t.Fatalf("consume before answer: %v", err)
			}
			if err := eng.Resume(ctx, run, wf.Response{RequestID: pend[0].ID, Decision: "approved", By: "human:kyle"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("node B never ran")
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.wantErr == nil {
				if consumeErr != nil {
					t.Fatalf("consume: %v", consumeErr)
				}
				if err := apr.Consume(ctx, id, action); !errors.Is(err, security.ErrConsumed) {
					t.Fatalf("second consume: %v", err)
				}
				if err := eng.MarkConsumed(ctx, pend[0].ID); !errors.Is(err, wf.ErrRequestConsumed) {
					t.Fatalf("engine MarkConsumed after security Consume: %v", err)
				}
			} else if !errors.Is(consumeErr, tc.wantErr) {
				t.Fatalf("consume: %v, want %v", consumeErr, tc.wantErr)
			}
		})
	}
}

# Drydock: interfaces

Scope: the Go contracts between subsystems as implemented. Each lives in the package named; signatures here are abbreviated from the code and the code is authoritative (`go doc ./internal/<pkg>`). Brief section 19 was the starting point; deviations are noted.

## Events (`internal/events`)

```go
type Event struct{ Seq uint64; Time time.Time; Type, ProjectID, RunID, TaskID, AgentID string; Data json.RawMessage }
type Filter struct{ Types []string /* "workflow.*" prefix match */; ProjectID, RunID, TaskID, AgentID string }
type Bus interface {
    Publish(ctx context.Context, e Event) (seq uint64, err error)   // persists (group commit), then fans out
    Subscribe(f Filter) (<-chan Event, cancel func())               // channel closed (without cancel) = fell behind: Replay and resubscribe
    Replay(ctx context.Context, afterSeq uint64, f Filter, limit int) ([]Event, error)
}
// MaxEventData (256 KiB): a larger Data is stored as a stand-in {"truncated":true,"bytes":N,"head":"..."} (the first 32 KiB), and Data
// that is not JSON as {"invalid":true,...}; every event is stored, copied to each subscriber and sent to the window (audit 2026-10-16 L2).
func Watch(ctx, b Bus, f Filter, handle func(Event)) <-chan struct{}   // subscribes before it returns; after a slow-drop it replays what it missed (no gap, no duplicate); a panic in handle is contained
func Consume(ctx, b Bus, f Filter, handle func(Event))                 // Watch, then waits for ctx to end
```

A consumer that must see every event of a kind (the responder, presence, economics, the sandbox and agent watchers) reads through
`Watch`/`Consume`: a bare `Subscribe` loop ends silently when the bus drops a slow subscriber, and the consumer then stops for good.

## Workflow (`internal/workflow`)

```go
type NodeHandler func(ctx context.Context, in NodeInput) (NodeOutput, error)
type Middleware  func(next NodeHandler) NodeHandler
type StreamObserver interface{ OnStart; OnChunk; OnEnd; OnError }       // registered via Engine.Observe(ObserverFactory)
type TurnController interface{ CancelTurn() error; Send(ctx, msg string) error }   // agent runtime implements; watchdog steers

type Engine struct{ /* New(Config{DB, Bus, DataDir, Owner, LeaseTTL, SafePoint}) */ }
func (*Engine) Register(*Graph) error
func (*Engine) Use(kind NodeKind, ...Middleware);   func (*Engine) Observe(ObserverFactory)
func (*Engine) Start(ctx, workflow string, input any, StartOptions) (RunID, error)
func (*Engine) Resume(ctx, RunID, Response) error                  // answers a pending request; exactly once
func (*Engine) ResumeFrom(ctx, CheckpointID) (RunID, error)        // time travel: a new lineage branch
func (*Engine) Replay(ctx, RunID) (RunID, error);  Cancel;  Status;  Await;  Recover(ctx) (int, error)
func (*Engine) Checkpoints / RunCheckpoints / Executions / Approvals / Pending / MarkConsumed / SafePointRef
```

Deviation from the brief: `Orchestrator`/`CheckpointStore` are not separate interfaces yet; the engine owns a private SQLite store, which is the only implementation. `Resume` takes a `Response{RequestID, Decision, Data, By}`.

## Agents (`internal/agents`)

```go
type Adapter interface {
    ID() string
    Detect(ctx) (Detection, error)               // installed, version, auth, status, install hint (never executed)
    Capabilities() Capabilities                  // verified capabilities only; false = not verified
    Start(ctx, proc.Manager, RunSpec) (Session, error)
}
type Session interface {
    ID() string; PID() int
    Send(ctx, msg string) error
    Output() <-chan workflow.Chunk               // normalized: text, thinking, message_end, tool_call, tool_result, permission_request, permission_denied,
                                                 // plan, usage, session, rate_limit, log, error, done (internal/workflow/chunks.go names each payload)
    Interrupt(ctx) error                         // protocol-level cancel; the session stays alive
    Stop(ctx) error                              // kills the process tree
    Wait() <-chan proc.ExitInfo;  Stderr() string
}
```

`internal/agents/runtime`: `Runtime{PM, Bus, DB, Adapters}`; `Spawn(ctx, Config) (*Managed, error)`; `(*Managed).RunTurn(ctx, prompt, *Controller, emit) (TurnResult, error)`; `Runtime.AgentTask(TaskSpec) workflow.NodeHandler`.

## Activity (`internal/agui`)

```go
func Project(events []events.Event) Activity          // one thread per agent process, one run per turn; pure, computed on read
func CheckOrder(events []Event) error                 // the ordering rules of AG-UI's reference verifier (@ag-ui/client 1.0.3 verify.ts) on one thread
func TypeScript() (string, error);  const TSPath      // the UI's types (ui/app/api/agui.gen.ts): "go run ./cmd/drydock-tsgen"; a test fails while stale
// core method task.activity {taskId} -> Activity{threads: [{id, agent, adapter, model, runMode, wslDistro, ended?, events: [AguiEvent]}], truncated}
```

The vocabulary is modelled on AG-UI 1.0 (decisions P87, P88, P100; `docs/agui-review-2026-10-25.md`): RUN_STARTED (with
`protocolVersion` "1.0"), RUN_FINISHED (`outcome` success or cancelled) and RUN_ERROR; TEXT_MESSAGE_* and REASONING_MESSAGE_*
(role `reasoning`); TOOL_CALL_START, ARGS, END and RESULT; ACTIVITY_SNAPSHOT for an ACP plan; CUSTOM `drydock.interrupt`,
`drydock.resume` and `drydock.decided` for a permission request, its answer, and a refusal or a rule's decision that asked nobody.
Drydock's own fields (`seq`, `stopReason`, `isError`, the ids on RUN_ERROR) ride in `metadata.drydock`, the field the protocol keeps
for them, so the published client strips nothing. It reads the stored `agent.started`, `agent.waiting`, `agent.output`,
`agent.completed`, `agent.failed`, `agent.stopped`, `approval.requested` and `permission.decided` events of the task (the gate's
events carry the agent run as their run id) and the newest 20000 of them; arguments and results are cut to 8 KiB (results keep
their head and tail, arguments stay valid JSON). Whether a turn failed is `workflow.TurnFailed`, the one rule the chat reads too.

## Process manager (`internal/proc`)

```go
type Manager interface{ Start(ctx, Spec) (Proc, error) }
type Proc interface{ PID() int; Stdin() io.WriteCloser; Stdout(), Stderr() io.Reader; Signal(Signal) error; Kill(tree bool) error; Wait() <-chan ExitInfo }
```

`Signal(Interrupt)` returns `ErrNotSupported` in the GUI host (no console); protocol-level cancel is the supported path.

## Tasks, git, checks, context

```go
tasks.Service{DB, Bus, Hooks}: Create, Get, List, Patch, Transition(ctx, id, to, TransitionOpts{By, Reason, Feedback}),
    AddEvidence, Evidence, AddReview, Reviews, PromoteReady, DeadLetter
git.Repo: Open/Init, CreateWorktree (idempotent), Status, Diff, DiffStat, DiffHash, Commit, Merge(strategy), Rebase,
    DeleteWorktree(force), DeleteBranch, SafePoint, RestoreSafePoint, LocalIgnore
checks.Run(ctx, proc.Manager, Spec, onLine) (Result, error);  checks.Detect(dir) []Spec
context.Engine{DB, Memory, Standing}: Generate, Write, SaveManifest, LoadManifest;  memory.Store: Add, SetStatus, List, Search
pipeline.Deps + Prepare/Work/Verify/Finalize/Review/Approve/Changes/Merge nodes; TaskPipeline, IterateUntilVerified graphs
watchdog.New(Store, Hooks, cfg).{Observer(), Middleware(), CheckMiddleware(diffHash), Reset(ctx, task)}
```

## In progress (contracts defined by the specs, being implemented)

`providers` (Registry, QuotaLedger), `llm` (LLMClient with exact-match cache and the `trainable` guard), `router` (ModelRouter, RoutingDecision), `security` (ActionDescriptor/Approvals, path guard, Analyzer, Scanner, audit chain), `room` (Service, presence Tracker, turn-taking), `mcp` (Gateway, workspace server, catalog), `agents/acp` and `connections`. See their docs: [`model-routing.md`](model-routing.md), [`security.md`](security.md), [`room-protocol.md`](room-protocol.md), [`mcp.md`](mcp.md), [`connections.md`](connections.md).

## Open questions / unverified

- `GitHost` and `Integration` interfaces ([`github.md`](github.md), [`integrations.md`](integrations.md)) are specified but not yet implemented.

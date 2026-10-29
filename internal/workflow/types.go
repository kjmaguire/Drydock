// Package workflow is Drydock's orchestration engine: supersteps, typed request ports, checkpoints with lineage,
// exactly-once node effects and middleware, on SQLite. It has no dependency on Wails or on any agent provider.
// See docs/workflow-engine.md for the contract these types implement.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type (
	RunID        string
	NodeID       string
	CheckpointID string
	RequestID    string
)

// NodeKind is the kind of work a node performs.
type NodeKind string

const (
	KindAgentTask   NodeKind = "agent_task"
	KindCheck       NodeKind = "check"
	KindRequest     NodeKind = "request"
	KindRoute       NodeKind = "route"
	KindSubworkflow NodeKind = "subworkflow"
	// KindGroup is a group-collaboration node (brief s4, decision P118): a bounded discussion between named agents in a room,
	// each turn granted by the room's scheduler. Its handler comes from the package that runs room turns
	// (responder.GroupNode); the engine treats it as any other node.
	KindGroup NodeKind = "group"
)

// EdgeKind is how an edge delivers messages.
type EdgeKind string

const (
	EdgeDirect      EdgeKind = "direct"
	EdgeConditional EdgeKind = "conditional"
	EdgeFanOut      EdgeKind = "fanout"
	EdgeFanIn       EdgeKind = "fanin"
	EdgeLoop        EdgeKind = "loop"
)

// RunStatus is the lifecycle state of a run (brief section 12).
type RunStatus string

const (
	StatusPending      RunStatus = "PENDING"
	StatusRunning      RunStatus = "RUNNING"
	StatusPaused       RunStatus = "PAUSED"
	StatusWaitingHuman RunStatus = "WAITING_HUMAN"
	StatusRecovering   RunStatus = "RECOVERING"
	StatusCompleted    RunStatus = "COMPLETED"
	StatusFailed       RunStatus = "FAILED"
	StatusCancelled    RunStatus = "CANCELLED"
)

// Terminal reports whether no further progress is possible.
func (s RunStatus) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// CheckpointKind distinguishes how a checkpoint was produced.
type CheckpointKind string

const (
	CheckpointEntry         CheckpointKind = "entry"
	CheckpointStep          CheckpointKind = "step"
	CheckpointResponseEntry CheckpointKind = "response_entry"
)

// Message travels along an edge to a node's inbox.
type Message struct {
	From      NodeID          `json:"from"`
	Value     json.RawMessage `json:"value,omitempty"`
	Iteration int             `json:"iteration,omitempty"`
}

// Chunk is one piece of a streaming node output (an agent text delta, a tool call, a check log line).
type Chunk struct {
	Kind string          `json:"kind"` // text, thinking, tool_call, tool_result, usage, log, ...
	Data json.RawMessage `json:"data,omitempty"`
}

// RequestSpec describes a human request port.
type RequestSpec struct {
	Kind       string          `json:"kind"` // approval | question | review
	Title      string          `json:"title"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ActionHash string          `json:"actionHash,omitempty"` // approvals are bound to the exact action
	// Phase says which gate a review request is: "spec" (accept the spec), "merge" (review and merge) or "plan"
	// (review a plan). It is stored with the
	// request (approvals.phase), so the inbox tells them apart without reading the title.
	Phase string `json:"phase,omitempty"`
	// Retry makes the node run again after the human answers, instead of completing with the answer as its output. The
	// node's original inbox is restored, so the second execution sees what the first saw. A gate in front of an agent
	// turn (the budget gate) uses it: the turn is still owed once the human has decided. Whatever the decision, the node
	// runs again and reads the decision itself (the approvals row), so a rejection is the node's to act on.
	Retry bool `json:"retry,omitempty"`
	// DeliverAnswer (with Retry) appends the human's answer {"decision","data","by","request"} to the restored inbox as one
	// more message, so the node that runs again sees what the human said. The watchdog's pause uses it: the human's guidance
	// reaches the agent's next turn, and a rejection is the node's to act on.
	DeliverAnswer bool `json:"deliverAnswer,omitempty"`
}

// NodeOutput is what a handler returns: a complete value, optionally a routing label, or a request.
type NodeOutput struct {
	Value   json.RawMessage `json:"value,omitempty"`
	Route   string          `json:"route,omitempty"`
	Request *RequestSpec    `json:"request,omitempty"`
	// Halt ends this branch of the run: the output is recorded but routed to no edge, not even an unconditional one. A node
	// that stopped the task (a human rejected a watchdog pause) uses it so the run completes cleanly instead of carrying on.
	Halt bool `json:"halt,omitempty"`
	// ResetLoops clears every loop counter of the run before the output is routed, so the loop budget starts again. A human
	// (or the team lead) continuing past an iteration limit uses it.
	ResetLoops bool `json:"resetLoops,omitempty"`
}

// Previous carries what an earlier, interrupted attempt of the same node left behind.
type Previous struct {
	Attempt    int
	PartialRef string // path of the chunk file the earlier attempt wrote (may be empty)
	Error      string
}

// TurnController lets middleware (the watchdog) steer a running agent turn without importing the agent runtime.
type TurnController interface {
	CancelTurn() error
	Send(ctx context.Context, msg string) error
}

// TurnSlot is a rendezvous point: the agent handler registers its TurnController; middleware reads it.
type TurnSlot struct {
	mu sync.Mutex
	tc TurnController
}

func (s *TurnSlot) Set(tc TurnController) { s.mu.Lock(); s.tc = tc; s.mu.Unlock() }
func (s *TurnSlot) Get() TurnController   { s.mu.Lock(); defer s.mu.Unlock(); return s.tc }

// NodeInfo identifies a node execution for observers and middleware.
type NodeInfo struct {
	RunID     RunID
	ProjectID string
	TaskID    string
	Workflow  string
	NodeID    NodeID
	Kind      NodeKind
	Step      int
	Attempt   int
	Key       string // idempotency key: run/node/step/attempt
	Meta      map[string]string
}

// NodeInput is everything a handler may read. Handlers must be deterministic functions of it plus their side effects.
type NodeInput struct {
	NodeInfo
	Input    json.RawMessage            // the run input
	Inbox    []Message                  // messages delivered for this step
	Shared   map[string]json.RawMessage // snapshot of shared state ("out/<node>")
	Previous *Previous                  // non-nil when retrying an interrupted attempt
	Turn     *TurnSlot
	// Emit streams a chunk to the UI/observers and the durable chunk file. Safe for concurrent use.
	Emit func(Chunk)
}

// NodeHandler executes a node.
type NodeHandler func(ctx context.Context, in NodeInput) (NodeOutput, error)

// Middleware wraps a handler (brief section 4).
type Middleware func(next NodeHandler) NodeHandler

// StreamObserver receives stream-aware hooks. A Middleware factory may return one to be registered per execution.
type StreamObserver interface {
	OnStart(ctx context.Context, n NodeInfo)
	OnChunk(ctx context.Context, n NodeInfo, c Chunk)
	OnEnd(ctx context.Context, n NodeInfo, out NodeOutput)
	OnError(ctx context.Context, n NodeInfo, err error)
}

// Node is a vertex of a Graph.
type Node struct {
	ID          NodeID
	Kind        NodeKind
	Handler     NodeHandler
	Request     *RequestSpec // static request for request nodes with no handler
	Sub         *Graph       // for subworkflow nodes
	FanIn       []NodeID     // for barrier nodes: all sources must deliver before the node runs
	MaxAttempts int          // total attempts including the first (default 1)
	Meta        map[string]string
}

// Edge connects two nodes.
type Edge struct {
	From, To    NodeID
	Kind        EdgeKind
	When        string                    // matches NodeOutput.Route (empty: any)
	Cond        func(out NodeOutput) bool // optional extra predicate on the upstream output
	MaxIter     int                       // loop edges: maximum number of traversals
	OnExhausted NodeID                    // loop edges: where to go once MaxIter is reached
}

// Graph is a workflow definition. Graphs are code, never serialized.
type Graph struct {
	Name  string
	Nodes map[NodeID]*Node
	Edges []Edge
	Entry []NodeID
	order []NodeID
}

// Errors.
var (
	ErrLeaseHeld        = errors.New("workflow: run lease held by another owner")
	ErrRequestConsumed  = errors.New("workflow: request already answered")
	ErrNotWaiting       = errors.New("workflow: run is not waiting for a response")
	ErrTopologyMismatch = errors.New("workflow: graph topology differs from the checkpoint")
	ErrUnknownWorkflow  = errors.New("workflow: unknown workflow")
	ErrLoopExhausted    = errors.New("workflow: loop iteration limit reached")
	ErrNoRoute          = errors.New("workflow: the workflow does not go on from this step with that answer")
	ErrRunTerminal      = errors.New("workflow: run is in a terminal state")
	ErrEngineClosed     = errors.New("workflow: the engine is closed")
)

// ErrWatchdogTrip is returned by a node cancelled by the watchdog.
type ErrWatchdogTrip struct {
	Signal   string
	Stage    int
	Evidence string
}

func (e ErrWatchdogTrip) Error() string {
	return fmt.Sprintf("watchdog tripped (%s, stage %d): %s", e.Signal, e.Stage, e.Evidence)
}

// Response is a human's answer to a pending request.
type Response struct {
	RequestID RequestID       `json:"requestId"`
	Decision  string          `json:"decision"` // approved | rejected | answered
	Data      json.RawMessage `json:"data,omitempty"`
	By        string          `json:"by,omitempty"`
}

// PendingRequest is a request waiting for a human, stored in the checkpoint.
type PendingRequest struct {
	ID     RequestID   `json:"id"`
	NodeID NodeID      `json:"nodeId"`
	Step   int         `json:"step"`
	Spec   RequestSpec `json:"spec"`
	// Inbox is the parked node's inbox, kept only for Spec.Retry requests.
	Inbox []Message `json:"inbox,omitempty"`
}

// EdgeKey identifies an edge for loop counting.
func EdgeKey(from, to NodeID) string { return string(from) + "->" + string(to) }

// Checkpoint is a persisted snapshot.
type Checkpoint struct {
	ID       CheckpointID   `json:"id"`
	RunID    RunID          `json:"runId"`
	Workflow string         `json:"workflow"`
	Step     int            `json:"step"`
	Kind     CheckpointKind `json:"kind"`
	Parent   CheckpointID   `json:"parent,omitempty"`
	State    RunState       `json:"state"`
	Created  int64          `json:"created"`
}

// RunState is the serialized state carried in a checkpoint.
type RunState struct {
	Step         int                          `json:"step"`
	Status       RunStatus                    `json:"status"`
	Input        json.RawMessage              `json:"input,omitempty"`
	Inboxes      map[NodeID][]Message         `json:"inboxes"`
	Barriers     map[NodeID][]NodeID          `json:"barriers"`
	Shared       map[string]json.RawMessage   `json:"shared"`
	Pending      map[RequestID]PendingRequest `json:"pending"`
	Waiting      map[NodeID]RequestID         `json:"waiting"`
	LoopCounts   map[string]int               `json:"loopCounts"`
	Topology     string                       `json:"topology"`
	LastNodes    []NodeID                     `json:"lastNodes,omitempty"` // nodes executed in the latest superstep
	Result       json.RawMessage              `json:"result,omitempty"`
	FailedReason string                       `json:"failedReason,omitempty"`
}

func newState(input json.RawMessage, topo string) RunState {
	return RunState{
		Status: StatusRunning, Input: input,
		Inboxes: map[NodeID][]Message{}, Barriers: map[NodeID][]NodeID{},
		Shared: map[string]json.RawMessage{}, Pending: map[RequestID]PendingRequest{},
		Waiting: map[NodeID]RequestID{}, LoopCounts: map[string]int{}, Topology: topo,
	}
}

func (s RunState) clone() RunState {
	b, _ := json.Marshal(s)
	var c RunState
	_ = json.Unmarshal(b, &c)
	if c.Inboxes == nil {
		c.Inboxes = map[NodeID][]Message{}
	}
	if c.Barriers == nil {
		c.Barriers = map[NodeID][]NodeID{}
	}
	if c.Shared == nil {
		c.Shared = map[string]json.RawMessage{}
	}
	if c.Pending == nil {
		c.Pending = map[RequestID]PendingRequest{}
	}
	if c.Waiting == nil {
		c.Waiting = map[NodeID]RequestID{}
	}
	if c.LoopCounts == nil {
		c.LoopCounts = map[string]int{}
	}
	return c
}

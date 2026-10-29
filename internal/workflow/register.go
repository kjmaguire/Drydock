package workflow

import (
	"errors"
	"fmt"
	"sort"
)

// ErrGraphChanged is returned by RegisterNew when a different graph is already registered under the name.
var ErrGraphChanged = errors.New("workflow: a graph with a different topology is already registered under this name")

// RegisterNew registers a graph while the engine is already running. Feature areas (templates, the planner) add their
// graphs from extensions after core.Open has started the engine, and runs of an earlier graph may be in flight.
//
// It differs from Register in one safety rule: it never replaces a registered graph whose topology differs. Checkpoints
// carry the topology hash and Resume, ResumeFrom and Recover refuse a mismatch (ErrTopologyMismatch), so silently
// swapping the graph under a waiting run would strand it. Registering a graph with the identical topology is allowed and
// only swaps the node handlers (a new closure over fresh dependencies); a name that is not yet registered is added.
// It is safe to call concurrently with Start, Resume and Recover.
func (e *Engine) RegisterNew(g *Graph) error {
	if g == nil {
		return errors.New("workflow: nil graph")
	}
	if err := g.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("workflow: engine is closed")
	}
	subs, err := nestedGraphs(g)
	if err != nil {
		return err
	}
	for _, s := range append([]*Graph{g}, subs...) {
		if old, ok := e.graphs[s.Name]; ok && old.TopologyHash() != s.TopologyHash() {
			return fmt.Errorf("%w: %q", ErrGraphChanged, s.Name)
		}
	}
	e.graphs[g.Name] = g
	for _, s := range subs {
		e.graphs[s.Name] = s
	}
	return nil
}

// nestedGraphs are the graphs of g's subworkflow nodes, at any depth, validated. They are registered with g (decision P124):
// a nested run outlives its parent's step (it waits for a person, or works while Drydock restarts), so the engine that
// recovers it or records its answer must know its graph before any parent step runs again.
func nestedGraphs(g *Graph) ([]*Graph, error) {
	var out []*Graph
	seen := map[*Graph]bool{g: true}
	var walk func(g *Graph, depth int) error
	walk = func(g *Graph, depth int) error {
		for _, id := range g.order {
			s := g.Nodes[id].Sub
			if s == nil || seen[s] {
				continue
			}
			if depth > 8 {
				return fmt.Errorf("workflow %s: subworkflows nested too deep", g.Name)
			}
			if err := s.Validate(); err != nil {
				return err
			}
			seen[s] = true
			out = append(out, s)
			if err := walk(s, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(g, 0)
}

// Registered reports whether a graph name is known to the engine.
func (e *Engine) Registered(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.graphs[name]
	return ok
}

// Workflows lists the names of the registered graphs, sorted (a workflow budget names one of them, decision P118).
func (e *Engine) Workflows() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.graphs))
	for name := range e.graphs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// UseOutermost prepends middleware for nodes of a kind, so it wraps every middleware registered before it. Use appends
// (inner); this is for a wrapper that must run before the others start their clocks: the agent-slot limiter waits for a
// free slot here, so the time a turn spends queued does not count against the watchdog's per-turn wall-clock cap, which
// starts when the watchdog middleware is entered.
func (e *Engine) UseOutermost(kind NodeKind, mw ...Middleware) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mw[kind] = append(append([]Middleware(nil), mw...), e.mw[kind]...)
}

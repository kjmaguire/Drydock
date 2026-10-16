package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// NewGraph starts a builder.
func NewGraph(name string) *Graph {
	return &Graph{Name: name, Nodes: map[NodeID]*Node{}}
}

// Add registers nodes in declaration order.
func (g *Graph) Add(nodes ...*Node) *Graph {
	for _, n := range nodes {
		g.Nodes[n.ID] = n
		g.order = append(g.order, n.ID)
	}
	return g
}

// Edge adds a direct edge.
func (g *Graph) Edge(from, to NodeID) *Graph {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: EdgeDirect})
	return g
}

// EdgeWhen adds a conditional edge that fires when the upstream output's Route equals when.
func (g *Graph) EdgeWhen(from, to NodeID, when string) *Graph {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: EdgeConditional, When: when})
	return g
}

// EdgeIf adds a conditional edge with a predicate.
func (g *Graph) EdgeIf(from, to NodeID, cond func(NodeOutput) bool) *Graph {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: EdgeConditional, Cond: cond})
	return g
}

// Loop adds a back edge traversed at most max times; afterwards messages go to onExhausted (may be empty: the run fails).
func (g *Graph) Loop(from, to NodeID, when string, max int, onExhausted NodeID) *Graph {
	g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: EdgeLoop, When: when, MaxIter: max, OnExhausted: onExhausted})
	return g
}

// Start declares entry nodes.
func (g *Graph) Start(ids ...NodeID) *Graph {
	g.Entry = append(g.Entry, ids...)
	return g
}

// AgentTask, Check, Request, Route are node constructors.
func AgentTask(id NodeID, h NodeHandler) *Node { return &Node{ID: id, Kind: KindAgentTask, Handler: h} }
func Check(id NodeID, h NodeHandler) *Node     { return &Node{ID: id, Kind: KindCheck, Handler: h} }
func Route(id NodeID, h NodeHandler) *Node     { return &Node{ID: id, Kind: KindRoute, Handler: h} }
func Request(id NodeID, spec RequestSpec) *Node {
	return &Node{ID: id, Kind: KindRequest, Request: &spec}
}

// Join makes n a fan-in barrier over sources.
func (n *Node) Join(sources ...NodeID) *Node { n.FanIn = append(n.FanIn, sources...); return n }

// Retries sets the total attempt count.
func (n *Node) Retries(total int) *Node { n.MaxAttempts = total; return n }

// With sets metadata.
func (n *Node) With(k, v string) *Node {
	if n.Meta == nil {
		n.Meta = map[string]string{}
	}
	n.Meta[k] = v
	return n
}

// Sequential builds a linear graph from nodes.
func Sequential(name string, nodes ...*Node) *Graph {
	g := NewGraph(name).Add(nodes...)
	for i := 0; i+1 < len(nodes); i++ {
		g.Edge(nodes[i].ID, nodes[i+1].ID)
	}
	if len(nodes) > 0 {
		g.Start(nodes[0].ID)
	}
	return g
}

// Validate checks structural rules from docs/workflow-engine.md section 2.
func (g *Graph) Validate() error {
	if g.Name == "" {
		return fmt.Errorf("workflow: graph has no name")
	}
	if len(g.Entry) == 0 {
		return fmt.Errorf("workflow %s: no entry node", g.Name)
	}
	for _, e := range g.Entry {
		if _, ok := g.Nodes[e]; !ok {
			return fmt.Errorf("workflow %s: unknown entry node %q", g.Name, e)
		}
	}
	for id, n := range g.Nodes {
		if n.ID != id {
			return fmt.Errorf("workflow %s: node key/id mismatch %q", g.Name, id)
		}
		switch n.Kind {
		case KindRequest:
			if n.Request == nil && n.Handler == nil {
				return fmt.Errorf("workflow %s: request node %q needs a RequestSpec or handler", g.Name, id)
			}
		case KindSubworkflow:
			if n.Sub == nil {
				return fmt.Errorf("workflow %s: subworkflow node %q has no graph", g.Name, id)
			}
		default:
			if n.Handler == nil {
				return fmt.Errorf("workflow %s: node %q has no handler", g.Name, id)
			}
		}
		for _, src := range n.FanIn {
			if _, ok := g.Nodes[src]; !ok {
				return fmt.Errorf("workflow %s: fan-in %q lists unknown source %q", g.Name, id, src)
			}
		}
	}
	for _, e := range g.Edges {
		if _, ok := g.Nodes[e.From]; !ok {
			return fmt.Errorf("workflow %s: edge from unknown node %q", g.Name, e.From)
		}
		if _, ok := g.Nodes[e.To]; !ok {
			return fmt.Errorf("workflow %s: edge to unknown node %q", g.Name, e.To)
		}
		if e.Kind == EdgeLoop {
			if e.MaxIter <= 0 {
				return fmt.Errorf("workflow %s: loop edge %s->%s needs MaxIter > 0", g.Name, e.From, e.To)
			}
			if e.OnExhausted != "" {
				if _, ok := g.Nodes[e.OnExhausted]; !ok {
					return fmt.Errorf("workflow %s: loop edge exhausted target %q unknown", g.Name, e.OnExhausted)
				}
			}
		}
	}
	return nil
}

// TopologyHash is a stable digest of node ids, kinds and edges; checkpoints record it and resume checks it.
func (g *Graph) TopologyHash() string {
	var parts []string
	for id, n := range g.Nodes {
		fan := make([]string, 0, len(n.FanIn))
		for _, f := range n.FanIn {
			fan = append(fan, string(f))
		}
		sort.Strings(fan)
		parts = append(parts, fmt.Sprintf("n:%s:%s:%s", id, n.Kind, strings.Join(fan, ",")))
	}
	for _, e := range g.Edges {
		parts = append(parts, fmt.Sprintf("e:%s>%s:%s:%s:%d:%s", e.From, e.To, e.Kind, e.When, e.MaxIter, e.OnExhausted))
	}
	for _, e := range g.Entry {
		parts = append(parts, "s:"+string(e))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(g.Name + "\n" + strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func (g *Graph) outEdges(from NodeID) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

// routesOf lists the routes the out-edges of a node are labelled with (the answers a request step goes on for), then "rejected",
// which a step may always be answered with: it ends the run when no edge takes it.
func (g *Graph) routesOf(from NodeID) []string {
	seen := map[string]bool{"rejected": true}
	out := []string{}
	for _, e := range g.outEdges(from) {
		if e.When != "" && !seen[e.When] {
			seen[e.When] = true
			out = append(out, e.When)
		}
	}
	sort.Strings(out)
	return append(out, "rejected")
}

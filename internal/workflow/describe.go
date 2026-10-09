package workflow

// GraphView is a graph's topology in a form the UI can draw (docs/ui.md 6.2). Handlers and predicates are code and are left
// out; a subworkflow node carries its nested view.
type GraphView struct {
	Name  string     `json:"name"`
	Nodes []NodeView `json:"nodes"`
	Edges []EdgeView `json:"edges"`
	Entry []NodeID   `json:"entry"`
}

// NodeView is one node of a GraphView, in declaration order.
type NodeView struct {
	ID          NodeID            `json:"id"`
	Kind        NodeKind          `json:"kind"`
	MaxAttempts int               `json:"maxAttempts,omitempty"`
	FanIn       []NodeID          `json:"fanIn,omitempty"`
	Request     string            `json:"request,omitempty"` // the kind of a static request
	Meta        map[string]string `json:"meta,omitempty"`
	Sub         *GraphView        `json:"sub,omitempty"`
}

// EdgeView is one edge of a GraphView.
type EdgeView struct {
	From        NodeID   `json:"from"`
	To          NodeID   `json:"to"`
	Kind        EdgeKind `json:"kind"`
	When        string   `json:"when,omitempty"`
	Conditional bool     `json:"conditional,omitempty"` // a predicate beyond When decides it
	MaxIter     int      `json:"maxIter,omitempty"`
	OnExhausted NodeID   `json:"onExhausted,omitempty"`
}

// Describe returns the topology of a registered workflow.
func (e *Engine) Describe(name string) (GraphView, error) {
	g, err := e.graph(name)
	if err != nil {
		return GraphView{}, err
	}
	return describe(g), nil
}

func describe(g *Graph) GraphView {
	v := GraphView{Name: g.Name, Entry: append([]NodeID(nil), g.Entry...), Nodes: []NodeView{}, Edges: []EdgeView{}}
	order := g.order
	if len(order) == 0 {
		for id := range g.Nodes {
			order = append(order, id)
		}
	}
	for _, id := range order {
		n := g.Nodes[id]
		if n == nil {
			continue
		}
		nv := NodeView{ID: n.ID, Kind: n.Kind, MaxAttempts: n.MaxAttempts, FanIn: n.FanIn, Meta: n.Meta}
		if n.Request != nil {
			nv.Request = n.Request.Kind
		}
		if n.Sub != nil {
			sub := describe(n.Sub)
			nv.Sub = &sub
		}
		v.Nodes = append(v.Nodes, nv)
	}
	for _, ed := range g.Edges {
		v.Edges = append(v.Edges, EdgeView{From: ed.From, To: ed.To, Kind: ed.Kind, When: ed.When, Conditional: ed.Cond != nil, MaxIter: ed.MaxIter, OnExhausted: ed.OnExhausted})
	}
	return v
}

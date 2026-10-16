package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/libver"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/skills"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Recommendations (decision P52). One read-only agent turn (the project's team lead unless the person names another agent)
// reads the project and what the workspace offers and says which agent should get which skill, MCP server or integration, and
// why. The core checks every item against what exists (a skill in the project or the library, a connected server, a
// connection, the project's agents) and drops the rest with the reason; the person applies or dismisses each item. Nothing is
// applied by the agent. The project asks again by itself as it evolves: after recommendEvery completed tasks since the last
// turn, at most once per recommendGap, within the budgets, unless the person switched that off.

const (
	recommendEvery       = 5
	recommendGap         = 24 * time.Hour
	recommendTimeout     = 10 * time.Minute
	recommendMaxItems    = 20
	recommendMaxLibrary  = 250
	recommendDescLen     = 140
	settingRecommendAuto = "recommend.auto:"
)

// RecommendItem is one recommendation: give (or add, or stop using) a skill, an MCP server or an integration.
type RecommendItem struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`   // skill | mcp | integration
	Action     string   `json:"action"` // add (into the project) | give (to agents) | remove (stop using)
	Name       string   `json:"name"`   // skill name, library MCP server id, integration kind
	Label      string   `json:"label"`  // what a person reads (a server's display name, the vendor's name)
	RegistryID string   `json:"registryId,omitempty"`
	Agents     []string `json:"agents"` // agent ids; empty means the whole team (skills) or the project (integrations)
	Reason     string   `json:"reason"`
	Status     string   `json:"status"` // open | applied | dismissed
	Note       string   `json:"note,omitempty"`
}

// RecommendCandidate is a library skill the person picked to add (skills_added).
type RecommendCandidate struct {
	RegistryID string `json:"registryId"`
	Name       string `json:"name"`
}

// Recommendation is one turn's result.
type Recommendation struct {
	ID         string               `json:"id"`
	ProjectID  string               `json:"projectId"`
	AgentID    string               `json:"agentId"`
	Trigger    string               `json:"trigger"`
	Status     string               `json:"status"`
	Candidates []RecommendCandidate `json:"candidates"`
	Summary    string               `json:"summary"`
	Items      []RecommendItem      `json:"items"`
	Dropped    []string             `json:"dropped"`
	Error      string               `json:"error,omitempty"`
	CostUSD    float64              `json:"costUsd"`
	Created    int64                `json:"createdAt"`
	Finished   int64                `json:"finishedAt,omitempty"`
}

// Recommendations is a project's recent turns, newest first, and whether the project asks again by itself.
type Recommendations struct {
	Auto bool             `json:"auto"`
	Runs []Recommendation `json:"runs"`
}

// recommendState is the extension's per-core state: the context turns run under (they end with the core), the turns still
// running (Close waits for them to record how they ended), and the lock that makes applying or dismissing an item one step.
type recommendState struct {
	ctx  context.Context
	runs sync.WaitGroup
	mu   sync.Mutex
}

var recommendStates sync.Map // *Core -> *recommendState

// recommendInterrupted is the reason a turn the app's end cut short is recorded with.
const recommendInterrupted = "interrupted: Drydock was closed while the agent was answering. Ask again."

// recommendCloseWait bounds how long Close waits for running turns to record how they ended.
const recommendCloseWait = 15 * time.Second

func (c *Core) recommendState() *recommendState {
	if v, ok := recommendStates.Load(c); ok {
		return v.(*recommendState)
	}
	v, _ := recommendStates.LoadOrStore(c, &recommendState{ctx: context.Background()})
	return v.(*recommendState)
}

// ErrRecommendRunning is returned while a turn for the project is still running.
var ErrRecommendRunning = errors.New("a recommendation is already being prepared for this project")

func init() {
	RegisterExtension(Extension{Name: "recommend", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		ctx, cancel := context.WithCancel(ctx)
		st := &recommendState{ctx: ctx} // turns end with the core
		recommendStates.Store(c, st)
		// a turn the app's end or a crash cut short is still "running" in the database, and would refuse every later turn of
		// its project: nothing runs yet, so every such row is one that ended without a word
		if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE recommendations SET status='failed', error=?, finished_at=? WHERE status='running'`, recommendInterrupted, storage.Now())
			return err
		}); err != nil {
			cancel()
			return nil, nil, err
		}
		// the project evolves: completed tasks may call for another look (bounded; see maybeRecommend)
		events.Watch(ctx, c.Bus, events.Filter{Types: []string{"task.completed"}}, func(ev events.Event) {
			if ev.ProjectID != "" {
				c.maybeRecommend(ctx, ev.ProjectID)
			}
		})
		methods := map[string]Method{
			"recommend.start": bind(func(ctx context.Context, p struct {
				ProjectID  string               `json:"projectId"`
				AgentID    string               `json:"agentId"`
				Candidates []RecommendCandidate `json:"candidates"`
			}) (any, error) {
				trigger := "manual"
				if len(p.Candidates) > 0 {
					trigger = "skills_added"
				}
				return c.Recommend(ctx, p.ProjectID, p.AgentID, trigger, p.Candidates)
			}),
			"recommend.list": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				return c.RecommendList(ctx, p.ProjectID)
			}),
			"recommend.apply": bind(func(ctx context.Context, p struct {
				ID     string `json:"id"`
				ItemID string `json:"itemId"`
			}) (any, error) {
				return c.RecommendApply(ctx, p.ID, p.ItemID)
			}),
			"recommend.dismiss": bind(func(ctx context.Context, p struct {
				ID     string `json:"id"`
				ItemID string `json:"itemId"`
			}) (any, error) {
				st := c.recommendState()
				st.mu.Lock()
				defer st.mu.Unlock()
				return c.recommendMark(ctx, p.ID, p.ItemID, "dismissed", "")
			}),
			"recommend.setAuto": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
				On        bool   `json:"on"`
			}) (any, error) {
				if _, err := c.GetProject(ctx, p.ProjectID); err != nil {
					return nil, err
				}
				v := "off"
				if p.On {
					v = "on"
				}
				if err := c.setSetting(ctx, settingRecommendAuto+p.ProjectID, v); err != nil {
					return nil, err
				}
				return c.RecommendList(ctx, p.ProjectID)
			}),
		}
		// the turns stop with the context and record how they ended before the database closes
		closeFn := func() {
			cancel()
			done := make(chan struct{})
			safe.Go("core.init#2", func() { st.runs.Wait(); close(done) })
			select {
			case <-done:
			case <-time.After(recommendCloseWait):
				c.logf("recommend: a turn did not end within %s of closing; it is recorded as interrupted at the next start", recommendCloseWait)
			}
		}
		return methods, closeFn, nil
	}})
}

// recommendAuto says whether the project asks again by itself (on unless the person switched it off).
func (c *Core) recommendAuto(ctx context.Context, projectID string) bool {
	var v string
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingRecommendAuto+projectID).Scan(&v)
	return v != "off"
}

// recommendAdvisor is the agent that answers: the one named, else the project's team lead.
func (c *Core) recommendAdvisor(ctx context.Context, projectID, agentID string) (Agent, []Agent, error) {
	team, err := c.Agents(ctx, projectID)
	if err != nil {
		return Agent{}, nil, err
	}
	var active []Agent
	for _, a := range team {
		if !a.Archived {
			active = append(active, a)
		}
	}
	for _, a := range active {
		if (agentID != "" && a.ID == agentID) || (agentID == "" && a.Role == pipeline.RoleLead) {
			return a, active, nil
		}
	}
	if agentID != "" {
		return Agent{}, nil, fmt.Errorf("agent %s is not on this project's team", agentID)
	}
	return Agent{}, nil, errors.New("this project has no team lead: choose which agent to ask")
}

// completedTasks counts the project's completed tasks.
func (c *Core) completedTasks(ctx context.Context, projectID string) int {
	var n int
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=? AND status=?`, projectID, string(tasks.Completed)).Scan(&n)
	return n
}

// maybeRecommend asks again for a project that evolved: auto is on, it has a lead, recommendEvery tasks were completed since
// the last turn and the last one is older than recommendGap. Errors (a budget used up, no lead) are not the person's to see
// here: the project simply does not ask.
func (c *Core) maybeRecommend(ctx context.Context, projectID string) {
	if !c.recommendAuto(ctx, projectID) {
		return
	}
	var done int
	var last int64
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT tasks_done, created_at FROM recommendations WHERE project_id=? ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&done, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if c.completedTasks(ctx, projectID)-done < recommendEvery {
		return
	}
	if last > 0 && time.Since(time.UnixMilli(last)) < recommendGap {
		return
	}
	if _, err := c.Recommend(ctx, projectID, "", "evolution", nil); err != nil && !errors.Is(err, ErrRecommendRunning) {
		c.logf("recommend %s: %v", projectID, err)
	}
}

// Recommend starts one recommendation turn and returns its row (status running); the result arrives as recommend.finished.
func (c *Core) Recommend(ctx context.Context, projectID, agentID, trigger string, candidates []RecommendCandidate) (Recommendation, error) {
	// a recommendation is a paid turn: while Drydock is read-only (license expired) neither a person's request nor the project's
	// own wish to ask again starts one (audit 2026-10-16, L1)
	if err := c.RequireLicensed(ctx, "start_work"); err != nil {
		return Recommendation{}, err
	}
	if _, err := c.GetProject(ctx, projectID); err != nil {
		return Recommendation{}, err
	}
	advisor, _, err := c.recommendAdvisor(ctx, projectID, agentID)
	if err != nil {
		return Recommendation{}, err
	}
	if err := c.leadBudget(ctx, projectID, "", advisor.ID); err != nil {
		return Recommendation{}, err
	}
	if len(candidates) > 50 {
		candidates = candidates[:50]
	}
	r := Recommendation{ID: ids.New("rec"), ProjectID: projectID, AgentID: advisor.ID, Trigger: trigger, Status: "running", Candidates: orEmpty(candidates),
		Items: []RecommendItem{}, Dropped: []string{}, Created: storage.Now()}
	cand, _ := json.Marshal(r.Candidates)
	done := c.completedTasks(ctx, projectID)
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM recommendations WHERE project_id=? AND status='running'`, projectID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrRecommendRunning
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO recommendations(id,project_id,agent_id,trigger,status,candidates,tasks_done,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			r.ID, projectID, advisor.ID, trigger, r.Status, string(cand), done, r.Created)
		return err
	})
	if err != nil {
		return Recommendation{}, err
	}
	c.publish(ctx, "recommend.started", projectID, map[string]any{"id": r.ID, "agent": advisor.ID, "trigger": trigger})
	st := c.recommendState()
	st.runs.Add(1)
	safe.Go("core.Core.Recommend", func() {
		defer st.runs.Done()
		c.runRecommend(r, advisor)
	})
	return r, nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// recommendWorld is what the turn is told and what its answer is checked against.
type recommendWorld struct {
	team      []Agent
	inProject map[string]skills.Skill          // project skills by name
	library   map[string][]skills.LibrarySkill // available library skills by name (several registries may share a name)
	servers   map[string]MCPLibraryServer      // connected library servers by id
	inUse     map[string]MCPServerView         // the project's servers by id
	conns     map[string]string                // connected integration kinds -> vendor name
}

func (c *Core) recommendWorld(ctx context.Context, projectID string) (recommendWorld, error) {
	w := recommendWorld{inProject: map[string]skills.Skill{}, library: map[string][]skills.LibrarySkill{}, servers: map[string]MCPLibraryServer{}, inUse: map[string]MCPServerView{}, conns: map[string]string{}}
	_, team, err := c.recommendAdvisor(ctx, projectID, "")
	if err != nil {
		team, err = c.Agents(ctx, projectID)
		if err != nil {
			return w, err
		}
	}
	w.team = team
	if k := c.Knowledge(); k != nil {
		mine, err := k.Skills.List(ctx, projectID)
		if err != nil {
			return w, err
		}
		for _, s := range mine {
			w.inProject[s.Name] = s
		}
		lib, err := k.Skills.LibraryList(ctx, "")
		if err != nil {
			return w, err
		}
		for _, l := range lib {
			if l.Status == "available" && !l.Gone {
				w.library[l.Name] = append(w.library[l.Name], l)
			}
		}
	}
	call := func(method string, params, out any) error {
		b, _ := json.Marshal(params)
		raw, err := c.Call(ctx, method, b)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	var libs []MCPLibraryServer
	if call("mcplib.list", struct{}{}, &libs) == nil {
		for _, s := range libs {
			w.servers[s.ID] = s
		}
	}
	var used []MCPServerView
	if call("mcpserver.list", map[string]string{"projectId": projectID}, &used) == nil {
		for _, s := range used {
			w.inUse[s.ID] = s
		}
	}
	var conns []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if call("integration.list", struct{}{}, &conns) == nil {
		for _, x := range conns {
			if x.Status != "disconnected" {
				w.conns[x.Kind] = x.Name
			}
		}
	}
	return w, nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// recommendPrompt is the turn's prompt. Every name the answer may use is listed in it.
func (c *Core) recommendPrompt(ctx context.Context, projectID string, w recommendWorld, candidates []RecommendCandidate) string {
	p, _ := c.GetProject(ctx, projectID)
	var b strings.Builder
	b.WriteString("Drydock phase: recommend\n\n")
	b.WriteString("You advise the person who runs this software project in Drydock. Recommend which agent of the team should get which skill, MCP server or integration, ")
	b.WriteString("based on what the project is building and what its agents do. Recommend only what helps the work; fewer good recommendations beat many. ")
	b.WriteString("When the repository uses a vendor's SDK and that vendor's documentation server or skills are available below, that is a good reason to recommend them to the agents who write that code. ")
	b.WriteString("You only advise: the person applies each recommendation. Do not edit files.\n\n")
	fmt.Fprintf(&b, "## Project\nName: %s\n", p.Name)
	if p.Brief != "" {
		fmt.Fprintf(&b, "Brief: %s\n", clip(p.Brief, 2000))
	}
	var goal, proposal string
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT goal, proposal FROM plans WHERE project_id=? AND status IN ('ACCEPTED','PROPOSED') ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&goal, &proposal)
	if goal != "" {
		fmt.Fprintf(&b, "Plan goal: %s\n", clip(goal, 1000))
		var pr struct {
			Tasks []struct {
				Title string `json:"title"`
			} `json:"tasks"`
		}
		if json.Unmarshal([]byte(proposal), &pr) == nil {
			for i, t := range pr.Tasks {
				if i == 30 {
					break
				}
				fmt.Fprintf(&b, "- planned: %s\n", clip(t.Title, 160))
			}
		}
	}
	if p.HasRepo && p.RepoPath != "" {
		// the SDKs and frameworks the code uses, so a matching documentation server or vendor skill can be recommended (P54)
		if r, err := libver.Scan(p.RepoPath, libver.Options{MaxLibs: 30}); err == nil && r.Libs() > 0 {
			fmt.Fprintf(&b, "Libraries the repository uses: %s\n", clip(r.Line(), 1500))
		}
	}
	names := map[string]string{}
	b.WriteString("\n## Team (use the agent ids)\n")
	for _, a := range w.team {
		names[a.ID] = a.Name
		fmt.Fprintf(&b, "- %s: %s, role %s, CLI %s", a.ID, a.Name, a.Role, a.Adapter)
		if a.Purpose != "" {
			fmt.Fprintf(&b, ", purpose: %s", clip(a.Purpose, 200))
		}
		b.WriteString("\n")
	}
	if ts, err := c.Tasks.List(ctx, tasks.Filter{ProjectID: projectID, Limit: 25}); err == nil && len(ts) > 0 {
		b.WriteString("\n## Recent tasks (newest first)\n")
		for _, t := range ts {
			who := names[t.Assignee]
			if who == "" {
				who = "unassigned"
			}
			fmt.Fprintf(&b, "- #%d %s [%s, %s]\n", t.Seq, clip(t.Title, 160), strings.ToLower(string(t.Status)), who)
		}
	}
	b.WriteString("\n## Skills already in the project\n")
	if len(w.inProject) == 0 {
		b.WriteString("(none)\n")
	}
	for _, name := range sortedKeys(w.inProject) {
		s := w.inProject[name]
		who := "whole team"
		if s.Audience == skills.AudienceAgents {
			var ns []string
			for _, id := range s.Agents {
				ns = append(ns, names[id])
			}
			who = "agents: " + strings.Join(ns, ", ")
		}
		fmt.Fprintf(&b, "- %s (%s; %s): %s\n", s.Name, s.Status, who, clip(s.Description, recommendDescLen))
	}
	if len(candidates) > 0 {
		b.WriteString("\n## Skills the person picked from the Library for this project\nSay for each whether to add it (and to whom) or not; a pick you do not recommend needs no item, but name it in the summary.\n")
		for _, k := range candidates {
			if ls := w.library[k.Name]; len(ls) > 0 {
				for _, l := range ls {
					if l.RegistryID == k.RegistryID {
						fmt.Fprintf(&b, "- %s (registry %s): %s\n", l.Name, l.RegistryID, clip(l.Description, 300))
					}
				}
			}
		}
	}
	b.WriteString("\n## Skills in the Library (not in the project)\n")
	n := 0
	for _, name := range sortedKeys(w.library) {
		if _, in := w.inProject[name]; in {
			continue
		}
		for _, l := range w.library[name] {
			if n == recommendMaxLibrary {
				break
			}
			fmt.Fprintf(&b, "- %s (registry %s): %s\n", l.Name, l.RegistryID, clip(l.Description, recommendDescLen))
			n++
		}
	}
	if n == 0 {
		b.WriteString("(none)\n")
	}
	b.WriteString("\n## MCP servers connected in the workspace (use the server id)\n")
	if len(w.servers) == 0 {
		b.WriteString("(none)\n")
	}
	for _, id := range sortedKeys(w.servers) {
		s := w.servers[id]
		var tools []string
		for i, t := range s.Tools {
			if i == 8 {
				tools = append(tools, "...")
				break
			}
			tools = append(tools, t.Name)
		}
		state := "not in the project"
		if v, ok := w.inUse[id]; ok {
			var ns []string
			for _, g := range v.Agents {
				ns = append(ns, names[g.AgentID])
			}
			state = "in the project, given to: " + strings.Join(ns, ", ")
		}
		fmt.Fprintf(&b, "- %s: %s (%s); tools: %s\n", id, s.Display, state, strings.Join(tools, ", "))
	}
	b.WriteString("\n## Integrations connected in the workspace (use the kind)\n")
	if len(w.conns) == 0 {
		b.WriteString("(none)\n")
	}
	for _, k := range sortedKeys(w.conns) {
		fmt.Fprintf(&b, "- %s: %s\n", k, w.conns[k])
	}
	fmt.Fprintf(&b, `
## Answer
Answer with one JSON object and nothing else:
{"summary": "two or three sentences for the person", "recommendations": [
  {"kind": "skill", "action": "add", "name": "<library skill name>", "registry": "<its registry>", "agents": ["<agent id>", ...], "reason": "<why, one sentence>"},
  {"kind": "skill", "action": "give", "name": "<skill already in the project>", "agents": ["<agent id>"], "reason": "..."},
  {"kind": "skill", "action": "remove", "name": "<skill already in the project>", "reason": "..."},
  {"kind": "mcp", "action": "give", "name": "<server id>", "agents": ["<agent id>"], "reason": "..."},
  {"kind": "integration", "action": "add", "name": "<integration kind>", "reason": "..."}
]}
An empty "agents" list on a skill means the whole team. At most %d recommendations. Use only names and ids listed above.
`, recommendMaxItems)
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseRecommend reads the answer and checks every item against w: valid items, the problems of the others, the summary.
func parseRecommend(text string, w recommendWorld) ([]RecommendItem, []string, string, bool) {
	raw := strings.TrimSpace(text)
	if i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); i >= 0 && j > i {
		raw = raw[i : j+1]
	}
	var ans struct {
		Summary string `json:"summary"`
		Recs    []struct {
			Kind     string          `json:"kind"`
			Action   string          `json:"action"`
			Name     string          `json:"name"`
			Registry string          `json:"registry"`
			Agents   json.RawMessage `json:"agents"`
			Reason   string          `json:"reason"`
		} `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(raw), &ans); err != nil {
		return nil, []string{"the answer is not the JSON object asked for: " + err.Error()}, "", false
	}
	byName := map[string]string{}
	for _, a := range w.team {
		byName[strings.ToLower(a.ID)] = a.ID
		byName[strings.ToLower(a.Name)] = a.ID
	}
	var items []RecommendItem
	var problems []string
	seen := map[string]bool{}
	for i, r := range ans.Recs {
		if len(items) == recommendMaxItems {
			problems = append(problems, fmt.Sprintf("more than %d recommendations: the rest were left out", recommendMaxItems))
			break
		}
		kind, action := strings.ToLower(strings.TrimSpace(r.Kind)), strings.ToLower(strings.TrimSpace(r.Action))
		name := strings.TrimSpace(r.Name)
		bad := func(why string) {
			problems = append(problems, fmt.Sprintf("recommendation %d (%s %s): %s", i+1, kind, name, why))
		}
		var agentsOut []string
		var list []string
		if len(r.Agents) > 0 && string(r.Agents) != "null" {
			var one string
			if json.Unmarshal(r.Agents, &list) != nil && json.Unmarshal(r.Agents, &one) == nil && !strings.EqualFold(one, "team") {
				list = []string{one}
			}
		}
		unknown := ""
		for _, a := range list {
			if id, ok := byName[strings.ToLower(strings.TrimSpace(a))]; ok {
				agentsOut = append(agentsOut, id)
			} else if !strings.EqualFold(a, "team") {
				unknown = a
			}
		}
		if unknown != "" {
			bad(fmt.Sprintf("no agent %q on the team", unknown))
			continue
		}
		reason := clip(r.Reason, 400)
		if reason == "" {
			bad("no reason given")
			continue
		}
		it := RecommendItem{Kind: kind, Action: action, Name: name, Agents: orEmpty(agentsOut), Reason: reason, Status: "open"}
		switch kind {
		case "skill":
			_, inProject := w.inProject[name]
			switch {
			case action == "add" && inProject:
				// already in the project: named agents are a give; none widens it to the whole team, unless it reaches them already
				switch {
				case len(agentsOut) > 0:
					it.Action = "give"
				case w.inProject[name].Audience == skills.AudienceAgents:
					it.Action = "team"
				default:
					bad("already in the project for the whole team")
					continue
				}
			case action == "add":
				ls := w.library[name]
				var pick *skills.LibrarySkill
				for i := range ls {
					if r.Registry == "" || ls[i].RegistryID == r.Registry {
						pick = &ls[i]
						break
					}
				}
				if pick == nil {
					bad("not an available skill of the Library")
					continue
				}
				it.RegistryID = pick.RegistryID
			case (action == "give" || action == "remove") && !inProject:
				bad("not a skill of the project")
				continue
			case action == "give" && len(agentsOut) == 0:
				bad("give needs the agents")
				continue
			case action != "give" && action != "remove":
				bad("action must be add, give or remove")
				continue
			}
			it.Label = name
		case "mcp":
			s, ok := w.servers[name]
			if !ok {
				bad("not a connected MCP server")
				continue
			}
			if action == "add" {
				it.Action = "give"
			}
			if it.Action != "give" || len(agentsOut) == 0 {
				bad("an MCP server is given to named agents")
				continue
			}
			it.Label = s.Display
		case "integration":
			vendor, ok := w.conns[strings.ToLower(name)]
			if !ok {
				bad("not a connected integration")
				continue
			}
			it.Name, it.Action, it.Label, it.Agents = strings.ToLower(name), "add", vendor, []string{}
		default:
			bad("kind must be skill, mcp or integration")
			continue
		}
		key := it.Kind + "/" + it.Action + "/" + it.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		it.ID = fmt.Sprintf("r%d", len(items)+1)
		items = append(items, it)
	}
	return orEmpty(items), problems, clip(ans.Summary, 1200), true
}

// runRecommend runs the turn (one retry when the answer is not the JSON asked for) and stores the result.
func (c *Core) runRecommend(r Recommendation, advisor Agent) {
	base := c.recommendState().ctx
	ctx, cancel := context.WithTimeout(base, recommendTimeout)
	defer cancel()
	fail := func(err error) {
		msg := c.Scanner.Redact(err.Error())
		if base.Err() != nil {
			msg = recommendInterrupted
		}
		_ = c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), `UPDATE recommendations SET status='failed', error=?, finished_at=? WHERE id=?`, msg, storage.Now(), r.ID)
			return err
		})
		c.publish(context.Background(), "recommend.failed", r.ProjectID, map[string]any{"id": r.ID, "error": msg})
	}
	w, err := c.recommendWorld(ctx, r.ProjectID)
	if err != nil {
		fail(err)
		return
	}
	prompt := c.recommendPrompt(ctx, r.ProjectID, w, r.Candidates)
	var cost float64
	text, usd, err := c.recommendTurn(ctx, r, advisor, prompt)
	cost += usd
	if err != nil {
		fail(err)
		return
	}
	items, problems, summary, ok := parseRecommend(text, w)
	if !ok {
		retry := prompt + "\n\nYour previous answer was rejected by the validator. Answer again with the JSON object only:\n- " + strings.Join(problems, "\n- ")
		text, usd, err = c.recommendTurn(ctx, r, advisor, retry)
		cost += usd
		if err != nil {
			fail(err)
			return
		}
		if items, problems, summary, ok = parseRecommend(text, w); !ok {
			fail(errors.New(strings.Join(problems, "; ")))
			return
		}
	}
	it, _ := json.Marshal(items)
	dr, _ := json.Marshal(orEmpty(problems))
	err = c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE recommendations SET status='done', summary=?, items=?, dropped=?, cost_usd=?, finished_at=? WHERE id=?`,
			c.Scanner.Redact(summary), c.Scanner.Redact(string(it)), c.Scanner.Redact(string(dr)), cost, storage.Now(), r.ID)
		return err
	})
	if err != nil {
		fail(err)
		return
	}
	c.publish(context.Background(), "recommend.finished", r.ProjectID, map[string]any{"id": r.ID, "items": len(items), "dropped": len(problems), "trigger": r.Trigger})
	// a turn the person did not ask for says so where they look
	if r.Trigger == "evolution" && len(items) > 0 {
		if rm, err := c.generalChannel(context.Background(), r.ProjectID); err == nil {
			body := fmt.Sprintf("%s has %d new recommendation%s for the team (skills, MCP servers, integrations): open the Skills page to apply or dismiss them.", advisor.Name, len(items), plural(len(items), "", "s"))
			_, _ = c.Room.Post(context.Background(), room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, Body: body, Payload: map[string]any{"recommendation": r.ID}})
		}
	}
}

// recommendTurn runs one read-only turn as the advisor through the agent runtime (the same path as every turn: its CLI, run
// mode, practices and budgets) and returns its text and cost.
func (c *Core) recommendTurn(ctx context.Context, r Recommendation, advisor Agent, prompt string) (string, float64, error) {
	if c.Runtime == nil {
		return "", 0, errors.New("no agent runtime")
	}
	cfg, err := c.leadConfig(ctx, advisor.ID)
	if err != nil {
		return "", 0, err
	}
	cfg.ProjectID = r.ProjectID
	cfg.Spec = agents.RunSpec{AgentID: advisor.ID, Role: advisor.Role, Model: advisor.Model, Effort: advisor.Effort, PermissionMode: "plan", ReadOnly: true,
		Cwd: c.leadCwd(ctx, r.ProjectID, "")}
	in := workflow.NodeInput{NodeInfo: workflow.NodeInfo{ProjectID: r.ProjectID, NodeID: "recommend", Kind: workflow.KindAgentTask, Meta: map[string]string{"agent": "recommend"}}}
	out, err := c.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})(ctx, in)
	if err != nil {
		return "", 0, err
	}
	var res rt.TurnResult
	_ = json.Unmarshal(out.Value, &res)
	var usd float64
	for _, u := range res.Usage {
		usd += u.CostUSD
	}
	return res.Text, usd, nil
}

// RecommendList returns the project's last ten turns, newest first.
func (c *Core) RecommendList(ctx context.Context, projectID string) (Recommendations, error) {
	out := Recommendations{Auto: c.recommendAuto(ctx, projectID), Runs: []Recommendation{}}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id FROM recommendations WHERE project_id=? ORDER BY created_at DESC LIMIT 10`, projectID)
	if err != nil {
		return out, err
	}
	var idsList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		idsList = append(idsList, id)
	}
	rows.Close()
	for _, id := range idsList {
		r, err := c.recommendation(ctx, id)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, r)
	}
	return out, nil
}

func (c *Core) recommendation(ctx context.Context, id string) (Recommendation, error) {
	var r Recommendation
	var cand, items, dropped string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,project_id,agent_id,trigger,status,candidates,summary,items,dropped,error,cost_usd,created_at,finished_at FROM recommendations WHERE id=?`, id).
		Scan(&r.ID, &r.ProjectID, &r.AgentID, &r.Trigger, &r.Status, &cand, &r.Summary, &items, &dropped, &r.Error, &r.CostUSD, &r.Created, &r.Finished)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("recommendation %s not found", id)
	}
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(cand), &r.Candidates)
	_ = json.Unmarshal([]byte(items), &r.Items)
	_ = json.Unmarshal([]byte(dropped), &r.Dropped)
	r.Candidates, r.Items, r.Dropped = orEmpty(r.Candidates), orEmpty(r.Items), orEmpty(r.Dropped)
	return r, nil
}

// recommendMark sets one item's status (and what applying it did). The caller holds the recommendState lock: the read, the
// change and the write of the items must not interleave with another mark.
func (c *Core) recommendMark(ctx context.Context, id, itemID, status, note string) (Recommendation, error) {
	r, err := c.recommendation(ctx, id)
	if err != nil {
		return r, err
	}
	found := false
	for i := range r.Items {
		if r.Items[i].ID == itemID {
			if r.Items[i].Status != "open" {
				return r, fmt.Errorf("this recommendation was already %s", r.Items[i].Status)
			}
			r.Items[i].Status, r.Items[i].Note, found = status, note, true
		}
	}
	if !found {
		return r, fmt.Errorf("no recommendation %s in %s", itemID, id)
	}
	b, _ := json.Marshal(r.Items)
	if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE recommendations SET items=? WHERE id=?`, string(b), id)
		return err
	}); err != nil {
		return r, err
	}
	c.publish(ctx, "recommend.updated", r.ProjectID, map[string]any{"id": id, "item": itemID, "status": status})
	return r, nil
}

// RecommendApply does what one item recommends, as the person, through the same methods the pages use (audited there), and
// marks it applied. An integration is assigned to the project on its Integrations page (the person picks the scope there).
func (c *Core) RecommendApply(ctx context.Context, id, itemID string) (Recommendation, error) {
	// checking the item is open, doing what it says and marking it applied are one step: two clicks (two windows, a script)
	// neither apply an item twice nor undo each other's marks
	st := c.recommendState()
	st.mu.Lock()
	defer st.mu.Unlock()
	r, err := c.recommendation(ctx, id)
	if err != nil {
		return r, err
	}
	var it *RecommendItem
	for i := range r.Items {
		if r.Items[i].ID == itemID {
			it = &r.Items[i]
		}
	}
	if it == nil {
		return r, fmt.Errorf("no recommendation %s in %s", itemID, id)
	}
	if it.Status != "open" {
		return r, fmt.Errorf("this recommendation was already %s", it.Status)
	}
	call := func(method string, params any) error {
		b, _ := json.Marshal(params)
		_, err := c.Call(ctx, method, b)
		return err
	}
	pid := r.ProjectID
	var note string
	switch it.Kind + "/" + it.Action {
	case "skill/add":
		audience := skills.AudienceTeam
		if len(it.Agents) > 0 {
			audience = skills.AudienceAgents
		}
		err = call("skill.libraryAdd", map[string]any{"projectId": pid, "registryId": it.RegistryID, "name": it.Name, "audience": audience, "agents": it.Agents})
		note = "added to the project"
	case "skill/give":
		for _, a := range it.Agents {
			if err = call("skill.assign", map[string]any{"projectId": pid, "name": it.Name, "agentId": a, "on": true}); err != nil {
				break
			}
		}
		note = "given to the agents"
	case "skill/team":
		// the whole team from now on; the agents it was given to keep it
		var cur skills.Skill
		if k := c.Knowledge(); k == nil {
			err = errors.New("no skills service")
		} else if cur, err = k.Skills.Get(ctx, pid, it.Name); err == nil {
			err = call("skill.audience", map[string]any{"projectId": pid, "name": it.Name, "audience": skills.AudienceTeam, "agents": cur.Agents})
		}
		note = "given to the whole team"
	case "skill/remove":
		err = call("skill.release", map[string]any{"projectId": pid, "name": it.Name})
		note = "no longer used by the project"
	case "mcp/give":
		for _, a := range it.Agents {
			if err = call("mcpserver.grant", map[string]any{"projectId": pid, "serverId": it.Name, "agentId": a, "on": true}); err != nil {
				break
			}
		}
		note = "given to the agents"
	case "integration/add":
		return r, errors.New("assign this connection on the project's Integrations page: you choose what it syncs there")
	default:
		return r, fmt.Errorf("cannot apply %s %s", it.Kind, it.Action)
	}
	if err != nil {
		return r, err
	}
	return c.recommendMark(ctx, id, itemID, "applied", note)
}

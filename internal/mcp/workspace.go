package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The Workspace server (docs/mcp.md section 8): the app's own knowledge exposed as retrieval tools. Nothing is preloaded;
// every tool is called on demand. Each backing subsystem is a provider interface; a nil provider makes the tool answer
// honestly that the subsystem does not exist yet. Results pass through the same pipeline as upstream results (secret scan,
// shaping, untrusted marking, audit).

// Workspace bundles the providers. Any field may be nil.
type Workspace struct {
	Tasks        TaskProvider
	Room         RoomProvider
	Handoffs     HandoffSink
	Skills       SkillsProvider
	Packs        PackSearcher
	Requirements RequirementsSearcher
	Memory       MemorySearcher
	// RepoMap is nil until the tree-sitter repo map exists (decision D5): repo_map then returns RepoMapGatedMessage.
	RepoMap RepoMapper
}

// TaskView is the current task as an agent sees it.
type TaskView struct {
	ID          string         `json:"id"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status,omitempty"`
	Acceptance  []string       `json:"acceptance,omitempty"`
	Evidence    []string       `json:"evidence,omitempty"`
	Feedback    []string       `json:"feedback,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// TaskProvider backs the task tool. It must refuse statuses other than BLOCKED, WAITING_HUMAN and REVIEW as well (the
// gateway already does): acceptance is human (brief section 12).
type TaskProvider interface {
	Get(ctx context.Context, run RunInfo, taskID string) (TaskView, error)
	AddNote(ctx context.Context, run RunInfo, taskID, note string, evidenceRefs []string) error
	ProposeStatus(ctx context.Context, run RunInfo, taskID, status string, evidenceRefs []string) error
}

// RoomMessage is one room message.
type RoomMessage struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"thread_id,omitempty"`
	Author   string    `json:"author"`
	Kind     string    `json:"kind"`
	Mentions []string  `json:"mentions,omitempty"`
	Body     string    `json:"body"`
	Time     time.Time `json:"time"`
}

// RoomQuery selects messages to read.
type RoomQuery struct {
	Channel  string
	ThreadID string
	Limit    int
}

// RoomPost is a message an agent posts. Bodies are agent output, never executed.
type RoomPost struct {
	Channel  string
	ThreadID string
	Kind     string
	Mentions []string
	Body     string
}

// RoomProvider backs the room tool.
type RoomProvider interface {
	Read(ctx context.Context, run RunInfo, q RoomQuery) ([]RoomMessage, error)
	Post(ctx context.Context, run RunInfo, p RoomPost) (RoomMessage, error)
}

// Handoff is a handoff artifact.
type Handoff struct {
	ID      string   `json:"id,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Done    []string `json:"done,omitempty"`
	Open    []string `json:"open,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// HandoffSink backs the handoff tool.
type HandoffSink interface {
	Create(ctx context.Context, run RunInfo, h Handoff) (id string, err error)
	Get(ctx context.Context, run RunInfo, id string) (Handoff, error)
}

// Skill is a glob-scoped skill pinned to a commit.
type Skill struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Body         string `json:"body,omitempty"`
	PinnedCommit string `json:"pinned_commit,omitempty"`
}

// SkillsProvider backs the skills tool (how non-Claude CLIs receive glob-scoped skills on demand).
type SkillsProvider interface {
	List(ctx context.Context, run RunInfo) ([]Skill, error)
	Get(ctx context.Context, run RunInfo, name string) (Skill, error)
	ForPaths(ctx context.Context, run RunInfo, paths []string) ([]Skill, error)
}

// PackHit is one expert-pack chunk with its citation (docs/mcp.md section 8, brief section 15).
type PackHit struct {
	Pack        string    `json:"pack"`
	Version     string    `json:"version"`
	URL         string    `json:"url"`
	HeadingPath []string  `json:"heading_path,omitempty"`
	ChunkID     string    `json:"chunk_id"`
	FetchedAt   time.Time `json:"fetched_at"`
	Text        string    `json:"text"`
}

// PackSearcher backs search_pack.
type PackSearcher interface {
	Search(ctx context.Context, run RunInfo, query string, packIDs []string, k int) ([]PackHit, error)
}

// TextHit is a requirement or memory hit.
type TextHit struct {
	ID     string  `json:"id"`
	Source string  `json:"source,omitempty"`
	Title  string  `json:"title,omitempty"`
	Text   string  `json:"text"`
	Score  float64 `json:"score,omitempty"`
}

// RequirementsSearcher backs search_requirements.
type RequirementsSearcher interface {
	Search(ctx context.Context, run RunInfo, query, docID string, k int) ([]TextHit, error)
}

// MemorySearcher backs search_memory.
type MemorySearcher interface {
	Search(ctx context.Context, run RunInfo, query, scope string, k int) ([]TextHit, error)
}

// RepoMapper backs repo_map.
type RepoMapper interface {
	Map(ctx context.Context, run RunInfo, focusPaths []string, tokenBudget int) (string, error)
}

// ---- tool definitions (schemas copied from docs/mcp.md section 8) ----

type wsTool struct {
	Name        string
	Description string
	Schema      map[string]any
	resolved    *jsonschema.Resolved
	Budget      int // inline budget override; 0 = gateway default
	// ReadOnly marks tools that never change anything. It becomes the readOnlyHint annotation, which is what lets a CLI in a
	// read-only mode (Claude's plan mode, used for reviewers and room replies) call them without a prompt it cannot answer.
	ReadOnly bool
	handler  func(ctx context.Context, r *Run, a map[string]any) (string, error)
}

const wsSchemas = `{
"search_pack":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"pack_ids":{"type":"array","items":{"type":"string"}},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
"search_requirements":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"doc_id":{"type":"string"},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
"search_memory":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"scope":{"enum":["project","workspace"],"default":"project"},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
"repo_map":{"type":"object","properties":{"focus_paths":{"type":"array","items":{"type":"string"}},"token_budget":{"type":"integer","minimum":256,"maximum":8192,"default":2048}}},
"read_range":{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1},"max_bytes":{"type":"integer","maximum":65536,"default":16384}}},
"task":{"type":"object","required":["action"],"properties":{"action":{"enum":["get","add_note","propose_status"]},"task_id":{"type":"string"},"note":{"type":"string","maxLength":4000},"status":{"enum":["BLOCKED","WAITING_HUMAN","REVIEW"]},"evidence_refs":{"type":"array","items":{"type":"string"}}}},
"skills":{"type":"object","required":["action"],"properties":{"action":{"enum":["list","get","for_paths"]},"name":{"type":"string"},"paths":{"type":"array","items":{"type":"string"},"maxItems":50}}},
"room":{"type":"object","required":["action"],"properties":{"action":{"enum":["read","post"]},"channel":{"type":"string"},"thread_id":{"type":"string"},"kind":{"enum":["CHAT","STATUS","RESULT","QUESTION","DECISION","HANDOFF"]},"mentions":{"type":"array","items":{"type":"string"}},"body":{"type":"string","maxLength":4000},"limit":{"type":"integer","maximum":50}}},
"handoff":{"type":"object","required":["action"],"properties":{"action":{"enum":["create","get"]},"handoff_id":{"type":"string"},"summary":{"type":"string","maxLength":6000},"done":{"type":"array","items":{"type":"string"}},"open":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"}}}}
}`

var (
	wsOnce sync.Once
	wsDefs []*wsTool
)

func workspaceToolDefs() []*wsTool {
	wsOnce.Do(func() {
		var schemas map[string]map[string]any
		if err := json.Unmarshal([]byte(wsSchemas), &schemas); err != nil {
			panic("mcp: workspace schemas: " + err.Error())
		}
		defs := []*wsTool{
			{Name: "search_pack", Description: "Search expert-pack documentation. Returns cited chunks, or says nothing was found.", ReadOnly: true, handler: wsSearchPack},
			{Name: "search_requirements", Description: "Search the project's requirements documents.", ReadOnly: true, handler: wsSearchRequirements},
			{Name: "search_memory", Description: "Search project or workspace memory.", ReadOnly: true, handler: wsSearchMemory},
			{Name: "repo_map", Description: "Token-budgeted map of the repository's important symbols (currently gated, decision D5).", ReadOnly: true, handler: wsRepoMap},
			{Name: "read_range", Description: "Read a line range of a file in the worktree. Paths are worktree-relative.", ReadOnly: true, handler: wsReadRange, Budget: 65536},
			{Name: "task", Description: "Read the current task, add a note, or propose BLOCKED, WAITING_HUMAN or REVIEW with evidence. Cannot complete or accept a task.", handler: wsTask},
			{Name: "skills", Description: "List skills, read one by name, or get the skills that apply to paths.", ReadOnly: true, handler: wsSkills},
			{Name: "room", Description: "Read or post in the engineering room thread. Posted bodies are agent output and are rate limited.", handler: wsRoom},
			{Name: "handoff", Description: "Create or read a handoff artifact (summary, done, open items, files).", handler: wsHandoff},
		}
		for _, d := range defs {
			d.Schema = schemas[d.Name]
			if d.Schema == nil {
				panic("mcp: missing schema for " + d.Name)
			}
			b, _ := json.Marshal(d.Schema)
			var js jsonschema.Schema
			if err := json.Unmarshal(b, &js); err != nil {
				panic("mcp: workspace schema " + d.Name + ": " + err.Error())
			}
			rs, err := js.Resolve(nil)
			if err != nil {
				panic("mcp: workspace schema " + d.Name + ": " + err.Error())
			}
			d.resolved = rs
		}
		wsDefs = defs
	})
	return wsDefs
}

// callWorkspace validates the arguments against the documented schema, runs the handler and sends the text through the
// shared pipeline. Failures come back as tool errors the agent can read.
func (r *Run) callWorkspace(ctx context.Context, c *callRec, t target, args json.RawMessage) *sdk.CallToolResult {
	var def *wsTool
	for _, d := range workspaceToolDefs() {
		if d.Name == t.tool {
			def = d
		}
	}
	a := map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &a); err != nil {
			return textResult("invalid arguments: "+err.Error(), true)
		}
	}
	if err := def.resolved.Validate(a); err != nil {
		return textResult("invalid arguments: "+r.g.scrub(err.Error()), true)
	}
	out, err := def.handler(ctx, r, a)
	if err != nil {
		c.upstreamStatus = "error"
		switch {
		case errors.Is(err, ErrNotAvailable):
			return textResult(r.g.scrub(err.Error()), true)
		default:
			return textResult(r.g.scrub(t.tool+": "+err.Error()), true)
		}
	}
	c.upstreamStatus = "ok"
	budget := def.Budget
	if budget == 0 {
		budget = r.g.opts.DefaultInlineBytes
	}
	return r.finish(ctx, c, WorkspaceServerID, t.tool, out, false, budget, nil)
}

func notAvailable(tool, what string) error {
	return fmt.Errorf("%w: %s: %s", ErrNotAvailable, tool, what)
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// ---- argument helpers (values come from a validated JSON document) ----

func argStr(a map[string]any, k string) string { s, _ := a[k].(string); return s }

func argInt(a map[string]any, k string, def int) int {
	if f, ok := a[k].(float64); ok {
		return int(f)
	}
	return def
}

func argStrings(a map[string]any, k string) []string {
	arr, _ := a[k].([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (r *Run) ws() Workspace { return r.g.opts.Workspace }

// ---- handlers ----

func wsSearchPack(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Packs
	if p == nil {
		return "", notAvailable("search_pack", "expert packs are not built yet")
	}
	hits, err := p.Search(ctx, r.cfg.Info, argStr(a, "query"), argStrings(a, "pack_ids"), argInt(a, "k", 5))
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "nothing found in the expert packs for this query", nil
	}
	return jsonText(hits)
}

func wsSearchRequirements(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Requirements
	if p == nil {
		return "", notAvailable("search_requirements", "requirements indexing is not built yet")
	}
	hits, err := p.Search(ctx, r.cfg.Info, argStr(a, "query"), argStr(a, "doc_id"), argInt(a, "k", 5))
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "nothing found in the requirements for this query", nil
	}
	return jsonText(hits)
}

func wsSearchMemory(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Memory
	if p == nil {
		return "", notAvailable("search_memory", "project memory is not built yet")
	}
	scope := argStr(a, "scope")
	if scope == "" {
		scope = "project"
	}
	hits, err := p.Search(ctx, r.cfg.Info, argStr(a, "query"), scope, argInt(a, "k", 5))
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "nothing found in memory for this query", nil
	}
	return jsonText(hits)
}

func wsRepoMap(ctx context.Context, r *Run, a map[string]any) (string, error) {
	m := r.ws().RepoMap
	if m == nil {
		return "", fmt.Errorf("%w: %s", ErrNotAvailable, RepoMapGatedMessage)
	}
	focus := argStrings(a, "focus_paths")
	for _, f := range focus {
		if err := lexicalCheck(f, filepath.Separator == '\\'); err != nil {
			return "", fmt.Errorf("focus_paths: %q: %v", f, err)
		}
	}
	return m.Map(ctx, r.cfg.Info, focus, argInt(a, "token_budget", 2048))
}

func wsReadRange(_ context.Context, r *Run, a map[string]any) (string, error) {
	root := r.cfg.Info.Worktree
	f, rel, err := openConfined(root, argStr(a, "path"))
	if err != nil {
		switch {
		case errors.Is(err, ErrOutsideRoot):
			return "", errors.New("the path is outside the worktree")
		case errors.Is(err, ErrProtectedPath):
			return "", errors.New("the path is protected (git internals and credential files are never served)")
		case errors.Is(err, os.ErrNotExist):
			return "", errors.New("no such file in the worktree")
		}
		return "", err
	}
	defer f.Close()
	start := argInt(a, "start_line", 1)
	end := argInt(a, "end_line", 0)
	if end != 0 && end < start {
		return "", errors.New("end_line is before start_line")
	}
	maxBytes := argInt(a, "max_bytes", 16384)
	if maxBytes < 1 {
		maxBytes = 16384
	}
	text, last, total, cut, capped, err := readLineRange(f, start, end, maxBytes)
	if err != nil {
		return "", err
	}
	if last == 0 {
		if total == 0 {
			return fmt.Sprintf("// %s: empty file", rel), nil
		}
		return "", fmt.Errorf("start_line %d is beyond the end of the file (%d lines)", start, total)
	}
	var b strings.Builder
	totalStr := fmt.Sprint(total)
	if capped {
		totalStr = fmt.Sprintf("more than %d", total)
	}
	fmt.Fprintf(&b, "// %s lines %d-%d of %s\n", rel, start, last, totalStr)
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	if cut || (end > 0 && last < end && last < total) {
		fmt.Fprintf(&b, "// output limited by max_bytes=%d; continue with start_line=%d\n", maxBytes, last+1)
	}
	return b.String(), nil
}

func (r *Run) taskID(a map[string]any) (string, error) {
	id := argStr(a, "task_id")
	switch {
	case id == "" && r.cfg.Info.TaskID == "":
		return "", errors.New("this run has no task")
	case id == "":
		return r.cfg.Info.TaskID, nil
	case r.cfg.Info.TaskID != "" && id != r.cfg.Info.TaskID:
		return "", errors.New("task_id must be this run's task")
	}
	return id, nil
}

func wsTask(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Tasks
	if p == nil {
		return "", notAvailable("task", "the task service is not wired in this build")
	}
	id, err := r.taskID(a)
	if err != nil {
		return "", err
	}
	switch argStr(a, "action") {
	case "get":
		v, err := p.Get(ctx, r.cfg.Info, id)
		if err != nil {
			return "", err
		}
		return jsonText(v)
	case "add_note":
		note := argStr(a, "note")
		if strings.TrimSpace(note) == "" {
			return "", errors.New("note is required")
		}
		note = r.g.scrub(note)
		if err := p.AddNote(ctx, r.cfg.Info, id, note, argStrings(a, "evidence_refs")); err != nil {
			return "", err
		}
		return "note added", nil
	case "propose_status":
		st := argStr(a, "status")
		switch st {
		case "BLOCKED", "WAITING_HUMAN", "REVIEW":
		default:
			return "", errors.New("status must be BLOCKED, WAITING_HUMAN or REVIEW: agents cannot complete, cancel or ready a task")
		}
		if err := p.ProposeStatus(ctx, r.cfg.Info, id, st, argStrings(a, "evidence_refs")); err != nil {
			return "", err
		}
		return "proposed status " + st, nil
	}
	return "", errors.New("unknown action")
}

func wsSkills(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Skills
	if p == nil {
		return "", notAvailable("skills", "the skills library is not built yet")
	}
	switch argStr(a, "action") {
	case "list":
		s, err := p.List(ctx, r.cfg.Info)
		if err != nil {
			return "", err
		}
		for i := range s {
			s[i].Body = "" // the list never carries bodies: progressive disclosure
		}
		return jsonText(s)
	case "get":
		name := argStr(a, "name")
		if name == "" {
			return "", errors.New("name is required")
		}
		s, err := p.Get(ctx, r.cfg.Info, name)
		if err != nil {
			return "", err
		}
		return jsonText(s)
	case "for_paths":
		paths := argStrings(a, "paths")
		if len(paths) == 0 {
			return "", errors.New("paths is required")
		}
		for _, x := range paths {
			if err := lexicalCheck(x, filepath.Separator == '\\'); err != nil {
				return "", fmt.Errorf("path %q: %v", x, err)
			}
		}
		s, err := p.ForPaths(ctx, r.cfg.Info, paths)
		if err != nil {
			return "", err
		}
		return jsonText(s)
	}
	return "", errors.New("unknown action")
}

func wsRoom(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Room
	if p == nil {
		return "", notAvailable("room", "the room service is not wired in this build")
	}
	switch argStr(a, "action") {
	case "read":
		limit := argInt(a, "limit", 20)
		if limit < 1 {
			limit = 20
		}
		msgs, err := p.Read(ctx, r.cfg.Info, RoomQuery{Channel: argStr(a, "channel"), ThreadID: argStr(a, "thread_id"), Limit: limit})
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			return "no messages", nil
		}
		return jsonText(msgs)
	case "post":
		body := argStr(a, "body")
		if strings.TrimSpace(body) == "" {
			return "", errors.New("body is required")
		}
		kind := argStr(a, "kind")
		if kind == "" {
			kind = "CHAT"
		}
		if !r.postLim.allow() {
			return "", fmt.Errorf("room.post is rate limited; retry in %s", r.postLim.retryAfter().Round(time.Second))
		}
		body, rules := redact(body, r.g.opts.Scanner.Scan(body)) // agent output is redacted before persistence
		m, err := p.Post(ctx, r.cfg.Info, RoomPost{Channel: argStr(a, "channel"), ThreadID: argStr(a, "thread_id"), Kind: kind, Mentions: argStrings(a, "mentions"), Body: body})
		if err != nil {
			return "", err
		}
		out := "posted " + m.ID
		if len(rules) > 0 {
			out += " (secrets were redacted from the body)"
		}
		return out, nil
	}
	return "", errors.New("unknown action")
}

func wsHandoff(ctx context.Context, r *Run, a map[string]any) (string, error) {
	p := r.ws().Handoffs
	if p == nil {
		return "", notAvailable("handoff", "handoff artifacts are not wired in this build")
	}
	switch argStr(a, "action") {
	case "create":
		sum := argStr(a, "summary")
		if strings.TrimSpace(sum) == "" {
			return "", errors.New("summary is required")
		}
		files := argStrings(a, "files")
		for _, f := range files {
			if why := relPathProblem(f); why != "" {
				return "", fmt.Errorf("file %q: %s", f, why)
			}
		}
		h := Handoff{Summary: r.g.scrub(sum), Done: scrubAll(r.g, argStrings(a, "done")), Open: scrubAll(r.g, argStrings(a, "open")), Files: files}
		id, err := p.Create(ctx, r.cfg.Info, h)
		if err != nil {
			return "", err
		}
		return "handoff created: " + id, nil
	case "get":
		id := argStr(a, "handoff_id")
		if id == "" {
			return "", errors.New("handoff_id is required")
		}
		h, err := p.Get(ctx, r.cfg.Info, id)
		if err != nil {
			return "", err
		}
		return jsonText(h)
	}
	return "", errors.New("unknown action")
}

func scrubAll(g *Gateway, in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = g.scrub(s)
	}
	return out
}

// relPathProblem checks a path that is only recorded (a handoff file list): it must be a worktree-relative path on every
// platform, so a Windows drive path or a backslash traversal is refused even when the host is not Windows.
func relPathProblem(f string) string {
	if f == "" || strings.ContainsRune(f, 0) {
		return "empty or contains NUL"
	}
	n := strings.ReplaceAll(f, `\`, "/")
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':' && isASCIILetter(n[0])) {
		return "must be a worktree-relative path"
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "must not leave the worktree"
		}
	}
	return ""
}

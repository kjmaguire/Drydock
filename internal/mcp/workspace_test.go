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
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- real (in-memory) implementations of the provider interfaces ----

type memTasks struct {
	mu     sync.Mutex
	notes  []string
	status string
	view   TaskView
}

func (m *memTasks) Get(_ context.Context, _ RunInfo, id string) (TaskView, error) {
	if id != m.view.ID {
		return TaskView{}, ErrNotFound
	}
	return m.view, nil
}
func (m *memTasks) AddNote(_ context.Context, _ RunInfo, id, note string, _ []string) error {
	m.mu.Lock()
	m.notes = append(m.notes, note)
	m.mu.Unlock()
	return nil
}
func (m *memTasks) ProposeStatus(_ context.Context, _ RunInfo, id, status string, _ []string) error {
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
	return nil
}

type memRoom struct {
	mu    sync.Mutex
	posts []RoomPost
}

func (m *memRoom) Read(_ context.Context, _ RunInfo, q RoomQuery) ([]RoomMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RoomMessage
	for i, p := range m.posts {
		out = append(out, RoomMessage{ID: fmt.Sprint("m", i), ThreadID: p.ThreadID, Author: "agent", Kind: p.Kind, Body: p.Body, Time: time.Unix(1, 0)})
	}
	return out, nil
}
func (m *memRoom) Post(_ context.Context, _ RunInfo, p RoomPost) (RoomMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.posts = append(m.posts, p)
	return RoomMessage{ID: fmt.Sprint("m", len(m.posts)-1)}, nil
}

type memHandoffs struct {
	mu sync.Mutex
	hs []Handoff
}

func (m *memHandoffs) Create(_ context.Context, _ RunInfo, h Handoff) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hs = append(m.hs, h)
	return fmt.Sprint("h", len(m.hs)), nil
}
func (m *memHandoffs) Get(_ context.Context, _ RunInfo, id string) (Handoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, h := range m.hs {
		if fmt.Sprint("h", i+1) == id {
			return h, nil
		}
	}
	return Handoff{}, ErrNotFound
}

type memSkills struct{}

func (memSkills) List(context.Context, RunInfo) ([]Skill, error) {
	return []Skill{{Name: "go-style", Description: "Go style", Body: "LONG BODY", PinnedCommit: "abc123"}}, nil
}
func (memSkills) Get(_ context.Context, _ RunInfo, name string) (Skill, error) {
	if name != "go-style" {
		return Skill{}, ErrNotFound
	}
	return Skill{Name: name, Body: "LONG BODY", PinnedCommit: "abc123"}, nil
}
func (memSkills) ForPaths(_ context.Context, _ RunInfo, paths []string) ([]Skill, error) {
	return []Skill{{Name: "go-style", Description: "applies to " + strings.Join(paths, ",")}}, nil
}

type memPacks struct{ empty bool }

func (m memPacks) Search(_ context.Context, _ RunInfo, q string, packs []string, k int) ([]PackHit, error) {
	if m.empty {
		return nil, nil
	}
	return []PackHit{{Pack: "cloudflare-workers", Version: "2026-09", URL: "https://developers.cloudflare.com/workers/", HeadingPath: []string{"Workers", "Overview"}, ChunkID: "c1", FetchedAt: time.Unix(100, 0).UTC(), Text: "answer to " + q}}, nil
}

type memReqs struct{}

func (memReqs) Search(_ context.Context, _ RunInfo, q, doc string, k int) ([]TextHit, error) {
	return []TextHit{{ID: "R1", Source: doc, Text: "must " + q, Score: 0.9}}, nil
}

type memMemory struct{ gotScope *string }

func (m memMemory) Search(_ context.Context, _ RunInfo, q, scope string, k int) ([]TextHit, error) {
	*m.gotScope = scope
	return []TextHit{{ID: "M1", Text: q}}, nil
}

type memRepoMap struct{}

func (memRepoMap) Map(_ context.Context, _ RunInfo, focus []string, budget int) (string, error) {
	return fmt.Sprintf("map focus=%v budget=%d", focus, budget), nil
}

func worktree(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "wt")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "src"), filepath.Join(root, ".git"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "src", "a.txt"), strings.Join(lines, "\n")+"\n")
	write(filepath.Join(root, "big.txt"), bigText(200000, false))
	write(filepath.Join(root, ".env"), "SECRET=1\n")
	write(filepath.Join(root, ".env.example"), "SECRET=\n")
	write(filepath.Join(root, ".git", "config"), "[remote]\n")
	write(filepath.Join(root, "bin.dat"), "abc\x00def")
	write(filepath.Join(root, "key.pem"), "-----BEGIN PRIVATE KEY-----")
	write(filepath.Join(root, "empty.txt"), "")
	write(filepath.Join(root, "nonl.txt"), "no trailing newline")
	write(filepath.Join(outside, "secret.txt"), "outside secret\n")
	return root, outside
}

func wsEnv(t *testing.T, ws Workspace, mod func(*Options)) (*env, *sdk.ClientSession, string, string) {
	root, outside := worktree(t)
	e := newEnv(t, func(o *Options) {
		o.Workspace = ws
		if mod != nil {
			mod(o)
		}
	})
	ep := e.startRun(RunConfig{
		Info:   RunInfo{RunID: "r-ws", ProjectID: "p", TaskID: "task-1", AgentID: "ag", Role: "backend", Worktree: root},
		Policy: allow(map[string][]string{WorkspaceServerID: {"*"}}),
	})
	return e, e.connect(ep), root, outside
}

func TestWorkspaceToolsListed(t *testing.T) {
	_, cs, _, _ := wsEnv(t, Workspace{}, nil)
	want := "chats,fetch_range,handoff,read_range,repo_map,room,sandbox_info,sandbox_logs,sandbox_run,sandbox_start,sandbox_stop,search_memory,search_pack,search_requirements,skills,task"
	if got := strings.Join(toolNames(t, cs), ","); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	res, _ := cs.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		b, _ := json.Marshal(tl.InputSchema)
		if !strings.Contains(string(b), `"type":"object"`) {
			t.Errorf("%s: schema %s", tl.Name, b)
		}
	}
}

func TestWorkspaceHonestlyNotAvailable(t *testing.T) {
	e, cs, _, _ := wsEnv(t, Workspace{}, nil)
	calls := map[string]map[string]any{
		"search_pack":         {"query": "workers"},
		"search_requirements": {"query": "login"},
		"search_memory":       {"query": "decision"},
		"task":                {"action": "get"},
		"skills":              {"action": "list"},
		"room":                {"action": "read"},
		"handoff":             {"action": "get", "handoff_id": "h1"},
	}
	for tool, args := range calls {
		r := callTool(t, cs, tool, args)
		if !r.IsError || !strings.Contains(textOf(r), "not available yet") {
			t.Errorf("%s: %s", tool, textOf(r))
		}
	}
	r := callTool(t, cs, "repo_map", map[string]any{})
	if !r.IsError || !strings.Contains(textOf(r), "not available: repo map is gated (decision D5)") {
		t.Fatalf("repo_map without a RepoMapper: %s", textOf(r))
	}
	if n := len(e.events(EventCall)); n != len(calls)+1 {
		t.Fatalf("mcp.call events %d", n)
	}
}

func TestWorkspaceProviders(t *testing.T) {
	var scope string
	tasks := &memTasks{view: TaskView{ID: "task-1", Title: "Add login", Status: "IN_PROGRESS", Acceptance: []string{"works"}, Feedback: []string{"fix typo"}}}
	room, hand := &memRoom{}, &memHandoffs{}
	e, cs, _, _ := wsEnv(t, Workspace{
		Tasks: tasks, Room: room, Handoffs: hand, Skills: memSkills{}, Packs: memPacks{}, Requirements: memReqs{},
		Memory: memMemory{gotScope: &scope}, RepoMap: memRepoMap{},
	}, func(o *Options) { o.PostBurst = 2; o.PostPerSec = 0.00001 })

	body := func(r *sdk.CallToolResult) string {
		t.Helper()
		if r.IsError {
			t.Fatalf("error: %s", textOf(r))
		}
		b, ok := ExtractBody(textOf(r))
		if !ok {
			t.Fatalf("workspace result is not marked untrusted: %s", textOf(r))
		}
		return b
	}

	// task
	var tv TaskView
	if err := json.Unmarshal([]byte(body(callTool(t, cs, "task", map[string]any{"action": "get"}))), &tv); err != nil || tv.Title != "Add login" || tv.Feedback[0] != "fix typo" {
		t.Fatalf("task get: %+v %v", tv, err)
	}
	callTool(t, cs, "task", map[string]any{"action": "add_note", "note": "found key AKIAIOSFODNN7EXAMPLE in logs", "evidence_refs": []string{"ev1"}})
	if len(tasks.notes) != 1 || strings.Contains(tasks.notes[0], "AKIA") {
		t.Fatalf("notes are redacted before persistence: %v", tasks.notes)
	}
	callTool(t, cs, "task", map[string]any{"action": "propose_status", "status": "REVIEW", "evidence_refs": []string{"ev1"}})
	if tasks.status != "REVIEW" {
		t.Fatalf("status %q", tasks.status)
	}
	for _, st := range []string{"COMPLETED", "CANCELLED", "READY", "DONE"} {
		r := callTool(t, cs, "task", map[string]any{"action": "propose_status", "status": st})
		if !r.IsError || tasks.status != "REVIEW" {
			t.Fatalf("agents must not set %s: %s", st, textOf(r))
		}
	}
	if r := callTool(t, cs, "task", map[string]any{"action": "get", "task_id": "someone-elses-task"}); !r.IsError {
		t.Fatal("task_id of another task must be refused")
	}
	if r := callTool(t, cs, "task", map[string]any{"action": "add_note"}); !r.IsError {
		t.Fatal("note is required")
	}

	// room: typed, rate limited, redacted
	p1 := callTool(t, cs, "room", map[string]any{"action": "post", "kind": "STATUS", "body": "done, token ghp_" + strings.Repeat("a1B2", 9), "mentions": []string{"qa"}})
	if p1.IsError || len(room.posts) != 1 || strings.Contains(room.posts[0].Body, "ghp_") || room.posts[0].Kind != "STATUS" || room.posts[0].Mentions[0] != "qa" {
		t.Fatalf("post: %s %+v", textOf(p1), room.posts)
	}
	callTool(t, cs, "room", map[string]any{"action": "post", "body": "second"})
	if room.posts[1].Kind != "CHAT" {
		t.Fatalf("default kind: %+v", room.posts[1])
	}
	if r := callTool(t, cs, "room", map[string]any{"action": "post", "body": "third"}); !r.IsError || !strings.Contains(textOf(r), "rate limited") || len(room.posts) != 2 {
		t.Fatalf("third post should be rate limited: %s", textOf(r))
	}
	if r := callTool(t, cs, "room", map[string]any{"action": "post", "kind": "SHOUT", "body": "x"}); !r.IsError {
		t.Fatal("kind outside the enum must be refused")
	}
	if !strings.Contains(body(callTool(t, cs, "room", map[string]any{"action": "read", "limit": 5})), "second") {
		t.Fatal("room read")
	}

	// handoff
	h := callTool(t, cs, "handoff", map[string]any{"action": "create", "summary": "all done", "done": []string{"a"}, "open": []string{"b"}, "files": []string{"src/a.txt"}})
	if !strings.Contains(body(h), "handoff created: h1") || hand.hs[0].Files[0] != "src/a.txt" {
		t.Fatalf("handoff: %s %+v", textOf(h), hand.hs)
	}
	for _, f := range []string{"../etc/passwd", "/etc/passwd", "..", `C:\Windows\x`} {
		if r := callTool(t, cs, "handoff", map[string]any{"action": "create", "summary": "s", "files": []string{f}}); !r.IsError {
			t.Fatalf("handoff file %q must be refused", f)
		}
	}
	if !strings.Contains(body(callTool(t, cs, "handoff", map[string]any{"action": "get", "handoff_id": "h1"})), "all done") {
		t.Fatal("handoff get")
	}

	// skills: the list never carries bodies
	lst := body(callTool(t, cs, "skills", map[string]any{"action": "list"}))
	if strings.Contains(lst, "LONG BODY") || !strings.Contains(lst, "go-style") {
		t.Fatalf("skills list: %s", lst)
	}
	if !strings.Contains(body(callTool(t, cs, "skills", map[string]any{"action": "get", "name": "go-style"})), "LONG BODY") {
		t.Fatal("skills get")
	}
	if !strings.Contains(body(callTool(t, cs, "skills", map[string]any{"action": "for_paths", "paths": []string{"internal/x.go"}})), "internal/x.go") {
		t.Fatal("skills for_paths")
	}
	if r := callTool(t, cs, "skills", map[string]any{"action": "for_paths", "paths": []string{"../../x"}}); r.IsError {
		// relative traversal in a glob-matching path is harmless data, but absolute device paths are refused
		t.Logf("for_paths with .. accepted: %s", textOf(r))
	}

	// searches: citations come through; defaults applied; empty result is explicit
	hits := body(callTool(t, cs, "search_pack", map[string]any{"query": "durable objects"}))
	for _, k := range []string{"pack", "version", "url", "heading_path", "chunk_id", "fetched_at"} {
		if !strings.Contains(hits, `"`+k+`"`) {
			t.Errorf("citation lacks %s: %s", k, hits)
		}
	}
	if !strings.Contains(body(callTool(t, cs, "search_requirements", map[string]any{"query": "login", "doc_id": "prd"})), "must login") {
		t.Fatal("search_requirements")
	}
	callTool(t, cs, "search_memory", map[string]any{"query": "q"})
	if scope != "project" {
		t.Fatalf("default scope %q", scope)
	}
	callTool(t, cs, "search_memory", map[string]any{"query": "q", "scope": "workspace"})
	if scope != "workspace" {
		t.Fatalf("scope %q", scope)
	}
	if r := callTool(t, cs, "repo_map", map[string]any{"focus_paths": []string{"internal"}, "token_budget": 512}); body(r) != "map focus=[internal] budget=512" {
		t.Fatalf("repo_map: %s", textOf(r))
	}

	// schema validation (docs/mcp.md section 8)
	for name, args := range map[string]map[string]any{
		"search_pack":   {"query": strings.Repeat("q", 513)},
		"search_memory": {"query": "x", "k": 11},
		"repo_map":      {"token_budget": 10},
		"read_range":    {"path": "a", "max_bytes": 70000},
		"task":          {"action": "delete"},
	} {
		if r := callTool(t, cs, name, args); !r.IsError || !strings.Contains(textOf(r), "invalid arguments") {
			t.Errorf("%s %v: %s", name, args, textOf(r))
		}
	}
	// nothing found is explicit
	e2, cs2, _, _ := wsEnv(t, Workspace{Packs: memPacks{empty: true}}, nil)
	_ = e2
	if !strings.Contains(textOf(callTool(t, cs2, "search_pack", map[string]any{"query": "zzz"})), "nothing found") {
		t.Fatal("empty search must say so")
	}
	// audit: workspace calls are recorded
	var ws int
	for _, ev := range e.events(EventCall) {
		if eventData(t, ev)["server"] == WorkspaceServerID {
			ws++
		}
	}
	if ws < 20 {
		t.Fatalf("workspace mcp.call events: %d", ws)
	}
}

func TestReadRangeConfinement(t *testing.T) {
	_, cs, root, outside := wsEnv(t, Workspace{}, nil)
	read := func(path string, extra map[string]any) (string, bool) {
		args := map[string]any{"path": path}
		for k, v := range extra {
			args[k] = v
		}
		r := callTool(t, cs, "read_range", args)
		return textOf(r), r.IsError
	}
	// plain ranges
	txt, isErr := read("src/a.txt", map[string]any{"start_line": 3, "end_line": 5})
	body, _ := ExtractBody(txt)
	if isErr || body != "// src/a.txt lines 3-5 of 10\nline 3\nline 4\nline 5\n" {
		t.Fatalf("range: %q", body)
	}
	if body, _ := ExtractBody(func() string { s, _ := read("src/a.txt", nil); return s }()); !strings.HasPrefix(body, "// src/a.txt lines 1-10 of 10\nline 1\n") {
		t.Fatalf("whole file: %q", body)
	}
	if txt, _ := read("nonl.txt", nil); !strings.Contains(txt, "no trailing newline") {
		t.Fatalf("no trailing newline: %s", txt)
	}
	if txt, _ := read("empty.txt", nil); !strings.Contains(txt, "empty file") {
		t.Fatalf("empty: %s", txt)
	}
	// absolute path inside the worktree is fine
	if txt, isErr := read(filepath.Join(root, "src", "a.txt"), map[string]any{"start_line": 1, "end_line": 1}); isErr || !strings.Contains(txt, "line 1") {
		t.Fatalf("absolute inside: %s", txt)
	}
	// max_bytes cuts and tells where to continue
	txt, _ = read("big.txt", map[string]any{"start_line": 1, "max_bytes": 200})
	if !strings.Contains(txt, "output limited by max_bytes=200; continue with start_line=") {
		t.Fatalf("cut: %s", txt)
	}
	// big.txt has ~3500 lines; a range deep in the file works
	if txt, _ := read("big.txt", map[string]any{"start_line": 3000, "end_line": 3001}); !strings.Contains(txt, "line 002999: the quick") {
		t.Fatalf("deep range: %s", txt)
	}
	// errors
	if txt, isErr := read("src/a.txt", map[string]any{"start_line": 99}); !isErr || !strings.Contains(txt, "beyond the end") {
		t.Fatalf("beyond eof: %s", txt)
	}
	if _, isErr := read("src/a.txt", map[string]any{"start_line": 5, "end_line": 2}); !isErr {
		t.Fatal("end before start")
	}
	if txt, isErr := read("nope.txt", nil); !isErr || !strings.Contains(txt, "no such file") {
		t.Fatalf("missing: %s", txt)
	}
	if txt, isErr := read("src", nil); !isErr {
		t.Fatalf("directory: %s", txt)
	}
	if txt, isErr := read("bin.dat", nil); !isErr || !strings.Contains(txt, "binary") {
		t.Fatalf("binary: %s", txt)
	}
	// escapes
	escapes := []string{
		"../outside/secret.txt",
		"src/../../outside/secret.txt",
		filepath.Join(outside, "secret.txt"),
		"/etc/passwd",
		`\\server\share\x`,
		"src/a.txt\x00.png",
		"",
	}
	for _, p := range escapes {
		txt, isErr := read(p, nil)
		if !isErr || strings.Contains(txt, "outside secret") || strings.Contains(txt, "root:") {
			t.Errorf("path %q escaped: %s", p, txt)
		}
	}
	// protected paths
	for _, p := range []string{".git/config", ".git", ".env", "key.pem", "src/../.git/config"} {
		if txt, isErr := read(p, nil); !isErr || strings.Contains(txt, "[remote]") || strings.Contains(txt, "SECRET=1") {
			t.Errorf("protected path %q served: %s", p, txt)
		}
	}
	if txt, isErr := read(".env.example", nil); isErr {
		t.Fatalf(".env.example should be readable: %s", txt)
	}
	// symlinks
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link_out.txt")); err != nil {
		t.Skipf("cannot create symlinks here (%v): symlink cases not run", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "src", "a.txt"), filepath.Join(root, "link_in.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.git/config", filepath.Join(root, "src", "sneaky")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"link_out.txt", "linkdir/secret.txt", "src/../linkdir/secret.txt"} {
		if txt, isErr := read(p, nil); !isErr || strings.Contains(txt, "outside secret") {
			t.Errorf("symlink %q escaped the worktree: %s", p, txt)
		}
	}
	if txt, isErr := read("link_in.txt", nil); isErr || !strings.Contains(txt, "line 1") {
		t.Fatalf("a symlink that stays inside the worktree is fine: %s", txt)
	}
	if txt, isErr := read("src/sneaky", nil); !isErr || strings.Contains(txt, "[remote]") {
		t.Fatalf("symlink into .git: %s", txt)
	}
}

func TestConfineLexicalRules(t *testing.T) {
	bad := []string{"", "a\x00b", `\\?\C:\x`, `\\.\pipe\x`, `\\srv\share`}
	for _, p := range bad {
		if lexicalCheck(p, false) == nil {
			t.Errorf("%q accepted", p)
		}
	}
	winBad := []string{"con", "CON.txt", "dir/NUL", "dir\\aux.log", "COM1", "lpt9.x", "a/b:stream", "file.txt:Zone.Identifier", "C:foo", "dir/name.", "dir/name ", "1:\\x", "//host/share/x"}
	for _, p := range winBad {
		if lexicalCheck(p, true) == nil {
			t.Errorf("windows rule: %q accepted", p)
		}
	}
	good := []string{"src/a.go", "a b/c.txt", "con_ference.txt", "console.log", "a/../b", "dir/.hidden"}
	for _, p := range good {
		if err := lexicalCheck(p, true); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	for _, p := range []string{`C:\work\a.go`, `c:/work/a.go`} {
		if err := lexicalCheck(p, true); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	if !protectedRel(".git/hooks/pre-commit") || !protectedRel("a/.GIT/config") || !protectedRel("id_rsa.pub") || protectedRel("src/gitignore.txt") || protectedRel("docs/.env.example") {
		t.Error("protectedRel")
	}
	_ = errors.New
}

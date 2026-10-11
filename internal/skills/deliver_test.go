package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/memory"
)

func acceptedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, map[string]string{
		".aet/skills/d1-migrations/SKILL.md": skillMD("d1-migrations", "D1 migration rules", metaLines("migrations/**/*.sql,wrangler.jsonc", "Database,Backend"), d1Skill),
		".aet/skills/api-style/SKILL.md":     skillMD("api-style", "API handlers", metaLines("src/api/**", ""), "Return typed errors."),
		".aet/skills/everywhere/SKILL.md":    skillMD("everywhere", "Applies to all paths", "", "Keep commits small."),
		".aet/skills/test-names/SKILL.md":    skillMD("test-names", "Test naming", metaLines("**/*.test.ts", "QA"), "Name tests after behavior."),
	})
	if res, err := e.s.ScanRepo(context.Background(), "p1"); err != nil || res.Accepted != 4 {
		t.Fatalf("%v %+v", err, res)
	}
	return e
}

func TestPlanClaudeRulesAreNativePathScopedAndRoleFiltered(t *testing.T) {
	e := acceptedEnv(t)
	plan, err := e.s.Plan(context.Background(), Target{ProjectID: "p1", Role: "Backend", Adapter: "claude", Delivery: DeliveryAll})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]PlannedFile{}
	for _, f := range plan.Files {
		byPath[f.Path] = f
		if f.Method != MethodClaudeRules || f.Fidelity != FidelityExact {
			t.Errorf("%+v", f)
		}
	}
	if len(byPath) != 3 || byPath[".claude/rules/test-names.md"].Path != "" {
		t.Fatalf("role Backend gets d1-migrations, api-style, everywhere but not the QA skill: %v", plan.Files)
	}
	d1 := byPath[".claude/rules/d1-migrations.md"].Content
	if !strings.HasPrefix(d1, "---\npaths:\n") {
		t.Fatalf("front matter first:\n%s", d1)
	}
	var fm struct {
		Paths []string `yaml:"paths"`
	}
	end := strings.Index(d1[4:], "\n---")
	if err := yaml.Unmarshal([]byte(d1[4:4+end]), &fm); err != nil || len(fm.Paths) != 2 || fm.Paths[0] != "migrations/**/*.sql" || fm.Paths[1] != "wrangler.jsonc" {
		t.Fatalf("paths: %v %v", fm.Paths, err)
	}
	if !strings.Contains(d1, "<!-- drydock:generated skill=d1-migrations source=repo@") || !strings.HasSuffix(d1, d1Skill+"\n") {
		t.Fatalf("marker and body:\n%s", d1)
	}
	if ev := byPath[".claude/rules/everywhere.md"].Content; strings.HasPrefix(ev, "---") {
		t.Fatalf("a skill with no globs has no paths: key (it always loads):\n%s", ev)
	}
	// glob braces survive YAML
	e.gitRun("rm", "-rq", ".aet/skills/api-style")
	writeFiles(t, e.repo, map[string]string{".aet/skills/braces/SKILL.md": skillMD("braces", "b", metaLines("src/**/*.{ts,tsx}", ""), "x")})
	e.gitRun("add", "-A")
	e.gitRun("commit", "-qm", "braces")
	e.s.ScanRepo(context.Background(), "p1")
	plan, _ = e.s.Plan(context.Background(), Target{ProjectID: "p1", Role: "Backend", Adapter: "", Delivery: DeliveryAll})
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, "braces.md") && !strings.Contains(f.Content, "src/**/*.{ts,tsx}") {
			t.Fatalf("%s", f.Content)
		}
	}
	// no unassigned-role leak: a role-less run only sees skills without roles
	plan, _ = e.s.Plan(context.Background(), Target{ProjectID: "p1", Role: "", Adapter: "claude", Delivery: DeliveryAll})
	for _, f := range plan.Files {
		if strings.Contains(f.Path, "d1-migrations") || strings.Contains(f.Path, "test-names") {
			t.Fatalf("role-scoped skills need a matching role: %v", f.Path)
		}
	}
}

// asImported marks accepted skills as imported from a registry (a test shortcut for Import, which needs a registry fixture):
// with DeliveryRelevant, origin decides whether a skill without matching globs loads.
func asImported(t *testing.T, e *env, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := e.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE skills SET origin='registry' WHERE project_id='p1' AND name=?`, n)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// With DeliveryRelevant, Claude gets in its instructions the repository's own skills and the imported ones whose globs match
// the task's files; every other imported skill is one index line (name, description, globs, file) and a file outside
// .claude/rules that it reads when the work needs it.
func TestPlanClaudeRelevantPutsOnlyMatchingSkillsIntoTheTurn(t *testing.T) {
	e := acceptedEnv(t)
	ctx := context.Background()
	asImported(t, e, "api-style", "d1-migrations", "everywhere")
	plan, err := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "claude", Delivery: DeliveryRelevant, Hints: []string{"src/api/users.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]PlannedFile{}
	for _, f := range plan.Files {
		byPath[f.Path] = f
	}
	if len(plan.Files) != 3 {
		t.Fatalf("api-style as a rule, d1-migrations and everywhere on demand: %v", plan.Files)
	}
	rule := byPath[".claude/rules/api-style.md"]
	if rule.Method != MethodClaudeRules || rule.Fidelity != FidelityRunStart || !strings.Contains(rule.Content, "Return typed errors.") {
		t.Fatalf("%+v", rule)
	}
	for _, name := range []string{"d1-migrations", "everywhere"} {
		f := byPath[OnDemandDir+"/"+name+".md"]
		if f.Method != MethodClaudeOnDemand || f.Fidelity != FidelityOnDemand || !strings.Contains(f.Content, "drydock:generated skill="+name) {
			t.Fatalf("%s: %+v", name, f)
		}
		if strings.HasPrefix(f.Content, "---") {
			t.Fatalf("an on-demand file has no front matter: it is read, not loaded:\n%s", f.Content)
		}
	}
	if !strings.Contains(byPath[OnDemandDir+"/d1-migrations.md"].Content, d1Skill) {
		t.Fatal("the on-demand file carries the whole skill")
	}
	if strings.Join(plan.OnDemand, ",") != "d1-migrations,everywhere" || !plan.Index {
		t.Fatalf("%+v", plan)
	}
	for _, want := range []string{"Read tool", "**d1-migrations** (`.claude/drydock-skills/d1-migrations.md`): D1 migration rules (for migrations/**/*.sql, wrangler.jsonc)",
		"**everywhere** (`.claude/drydock-skills/everywhere.md`): Applies to all paths"} {
		if !strings.Contains(plan.Note, want) {
			t.Errorf("index lacks %q:\n%s", want, plan.Note)
		}
	}
	if strings.Contains(plan.Note, "api-style") || strings.Contains(plan.Note, d1Skill) {
		t.Fatalf("the index lists only on-demand skills, never a body:\n%s", plan.Note)
	}
	// no file hints: no imported skill can be matched, so every imported skill is on demand
	plan, _ = e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "claude", Delivery: DeliveryRelevant})
	for _, f := range plan.Files {
		if f.Method != MethodClaudeOnDemand {
			t.Fatalf("without hints: %+v", f)
		}
	}
	// "all" keeps every skill as a rule; an unknown or empty value means the default, relevant
	if DeliveryOf("") != DeliveryRelevant || DeliveryOf("bogus") != DeliveryRelevant {
		t.Fatal("the default is relevant")
	}
	plan, _ = e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "claude", Delivery: DeliveryAll, Hints: []string{"src/api/users.ts"}})
	if len(plan.Files) != 3 || plan.Index || plan.Note != "" || len(plan.OnDemand) != 0 {
		t.Fatalf("all: %+v", plan)
	}
	for _, f := range plan.Files {
		if f.Method != MethodClaudeRules {
			t.Fatalf("all: %+v", f)
		}
	}
	// other CLIs ignore the setting
	cx, _ := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "codex", Delivery: DeliveryAll, Hints: []string{"src/api/users.ts"}})
	if len(cx.OnDemand) != 1 {
		t.Fatalf("%+v", cx)
	}
}

// The repository's own skills (.aet/skills) were written for this codebase: with "relevant" one without globs is in every
// turn (fidelity exact, as under "all"); one its author scoped with paths loads when the task names no files or names one
// that matches (run_start), and is listed when the task's files match none of its paths. An imported skill loads only on
// a match. The index tells the agent which skills are loaded and which are not.
func TestPlanClaudeRelevantLoadsTheRepositorysOwnSkills(t *testing.T) {
	e := acceptedEnv(t)
	asImported(t, e, "d1-migrations")
	for _, c := range []struct {
		hints    []string
		apiStyle string // method/fidelity of the repository's api-style (globs src/api/**)
	}{
		{nil, MethodClaudeRules + "/" + FidelityRunStart},
		{[]string{"src/api/users.ts"}, MethodClaudeRules + "/" + FidelityRunStart},
		{[]string{"README.md"}, MethodClaudeOnDemand + "/" + FidelityOnDemand},
	} {
		plan, err := e.s.Plan(context.Background(), Target{ProjectID: "p1", Role: "Backend", Adapter: "claude", Hints: c.hints})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range plan.Files {
			got[f.Skills[0]] = f.Method + "/" + f.Fidelity
		}
		if got["everywhere"] != MethodClaudeRules+"/"+FidelityExact || got["api-style"] != c.apiStyle || got["d1-migrations"] != MethodClaudeOnDemand+"/"+FidelityOnDemand {
			t.Fatalf("hints %v: %v", c.hints, got)
		}
		if !plan.Index || !strings.Contains(plan.Note, "not loaded into your instructions (the ones that are appear as rules above)") || !strings.Contains(plan.Note, "**d1-migrations**") {
			t.Fatalf("hints %v: %q", c.hints, plan.Note)
		}
	}
}

func TestPlanForCLIsWithoutNativeScoping(t *testing.T) {
	e := acceptedEnv(t)
	ctx := context.Background()
	// Gemini: nested GEMINI.md for directory-anchored globs, the rest on demand
	gem, _ := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "gemini"})
	paths := map[string]PlannedFile{}
	for _, f := range gem.Files {
		paths[f.Path] = f
	}
	if len(gem.Files) != 1 || paths["src/api/GEMINI.md"].Method != MethodNestedFile || paths["src/api/GEMINI.md"].Fidelity != FidelityExact {
		t.Fatalf("%v", gem.Files)
	}
	if !strings.Contains(paths["src/api/GEMINI.md"].Content, "drydock:generated") || !strings.Contains(paths["src/api/GEMINI.md"].Content, "Return typed errors.") {
		t.Fatal(paths["src/api/GEMINI.md"].Content)
	}
	// d1-migrations has a glob with no directory (wrangler.jsonc) and "everywhere" has none: a nested file would scope them
	// wrongly, so they stay on demand through the skills tool
	if strings.Join(gem.OnDemand, ",") != "d1-migrations,everywhere" || !strings.Contains(gem.Note, "for_paths") {
		t.Fatalf("%+v", gem)
	}
	// Goose: nested AGENTS.md
	gs, _ := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "goose"})
	for _, f := range gs.Files {
		if !strings.HasSuffix(f.Path, "/AGENTS.md") {
			t.Fatalf("%s", f.Path)
		}
	}
	// Codex: run-start selection by task file hints into .agents/skills, the rest on demand
	cx, _ := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "codex", Hints: []string{"src/api/users.ts", "README.md"}})
	cp := map[string]PlannedFile{}
	for _, f := range cx.Files {
		cp[f.Path] = f
		if f.Method != MethodAgentsSkills || f.Fidelity != FidelityRunStart {
			t.Errorf("%+v", f)
		}
	}
	if len(cp) != 2 || cp[".agents/skills/api-style/SKILL.md"].Path == "" || cp[".agents/skills/everywhere/SKILL.md"].Path == "" {
		t.Fatalf("hint-matched and global skills are copied at run start: %v", cx.Files)
	}
	if len(cx.OnDemand) != 1 || cx.OnDemand[0] != "d1-migrations" || !strings.Contains(cx.Note, "for_paths") {
		t.Fatalf("%+v", cx)
	}
	// the copied SKILL.md is itself spec-valid
	ps, err := ParseSkill([]byte(cp[".agents/skills/api-style/SKILL.md"].Content), "api-style")
	if err != nil || len(ps.Globs) != 1 || ps.Globs[0] != "src/api/**" {
		t.Fatalf("%v %+v", err, ps)
	}
	// with no hints only global skills are copied
	cx2, _ := e.s.Plan(ctx, Target{ProjectID: "p1", Role: "Backend", Adapter: "opencode"})
	if len(cx2.Files) != 1 || cx2.Files[0].Skills[0] != "everywhere" {
		t.Fatalf("%v", cx2.Files)
	}
}

// The context engine writes the planned files into a real worktree through its hooks, keeps the repository's own files, and
// the files stay out of git.
func TestContextEngineWritesRulesIntoRealWorktree(t *testing.T) {
	e := acceptedEnv(t)
	ctx := context.Background()
	repo, err := git.Open(ctx, e.repo)
	if err != nil {
		t.Fatal(err)
	}
	w, err := repo.CreateWorktree(ctx, git.WorktreeReq{TaskID: "task_1", Title: "t", Parent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var delivered []string
	eng := &ctxeng.Engine{DB: e.db, Memory: memory.Store{DB: e.db}}
	eng.Files = func(ctx context.Context, projectID, worktree string) ([]ctxeng.ExtraFile, error) {
		plan, err := e.s.Plan(ctx, Target{ProjectID: projectID, TaskID: "task_1", AgentID: "agt_1", Role: "Backend", Adapter: "claude", Delivery: DeliveryAll})
		if err != nil {
			return nil, err
		}
		var out []ctxeng.ExtraFile
		for _, f := range plan.Files {
			f := f
			out = append(out, ctxeng.ExtraFile{Path: f.Path, Content: f.Content, Source: "skill:" + f.Skills[0], Reason: "skill " + f.Method,
				Done: func(ctx context.Context, written bool) {
					if written {
						delivered = append(delivered, f.Skills[0])
						e.s.RecordDelivery(ctx, Target{ProjectID: projectID, TaskID: "task_1", AgentID: "agt_1", Role: "Backend"}, "", f.Skills[0], f.Hash, f.Method, f.Fidelity, f.Path)
					}
				}})
		}
		return out, nil
	}
	res, m, err := eng.Write(ctx, "p1", w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Extras) != 3 || len(delivered) != 3 {
		t.Fatalf("%+v delivered=%v", res, delivered)
	}
	b, err := readFile(filepath.Join(w.Path, ".claude", "rules", "d1-migrations.md"))
	if err != nil || !strings.Contains(b, "migrations/**/*.sql") {
		t.Fatalf("%v %s", err, b)
	}
	foundManifest := false
	for _, it := range m.Items {
		if it.Source == "skill:d1-migrations" && it.Included {
			foundManifest = true
		}
	}
	if !foundManifest {
		t.Fatalf("the context manifest records each skill file: %+v", m.Items)
	}
	// what the pipeline does next: keep generated files out of git
	if err := repo.LocalIgnore(ctx, w, res.Written); err != nil {
		t.Fatal(err)
	}
	st, _ := repo.Status(ctx, w)
	if st.Dirty() {
		t.Fatalf("generated rules leaked into git status: %+v", st)
	}
	ds, _ := e.s.Deliveries(ctx, "p1", "task_1")
	if len(ds) != 3 || ds[0].Method != MethodClaudeRules || ds[0].Fidelity != FidelityExact {
		t.Fatalf("%+v", ds)
	}
	if len(e.events("skill.delivered")) != 3 {
		t.Fatal("skill.delivered events")
	}
	if cov, _ := e.s.TaskCoverage(ctx, "p1", "task_1"); cov.Files != 3 || cov.Tool != 0 {
		t.Fatalf("%+v", cov)
	}
	// a rejected skill disappears from the next write
	if _, err := e.s.Reject(ctx, human, "p1", "everywhere"); err != nil {
		t.Fatal(err)
	}
	res, _, _ = eng.Write(ctx, "p1", w.Path)
	if len(res.Extras) != 2 {
		t.Fatalf("%+v", res)
	}
	if _, err := readFile(filepath.Join(w.Path, ".claude", "rules", "everywhere.md")); err == nil {
		t.Fatal("a rejected skill's rule file must be removed")
	}
}

func TestSkillsToolThroughTheRealGateway(t *testing.T) {
	e := acceptedEnv(t)
	ctx := context.Background()
	// an offered (unaccepted) registry skill must never be served
	gh := newGHFixture(t)
	gh.publish("cloudflare/skills", "HEAD", sha1, fixtureTree(nil))
	e.useFixture(gh)
	if _, err := e.s.Import(ctx, human, ImportReq{ProjectID: "p1", RegistryID: "cloudflare-skills", Name: "workers-style"}); err != nil {
		t.Fatal(err)
	}
	g, err := mcp.New(mcp.Options{DataDir: filepath.Join(t.TempDir(), "gw"), DB: e.db, Bus: e.bus, Workspace: mcp.Workspace{Skills: NewProvider(e.s)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	ep, err := g.StartRun(mcp.RunConfig{Info: mcp.RunInfo{RunID: "r1", ProjectID: "p1", TaskID: "task_9", AgentID: "agt_9", Role: "Backend"}, Policy: mcp.RunPolicy{Allow: map[string][]string{"workspace": {"skills"}}}})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "cli", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: ep.URL, HTTPClient: &http.Client{Transport: bearerRT{ep.Token}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(args map[string]any) (string, bool) {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "skills", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].(*sdk.TextContent).Text, res.IsError
	}
	txt, isErr := call(map[string]any{"action": "list"})
	body, _ := mcp.ExtractBody(txt)
	var list []mcp.Skill
	if err := json.Unmarshal([]byte(body), &list); err != nil || isErr {
		t.Fatalf("%v %s", err, txt)
	}
	names := map[string]bool{}
	for _, s := range list {
		names[s.Name] = true
		if s.Body != "" {
			t.Fatal("list never carries bodies")
		}
	}
	if len(list) != 3 || names["test-names"] || names["workers-style"] {
		t.Fatalf("Backend sees accepted, role-matching skills only (not the QA one, not the merely offered one): %v", names)
	}
	txt, _ = call(map[string]any{"action": "for_paths", "paths": []string{"migrations/0002_users.sql", "README.md"}})
	body, _ = mcp.ExtractBody(txt)
	list = nil
	json.Unmarshal([]byte(body), &list)
	got := map[string]mcp.Skill{}
	for _, s := range list {
		got[s.Name] = s
	}
	if len(got) != 2 || got["d1-migrations"].Body != d1Skill || got["d1-migrations"].PinnedCommit == "" || got["everywhere"].Name == "" {
		t.Fatalf("%v", got)
	}
	txt, isErr = call(map[string]any{"action": "get", "name": "workers-style"})
	if !isErr || !strings.Contains(txt, "no accepted skill") {
		t.Fatalf("an offered skill is not served: %s", txt)
	}
	txt, isErr = call(map[string]any{"action": "get", "name": "api-style"})
	if isErr || !strings.Contains(txt, "Return typed errors.") {
		t.Fatalf("%s", txt)
	}
	// deliveries recorded: method tool, on demand, with the run's task and agent
	ds, _ := e.s.Deliveries(ctx, "p1", "task_9")
	if len(ds) != 3 {
		t.Fatalf("%+v", ds)
	}
	for _, d := range ds {
		if d.Method != MethodTool || d.Fidelity != FidelityOnDemand || d.AgentID != "agt_9" {
			t.Fatalf("%+v", d)
		}
	}
	if cov, _ := e.s.TaskCoverage(ctx, "p1", "task_9"); cov.Tool != 3 || cov.Files != 0 {
		t.Fatalf("%+v", cov)
	}
}

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

package security

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func analyze(t *testing.T, an *RuleAnalyzer, shell ShellKind, cmd string) CommandReport {
	t.Helper()
	root := posixRoot
	if an == winAn {
		root = winRoot
	}
	rep, err := an.Analyze(context.Background(), CommandInput{Raw: cmd, Shell: shell, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func rank(v Verdict) int {
	switch v {
	case VerdictAllow:
		return 0
	case VerdictAsk:
		return 1
	}
	return 2
}

func TestDecisionTable(t *testing.T) {
	tests := []struct {
		name    string
		an      *RuleAnalyzer
		shell   ShellKind
		cmd     string
		verdict Verdict
		kind    string
	}{
		{"parsed readonly", posixAn, bash, "ls", VerdictAllow, ""},
		{"parsed workspace_write inside", posixAn, bash, "touch a", VerdictAllow, ""},
		{"parsed exec_code is allowed and logged", posixAn, bash, "go test ./...", VerdictAllow, ""},
		{"parsed destructive", posixAn, bash, "rm -rf x", VerdictAsk, KindShellDestructive},
		{"parsed history rewrite", posixAn, bash, "git rebase main", VerdictAsk, KindShellDestructive},
		{"parsed force push", posixAn, bash, "git push -f", VerdictAsk, KindGitForcePush},
		{"parsed deploy hard deny", posixAn, bash, "wrangler deploy", VerdictDeny, ""},
		{"parsed deploy ask", posixAn, bash, "npm publish", VerdictAsk, KindDeploy},
		{"parsed secrets", posixAn, bash, "wrangler secret put X", VerdictAsk, KindSecretsAccess},
		{"parsed migration", posixAn, bash, "alembic upgrade head", VerdictAsk, KindDBMigration},
		{"parsed outside root", posixAn, bash, "cat /etc/hosts", VerdictAsk, KindFSOutsideWorktree},
		{"parsed unknown program", posixAn, bash, "mytool build", VerdictAsk, KindShellCommand},
		{"lexed readonly asks", winAn, psh, "Get-ChildItem", VerdictAsk, KindShellCommand},
		{"lexed workspace_write asks", winAn, psh, "Set-Content a.txt hi", VerdictAsk, KindShellCommand},
		{"lexed outside root", winAn, psh, "Get-ChildItem C:\\Windows", VerdictAsk, KindFSOutsideWorktree},
		{"lexed destructive", winAn, psh, "Remove-Item x", VerdictAsk, KindShellDestructive},
		{"opaque", posixAn, bash, "eval ls", VerdictAsk, KindShellUnanalyzable},
		{"opaque powershell", winAn, psh, "iex 'ls'", VerdictAsk, KindShellUnanalyzable},
		{"hard deny credential file", posixAn, bash, "cat ~/.claude/.credentials.json", VerdictDeny, ""},
		{"hard deny execution policy", winAn, psh, "Set-ExecutionPolicy Bypass", VerdictDeny, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(analyze(t, tc.an, tc.shell, tc.cmd))
			if d.Verdict != tc.verdict || (tc.kind != "" && d.Kind != tc.kind) {
				t.Fatalf("%q = %+v, want %s %s", tc.cmd, d, tc.verdict, tc.kind)
			}
			if d.Verdict == VerdictAsk && tc.kind == KindShellUnanalyzable && d.Remember {
				t.Error("unanalyzable commands are never remembered")
			}
		})
	}
	// logged flag
	if d := Decide(analyze(t, posixAn, bash, "go test ./...")); !d.Log {
		t.Error("exec_code must be logged")
	}
	if d := Decide(analyze(t, posixAn, bash, "ls")); d.Log {
		t.Error("readonly need not be logged")
	}
}

func TestKindsCarrySpecificity(t *testing.T) {
	d := Decide(analyze(t, posixAn, bash, "git push --force origin main"))
	if d.Kind != KindGitForcePush || d.Remember {
		t.Fatalf("force push must not be remembered: %+v", d)
	}
	d = Decide(analyze(t, posixAn, bash, "rm -rf /"))
	if d.Kind != KindShellDestructive || !contains(d.Kinds, KindFSOutsideWorktree) {
		t.Fatalf("outside-root deletion: %+v", d)
	}
	d = Decide(analyze(t, posixAn, bash, "terraform apply"))
	if d.Kind != KindInfraChange {
		t.Fatalf("terraform kind: %+v", d)
	}
	for _, cmd := range []string{"git push origin main", "rm x", "mytool build"} {
		if d := Decide(analyze(t, posixAn, bash, cmd)); !d.Remember {
			t.Errorf("%q should be rememberable: %+v", cmd, d)
		}
	}
	for _, cmd := range []string{"npm publish", "wrangler secret put X", "git branch -D x", "cat /etc/hosts", "eval x"} {
		if d := Decide(analyze(t, posixAn, bash, cmd)); d.Remember {
			t.Errorf("%q must not be rememberable: %+v", cmd, d)
		}
	}
}

func mustPolicy(t *testing.T, spec PolicySpec) *Policy {
	t.Helper()
	p, err := NewPolicy(spec)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStoredRules(t *testing.T) {
	p := mustPolicy(t, PolicySpec{Stored: []StoredRule{
		{Program: "mytool", Args: []string{"build"}},
		{Program: "get-childitem", Args: []string{"**"}},
		{Program: "git", Args: []string{"push", "origin", "*"}},
		{Program: "rm", Args: []string{"-rf", "build"}},
		{Program: "npm", Args: []string{"publish"}},
		{Program: "eval", Args: []string{"**"}},
		{Program: "wrangler", Args: []string{"**"}},
		{Program: "cat", Args: []string{"**"}},
		{Program: "git", Args: []string{"push", "--force", "**"}},
		{Program: "tool", Args: []string{"*"}},
	}})
	dec := func(an *RuleAnalyzer, shell ShellKind, cmd string) CommandDecision {
		return p.Decide(analyze(t, an, shell, cmd))
	}

	if d := dec(posixAn, bash, "mytool build"); d.Verdict != VerdictAllow || d.Stored != 0 || !d.Log {
		t.Errorf("exact normalized form: %+v", d)
	}
	if d := dec(posixAn, bash, `"mytool"   'build'`); d.Verdict != VerdictAllow {
		t.Errorf("rules match the normalized form, not the raw text: %+v", d)
	}
	for _, cmd := range []string{"mytool build extra", "mytool", "mytool $X", "mytool *", "othertool build", "mytool build > ../x", "mytool build ../x"} {
		if d := dec(posixAn, bash, cmd); d.Verdict == VerdictAllow {
			t.Errorf("%q must not be allowed by the stored rule: %+v", cmd, d)
		}
	}
	if d := dec(winAn, psh, "Get-ChildItem -Recurse src"); d.Verdict != VerdictAllow {
		t.Errorf("lexed command with a matching rule: %+v", d)
	}
	if d := dec(winAn, psh, "Get-ChildItem C:\\Windows"); d.Verdict == VerdictAllow {
		t.Errorf("path scope is the worktree: %+v", d)
	}
	if d := dec(posixAn, bash, "git push origin feature-1"); d.Verdict != VerdictAllow {
		t.Errorf("git_push may be remembered: %+v", d)
	}
	if d := dec(posixAn, bash, "rm -rf build"); d.Verdict != VerdictAllow {
		t.Errorf("shell_destructive may be remembered by exact form: %+v", d)
	}
	if d := dec(posixAn, bash, "rm -rf build2"); d.Verdict == VerdictAllow {
		t.Errorf("a different target is not covered: %+v", d)
	}
	// never remembered, however the rules are written
	for _, cmd := range []string{"npm publish", "git push --force origin main", "wrangler secret put X", "wrangler versions upload", "cat /etc/hosts"} {
		if d := dec(posixAn, bash, cmd); d.Verdict == VerdictAllow {
			t.Errorf("%q was allowed by a stored rule: %+v", cmd, d)
		}
	}
	// hard deny and opaque are untouchable
	for _, cmd := range []string{"wrangler deploy", "eval ls", "cat ~/.claude/.credentials.json"} {
		if d := dec(posixAn, bash, cmd); d.Verdict == VerdictAllow {
			t.Errorf("invariant lowered for %q: %+v", cmd, d)
		}
	}
	// a wrapper needs a rule too
	if d := dec(posixAn, bash, "env A=b mytool build"); d.Verdict == VerdictAllow {
		t.Errorf("partial coverage of a wrapper chain: %+v", d)
	}
	// a nil policy is the plain table
	if d := (*Policy)(nil).Decide(analyze(t, posixAn, bash, "mytool build")); d.Verdict != VerdictAsk {
		t.Errorf("nil policy: %+v", d)
	}
	// "tool *" does not match dynamic or glob arguments
	if d := dec(posixAn, bash, "tool $X"); d.Verdict == VerdictAllow {
		t.Errorf("wildcard matched a dynamic argument: %+v", d)
	}
	if d := dec(posixAn, bash, "tool v1"); d.Verdict != VerdictAllow {
		t.Errorf("wildcard literal: %+v", d)
	}
}

func TestStoredRuleValidation(t *testing.T) {
	if _, err := NewPolicy(PolicySpec{Stored: []StoredRule{{}}}); err == nil {
		t.Error("rule without program accepted")
	}
	if _, err := NewPolicy(PolicySpec{Stored: []StoredRule{{Program: "x", Args: []string{"**", "y"}}}}); err == nil {
		t.Error("** in the middle accepted")
	}
}

func TestCELRestrictions(t *testing.T) {
	p := mustPolicy(t, PolicySpec{Restrictions: []Restriction{
		{Name: "no-auto-exec", Expr: `"exec_code" in classes && confidence == "parsed_exact"`, Effect: VerdictAsk},
		{Name: "no-git-network", Expr: `programs.exists(p, p == "git") && "network" in classes`, Effect: VerdictDeny},
	}})
	if d := p.Decide(analyze(t, posixAn, bash, "go test ./...")); d.Verdict != VerdictAsk || d.Kind != KindShellCommand || len(d.Policy) != 1 || d.Log {
		t.Errorf("restriction raises Allow to Ask: %+v", d)
	}
	if d := p.Decide(analyze(t, posixAn, bash, "git fetch origin")); d.Verdict != VerdictDeny || d.Remember {
		t.Errorf("restriction denies: %+v", d)
	}
	if d := p.Decide(analyze(t, posixAn, bash, "git status")); d.Verdict != VerdictAllow || len(d.Policy) != 0 {
		t.Errorf("unrelated command: %+v", d)
	}
	if d := p.Decide(analyze(t, posixAn, bash, "rm x")); d.Verdict != VerdictAsk || d.Kind != KindShellDestructive {
		t.Errorf("an Ask keeps its kind when no restriction applies: %+v", d)
	}
}

func TestCELCannotLowerInvariants(t *testing.T) {
	// the strongest restriction-only policy: everything matches
	all := mustPolicy(t, PolicySpec{Restrictions: []Restriction{{Name: "all", Expr: "true", Effect: VerdictAsk}}})
	none := mustPolicy(t, PolicySpec{Restrictions: []Restriction{{Name: "none", Expr: "false", Effect: VerdictDeny}}})
	// opaque stays Ask(shell_unanalyzable) and hard deny stays Deny
	for _, p := range []*Policy{all, none, nil} {
		if d := p.Decide(analyze(t, posixAn, bash, "eval ls")); d.Verdict != VerdictAsk || d.Kind != KindShellUnanalyzable {
			t.Errorf("opaque: %+v", d)
		}
		if d := p.Decide(analyze(t, posixAn, bash, "wrangler deploy")); d.Verdict != VerdictDeny {
			t.Errorf("hard deny: %+v", d)
		}
	}
	// policy cannot grant: effect allow is not representable
	if _, err := NewPolicy(PolicySpec{Restrictions: []Restriction{{Name: "x", Expr: "true", Effect: VerdictAllow}}}); err == nil {
		t.Error("an allow restriction was accepted")
	}
	if _, err := NewPolicy(PolicySpec{Restrictions: []Restriction{{Name: "x", Expr: "true", Effect: Verdict("")}}}); err == nil {
		t.Error("an empty effect was accepted")
	}
	// property: no restriction-only policy is ever more permissive than the plain table
	cmds := []struct {
		an    *RuleAnalyzer
		shell ShellKind
		cmd   string
	}{
		{posixAn, bash, "ls"}, {posixAn, bash, "rm -rf x"}, {posixAn, bash, "eval x"}, {posixAn, bash, "wrangler deploy"}, {posixAn, bash, "curl https://x"},
		{posixAn, bash, "go test ./..."}, {posixAn, bash, "git push -f"}, {posixAn, bash, "cat /etc/passwd"}, {winAn, psh, "Get-Date"}, {winAn, psh, "iex $x"},
		{winAn, cmdx, "del x"}, {winAn, cmdx, "echo %X%"}, {posixAn, bash, "mytool"}, {posixAn, bash, "cat ~/.codex/auth.json"},
	}
	policies := []*Policy{all, none, mustPolicy(t, PolicySpec{Restrictions: []Restriction{
		{Name: "a", Expr: `confidence != "parsed_exact"`, Effect: VerdictAsk}, {Name: "b", Expr: `"network" in classes`, Effect: VerdictDeny}}}),
		mustPolicy(t, PolicySpec{Stored: []StoredRule{{Program: "ls", Args: []string{"**"}}}, Restrictions: []Restriction{{Name: "c", Expr: `!inside`, Effect: VerdictAsk}}})}
	for _, c := range cmds {
		rep := analyze(t, c.an, c.shell, c.cmd)
		base := Decide(rep)
		for i, p := range policies {
			got := p.Decide(rep)
			if rank(got.Verdict) < rank(base.Verdict) {
				t.Errorf("policy %d lowered %q from %s to %s", i, c.cmd, base.Verdict, got.Verdict)
			}
		}
	}
}

func TestCELEvaluationFailsClosed(t *testing.T) {
	// classes[5] is out of range for every report: the evaluation error counts as a hit
	p := mustPolicy(t, PolicySpec{Restrictions: []Restriction{{Name: "boom", Expr: `classes[5] == "x"`, Effect: VerdictAsk}}})
	if d := p.Decide(analyze(t, posixAn, bash, "ls")); d.Verdict != VerdictAsk {
		t.Fatalf("evaluation error did not restrict: %+v", d)
	}
}

func TestCELValidation(t *testing.T) {
	bad := []Restriction{
		{Name: "syntax", Expr: `classes ==`, Effect: VerdictAsk},
		{Name: "undeclared", Expr: `nosuchvar == 1`, Effect: VerdictAsk},
		{Name: "not bool", Expr: `confidence`, Effect: VerdictAsk},
		{Name: "type error", Expr: `classes + 1 == 2`, Effect: VerdictAsk},
		{Name: "too long", Expr: strings.Repeat("true && ", 400) + "true", Effect: VerdictAsk},
	}
	for _, r := range bad {
		if _, err := NewPolicy(PolicySpec{Restrictions: []Restriction{r}}); err == nil {
			t.Errorf("%s: accepted", r.Name)
		}
	}
}

func TestDescribeCommandFeedsApprovals(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	cmd := "git push --force origin main"
	in := CommandInput{Raw: cmd, Shell: bash, Root: posixRoot}
	rep := analyze(t, posixAn, bash, cmd)
	dec := Decide(rep)
	if dec.Verdict != VerdictAsk || dec.Kind != KindGitForcePush {
		t.Fatalf("%+v", dec)
	}
	act := DescribeCommand(in, rep, dec.Kind)
	act.RunID, act.NodeID, act.Step, act.Attempt = "run_1", "n1", 2, 1
	id, err := r.apr.Request(ctx, act, "agent wants to force push")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	// the executor re-analyzes what it is about to run and rebuilds the descriptor
	again := DescribeCommand(in, analyze(t, posixAn, bash, cmd), dec.Kind)
	again.RunID, again.NodeID, again.Step, again.Attempt = "run_1", "n1", 2, 1
	// different command, same approval id: refused
	other := "git push --force origin release"
	otherAct := DescribeCommand(CommandInput{Raw: other, Shell: bash, Root: posixRoot}, analyze(t, posixAn, bash, other), dec.Kind)
	otherAct.RunID, otherAct.NodeID, otherAct.Step, otherAct.Attempt = "run_1", "n1", 2, 1
	if err := r.apr.Consume(ctx, id, otherAct); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("different push target: %v", err)
	}
	if err := r.apr.Consume(ctx, id, again); err != nil {
		t.Fatalf("the approved command: %v", err)
	}
}

func TestRulesLoadAndAreWellFormed(t *testing.T) {
	rs, err := LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	validKinds := set(KindFSDelete, KindFSOutsideWorktree, KindGitForcePush, KindGitBranchDelete, KindGitPush, KindDeploy, KindDBMigration, KindSecretsAccess,
		KindInfraChange, KindShellDestructive, KindShellUnanalyzable, KindShellCommand)
	classesSeen := map[Class]int{}
	for _, r := range rs.Rules {
		if r.Kind != "" && !validKinds[r.Kind] {
			t.Errorf("rule %s: unknown kind %q", r.ID, r.Kind)
		}
		classesSeen[r.class]++
		for _, p := range r.Programs {
			if p != strings.ToLower(p) {
				t.Errorf("rule %s: program %q is not lower case", r.ID, p)
			}
		}
		if !strings.HasPrefix(r.ID, string(r.class)+".") && r.class != classHardDeny {
			t.Errorf("rule %s does not start with its class %s", r.ID, r.class)
		}
	}
	for _, c := range []Class{ClassReadonly, ClassWorkspaceWrite, ClassDestructive, ClassNetwork, ClassExecCode, ClassDeploy, ClassSecrets, ClassDBMigration, ClassGitHistoryRewrite, classHardDeny} {
		if classesSeen[c] == 0 {
			t.Errorf("no rules for class %s", c)
		}
	}
	if len(rs.Aliases) < 10 || rs.Aliases["rm"] != "remove-item" || rs.Aliases["iex"] != "invoke-expression" || rs.Aliases["curl"] != "invoke-webrequest" || rs.Aliases["saps"] != "start-process" {
		t.Errorf("alias table missing the doc's entries: %v", rs.Aliases)
	}
	for _, a := range []string{"ri", "del", "erase", "rd", "rmdir"} {
		if rs.Aliases[a] != "remove-item" {
			t.Errorf("alias %s", a)
		}
	}
	for _, a := range []string{"iwr", "wget"} {
		if rs.Aliases[a] != "invoke-webrequest" {
			t.Errorf("alias %s", a)
		}
	}
	// the opaque list names the doc's cmdlets
	for _, c := range []string{"invoke-expression", "add-type", "start-process", "invoke-command", "start-job"} {
		if !rs.OpaqueCmdlets[c] {
			t.Errorf("%s must be opaque", c)
		}
	}
	// every table file says it is illustrative
	ents, _ := rulesFS.ReadDir("rules")
	for _, e := range ents {
		b, _ := rulesFS.ReadFile("rules/" + e.Name())
		if !strings.Contains(string(b), "ILLUSTRATIVE") && !strings.Contains(string(b), "illustrative") {
			t.Errorf("%s is not labelled illustrative", e.Name())
		}
	}
	if len(rs.RuleIDs()) < 80 {
		t.Errorf("only %d rules", len(rs.RuleIDs()))
	}
}

func TestRulesRejectMistakes(t *testing.T) {
	good := "class: readonly\nrules:\n  - id: readonly.x\n    programs: [ls]\n"
	cases := map[string]string{
		"unknown field":   good + "    bogus: 1\n",
		"unknown class":   "class: weird\nrules:\n  - id: weird.x\n    programs: [ls]\n",
		"no programs":     "class: readonly\nrules:\n  - id: readonly.x\n",
		"no id":           "class: readonly\nrules:\n  - programs: [ls]\n",
		"bad regex":       good + "    args_regex: ['(']\n",
		"bad paths mode":  good + "    paths: everywhere\n",
		"bad shell":       good + "    shells: [zsh]\n",
		"bad also":        good + "    also_classes: [nonsense]\n",
		"bad secret path": good + "secret_paths: ['(']\n",
	}
	for name, body := range cases {
		fsys := fstest.MapFS{"rules/a.yaml": {Data: []byte(body)}}
		if _, err := loadRulesFrom(fsys, "rules"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	dup := fstest.MapFS{"rules/a.yaml": {Data: []byte(good)}, "rules/b.yaml": {Data: []byte(good)}}
	if _, err := loadRulesFrom(dup, "rules"); err == nil {
		t.Error("duplicate rule ids accepted")
	}
	if _, err := loadRulesFrom(fstest.MapFS{"rules/a.yaml": {Data: []byte(good)}}, "rules"); err != nil {
		t.Errorf("good table rejected: %v", err)
	}
}

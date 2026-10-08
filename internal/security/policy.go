package security

import (
	"fmt"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
)

// Verdict is the analyzer decision.
type Verdict string

// Verdicts.
const (
	VerdictAllow Verdict = "allow"
	VerdictAsk   Verdict = "ask"
	VerdictDeny  Verdict = "deny"
)

// CommandDecision is the output of the decision table (docs/security.md s7): {Allow, Ask(kind), Deny}.
type CommandDecision struct {
	Verdict  Verdict  `json:"verdict"`
	Kind     string   `json:"kind,omitempty"`  // primary approval kind when Verdict is ask
	Kinds    []string `json:"kinds,omitempty"` // every approval kind that applies
	Reasons  []string `json:"reasons,omitempty"`
	Remember bool     `json:"remember"`      // a stored "always allow" rule may be offered for this action
	Log      bool     `json:"log,omitempty"` // allowed, but the action must be logged (exec_code)
	Stored   int      `json:"stored"`        // index of the stored rule that allowed it, or -1
	Policy   []string `json:"policy,omitempty"`
}

// neverRemember kinds are not offered as stored allow rules (docs/security.md s5: rules 6 and the table).
var neverRemember = set(KindGitForcePush, KindGitBranchDelete, KindDeploy, KindSecretsAccess, KindShellUnanalyzable, KindFSOutsideWorktree)

// kindPriority orders kinds for the primary kind: most specific first.
var kindPriority = []string{KindGitForcePush, KindGitBranchDelete, KindDeploy, KindDBMigration, KindSecretsAccess, KindInfraChange, KindFSDelete,
	KindShellDestructive, KindFSOutsideWorktree, KindShellUnanalyzable, KindShellCommand}

func classKind(c Class) string {
	switch c {
	case ClassDestructive, ClassGitHistoryRewrite:
		return KindShellDestructive
	case ClassDeploy:
		return KindDeploy
	case ClassSecrets:
		return KindSecretsAccess
	case ClassDBMigration:
		return KindDBMigration
	}
	return ""
}

// StoredRule is an "always allow" rule. It matches on the normalized form (program and argument pattern) and is scoped
// to the worktree: it never matches a command with a path outside the root, a dynamic argument, an Opaque report, a
// hard-deny hit, or a kind that is never remembered.
type StoredRule struct {
	Program string   `json:"program"` // normalized program name
	Args    []string `json:"args"`    // one pattern per argument; "*" matches any one literal argument; a final "**" matches the rest
}

// Restriction is a stored CEL expression that can only restrict (docs/security.md s7, "Stored policy interplay").
type Restriction struct {
	Name   string  `json:"name"`
	Expr   string  `json:"expr"`   // CEL, boolean
	Effect Verdict `json:"effect"` // ask or deny; allow is not accepted
}

// PolicySpec is the serializable stored policy of a project.
type PolicySpec struct {
	Stored       []StoredRule  `json:"stored"`
	Restrictions []Restriction `json:"restrictions"`
}

// Policy applies stored rules and CEL restrictions on top of the invariant decision table.
type Policy struct {
	stored []StoredRule
	rs     []compiledRestriction
}

type compiledRestriction struct {
	Restriction
	prg cel.Program
}

const maxCELExprBytes = 2 << 10

// NewPolicy compiles a PolicySpec. A nil *Policy is valid and means "no stored rules, no restrictions".
func NewPolicy(spec PolicySpec) (*Policy, error) {
	env, err := cel.NewEnv(
		cel.Variable("confidence", cel.StringType),
		cel.Variable("shell", cel.StringType),
		cel.Variable("classes", cel.ListType(cel.StringType)),
		cel.Variable("programs", cel.ListType(cel.StringType)),
		cel.Variable("kinds", cel.ListType(cel.StringType)),
		cel.Variable("verdict", cel.StringType),
		cel.Variable("inside", cel.BoolType),
		cel.Variable("paths", cel.IntType),
	)
	if err != nil {
		return nil, err
	}
	p := &Policy{stored: append([]StoredRule{}, spec.Stored...)}
	for i, s := range spec.Stored {
		if s.Program == "" {
			return nil, fmt.Errorf("security: stored rule %d has no program", i)
		}
		for j, a := range s.Args {
			if a == "**" && j != len(s.Args)-1 {
				return nil, fmt.Errorf("security: stored rule %d: ** must be last", i)
			}
		}
	}
	for _, rs := range spec.Restrictions {
		if rs.Effect != VerdictAsk && rs.Effect != VerdictDeny {
			return nil, fmt.Errorf("security: restriction %q: effect must be ask or deny; policy can only restrict", rs.Name)
		}
		if len(rs.Expr) > maxCELExprBytes {
			return nil, fmt.Errorf("security: restriction %q: expression too long", rs.Name)
		}
		ast, iss := env.Compile(rs.Expr)
		if iss != nil && iss.Err() != nil {
			return nil, fmt.Errorf("security: restriction %q: %w", rs.Name, iss.Err())
		}
		if ast.OutputType() != cel.BoolType {
			return nil, fmt.Errorf("security: restriction %q: expression must be boolean", rs.Name)
		}
		prg, err := env.Program(ast, cel.CostLimit(100000))
		if err != nil {
			return nil, fmt.Errorf("security: restriction %q: %w", rs.Name, err)
		}
		p.rs = append(p.rs, compiledRestriction{rs, prg})
	}
	return p, nil
}

// Decide applies the decision table to a report with no stored policy.
func Decide(rep CommandReport) CommandDecision { return (*Policy)(nil).Decide(rep) }

// Decide applies the decision table. The invariants cannot be lowered by anything in the policy: a hard-deny hit is
// Deny, an Opaque report is always Ask(shell_unanalyzable) and is never matched by a stored rule.
func (p *Policy) Decide(rep CommandReport) CommandDecision {
	d := CommandDecision{Stored: -1}
	// 1. hard deny
	if len(rep.HardDeny) > 0 {
		d.Verdict = VerdictDeny
		d.Reasons = append(d.Reasons, rep.HardDeny...)
		return d
	}
	// 2. opaque
	if rep.Confidence == Opaque {
		d.Verdict, d.Kind, d.Kinds = VerdictAsk, KindShellUnanalyzable, []string{KindShellUnanalyzable}
		d.Reasons = append(d.Reasons, rep.Reasons...)
		return p.restrict(rep, d)
	}
	// 3. kinds from classes and rules
	kinds := map[string]bool{}
	for _, k := range rep.Kinds {
		kinds[k] = true
	}
	dangerous := false
	for _, c := range rep.Classes {
		if k := classKind(c); k != "" {
			kinds[k] = true
			dangerous = true
		}
	}
	for k := range kinds {
		if k != KindShellCommand && k != KindShellDestructive && kinds[k] {
			dangerous = true
		}
	}
	outside := !rep.AllInside()
	if outside {
		kinds[KindFSOutsideWorktree] = true
	}
	unknown := rep.HasClass(ClassUnknown)
	network := rep.HasClass(ClassNetwork)
	exec := rep.HasClass(ClassExecCode)
	d.Reasons = append(d.Reasons, rep.Reasons...)

	switch {
	case dangerous || outside:
		d.Verdict = VerdictAsk
	case unknown || network || rep.Confidence == LexedConservative:
		d.Verdict = VerdictAsk
		kinds[KindShellCommand] = true
	default: // ParsedExact readonly / workspace_write inside the root, plus exec_code (logged)
		d.Verdict = VerdictAllow
		d.Log = exec
		return p.restrict(rep, d)
	}
	d.Kinds, d.Kind = orderKinds(kinds)
	d.Remember = true
	for _, k := range d.Kinds {
		if neverRemember[k] {
			d.Remember = false
		}
	}
	// stored rules can turn an Ask into an Allow, never for opaque, hard-deny, or never-remembered kinds
	if d.Remember && !outside && p != nil {
		if idx, ok := p.matchStored(rep); ok {
			d.Verdict, d.Stored = VerdictAllow, idx
			d.Kind, d.Kinds = "", nil
			d.Log = true
			d.Reasons = append(d.Reasons, fmt.Sprintf("allowed by stored rule %d", idx))
		}
	}
	return p.restrict(rep, d)
}

func orderKinds(m map[string]bool) (kinds []string, primary string) {
	for _, k := range kindPriority {
		if m[k] {
			kinds = append(kinds, k)
			delete(m, k)
		}
	}
	var rest []string
	for k := range m {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	kinds = append(kinds, rest...)
	if len(kinds) > 0 {
		primary = kinds[0]
	}
	return kinds, primary
}

// matchStored: every invocation of the report is matched by a stored rule.
func (p *Policy) matchStored(rep CommandReport) (int, bool) {
	if len(rep.Invocations) == 0 || rep.Confidence == Opaque {
		return 0, false
	}
	first := -1
	for _, inv := range rep.Invocations {
		idx := -1
		for i, s := range p.stored {
			if storedMatches(s, inv) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return 0, false
		}
		if first < 0 {
			first = idx
		}
	}
	return first, true
}

func storedMatches(s StoredRule, inv Invocation) bool {
	if s.Program != inv.Program {
		return false
	}
	for _, a := range inv.Args {
		if a.Dynamic || a.Glob {
			return false
		}
	}
	for _, rd := range inv.Redirects {
		if rd.Dynamic {
			return false
		}
	}
	args := inv.Args
	for i, pat := range s.Args {
		if pat == "**" {
			return true
		}
		if i >= len(args) {
			return false
		}
		if pat != "*" && pat != args[i].Text {
			return false
		}
	}
	return len(args) == len(s.Args)
}

// restrict applies the CEL restrictions: they can raise Allow to Ask, and Allow or Ask to Deny; nothing else.
func (p *Policy) restrict(rep CommandReport, d CommandDecision) CommandDecision {
	if p == nil || len(p.rs) == 0 || d.Verdict == VerdictDeny {
		return d
	}
	progs := make([]string, 0, len(rep.Invocations))
	for _, i := range rep.Invocations {
		progs = append(progs, i.Program)
	}
	classes := make([]string, 0, len(rep.Classes))
	for _, c := range rep.Classes {
		classes = append(classes, string(c))
	}
	kinds := append([]string{}, d.Kinds...)
	vars := map[string]any{
		"confidence": rep.Confidence.String(), "shell": string(rep.Shell), "classes": classes, "programs": progs, "kinds": kinds,
		"verdict": string(d.Verdict), "inside": rep.AllInside(), "paths": int64(len(rep.Paths)),
	}
	for _, r := range p.rs {
		hit := true // fail closed: an evaluation error restricts
		if out, _, err := r.prg.Eval(vars); err == nil {
			if b, ok := out.Value().(bool); ok {
				hit = b
			}
		}
		if !hit {
			continue
		}
		d.Policy = append(d.Policy, r.Name)
		switch {
		case r.Effect == VerdictDeny:
			d.Verdict, d.Kind, d.Kinds, d.Remember, d.Log = VerdictDeny, "", nil, false, false
			d.Reasons = append(d.Reasons, "denied by policy restriction "+r.Name)
			return d
		case d.Verdict == VerdictAllow:
			d.Verdict = VerdictAsk
			d.Kind, d.Kinds = KindShellCommand, []string{KindShellCommand}
			d.Remember, d.Log = true, false
			d.Reasons = append(d.Reasons, "policy restriction "+r.Name+" requires approval")
		}
	}
	return d
}

// DescribeCommand builds the action descriptor for an analyzed shell command. The caller fills the run, node, agent
// and worktree identity before requesting an approval.
func DescribeCommand(in CommandInput, rep CommandReport, kind string) ActionDescriptor {
	a := ActionDescriptor{Kind: kind, Tool: "shell", Shell: string(in.Shell), Cwd: firstNonEmpty(in.Cwd, in.Root), Command: in.Raw, ArgvDigest: rep.Digest}
	if len(rep.Invocations) > 0 {
		a.Program = rep.Invocations[0].Program
		for _, x := range rep.Invocations[0].Args {
			a.Argv = append(a.Argv, x.Text)
		}
	}
	for _, p := range rep.Paths {
		if p.Canonical != "" {
			a.Targets = append(a.Targets, Target{Path: p.Canonical})
		} else {
			a.Targets = append(a.Targets, Target{Path: p.Raw})
		}
	}
	return a
}

// summary helps tests and the audit view.
func (d CommandDecision) String() string {
	if d.Verdict == VerdictAsk {
		return "ask(" + d.Kind + ")"
	}
	return strings.ToLower(string(d.Verdict))
}

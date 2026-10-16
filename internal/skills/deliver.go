package skills

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Delivery methods and fidelities (docs/expert-packs.md section 9, delivery table).
const (
	MethodClaudeRules  = "claude_rules"
	MethodNestedFile   = "nested_file"
	MethodAgentsSkills = "agents_skills"
	MethodTool         = "tool"
	// MethodClaudeOnDemand is a skill Claude gets as an index line in AGENTS.md and a file it reads when the work needs it
	// (DeliveryRelevant).
	MethodClaudeOnDemand = "claude_on_demand"

	FidelityExact    = "exact"
	FidelityRunStart = "run_start"
	FidelityOnDemand = "on_demand"
)

// How Claude receives the skills that reach it (DevSettings.SkillDelivery).
const (
	// DeliveryAll puts every skill into every turn as a .claude/rules file, whatever the task touches.
	DeliveryAll = "all"
	// DeliveryRelevant puts into every turn the repository's own skills (.aet/skills: written for this codebase; one with
	// globs only when the task names no files or names one they match) and the imported ones whose globs match the task's
	// file hints; every other skill is one line in AGENTS.md (name, what it is for, where its text is) and Claude reads it
	// with the Read tool when the work needs it.
	DeliveryRelevant = "relevant"
)

// OnDemandDir holds the text of the skills Claude reads on demand. It is not .claude/rules, so nothing loads it by itself.
const OnDemandDir = ".claude/drydock-skills"

// GeneratedMarker marks generated files (the same string internal/context looks for).
const GeneratedMarker = "drydock:generated"

// Target describes who a worktree is for.
type Target struct {
	ProjectID string
	TaskID    string
	AgentID   string
	Role      string
	// Adapter is the agent adapter id (claude, codex, gemini, opencode, goose, antigravity). Empty means unknown, which is
	// treated as Claude: rules for a CLI that is not used are harmless files.
	Adapter string
	// Hints are the files the task is expected to touch (task file hints); run-start selection for CLIs without native
	// scoping uses them.
	Hints []string
	// Delivery is DeliveryAll or DeliveryRelevant for Claude; empty means DefaultDelivery.
	Delivery string
}

// PlannedFile is one file to write into the worktree.
type PlannedFile struct {
	Path     string
	Content  string
	Skills   []string
	Method   string
	Fidelity string
	// Hash is the content hash of the first skill (for the delivery record).
	Hash string
}

// Plan is the delivery plan for a worktree.
type Plan struct {
	Files []PlannedFile
	// OnDemand lists eligible skills the agent fetches when it needs them: from the skills tool, or for Claude from its file in
	// OnDemandDir.
	OnDemand []string
	// Note is a sentence for the generated AGENTS.md telling a CLI without native scoping to call the skills tool; empty for
	// Claude or when there are no skills. For Claude with DeliveryRelevant it is the index of the skills read on demand.
	Note string
	// Index is true when Note is Claude's index of on-demand skills.
	Index bool
}

// family maps an adapter id to its delivery family.
func family(adapter string) string {
	a := strings.ToLower(adapter)
	switch {
	case a == "" || strings.HasPrefix(a, "claude"):
		return "claude"
	case strings.HasPrefix(a, "gemini"):
		return "gemini"
	case strings.HasPrefix(a, "goose"):
		return "goose"
	default:
		return "agents" // codex, opencode, antigravity, custom: run-start selection plus the tool
	}
}

// Plan decides which accepted skills go into the worktree and how. Only accepted skills are considered, and only those whose
// roles match the agent's role. Nothing is written here; the context engine writes the files.
func (s *Service) Plan(ctx context.Context, t Target) (Plan, error) {
	var p Plan
	eligible, err := s.Eligible(ctx, t.ProjectID, t.AgentID, t.Role)
	if err != nil {
		return p, err
	}
	if len(eligible) == 0 {
		return p, nil
	}
	switch family(t.Adapter) {
	case "claude":
		var index []Skill
		delivery := DeliveryOf(t.Delivery)
		for _, k := range eligible {
			rule := func(fidelity string) {
				p.Files = append(p.Files, PlannedFile{Path: ".claude/rules/" + k.Name + ".md", Content: claudeRule(k), Skills: []string{k.Name}, Method: MethodClaudeRules, Fidelity: fidelity, Hash: k.ContentHash})
			}
			matches := len(k.Globs) > 0 && len(t.Hints) > 0 && PathsMatch(k.Globs, t.Hints)
			switch {
			case delivery == DeliveryAll:
				rule(FidelityExact)
			case k.Origin == OriginRepo && len(k.Globs) == 0:
				// the repository's own skill (.aet/skills) was written for this codebase: it loads (a listed one was opened
				// in 2 of 6 runs, docs/evals.md section 14); without globs it is in every turn, as under "all"
				rule(FidelityExact)
			case k.Origin == OriginRepo && (len(t.Hints) == 0 || matches):
				// its author scoped it with paths: it loads unless the task names files and none of them matches
				rule(FidelityRunStart)
			case matches:
				rule(FidelityRunStart)
			default:
				p.Files = append(p.Files, PlannedFile{Path: OnDemandDir + "/" + k.Name + ".md", Content: onDemandFile(k), Skills: []string{k.Name}, Method: MethodClaudeOnDemand, Fidelity: FidelityOnDemand, Hash: k.ContentHash})
				p.OnDemand = append(p.OnDemand, k.Name)
				index = append(index, k)
			}
		}
		p.Note, p.Index = claudeIndex(index), len(index) > 0
	case "gemini", "goose":
		file := "GEMINI.md"
		if family(t.Adapter) == "goose" {
			file = "AGENTS.md"
		}
		byDir := map[string][]Skill{}
		for _, k := range eligible {
			dirs, ok := anchoredDirs(k.Globs)
			if !ok {
				p.OnDemand = append(p.OnDemand, k.Name)
				continue
			}
			for _, d := range dirs {
				byDir[d] = append(byDir[d], k)
			}
		}
		var ds []string
		for d := range byDir {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		for _, d := range ds {
			var names []string
			for _, k := range byDir[d] {
				names = append(names, k.Name)
			}
			p.Files = append(p.Files, PlannedFile{Path: d + "/" + file, Content: nestedFile(d, byDir[d]), Skills: names, Method: MethodNestedFile, Fidelity: FidelityExact, Hash: byDir[d][0].ContentHash})
		}
		p.Note = skillsNote(len(p.OnDemand) > 0 || len(p.Files) == 0)
	default:
		for _, k := range eligible {
			if PathsMatch(k.Globs, t.Hints) && (len(k.Globs) == 0 || len(t.Hints) > 0) {
				p.Files = append(p.Files, PlannedFile{Path: ".agents/skills/" + k.Name + "/SKILL.md", Content: agentsSkill(k), Skills: []string{k.Name}, Method: MethodAgentsSkills, Fidelity: FidelityRunStart, Hash: k.ContentHash})
			} else {
				p.OnDemand = append(p.OnDemand, k.Name)
			}
		}
		p.Note = skillsNote(true)
	}
	sort.Strings(p.OnDemand)
	return p, nil
}

// DefaultDelivery is how Claude receives skills when the project has not chosen. Measured on the tally suite (docs/evals.md
// section 14): with the twelve prebuilt agents' skills, "all" cost 1.9 times as much as no skills for no better acceptance;
// "relevant" cost the same as no skills.
const DefaultDelivery = DeliveryRelevant

// DeliveryOf returns the delivery in force for a setting value.
func DeliveryOf(v string) string {
	switch v {
	case DeliveryAll, DeliveryRelevant:
		return v
	}
	return DefaultDelivery
}

// claudeIndex is the AGENTS.md text listing the skills Claude reads on demand: one line each, with the file to read. Empty
// when there are none.
func claudeIndex(ks []Skill) string {
	if len(ks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The skills below are not loaded into your instructions (the loaded ones are the .claude/rules files among your " +
		"instructions): each line names a skill, says what it is for and where its text is. Before you work on something a skill " +
		"covers, read that file with the Read tool and follow it; skip the ones that have nothing to do with your task.\n")
	for _, k := range ks {
		d := oneLineText(k.Description)
		if d == "" {
			d = "no description"
		}
		if len(d) > 300 {
			d = d[:300] + "..."
		}
		if len(k.Globs) > 0 {
			d += " (for " + strings.Join(k.Globs, ", ") + ")"
		}
		b.WriteString("\n- **" + k.Name + "** (`" + OnDemandDir + "/" + k.Name + ".md`): " + d)
	}
	return b.String()
}

func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// onDemandFile renders a skill Claude reads on demand: a generator marker, the description, then the skill body.
func onDemandFile(k Skill) string {
	var b strings.Builder
	b.WriteString(marker("skill="+k.Name, "source="+provenance(k), "sha256="+shortHash(k.ContentHash)) + "\n\n")
	b.WriteString("# Skill: " + k.Name + "\n\n")
	if d := oneLineText(k.Description); d != "" {
		b.WriteString("_" + d + "_\n\n")
	}
	b.WriteString(strings.TrimSpace(k.Body) + "\n")
	return b.String()
}

func skillsNote(onDemand bool) string {
	n := "This project has glob-scoped skills (rules for parts of the codebase). Before editing files, call the skills tool with action for_paths and the paths you will touch; follow what it returns."
	if !onDemand {
		return ""
	}
	return n
}

// anchoredDirs returns the nested directories that carry a skill: every glob must be rooted in a static directory.
func anchoredDirs(globs []string) ([]string, bool) {
	if len(globs) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		d, ok := AnchorDir(g)
		if !ok {
			return nil, false
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, true
}

func marker(parts ...string) string {
	return "<!-- " + GeneratedMarker + " " + strings.Join(parts, " ") + " -->"
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func provenance(k Skill) string {
	src := k.Origin
	if k.RegistryID != "" {
		src = "registry:" + k.RegistryID
	}
	if k.PinnedCommit != "" {
		src += "@" + shortHash(k.PinnedCommit)
	}
	return src
}

// claudeRule renders .claude/rules/<name>.md: front matter with paths: (the key name is inferred from Claude Code's changelog,
// the docs page was not read: unverified), a generator marker, then the skill body. A skill with no globs has no paths: key
// and so loads always.
func claudeRule(k Skill) string {
	var b strings.Builder
	if len(k.Globs) > 0 {
		fm, _ := yaml.Marshal(map[string][]string{"paths": k.Globs})
		b.WriteString("---\n" + string(fm) + "---\n")
	}
	b.WriteString(marker("skill="+k.Name, "source="+provenance(k), "sha256="+shortHash(k.ContentHash)) + "\n\n")
	b.WriteString(strings.TrimSpace(k.Body) + "\n")
	return b.String()
}

func nestedFile(dir string, ks []Skill) string {
	var b strings.Builder
	var names []string
	for _, k := range ks {
		names = append(names, k.Name+"@"+shortHash(k.ContentHash))
	}
	b.WriteString(marker("nested-skills", "dir="+dir, "skills="+strings.Join(names, ",")) + "\n\n")
	b.WriteString("# Skills for " + dir + "\n")
	for _, k := range ks {
		b.WriteString("\n## " + k.Name + "\n\n")
		if k.Description != "" {
			b.WriteString("_" + strings.TrimSpace(k.Description) + "_\n\n")
		}
		b.WriteString(strings.TrimSpace(k.Body) + "\n")
	}
	return b.String()
}

// agentsSkill renders .agents/skills/<name>/SKILL.md in the Agent Skills format (spec-valid: Drydock keys live in metadata).
func agentsSkill(k Skill) string {
	meta := map[string]string{}
	if len(k.Globs) > 0 {
		meta[MetaGlobs] = strings.Join(k.Globs, ",")
	}
	if len(k.Roles) > 0 {
		meta[MetaRoles] = strings.Join(k.Roles, ",")
	}
	meta[MetaSource] = provenance(k)
	fm := struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		License     string            `yaml:"license,omitempty"`
		Metadata    map[string]string `yaml:"metadata"`
	}{k.Name, k.Description, k.License, meta}
	y, _ := yaml.Marshal(fm)
	return "---\n" + string(y) + "---\n" + marker("skill="+k.Name, "sha256="+shortHash(k.ContentHash)) + "\n\n" + strings.TrimSpace(k.Body) + "\n"
}

// RecordDelivery stores one delivery and emits skill.delivered.
func (s *Service) RecordDelivery(ctx context.Context, t Target, runID string, skill, hash, method, fidelity, filePath string) {
	_ = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_deliveries(ts,project_id,task_id,agent_id,run_id,skill_name,content_hash,method,fidelity,path) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			s.now().UnixMilli(), t.ProjectID, t.TaskID, t.AgentID, runID, skill, hash, method, fidelity, filePath)
		return err
	})
	s.emit(ctx, EventDelivered, t.ProjectID, t.TaskID, t.AgentID, map[string]any{"skill": skill, "method": method, "fidelity": fidelity, "path": filePath, "run": runID, "role": t.Role})
}

// Delivery is a stored delivery record.
type Delivery struct {
	TS       int64  `json:"ts"`
	TaskID   string `json:"taskId"`
	AgentID  string `json:"agentId"`
	RunID    string `json:"runId,omitempty"`
	Skill    string `json:"skill"`
	Hash     string `json:"contentHash,omitempty"`
	Method   string `json:"method"`
	Fidelity string `json:"fidelity"`
	Path     string `json:"path,omitempty"`
}

// Deliveries lists deliveries for a task (all tasks of the project when taskID is empty), oldest first.
func (s *Service) Deliveries(ctx context.Context, projectID, taskID string) ([]Delivery, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT ts,task_id,agent_id,run_id,skill_name,content_hash,method,fidelity,path FROM skill_deliveries
		WHERE project_id=? AND (?='' OR task_id=?) ORDER BY id`, projectID, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.TS, &d.TaskID, &d.AgentID, &d.RunID, &d.Skill, &d.Hash, &d.Method, &d.Fidelity, &d.Path); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Coverage reports, for a task, how many deliveries were native/run-start files and how many came from the tool: on-demand
// delivery depends on the model calling the tool, so evals report this ratio (docs/expert-packs.md section 9).
type Coverage struct {
	Files int `json:"files"`
	Tool  int `json:"tool"`
}

// TaskCoverage counts a task's deliveries by kind.
func (s *Service) TaskCoverage(ctx context.Context, projectID, taskID string) (Coverage, error) {
	ds, err := s.Deliveries(ctx, projectID, taskID)
	var c Coverage
	for _, d := range ds {
		if d.Method == MethodTool {
			c.Tool++
		} else {
			c.Files++
		}
	}
	return c, err
}

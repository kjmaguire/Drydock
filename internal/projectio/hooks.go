package projectio

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// ExportEnv is what export hooks may consult.
type ExportEnv struct {
	DataDir     string
	IncludeRuns bool
}

// ImportEnv carries the importing installation's facts and collects what the import changed, for the summary shown to
// the human.
type ImportEnv struct {
	DataDir string
	Human   string // the importing user's member id ("human:<name>")
	Now     int64
	Source  string // file name of the archive

	ProjectName string
	RepoPath    string
	Branch      string

	Stats Stats
}

// Stats counts the safety adjustments an import made.
type Stats struct {
	MemoryPending      int            `json:"memoryPending"`
	SkillsPending      int            `json:"skillsPending"`
	RequirementChunks  int            `json:"requirementChunksQuarantined"`
	RoutingRulesOff    int            `json:"routingRulesDisabled"`
	RunsCancelled      int            `json:"runsNotResumed"`
	TasksBlocked       int            `json:"tasksBlocked"`
	ApprovalsCancelled int            `json:"approvalsCancelled"`
	AgentPurposes      int            `json:"agentPurposesCleared"`
	AgentModesLowered  int            `json:"agentPermissionModesLowered"`
	Providers          []ProviderNote `json:"providersNeedingKeys"`
}

// ProviderNote names an imported provider that has no key.
type ProviderNote struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"baseUrl"`
}

func str(r Row, k string) string {
	switch v := r[k].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return ""
}

// ---- export hooks ----

// exportProvider removes credentials embedded in a base URL (https://user:pass@host).
func exportProvider(r Row, e *ExportEnv) {
	u, err := url.Parse(str(r, "base_url"))
	if err == nil && u.User != nil {
		u.User = nil
		r["base_url"] = u.String()
	}
}

// exportEvidence keeps references that mean something elsewhere (commit ids, URLs) and drops local file paths, unless the
// raw run output travels with the archive, in which case the path is kept as a $DATA reference to it.
func exportEvidence(r Row, e *ExportEnv) {
	ref := str(r, "ref")
	if ref == "" || !looksLikeLocalPath(ref) {
		return
	}
	inRuns := e.DataDir != "" && strings.HasPrefix(filepath.ToSlash(ref), filepath.ToSlash(filepath.Join(e.DataDir, "runs"))+"/")
	if !(e.IncludeRuns && inRuns) {
		r["ref"] = ""
	}
}

func looksLikeLocalPath(s string) bool {
	if filepath.IsAbs(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\\`) {
		return true
	}
	return len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// ---- import hooks: everything an imported file could use to act is switched off or waits for a human ----

func importProject(r Row, im *ImportEnv) {
	r["repo_path"] = im.RepoPath
	r["default_branch"] = im.Branch
	r["name"] = im.ProjectName
	r["created_at"] = im.Now
	r["version"] = int64(1)
}

func importProvider(r Row, im *ImportEnv) {
	r["api_key_ref"] = ""
	r["status"] = "unauthenticated"
	r["enabled"] = int64(0) // an imported endpoint is not live until the human enables it and adds a key
	im.Stats.Providers = append(im.Stats.Providers, ProviderNote{ID: str(r, "id"), Name: str(r, "name"), URL: str(r, "base_url")})
}

func importAgentRun(r Row, im *ImportEnv) {
	if n, _ := r["ended_at"].(int64); n == 0 {
		r["ended_at"] = r["started_at"]
		r["status"] = "interrupted"
	}
}

func importMember(r Row, im *ImportEnv) {
	if str(r, "kind") == "human" {
		r["member_id"] = im.Human
	}
}

func importTurn(r Row, im *ImportEnv) {
	if str(r, "status") == "running" {
		r["status"] = "interrupted"
		if n, _ := r["ended_at"].(int64); n == 0 {
			r["ended_at"] = r["started_at"]
		}
	}
}

func importTask(r Row, im *ImportEnv) {
	switch str(r, "status") {
	case "BACKLOG", "READY", "COMPLETED", "FAILED", "CANCELLED":
		return
	}
	r["status"] = "BLOCKED"
	r["blocked_reason"] = "Imported from " + im.Source + ": the run was in progress and is not resumed on import. Check the work, then start the task again."
	im.Stats.TasksBlocked++
}

func importRun(r Row, im *ImportEnv) {
	switch str(r, "status") {
	case "COMPLETED", "FAILED", "CANCELLED":
		return
	}
	r["status"] = "CANCELLED"
	r["failed_reason"] = "imported from " + im.Source + ": a run that was still going on the exporting machine is never resumed here"
	im.Stats.RunsCancelled++
}

func importApproval(r Row, im *ImportEnv) {
	switch str(r, "status") {
	case "REQUESTED", "APPROVED":
		// An approval is bound to one exact action on the exporting machine. It must never authorize anything here.
		r["status"] = "CANCELLED"
		r["resolved_at"] = im.Now
		im.Stats.ApprovalsCancelled++
	}
}

func importMemory(r Row, im *ImportEnv) {
	if str(r, "status") == "accepted" {
		r["status"] = "proposed"
		r["source"] = "import:accepted"
		im.Stats.MemoryPending++
	} else if str(r, "status") == "proposed" {
		im.Stats.MemoryPending++
	}
}

func importRoutingRule(r Row, im *ImportEnv) {
	if n, _ := r["enabled"].(int64); n != 0 {
		r["enabled"] = int64(0)
		im.Stats.RoutingRulesOff++
	}
}

func importRequirementChunk(r Row, im *ImportEnv) {
	r["quarantined"] = int64(1) // held back from retrieval until a human releases it
	im.Stats.RequirementChunks++
}

func importSkill(r Row, im *ImportEnv) {
	switch str(r, "status") {
	case "accepted":
		r["status"] = "offered"
		r["decided_by"] = ""
		r["decided_at"] = int64(0)
		im.Stats.SkillsPending++
	case "offered":
		im.Stats.SkillsPending++
	}
}

// importAgent keeps an imported team member from carrying an archive's words into a system prompt or its permissions into
// this machine: the purpose text is dropped (it is instructions written by whoever made the file) and a permission mode that
// skips approvals is lowered to the default for headless agents. The human can set both again.
func importAgent(r Row, im *ImportEnv) {
	if str(r, "purpose") != "" {
		r["purpose"] = ""
		im.Stats.AgentPurposes++
	}
	switch str(r, "permission_mode") {
	case "bypassPermissions", "auto", "dontAsk":
		r["permission_mode"] = "acceptEdits"
		im.Stats.AgentModesLowered++
	}
}

// importSetting switches off what starts work or runs commands by itself: automatic scheduling, the e2e and preview
// commands (the commands stay visible for the human to review) and letting workers run the project's checks. Only a human
// on this machine switches them on again.
func importSetting(r Row, im *ImportEnv) {
	var m map[string]any
	if json.Unmarshal([]byte(str(r, "value")), &m) != nil {
		r["value"] = "{}"
		return
	}
	m["autoSchedule"] = false
	m["workerChecks"] = false
	for _, k := range []string{"e2e", "preview"} {
		if sub, ok := m[k].(map[string]any); ok {
			sub["enabled"] = false
		}
	}
	b, _ := json.Marshal(m)
	r["value"] = string(b)
}

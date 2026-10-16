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

	// NewProject is the id the imported project gets (the archive's own id in a preview, where nothing is remapped).
	NewProject string
	// RunDirs are the folders under <DataDir>/runs this import restored from the archive. A stored reference to a local file
	// is kept only inside one of them; any other path in an archive means a file of the exporting machine, or a file of this
	// one the archive's author would like read.
	RunDirs []string

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
	// RefsCleared counts stored references to local files that pointed outside the restored run folders and were emptied;
	// RowsRejected counts rows that did not belong to the project (another project's budget, a setting of another key);
	// MessagesDistrusted counts room messages stored as untrusted whoever the file says wrote them.
	RefsCleared        int `json:"fileReferencesCleared"`
	RowsRejected       int `json:"rowsRejected"`
	MessagesDistrusted int `json:"messagesDistrusted"`
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
	if im.RepoPath != "" {
		r["default_branch"] = im.Branch
	}
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
// commands (the commands stay visible for the human to review) and letting workers run the project's checks. It also puts
// the CI gate back to required: a file cannot declare that the repository has no CI. Only a human on this machine changes
// them again.
func importSetting(r Row, im *ImportEnv) {
	r["value"] = SanitizeDevSettings(str(r, "value"))
}

// SanitizeDevSettings is importSetting's change to one project's settings JSON. The reader of that JSON is encoding/json into
// a struct, which matches a key whatever its case (autoschedule, AUTOSCHEDULE, even the Kelvin sign for k) and lets the last of
// two keys that match win, so editing the key spelled the way the struct spells it leaves "autoschedule":true, "workerchecks":
// true, "cimode":"none" or "E2E":{"enabled":true,"command":[...]} in force. Every key that matches one of the switches, in any
// spelling, is therefore removed and the switch written once, off; the e2e and preview objects keep their commands (spelled
// as the struct spells them) and lose every spelling of "enabled".
func SanitizeDevSettings(raw string) string {
	var in map[string]any
	if json.Unmarshal([]byte(raw), &in) != nil {
		return "{}"
	}
	out := map[string]any{}
	nested := map[string]map[string]any{}
	for k, v := range in {
		switch {
		case strings.EqualFold(k, "autoSchedule"), strings.EqualFold(k, "workerChecks"), strings.EqualFold(k, "ciMode"),
			strings.EqualFold(k, "ciNoneBy"), strings.EqualFold(k, "ciNoneAt"):
			// replaced below
		case strings.EqualFold(k, "e2e"), strings.EqualFold(k, "preview"):
			sub, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name := strings.ToLower(k)
			if nested[name] == nil {
				nested[name] = map[string]any{}
			}
			for sk, sv := range sub {
				if strings.EqualFold(sk, "enabled") {
					continue
				}
				nested[name][canonicalSubKey(sk)] = sv
			}
		default:
			out[k] = v
		}
	}
	for _, name := range []string{"e2e", "preview"} {
		sub := nested[name]
		if sub == nil {
			sub = map[string]any{}
		}
		sub["enabled"] = false
		out[name] = sub
	}
	out["autoSchedule"] = false
	out["workerChecks"] = false
	out["ciMode"] = "required"
	b, _ := json.Marshal(out)
	return string(b)
}

// canonicalSubKey spells the keys of the e2e and preview objects the way the settings struct does, so two spellings of one key
// cannot both survive.
func canonicalSubKey(k string) string {
	for _, want := range []string{"command", "timeoutSec", "url"} {
		if strings.EqualFold(k, want) {
			return want
		}
	}
	return k
}

// ---- references to local files ----

// importEvidence empties a stored reference to a local file that is not inside a run folder this import restored. The readers
// of evidence (the prompt of the next worker, the diff viewer, the log viewer) open the reference as a path, so an archive
// whose reference is C:\Users\you\.ssh\id_ed25519, ..\..\x or a \\host\share path would have that file read into a prompt
// or have this machine authenticate to a server of the archive's choosing. Commit ids, pull request references and web URLs
// are not files and stay.
func importEvidence(r Row, im *ImportEnv) {
	ref := str(r, "ref")
	if ref == "" {
		return
	}
	switch str(r, "kind") {
	case "commit", "pr", "ci":
		return
	}
	if isWebURL(ref) {
		return
	}
	if !im.insideRunDirs(ref) {
		r["ref"] = ""
		if im.DataDir != "" { // a preview restores nothing, so it would count every path
			im.Stats.RefsCleared++
		}
	}
}

// importNodeExecution empties a node's stored log path unless it is inside a restored run folder, and its safe point (a hidden
// ref in the exporting machine's repository, which this installation does not have and git must never be handed as text).
func importNodeExecution(r Row, im *ImportEnv) {
	if ref := str(r, "partial_ref"); ref != "" && !im.insideRunDirs(ref) {
		r["partial_ref"] = ""
		if im.DataDir != "" {
			im.Stats.RefsCleared++
		}
	}
	r["safe_point_ref"] = ""
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// insideRunDirs reports whether ref is an absolute path inside one of the restored run folders (".." is resolved first, so
// <data>/runs/<run>/../../x is outside).
func (im *ImportEnv) insideRunDirs(ref string) bool {
	if !filepath.IsAbs(ref) {
		return false
	}
	clean := filepath.Clean(ref)
	for _, d := range im.RunDirs {
		d = filepath.Clean(d)
		if clean == d || strings.HasPrefix(clean, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---- rows that are not the project's ----

// acceptOwnProject keeps only the project the manifest names (an archive can carry other rows; each would become a project).
func acceptOwnProject(r Row, im *ImportEnv) bool { return str(r, "id") == im.NewProject }

// acceptOwnSetting keeps only the project's own settings key; any other key of the settings table is an installation-wide
// setting (the backup folder, the license) that no project archive may set.
func acceptOwnSetting(r Row, im *ImportEnv) bool { return str(r, "key") == "devloop:"+im.NewProject }

// acceptProjectBudget keeps budgets of a project, a task, a run or an agent; a global or provider budget is the importing
// installation's own, and an archive with a one-cent global budget would stop all spending.
func acceptProjectBudget(r Row, im *ImportEnv) bool {
	switch str(r, "scope") {
	case "project", "task", "run", "agent":
		return true
	}
	return false
}

// acceptOwnRoutingRule keeps rules scoped to this project; a workspace-wide rule would route every project's work.
func acceptOwnRoutingRule(r Row, im *ImportEnv) bool {
	return str(r, "scope") == "project:"+im.NewProject
}

// importMessage stores every message as untrusted. Trust decides whether the message is shown to an agent as the human's own
// word or wrapped as data (internal/room), and an archive can name any author, including the person importing it.
func importMessage(r Row, im *ImportEnv) {
	if str(r, "trust") != "untrusted" {
		r["trust"] = "untrusted"
		im.Stats.MessagesDistrusted++
	}
}

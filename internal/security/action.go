package security

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// DescriptorVersion is the current ActionDescriptor schema version; it is part of the hash.
const DescriptorVersion = 1

// Approval kinds (docs/security.md s5). KindShellCommand is the kind for an ordinary analyzed command that needs a
// one-off or stored-rule approval (not named in the doc table; see Decision).
const (
	KindFSDelete             = "fs_delete"
	KindFSOutsideWorktree    = "fs_outside_worktree"
	KindGitForcePush         = "git_force_push"
	KindGitBranchDelete      = "git_branch_delete"
	KindGitPush              = "git_push"
	KindDeploy               = "deploy"
	KindDBMigration          = "db_migration"
	KindSecretsAccess        = "secrets_access"
	KindInfraChange          = "infra_change"
	KindShellDestructive     = "shell_destructive"
	KindShellUnanalyzable    = "shell_unanalyzable"
	KindShellCommand         = "shell_command"
	KindMCPWriteTool         = "mcp_write_tool"
	KindIntegrationWrite     = "integration_write"
	KindPermissionEscalation = "permission_mode_escalation"
	KindRepoConfigChanged    = "repo_config_changed"
)

// Target is one resolved object an action touches: a filesystem path with its identity, or a remote ref.
type Target struct {
	Path         string `json:"path"`
	VolumeSerial string `json:"volumeSerial"`
	FileIndex    string `json:"fileIndex"`
	Remote       string `json:"remote"`
	Ref          string `json:"ref"`
	OldOID       string `json:"oldOid"`
	NewOID       string `json:"newOid"`
}

// ActionDescriptor is the canonical description of the exact action an approval is bound to (docs/security.md s5).
// It never holds environment values, only names. RunID/NodeID/Step/Attempt are the node idempotency key, so an
// approval granted on one checkpoint lineage cannot be consumed on another.
type ActionDescriptor struct {
	V          int      `json:"v"`
	Kind       string   `json:"kind"`
	Project    string   `json:"project"`
	TaskID     string   `json:"taskId"`
	RunID      string   `json:"runId"`
	NodeID     string   `json:"nodeId"`
	Step       int      `json:"step"`
	Attempt    int      `json:"attempt"`
	AgentID    string   `json:"agentId"`
	WorktreeID string   `json:"worktreeId"`
	Cwd        string   `json:"cwd"`  // canonical resolved path
	Tool       string   `json:"tool"` // "shell", "git", "fs.delete", "mcp:<server>/<tool>"
	Shell      string   `json:"shell"`
	Program    string   `json:"program"`
	Argv       []string `json:"argv"`
	Command    string   `json:"command"`    // exact text as executed (for a non-shell tool: the text a person reads, ReviewInput)
	ArgvDigest string   `json:"argvDigest"` // CommandReport.Digest
	Targets    []Target `json:"targets"`
	// Shortened is set when a long value is cut in Command: the approval still covers the whole input (ArgvDigest). It is in the
	// hash only when set, so every other descriptor hashes as it always did.
	Shortened bool     `json:"shortened,omitempty"`
	EnvKeys   []string `json:"envKeys"` // names only
	EnvDigest string   `json:"envDigest"`
}

// normalized returns a copy with defaults and ordering fixed so equal actions hash equally.
func (a ActionDescriptor) normalized() ActionDescriptor {
	if a.V == 0 {
		a.V = DescriptorVersion
	}
	a.Argv = append([]string{}, a.Argv...)
	a.Targets = append([]Target{}, a.Targets...)
	sort.SliceStable(a.Targets, func(i, j int) bool { return targetKey(a.Targets[i]) < targetKey(a.Targets[j]) })
	keys := make([]string, 0, len(a.EnvKeys))
	seen := map[string]bool{}
	for _, k := range a.EnvKeys {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	a.EnvKeys = keys
	if a.EnvDigest == "" && len(keys) > 0 {
		a.EnvDigest = EnvDigest(keys)
	}
	return a
}

func targetKey(t Target) string {
	return strings.Join([]string{t.Path, t.VolumeSerial, t.FileIndex, t.Remote, t.Ref, t.OldOID, t.NewOID}, "\x00")
}

// EnvDigest is the SHA-256 of the sorted, de-duplicated environment variable names (never values).
func EnvDigest(names []string) string {
	seen := map[string]bool{}
	var ks []string
	for _, k := range names {
		if !seen[k] {
			seen[k] = true
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	sum := sha256.Sum256([]byte(strings.Join(ks, "\n")))
	return hex.EncodeToString(sum[:])
}

// Canonical returns the canonical JSON of the normalized descriptor.
func (a ActionDescriptor) Canonical() ([]byte, error) { return CanonicalJSON(a.normalized()) }

// Hash is hex(SHA-256(canonicalJSON(descriptor))).
func (a ActionDescriptor) Hash() string {
	b, err := a.Canonical()
	if err != nil { // a descriptor of strings and ints always marshals
		panic("security: canonical descriptor: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

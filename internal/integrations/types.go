// Package integrations connects Drydock to Linear, Notion, Sentry and Aikido (docs/integrations.md, brief section 11).
//
// Each integration is a Connection with two independent channels. App sync polls the vendor's MCP server through the MCP
// gateway client (internal/mcp AppUpstream) with cursors and writes what it sees into external_items. Agent access goes
// through the gateway's per-role allowlists and is not this package's business; the two share one stored credential.
//
// The rules the package enforces:
//
//   - Nothing becomes a task without a human action (Import) or a stored rule a human authored (Rules). Imported tasks are
//     BACKLOG tasks; the external text is quoted data inside them and can never set parameters, roles or permissions.
//   - Inbound content is untrusted: size-limited, stripped of control and invisible characters, scanned for secrets (which are
//     redacted before anything is stored), scored for prompt-injection patterns, never executed.
//   - Outbound writes (comment, change state, create a page, resolve an issue, ignore a finding) are rows in the outbox bound to
//     a security.Approvals request on the hash of the exact call. Only a human can approve; a changed payload, a reused or
//     unapproved approval, or a non-human approver does nothing.
//   - Credentials live only in the Vault (providers.SecretStore). They are never in SQLite rows, events, logs or errors.
package integrations

import (
	"errors"
	"fmt"
	"time"
)

// Kinds of integration.
const (
	KindLinear = "linear"
	KindNotion = "notion"
	KindSentry = "sentry"
	KindAikido = "aikido"
)

// Kinds lists the supported integrations in display order.
var Kinds = []string{KindLinear, KindNotion, KindSentry, KindAikido}

// Item kinds (external_items.kind).
const (
	ItemIssue      = "issue"
	ItemPage       = "page"
	ItemErrorEvent = "error_event"
	ItemFinding    = "finding"
)

// Connection statuses.
const (
	StatusUntested        = "untested"
	StatusOK              = "ok"
	StatusUnauthenticated = "unauthenticated"
	StatusUnreachable     = "unreachable"
	StatusDegraded        = "degraded"
	StatusDisconnected    = "disconnected"
)

// Failure codes recorded with a sync failure (integration_cursors.last_error_code).
const (
	CodeUnauthorized = "unauthorized"
	CodeRateLimited  = "rate_limited"
	CodeUnreachable  = "unreachable"
	CodeToolMissing  = "tool_missing"
	CodeSchemaDrift  = "schema_drift"
	CodeTimeout      = "timeout"
	CodeOther        = "error"
)

// Event types (docs/integrations.md section 9).
const (
	EventSyncStarted        = "integration.sync.started"
	EventSyncCompleted      = "integration.sync.completed"
	EventSyncFailed         = "integration.sync.failed"
	EventItemUpserted       = "integration.item.upserted"
	EventTaskCreated        = "integration.task_created"
	EventOutboundRequested  = "integration.outbound.requested"
	EventOutboundApproved   = "integration.outbound.approved"
	EventOutboundRejected   = "integration.outbound.rejected"
	EventOutboundSent       = "integration.outbound.sent"
	EventOutboundFailed     = "integration.outbound.failed"
	EventRateLimited        = "integration.rate_limited"
	EventAuthExpired        = "integration.auth_expired"
	EventSchemaDrift        = "integration.schema_drift"
	EventConnected          = "integration.connected"
	EventDisconnected       = "integration.disconnected"
	EventRuleChanged        = "integration.rule.changed"
	EventRuleWouldCreate    = "integration.rule.would_create"
	EventRuleRateLimited    = "integration.rule.rate_limited"
	EventRuleSkippedRisk    = "integration.rule.skipped_risky"
	EventNotionPageReady    = "integration.notion_page_ready"
	EventTestCompleted      = "integration.test.completed"
	EventItemRiskFlagged    = "integration.item.risk_flagged"
	EventItemSecretRedacted = "integration.item.secret_redacted"
)

// Errors.
var (
	ErrNotFound          = errors.New("integrations: not found")
	ErrConflict          = errors.New("integrations: concurrent modification (version mismatch)")
	ErrNotHuman          = errors.New("integrations: this action needs a human; agents, tools and external content cannot do it")
	ErrNoCredential      = errors.New("integrations: no token is stored for this connection")
	ErrRateLimited       = errors.New("integrations: the vendor asked Drydock to slow down")
	ErrSyncBusy          = errors.New("integrations: a sync for this connection is already running")
	ErrAlreadyImported   = errors.New("integrations: this item already has a task")
	ErrNotImportable     = errors.New("integrations: this item cannot become a task")
	ErrOpUnsupported     = errors.New("integrations: this operation is not supported for this integration")
	ErrContainsSecret    = errors.New("integrations: the text contains what looks like a secret, so it is not sent")
	ErrNotApproved       = errors.New("integrations: the action is not approved")
	ErrPayloadChanged    = errors.New("integrations: the action changed after it was approved")
	ErrBadRequest        = errors.New("integrations: invalid request")
	ErrSendFailed        = errors.New("integrations: the vendor did not accept the write")
	ErrRuleNotAllowed    = errors.New("integrations: this rule is not allowed")
	ErrToolMissing       = errors.New("integrations: the server does not offer the tool Drydock needs")
	ErrCredentialsLocked = errors.New("integrations: the credential store is not available")
)

// Connection is one configured integration.
type Connection struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Name          string            `json:"name"`
	ProjectID     string            `json:"projectId"`
	Status        string            `json:"status"`
	StatusDetail  string            `json:"statusDetail"`
	AuthKind      string            `json:"authKind"`
	CredentialRef string            `json:"-"` // a reference, not a secret; kept out of the UI payload anyway
	BaseURL       string            `json:"baseUrl,omitempty"`
	Config        map[string]string `json:"config"`
	PollIntervalS int               `json:"pollIntervalS"`
	Enabled       bool              `json:"enabled"`
	Created       int64             `json:"created"`
	Updated       int64             `json:"updated"`
	Version       int64             `json:"version"`
}

// SyncStatus is the per-Connection sync state shown on the Connections page.
type SyncStatus struct {
	State               string `json:"state"` // never_synced | ok | error | unauthenticated | rate_limited | backing_off | paused
	LastSuccessAt       int64  `json:"lastSuccessAt"`
	LastAttemptAt       int64  `json:"lastAttemptAt"`
	LastError           string `json:"lastError,omitempty"` // plain language
	LastErrorCode       string `json:"lastErrorCode,omitempty"`
	ItemsSeen           int    `json:"itemsSeen"` // items stored for this connection
	ItemsLastSync       int    `json:"itemsLastSync"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	NextAttemptAt       int64  `json:"nextAttemptAt,omitempty"`
	Watermark           int64  `json:"watermark"`
	Resuming            bool   `json:"resuming"` // a sweep was interrupted and will resume from its cursor
}

// ConnectionView is Connection plus derived state.
type ConnectionView struct {
	Connection
	HasCredential bool       `json:"hasCredential"`
	Sync          SyncStatus `json:"sync"`
	Tools         ToolInfo   `json:"tools"`
}

// ToolInfo describes what the integration's tools can do (from the adapter; the live list comes from Test).
type ToolInfo struct {
	SyncTools []string `json:"syncTools"`
	Ops       []OpInfo `json:"ops"`
}

// Item is a stored external item. All text fields are sanitized: they are data, never instructions.
type Item struct {
	ID               string         `json:"id"`
	IntegrationID    string         `json:"integrationId"`
	Source           string         `json:"source"`
	ProjectID        string         `json:"projectId"`
	ExternalID       string         `json:"externalId"`
	Kind             string         `json:"kind"`
	IssueRef         string         `json:"issueRef"`
	URL              string         `json:"url"`
	Title            string         `json:"title"`
	Excerpt          string         `json:"excerpt"`
	BodyRef          string         `json:"-"`
	State            string         `json:"state"`
	Labels           []string       `json:"labels"`
	Assignee         string         `json:"assignee"`
	Priority         string         `json:"priority"`
	ParentExternalID string         `json:"parentExternalId,omitempty"`
	Meta             map[string]any `json:"meta"`
	ExternalCreated  int64          `json:"externalCreatedAt"`
	ExternalUpdated  int64          `json:"externalUpdatedAt"`
	Etag             string         `json:"etag"`
	ContentHash      string         `json:"contentHash"`
	FetchedAt        int64          `json:"fetchedAt"`
	Risk             int            `json:"risk"`
	RiskFlags        []string       `json:"riskFlags"`
	Redactions       int            `json:"redactions"`
	Truncated        bool           `json:"truncated"`
	Untrusted        bool           `json:"untrusted"` // always true
	TaskID           string         `json:"taskId,omitempty"`
	ImportedBy       string         `json:"importedBy,omitempty"`
	ImportedAt       int64          `json:"importedAt,omitempty"`
	Gone             bool           `json:"gone"`
	Version          int64          `json:"version"`
	Warnings         []string       `json:"warnings,omitempty"`
}

// ItemFilter selects items.
type ItemFilter struct {
	IntegrationID string
	Kind          string
	State         string
	Imported      *bool // nil = both
	Gone          *bool // nil = not gone only
	Limit         int
	Offset        int
}

// SyncResult is the outcome of one Sync call.
type SyncResult struct {
	IntegrationID string        `json:"integrationId"`
	Pages         int           `json:"pages"`
	Seen          int           `json:"seen"`
	New           int           `json:"new"`
	Changed       int           `json:"changed"`
	Unchanged     int           `json:"unchanged"`
	Gone          int           `json:"gone"`
	Complete      bool          `json:"complete"` // the sweep reached the watermark (or the end); false = more pages next time
	Skipped       string        `json:"skipped,omitempty"`
	RuleResults   []RuleOutcome `json:"ruleResults,omitempty"`
}

// Clock lets tests control time.
type Clock func() time.Time

func errf(base error, format string, a ...any) error {
	return fmt.Errorf("%w: %s", base, fmt.Sprintf(format, a...))
}

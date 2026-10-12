package integrations

import (
	"context"
)

// OpInfo describes one outbound operation.
type OpInfo struct {
	Op          string   `json:"op"`
	Label       string   `json:"label"`
	Params      []string `json:"params"`
	NeverAuto   bool     `json:"neverAuto"` // suppresses a signal or changes state in bulk: never eligible for a standing approval
	Description string   `json:"description"`
}

// RawItem is one item as an adapter understood it, before sanitizing.
type RawItem struct {
	ExternalID string
	Kind       string
	IssueRef   string
	URL        string
	Title      string
	Body       string // description / page text / finding detail
	State      string
	Labels     []string
	Assignee   string
	Priority   string // urgent | high | medium | low | none, or a Sentry level / Aikido severity
	Parent     string
	Created    int64
	Updated    int64
	Etag       string
	Meta       map[string]any
	// NeedsBody is true when Body is only a summary and the full text must be fetched with Detail (Notion).
	NeedsBody bool
}

// Page is one page of a listing, newest first where the vendor supports an order.
type Page struct {
	Items []RawItem
	Next  string // the vendor's cursor for the next page; "" = no more
}

// PageQuery asks an adapter for one page of an assignment's scope.
type PageQuery struct {
	Config map[string]string // the assignment's remote scope
	After  string            // the vendor cursor ("" = first page)
	Limit  int
	// Since is the watermark (ms): an adapter whose API can filter by update time asks only for newer items. The cursor of an
	// interrupted sweep was produced under the same Since (the watermark moves only when a sweep completes).
	Since int64
}

// Choice is one value a setting can take, read from the vendor's API (a Linear team, a Sentry project, a Notion data source).
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Adapter is the per-vendor knowledge: which API calls read the items, how to read their results, and how to build and send
// outbound writes.
type Adapter interface {
	Kind() string
	// Identity reads who the credential is and what it reaches. It is also the cheapest authenticated call, so Test uses it to
	// prove the credential works.
	Identity(ctx context.Context, a *API) (Identity, error)
	// Choices lists the values of an assignment setting ("team", "organization", "project", "data_source", "repo").
	Choices(ctx context.Context, a *API, key string, cfg map[string]string) ([]Choice, error)
	// Page fetches one page of an assignment's items. It must be read-only.
	Page(ctx context.Context, a *API, q PageQuery) (Page, error)
	// Detail returns the full text for an item whose list entry was a summary (Notion page body, Sentry stack trace). ok=false
	// means there is nothing more to fetch.
	Detail(ctx context.Context, a *API, cfg map[string]string, externalID string) (text string, ok bool, err error)
	// FullSweep reports that every sync lists everything (no vendor cursor or order), so items missing afterwards are gone.
	FullSweep() bool
	// Endpoints names the API calls sync makes (shown to the person).
	Endpoints() []string
	// Ops lists the outbound operations.
	Ops() []OpInfo
	// Build builds the exact call of an outbound operation. It is pure: nothing is sent.
	Build(op, target string, params map[string]string, cfg map[string]string) (OutCall, error)
	// Send performs a call Build made (and a human approved). The receipt is a short vendor reference (a comment id).
	Send(ctx context.Context, a *API, call OutCall) (receipt string, err error)
	// TaskMapping maps an item to the task fields (priority, kind label) the importer needs.
	TaskMapping(it Item) TaskMapping
}

// OutCall is the exact outbound call: the API operation, its arguments and a preview for the human. Endpoint and Args are what
// the approval is bound to.
type OutCall struct {
	Endpoint string         `json:"endpoint"` // e.g. "graphql commentCreate", "PUT /api/0/organizations/{org}/issues/{id}/"
	Args     map[string]any `json:"args"`
	Preview  string         `json:"preview"`
}

// TaskMapping carries what an importer takes from an item that is not free text.
type TaskMapping struct {
	Priority int    // Drydock priority: higher is more urgent (0 none ... 4 urgent)
	Label    string // "bug", "security", "requirements", "issue"
}

// registry of adapters.
var adapters = map[string]Adapter{}

func register(a Adapter) { adapters[a.Kind()] = a }

// AdapterFor returns the adapter of a kind.
func AdapterFor(kind string) (Adapter, bool) { a, ok := adapters[kind]; return a, ok }

func init() {
	register(linearAdapter{})
	register(notionAdapter{})
	register(sentryAdapter{})
	register(aikidoAdapter{})
	register(githubAdapter{})
}

func argString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

package llm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/storage"
)

// cacheSchema versions the canonical request layout; bump it to invalidate every entry.
const cacheSchema = 1

// DefaultCacheTTLs are the purposes that may be cached and for how long. A call whose purpose is not listed is not
// cached unless the request sets CacheTTL (docs/token-control.md s8: "cacheable only if its purpose is on an
// allowlist"). Values are starting points to tune, not measured.
var DefaultCacheTTLs = map[string]time.Duration{
	"classify":  24 * time.Hour,
	"summarize": 24 * time.Hour,
	"consult":   time.Hour, // documentation changes; answers carry source age (brief section 15)
	"probe":     10 * time.Minute,
}

// cacheDoc is the canonical request hashed into the cache key. Excluded: API key, request ids, timestamps.
type cacheDoc struct {
	V           int              `json:"v"`
	Provider    string           `json:"provider"`
	BaseURL     string           `json:"baseUrl"`
	Model       string           `json:"model"`
	Effort      Effort           `json:"effort"`
	MaxTokens   int              `json:"maxTokens"`
	Temperature *float64         `json:"temperature"`
	TopP        *float64         `json:"topP"`
	System      []Block          `json:"system"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDef        `json:"tools"`
	ToolChoice  string           `json:"toolChoice"`
	Routing     json.RawMessage  `json:"providerRouting"`
	DataClass   providers.DataClass `json:"dataClass"`
	PackVersion string           `json:"packVersion"`
	ProjectID   string           `json:"projectId"`
}

func normText(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), " \t\r\n")
}

// CacheKey is the hex SHA-256 of the canonical, normalized request for a concrete target. Line endings and trailing
// whitespace are normalized; the key covers provider, model, request, effective data class, pack version and project.
func CacheKey(p providers.Provider, model string, class providers.DataClass, r Request) string {
	d := cacheDoc{V: cacheSchema, Provider: p.ID, BaseURL: p.BaseURL, Model: model, Effort: r.Effort, MaxTokens: r.MaxTokens,
		Temperature: r.Temperature, TopP: r.TopP, ToolChoice: r.ToolChoice, Routing: p.RequestOverrides, DataClass: class,
		PackVersion: r.PackVersion, ProjectID: r.ProjectID}
	for _, b := range r.System {
		d.System = append(d.System, Block{Text: normText(b.Text), Cache: b.Cache})
	}
	for _, m := range r.Messages {
		m.Content, m.Reasoning = normText(m.Content), normText(m.Reasoning)
		d.Messages = append(d.Messages, m)
	}
	d.Tools = r.Tools
	b, _ := json.Marshal(d) // struct field order and sorted map keys make this deterministic
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cached is the stored response.
type cached struct {
	Content      string     `json:"content"`
	Reasoning    string     `json:"reasoning,omitempty"`
	ToolCalls    []ToolCall `json:"toolCalls,omitempty"`
	FinishReason string     `json:"finishReason"`
	Usage        Usage      `json:"usage"`
	ModelUsed    string     `json:"modelUsed"`
	CostUSD      float64    `json:"costUsd"`
	CostKnown    bool       `json:"costKnown"`
}

// ttlFor returns the cache TTL for a request, or 0 when the call must not be cached.
func (w *Wrapper) ttlFor(r Request) time.Duration {
	if r.NoCache || r.SideEffecting {
		return 0
	}
	if r.CacheTTL > 0 {
		return r.CacheTTL
	}
	return w.ttls[r.Purpose]
}

func (w *Wrapper) cacheGet(ctx context.Context, key string) (cached, bool) {
	var blob []byte
	err := w.db.Reader().QueryRowContext(ctx, `SELECT response FROM llm_cache WHERE key=? AND expires_at>?`, key, ms(w.now())).Scan(&blob)
	if err != nil {
		return cached{}, false
	}
	var c cached
	if json.Unmarshal(blob, &c) != nil {
		return cached{}, false
	}
	_ = w.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE llm_cache SET hits=hits+1, last_hit_at=? WHERE key=?`, ms(w.now()), key)
		return err
	})
	return c, true
}

func (w *Wrapper) cachePut(ctx context.Context, key string, p providers.Provider, class providers.DataClass, r Request, resp Response, ttl time.Duration) {
	c := cached{Content: resp.Content, Reasoning: resp.Reasoning, ToolCalls: resp.ToolCalls, FinishReason: resp.FinishReason,
		Usage: resp.Usage, ModelUsed: resp.ModelUsed, CostUSD: resp.Cost.USD, CostKnown: resp.Cost.Known}
	blob, err := json.Marshal(c)
	if err != nil {
		return
	}
	now := w.now()
	_ = w.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO llm_cache(key,provider,model,response,data_class,purpose,project_id,pack_version,cost_usd_at_fill,created_at,expires_at,hits)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(key) DO UPDATE SET response=excluded.response, created_at=excluded.created_at,
			expires_at=excluded.expires_at, cost_usd_at_fill=excluded.cost_usd_at_fill, hits=0`,
			key, p.ID, resp.ModelUsed, blob, string(class), r.Purpose, r.ProjectID, r.PackVersion, resp.Cost.USD, ms(now), ms(now.Add(ttl)))
		return err
	})
}

// PurgeExpiredCache deletes expired entries and returns how many were removed.
func (w *Wrapper) PurgeExpiredCache(ctx context.Context) (int64, error) {
	var n int64
	err := w.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM llm_cache WHERE expires_at<=?`, ms(w.now()))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// PurgeProjectCache deletes a project's cache entries (entries are purged with the project, docs/token-control.md s8).
func (w *Wrapper) PurgeProjectCache(ctx context.Context, projectID string) error {
	if projectID == "" {
		return errors.New("llm: empty project id")
	}
	return w.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM llm_cache WHERE project_id=?`, projectID)
		return err
	})
}

func ms(t time.Time) int64 { return t.UnixMilli() }

var _ = storage.Now

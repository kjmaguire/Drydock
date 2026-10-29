package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Registry is the provider registry and model catalog on SQLite (tables providers and model_catalog). API keys are
// resolved through the SecretStore by reference and never stored in the database.
type Registry struct {
	db      *storage.DB
	secrets SecretStore
	bus     events.Bus // optional
	resolve Resolver

	mu      sync.Mutex
	clients map[string]*clientEntry
}

type clientEntry struct {
	sig string
	hc  *http.Client
}

// RegistryOptions configures NewRegistry. Every field is optional except that secrets is needed to store keys.
type RegistryOptions struct {
	Secrets  SecretStore
	Bus      events.Bus
	Resolver Resolver // host name resolution for the transport check; default the system resolver
}

// NewRegistry opens the registry over db.
func NewRegistry(db *storage.DB, o RegistryOptions) *Registry {
	if o.Resolver == nil {
		o.Resolver = SystemResolver
	}
	return &Registry{db: db, secrets: o.Secrets, bus: o.Bus, resolve: o.Resolver, clients: map[string]*clientEntry{}}
}

// NewProvider is the input to Create. APIKey, when set, is stored in the SecretStore and replaced by a reference.
type NewProvider struct {
	Name             string
	Kind             Kind
	BaseURL          string
	APIKey           string
	DataClass        DataClass
	Tier             Tier
	Flavor           Flavor // empty = detect from the base URL
	MaxConcurrent    int    // 0 = default (1 for ollama and plain-http LAN hosts, else unlimited)
	HourlyCost       float64
	TLSFingerprint   string
	Attestations     map[string]bool
	WireAPIs         []string
	RequestOverrides json.RawMessage
	Disabled         bool
}

// DetectFlavor guesses the flavor from the base URL's host. Hosts are refined by the probe.
func DetectFlavor(baseURL string) Flavor {
	u, err := url.Parse(baseURL)
	if err != nil {
		return FlavorOpenAI
	}
	switch h := strings.ToLower(u.Hostname()); {
	case h == "openrouter.ai" || strings.HasSuffix(h, ".openrouter.ai"):
		return FlavorOpenRouter
	case h == "generativelanguage.googleapis.com":
		return FlavorGemini
	case h == "api.anthropic.com":
		return FlavorAnthropic
	}
	return FlavorOpenAI
}

func (r *Registry) validate(ctx context.Context, p *Provider) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	switch p.Kind {
	case KindAggregator, KindDirect, KindHost:
	default:
		return fmt.Errorf("%w: kind must be aggregator, direct or host", ErrInvalid)
	}
	switch p.DataClass {
	case DataPrivate, DataTrainable:
	default:
		return fmt.Errorf("%w: data_class must be private or trainable (it has no default)", ErrInvalid)
	}
	switch p.Tier {
	case TierPremium, TierCheap, TierFree:
	default:
		return fmt.Errorf("%w: tier must be premium, cheap or free", ErrInvalid)
	}
	if p.MaxConcurrent < 0 {
		return fmt.Errorf("%w: max_concurrent must be >= 0", ErrInvalid)
	}
	if p.Flavor == "" {
		p.Flavor = DetectFlavor(p.BaseURL)
	}
	if p.TLSFingerprint != "" {
		fp, err := NormalizeFingerprint(p.TLSFingerprint)
		if err != nil {
			return err
		}
		p.TLSFingerprint = fp
	}
	tr, err := CheckTransport(ctx, p.BaseURL, p.TLSFingerprint, r.resolve)
	if err != nil {
		return err
	}
	p.TLSMode, p.LANUnencrypted = tr.TLS, tr.LANUnencrypted
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	// A private route over plain http on a LAN needs the user's attestation (docs/security.md s9, Q3).
	if p.DataClass == DataPrivate && p.LANUnencrypted && !p.Attested(AttestLANAttested) {
		return fmt.Errorf("%w: %q is plain http on a private network address (lan_unencrypted); a private route needs the %s attestation or https", ErrAttestationNeeded, p.BaseURL, AttestLANAttested)
	}
	// A Gemini key is private only for a billing-enabled project, which cannot be detected from a key: the user attests.
	if p.Flavor == FlavorGemini && p.DataClass == DataPrivate && !p.Attested(AttestBillingEnabled) {
		return fmt.Errorf("%w: a Google AI Studio route is private only for a billing-enabled project (attestation %s); an unbilled key is trainable", ErrAttestationNeeded, AttestBillingEnabled)
	}
	if len(p.WireAPIs) == 0 {
		p.WireAPIs = []string{"chat"}
		if p.Flavor == FlavorAnthropic {
			p.WireAPIs = []string{"anthropic_messages"}
		}
	}
	if len(p.RequestOverrides) == 0 {
		p.RequestOverrides = json.RawMessage(`{}`)
	} else if !json.Valid(p.RequestOverrides) {
		return fmt.Errorf("%w: request_overrides is not valid JSON", ErrInvalid)
	}
	return nil
}

// Create validates np, stores the API key in the SecretStore, and inserts the provider.
func (r *Registry) Create(ctx context.Context, np NewProvider) (Provider, error) {
	p := Provider{
		ID: ids.New("prov"), Name: np.Name, Kind: np.Kind, BaseURL: np.BaseURL, DataClass: np.DataClass, Tier: np.Tier,
		Flavor: np.Flavor, Enabled: !np.Disabled, MaxConcurrent: np.MaxConcurrent, HourlyCost: np.HourlyCost,
		TLSFingerprint: np.TLSFingerprint, Attestations: np.Attestations, WireAPIs: np.WireAPIs,
		RequestOverrides: np.RequestOverrides, Status: string(HealthUnknown), ServerInfo: json.RawMessage(`{}`),
		CreatedAt: time.Now().UTC(),
	}
	if err := r.validate(ctx, &p); err != nil {
		return Provider{}, err
	}
	if p.MaxConcurrent == 0 && (p.Flavor == FlavorOllama || p.LANUnencrypted) {
		p.MaxConcurrent = 1 // OLLAMA_NUM_PARALLEL defaults to 1 (MP E); plain-http LAN hosts default to 1 (Q3)
	}
	p.UpdatedAt = p.CreatedAt
	if np.APIKey != "" {
		if r.secrets == nil {
			return Provider{}, ErrNoSecretStore
		}
		p.KeyRef = "provider/" + p.ID + "/api-key"
		if err := r.secrets.Put(ctx, p.KeyRef, np.APIKey); err != nil {
			return Provider{}, fmt.Errorf("store api key: %w", err)
		}
	}
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO providers(id,name,kind,base_url,api_key_ref,data_class,tier,max_concurrent_agents,hourly_cost,
			status,last_seen,server_info,created_at,flavor,enabled,tls_mode,tls_fingerprint,lan_unencrypted,attestations,wire_apis,
			request_overrides,responses_validated_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, p.Kind, p.BaseURL, p.KeyRef, p.DataClass, p.Tier, p.MaxConcurrent, p.HourlyCost, p.Status, 0,
			string(p.ServerInfo), ms(p.CreatedAt), p.Flavor, b2i(p.Enabled), p.TLSMode, p.TLSFingerprint, b2i(p.LANUnencrypted),
			jsonString(p.Attestations), jsonString(p.WireAPIs), string(p.RequestOverrides), 0, ms(p.UpdatedAt))
		return err
	})
	if err != nil {
		if p.KeyRef != "" {
			_ = r.secrets.Delete(ctx, p.KeyRef)
		}
		return Provider{}, err
	}
	r.emit(ctx, "provider.registered", map[string]any{"providerId": p.ID, "name": p.Name, "kind": p.Kind,
		"dataClass": p.DataClass, "tier": p.Tier, "tlsMode": p.TLSMode, "lanUnencrypted": p.LANUnencrypted})
	return p, nil
}

const providerCols = `id,name,kind,base_url,api_key_ref,data_class,tier,max_concurrent_agents,hourly_cost,status,last_seen,server_info,
created_at,flavor,enabled,tls_mode,tls_fingerprint,lan_unencrypted,attestations,wire_apis,request_overrides,responses_validated_at,updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanProvider(s scanner) (Provider, error) {
	var p Provider
	var lastSeen, created, validated, updated int64
	var enabled, lan int
	var att, wire, over, info string
	err := s.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.KeyRef, &p.DataClass, &p.Tier, &p.MaxConcurrent, &p.HourlyCost,
		&p.Status, &lastSeen, &info, &created, &p.Flavor, &enabled, &p.TLSMode, &p.TLSFingerprint, &lan, &att, &wire, &over,
		&validated, &updated)
	if err != nil {
		return Provider{}, err
	}
	p.Enabled, p.LANUnencrypted = enabled != 0, lan != 0
	p.LastSeen, p.CreatedAt, p.ResponsesValidatedAt, p.UpdatedAt = fromMS(lastSeen), fromMS(created), fromMS(validated), fromMS(updated)
	p.ServerInfo, p.RequestOverrides = json.RawMessage(info), json.RawMessage(over)
	_ = json.Unmarshal([]byte(att), &p.Attestations)
	_ = json.Unmarshal([]byte(wire), &p.WireAPIs)
	return p, nil
}

// Get returns one provider.
func (r *Registry) Get(ctx context.Context, id string) (Provider, error) {
	p, err := scanProvider(r.db.Reader().QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, fmt.Errorf("%w: provider %s", ErrNotFound, id)
	}
	return p, err
}

// List returns all providers by name.
func (r *Registry) List(ctx context.Context) ([]Provider, error) {
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update replaces the editable fields of p (name, base URL, class, tier, concurrency, attestations, overrides, enabled)
// after re-running the validation, including the transport rule. A non-empty newAPIKey replaces the stored key.
func (r *Registry) Update(ctx context.Context, p Provider, newAPIKey string) (Provider, error) {
	cur, err := r.Get(ctx, p.ID)
	if err != nil {
		return Provider{}, err
	}
	p.CreatedAt, p.KeyRef, p.Status, p.LastSeen, p.ServerInfo = cur.CreatedAt, cur.KeyRef, cur.Status, cur.LastSeen, cur.ServerInfo
	if p.ResponsesValidatedAt.IsZero() {
		p.ResponsesValidatedAt = cur.ResponsesValidatedAt
	}
	if err := r.validate(ctx, &p); err != nil {
		return Provider{}, err
	}
	// A stored key belongs to the address the person gave it for. Pointing the provider at another address without giving a key
	// for it would send the old key there, so the old key is forgotten (audit 2026-10-16, M9).
	keyCleared := false
	if newAPIKey == "" && cur.KeyRef != "" && !SameOrigin(cur.BaseURL, p.BaseURL) {
		if r.secrets != nil {
			if err := r.secrets.Delete(ctx, cur.KeyRef); err != nil {
				return Provider{}, fmt.Errorf("forget the api key of the old address: %w", err)
			}
		}
		p.KeyRef, keyCleared = "", true
	}
	if newAPIKey != "" {
		if r.secrets == nil {
			return Provider{}, ErrNoSecretStore
		}
		if p.KeyRef == "" {
			p.KeyRef = "provider/" + p.ID + "/api-key"
		}
		if err := r.secrets.Put(ctx, p.KeyRef, newAPIKey); err != nil {
			return Provider{}, fmt.Errorf("store api key: %w", err)
		}
	}
	p.UpdatedAt = time.Now().UTC()
	err = r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE providers SET name=?,kind=?,base_url=?,api_key_ref=?,data_class=?,tier=?,max_concurrent_agents=?,
			hourly_cost=?,flavor=?,enabled=?,tls_mode=?,tls_fingerprint=?,lan_unencrypted=?,attestations=?,wire_apis=?,
			request_overrides=?,responses_validated_at=?,updated_at=? WHERE id=?`,
			p.Name, p.Kind, p.BaseURL, p.KeyRef, p.DataClass, p.Tier, p.MaxConcurrent, p.HourlyCost, p.Flavor, b2i(p.Enabled),
			p.TLSMode, p.TLSFingerprint, b2i(p.LANUnencrypted), jsonString(p.Attestations), jsonString(p.WireAPIs),
			string(p.RequestOverrides), ms(p.ResponsesValidatedAt), ms(p.UpdatedAt), p.ID)
		return err
	})
	if err != nil {
		return Provider{}, err
	}
	r.dropClient(p.ID)
	r.emit(ctx, "provider.updated", map[string]any{"providerId": p.ID, "keyCleared": keyCleared})
	return p, nil
}

// SameOrigin reports whether two addresses have the same scheme, host and port: the unit a credential belongs to. Addresses
// that do not parse are different unless they are equal.
func SameOrigin(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Hostname(), ub.Hostname()) && portOf(ua) == portOf(ub)
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// SetEnabled enables or disables a provider.
func (r *Registry) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return r.exec(ctx, id, `UPDATE providers SET enabled=?, updated_at=? WHERE id=?`, b2i(enabled), storage.Now(), id)
}

// Delete removes the provider, its catalog and quota rows (cascade) and its stored API key.
func (r *Registry) Delete(ctx context.Context, id string) error {
	p, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := r.exec(ctx, id, `DELETE FROM providers WHERE id=?`, id); err != nil {
		return err
	}
	if p.KeyRef != "" && r.secrets != nil {
		_ = r.secrets.Delete(ctx, p.KeyRef)
	}
	_ = r.db.WriteTx(ctx, func(tx *sql.Tx) error { // the endpoint uptime threshold (uptime.go) goes with the provider
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, settingMinEndpointUptime+id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM model_retention_blocks WHERE provider_id=?`, id) // and its retention marks (retention.go)
		return err
	})
	r.dropClient(id)
	r.emit(ctx, "provider.removed", map[string]any{"providerId": id})
	return nil
}

func (r *Registry) exec(ctx context.Context, id, q string, args ...any) error {
	var n int64
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(q, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: provider %s", ErrNotFound, id)
	}
	return nil
}

// APIKey resolves the provider's key from the SecretStore. An empty KeyRef (a keyless host) returns "".
func (r *Registry) APIKey(ctx context.Context, p Provider) (string, error) {
	if p.KeyRef == "" {
		return "", nil
	}
	if r.secrets == nil {
		return "", ErrNoSecretStore
	}
	return r.secrets.Get(ctx, p.KeyRef)
}

// SetResponsesValidated records the result of the Codex Responses connection test (D3).
func (r *Registry) SetResponsesValidated(ctx context.Context, id string, at time.Time) error {
	return r.exec(ctx, id, `UPDATE providers SET responses_validated_at=?, updated_at=? WHERE id=?`, ms(at), storage.Now(), id)
}

// setMaxConcurrentIfZero sets max_concurrent_agents when the user left it unlimited.
func (r *Registry) setMaxConcurrentIfZero(ctx context.Context, id string, n int) error {
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE providers SET max_concurrent_agents=? WHERE id=? AND max_concurrent_agents=0`, n, id)
		return err
	})
}

// setStatus records probe or health results.
func (r *Registry) setStatus(ctx context.Context, id string, status Health, seen time.Time, flavor Flavor, info json.RawMessage) error {
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		q := `UPDATE providers SET status=?, last_seen=CASE WHEN ?>0 THEN ? ELSE last_seen END`
		args := []any{string(status), ms(seen), ms(seen)}
		if flavor != "" {
			q += `, flavor=?`
			args = append(args, string(flavor))
		}
		if len(info) > 0 {
			q += `, server_info=?`
			args = append(args, string(info))
		}
		q += ` WHERE id=?`
		args = append(args, id)
		_, err := tx.Exec(q, args...)
		return err
	})
}

// HTTPClient returns the provider's HTTP client (transport rule enforced at dial time; pinned fingerprint applied).
func (r *Registry) HTTPClient(p Provider) (*http.Client, error) {
	sig := fmt.Sprintf("%s|%s|%s|%s", p.BaseURL, p.TLSMode, p.TLSFingerprint, p.ID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.clients[p.ID]; ok && e.sig == sig {
		return e.hc, nil
	}
	c, err := NewHTTPClient(p, r.resolve, 0)
	if err != nil {
		return nil, err
	}
	r.clients[p.ID] = &clientEntry{sig: sig, hc: c}
	return c, nil
}

func (r *Registry) dropClient(id string) {
	r.mu.Lock()
	delete(r.clients, id)
	r.mu.Unlock()
}

func (r *Registry) emit(ctx context.Context, typ string, data any) {
	if r.bus == nil {
		return
	}
	_, _ = events.Emit(ctx, r.bus, typ, events.Event{}, data)
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		if _, isMap := v.(map[string]bool); isMap {
			return "{}"
		}
		return "[]"
	}
	return string(b)
}

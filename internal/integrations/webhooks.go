package integrations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/safe"
)

// The webhook receiver (decision P114). Drydock is a desktop app behind the person's router, so a vendor can reach it only if
// the person exposes the port themselves (their own tunnel or port forward); Drydock never does. The receiver is off by
// default, listens on 127.0.0.1 only, on a port the person chooses, and treats every delivery as a hint, never as data:
//
//   - each vendor's signature is checked over the raw body with the secret the person pasted (Linear's signing secret, Sentry's
//     client secret, the verification token Notion sent); a request without one, or with a wrong one, is refused;
//   - the body is at most 1 MiB; the signed timestamp must be recent (Linear: 60 seconds, as its SDK checks; Sentry and Notion,
//     which document no window: 5 minutes); a delivery id seen before is refused (the replay guard);
//   - the only thing taken from a verified payload is the id of the item it names. That item is queued and read again through the
//     normal adapter with the connection's credential, then stored like any synced item (cleaned, scored, rules); nothing in the
//     payload becomes item text, state or anything else.
//
// What the vendors document, and what is unverified: Linear's header and check are from its SDK (packages/sdk/src/webhooks,
// @linear/sdk 100.0.0: Linear-Signature = hex HMAC-SHA256 of the raw body with the webhook's signing secret; webhookTimestamp in
// the signed body within a minute). Sentry's are from its documentation (sentry-docs webhooks.mdx: Sentry-Hook-Signature = hex
// HMAC-SHA256 with the integration's client secret, Sentry-Hook-Resource, Sentry-Hook-Timestamp, Request-ID); its examples sign
// a re-serialized body, Drydock checks the raw body as sent. Notion's (X-Notion-Signature: "sha256=" + hex HMAC-SHA256 of the raw
// body keyed with the verification token; the one-time {"verification_token": ...} request) are from memory of Notion's
// documentation, which could not be read from the build machine: unverified.

const (
	hookEnabledKey   = "integrations.webhooks.enabled"
	hookPortKey      = "integrations.webhooks.port"
	maxHookBody      = 1 << 20
	maxHookQueue     = 500
	hookWindowLinear = time.Minute
	hookWindow       = 5 * time.Minute
)

// Webhook delivery outcomes.
const (
	HookQueued        = "queued"
	HookIgnored       = "ignored"
	HookVerification  = "verification"
	HookUnsigned      = "refused_unsigned"
	HookBadSignature  = "refused_signature"
	HookStale         = "refused_stale"
	HookReplay        = "refused_replay"
	HookTooLarge      = "refused_size"
	HookMalformed     = "refused_malformed"
	HookBusy          = "refused_busy"
	HookNoConnection  = "refused_not_connected"
	EventWebhookQueue = "integration.webhook.queued"
)

// hookKinds are the vendors the receiver accepts, with the secret each one signs with.
var hookKinds = map[string]struct{ Header, Secret, Where string }{
	KindLinear: {Header: "Linear-Signature", Secret: "signing secret", Where: "Linear: Settings > API > Webhooks > New webhook; copy its signing secret."},
	KindSentry: {Header: "Sentry-Hook-Signature", Secret: "client secret", Where: "Sentry: Settings > Developer Settings > your integration (with the issue webhook); copy its client secret."},
	KindNotion: {Header: "X-Notion-Signature", Secret: "verification token", Where: "Notion: your integration's Webhooks tab > Create a subscription with this address; Notion then sends a verification token, which appears here to use and to paste back into Notion."},
}

type hookJob struct {
	IntegrationID string
	ExternalID    string
	At            int64
}

type notionPending struct {
	Token string
	At    int64
}

type webhookReceiver struct {
	mu      sync.Mutex
	srv     *http.Server
	port    int
	err     string
	queue   map[string]hookJob
	pending *notionPending
	wake    chan struct{}
}

func hookSecretRef(id string) string { return "integrations/" + id + "/webhook" }

// WebhookEndpoint is the receiver's address for one vendor and whether its secret is set.
type WebhookEndpoint struct {
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	Path          string `json:"path"`
	URL           string `json:"url,omitempty"` // http://127.0.0.1:<port><path>: what the person's own tunnel forwards to
	IntegrationID string `json:"integrationId,omitempty"`
	HasSecret     bool   `json:"hasSecret"`
	Secret        string `json:"secret"` // what the secret is called at the vendor (never its value)
	Header        string `json:"header"`
	Where         string `json:"where"`
}

// WebhookDelivery is one answered delivery, without its body.
type WebhookDelivery struct {
	Kind       string `json:"kind"`
	Received   int64  `json:"receivedAt"`
	Outcome    string `json:"outcome"`
	ExternalID string `json:"externalId,omitempty"`
}

// NotionVerification is the token Notion sent to the receiver when a subscription was created, offered to the person: they
// paste it into Notion to verify the subscription and choose Use this token so Drydock checks Notion's signatures with it.
type NotionVerification struct {
	Token    string `json:"token"`
	Received int64  `json:"receivedAt"`
}

// WebhookStatus is the receiver's settings and state.
type WebhookStatus struct {
	Enabled    bool                `json:"enabled"`
	Port       int                 `json:"port"`
	Listening  bool                `json:"listening"`
	Error      string              `json:"error,omitempty"`
	Address    string              `json:"address,omitempty"`
	Endpoints  []WebhookEndpoint   `json:"endpoints"`
	Queued     int                 `json:"queued"`
	Recent     []WebhookDelivery   `json:"recent"`
	Notion     *NotionVerification `json:"notionVerification,omitempty"`
	ExposeNote string              `json:"exposeNote"`
}

const exposeNote = "Drydock listens only on this computer (127.0.0.1). The services reach it only if you expose this port yourself, for example with your own tunnel; Drydock does not do that for you."

func (s *Service) hookSettings(ctx context.Context) (bool, int) {
	en, _ := s.setting(ctx, hookEnabledKey)
	p, _ := s.setting(ctx, hookPortKey)
	port, _ := strconv.Atoi(p)
	return en == "1", port
}

// WebhookStatus reports the receiver's settings, its state, the address of each vendor and the latest deliveries.
func (s *Service) WebhookStatus(ctx context.Context) (WebhookStatus, error) {
	en, port := s.hookSettings(ctx)
	st := WebhookStatus{Enabled: en, Port: port, Endpoints: []WebhookEndpoint{}, Recent: []WebhookDelivery{}, ExposeNote: exposeNote}
	s.hooks.mu.Lock()
	st.Listening = s.hooks.srv != nil
	st.Error = s.hooks.err
	st.Queued = len(s.hooks.queue)
	if p := s.hooks.pending; p != nil {
		st.Notion = &NotionVerification{Token: p.Token, Received: p.At}
	}
	s.hooks.mu.Unlock()
	if port > 0 {
		st.Address = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		return st, err
	}
	for _, k := range []string{KindLinear, KindSentry, KindNotion} {
		h := hookKinds[k]
		ep := WebhookEndpoint{Kind: k, Label: vendor(k), Path: "/hooks/" + k, Secret: h.Secret, Header: h.Header, Where: h.Where}
		if st.Address != "" {
			ep.URL = st.Address + ep.Path
		}
		for _, c := range conns {
			if c.Kind == k {
				ep.IntegrationID = c.ID
				ep.HasSecret = s.hookSecret(ctx, c.ID) != ""
			}
		}
		st.Endpoints = append(st.Endpoints, ep)
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT kind, received_at, outcome, external_id FROM integration_webhook_deliveries ORDER BY received_at DESC LIMIT 20`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.Kind, &d.Received, &d.Outcome, &d.ExternalID); err != nil {
			return st, err
		}
		st.Recent = append(st.Recent, d)
	}
	return st, rows.Err()
}

// WebhookSettingsReq turns the receiver on or off and sets its port.
type WebhookSettingsReq struct {
	Enabled *bool `json:"enabled"`
	Port    *int  `json:"port"`
}

// SetWebhooks changes the receiver's settings and starts or stops it. Only a human can.
func (s *Service) SetWebhooks(ctx context.Context, by string, r WebhookSettingsReq) (WebhookStatus, error) {
	if err := requireHuman(by); err != nil {
		return WebhookStatus{}, err
	}
	en, port := s.hookSettings(ctx)
	if r.Port != nil {
		if *r.Port != 0 && (*r.Port < 1024 || *r.Port > 65535) {
			return WebhookStatus{}, errf(ErrBadRequest, "choose a port between 1024 and 65535")
		}
		port = *r.Port
	}
	if r.Enabled != nil {
		en = *r.Enabled
	}
	if en && port == 0 {
		return WebhookStatus{}, errf(ErrBadRequest, "choose the port the receiver listens on first")
	}
	if err := s.putSetting(ctx, hookPortKey, strconv.Itoa(port)); err != nil {
		return WebhookStatus{}, err
	}
	if err := s.putSetting(ctx, hookEnabledKey, map[bool]string{true: "1", false: "0"}[en]); err != nil {
		return WebhookStatus{}, err
	}
	s.emit(ctx, "integration.webhooks.changed", "", "", map[string]any{"enabled": en, "port": port, "by": by})
	s.applyWebhooks(en, port)
	return s.WebhookStatus(ctx)
}

// StartWebhooks starts the receiver when the person turned it on (the core extension calls it at start).
func (s *Service) StartWebhooks(ctx context.Context) {
	en, port := s.hookSettings(ctx)
	s.applyWebhooks(en, port)
}

func (s *Service) applyWebhooks(enabled bool, port int) {
	s.hooks.mu.Lock()
	defer s.hooks.mu.Unlock()
	if s.hooks.srv != nil && (!enabled || s.hooks.port != port) {
		_ = s.hooks.srv.Close()
		s.hooks.srv = nil
	}
	s.hooks.err = ""
	if !enabled || port == 0 || s.hooks.srv != nil {
		return
	}
	// 127.0.0.1 only: never every interface. The person's tunnel connects to this address.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		s.hooks.err = fmt.Sprintf("Drydock could not listen on 127.0.0.1:%d (%v). Choose another port.", port, err)
		return
	}
	srv := &http.Server{Handler: s.WebhookHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	s.hooks.srv, s.hooks.port = srv, port
	safe.Go("integrations.webhooks.serve", func() { _ = srv.Serve(ln) })
}

func (s *Service) stopWebhooks() {
	s.hooks.mu.Lock()
	defer s.hooks.mu.Unlock()
	if s.hooks.srv != nil {
		_ = s.hooks.srv.Close()
		s.hooks.srv = nil
	}
}

func (s *Service) hookSecret(ctx context.Context, integrationID string) string {
	if s.o.Vault == nil {
		return ""
	}
	v, err := s.o.Vault.Get(ctx, hookSecretRef(integrationID))
	if err != nil {
		return ""
	}
	return v
}

// SetWebhookSecret stores (or, empty, removes) the secret a connection's webhooks are signed with, in the credential store. Only
// a human can; the value is never returned, logged or published.
func (s *Service) SetWebhookSecret(ctx context.Context, by, integrationID, secret string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	c, err := s.st.GetConnection(ctx, integrationID)
	if err != nil {
		return err
	}
	if _, ok := hookKinds[c.Kind]; !ok {
		return errf(ErrBadRequest, "%s webhooks are not received by Drydock", vendor(c.Kind))
	}
	if s.o.Vault == nil {
		return ErrCredentialsLocked
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		if err := s.o.Vault.Delete(ctx, hookSecretRef(c.ID)); err != nil {
			return err
		}
		if s.o.UnregisterSecret != nil {
			s.o.UnregisterSecret("integration:" + c.ID + ":webhook")
		}
	} else {
		if len(secret) > 1024 || !tokenRe.MatchString(secret) {
			return errf(ErrBadRequest, "the secret must be 8 to 1024 printable characters without spaces or line breaks")
		}
		if err := s.o.Vault.Put(ctx, hookSecretRef(c.ID), secret); err != nil {
			return fmt.Errorf("integrations: could not store the webhook secret in the credential store: %w", err)
		}
		if s.o.RegisterSecret != nil {
			s.o.RegisterSecret("integration:"+c.ID+":webhook", secret)
		}
	}
	s.emit(ctx, "integration.webhooks.secret_changed", "", "", map[string]any{"integrationId": c.ID, "kind": c.Kind, "set": secret != "", "by": by})
	return nil
}

// UseNotionVerification stores the verification token Notion sent as the Notion connection's webhook secret. Only a human can:
// anything that reaches the port could send a token, so it is used only when the person, who just created the subscription in
// Notion, says so.
func (s *Service) UseNotionVerification(ctx context.Context, by string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	s.hooks.mu.Lock()
	p := s.hooks.pending
	s.hooks.mu.Unlock()
	if p == nil {
		return errf(ErrBadRequest, "Notion has not sent a verification token to the receiver")
	}
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if c.Kind == KindNotion {
			if err := s.SetWebhookSecret(ctx, by, c.ID, p.Token); err != nil {
				return err
			}
			s.hooks.mu.Lock()
			s.hooks.pending = nil
			s.hooks.mu.Unlock()
			return nil
		}
	}
	return errf(ErrBadRequest, "connect Notion first")
}

// ---- the handler ----

var hookIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
var hookItemRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

// WebhookHandler answers POST /hooks/linear, /hooks/sentry and /hooks/notion.
func (s *Service) WebhookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, ok := strings.CutPrefix(r.URL.Path, "/hooks/")
		if _, known := hookKinds[kind]; !ok || !known {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		status, outcome := s.receive(r.Context(), kind, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"outcome":"` + outcome + `"}`))
	})
}

func hmacHex(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func sigEqual(got, want string) bool {
	return hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(got))), []byte(want))
}

// receive checks one delivery and queues the item it names. It returns the HTTP status and the outcome.
func (s *Service) receive(ctx context.Context, kind string, r *http.Request) (int, string) {
	now := s.o.Now()
	record := func(id, conn, outcome, ext string) {
		s.recordDelivery(ctx, id, conn, kind, outcome, ext, now)
	}
	refuse := func(status int, outcome string, conn string) (int, string) {
		record("", conn, outcome, "")
		return status, outcome
	}
	if r.ContentLength > maxHookBody {
		return refuse(http.StatusRequestEntityTooLarge, HookTooLarge, "")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
	if err != nil {
		return refuse(http.StatusBadRequest, HookMalformed, "")
	}
	if len(body) > maxHookBody {
		return refuse(http.StatusRequestEntityTooLarge, HookTooLarge, "")
	}
	conns, err := s.st.ListConnections(ctx)
	if err != nil {
		return http.StatusServiceUnavailable, HookBusy
	}
	var conn *Connection
	for i := range conns {
		if conns[i].Kind == kind {
			conn = &conns[i]
		}
	}
	if conn == nil {
		return refuse(http.StatusNotFound, HookNoConnection, "")
	}
	sig := r.Header.Get(hookKinds[kind].Header)
	if kind == KindNotion && sig == "" {
		// Notion's one-time verification request carries the token and no signature: it is offered to the person, never used
		var v map[string]any
		if json.Unmarshal(body, &v) == nil && len(v) == 1 {
			if tok, ok := v["verification_token"].(string); ok && tokenRe.MatchString(tok) && len(tok) <= 1024 {
				s.hooks.mu.Lock()
				s.hooks.pending = &notionPending{Token: tok, At: now.UnixMilli()}
				s.hooks.mu.Unlock()
				if s.o.RegisterSecret != nil {
					s.o.RegisterSecret("integration:"+conn.ID+":webhook-pending", tok)
				}
				record("", conn.ID, HookVerification, "")
				return http.StatusOK, HookVerification
			}
		}
	}
	secret := s.hookSecret(ctx, conn.ID)
	if sig == "" || secret == "" {
		return refuse(http.StatusUnauthorized, HookUnsigned, conn.ID)
	}
	if kind == KindNotion {
		sig = strings.TrimPrefix(strings.TrimSpace(sig), "sha256=")
	}
	if !sigEqual(sig, hmacHex(secret, body)) {
		return refuse(http.StatusUnauthorized, HookBadSignature, conn.ID)
	}
	// verified: the body is the vendor's, still only a hint
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var p map[string]any
	if dec.Decode(&p) != nil || p == nil {
		return refuse(http.StatusBadRequest, HookMalformed, conn.ID)
	}
	var ts time.Time
	var deliveryID, ext string
	window := hookWindow
	switch kind {
	case KindLinear:
		window = hookWindowLinear
		if ms, ok := num(p["webhookTimestamp"]); ok && ms > 0 {
			ts = time.UnixMilli(ms)
		}
		deliveryID = r.Header.Get("Linear-Delivery")
		if deliveryID == "" && str(p["webhookId"]) != "" {
			deliveryID = str(p["webhookId"]) + "-" + str(p["webhookTimestamp"])
		}
		data := asMap(p["data"])
		switch str(p["type"]) {
		case "Issue":
			ext = str(data["id"])
		case "Comment", "Attachment":
			ext = str(data["issueId"])
		}
	case KindSentry:
		if f, err := strconv.ParseFloat(strings.TrimSpace(r.Header.Get("Sentry-Hook-Timestamp")), 64); err == nil && f > 0 {
			ts = time.Unix(int64(f), 0)
		}
		deliveryID = r.Header.Get("Request-ID")
		data := asMap(p["data"])
		switch r.Header.Get("Sentry-Hook-Resource") {
		case "issue":
			ext = str(asMap(data["issue"])["id"])
		case "error":
			ext = str(asMap(data["error"])["issue_id"])
		case "event_alert":
			ext = str(asMap(data["event"])["issue_id"])
		case "comment":
			ext = str(data["issue_id"])
		}
	case KindNotion:
		if t := millis(p["timestamp"]); t > 0 {
			ts = time.UnixMilli(t)
		}
		deliveryID = str(p["id"])
		typ := str(p["type"])
		ent := asMap(p["entity"])
		switch {
		case strings.HasPrefix(typ, "page.") && str(ent["type"]) == "page":
			ext = str(ent["id"])
		case strings.HasPrefix(typ, "comment."):
			ext = str(asMap(p["data"])["page_id"])
		}
	}
	if ts.IsZero() || now.Sub(ts) > window || ts.Sub(now) > window {
		return refuse(http.StatusBadRequest, HookStale, conn.ID)
	}
	key := kind + ":"
	if hookIDRe.MatchString(deliveryID) {
		key += deliveryID
	} else {
		key += "body-" + hashHex(string(body))[:32]
	}
	if !hookItemRe.MatchString(ext) {
		ext = ""
	}
	outcome := HookIgnored
	if ext != "" {
		outcome = HookQueued
	}
	inserted, err := s.insertDelivery(ctx, key, conn.ID, kind, outcome, ext, now)
	if err != nil {
		return http.StatusServiceUnavailable, HookBusy
	}
	if !inserted {
		return refuse(http.StatusConflict, HookReplay, conn.ID)
	}
	if ext == "" {
		return http.StatusOK, HookIgnored
	}
	if !s.queueItem(conn.ID, ext) {
		return http.StatusServiceUnavailable, HookBusy
	}
	s.emit(ctx, EventWebhookQueue, "", "", map[string]any{"integrationId": conn.ID, "kind": kind, "externalId": ext})
	return http.StatusOK, HookQueued
}

// insertDelivery records a verified delivery under its id; false when the id was seen before (a replay).
func (s *Service) insertDelivery(ctx context.Context, id, conn, kind, outcome, ext string, now time.Time) (bool, error) {
	var n int64
	err := s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM integration_webhook_deliveries WHERE received_at<?`, now.Add(-24*time.Hour).UnixMilli()); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO integration_webhook_deliveries(id,integration_id,kind,received_at,outcome,external_id) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
			id, conn, kind, now.UnixMilli(), outcome, ext)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n == 1, err
}

// recordDelivery records a refused or ignored delivery under a fresh id, at most 1000 an hour (a flood is not stored).
func (s *Service) recordDelivery(ctx context.Context, id, conn, kind, outcome, ext string, now time.Time) {
	var recent int
	_ = s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_webhook_deliveries WHERE received_at>=?`, now.Add(-time.Hour).UnixMilli()).Scan(&recent)
	if recent >= 1000 {
		return
	}
	if id == "" {
		id = kind + ":" + ids.New("hook")
	}
	_, _ = s.insertDelivery(ctx, id, conn, kind, outcome, ext, now)
}

func (s *Service) queueItem(integrationID, externalID string) bool {
	s.hooks.mu.Lock()
	defer s.hooks.mu.Unlock()
	key := integrationID + "/" + externalID
	if _, ok := s.hooks.queue[key]; !ok && len(s.hooks.queue) >= maxHookQueue {
		return false
	}
	s.hooks.queue[key] = hookJob{IntegrationID: integrationID, ExternalID: externalID, At: s.nowMS()}
	if s.hooks.wake != nil {
		select {
		case s.hooks.wake <- struct{}{}:
		default:
		}
	}
	return true
}

// ProcessWebhookQueue reads every queued item through its adapter. A connection whose sync is running keeps its items queued for
// the next round. It returns how many items it read.
func (s *Service) ProcessWebhookQueue(ctx context.Context) int {
	s.hooks.mu.Lock()
	jobs := make([]hookJob, 0, len(s.hooks.queue))
	for _, j := range s.hooks.queue {
		jobs = append(jobs, j)
	}
	s.hooks.mu.Unlock()
	n := 0
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		_, err := s.SyncItem(ctx, j.IntegrationID, j.ExternalID)
		var f *Failure
		if errors.Is(err, ErrSyncBusy) || (errors.As(err, &f) && (f.Code == CodeRateLimited || f.Code == CodeUnreachable || f.Code == CodeTimeout)) {
			continue // stays queued: the next round reads it (a rate limit holds the connection's limiter until its reset)
		}
		s.hooks.mu.Lock()
		if cur, ok := s.hooks.queue[j.IntegrationID+"/"+j.ExternalID]; ok && cur.At == j.At {
			delete(s.hooks.queue, j.IntegrationID+"/"+j.ExternalID)
		}
		s.hooks.mu.Unlock()
		if err != nil {
			s.o.Logf("integrations: webhook item %s: %s", j.IntegrationID, s.scrub(err.Error()))
			continue
		}
		n++
	}
	return n
}

// itemFetcher is an adapter that reads one item by its id and says whether it is in an assignment's scope.
type itemFetcher interface {
	Fetch(ctx context.Context, a *API, cfg map[string]string, externalID string) (raw RawItem, inScope bool, err error)
}

// SyncItem reads one item of a connection through its adapter, for every enabled assignment whose scope holds it, and stores it
// like a synced item (cleaned, scored, rules evaluated). An item the vendor no longer has, or that left an assignment's scope, is
// marked gone there. The cursor is not moved.
func (s *Service) SyncItem(ctx context.Context, integrationID, externalID string) (SyncResult, error) {
	res := SyncResult{IntegrationID: integrationID}
	lk := s.lock("sync:" + integrationID)
	if !lk.TryLock() {
		return res, ErrSyncBusy
	}
	defer lk.Unlock()
	conn, err := s.st.GetConnection(ctx, integrationID)
	if err != nil {
		return res, err
	}
	if !conn.Enabled || conn.Status == StatusUnauthenticated {
		res.Skipped = "the connection is paused"
		return res, nil
	}
	ad, _ := AdapterFor(conn.Kind)
	f, ok := ad.(itemFetcher)
	if !ok {
		return res, errf(ErrOpUnsupported, "%s items cannot be read one at a time", vendor(conn.Kind))
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return res, s.scrubFailure(fail)
	}
	asgs, err := s.st.ListAssignments(ctx, conn.ID)
	if err != nil {
		return res, err
	}
	for _, asg := range asgs {
		if !asg.Enabled {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
		raw, in, ferr := f.Fetch(cctx, api, asg.Config, externalID)
		cancel()
		if ferr != nil {
			var fl *Failure
			if !errors.As(ferr, &fl) {
				fl = Classify(conn.Kind, ferr, "")
			}
			return res, s.scrubFailure(fl)
		}
		if !in || raw.ExternalID == "" {
			if n, err := s.st.MarkItemGone(ctx, conn.ID, asg.ID, externalID); err == nil {
				res.Gone += n
			}
			continue
		}
		res.Seen++
		detail := ""
		if raw.NeedsBody {
			dctx, dcancel := context.WithTimeout(ctx, s.o.CallTimeout)
			if text, has, derr := ad.Detail(dctx, api, asg.Config, raw.ExternalID); derr == nil && has {
				detail = text
			}
			dcancel()
		}
		if conn.Kind == KindNotion {
			pm, _ := s.st.ListMappings(ctx, asg.ID, MapProperty)
			applyPropMapping(&raw, pm)
		}
		rec, _ := s.buildRecord(ctx, conn, asg, raw, detail)
		cur, err := s.st.GetCursor(ctx, conn.ID, asg.ID)
		if err != nil {
			return res, err
		}
		pr, err := s.st.CommitPage(ctx, PageCommit{Items: []ItemRecord{rec}, Cursor: cur, IntegrationID: conn.ID, AssignmentID: asg.ID})
		if err != nil {
			return res, err
		}
		res.New += pr.New
		res.Changed += pr.Changed
		res.Unchanged += pr.Unchanged
		res.Gone += pr.Closed
		s.afterCommit(ctx, conn, asg, pr)
		res.RuleResults = append(res.RuleResults, s.EvaluateRules(ctx, conn, asg)...)
	}
	res.Complete = true
	return res, nil
}

// MarkItemGone marks one stored item of an assignment gone.
func (s *Store) MarkItemGone(ctx context.Context, integrationID, assignmentID, externalID string) (int, error) {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE external_items SET gone=1, version=version+1 WHERE integration_id=? AND assignment_id=? AND external_id=? AND gone=0`, integrationID, assignmentID, externalID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

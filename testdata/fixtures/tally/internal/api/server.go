// Package api is Tally's JSON HTTP API (version 1). Every request under /v1/ presents an API key as
// "Authorization: Bearer <token>"; the key acts as the member who created it, in that member's organization, with the
// member's current role. The API only ever shows the caller's organization's data.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

// Options configure a Server.
type Options struct {
	Clock         clock.Clock            // nil: clock.Real
	Payments      *payments.Service      // required
	Subscriptions *subscriptions.Service // required
	Webhooks      *webhooks.Service      // nil: a service with the default HTTP client and resolver
	Mail          mail.Sender            // nil: an Outbox (nothing is sent)
	Logger        *slog.Logger           // nil: discard
	// BaseURL is the dashboard's address, for links in emails (default "https://app.tally.example").
	BaseURL string
}

// Server serves the API.
type Server struct {
	store   *store.Store
	clock   clock.Clock
	pay     *payments.Service
	subs    *subscriptions.Service
	hooks   *webhooks.Service
	mail    mail.Sender
	log     *slog.Logger
	baseURL string
	mux     *http.ServeMux
	handler http.Handler
}

// New returns a server over a store.
func New(st *store.Store, opt Options) *Server {
	if opt.Payments == nil || opt.Subscriptions == nil {
		panic("api.New: Options.Payments and Options.Subscriptions are required")
	}
	s := &Server{store: st, clock: opt.Clock, pay: opt.Payments, subs: opt.Subscriptions, hooks: opt.Webhooks, mail: opt.Mail, log: opt.Logger,
		baseURL: strings.TrimRight(opt.BaseURL, "/"), mux: http.NewServeMux()}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	if s.hooks == nil {
		s.hooks = webhooks.New(webhooks.Options{Store: st, Clock: s.clock})
	}
	if s.mail == nil {
		s.mail = &mail.Outbox{}
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if s.baseURL == "" {
		s.baseURL = "https://app.tally.example"
	}
	s.routes()
	s.handler = s.withRequestID(s.recoverPanics(s.mux))
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /v1/me", s.authed(s.getMe))

	m.HandleFunc("GET /v1/org", s.authed(s.getOrg))
	m.HandleFunc("PATCH /v1/org", s.authed(s.updateOrg))
	m.HandleFunc("GET /v1/members", s.authed(s.listMembers))
	m.HandleFunc("PATCH /v1/members/{user_id}", s.authed(s.updateMember))
	m.HandleFunc("DELETE /v1/members/{user_id}", s.authed(s.removeMember))
	m.HandleFunc("GET /v1/invites", s.authed(s.listInvites))
	m.HandleFunc("POST /v1/invites", s.authed(s.createInvite))
	m.HandleFunc("DELETE /v1/invites/{id}", s.authed(s.revokeInvite))
	m.HandleFunc("POST /v1/invites/accept", s.acceptInvite) // no API key: the token is the credential
	m.HandleFunc("GET /v1/api-keys", s.authed(s.listAPIKeys))
	m.HandleFunc("POST /v1/api-keys", s.authed(s.createAPIKey))
	m.HandleFunc("DELETE /v1/api-keys/{id}", s.authed(s.revokeAPIKey))

	m.HandleFunc("GET /v1/customers", s.authed(s.listCustomers))
	m.HandleFunc("POST /v1/customers", s.authed(s.createCustomer))
	m.HandleFunc("GET /v1/customers/export.csv", s.authed(s.exportCustomers))
	m.HandleFunc("GET /v1/customers/{id}", s.authed(s.getCustomer))
	m.HandleFunc("PATCH /v1/customers/{id}", s.authed(s.updateCustomer))
	m.HandleFunc("DELETE /v1/customers/{id}", s.authed(s.deleteCustomer))

	m.HandleFunc("GET /v1/plans", s.authed(s.listPlans))
	m.HandleFunc("POST /v1/plans", s.authed(s.createPlan))
	m.HandleFunc("GET /v1/plans/{id}", s.authed(s.getPlan))
	m.HandleFunc("PATCH /v1/plans/{id}", s.authed(s.updatePlan))

	m.HandleFunc("GET /v1/subscriptions", s.authed(s.listSubscriptions))
	m.HandleFunc("POST /v1/subscriptions", s.authed(s.createSubscription))
	m.HandleFunc("GET /v1/subscriptions/{id}", s.authed(s.getSubscription))
	m.HandleFunc("PATCH /v1/subscriptions/{id}", s.authed(s.updateSubscription))
	m.HandleFunc("POST /v1/subscriptions/{id}/cancel", s.authed(s.cancelSubscription))
	m.HandleFunc("POST /v1/subscriptions/{id}/usage", s.authed(s.reportUsage))

	m.HandleFunc("GET /v1/invoices", s.authed(s.listInvoices))
	m.HandleFunc("POST /v1/invoices", s.authed(s.createInvoice))
	m.HandleFunc("GET /v1/invoices/{id}", s.authed(s.getInvoice))
	m.HandleFunc("GET /v1/invoices/{id}/lines", s.authed(s.getInvoiceLines))
	m.HandleFunc("POST /v1/invoices/{id}/pay", s.authed(s.payInvoice))
	m.HandleFunc("POST /v1/invoices/{id}/void", s.authed(s.voidInvoice))
	m.HandleFunc("GET /v1/payments", s.authed(s.listPayments))

	m.HandleFunc("GET /v1/webhook-endpoints", s.authed(s.listEndpoints))
	m.HandleFunc("POST /v1/webhook-endpoints", s.authed(s.createEndpoint))
	m.HandleFunc("GET /v1/webhook-endpoints/{id}", s.authed(s.getEndpoint))
	m.HandleFunc("PATCH /v1/webhook-endpoints/{id}", s.authed(s.updateEndpoint))
	m.HandleFunc("DELETE /v1/webhook-endpoints/{id}", s.authed(s.deleteEndpoint))
	m.HandleFunc("GET /v1/webhook-endpoints/{id}/deliveries", s.authed(s.listDeliveries))
	m.HandleFunc("GET /v1/events", s.authed(s.listEvents))
	m.HandleFunc("GET /v1/events/{id}", s.authed(s.getEvent))

	m.HandleFunc("GET /v1/reports/revenue", s.authed(s.revenueReport))
}

// handler is an endpoint that runs for a known member.
type handler func(w http.ResponseWriter, r *http.Request, p auth.Principal)

// authed resolves the request's API key to the member it acts for, or answers 401. Viewers may only read: any other method
// answers 403.
func (s *Server) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "send an API key as Authorization: Bearer <token>")
			return
		}
		key, ok := s.store.APIKeyByToken(strings.TrimSpace(token))
		if !ok || key.RevokedAt != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unknown or revoked API key")
			return
		}
		m, ok := s.store.Member(key.OrgID, key.UserID)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "the key's owner is no longer a member")
			return
		}
		s.store.TouchAPIKey(key.ID, s.clock.Now())
		p := auth.Principal{OrgID: key.OrgID, UserID: key.UserID, KeyID: key.ID, Role: m.Role}
		if r.Method != http.MethodGet && !auth.CanWrite(p.Role) {
			writeError(w, http.StatusForbidden, "forbidden", "viewers can only read")
			return
		}
		h(w, r, p)
	}
}

type ctxKey int

const requestIDKey ctxKey = 0

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// withRequestID gives every request an ID: the caller's X-Request-Id when it is sensible, a new one otherwise. It is sent
// back in the X-Request-Id header.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDRe.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = "req_" + hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestID returns the ID of the request ctx belongs to.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "method", r.Method, "path", r.URL.Path, "panic", v, "request_id", RequestID(r.Context()))
				writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) publish(ctx context.Context, orgID, typ string, data any) {
	if err := s.hooks.Publish(ctx, orgID, typ, data); err != nil {
		s.log.Error("publish event", "type", typ, "err", err)
	}
}

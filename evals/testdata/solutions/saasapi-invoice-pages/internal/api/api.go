// Package api is the product's JSON HTTP API.
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/saasapi/internal/billing"
	"example.com/saasapi/internal/store"
)

// Options configure a Server.
type Options struct {
	// Now is the server's clock; nil means time.Now. Tests set it to move time without sleeping.
	Now func() time.Time
}

// Server serves the API.
type Server struct {
	store *store.Store
	now   func() time.Time
	mux   *http.ServeMux
}

// New returns a server over a store.
func New(st *store.Store, opt Options) *Server {
	s := &Server{store: st, now: opt.Now, mux: http.NewServeMux()}
	if s.now == nil {
		s.now = time.Now
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /api/customers", s.authed(s.listCustomers))
	s.mux.HandleFunc("POST /api/customers", s.authed(s.createCustomer))
	s.mux.HandleFunc("GET /api/customers/{id}", s.authed(s.getCustomer))
	s.mux.HandleFunc("GET /api/invoices", s.authed(s.listInvoices))
	s.mux.HandleFunc("POST /api/invoices", s.authed(s.createInvoice))
	s.mux.HandleFunc("GET /api/invoices/{id}", s.authed(s.getInvoice))
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// handler is an endpoint that runs for a known account.
type handler func(w http.ResponseWriter, r *http.Request, acct store.Account)

// authed resolves the X-API-Key header to its account, or answers 401.
func (s *Server) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := s.store.AccountForKey(r.Header.Get("X-API-Key"))
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r, acct)
	}
}

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request, acct store.Account) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.Customers(acct.ID)})
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request, acct store.Account) {
	var in struct {
		Name  string     `json:"name"`
		Email string     `json:"email"`
		Plan  store.Plan `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	c, err := s.store.CreateCustomer(acct.ID, in.Name, in.Email, in.Plan, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request, acct store.Account) {
	c, ok := s.store.Customer(acct.ID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// invoiceView is an invoice with its totals.
type invoiceView struct {
	store.Invoice
	Totals billing.Totals `json:"totals"`
}

func view(inv store.Invoice) invoiceView {
	return invoiceView{Invoice: inv, Totals: billing.Compute(inv)}
}

// listInvoices returns one page of the account's invoices, oldest first (ties by id). The cursor names the last invoice of
// the previous page, so invoices created while a client pages appear at the end, never twice and never skipped.
func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request, acct store.Account) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = n
	}
	all := s.store.Invoices(acct.ID)
	start := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		at, id, ok := decodeCursor(c)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		start = sort.Search(len(all), func(i int) bool { return after(all[i], at, id) })
	}
	end := min(start+limit, len(all))
	items := []invoiceView{}
	for _, inv := range all[start:end] {
		items = append(items, view(inv))
	}
	next := ""
	if end < len(all) {
		next = encodeCursor(all[end-1].CreatedAt, all[end-1].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func after(inv store.Invoice, at time.Time, id string) bool {
	if !inv.CreatedAt.Equal(at) {
		return inv.CreatedAt.After(at)
	}
	return inv.ID > id
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", false
	}
	ns, id, ok := strings.Cut(string(b), "|")
	if !ok || id == "" {
		return time.Time{}, "", false
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.Unix(0, n), id, true
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request, acct store.Account) {
	var in struct {
		CustomerID    string           `json:"customer_id"`
		Lines         []store.LineItem `json:"lines"`
		CouponPercent int              `json:"coupon_percent"`
		CreditCents   int64            `json:"credit_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	inv, err := s.store.CreateInvoice(store.Invoice{AccountID: acct.ID, CustomerID: in.CustomerID, Lines: in.Lines, CouponPercent: in.CouponPercent, CreditCents: in.CreditCents}, s.now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view(inv))
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request, acct store.Account) {
	inv, ok := s.store.Invoice(acct.ID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, view(inv))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers {"error": code}.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid")
	default:
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

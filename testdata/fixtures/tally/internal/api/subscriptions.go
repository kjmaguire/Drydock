package api

import (
	"errors"
	"net/http"
	"time"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
)

func subKey(s store.Subscription) cursor { return cursor{T: s.CreatedAt, ID: s.ID} }

func (s *Server) subError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscriptions.ErrPlanInactive):
		writeError(w, http.StatusUnprocessableEntity, "plan_inactive", err.Error())
	case errors.Is(err, subscriptions.ErrCanceled):
		writeError(w, http.StatusConflict, "subscription_canceled", err.Error())
	default:
		s.payError(w, err)
	}
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pg, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	subs := s.store.Subscriptions(p.OrgID, store.SubscriptionFilter{CustomerID: q.Get("customer_id"), Status: store.SubStatus(q.Get("status"))})
	items, next := pageOf(subs, subKey, pg)
	writeJSON(w, http.StatusOK, list{Items: items, NextCursor: next})
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		CustomerID      string `json:"customer_id"`
		PlanID          string `json:"plan_id"`
		Quantity        int    `json:"quantity"`
		DiscountPercent int    `json:"discount_percent"`
	}
	if !decode(w, r, &in) {
		return
	}
	sub, err := s.subs.Create(r.Context(), p.OrgID, subscriptions.CreateInput{CustomerID: in.CustomerID, PlanID: in.PlanID, Quantity: in.Quantity, DiscountPercent: in.DiscountPercent})
	if err != nil && sub.ID == "" {
		s.subError(w, err)
		return
	}
	if err != nil {
		s.log.Error("first invoice", "subscription", sub.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	sub, ok := s.store.Subscription(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such subscription")
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) updateSubscription(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		PlanID   *string `json:"plan_id"`
		Quantity *int    `json:"quantity"`
	}
	if !decode(w, r, &in) {
		return
	}
	sub, err := s.subs.Update(r.Context(), p.OrgID, r.PathValue("id"), subscriptions.UpdateInput{PlanID: in.PlanID, Quantity: in.Quantity})
	if err != nil {
		s.subError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) cancelSubscription(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		AtPeriodEnd bool `json:"at_period_end"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	sub, err := s.subs.Cancel(r.Context(), p.OrgID, r.PathValue("id"), in.AtPeriodEnd)
	if err != nil {
		s.subError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// reportUsage records usage of a metered subscription: {"quantity": n, "timestamp": RFC 3339 (default now)}.
func (s *Server) reportUsage(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Quantity  int64      `json:"quantity"`
		Timestamp *time.Time `json:"timestamp"`
	}
	if !decode(w, r, &in) {
		return
	}
	now := s.clock.Now()
	at := now
	if in.Timestamp != nil {
		at = *in.Timestamp
	}
	u, err := s.store.AddUsage(p.OrgID, r.PathValue("id"), in.Quantity, at, now)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

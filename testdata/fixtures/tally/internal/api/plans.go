package api

import (
	"errors"
	"fmt"
	"net/http"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

// planView is a plan with its price in major units ("12.50") and formatted for people ("$12.50").
type planView struct {
	store.Plan
	Price          string `json:"price"`
	PriceFormatted string `json:"price_formatted"`
}

func viewPlan(p store.Plan) planView {
	return planView{Plan: p, Price: fmt.Sprintf("%.2f", float64(p.UnitAmount)/100), PriceFormatted: money.Format(p.UnitAmount, p.Currency)}
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	items := []planView{}
	for _, pl := range s.store.Plans(p.OrgID) {
		items = append(items, viewPlan(pl))
	}
	writeJSON(w, http.StatusOK, list{Items: items})
}

// createPlan takes the price in major units, as a string ("12.50"), the way people type it.
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Name      string          `json:"name"`
		Currency  money.Currency  `json:"currency"`
		Price     string          `json:"price"`
		Interval  store.Interval  `json:"interval"`
		UsageType store.UsageType `json:"usage_type"`
		TrialDays int             `json:"trial_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	amount, err := money.ParseMajor(in.Price, in.Currency)
	if err != nil {
		if errors.Is(err, money.ErrInvalidAmount) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_amount", err.Error())
			return
		}
		s.storeError(w, err)
		return
	}
	pl, err := s.store.CreatePlan(store.Plan{OrgID: p.OrgID, Name: in.Name, Currency: in.Currency, UnitAmount: amount, Interval: in.Interval,
		UsageType: in.UsageType, TrialDays: in.TrialDays}, s.clock.Now())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewPlan(pl))
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pl, ok := s.store.Plan(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such plan")
		return
	}
	writeJSON(w, http.StatusOK, viewPlan(pl))
}

func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Name   *string `json:"name"`
		Active *bool   `json:"active"`
	}
	if !decode(w, r, &in) {
		return
	}
	pl, err := s.store.UpdatePlan(p.OrgID, r.PathValue("id"), func(pl *store.Plan) error {
		if in.Name != nil {
			pl.Name = *in.Name
		}
		if in.Active != nil {
			pl.Active = *in.Active
		}
		return nil
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewPlan(pl))
}

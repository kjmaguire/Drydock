package api

import (
	"errors"
	"net/http"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/store"
	"example.com/tally/internal/webhooks"
)

func (s *Server) hookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webhooks.ErrInvalidURL):
		writeError(w, http.StatusUnprocessableEntity, "invalid_url", err.Error())
	case errors.Is(err, webhooks.ErrInvalidEvents):
		writeError(w, http.StatusUnprocessableEntity, "invalid_events", err.Error())
	default:
		s.storeError(w, err)
	}
}

func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	writeJSON(w, http.StatusOK, list{Items: s.store.WebhookEndpoints(p.OrgID)})
}

// createEndpoint adds an endpoint: {"url": ..., "events": [...]}. The answer includes the signing secret.
func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.hooks.CheckURL(r.Context(), in.URL); err != nil {
		s.hookError(w, err)
		return
	}
	if err := webhooks.CheckEvents(in.Events); err != nil {
		s.hookError(w, err)
		return
	}
	ep, err := s.store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: p.OrgID, URL: in.URL, Events: in.Events, Secret: webhooks.NewSecret(), Enabled: true}, s.clock.Now())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ep)
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ep, ok := s.store.WebhookEndpoint(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such webhook endpoint")
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (s *Server) updateEndpoint(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		URL     *string   `json:"url"`
		Events  *[]string `json:"events"`
		Enabled *bool     `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.URL != nil {
		if err := s.hooks.CheckURL(r.Context(), *in.URL); err != nil {
			s.hookError(w, err)
			return
		}
	}
	if in.Events != nil {
		if err := webhooks.CheckEvents(*in.Events); err != nil {
			s.hookError(w, err)
			return
		}
	}
	ep, err := s.store.UpdateWebhookEndpoint(p.OrgID, r.PathValue("id"), func(ep *store.WebhookEndpoint) error {
		if in.URL != nil {
			ep.URL = *in.URL
		}
		if in.Events != nil {
			ep.Events = *in.Events
		}
		if in.Enabled != nil {
			ep.Enabled = *in.Enabled
			if ep.Enabled {
				ep.DisabledReason = ""
			}
		}
		return nil
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if err := s.store.DeleteWebhookEndpoint(r.PathValue("id")); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	ep, ok := s.store.WebhookEndpoint(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such webhook endpoint")
		return
	}
	writeJSON(w, http.StatusOK, list{Items: s.store.Deliveries(p.OrgID, ep.ID)})
}

func eventKey(e store.Event) cursor { return cursor{T: e.CreatedAt, ID: e.ID} }

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pg, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next := pageOf(s.store.Events(p.OrgID, r.URL.Query().Get("type")), eventKey, pg)
	writeJSON(w, http.StatusOK, list{Items: items, NextCursor: next})
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	e, ok := s.store.Event(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such event")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

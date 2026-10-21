package api

import (
	"encoding/csv"
	"net/http"
	"time"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/store"
)

func customerKey(c store.Customer) cursor { return cursor{T: c.CreatedAt, ID: c.ID} }

// listCustomers lists the organization's customers, oldest first: all of them, those matching ?q= (name or email contains
// it, ignoring case), or the one with exactly ?email=.
func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pg, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var cs []store.Customer
	switch {
	case q.Get("email") != "":
		cs = []store.Customer{}
		if c, ok := s.store.FindCustomerByEmail(p.OrgID, q.Get("email")); ok {
			cs = append(cs, c)
		}
	case q.Get("q") != "":
		cs = s.store.SearchCustomers(p.OrgID, q.Get("q"))
	default:
		cs = s.store.Customers(p.OrgID)
	}
	items, next := pageOf(cs, customerKey, pg)
	writeJSON(w, http.StatusOK, list{Items: items, NextCursor: next})
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Name                 string            `json:"name"`
		Email                string            `json:"email"`
		DefaultPaymentMethod string            `json:"default_payment_method"`
		Metadata             map[string]string `json:"metadata"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.CreateCustomer(p.OrgID, in.Name, in.Email, s.clock.Now())
	if err != nil {
		s.storeError(w, err)
		return
	}
	if in.DefaultPaymentMethod != "" || len(in.Metadata) > 0 {
		c, err = s.store.UpdateCustomer(p.OrgID, c.ID, func(c *store.Customer) error {
			c.DefaultPaymentMethod, c.Metadata = in.DefaultPaymentMethod, in.Metadata
			return nil
		})
		if err != nil {
			_ = s.store.DeleteCustomer(p.OrgID, c.ID)
			s.storeError(w, err)
			return
		}
	}
	s.publish(r.Context(), p.OrgID, "customer.created", c)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	c, ok := s.store.Customer(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such customer")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) updateCustomer(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Name                 *string            `json:"name"`
		Email                *string            `json:"email"`
		DefaultPaymentMethod *string            `json:"default_payment_method"`
		Metadata             *map[string]string `json:"metadata"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.store.UpdateCustomer(p.OrgID, r.PathValue("id"), func(c *store.Customer) error {
		if in.Name != nil {
			c.Name = *in.Name
		}
		if in.Email != nil {
			c.Email = *in.Email
		}
		if in.DefaultPaymentMethod != nil {
			c.DefaultPaymentMethod = *in.DefaultPaymentMethod
		}
		if in.Metadata != nil {
			c.Metadata = *in.Metadata
		}
		return nil
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.publish(r.Context(), p.OrgID, "customer.updated", c)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCustomer(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	if err := s.store.DeleteCustomer(p.OrgID, id); err != nil {
		s.storeError(w, err)
		return
	}
	s.publish(r.Context(), p.OrgID, "customer.deleted", map[string]string{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

// exportCustomers answers the organization's customers as CSV, oldest first.
func (s *Server) exportCustomers(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="customers.csv"`)
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	_ = cw.Write([]string{"id", "name", "email", "created_at"})
	for _, c := range s.store.Customers(p.OrgID) {
		_ = cw.Write([]string{c.ID, c.Name, c.Email, c.CreatedAt.UTC().Format(time.RFC3339)})
	}
	cw.Flush()
}

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/billing"
	"example.com/tally/internal/money"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
)

// invoiceView is an invoice as the API shows it: with its totals, a one-line description and its customer.
type invoiceView struct {
	store.Invoice
	billing.Totals
	TotalFormatted string       `json:"total_formatted"`
	Description    string       `json:"description"`
	Customer       *customerRef `json:"customer"`
}

// customerRef is the customer shown on an invoice; null when the customer was deleted.
type customerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func newInvoiceView(inv store.Invoice, c *store.Customer) invoiceView {
	t := billing.Compute(inv)
	v := invoiceView{Invoice: inv, Totals: t, TotalFormatted: money.Format(t.Total, inv.Currency), Description: describe(inv.Lines)}
	if c != nil {
		v.Customer = &customerRef{ID: c.ID, Name: c.Name, Email: c.Email}
	}
	return v
}

// describe is the invoice's first line, and how many more there are.
func describe(lines []store.Line) string {
	d := lines[0].Description
	if len(lines) > 1 {
		d += fmt.Sprintf(" and %d more", len(lines)-1)
	}
	return d
}

func (s *Server) viewInvoice(inv store.Invoice) invoiceView {
	if c, ok := s.store.Customer(inv.OrgID, inv.CustomerID); ok {
		return newInvoiceView(inv, &c)
	}
	return newInvoiceView(inv, nil)
}

func findCustomer(cs []store.Customer, id string) *store.Customer {
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	return nil
}

func invoiceViewKey(v invoiceView) cursor { return cursor{T: v.CreatedAt, ID: v.ID} }

// listInvoices lists the organization's invoices, oldest first, optionally only those of ?customer_id=, ?subscription_id=
// or with ?status=.
func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	pg, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	status := store.InvoiceStatus(q.Get("status"))
	if status != "" && status != store.InvoiceOpen && status != store.InvoicePaid && status != store.InvoiceVoid {
		writeError(w, http.StatusBadRequest, "invalid_status", "status is open, paid or void")
		return
	}
	invs := s.store.ListInvoices(p.OrgID, store.InvoiceFilter{CustomerID: q.Get("customer_id"), SubscriptionID: q.Get("subscription_id"), Status: status})
	customers := s.store.Customers(p.OrgID)
	views := make([]invoiceView, 0, len(invs))
	for _, inv := range invs {
		views = append(views, newInvoiceView(inv, findCustomer(customers, inv.CustomerID)))
	}
	items, next := pageOf(views, invoiceViewKey, pg)
	writeJSON(w, http.StatusOK, list{Items: items, NextCursor: next})
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		CustomerID      string         `json:"customer_id"`
		Currency        money.Currency `json:"currency"`
		Lines           []store.Line   `json:"lines"`
		DiscountPercent int            `json:"discount_percent"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Lines) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "an invoice needs at least one line")
		return
	}
	for _, l := range in.Lines {
		if strings.TrimSpace(l.Description) == "" || l.Quantity <= 0 || l.UnitAmount < 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid", "each line needs a description, a positive quantity and a unit amount that is not negative")
			return
		}
	}
	inv, err := s.store.CreateInvoice(store.Invoice{OrgID: p.OrgID, CustomerID: in.CustomerID, Currency: in.Currency, Lines: in.Lines, DiscountPercent: in.DiscountPercent}, s.clock.Now())
	if err != nil {
		if errors.Is(err, store.ErrCustomerOrgMismatch) {
			writeError(w, http.StatusUnprocessableEntity, "customer_org_mismatch", fmt.Sprintf("customer %s belongs to another organization", in.CustomerID))
			return
		}
		s.storeError(w, err)
		return
	}
	s.publish(r.Context(), p.OrgID, "invoice.created", map[string]any{"invoice_id": inv.ID})
	writeJSON(w, http.StatusCreated, s.viewInvoice(inv))
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	inv, ok := s.store.Invoice(p.OrgID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such invoice")
		return
	}
	writeJSON(w, http.StatusOK, s.viewInvoice(inv))
}

// getInvoiceLines answers an invoice's lines with each line's amount, for the dashboard's invoice table.
func (s *Server) getInvoiceLines(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	inv, ok := s.store.InvoiceByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such invoice")
		return
	}
	type lineView struct {
		store.Line
		Amount int64 `json:"amount"`
	}
	items := []lineView{}
	for _, l := range inv.Lines {
		items = append(items, lineView{Line: l, Amount: l.Amount()})
	}
	writeJSON(w, http.StatusOK, list{Items: items})
}

// payInvoice charges an invoice: {"payment_method": "pm_..."}, or the customer's default payment method when it is absent.
func (s *Server) payInvoice(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		PaymentMethod string `json:"payment_method"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	inv, pay, err := s.pay.Pay(r.Context(), p.OrgID, r.PathValue("id"), in.PaymentMethod)
	if err != nil {
		s.payError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoice": s.viewInvoice(inv), "payment": pay})
}

func (s *Server) payError(w http.ResponseWriter, err error) {
	var decline *payments.DeclineError
	switch {
	case errors.As(err, &decline):
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": "payment_declined", "decline_code": decline.Code, "message": err.Error()})
	case errors.Is(err, payments.ErrAlreadyPaid):
		writeError(w, http.StatusConflict, "already_paid", err.Error())
	case errors.Is(err, payments.ErrInvoiceVoid):
		writeError(w, http.StatusConflict, "invoice_void", err.Error())
	case errors.Is(err, payments.ErrNoPaymentMethod):
		writeError(w, http.StatusUnprocessableEntity, "no_payment_method", err.Error())
	case errors.Is(err, payments.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "gateway_unavailable", "the payment gateway is unavailable; nothing was charged, try again")
	default:
		s.storeError(w, err)
	}
}

func (s *Server) voidInvoice(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	now := s.clock.Now()
	inv, err := s.store.UpdateInvoice(p.OrgID, r.PathValue("id"), func(inv *store.Invoice) error {
		switch inv.Status {
		case store.InvoicePaid:
			return payments.ErrAlreadyPaid
		case store.InvoiceVoid:
			return payments.ErrInvoiceVoid
		}
		inv.Status, inv.VoidedAt = store.InvoiceVoid, &now
		return nil
	})
	if err != nil {
		s.payError(w, err)
		return
	}
	s.publish(r.Context(), p.OrgID, "invoice.voided", map[string]any{"invoice_id": inv.ID})
	writeJSON(w, http.StatusOK, s.viewInvoice(inv))
}

func (s *Server) listPayments(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	writeJSON(w, http.StatusOK, list{Items: s.store.Payments(p.OrgID, r.URL.Query().Get("invoice_id"))})
}

package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ---- plans

// CreatePlan adds a plan. UsageType defaults to Licensed; a new plan is active.
func (s *Store) CreatePlan(p Plan, now time.Time) (Plan, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.UsageType == "" {
		p.UsageType = Licensed
	}
	p.Active = true
	if err := checkPlan(p); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[p.OrgID]; !ok {
		return Plan{}, ErrNotFound
	}
	p.ID = s.newID("plan")
	p.CreatedAt = now.UTC()
	s.plans[p.ID] = p
	return p, nil
}

func checkPlan(p Plan) error {
	switch {
	case p.Name == "":
		return invalid("a plan needs a name")
	case !p.Currency.Valid():
		return invalid("unknown currency %q", p.Currency)
	case p.UnitAmount < 0:
		return invalid("a plan's price cannot be negative")
	case p.Interval != Monthly && p.Interval != Yearly:
		return invalid("a plan bills every month or every year")
	case p.UsageType != Licensed && p.UsageType != Metered:
		return invalid("a plan is licensed or metered")
	case p.TrialDays < 0 || p.TrialDays > 365:
		return invalid("a trial lasts 0 to 365 days")
	}
	return nil
}

// Plan returns one of an organization's plans.
func (s *Store) Plan(orgID, id string) (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[id]
	if !ok || p.OrgID != orgID {
		return Plan{}, false
	}
	return p, true
}

// Plans returns an organization's plans, oldest first.
func (s *Store) Plans(orgID string) []Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Plan{}
	for _, p := range s.plans {
		if p.OrgID == orgID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpdatePlan changes a plan with f. Its currency, price, interval and usage type cannot change once created: subscriptions
// depend on them.
func (s *Store) UpdatePlan(orgID, id string, f func(*Plan) error) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.plans[id]
	if !ok || old.OrgID != orgID {
		return Plan{}, ErrNotFound
	}
	p := old
	if err := f(&p); err != nil {
		return Plan{}, err
	}
	p.ID, p.OrgID, p.CreatedAt = old.ID, old.OrgID, old.CreatedAt
	p.Currency, p.UnitAmount, p.Interval, p.UsageType = old.Currency, old.UnitAmount, old.Interval, old.UsageType
	p.Name = strings.TrimSpace(p.Name)
	if err := checkPlan(p); err != nil {
		return Plan{}, err
	}
	s.plans[id] = p
	return p, nil
}

// ---- subscriptions

// CreateSubscription stores a new subscription. The caller sets its status and periods.
func (s *Store) CreateSubscription(sub Subscription, now time.Time) (Subscription, error) {
	if sub.Quantity <= 0 {
		return Subscription{}, invalid("the quantity must be at least 1")
	}
	if sub.DiscountPercent < 0 || sub.DiscountPercent > 100 {
		return Subscription{}, invalid("the discount is 0 to 100 percent")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.customers[sub.CustomerID]; !ok {
		return Subscription{}, ErrNotFound
	}
	if p, ok := s.plans[sub.PlanID]; !ok || p.OrgID != sub.OrgID {
		return Subscription{}, ErrNotFound
	}
	sub.ID = s.newID("sub")
	sub.CreatedAt = now.UTC()
	s.subs[sub.ID] = sub
	return copySub(sub), nil
}

// Subscription returns one of an organization's subscriptions.
func (s *Store) Subscription(orgID, id string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[id]
	if !ok || sub.OrgID != orgID {
		return Subscription{}, false
	}
	return copySub(sub), true
}

// SubscriptionFilter narrows Subscriptions.
type SubscriptionFilter struct {
	CustomerID string
	Status     SubStatus
}

// Subscriptions returns an organization's subscriptions that match f, oldest first.
func (s *Store) Subscriptions(orgID string, f SubscriptionFilter) []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Subscription{}
	for _, sub := range s.subs {
		if sub.OrgID != orgID || (f.CustomerID != "" && sub.CustomerID != f.CustomerID) || (f.Status != "" && sub.Status != f.Status) {
			continue
		}
		out = append(out, copySub(sub))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpdateSubscription changes a subscription with f, which may refuse with an error.
func (s *Store) UpdateSubscription(orgID, id string, f func(*Subscription) error) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.subs[id]
	if !ok || old.OrgID != orgID {
		return Subscription{}, ErrNotFound
	}
	sub := copySub(old)
	if err := f(&sub); err != nil {
		return Subscription{}, err
	}
	sub.ID, sub.OrgID, sub.CustomerID, sub.CreatedAt = old.ID, old.OrgID, old.CustomerID, old.CreatedAt
	if sub.Quantity <= 0 {
		return Subscription{}, invalid("the quantity must be at least 1")
	}
	if p, ok := s.plans[sub.PlanID]; !ok || p.OrgID != orgID {
		return Subscription{}, ErrNotFound
	}
	s.subs[id] = sub
	return copySub(sub), nil
}

// DueSubscriptions returns the subscriptions of every organization whose current period has ended by now and that are not
// canceled, the longest overdue first.
func (s *Store) DueSubscriptions(now time.Time) []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Subscription{}
	for _, sub := range s.subs {
		if sub.Status != Canceled && !sub.CurrentPeriodEnd.After(now) {
			out = append(out, copySub(sub))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CurrentPeriodEnd.Equal(out[j].CurrentPeriodEnd) {
			return out[i].CurrentPeriodEnd.Before(out[j].CurrentPeriodEnd)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func copySub(sub Subscription) Subscription {
	if sub.TrialEnd != nil {
		t := *sub.TrialEnd
		sub.TrialEnd = &t
	}
	if sub.CanceledAt != nil {
		t := *sub.CanceledAt
		sub.CanceledAt = &t
	}
	return sub
}

// AddUsage records usage of a subscription to a metered plan at time at.
func (s *Store) AddUsage(orgID, subID string, quantity int64, at, now time.Time) (UsageRecord, error) {
	if quantity <= 0 {
		return UsageRecord{}, invalid("usage must be a positive quantity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[subID]
	if !ok || sub.OrgID != orgID {
		return UsageRecord{}, ErrNotFound
	}
	if p := s.plans[sub.PlanID]; p.UsageType != Metered {
		return UsageRecord{}, invalid("the subscription's plan is not metered")
	}
	u := UsageRecord{ID: s.newID("use"), OrgID: orgID, SubscriptionID: subID, Quantity: quantity, At: at.UTC(), CreatedAt: now.UTC()}
	s.usage = append(s.usage, u)
	return u, nil
}

// UsageTotal adds up a subscription's usage at times in [from, to).
func (s *Store) UsageTotal(orgID, subID string, from, to time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, u := range s.usage {
		if u.OrgID == orgID && u.SubscriptionID == subID && !u.At.Before(from) && u.At.Before(to) {
			n += u.Quantity
		}
	}
	return n
}

// ---- invoices

// CreateInvoice stores an invoice for one of the organization's customers, with the organization's tax rate. It is open.
func (s *Store) CreateInvoice(inv Invoice, now time.Time) (Invoice, error) {
	if !inv.Currency.Valid() {
		return Invoice{}, invalid("unknown currency %q", inv.Currency)
	}
	if inv.DiscountPercent < 0 || inv.DiscountPercent > 100 {
		return Invoice{}, invalid("the discount is 0 to 100 percent")
	}
	for _, l := range inv.Lines {
		if l.Quantity <= 0 {
			return Invoice{}, invalid("a line needs a positive quantity")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	org, ok := s.orgs[inv.OrgID]
	if !ok {
		return Invoice{}, ErrNotFound
	}
	c, ok := s.customers[inv.CustomerID]
	if !ok {
		return Invoice{}, ErrNotFound
	}
	if c.OrgID != inv.OrgID {
		return Invoice{}, ErrCustomerOrgMismatch
	}
	inv.ID = s.newID("in")
	inv.TaxRateBP = org.TaxRateBP
	inv.Status = InvoiceOpen
	inv.CreatedAt = now.UTC()
	inv.PaidAt, inv.VoidedAt = nil, nil
	inv = copyInvoice(inv)
	s.invoices[inv.ID] = inv
	return copyInvoice(inv), nil
}

// Invoice returns one of an organization's invoices.
func (s *Store) Invoice(orgID, id string) (Invoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[id]
	if !ok || inv.OrgID != orgID {
		return Invoice{}, false
	}
	return copyInvoice(inv), true
}

// InvoiceByID returns an invoice of any organization (background jobs and the admin tool).
func (s *Store) InvoiceByID(id string) (Invoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[id]
	return copyInvoice(inv), ok
}

// InvoiceFilter narrows ListInvoices.
type InvoiceFilter struct {
	CustomerID     string
	SubscriptionID string
	Status         InvoiceStatus
}

// ListInvoices returns an organization's invoices that match f, oldest first (ties by ID).
func (s *Store) ListInvoices(orgID string, f InvoiceFilter) []Invoice {
	s.mu.Lock()
	all := make([]Invoice, 0, len(s.invoices))
	for _, inv := range s.invoices {
		all = append(all, inv)
	}
	s.mu.Unlock()
	out := []Invoice{}
	for _, inv := range all {
		c := cloneInvoice(inv)
		if c.OrgID != orgID {
			continue
		}
		if (f.CustomerID != "" && c.CustomerID != f.CustomerID) || (f.SubscriptionID != "" && c.SubscriptionID != f.SubscriptionID) || (f.Status != "" && c.Status != f.Status) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// UpdateInvoice changes an invoice with f, which may refuse with an error. Only its status, timestamps and lines change.
func (s *Store) UpdateInvoice(orgID, id string, f func(*Invoice) error) (Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.invoices[id]
	if !ok || old.OrgID != orgID {
		return Invoice{}, ErrNotFound
	}
	inv := copyInvoice(old)
	if err := f(&inv); err != nil {
		return Invoice{}, err
	}
	inv.ID, inv.OrgID, inv.CustomerID, inv.SubscriptionID, inv.Currency, inv.CreatedAt = old.ID, old.OrgID, old.CustomerID, old.SubscriptionID, old.Currency, old.CreatedAt
	s.invoices[id] = copyInvoice(inv)
	return copyInvoice(inv), nil
}

// copyInvoice is a deep copy, so callers cannot change stored lines.
func copyInvoice(inv Invoice) Invoice {
	inv.Lines = append([]Line(nil), inv.Lines...)
	for _, p := range []**time.Time{&inv.PeriodStart, &inv.PeriodEnd, &inv.PaidAt, &inv.VoidedAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return inv
}

// cloneInvoice is a deep copy through JSON, which cannot miss a field added later.
func cloneInvoice(inv Invoice) Invoice {
	b, err := json.Marshal(inv)
	if err != nil {
		return copyInvoice(inv)
	}
	var out Invoice
	if err := json.Unmarshal(b, &out); err != nil {
		return copyInvoice(inv)
	}
	return out
}

// ---- payments

// CreatePayment records a payment attempt for one of the organization's invoices.
func (s *Store) CreatePayment(p Payment, now time.Time) (Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[p.InvoiceID]
	if !ok || inv.OrgID != p.OrgID {
		return Payment{}, ErrNotFound
	}
	if p.Status != PaymentSucceeded && p.Status != PaymentFailed {
		return Payment{}, invalid("a payment succeeded or failed")
	}
	p.ID = s.newID("pay")
	p.CreatedAt = now.UTC()
	s.payments[p.ID] = p
	return p, nil
}

// Payments returns an organization's payments, oldest first, only those of one invoice when invoiceID is set.
func (s *Store) Payments(orgID, invoiceID string) []Payment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Payment{}
	for _, p := range s.payments {
		if p.OrgID == orgID && (invoiceID == "" || p.InvoiceID == invoiceID) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

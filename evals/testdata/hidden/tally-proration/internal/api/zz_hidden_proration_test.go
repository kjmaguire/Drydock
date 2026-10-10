package api_test

// Hidden acceptance test of the trial task tally-proration (evals/tasks). The agents never see it.
//
// Everything goes through the HTTP API, with e.Clock to choose when a change is made and e.Subs.RenewDue for renewals.
// Expected line amounts are worked out from the prompt's formula with exact rational arithmetic:
// unit_amount = -round(old value * remaining / length) and round(new value * remaining / length), in seconds, rounding half
// away from zero.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // apitest's organizations use IANA time zones; do not depend on the machine's zoneinfo

	"example.com/tally/internal/apitest"
)

type hiddenProSub struct {
	ID                 string    `json:"id"`
	PlanID             string    `json:"plan_id"`
	Quantity           int       `json:"quantity"`
	Status             string    `json:"status"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	PendingPlanID      *string   `json:"pending_plan_id"`
	PendingQuantity    *int      `json:"pending_quantity"`
}

func (s hiddenProSub) String() string {
	pp, pq := "null", "null"
	if s.PendingPlanID != nil {
		pp = *s.PendingPlanID
	}
	if s.PendingQuantity != nil {
		pq = fmt.Sprint(*s.PendingQuantity)
	}
	return fmt.Sprintf("{plan_id %s, quantity %d, status %s, period %s to %s, pending_plan_id %s, pending_quantity %s}", s.PlanID, s.Quantity,
		s.Status, s.CurrentPeriodStart.UTC().Format(time.RFC3339), s.CurrentPeriodEnd.UTC().Format(time.RFC3339), pp, pq)
}

type hiddenProLine struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitAmount  int64  `json:"unit_amount"`
}

type hiddenProInvoice struct {
	ID              string          `json:"id"`
	SubscriptionID  string          `json:"subscription_id"`
	Currency        string          `json:"currency"`
	Status          string          `json:"status"`
	DiscountPercent int             `json:"discount_percent"`
	TaxRateBP       int             `json:"tax_rate_bp"`
	Subtotal        int64           `json:"subtotal"`
	Discount        int64           `json:"discount"`
	Tax             int64           `json:"tax"`
	Total           int64           `json:"total"`
	Lines           []hiddenProLine `json:"lines"`
}

func (inv hiddenProInvoice) String() string {
	return fmt.Sprintf("{id %s, subscription_id %s, currency %s, status %s, discount_percent %d, tax_rate_bp %d, lines %+v, subtotal %d, discount %d, tax %d, total %d}",
		inv.ID, inv.SubscriptionID, inv.Currency, inv.Status, inv.DiscountPercent, inv.TaxRateBP, inv.Lines, inv.Subtotal, inv.Discount, inv.Tax, inv.Total)
}

func hiddenProAt(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

// hiddenProPlan creates a plan of Acme through the API and returns its ID.
func hiddenProPlan(t *testing.T, e *apitest.Env, body map[string]any) string {
	t.Helper()
	if body["interval"] == nil {
		body["interval"] = "month"
	}
	if body["currency"] == nil {
		body["currency"] = "USD"
	}
	var p struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, body, &p)
	return p.ID
}

// hiddenProCustomer creates a customer of Acme, with a default payment method when pm is set.
func hiddenProCustomer(t *testing.T, e *apitest.Env, email, pm string) string {
	t.Helper()
	body := map[string]any{"name": "Proration " + email, "email": email}
	if pm != "" {
		body["default_payment_method"] = pm
	}
	var c struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/customers", e.Acme.Key, body, &c)
	return c.ID
}

func hiddenProSubscribe(t *testing.T, e *apitest.Env, customerID, planID string, quantity, discount int) hiddenProSub {
	t.Helper()
	var sub hiddenProSub
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key,
		map[string]any{"customer_id": customerID, "plan_id": planID, "quantity": quantity, "discount_percent": discount}, &sub)
	return sub
}

func hiddenProGetSub(t *testing.T, e *apitest.Env, id string) hiddenProSub {
	t.Helper()
	var sub hiddenProSub
	e.MustDo(http.StatusOK, "GET", "/v1/subscriptions/"+id, e.Acme.Key, nil, &sub)
	return sub
}

// hiddenProPatch changes a subscription and expects 200 with the subscription.
func hiddenProPatch(t *testing.T, e *apitest.Env, id string, body map[string]any) hiddenProSub {
	t.Helper()
	var sub hiddenProSub
	e.MustDo(http.StatusOK, "PATCH", "/v1/subscriptions/"+id, e.Acme.Key, body, &sub)
	return sub
}

func hiddenProInvoices(t *testing.T, e *apitest.Env, subID string) []hiddenProInvoice {
	t.Helper()
	var page struct {
		Items []hiddenProInvoice `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices?limit=100&subscription_id="+subID, e.Acme.Key, nil, &page)
	return page.Items
}

// hiddenProNewInvoices returns the subscription's invoices that are not in before.
func hiddenProNewInvoices(t *testing.T, e *apitest.Env, subID string, before []hiddenProInvoice) []hiddenProInvoice {
	t.Helper()
	seen := map[string]bool{}
	for _, inv := range before {
		seen[inv.ID] = true
	}
	var out []hiddenProInvoice
	for _, inv := range hiddenProInvoices(t, e, subID) {
		if !seen[inv.ID] {
			out = append(out, inv)
		}
	}
	return out
}

// hiddenProOneNewInvoice expects exactly one new invoice of the subscription since before.
func hiddenProOneNewInvoice(t *testing.T, e *apitest.Env, when, subID string, before []hiddenProInvoice) hiddenProInvoice {
	t.Helper()
	invs := hiddenProNewInvoices(t, e, subID, before)
	if len(invs) != 1 {
		t.Fatalf("%s: %d new invoices for the subscription, want exactly 1: %v", when, len(invs), invs)
	}
	return invs[0]
}

func hiddenProNoNewInvoice(t *testing.T, e *apitest.Env, when, subID string, before []hiddenProInvoice) {
	t.Helper()
	if invs := hiddenProNewInvoices(t, e, subID, before); len(invs) != 0 {
		t.Fatalf("%s: want no new invoice, got %v", when, invs)
	}
}

// hiddenProCheckLines checks an invoice's lines, in order, exactly.
func hiddenProCheckLines(t *testing.T, when string, inv hiddenProInvoice, want ...hiddenProLine) {
	t.Helper()
	ok := len(inv.Lines) == len(want)
	for i := 0; ok && i < len(want); i++ {
		ok = inv.Lines[i] == want[i]
	}
	if !ok {
		t.Fatalf("%s: invoice lines %+v, want exactly %+v\ninvoice: %v", when, inv.Lines, want, inv)
	}
}

func hiddenProCheckTotals(t *testing.T, when string, inv hiddenProInvoice, subtotal, discount, tax, total int64) {
	t.Helper()
	if inv.Subtotal != subtotal || inv.Discount != discount || inv.Tax != tax || inv.Total != total {
		t.Fatalf("%s: subtotal %d, discount %d, tax %d, total %d; want %d, %d, %d, %d\ninvoice: %v",
			when, inv.Subtotal, inv.Discount, inv.Tax, inv.Total, subtotal, discount, tax, total, inv)
	}
}

// hiddenProCheckSub checks the subscription's plan, quantity and status, and that nothing is pending.
func hiddenProCheckSub(t *testing.T, when string, sub hiddenProSub, planID string, quantity int, status string) {
	t.Helper()
	if sub.PlanID != planID || sub.Quantity != quantity || sub.Status != status || sub.PendingPlanID != nil || sub.PendingQuantity != nil {
		t.Fatalf("%s: subscription %v, want plan_id %s, quantity %d, status %s and pending_plan_id and pending_quantity null",
			when, sub, planID, quantity, status)
	}
}

// hiddenProCheckPending checks a pending downgrade: the subscription keeps its plan and quantity, and shows the plan and
// quantity that take effect at the next renewal. A pending field whose value would not change may also be null.
func hiddenProCheckPending(t *testing.T, when string, sub hiddenProSub, curPlan string, curQty int, wantPlan string, wantQty int) {
	t.Helper()
	planOK := (sub.PendingPlanID != nil && *sub.PendingPlanID == wantPlan) || (sub.PendingPlanID == nil && wantPlan == curPlan)
	qtyOK := (sub.PendingQuantity != nil && *sub.PendingQuantity == wantQty) || (sub.PendingQuantity == nil && wantQty == curQty)
	if sub.PlanID != curPlan || sub.Quantity != curQty || !planOK || !qtyOK || (sub.PendingPlanID == nil && sub.PendingQuantity == nil) {
		t.Fatalf("%s: subscription %v, want it to keep plan_id %s and quantity %d with pending_plan_id %s and pending_quantity %d",
			when, sub, curPlan, curQty, wantPlan, wantQty)
	}
}

func hiddenProCheckPeriod(t *testing.T, when string, sub, orig hiddenProSub) {
	t.Helper()
	if !sub.CurrentPeriodStart.Equal(orig.CurrentPeriodStart) || !sub.CurrentPeriodEnd.Equal(orig.CurrentPeriodEnd) {
		t.Fatalf("%s: the current period changed to %s to %s, want it to stay %s to %s", when,
			sub.CurrentPeriodStart.UTC().Format(time.RFC3339), sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
			orig.CurrentPeriodStart.UTC().Format(time.RFC3339), orig.CurrentPeriodEnd.UTC().Format(time.RFC3339))
	}
}

func hiddenProRenewAt(t *testing.T, e *apitest.Env, at time.Time) {
	t.Helper()
	e.Clock.Set(at)
	if _, err := e.Subs.RenewDue(context.Background()); err != nil {
		t.Fatalf("RenewDue at %s: %v", at.Format(time.RFC3339), err)
	}
}

func TestHiddenProrationUpgradeIsBilledAtOnce(t *testing.T) {
	t.Run("more seats, with discount and tax, charged to the card", func(t *testing.T) {
		e := apitest.New(t) // the clock starts at 2026-01-15 10:00 UTC
		e.MustDo(http.StatusOK, "PATCH", "/v1/org", e.Acme.Key, map[string]any{"tax_rate_bp": 825}, nil)
		cust := hiddenProCustomer(t, e, "card@customers.example", "pm_card_visa")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		orig := hiddenProSubscribe(t, e, cust, basic, 2, 10) // period 2026-01-15 to 2026-02-15, 31 days
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 1, 25, 10, 0, 0)) // 21 of 31 days remain
		when := "PATCH quantity 2 -> 3 on 2026-01-25"
		got := hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 3})
		hiddenProCheckSub(t, when+" (answer)", got, basic, 3, "active")
		hiddenProCheckPeriod(t, when, got, orig)
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// old value 4000, new value 6000: -round(4000*21/31) = -2710, round(6000*21/31) = 4065
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Basic", 1, -2710}, hiddenProLine{"Remaining time on Basic", 1, 4065})
		if inv.SubscriptionID != orig.ID || inv.Currency != "USD" || inv.DiscountPercent != 10 || inv.TaxRateBP != 825 {
			t.Fatalf("%s: invoice %v, want subscription_id %s, currency USD, discount_percent 10 and tax_rate_bp 825", when, inv, orig.ID)
		}
		// subtotal 1355; discount 10% = 135.5 -> 136; tax 8.25% of 1219 = 100.57 -> 101
		hiddenProCheckTotals(t, when, inv, 1355, 136, 101, 1320)
		if inv.Status != "paid" {
			t.Fatalf("%s: the invoice is %q, want paid (charged to the customer's card)", when, inv.Status)
		}
		if cs := e.Gateway.Charges(); len(cs) != 2 || cs[1].Amount != 1320 || cs[1].PaymentMethod != "pm_card_visa" {
			t.Fatalf("%s: charges %+v, want the first invoice's and then 1320 on pm_card_visa", when, cs)
		}
		hiddenProCheckSub(t, when+" (GET)", hiddenProGetSub(t, e, orig.ID), basic, 3, "active")
	})
	t.Run("plan and seats, without a payment method", func(t *testing.T) {
		e := apitest.New(t)
		cust := hiddenProCustomer(t, e, "nocard@customers.example", "")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
		orig := hiddenProSubscribe(t, e, cust, basic, 1, 0)
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 1, 19, 17, 43, 21)) // 2,304,999 of 2,678,400 seconds remain
		when := "PATCH Basic x1 -> Pro x2 on 2026-01-19 17:43:21"
		got := hiddenProPatch(t, e, orig.ID, map[string]any{"plan_id": pro, "quantity": 2})
		hiddenProCheckSub(t, when+" (answer)", got, pro, 2, "active")
		hiddenProCheckPeriod(t, when, got, orig)
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// -round(2000*2304999/2678400) = -1721, round(10000*2304999/2678400) = 8606
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Basic", 1, -1721}, hiddenProLine{"Remaining time on Pro", 1, 8606})
		hiddenProCheckTotals(t, when, inv, 6885, 0, 0, 6885)
		if inv.Status != "open" {
			t.Fatalf("%s: the invoice is %q, want open (the customer has no payment method)", when, inv.Status)
		}
		if calls := e.Gateway.Calls(); len(calls) != 0 {
			t.Fatalf("%s: the gateway was asked to charge %+v, but the customer has no payment method", when, calls)
		}
		hiddenProCheckSub(t, when+" (GET)", hiddenProGetSub(t, e, orig.ID), pro, 2, "active")
	})
}

func TestHiddenProrationRounding(t *testing.T) {
	t.Run("half of a 28-day period", func(t *testing.T) {
		e := apitest.New(t)
		e.Clock.Set(hiddenProAt(2026, 2, 10, 10, 0, 0))
		cust := hiddenProCustomer(t, e, "half@customers.example", "")
		starter := hiddenProPlan(t, e, map[string]any{"name": "Starter", "price": "9.99"})
		pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "24.99"})
		orig := hiddenProSubscribe(t, e, cust, starter, 1, 0) // 2026-02-10 to 2026-03-10
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 2, 24, 10, 0, 0))
		when := "PATCH Starter -> Pro half way through the period"
		hiddenProPatch(t, e, orig.ID, map[string]any{"plan_id": pro})
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// 999/2 = 499.5 rounds to 500 (so the credit is -500), 2499/2 = 1249.5 rounds to 1250
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Starter", 1, -500}, hiddenProLine{"Remaining time on Pro", 1, 1250})
		hiddenProCheckTotals(t, when, inv, 750, 0, 0, 750)
	})
	t.Run("a sixth of a 30-day period", func(t *testing.T) {
		e := apitest.New(t)
		e.Clock.Set(hiddenProAt(2026, 4, 15, 10, 0, 0))
		cust := hiddenProCustomer(t, e, "sixth@customers.example", "")
		starter := hiddenProPlan(t, e, map[string]any{"name": "Starter", "price": "9.99"})
		orig := hiddenProSubscribe(t, e, cust, starter, 1, 0) // 2026-04-15 to 2026-05-15
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 5, 10, 10, 0, 0))
		when := "PATCH quantity 1 -> 3 with a sixth of the period left"
		hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 3})
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// 999/6 = 166.5 rounds to 167 (credit -167), 2997/6 = 499.5 rounds to 500
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Starter", 1, -167}, hiddenProLine{"Remaining time on Starter", 1, 500})
		hiddenProCheckTotals(t, when, inv, 333, 0, 0, 333)
	})
}

func TestHiddenProrationDeclinedUpgrade(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "declined@customers.example", "")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
	orig := hiddenProSubscribe(t, e, cust, basic, 1, 0)
	if orig.Status != "active" {
		t.Fatalf("a new subscription without a payment method: status %q, want active", orig.Status)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+cust, e.Acme.Key, map[string]any{"default_payment_method": "pm_card_declined"}, nil)
	before := hiddenProInvoices(t, e, orig.ID)
	e.Clock.Set(hiddenProAt(2026, 1, 25, 10, 0, 0))
	when := "PATCH quantity 1 -> 2 with a card that is declined"
	r := e.Do("PATCH", "/v1/subscriptions/"+orig.ID, e.Acme.Key, map[string]any{"quantity": 2})
	switch r.Code {
	case http.StatusOK:
		var got hiddenProSub
		r.JSON(t, &got)
		hiddenProCheckSub(t, when+" (answer)", got, basic, 2, "past_due")
	case http.StatusPaymentRequired: // the answer may report the decline; the change applies all the same
	default:
		t.Fatalf("%s: want 200 with the subscription (or 402 reporting the decline), got %s", when, r)
	}
	got := hiddenProGetSub(t, e, orig.ID)
	hiddenProCheckSub(t, when+" (GET)", got, basic, 2, "past_due")
	hiddenProCheckPeriod(t, when, got, orig)
	inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
	hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Basic", 1, -1355}, hiddenProLine{"Remaining time on Basic", 1, 2710})
	if inv.Status != "open" {
		t.Fatalf("%s: the invoice is %q, want open (the charge was declined)", when, inv.Status)
	}
	charged := false
	for _, c := range e.Gateway.Calls() {
		charged = charged || (c.Amount == inv.Total && c.PaymentMethod == "pm_card_declined")
	}
	if !charged || len(e.Gateway.Charges()) != 0 {
		t.Fatalf("%s: gateway requests %+v, money taken %+v; want a charge of %d tried on pm_card_declined and nothing taken",
			when, e.Gateway.Calls(), e.Gateway.Charges(), inv.Total)
	}
}

func TestHiddenProrationDowngradeWaitsForRenewal(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "downgrade@customers.example", "pm_card_visa")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
	pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
	type change struct {
		name     string
		plan     string
		qty      int
		patch    map[string]any
		wantPlan string
		wantQty  int
		wantName string
	}
	changes := []*change{
		{name: "fewer seats (Pro x3 -> x2)", plan: pro, qty: 3, patch: map[string]any{"quantity": 2}, wantPlan: pro, wantQty: 2, wantName: "Pro"},
		{name: "a cheaper plan (Pro -> Basic)", plan: pro, qty: 1, patch: map[string]any{"plan_id": basic}, wantPlan: basic, wantQty: 1, wantName: "Basic"},
		{name: "a cheaper plan and more seats (Pro x2 -> Basic x3)", plan: pro, qty: 2, patch: map[string]any{"plan_id": basic, "quantity": 3}, wantPlan: basic, wantQty: 3, wantName: "Basic"},
		{name: "a dearer plan and fewer seats (Basic x5 -> Pro x1)", plan: basic, qty: 5, patch: map[string]any{"plan_id": pro, "quantity": 1}, wantPlan: pro, wantQty: 1, wantName: "Pro"},
	}
	unit := map[string]int64{basic: 2000, pro: 5000}
	subs := make([]hiddenProSub, len(changes))
	befores := make([][]hiddenProInvoice, len(changes))
	for i, c := range changes {
		subs[i] = hiddenProSubscribe(t, e, cust, c.plan, c.qty, 0)
	}
	e.Clock.Set(hiddenProAt(2026, 1, 20, 12, 0, 0))
	charges := len(e.Gateway.Charges())
	for i, c := range changes {
		befores[i] = hiddenProInvoices(t, e, subs[i].ID)
		when := "PATCH to " + c.name
		got := hiddenProPatch(t, e, subs[i].ID, c.patch)
		hiddenProCheckPending(t, when+" (answer)", got, c.plan, c.qty, c.wantPlan, c.wantQty)
		if got.Status != "active" {
			t.Fatalf("%s: status %q, want active", when, got.Status)
		}
		hiddenProCheckPeriod(t, when, got, subs[i])
		hiddenProCheckPending(t, when+" (GET)", hiddenProGetSub(t, e, subs[i].ID), c.plan, c.qty, c.wantPlan, c.wantQty)
		hiddenProNoNewInvoice(t, e, when, subs[i].ID, befores[i])
	}
	if n := len(e.Gateway.Charges()); n != charges {
		t.Fatalf("downgrades charged the card: %d charges, want still %d", n, charges)
	}
	hiddenProRenewAt(t, e, subs[0].CurrentPeriodEnd)
	for i, c := range changes {
		when := "the renewal after a downgrade to " + c.name
		hiddenProCheckSub(t, when, hiddenProGetSub(t, e, subs[i].ID), c.wantPlan, c.wantQty, "active")
		inv := hiddenProOneNewInvoice(t, e, when, subs[i].ID, befores[i])
		if len(inv.Lines) != 1 || inv.Lines[0].Quantity != int64(c.wantQty) || inv.Lines[0].UnitAmount != unit[c.wantPlan] ||
			!strings.HasPrefix(inv.Lines[0].Description, c.wantName) || inv.Total != int64(c.wantQty)*unit[c.wantPlan] {
			t.Fatalf("%s: renewal invoice %v, want one line for %s: quantity %d at %d", when, inv, c.wantName, c.wantQty, unit[c.wantPlan])
		}
	}
}

func TestHiddenProrationPendingChangeIsCleared(t *testing.T) {
	t.Run("by a later upgrade of the quantity", func(t *testing.T) {
		e := apitest.New(t)
		cust := hiddenProCustomer(t, e, "later@customers.example", "")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		orig := hiddenProSubscribe(t, e, cust, basic, 5, 0)
		e.Clock.Set(hiddenProAt(2026, 1, 20, 10, 0, 0))
		hiddenProCheckPending(t, "PATCH quantity 5 -> 3", hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 3}), basic, 5, basic, 3)
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 1, 30, 10, 0, 0)) // 16 of 31 days remain
		when := "PATCH quantity 6 while 3 is pending"
		got := hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 6})
		hiddenProCheckSub(t, when, got, basic, 6, "active")
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// the old value is the subscription's value, 5 x 2000: -round(10000*16/31) = -5161, round(12000*16/31) = 6194
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Basic", 1, -5161}, hiddenProLine{"Remaining time on Basic", 1, 6194})
		before = hiddenProInvoices(t, e, orig.ID)
		hiddenProRenewAt(t, e, orig.CurrentPeriodEnd)
		when = "the renewal after the pending change was cleared"
		hiddenProCheckSub(t, when, hiddenProGetSub(t, e, orig.ID), basic, 6, "active")
		if inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before); len(inv.Lines) != 1 || inv.Lines[0].Quantity != 6 || inv.Lines[0].UnitAmount != 2000 {
			t.Fatalf("%s: renewal invoice %v, want one line of 6 x 2000", when, inv)
		}
	})
	t.Run("by a later upgrade of the plan", func(t *testing.T) {
		e := apitest.New(t)
		cust := hiddenProCustomer(t, e, "laterplan@customers.example", "")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
		orig := hiddenProSubscribe(t, e, cust, basic, 5, 0)
		e.Clock.Set(hiddenProAt(2026, 1, 20, 10, 0, 0))
		hiddenProCheckPending(t, "PATCH quantity 5 -> 3", hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 3}), basic, 5, basic, 3)
		before := hiddenProInvoices(t, e, orig.ID)
		e.Clock.Set(hiddenProAt(2026, 1, 30, 10, 0, 0))
		when := "PATCH plan Pro while quantity 3 is pending"
		hiddenProCheckSub(t, when, hiddenProPatch(t, e, orig.ID, map[string]any{"plan_id": pro}), pro, 5, "active")
		inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before)
		// -round(10000*16/31) = -5161, round(25000*16/31) = 12903
		hiddenProCheckLines(t, when, inv, hiddenProLine{"Unused time on Basic", 1, -5161}, hiddenProLine{"Remaining time on Pro", 1, 12903})
		before = hiddenProInvoices(t, e, orig.ID)
		hiddenProRenewAt(t, e, orig.CurrentPeriodEnd)
		when = "the renewal after the pending change was cleared"
		hiddenProCheckSub(t, when, hiddenProGetSub(t, e, orig.ID), pro, 5, "active")
		if inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before); len(inv.Lines) != 1 || inv.Lines[0].Quantity != 5 || inv.Lines[0].UnitAmount != 5000 {
			t.Fatalf("%s: renewal invoice %v, want one line of 5 x 5000", when, inv)
		}
	})
	t.Run("by a PATCH back to the current quantity", func(t *testing.T) {
		e := apitest.New(t)
		cust := hiddenProCustomer(t, e, "back@customers.example", "")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		orig := hiddenProSubscribe(t, e, cust, basic, 5, 0)
		e.Clock.Set(hiddenProAt(2026, 1, 20, 10, 0, 0))
		hiddenProCheckPending(t, "PATCH quantity 5 -> 3", hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 3}), basic, 5, basic, 3)
		before := hiddenProInvoices(t, e, orig.ID)
		when := "PATCH quantity 5 (the current quantity) while 3 is pending"
		hiddenProCheckSub(t, when, hiddenProPatch(t, e, orig.ID, map[string]any{"quantity": 5}), basic, 5, "active")
		hiddenProCheckSub(t, when+" (GET)", hiddenProGetSub(t, e, orig.ID), basic, 5, "active")
		hiddenProNoNewInvoice(t, e, when, orig.ID, before)
		hiddenProRenewAt(t, e, orig.CurrentPeriodEnd)
		when = "the renewal after the pending change was cleared"
		hiddenProCheckSub(t, when, hiddenProGetSub(t, e, orig.ID), basic, 5, "active")
		if inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before); len(inv.Lines) != 1 || inv.Lines[0].Quantity != 5 || inv.Lines[0].UnitAmount != 2000 {
			t.Fatalf("%s: renewal invoice %v, want one line of 5 x 2000", when, inv)
		}
	})
	t.Run("by a PATCH back to the current plan and quantity", func(t *testing.T) {
		e := apitest.New(t)
		cust := hiddenProCustomer(t, e, "backplan@customers.example", "")
		basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
		pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
		orig := hiddenProSubscribe(t, e, cust, pro, 1, 0)
		e.Clock.Set(hiddenProAt(2026, 1, 20, 10, 0, 0))
		hiddenProCheckPending(t, "PATCH plan Pro -> Basic", hiddenProPatch(t, e, orig.ID, map[string]any{"plan_id": basic}), pro, 1, basic, 1)
		before := hiddenProInvoices(t, e, orig.ID)
		when := "PATCH plan Pro, quantity 1 (the current plan and quantity) while Basic is pending"
		hiddenProCheckSub(t, when, hiddenProPatch(t, e, orig.ID, map[string]any{"plan_id": pro, "quantity": 1}), pro, 1, "active")
		hiddenProNoNewInvoice(t, e, when, orig.ID, before)
		hiddenProRenewAt(t, e, orig.CurrentPeriodEnd)
		when = "the renewal after the pending change was cleared"
		hiddenProCheckSub(t, when, hiddenProGetSub(t, e, orig.ID), pro, 1, "active")
		if inv := hiddenProOneNewInvoice(t, e, when, orig.ID, before); len(inv.Lines) != 1 || inv.Lines[0].Quantity != 1 || inv.Lines[0].UnitAmount != 5000 {
			t.Fatalf("%s: renewal invoice %v, want one line of 1 x 5000", when, inv)
		}
	})
}

func TestHiddenProrationEqualValueAppliesWithoutBill(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "equal@customers.example", "pm_card_visa")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
	double := hiddenProPlan(t, e, map[string]any{"name": "Double", "price": "40.00"})
	twin := hiddenProPlan(t, e, map[string]any{"name": "Twin", "price": "20.00"})
	a := hiddenProSubscribe(t, e, cust, basic, 2, 0)
	b := hiddenProSubscribe(t, e, cust, basic, 1, 0)
	e.Clock.Set(hiddenProAt(2026, 1, 25, 10, 0, 0))
	charges := len(e.Gateway.Charges())
	for _, c := range []struct {
		name  string
		sub   hiddenProSub
		patch map[string]any
		plan  string
		qty   int
	}{
		{"Basic x2 -> Double x1 (4000 = 4000)", a, map[string]any{"plan_id": double, "quantity": 1}, double, 1},
		{"Basic -> Twin at the same price", b, map[string]any{"plan_id": twin}, twin, 1},
	} {
		before := hiddenProInvoices(t, e, c.sub.ID)
		when := "PATCH " + c.name
		got := hiddenProPatch(t, e, c.sub.ID, c.patch)
		hiddenProCheckSub(t, when+" (answer)", got, c.plan, c.qty, "active")
		hiddenProCheckPeriod(t, when, got, c.sub)
		hiddenProCheckSub(t, when+" (GET)", hiddenProGetSub(t, e, c.sub.ID), c.plan, c.qty, "active")
		hiddenProNoNewInvoice(t, e, when, c.sub.ID, before)
	}
	if n := len(e.Gateway.Charges()); n != charges {
		t.Fatalf("changes of equal value charged the card: %d charges, want still %d", n, charges)
	}
}

func TestHiddenProrationIncompatiblePlans(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "incompatible@customers.example", "")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
	euro := hiddenProPlan(t, e, map[string]any{"name": "Euro", "price": "50.00", "currency": "EUR"})
	yearly := hiddenProPlan(t, e, map[string]any{"name": "Yearly", "price": "10.00", "interval": "year"})
	metered := hiddenProPlan(t, e, map[string]any{"name": "Calls", "price": "0.10", "usage_type": "metered"})
	metered2 := hiddenProPlan(t, e, map[string]any{"name": "Calls 2", "price": "0.20", "usage_type": "metered"})
	licensedSub := hiddenProSubscribe(t, e, cust, basic, 1, 0)
	meteredSub := hiddenProSubscribe(t, e, cust, metered, 1, 0)
	e.Clock.Set(hiddenProAt(2026, 1, 25, 10, 0, 0))
	for _, c := range []struct {
		name  string
		sub   hiddenProSub
		plan  string
		patch map[string]any
	}{
		{"to a plan in another currency", licensedSub, basic, map[string]any{"plan_id": euro}},
		{"to a plan with another interval", licensedSub, basic, map[string]any{"plan_id": yearly}},
		{"to a plan with another interval, with more seats", licensedSub, basic, map[string]any{"plan_id": yearly, "quantity": 3}},
		{"to a metered plan", licensedSub, basic, map[string]any{"plan_id": metered}},
		{"from a metered plan", meteredSub, metered, map[string]any{"plan_id": basic}},
		{"from a metered plan to another metered plan", meteredSub, metered, map[string]any{"plan_id": metered2}},
	} {
		before := hiddenProInvoices(t, e, c.sub.ID)
		when := "PATCH " + c.name
		r := e.Do("PATCH", "/v1/subscriptions/"+c.sub.ID, e.Acme.Key, c.patch)
		if r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "incompatible_plan" {
			t.Fatalf("%s: want 422 {\"error\":\"incompatible_plan\"}, got %s", when, r)
		}
		hiddenProCheckSub(t, when+": the subscription must not change", hiddenProGetSub(t, e, c.sub.ID), c.plan, 1, "active")
		hiddenProNoNewInvoice(t, e, when, c.sub.ID, before)
	}
}

func TestHiddenProrationTrialingChangesAtOnce(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "trial@customers.example", "pm_card_visa")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00", "trial_days": 14})
	pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
	orig := hiddenProSubscribe(t, e, cust, basic, 2, 0)
	if orig.Status != "trialing" {
		t.Fatalf("a subscription to a plan with a trial: status %q, want trialing", orig.Status)
	}
	e.Clock.Set(hiddenProAt(2026, 1, 20, 10, 0, 0))
	for _, c := range []struct {
		name  string
		patch map[string]any
		plan  string
		qty   int
	}{
		{"more seats (2 -> 4)", map[string]any{"quantity": 4}, basic, 4},
		{"fewer seats (4 -> 1)", map[string]any{"quantity": 1}, basic, 1},
		{"a dearer plan (Basic -> Pro)", map[string]any{"plan_id": pro}, pro, 1},
		{"more seats on the dearer plan (1 -> 3)", map[string]any{"quantity": 3}, pro, 3},
		{"a cheaper plan (Pro -> Basic)", map[string]any{"plan_id": basic}, basic, 3},
	} {
		when := "PATCH a trialing subscription to " + c.name
		got := hiddenProPatch(t, e, orig.ID, c.patch)
		hiddenProCheckSub(t, when+" (answer)", got, c.plan, c.qty, "trialing")
		hiddenProCheckPeriod(t, when, got, orig)
		hiddenProCheckSub(t, when+" (GET)", hiddenProGetSub(t, e, orig.ID), c.plan, c.qty, "trialing")
		hiddenProNoNewInvoice(t, e, when, orig.ID, nil)
	}
	if calls := e.Gateway.Calls(); len(calls) != 0 {
		t.Fatalf("changes during the trial asked the gateway to charge %+v, want nothing billed", calls)
	}
}

func TestHiddenProrationCanceledCannotChange(t *testing.T) {
	e := apitest.New(t)
	cust := hiddenProCustomer(t, e, "canceled@customers.example", "")
	basic := hiddenProPlan(t, e, map[string]any{"name": "Basic", "price": "20.00"})
	pro := hiddenProPlan(t, e, map[string]any{"name": "Pro", "price": "50.00"})
	orig := hiddenProSubscribe(t, e, cust, basic, 2, 0)
	e.MustDo(http.StatusOK, "POST", "/v1/subscriptions/"+orig.ID+"/cancel", e.Acme.Key, nil, nil)
	before := hiddenProInvoices(t, e, orig.ID)
	e.Clock.Set(hiddenProAt(2026, 1, 25, 10, 0, 0))
	for _, patch := range []map[string]any{{"quantity": 5}, {"quantity": 1}, {"plan_id": pro}} {
		if r := e.Do("PATCH", "/v1/subscriptions/"+orig.ID, e.Acme.Key, patch); r.Code != http.StatusConflict {
			t.Fatalf("PATCH %v on a canceled subscription: want 409, got %s", patch, r)
		}
	}
	hiddenProCheckSub(t, "a canceled subscription after refused changes", hiddenProGetSub(t, e, orig.ID), basic, 2, "canceled")
	hiddenProNoNewInvoice(t, e, "PATCH of a canceled subscription", orig.ID, before)
}

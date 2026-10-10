package api_test

// Hidden acceptance test of the trial task tally-zero-line-invoices (evals/tasks). The agents never see it.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/apitest"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/money"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

type hiddenZeroLineLine struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitAmount  int64  `json:"unit_amount"`
}

type hiddenZeroLineInvoice struct {
	ID             string               `json:"id"`
	SubscriptionID string               `json:"subscription_id"`
	Status         string               `json:"status"`
	Description    string               `json:"description"`
	Total          int64                `json:"total"`
	Lines          []hiddenZeroLineLine `json:"lines"`
}

type hiddenZeroLinePage struct {
	Items      []hiddenZeroLineInvoice `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

// hiddenZeroLineDo sends a request and fails the test unless the answer has the status want; it decodes the body into out.
func hiddenZeroLineDo(t *testing.T, e *apitest.Env, want int, method, path, key string, body, out any) {
	t.Helper()
	r := e.Do(method, path, key, body)
	if r.Code != want {
		t.Fatalf("%s %s: want status %d, got %s", method, path, want, r)
	}
	if out != nil {
		r.JSON(t, out)
	}
}

// hiddenZeroLineSubInvoices lists a subscription's invoices through the API, oldest first.
func hiddenZeroLineSubInvoices(t *testing.T, e *apitest.Env, key, subID string) []hiddenZeroLineInvoice {
	t.Helper()
	var page hiddenZeroLinePage
	hiddenZeroLineDo(t, e, http.StatusOK, "GET", "/v1/invoices?limit=100&subscription_id="+subID, key, nil, &page)
	return page.Items
}

func hiddenZeroLinePeriod(start, end time.Time) string {
	return start.UTC().Format("Jan 2, 2006") + " to " + end.UTC().Format("Jan 2, 2006")
}

// hiddenZeroLineHeld is a service whose store holds invoices without lines.
type hiddenZeroLineHeld struct {
	do       func(method, path, key string, body any) *apitest.Response
	gateway  *payments.Fake
	lineless []string // Acme's invoices without lines
}

// hiddenZeroLineHold puts three invoices without lines of Acme's customer c (and one of Globex's customer g) in the store.
// The prompt says the store can hold such invoices: they are created with CreateInvoice, or, when a solution made
// CreateInvoice refuse them, loaded with store.FromState the way a snapshot written before the fix brings them back.
func hiddenZeroLineHold(t *testing.T, e *apitest.Env, c, g store.Customer) hiddenZeroLineHeld {
	t.Helper()
	at := e.Clock.Now()
	var ids []string
	for i := range 3 {
		inv, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Acme.ID, CustomerID: c.ID, Currency: money.USD}, at.Add(time.Duration(i+1)*time.Second))
		if err != nil {
			break
		}
		ids = append(ids, inv.ID)
	}
	if len(ids) == 3 {
		if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Globex.ID, CustomerID: g.ID, Currency: money.EUR}, at); err != nil {
			t.Fatalf("the store created Acme's invoices without lines but refused Globex's: %v", err)
		}
		return hiddenZeroLineHeld{do: func(method, path, key string, body any) *apitest.Response { return e.Do(method, path, key, body) },
			gateway: e.Gateway, lineless: ids}
	}
	if len(ids) != 0 {
		t.Fatalf("the store created %d of 3 invoices without lines", len(ids))
	}
	st := e.Store.Export()
	ids = []string{"in_900001", "in_900002", "in_900003"}
	for i, id := range ids {
		st.Invoices = append(st.Invoices, store.Invoice{ID: id, OrgID: e.Acme.ID, CustomerID: c.ID, Currency: money.USD, Status: store.InvoiceOpen,
			CreatedAt: at.Add(time.Duration(i+1) * time.Second)})
	}
	st.Invoices = append(st.Invoices, store.Invoice{ID: "in_900004", OrgID: e.Globex.ID, CustomerID: g.ID, Currency: money.EUR, Status: store.InvoiceOpen, CreatedAt: at})
	held, err := store.FromState(st)
	if err != nil {
		t.Fatalf("the store can hold no invoice without lines: CreateInvoice refuses one and FromState fails: %v", err)
	}
	hooks := webhooks.New(webhooks.Options{Store: held, Clock: e.Clock, Doer: &apitest.FakeHTTP{}, Resolver: &apitest.FakeDNS{}})
	gw := &payments.Fake{}
	pay := &payments.Service{Store: held, Gateway: gw, Clock: e.Clock, Publisher: hooks}
	subs := &subscriptions.Service{Store: held, Payments: pay, Clock: e.Clock, Publisher: hooks}
	srv := api.New(held, api.Options{Clock: e.Clock, Payments: pay, Subscriptions: subs, Webhooks: hooks, Mail: &mail.Outbox{}})
	do := func(method, path, key string, body any) *apitest.Response {
		var rd io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
	return hiddenZeroLineHeld{do: do, gateway: gw, lineless: ids}
}

// The incident: the store holds invoices without lines, and every answer that shows one must work and describe it as "".
func TestHiddenInvoiceWithoutLinesIsShown(t *testing.T) {
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	g := e.CreateCustomer(e.Globex, "Gus", "gus@example.com")
	var lined hiddenZeroLineInvoice
	hiddenZeroLineDo(t, e, http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": c.ID, "currency": "USD",
		"lines": []map[string]any{{"description": "Consulting", "quantity": 2, "unit_amount": 5000}, {"description": "Travel", "quantity": 1, "unit_amount": 900}}}, &lined)
	if lined.Description != "Consulting and 1 more" {
		t.Fatalf("an invoice with two lines: description %q, want %q", lined.Description, "Consulting and 1 more")
	}
	h := hiddenZeroLineHold(t, e, c, g)
	must := func(want int, method, path, key string, body, out any) {
		t.Helper()
		r := h.do(method, path, key, body)
		if r.Code != want {
			t.Fatalf("%s %s: want status %d, got %s", method, path, want, r)
		}
		if out != nil {
			r.JSON(t, out)
		}
	}

	for _, q := range []string{"", "?status=open", "?limit=2", "?customer_id=" + c.ID} {
		var page hiddenZeroLinePage
		must(http.StatusOK, "GET", "/v1/invoices"+q, e.Acme.Key, nil, &page)
		if len(page.Items) == 0 {
			t.Fatalf("GET /v1/invoices%s: no items", q)
		}
	}
	var all hiddenZeroLinePage
	must(http.StatusOK, "GET", "/v1/invoices?limit=100", e.Acme.Key, nil, &all)
	if len(all.Items) != 4 {
		t.Fatalf("GET /v1/invoices: %d items, want 4: %+v", len(all.Items), all.Items)
	}
	desc := map[string]string{}
	for _, it := range all.Items {
		desc[it.ID] = it.Description
	}
	if desc[lined.ID] != "Consulting and 1 more" {
		t.Fatalf("GET /v1/invoices: the invoice with lines is described %q, want %q", desc[lined.ID], "Consulting and 1 more")
	}
	for _, id := range h.lineless {
		d, ok := desc[id]
		if !ok || d != "" {
			t.Fatalf("GET /v1/invoices: invoice %s without lines: listed %v, description %q, want \"\"", id, ok, d)
		}
		var got hiddenZeroLineInvoice
		must(http.StatusOK, "GET", "/v1/invoices/"+id, e.Acme.Key, nil, &got)
		if got.ID != id || got.Description != "" || got.Total != 0 {
			t.Fatalf("GET /v1/invoices/%s = %+v, want the invoice with description \"\" and total 0", id, got)
		}
	}
	must(http.StatusOK, "GET", "/v1/invoices", e.Globex.Key, nil, nil)

	var paid struct {
		Invoice hiddenZeroLineInvoice `json:"invoice"`
	}
	must(http.StatusOK, "POST", "/v1/invoices/"+h.lineless[0]+"/pay", e.Acme.Key, nil, &paid)
	if paid.Invoice.ID != h.lineless[0] || paid.Invoice.Description != "" || paid.Invoice.Status != "paid" {
		t.Fatalf("pay an invoice without lines: invoice = %+v, want it paid with description \"\"", paid.Invoice)
	}
	if calls := h.gateway.Calls(); len(calls) != 0 {
		t.Fatalf("an invoice without lines was charged: %+v", calls)
	}
	var voided hiddenZeroLineInvoice
	must(http.StatusOK, "POST", "/v1/invoices/"+h.lineless[1]+"/void", e.Acme.Key, nil, &voided)
	if voided.ID != h.lineless[1] || voided.Description != "" || voided.Status != "void" {
		t.Fatalf("void an invoice without lines = %+v, want it void with description \"\"", voided)
	}
	for _, q := range []string{"?status=paid", "?status=void", "?status=open"} {
		var page hiddenZeroLinePage
		must(http.StatusOK, "GET", "/v1/invoices"+q, e.Acme.Key, nil, &page)
		if len(page.Items) == 0 {
			t.Fatalf("GET /v1/invoices%s: no items", q)
		}
	}
	var created hiddenZeroLineInvoice
	must(http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": c.ID, "currency": "USD",
		"lines": []map[string]any{{"description": "Support", "quantity": 1, "unit_amount": 100}}}, &created)
	if created.Description != "Support" {
		t.Fatalf("create: description %q, want %q", created.Description, "Support")
	}
	var list hiddenZeroLinePage
	must(http.StatusOK, "GET", "/v1/invoices?limit=100", e.Acme.Key, nil, &list)
	if len(list.Items) != 5 {
		t.Fatalf("GET /v1/invoices after a new invoice: %d items, want 5", len(list.Items))
	}
}

// A free licensed plan is invoiced like any other: one line for the plan on the first invoice and on every renewal, paid at
// once without a charge.
func TestHiddenFreePlanInvoicesHaveTheirLine(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	withCard := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	hiddenZeroLineDo(t, e, http.StatusOK, "PATCH", "/v1/customers/"+withCard.ID, e.Acme.Key, map[string]any{"default_payment_method": "pm_card_visa"}, nil)
	withoutCard := e.CreateCustomer(e.Acme, "John Roe", "john@example.com")
	plan := e.CreatePlan(e.Acme, "Starter Free", "USD", "0")
	if plan.UnitAmount != 0 {
		t.Fatalf("the free plan's unit_amount is %d", plan.UnitAmount)
	}

	type subCase struct {
		customer string
		quantity int
		sub      store.Subscription
	}
	cases := []*subCase{{customer: withCard.ID, quantity: 3}, {customer: withoutCard.ID, quantity: 1}}
	for _, sc := range cases {
		hiddenZeroLineDo(t, e, http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": sc.customer, "plan_id": plan.ID, "quantity": sc.quantity}, &sc.sub)
		if sc.sub.Status != store.Active {
			t.Fatalf("a subscription to a free plan is %q, want active: %+v", sc.sub.Status, sc.sub)
		}
	}

	check := func(sc *subCase, n int, start, end time.Time, quantity int64) {
		t.Helper()
		invs := hiddenZeroLineSubInvoices(t, e, e.Acme.Key, sc.sub.ID)
		if len(invs) != n {
			t.Fatalf("subscription %s (free plan): %d invoices, want %d: %+v", sc.sub.ID, len(invs), n, invs)
		}
		inv := invs[n-1]
		want := hiddenZeroLineLine{Description: "Starter Free, " + hiddenZeroLinePeriod(start, end), Quantity: quantity, UnitAmount: 0}
		if len(inv.Lines) != 1 || inv.Lines[0] != want {
			t.Fatalf("subscription %s (free plan), invoice %d: lines %+v, want exactly one line %+v", sc.sub.ID, n, inv.Lines, want)
		}
		if inv.Status != "paid" || inv.Total != 0 || inv.Description != want.Description {
			t.Fatalf("subscription %s (free plan), invoice %d = %+v, want it paid at once, total 0, description %q", sc.sub.ID, n, inv, want.Description)
		}
	}
	for _, sc := range cases {
		check(sc, 1, sc.sub.CurrentPeriodStart, sc.sub.CurrentPeriodEnd, int64(sc.quantity))
	}

	// the quantity changes before the first renewal; every renewal bills the subscription's current quantity
	hiddenZeroLineDo(t, e, http.StatusOK, "PATCH", "/v1/subscriptions/"+cases[0].sub.ID, e.Acme.Key, map[string]any{"quantity": 5}, nil)
	cases[0].quantity = 5
	for renewal := 1; renewal <= 2; renewal++ {
		e.Clock.Set(cases[0].sub.CurrentPeriodEnd)
		if n, err := e.Subs.RenewDue(ctx); err != nil || n != len(cases) {
			t.Fatalf("renewal %d: RenewDue = %d, %v; want %d, nil", renewal, n, err, len(cases))
		}
		for _, sc := range cases {
			var got store.Subscription
			hiddenZeroLineDo(t, e, http.StatusOK, "GET", "/v1/subscriptions/"+sc.sub.ID, e.Acme.Key, nil, &got)
			if got.Status != store.Active || !got.CurrentPeriodEnd.After(sc.sub.CurrentPeriodEnd) {
				t.Fatalf("renewal %d: subscription = %+v, want it active in a new period after %s", renewal, got, sc.sub.CurrentPeriodEnd)
			}
			check(sc, renewal+1, got.CurrentPeriodStart, got.CurrentPeriodEnd, int64(sc.quantity))
			sc.sub = got
		}
	}
	if calls := e.Gateway.Calls(); len(calls) != 0 {
		t.Fatalf("free invoices were sent to the gateway: %+v", calls)
	}
}

// A metered period without usage is not invoiced at all; the subscription still renews, and usage is billed as before.
func TestHiddenMeteredRenewalWithoutUsageCreatesNoInvoice(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	c := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	var plan store.Plan
	hiddenZeroLineDo(t, e, http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, map[string]any{"name": "API calls", "currency": "USD", "price": "0.03",
		"interval": "month", "usage_type": "metered"}, &plan)
	var sub store.Subscription
	hiddenZeroLineDo(t, e, http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": plan.ID}, &sub)
	if invs := hiddenZeroLineSubInvoices(t, e, e.Acme.Key, sub.ID); len(invs) != 0 {
		t.Fatalf("a metered subscription was invoiced when it started: %+v", invs)
	}

	firstEnd := sub.CurrentPeriodEnd
	e.Clock.Set(firstEnd)
	if n, err := e.Subs.RenewDue(ctx); err != nil || n != 1 {
		t.Fatalf("RenewDue = %d, %v; want 1, nil", n, err)
	}
	if invs := hiddenZeroLineSubInvoices(t, e, e.Acme.Key, sub.ID); len(invs) != 0 {
		t.Fatalf("a metered period without usage was invoiced: %+v", invs)
	}
	if invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{}); len(invs) != 0 {
		t.Fatalf("the store holds invoices after a metered renewal without usage: %+v", invs)
	}
	var got store.Subscription
	hiddenZeroLineDo(t, e, http.StatusOK, "GET", "/v1/subscriptions/"+sub.ID, e.Acme.Key, nil, &got)
	if got.Status != store.Active || !got.CurrentPeriodEnd.After(firstEnd) {
		t.Fatalf("after a renewal without usage: subscription = %+v, want it active in a new period after %s", got, firstEnd)
	}
	if n, err := e.Subs.RenewDue(ctx); err != nil || n != 0 {
		t.Fatalf("the renewed subscription is still due: RenewDue = %d, %v", n, err)
	}

	// usage in the next period is billed as before
	hiddenZeroLineDo(t, e, http.StatusCreated, "POST", "/v1/subscriptions/"+sub.ID+"/usage", e.Acme.Key, map[string]any{"quantity": 40,
		"timestamp": got.CurrentPeriodStart.Add(time.Hour).Format(time.RFC3339)}, nil)
	e.Clock.Set(got.CurrentPeriodEnd)
	if n, err := e.Subs.RenewDue(ctx); err != nil || n != 1 {
		t.Fatalf("second renewal: RenewDue = %d, %v; want 1, nil", n, err)
	}
	invs := hiddenZeroLineSubInvoices(t, e, e.Acme.Key, sub.ID)
	if len(invs) != 1 || len(invs[0].Lines) != 1 || invs[0].Lines[0].Quantity != 40 || invs[0].Lines[0].UnitAmount != 3 {
		t.Fatalf("a metered period with usage 40: invoices %+v, want one invoice with one line of 40 x 3", invs)
	}
}

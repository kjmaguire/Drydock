package api_test

// Hidden acceptance test of the trial task tally-snapshot-v3 (evals/tasks). The agents never see it.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

type hiddenNumberedInvoice struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Status string `json:"status"`
}

var hiddenNumberRe = regexp.MustCompile(`^INV-[0-9]{6,}$`)

func hiddenNumberDo(t *testing.T, e *apitest.Env, want int, method, path, key string, body, out any) {
	t.Helper()
	r := e.Do(method, path, key, body)
	if r.Code != want {
		t.Fatalf("%s %s: want status %d, got %s", method, path, want, r)
	}
	if out != nil {
		r.JSON(t, out)
	}
}

// hiddenNumbersOf lists all of an organization's invoices through the API: invoice ID -> number.
func hiddenNumbersOf(t *testing.T, e *apitest.Env, key, query string) map[string]string {
	t.Helper()
	out := map[string]string{}
	cursor := ""
	for range 1000 {
		path := "/v1/invoices?limit=100" + query
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items      []hiddenNumberedInvoice `json:"items"`
			NextCursor string                  `json:"next_cursor"`
		}
		hiddenNumberDo(t, e, http.StatusOK, "GET", path, key, nil, &page)
		for _, it := range page.Items {
			out[it.ID] = it.Number
		}
		if cursor = page.NextCursor; cursor == "" {
			return out
		}
	}
	t.Fatal("GET /v1/invoices: too many pages")
	return nil
}

func hiddenNumber(n int) string { return fmt.Sprintf("INV-%06d", n) }

// Every invoice answer shows the invoice's number: "INV-" and a per-organization sequence of at least 6 digits, given when
// the invoice is created, in creation order, never reused.
func TestHiddenInvoiceNumbersOnEveryAnswer(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	gus := e.CreateCustomer(e.Globex, "Gus", "gus@example.com")
	want := map[string]map[string]string{e.Acme.ID: {}, e.Globex.ID: {}} // org -> invoice ID -> number

	create := func(org apitest.Org, customer string, number int) hiddenNumberedInvoice {
		t.Helper()
		var inv hiddenNumberedInvoice
		hiddenNumberDo(t, e, http.StatusCreated, "POST", "/v1/invoices", org.Key, map[string]any{"customer_id": customer, "currency": "USD",
			"lines": []map[string]any{{"description": "Work", "quantity": 1, "unit_amount": 1000}}}, &inv)
		if inv.Number != hiddenNumber(number) {
			t.Fatalf("POST /v1/invoices (%s): the new invoice %s has the number %q, want %q", org.Name, inv.ID, inv.Number, hiddenNumber(number))
		}
		want[org.ID][inv.ID] = inv.Number
		return inv
	}
	a1 := create(e.Acme, jane.ID, 1)
	create(e.Globex, gus.ID, 1)
	e.Clock.Advance(time.Minute)
	a2 := create(e.Acme, jane.ID, 2)
	create(e.Acme, jane.ID, 3)
	create(e.Globex, gus.ID, 2)

	check := func(what string) {
		t.Helper()
		for _, org := range []apitest.Org{e.Acme, e.Globex} {
			got := hiddenNumbersOf(t, e, org.Key, "")
			if len(got) != len(want[org.ID]) {
				t.Fatalf("%s: GET /v1/invoices (%s): %v, want %v", what, org.Name, got, want[org.ID])
			}
			for id, n := range want[org.ID] {
				if got[id] != n {
					t.Fatalf("%s: GET /v1/invoices (%s): invoice %s has the number %q, want %q", what, org.Name, id, got[id], n)
				}
				var one hiddenNumberedInvoice
				hiddenNumberDo(t, e, http.StatusOK, "GET", "/v1/invoices/"+id, org.Key, nil, &one)
				if one.Number != n {
					t.Fatalf("%s: GET /v1/invoices/%s: number %q, want %q", what, id, one.Number, n)
				}
			}
		}
	}
	check("after creating invoices")

	var paid struct {
		Invoice hiddenNumberedInvoice `json:"invoice"`
	}
	hiddenNumberDo(t, e, http.StatusOK, "POST", "/v1/invoices/"+a1.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, &paid)
	if paid.Invoice.Number != a1.Number || paid.Invoice.Status != "paid" {
		t.Fatalf("POST /v1/invoices/%s/pay: invoice %+v, want it paid with the number %q", a1.ID, paid.Invoice, a1.Number)
	}
	var voided hiddenNumberedInvoice
	hiddenNumberDo(t, e, http.StatusOK, "POST", "/v1/invoices/"+a2.ID+"/void", e.Acme.Key, nil, &voided)
	if voided.Number != a2.Number || voided.Status != "void" {
		t.Fatalf("POST /v1/invoices/%s/void: invoice %+v, want it void with the number %q", a2.ID, voided, a2.Number)
	}
	for _, q := range []string{"&status=paid", "&status=void", "&status=open", "&customer_id=" + jane.ID} {
		for id, n := range hiddenNumbersOf(t, e, e.Acme.Key, q) {
			if n != want[e.Acme.ID][id] {
				t.Fatalf("GET /v1/invoices?%s: invoice %s has the number %q, want %q", q[1:], id, n, want[e.Acme.ID][id])
			}
		}
	}
	check("after paying and voiding")

	// a subscription's invoices are numbered too: the first one and a renewal
	plan := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	var sub store.Subscription
	hiddenNumberDo(t, e, http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": jane.ID, "plan_id": plan.ID}, &sub)
	first := hiddenNumbersOf(t, e, e.Acme.Key, "&subscription_id="+sub.ID)
	if len(first) != 1 {
		t.Fatalf("the subscription's first invoice: %v, want one invoice numbered %s", first, hiddenNumber(4))
	}
	for id, n := range first {
		if n != hiddenNumber(4) {
			t.Fatalf("the subscription's first invoice %s has the number %q, want %q", id, n, hiddenNumber(4))
		}
		want[e.Acme.ID][id] = n
	}
	e.Clock.Set(sub.CurrentPeriodEnd)
	if n, err := e.Subs.RenewDue(ctx); err != nil || n != 1 {
		t.Fatalf("RenewDue = %d, %v", n, err)
	}
	renewed := hiddenNumbersOf(t, e, e.Acme.Key, "&subscription_id="+sub.ID)
	if len(renewed) != 2 {
		t.Fatalf("after the renewal the subscription has invoices %v, want 2", renewed)
	}
	for id, n := range renewed {
		if _, ok := first[id]; !ok {
			if n != hiddenNumber(5) {
				t.Fatalf("the renewal's invoice %s has the number %q, want %q", id, n, hiddenNumber(5))
			}
			want[e.Acme.ID][id] = n
		}
	}

	// the clock goes back: a new invoice still gets the next number, and no other invoice's number changes
	e.Clock.Set(apitest.Start.Add(-48 * time.Hour))
	create(e.Acme, jane.ID, 6)
	inv, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Acme.ID, CustomerID: jane.ID, Currency: money.EUR,
		Lines: []store.Line{{Description: "Earlier", Quantity: 1, UnitAmount: 500}}}, apitest.Start.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want[e.Acme.ID][inv.ID] = hiddenNumber(7)
	create(e.Globex, gus.ID, 3)
	check("after the clock went back")
	for _, org := range want {
		for id, n := range org {
			if !hiddenNumberRe.MatchString(n) {
				t.Fatalf("invoice %s has the number %q", id, n)
			}
		}
	}
}

// Invoices created at the same time from many goroutines still get distinct numbers, one after the other.
func TestHiddenInvoiceNumbersUnderConcurrency(t *testing.T) {
	e := apitest.New(t)
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	gus := e.CreateCustomer(e.Globex, "Gus", "gus@example.com")
	const workers, each = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*workers*each)
	start := make(chan struct{})
	for w := range workers {
		for _, c := range []struct{ org, customer string }{{e.Acme.ID, jane.ID}, {e.Globex.ID, gus.ID}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := range each {
					if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: c.org, CustomerID: c.customer, Currency: money.USD,
						Lines: []store.Line{{Description: fmt.Sprintf("w%d-%d", w, i), Quantity: 1, UnitAmount: 100}}}, e.Clock.Now()); err != nil {
						errs <- err
					}
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for _, org := range []apitest.Org{e.Acme, e.Globex} {
		got := hiddenNumbersOf(t, e, org.Key, "")
		if len(got) != workers*each {
			t.Fatalf("%s: %d invoices, want %d", org.Name, len(got), workers*each)
		}
		seen := map[string]string{}
		for id, n := range got {
			if other, dup := seen[n]; dup {
				t.Fatalf("%s: invoices %s and %s both have the number %q", org.Name, other, id, n)
			}
			seen[n] = id
		}
		for i := 1; i <= workers*each; i++ {
			if _, ok := seen[hiddenNumber(i)]; !ok {
				t.Fatalf("%s: no invoice has the number %s; numbers given: %d", org.Name, hiddenNumber(i), len(seen))
			}
		}
	}
}

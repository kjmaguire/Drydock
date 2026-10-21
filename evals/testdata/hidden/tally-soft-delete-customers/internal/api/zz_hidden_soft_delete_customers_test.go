package api_test

// Hidden acceptance test of the trial task tally-soft-delete-customers (evals/tasks). The agents never see it.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

const hiddenSDDay = 24 * time.Hour

// hiddenSDPurger is the method the task asks for: store.PurgeDeletedCustomers(now time.Time) int.
type hiddenSDPurger interface {
	PurgeDeletedCustomers(now time.Time) int
}

func hiddenSDPurge(t *testing.T, e *apitest.Env, now time.Time) int {
	t.Helper()
	p, ok := any(e.Store).(hiddenSDPurger)
	if !ok {
		t.Fatalf("*store.Store has no method PurgeDeletedCustomers(now time.Time) int")
	}
	return p.PurgeDeletedCustomers(now)
}

// hiddenSDWantError checks that a response is the error status with {"error": code}.
func hiddenSDWantError(t *testing.T, what string, r *apitest.Response, status int, code string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if r.Code != status || json.Unmarshal(r.Body, &body) != nil || body.Error != code {
		t.Fatalf("%s: want %d {\"error\":%q}, got %s", what, status, code, r)
	}
}

func hiddenSDWantStatus(t *testing.T, what string, r *apitest.Response, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("%s: want %d, got %s", what, status, r)
	}
}

type hiddenSDPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func (p hiddenSDPage) ids() []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.ID)
	}
	return out
}

// hiddenSDList returns the IDs GET /v1/customers answers with the query, following every page.
func hiddenSDList(t *testing.T, e *apitest.Env, key, query string) []string {
	t.Helper()
	out := []string{}
	cursor := ""
	for range 100 {
		path := "/v1/customers?limit=100"
		if query != "" {
			path += "&" + query
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := e.Do("GET", path, key, nil)
		hiddenSDWantStatus(t, "GET "+path, r, http.StatusOK)
		var page hiddenSDPage
		r.JSON(t, &page)
		out = append(out, page.ids()...)
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
	t.Fatalf("GET /v1/customers?%s: the pages never end", query)
	return nil
}

func hiddenSDSame(got, want []string) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// hiddenSDInvoiceCustomers returns the raw "customer" of an invoice as GET /v1/invoices/{id} shows it and as GET
// /v1/invoices shows it.
func hiddenSDInvoiceCustomers(t *testing.T, e *apitest.Env, key, invoiceID string) (single, listed json.RawMessage) {
	t.Helper()
	r := e.Do("GET", "/v1/invoices/"+invoiceID, key, nil)
	hiddenSDWantStatus(t, "GET /v1/invoices/"+invoiceID, r, http.StatusOK)
	var one map[string]json.RawMessage
	r.JSON(t, &one)
	single, ok := one["customer"]
	if !ok {
		t.Fatalf("GET /v1/invoices/%s: no \"customer\" field: %s", invoiceID, r)
	}
	cursor := ""
	for range 100 {
		path := "/v1/invoices?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		lr := e.Do("GET", path, key, nil)
		hiddenSDWantStatus(t, "GET "+path, lr, http.StatusOK)
		var page struct {
			Items      []map[string]json.RawMessage `json:"items"`
			NextCursor string                       `json:"next_cursor"`
		}
		lr.JSON(t, &page)
		for _, it := range page.Items {
			var id string
			_ = json.Unmarshal(it["id"], &id)
			if id == invoiceID {
				if listed = it["customer"]; listed == nil {
					t.Fatalf("GET /v1/invoices: invoice %s has no \"customer\" field: %s", invoiceID, lr)
				}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if listed == nil {
		t.Fatalf("GET /v1/invoices does not list invoice %s", invoiceID)
	}
	return single, listed
}

// hiddenSDWantDeletedRef checks that an invoice shows its customer c with "deleted": true, alone and in the list.
func hiddenSDWantDeletedRef(t *testing.T, e *apitest.Env, key, invoiceID string, c store.Customer) {
	t.Helper()
	single, listed := hiddenSDInvoiceCustomers(t, e, key, invoiceID)
	for _, v := range []struct {
		where string
		raw   json.RawMessage
	}{{"GET /v1/invoices/" + invoiceID, single}, {"GET /v1/invoices", listed}} {
		var ref map[string]any
		if err := json.Unmarshal(v.raw, &ref); err != nil || ref == nil || ref["id"] != c.ID || ref["name"] != c.Name ||
			ref["email"] != c.Email || ref["deleted"] != true {
			t.Fatalf("%s: invoice %s of the deleted customer %s: want \"customer\": {\"id\":%q,\"name\":%q,\"email\":%q,\"deleted\":true}, got %s",
				v.where, invoiceID, c.ID, c.ID, c.Name, c.Email, v.raw)
		}
	}
}

// hiddenSDWantLiveRef checks that an invoice shows its customer c, not marked deleted, alone and in the list.
func hiddenSDWantLiveRef(t *testing.T, e *apitest.Env, key, invoiceID string, c store.Customer) {
	t.Helper()
	single, listed := hiddenSDInvoiceCustomers(t, e, key, invoiceID)
	for _, v := range []struct {
		where string
		raw   json.RawMessage
	}{{"GET /v1/invoices/" + invoiceID, single}, {"GET /v1/invoices", listed}} {
		var ref map[string]any
		if err := json.Unmarshal(v.raw, &ref); err != nil || ref == nil || ref["id"] != c.ID || ref["name"] != c.Name ||
			ref["email"] != c.Email || ref["deleted"] == true {
			t.Fatalf("%s: invoice %s of the customer %s (not deleted): want \"customer\": {\"id\":%q,\"name\":%q,\"email\":%q} without \"deleted\": true, got %s",
				v.where, invoiceID, c.ID, c.ID, c.Name, c.Email, v.raw)
		}
	}
}

func hiddenSDInvoice(t *testing.T, e *apitest.Env, key, customerID string) string {
	t.Helper()
	var inv struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", key, map[string]any{"customer_id": customerID, "currency": "USD",
		"lines": []map[string]any{{"description": "Setup", "quantity": 1, "unit_amount": 5000}}}, &inv)
	return inv.ID
}

func hiddenSDExportRows(t *testing.T, e *apitest.Env, key string) [][]string {
	t.Helper()
	r := e.Do("GET", "/v1/customers/export.csv", key, nil)
	hiddenSDWantStatus(t, "GET /v1/customers/export.csv", r, http.StatusOK)
	rows, err := csv.NewReader(strings.NewReader(string(r.Body))).ReadAll()
	if err != nil {
		t.Fatalf("GET /v1/customers/export.csv is not CSV: %v\n%s", err, r.Body)
	}
	return rows
}

func hiddenSDExportHas(rows [][]string, id string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if cell == id {
				return true
			}
		}
	}
	return false
}

func TestHiddenSoftDeleteHidesTheCustomerEverywhere(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	ann := e.CreateCustomer(e.Acme, "Ann Alder", "ann@example.com")
	e.Clock.Advance(time.Minute)
	bob := e.CreateCustomer(e.Acme, "Bob Birch", "bob.birch@example.com")
	e.Clock.Advance(time.Minute)
	cid := e.CreateCustomer(e.Acme, "Cid Cedar", "cid@example.com")
	e.Clock.Advance(time.Minute)
	plan := e.CreatePlan(e.Acme, "Team", "USD", "10.00")

	bobInvoice := hiddenSDInvoice(t, e, key, bob.ID)
	var paid struct {
		Payment struct {
			ID string `json:"id"`
		} `json:"payment"`
	}
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+bobInvoice+"/pay", key, map[string]string{"payment_method": "pm_card_visa"}, &paid)
	annInvoice := hiddenSDInvoice(t, e, key, ann.ID)

	r := e.Do("DELETE", "/v1/customers/"+bob.ID, key, nil)
	hiddenSDWantStatus(t, "DELETE /v1/customers/{id}", r, http.StatusNoContent)

	hiddenSDWantError(t, "GET a deleted customer", e.Do("GET", "/v1/customers/"+bob.ID, key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "PATCH a deleted customer", e.Do("PATCH", "/v1/customers/"+bob.ID, key, map[string]any{"name": "Bob Again"}), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "DELETE a deleted customer", e.Do("DELETE", "/v1/customers/"+bob.ID, key, nil), http.StatusNotFound, "not_found")

	want := []string{ann.ID, cid.ID}
	if got := hiddenSDList(t, e, key, ""); !hiddenSDSame(got, want) {
		t.Fatalf("GET /v1/customers lists %v, want %v (the deleted customer %s left out)", got, want, bob.ID)
	}
	var page hiddenSDPage
	e.MustDo(http.StatusOK, "GET", "/v1/customers?limit=2", key, nil, &page)
	if got := page.ids(); !hiddenSDSame(got, want) {
		t.Fatalf("GET /v1/customers?limit=2 lists %v, want %v: the deleted customer must not take a place in a page", got, want)
	}
	page = hiddenSDPage{}
	e.MustDo(http.StatusOK, "GET", "/v1/customers?limit=1", key, nil, &page)
	if got := page.ids(); !hiddenSDSame(got, []string{ann.ID}) || page.NextCursor == "" {
		t.Fatalf("GET /v1/customers?limit=1: want [%s] and a next_cursor, got %+v", ann.ID, page)
	}
	cur := page.NextCursor
	page = hiddenSDPage{}
	e.MustDo(http.StatusOK, "GET", "/v1/customers?limit=1&cursor="+url.QueryEscape(cur), key, nil, &page)
	if got := page.ids(); !hiddenSDSame(got, []string{cid.ID}) {
		t.Fatalf("the second page of GET /v1/customers?limit=1 lists %v, want [%s]", got, cid.ID)
	}
	for _, q := range []string{"q=birch", "q=Bob", "email=" + url.QueryEscape("bob.birch@example.com"), "email=" + url.QueryEscape("Bob.Birch@Example.COM")} {
		if got := hiddenSDList(t, e, key, q); len(got) != 0 {
			t.Fatalf("GET /v1/customers?%s lists %v, want nothing: the customer is deleted", q, got)
		}
	}
	if got := hiddenSDList(t, e, key, "q=example.com"); !hiddenSDSame(got, want) {
		t.Fatalf("GET /v1/customers?q=example.com lists %v, want %v", got, want)
	}
	if got := hiddenSDList(t, e, key, "email="+url.QueryEscape("cid@example.com")); !hiddenSDSame(got, []string{cid.ID}) {
		t.Fatalf("GET /v1/customers?email=cid@example.com lists %v, want [%s]", got, cid.ID)
	}

	rows := hiddenSDExportRows(t, e, key)
	if hiddenSDExportHas(rows, bob.ID) || hiddenSDExportHas(rows, bob.Email) {
		t.Fatalf("export.csv still has the deleted customer %s:\n%q", bob.ID, rows)
	}
	if !hiddenSDExportHas(rows, ann.ID) || !hiddenSDExportHas(rows, cid.ID) || len(rows) != 3 {
		t.Fatalf("export.csv: want the header and the customers %s and %s, got\n%q", ann.ID, cid.ID, rows)
	}

	hiddenSDWantError(t, "POST /v1/invoices for a deleted customer", e.Do("POST", "/v1/invoices", key, map[string]any{"customer_id": bob.ID,
		"currency": "USD", "lines": []map[string]any{{"description": "More", "quantity": 1, "unit_amount": 100}}}), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "POST /v1/subscriptions for a deleted customer", e.Do("POST", "/v1/subscriptions", key,
		map[string]any{"customer_id": bob.ID, "plan_id": plan.ID, "quantity": 1}), http.StatusNotFound, "not_found")
	var subs struct {
		Items []json.RawMessage `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/subscriptions?limit=100", key, nil, &subs)
	if len(subs.Items) != 0 {
		t.Fatalf("a refused subscription for a deleted customer was created: %d subscriptions", len(subs.Items))
	}

	// Its invoice and its payment stay readable, and the invoice shows the customer as deleted.
	hiddenSDWantDeletedRef(t, e, key, bobInvoice, bob)
	hiddenSDWantLiveRef(t, e, key, annInvoice, ann)
	var pays struct {
		Items []struct {
			ID        string `json:"id"`
			InvoiceID string `json:"invoice_id"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/payments?invoice_id="+bobInvoice, key, nil, &pays)
	if len(pays.Items) != 1 || pays.Items[0].ID != paid.Payment.ID {
		t.Fatalf("GET /v1/payments?invoice_id=%s: want the payment %s, got %+v", bobInvoice, paid.Payment.ID, pays.Items)
	}

	var events struct {
		Items []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/events?type=customer.deleted&limit=100", key, nil, &events)
	sent := false
	for _, ev := range events.Items {
		if ev.Type == "customer.deleted" && strings.Contains(string(ev.Data), bob.ID) {
			sent = true
		}
	}
	if !sent {
		t.Fatalf("no customer.deleted event for %s: %+v", bob.ID, events.Items)
	}
}

func TestHiddenSoftDeleteFreesTheEmailAddress(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+jane.ID, key, nil, nil)

	r := e.Do("POST", "/v1/customers", key, map[string]string{"name": "Jane Newer", "email": "Jane@Example.com"})
	hiddenSDWantStatus(t, "a new customer with a deleted customer's email address", r, http.StatusCreated)
	var jane2 store.Customer
	r.JSON(t, &jane2)
	if got := hiddenSDList(t, e, key, "email="+url.QueryEscape("jane@example.com")); !hiddenSDSame(got, []string{jane2.ID}) {
		t.Fatalf("GET /v1/customers?email=jane@example.com lists %v, want only the new customer %s", got, jane2.ID)
	}
	hiddenSDWantError(t, "restore a customer whose email address another customer now uses (in another case)",
		e.Do("POST", "/v1/customers/"+jane.ID+"/restore", key, nil), http.StatusConflict, "email_taken")
	hiddenSDWantError(t, "GET a customer whose restore was refused", e.Do("GET", "/v1/customers/"+jane.ID, key, nil), http.StatusNotFound, "not_found")

	john := e.CreateCustomer(e.Acme, "John Roe", "john@example.com")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+john.ID, key, nil, nil)
	john2 := e.CreateCustomer(e.Acme, "John Two", "john@example.com")
	hiddenSDWantError(t, "restore a customer whose email address another customer now uses",
		e.Do("POST", "/v1/customers/"+john.ID+"/restore", key, nil), http.StatusConflict, "email_taken")
	if got := hiddenSDList(t, e, key, ""); !hiddenSDSame(got, []string{jane2.ID, john2.ID}) {
		t.Fatalf("GET /v1/customers lists %v after refused restores, want %v", got, []string{jane2.ID, john2.ID})
	}

	// Once the address is free again, the restore works.
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+jane2.ID, key, nil, nil)
	var back store.Customer
	r = e.Do("POST", "/v1/customers/"+jane.ID+"/restore", key, nil)
	hiddenSDWantStatus(t, "restore once the email address is free", r, http.StatusOK)
	r.JSON(t, &back)
	if back.ID != jane.ID || back.Email != "jane@example.com" || back.Name != "Jane Doe" {
		t.Fatalf("restore answered %+v, want the customer %s", back, jane.ID)
	}
	if got := hiddenSDList(t, e, key, "email="+url.QueryEscape("JANE@example.com")); !hiddenSDSame(got, []string{jane.ID}) {
		t.Fatalf("GET /v1/customers?email=JANE@example.com lists %v after the restore, want [%s]", got, jane.ID)
	}
	if r := e.Do("POST", "/v1/customers", key, map[string]string{"name": "Jane Third", "email": "jane@example.com"}); r.Code != http.StatusConflict {
		t.Fatalf("a new customer with the address of a restored customer: want 409, got %s", r)
	}
}

func TestHiddenRestoreCustomer(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	live := e.CreateCustomer(e.Acme, "Live Larch", "live@example.com")
	hiddenSDWantError(t, "restore a customer that is not deleted", e.Do("POST", "/v1/customers/"+live.ID+"/restore", key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "restore an unknown customer", e.Do("POST", "/v1/customers/cus_999999/restore", key, nil), http.StatusNotFound, "not_found")

	e.Clock.Advance(time.Minute)
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+jane.ID, key, map[string]any{"metadata": map[string]string{"crm": "7"},
		"default_payment_method": "pm_card_visa"}, &jane)
	inv := hiddenSDInvoice(t, e, key, jane.ID)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+jane.ID, key, nil, nil)
	hiddenSDWantError(t, "another organization restores the customer", e.Do("POST", "/v1/customers/"+jane.ID+"/restore", e.Globex.Key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "GET after another organization tried to restore", e.Do("GET", "/v1/customers/"+jane.ID, key, nil), http.StatusNotFound, "not_found")

	e.Clock.Advance(29*hiddenSDDay + 23*time.Hour)
	r := e.Do("POST", "/v1/customers/"+jane.ID+"/restore", key, nil)
	hiddenSDWantStatus(t, "restore 29 days and 23 hours after the deletion", r, http.StatusOK)
	var back store.Customer
	r.JSON(t, &back)
	if back.ID != jane.ID || back.Name != jane.Name || back.Email != jane.Email || back.DefaultPaymentMethod != "pm_card_visa" ||
		!reflect.DeepEqual(back.Metadata, map[string]string{"crm": "7"}) {
		t.Fatalf("restore answered %s, want the customer as it was: %+v", r, jane)
	}
	var got store.Customer
	e.MustDo(http.StatusOK, "GET", "/v1/customers/"+jane.ID, key, nil, &got)
	if got.Email != jane.Email || got.Metadata["crm"] != "7" {
		t.Fatalf("GET a restored customer = %+v", got)
	}
	if ids := hiddenSDList(t, e, key, ""); !hiddenSDSame(ids, []string{live.ID, jane.ID}) {
		t.Fatalf("GET /v1/customers lists %v after the restore, want %v", ids, []string{live.ID, jane.ID})
	}
	if ids := hiddenSDList(t, e, key, "q=jane"); !hiddenSDSame(ids, []string{jane.ID}) {
		t.Fatalf("GET /v1/customers?q=jane lists %v after the restore, want [%s]", ids, jane.ID)
	}
	if !hiddenSDExportHas(hiddenSDExportRows(t, e, key), jane.ID) {
		t.Fatalf("export.csv does not have the restored customer %s", jane.ID)
	}
	hiddenSDWantLiveRef(t, e, key, inv, jane)
	hiddenSDWantError(t, "restore a restored customer", e.Do("POST", "/v1/customers/"+jane.ID+"/restore", key, nil), http.StatusNotFound, "not_found")
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+jane.ID, key, map[string]any{"name": "Jane Q. Doe"}, nil)

	// The 30 days count from each deletion, on the service's clock.
	xena := e.CreateCustomer(e.Acme, "Xena", "xena@example.com")
	yuri := e.CreateCustomer(e.Acme, "Yuri", "yuri@example.com")
	zoe := e.CreateCustomer(e.Acme, "Zoe", "zoe@example.com")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+xena.ID, key, nil, nil)
	e.Clock.Advance(20 * hiddenSDDay)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+yuri.ID, key, nil, nil)
	e.Clock.Advance(10*hiddenSDDay + time.Minute)
	hiddenSDWantError(t, "restore 30 days and a minute after the deletion", e.Do("POST", "/v1/customers/"+xena.ID+"/restore", key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "GET a customer deleted 30 days and a minute ago", e.Do("GET", "/v1/customers/"+xena.ID, key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantStatus(t, "restore 10 days after the deletion", e.Do("POST", "/v1/customers/"+yuri.ID+"/restore", key, nil), http.StatusOK)
	e.Clock.Advance(40 * hiddenSDDay)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+zoe.ID, key, nil, nil)
	e.Clock.Advance(time.Hour)
	hiddenSDWantStatus(t, "restore an hour after deleting a customer created 70 days ago", e.Do("POST", "/v1/customers/"+zoe.ID+"/restore", key, nil), http.StatusOK)
}

func TestHiddenDeleteCustomerWithSubscriptions(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	plan := e.CreatePlan(e.Acme, "Team", "USD", "10.00")
	var trialPlan store.Plan
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", key, map[string]any{"name": "Trial", "currency": "USD", "price": "10.00", "interval": "month", "trial_days": 14}, &trialPlan)

	type hiddenCase struct {
		status   store.SubStatus
		customer store.Customer
		sub      store.Subscription
	}
	var cases []*hiddenCase
	for _, st := range []store.SubStatus{store.Active, store.Trialing, store.PastDue} {
		c := e.CreateCustomer(e.Acme, "Customer "+string(st), string(st)+"@example.com")
		planID := plan.ID
		switch st {
		case store.Trialing:
			planID = trialPlan.ID
		case store.PastDue:
			e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+c.ID, key, map[string]any{"default_payment_method": "pm_card_declined"}, nil)
		}
		var sub store.Subscription
		e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", key, map[string]any{"customer_id": c.ID, "plan_id": planID, "quantity": 1}, &sub)
		if sub.Status != st {
			t.Fatalf("setting up: a subscription that should be %s is %s", st, sub.Status)
		}
		cases = append(cases, &hiddenCase{status: st, customer: c, sub: sub})
	}
	for _, c := range cases {
		hiddenSDWantError(t, "DELETE a customer with a subscription that is "+string(c.status), e.Do("DELETE", "/v1/customers/"+c.customer.ID, key, nil),
			http.StatusConflict, "has_active_subscriptions")
		hiddenSDWantStatus(t, "GET a customer whose deletion was refused", e.Do("GET", "/v1/customers/"+c.customer.ID, key, nil), http.StatusOK)
	}
	for _, c := range cases {
		var sub store.Subscription
		e.MustDo(http.StatusOK, "POST", "/v1/subscriptions/"+c.sub.ID+"/cancel", key, nil, &sub)
		if sub.Status != store.Canceled {
			t.Fatalf("setting up: cancel answered %+v", sub)
		}
		hiddenSDWantStatus(t, "DELETE a customer whose subscription ("+string(c.status)+") was canceled", e.Do("DELETE", "/v1/customers/"+c.customer.ID, key, nil), http.StatusNoContent)
		hiddenSDWantError(t, "GET a deleted customer", e.Do("GET", "/v1/customers/"+c.customer.ID, key, nil), http.StatusNotFound, "not_found")
	}
}

func TestHiddenPurgeDeletedCustomers(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	t0 := e.Clock.Now()
	olga := e.CreateCustomer(e.Acme, "Olga Old", "olga@example.com")
	olgaInvoice := hiddenSDInvoice(t, e, key, olga.ID)
	kim := e.CreateCustomer(e.Acme, "Kim Keep", "kim@example.com")
	kimInvoice := hiddenSDInvoice(t, e, key, kim.ID)
	mia := e.CreateCustomer(e.Acme, "Mia Mid", "mia@example.com")
	lou := e.CreateCustomer(e.Acme, "Lou Late", "lou@example.com")
	gus := e.CreateCustomer(e.Globex, "Gus Globex", "gus@globex-customers.example")
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+olga.ID, key, nil, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+gus.ID, e.Globex.Key, nil, nil)
	e.Clock.Advance(10 * hiddenSDDay)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+mia.ID, key, nil, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+lou.ID, key, nil, nil)

	if n := hiddenSDPurge(t, e, t0.Add(30*hiddenSDDay-time.Minute)); n != 0 {
		t.Fatalf("PurgeDeletedCustomers(30 days less a minute after the first deletions) = %d, want 0", n)
	}
	hiddenSDWantDeletedRef(t, e, key, olgaInvoice, olga)

	e.Clock.Set(t0.Add(30*hiddenSDDay + time.Minute))
	if n := hiddenSDPurge(t, e, e.Clock.Now()); n != 2 {
		t.Fatalf("PurgeDeletedCustomers(30 days and a minute after deleting two customers, of two organizations) = %d, want 2", n)
	}
	if single, listed := hiddenSDInvoiceCustomers(t, e, key, olgaInvoice); string(single) != "null" || string(listed) != "null" {
		t.Fatalf("invoice %s of the purged customer %s: want \"customer\": null, got %s (GET /v1/invoices/{id}) and %s (GET /v1/invoices)",
			olgaInvoice, olga.ID, single, listed)
	}
	hiddenSDWantError(t, "restore a purged customer", e.Do("POST", "/v1/customers/"+olga.ID+"/restore", key, nil), http.StatusNotFound, "not_found")
	hiddenSDWantError(t, "restore a purged customer of Globex", e.Do("POST", "/v1/customers/"+gus.ID+"/restore", e.Globex.Key, nil), http.StatusNotFound, "not_found")
	for _, c := range e.Store.Export().Customers {
		if c.ID == olga.ID || c.ID == gus.ID {
			t.Fatalf("the purged customer %s is still in the store: %+v", c.ID, c)
		}
	}
	hiddenSDWantLiveRef(t, e, key, kimInvoice, kim)
	hiddenSDWantStatus(t, "GET a customer that was never deleted, after a purge", e.Do("GET", "/v1/customers/"+kim.ID, key, nil), http.StatusOK)
	hiddenSDWantStatus(t, "restore a customer deleted 20 days before a purge", e.Do("POST", "/v1/customers/"+mia.ID+"/restore", key, nil), http.StatusOK)
	if n := hiddenSDPurge(t, e, e.Clock.Now()); n != 0 {
		t.Fatalf("a second PurgeDeletedCustomers at the same time = %d, want 0", n)
	}

	e.Clock.Set(t0.Add(40*hiddenSDDay + 2*time.Minute))
	if n := hiddenSDPurge(t, e, e.Clock.Now()); n != 1 {
		t.Fatalf("PurgeDeletedCustomers(30 days and 2 minutes after the last deletion) = %d, want 1", n)
	}
	hiddenSDWantError(t, "restore a purged customer", e.Do("POST", "/v1/customers/"+lou.ID+"/restore", key, nil), http.StatusNotFound, "not_found")
	if ids := hiddenSDList(t, e, key, ""); !hiddenSDSame(ids, []string{kim.ID, mia.ID}) {
		t.Fatalf("GET /v1/customers lists %v after the purges, want %v", ids, []string{kim.ID, mia.ID})
	}
}

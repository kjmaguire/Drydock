package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/saasapi/internal/api"
	"example.com/saasapi/internal/store"
)

func newServer(t *testing.T) (*api.Server, *store.Store) {
	t.Helper()
	st := store.New()
	st.AddAccount(store.Account{ID: "acc_acme", Name: "Acme", Plan: store.PlanFree, TaxRateBP: 825})
	st.AddAccount(store.Account{ID: "acc_globex", Name: "Globex", Plan: store.PlanPro})
	st.AddKey("key-acme", "acc_acme")
	st.AddKey("key-globex", "acc_globex")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return api.New(st, api.Options{Now: func() time.Time { return now }}), st
}

func do(t *testing.T, h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestARequestWithoutAValidKeyIsUnauthorized(t *testing.T) {
	srv, _ := newServer(t)
	for _, key := range []string{"", "nope"} {
		if r := do(t, srv, "GET", "/api/customers", key, ""); r.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d", key, r.Code)
		}
	}
	if r := do(t, srv, "GET", "/healthz", "", ""); r.Code != http.StatusOK {
		t.Fatalf("healthz: %d", r.Code)
	}
}

func TestCreateAndReadACustomer(t *testing.T) {
	srv, _ := newServer(t)
	r := do(t, srv, "POST", "/api/customers", "key-acme", `{"name":"Jane Doe","email":"jane@example.com","plan":"pro"}`)
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var c store.Customer
	if err := json.Unmarshal(r.Body.Bytes(), &c); err != nil || c.ID == "" || c.Plan != store.PlanPro {
		t.Fatalf("created %+v %v", c, err)
	}
	if r := do(t, srv, "GET", "/api/customers/"+c.ID, "key-acme", ""); r.Code != http.StatusOK {
		t.Fatalf("get: %d", r.Code)
	}
	if r := do(t, srv, "POST", "/api/customers", "key-acme", `{"name":"","email":"x"}`); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid customer: %d", r.Code)
	}
}

func TestAnotherAccountsCustomerIsNotFound(t *testing.T) {
	srv, st := newServer(t)
	c, err := st.CreateCustomer("acc_globex", "Hank", "hank@globex.test", store.PlanFree, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r := do(t, srv, "GET", "/api/customers/"+c.ID, "key-acme", ""); r.Code != http.StatusNotFound {
		t.Fatalf("cross-account read: %d", r.Code)
	}
	r := do(t, srv, "GET", "/api/customers", "key-acme", "")
	if strings.Contains(r.Body.String(), c.ID) {
		t.Fatalf("another account's customer is listed: %s", r.Body)
	}
}

func TestAnInvoiceHasItsTotals(t *testing.T) {
	srv, st := newServer(t)
	c, _ := st.CreateCustomer("acc_acme", "Jane", "jane@example.com", store.PlanPro, time.Now())
	r := do(t, srv, "POST", "/api/invoices", "key-acme", `{"customer_id":"`+c.ID+`","lines":[{"description":"Pro plan","quantity":1,"unit_cents":10000}]}`)
	if r.Code != http.StatusCreated {
		t.Fatalf("create invoice: %d %s", r.Code, r.Body)
	}
	var v struct {
		ID     string `json:"id"`
		Totals struct {
			TotalCents int64 `json:"total_cents"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil || v.Totals.TotalCents != 10825 {
		t.Fatalf("invoice %s %v", r.Body, err)
	}
	if r := do(t, srv, "GET", "/api/invoices/"+v.ID, "key-globex", ""); r.Code != http.StatusNotFound {
		t.Fatalf("cross-account invoice: %d", r.Code)
	}
}

package api_test

// Hidden acceptance test of the trial task saasapi-csv-export (evals/tasks). The agents never see it.

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/saasapi/internal/api"
	"example.com/saasapi/internal/store"
)

func TestHiddenCustomersCSV(t *testing.T) {
	st := store.New()
	st.AddAccount(store.Account{ID: "acc_a", Name: "A", Plan: store.PlanFree})
	st.AddAccount(store.Account{ID: "acc_b", Name: "B", Plan: store.PlanPro})
	st.AddKey("key-a", "acc_a")
	st.AddKey("key-b", "acc_b")
	east := time.FixedZone("UTC-5", -5*3600)
	when := time.Date(2026, 3, 1, 9, 30, 0, 0, east) // 14:30 UTC
	mk := func(acct, name, email string, plan store.Plan, at time.Time) store.Customer {
		c, err := st.CreateCustomer(acct, name, email, plan, at)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1 := mk("acc_a", "Plain Person", "plain@example.com", store.PlanFree, when)
	other := mk("acc_b", "Other Tenant", "secret@other.test", store.PlanPro, when)
	c2 := mk("acc_a", `Smith, "Jr." & Co`, "smith@example.com", store.PlanPro, when.Add(time.Hour))
	c3 := mk("acc_a", "Line\nBreak Ltd", "lines@example.com", store.PlanFree, when.Add(2*time.Hour))
	c4 := mk("acc_a", `=HYPERLINK("http://evil.test")`, "+1@example.com", store.PlanFree, when.Add(3*time.Hour))
	c5 := mk("acc_a", "-dash", "@at@example.com", store.PlanFree, when.Add(4*time.Hour))
	srv := api.New(st, api.Options{Now: func() time.Time { return when }})

	req := httptest.NewRequest("GET", "/api/customers.csv", nil)
	req.Header.Set("X-API-Key", "key-a")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="customers.csv"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	body := rec.Body.String()
	if strings.Contains(body, other.ID) || strings.Contains(body, "secret@other.test") {
		t.Fatalf("another account's customer is in the export:\n%s", body)
	}
	// every line ends with CRLF (the line break inside the quoted name may be either)
	raw := strings.NewReplacer("Line\r\nBreak", "LineBreak", "Line\nBreak", "LineBreak").Replace(body)
	if !strings.HasSuffix(raw, "\r\n") || strings.Count(raw, "\n") != strings.Count(raw, "\r\n") {
		t.Fatalf("lines must end with CRLF:\n%q", body)
	}
	if !strings.HasPrefix(body, "id,name,email,plan,created_at\r\n") {
		t.Fatalf("header row:\n%q", body)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v\n%q", err, body)
	}
	at := func(d time.Duration) string { return when.Add(d).UTC().Format(time.RFC3339) }
	want := [][]string{
		{"id", "name", "email", "plan", "created_at"},
		{c1.ID, "Plain Person", "plain@example.com", "free", at(0)},
		{c2.ID, `Smith, "Jr." & Co`, "smith@example.com", "pro", at(time.Hour)},
		{c3.ID, "Line\nBreak Ltd", "lines@example.com", "free", at(2 * time.Hour)},
		{c4.ID, `'=HYPERLINK("http://evil.test")`, "'+1@example.com", "free", at(3 * time.Hour)},
		{c5.ID, "'-dash", "'@at@example.com", "free", at(4 * time.Hour)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows\n got %q\nwant %q", rows, want)
	}
	if at(0) != "2026-03-01T14:30:00Z" {
		t.Fatalf("test clock: %s", at(0))
	}

	for _, key := range []string{"", "wrong"} {
		req := httptest.NewRequest("GET", "/api/customers.csv", nil)
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d", key, rec.Code)
		}
	}

	// an account without customers gets just the header
	st.AddAccount(store.Account{ID: "acc_c", Name: "C", Plan: store.PlanFree})
	st.AddKey("key-c", "acc_c")
	req = httptest.NewRequest("GET", "/api/customers.csv", nil)
	req.Header.Set("X-API-Key", "key-c")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "id,name,email,plan,created_at\r\n" {
		t.Fatalf("empty export: %d %q", rec.Code, rec.Body)
	}
}

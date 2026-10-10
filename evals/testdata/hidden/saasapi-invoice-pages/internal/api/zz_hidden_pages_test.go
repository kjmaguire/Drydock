package api_test

// Hidden acceptance test of the trial task saasapi-invoice-pages (evals/tasks). The agents never see it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"example.com/saasapi/internal/api"
	"example.com/saasapi/internal/store"
)

type hiddenPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func TestHiddenInvoicePagination(t *testing.T) {
	st := store.New()
	st.AddAccount(store.Account{ID: "acc_a", Name: "A", Plan: store.PlanPro})
	st.AddAccount(store.Account{ID: "acc_b", Name: "B", Plan: store.PlanPro})
	st.AddKey("key-a", "acc_a")
	st.AddKey("key-b", "acc_b")
	ca, _ := st.CreateCustomer("acc_a", "A1", "a1@example.com", store.PlanPro, time.Now())
	cb, _ := st.CreateCustomer("acc_b", "B1", "b1@example.com", store.PlanPro, time.Now())
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	var want []string
	add := func(acct, cust string, at time.Time) string {
		inv, err := st.CreateInvoice(store.Invoice{AccountID: acct, CustomerID: cust, Lines: []store.LineItem{{Description: "x", Quantity: 1, UnitCents: 100}}}, at)
		if err != nil {
			t.Fatal(err)
		}
		return inv.ID
	}
	for i := 0; i < 45; i++ {
		// groups of three share a timestamp, so ties are ordered by id
		want = append(want, add("acc_a", ca.ID, base.Add(time.Duration(i/3)*time.Minute)))
		if i%5 == 0 {
			add("acc_b", cb.ID, base.Add(time.Duration(i)*time.Minute)) // another tenant's invoices never show up
		}
	}
	srv := api.New(st, api.Options{Now: func() time.Time { return base }})
	get := func(q url.Values, key string) (*httptest.ResponseRecorder, hiddenPage) {
		req := httptest.NewRequest("GET", "/api/invoices?"+q.Encode(), nil)
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var p hiddenPage
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec, p
	}

	// default limit 20: 20, 20, 5, and new invoices created while paging appear at the end exactly once
	var got []string
	cursor := ""
	pages := 0
	for {
		q := url.Values{}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		rec, p := get(q, "key-a")
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", pages+1, rec.Code, rec.Body)
		}
		pages++
		for _, it := range p.Items {
			got = append(got, it.ID)
		}
		if pages == 1 {
			if len(p.Items) != 20 {
				t.Fatalf("default limit: %d items", len(p.Items))
			}
			// two invoices arrive between page requests, newer than everything so far
			want = append(want, add("acc_a", ca.ID, base.Add(time.Hour)), add("acc_a", ca.ID, base.Add(time.Hour)))
		}
		if p.NextCursor == "" {
			break
		}
		if pages > 10 {
			t.Fatal("paging does not end")
		}
		cursor = p.NextCursor
	}
	if pages != 3 {
		t.Fatalf("47 invoices in pages of 20 took %d pages", pages)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d invoices, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d is %s, want %s (order oldest first, ties by id, nothing skipped or repeated)\n got %v\nwant %v", i, got[i], want[i], got, want)
		}
	}

	// an explicit limit
	rec, p := get(url.Values{"limit": {"100"}}, "key-a")
	if rec.Code != http.StatusOK || len(p.Items) != 47 || p.NextCursor != "" {
		t.Fatalf("limit 100: %d, %d items, next %q", rec.Code, len(p.Items), p.NextCursor)
	}
	rec, p = get(url.Values{"limit": {"1"}}, "key-a")
	if rec.Code != http.StatusOK || len(p.Items) != 1 || p.Items[0].ID != want[0] || p.NextCursor == "" {
		t.Fatalf("limit 1: %d %+v", rec.Code, p)
	}
	rec, p2 := get(url.Values{"limit": {"1"}, "cursor": {p.NextCursor}}, "key-a")
	if rec.Code != http.StatusOK || len(p2.Items) != 1 || p2.Items[0].ID != want[1] {
		t.Fatalf("second page of 1: %d %+v", rec.Code, p2)
	}

	// another account sees only its own invoices
	_, pb := get(url.Values{"limit": {"100"}}, "key-b")
	if len(pb.Items) != 9 {
		t.Fatalf("the other account has 9 invoices, got %d", len(pb.Items))
	}

	for _, bad := range []string{"0", "101", "-1", "abc", "1.5", ""} {
		if bad == "" {
			continue
		}
		rec, _ := get(url.Values{"limit": {bad}}, "key-a")
		var e map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || e["error"] != "invalid_limit" {
			t.Fatalf("limit %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	for _, bad := range []string{"!!!not-a-cursor!!!", "%%%"} {
		rec, _ := get(url.Values{"cursor": {bad}}, "key-a")
		var e map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || e["error"] != "invalid_cursor" {
			t.Fatalf("cursor %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

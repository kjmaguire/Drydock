package api_test

// Hidden acceptance test of the trial task tally-tiered-pricing (evals/tasks). The agents never see it.
//
// Plans, subscriptions and usage go through the HTTP API; renewals through e.Subs.RenewDue with e.Clock.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // apitest's organizations use IANA time zones; do not depend on the machine's zoneinfo

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

type hiddenTierView struct {
	UpTo       *int64 `json:"up_to"`
	UnitAmount int64  `json:"unit_amount"`
	FlatAmount int64  `json:"flat_amount"`
}

type hiddenTierPlan struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	UsageType string           `json:"usage_type"`
	TiersMode string           `json:"tiers_mode"`
	Tiers     []hiddenTierView `json:"tiers"`
}

func hiddenTierN(n int64) *int64 { return &n }

func hiddenTierFmt(tiers []hiddenTierView) string {
	parts := make([]string, 0, len(tiers))
	for _, t := range tiers {
		up := "null"
		if t.UpTo != nil {
			up = fmt.Sprint(*t.UpTo)
		}
		parts = append(parts, fmt.Sprintf("{up_to %s, unit_amount %d, flat_amount %d}", up, t.UnitAmount, t.FlatAmount))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// hiddenTierCheckPlan checks a plan answer's tiers_mode and tiers (amounts in minor units).
func hiddenTierCheckPlan(t *testing.T, when string, got hiddenTierPlan, mode string, tiers ...hiddenTierView) {
	t.Helper()
	if got.TiersMode != mode || hiddenTierFmt(got.Tiers) != hiddenTierFmt(tiers) {
		t.Fatalf("%s: tiers_mode %q and tiers %s, want %q and %s", when, got.TiersMode, hiddenTierFmt(got.Tiers), mode, hiddenTierFmt(tiers))
	}
}

func hiddenTierCreatePlan(t *testing.T, e *apitest.Env, body map[string]any) hiddenTierPlan {
	t.Helper()
	var p hiddenTierPlan
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, body, &p)
	return p
}

// hiddenTierAPICalls is a graduated or volume plan: units 1-1000 at 0.05; 1001-10000 at 0.04 plus 5.00; above at 0.03 plus
// 10.00.
func hiddenTierAPICalls(name, mode string) map[string]any {
	return map[string]any{"name": name, "currency": "USD", "interval": "month", "usage_type": "metered", "tiers_mode": mode,
		"tiers": []map[string]any{
			{"up_to": 1000, "unit_amount": "0.05"},
			{"up_to": 10000, "unit_amount": "0.04", "flat_amount": "5.00"},
			{"up_to": nil, "unit_amount": "0.03", "flat_amount": "10.00"},
		}}
}

var hiddenTierAPICallsTiers = []hiddenTierView{{hiddenTierN(1000), 5, 0}, {hiddenTierN(10000), 4, 500}, {nil, 3, 1000}}

func TestHiddenTieredPlanAnswers(t *testing.T) {
	e := apitest.New(t)
	grad := hiddenTierCreatePlan(t, e, hiddenTierAPICalls("API calls", "graduated"))
	if grad.UsageType != "metered" || grad.Name != "API calls" {
		t.Fatalf("POST /v1/plans: name %q, usage_type %q, want \"API calls\" and metered", grad.Name, grad.UsageType)
	}
	hiddenTierCheckPlan(t, "POST /v1/plans (graduated, no price)", grad, "graduated", hiddenTierAPICallsTiers...)

	vol := hiddenTierCreatePlan(t, e, map[string]any{"name": "Storage", "currency": "EUR", "interval": "year", "usage_type": "metered",
		"tiers_mode": "volume", "tiers": []map[string]any{{"up_to": nil, "unit_amount": "0.25", "flat_amount": "2.50"}}})
	hiddenTierCheckPlan(t, "POST /v1/plans (volume, one tier)", vol, "volume", hiddenTierView{nil, 25, 250})

	free := hiddenTierCreatePlan(t, e, map[string]any{"name": "Free start", "currency": "GBP", "interval": "month", "usage_type": "metered",
		"tiers_mode": "graduated", "tiers": []map[string]any{{"up_to": 100, "unit_amount": "0"}, {"up_to": nil, "unit_amount": "1.25"}}})
	hiddenTierCheckPlan(t, "POST /v1/plans (a free first tier)", free, "graduated", hiddenTierView{hiddenTierN(100), 0, 0}, hiddenTierView{nil, 125, 0})

	var got hiddenTierPlan
	e.MustDo(http.StatusOK, "GET", "/v1/plans/"+grad.ID, e.Acme.Key, nil, &got)
	hiddenTierCheckPlan(t, "GET /v1/plans/{id}", got, "graduated", hiddenTierAPICallsTiers...)
	var list struct {
		Items []hiddenTierPlan `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/plans", e.Acme.Key, nil, &list)
	found := 0
	for _, p := range list.Items {
		switch p.ID {
		case grad.ID:
			hiddenTierCheckPlan(t, "GET /v1/plans (graduated)", p, "graduated", hiddenTierAPICallsTiers...)
			found++
		case vol.ID:
			hiddenTierCheckPlan(t, "GET /v1/plans (volume)", p, "volume", hiddenTierView{nil, 25, 250})
			found++
		}
	}
	if found != 2 {
		t.Fatalf("GET /v1/plans lists %d of the two tiered plans: %+v", found, list.Items)
	}
}

func TestHiddenTieredPlanValidation(t *testing.T) {
	e := apitest.New(t)
	tier := func(upTo any, amounts ...string) map[string]any {
		m := map[string]any{"up_to": upTo, "unit_amount": "0.01"}
		if len(amounts) > 0 {
			m["unit_amount"] = amounts[0]
		}
		if len(amounts) > 1 {
			m["flat_amount"] = amounts[1]
		}
		return m
	}
	body := func(change func(map[string]any)) map[string]any {
		b := map[string]any{"name": "API", "currency": "USD", "interval": "month", "usage_type": "metered", "tiers_mode": "graduated",
			"tiers": []map[string]any{tier(100), tier(nil)}}
		change(b)
		return b
	}
	withTiers := func(tiers ...map[string]any) func(map[string]any) {
		return func(b map[string]any) {
			if tiers == nil {
				tiers = []map[string]any{}
			}
			b["tiers"] = tiers
		}
	}
	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"no tiers", body(withTiers())},
		{"up_to not ascending", body(withTiers(tier(100), tier(50), tier(nil)))},
		{"up_to repeated", body(withTiers(tier(100), tier(100), tier(nil)))},
		{"up_to 0", body(withTiers(tier(0), tier(nil)))},
		{"negative up_to", body(withTiers(tier(-5), tier(nil)))},
		{"the last tier has an upper limit", body(withTiers(tier(100), tier(200)))},
		{"the only tier has an upper limit", body(withTiers(tier(100)))},
		{"a tier without upper limit before the last", body(withTiers(tier(nil), tier(100)))},
		{"two tiers without upper limit", body(withTiers(tier(100), tier(nil), tier(nil)))},
		{"negative unit_amount", body(withTiers(tier(100, "-0.01"), tier(nil)))},
		{"negative flat_amount", body(withTiers(tier(100, "0.01", "-1.00"), tier(nil)))},
		{"unknown tiers_mode", body(func(b map[string]any) { b["tiers_mode"] = "stairstep" })},
		{"no tiers_mode", body(func(b map[string]any) { delete(b, "tiers_mode") })},
		{"empty tiers_mode", body(func(b map[string]any) { b["tiers_mode"] = "" })},
		{"tiers on a licensed plan", body(func(b map[string]any) { b["usage_type"] = "licensed" })},
		{"tiers on a plan without usage_type (licensed)", body(func(b map[string]any) { delete(b, "usage_type") })},
	} {
		if r := e.Do("POST", "/v1/plans", e.Acme.Key, c.body); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid_tiers" {
			raw, _ := json.Marshal(c.body)
			t.Errorf("POST /v1/plans with %s: want 422 {\"error\":\"invalid_tiers\"}, got %s\nbody: %s", c.name, r, raw)
		}
	}
	// Amounts that cannot be read are refused as invalid tiers or as an invalid amount; an up_to that is not a whole number
	// is refused as invalid tiers or as a body that does not decode.
	for _, c := range []struct {
		name string
		body map[string]any
		ok   func(r *apitest.Response) bool
	}{
		{"unit_amount that is not a number", body(withTiers(tier(100, "abc"), tier(nil))), hiddenTier422},
		{"unit_amount with too many decimals", body(withTiers(tier(100, "0.001"), tier(nil))), hiddenTier422},
		{"up_to that is not a whole number", body(withTiers(tier(1.5), tier(nil))), func(r *apitest.Response) bool {
			return r.Code == http.StatusBadRequest || (r.Code == http.StatusUnprocessableEntity && r.Map(t)["error"] == "invalid_tiers")
		}},
	} {
		if r := e.Do("POST", "/v1/plans", e.Acme.Key, c.body); !c.ok(r) {
			raw, _ := json.Marshal(c.body)
			t.Errorf("POST /v1/plans with %s: want it refused (422 invalid_tiers), got %s\nbody: %s", c.name, r, raw)
		}
	}
	var list struct {
		Items []hiddenTierPlan `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/plans", e.Acme.Key, nil, &list)
	if len(list.Items) != 0 {
		t.Fatalf("refused requests created plans: %+v", list.Items)
	}
	// The same body with valid tiers is accepted, so the refusals above were about the tiers.
	valid := hiddenTierCreatePlan(t, e, body(func(map[string]any) {}))
	hiddenTierCheckPlan(t, "POST /v1/plans with valid tiers", valid, "graduated", hiddenTierView{hiddenTierN(100), 1, 0}, hiddenTierView{nil, 1, 0})
}

func hiddenTier422(r *apitest.Response) bool {
	if r.Code != http.StatusUnprocessableEntity {
		return false
	}
	var m map[string]any
	if json.Unmarshal(r.Body, &m) != nil {
		return false
	}
	return m["error"] == "invalid_tiers" || m["error"] == "invalid_amount"
}

type hiddenTierInvoice struct {
	ID     string `json:"id"`
	Total  int64  `json:"total"`
	Status string `json:"status"`
	Lines  []struct {
		Description string `json:"description"`
		Quantity    int64  `json:"quantity"`
		UnitAmount  int64  `json:"unit_amount"`
	} `json:"lines"`
}

func TestHiddenTieredRenewalBillsUsage(t *testing.T) {
	e := apitest.New(t) // 2026-01-15 10:00 UTC; monthly periods end on the 15th at 10:00
	ctx := context.Background()
	grad := hiddenTierCreatePlan(t, e, hiddenTierAPICalls("API calls", "graduated"))
	vol := hiddenTierCreatePlan(t, e, hiddenTierAPICalls("API volume", "volume"))
	subscribe := func(email, planID string) string {
		var c, s struct {
			ID string `json:"id"`
		}
		e.MustDo(http.StatusCreated, "POST", "/v1/customers", e.Acme.Key, map[string]any{"name": email, "email": email}, &c)
		e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": planID}, &s)
		return s.ID
	}
	subGrad := subscribe("graduated@customers.example", grad.ID)
	subVol := subscribe("volume@customers.example", vol.ID)
	subIdle := subscribe("idle@customers.example", grad.ID)
	use := func(subID string, q int64, at time.Time) {
		e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions/"+subID+"/usage", e.Acme.Key, map[string]any{"quantity": q, "timestamp": at}, nil)
	}
	end := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	use(subGrad, 10000, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC))
	use(subGrad, 2345, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	use(subGrad, 99, end) // belongs to the next period
	use(subVol, 12345, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	use(subIdle, 50, end) // only in the next period

	invoices := func(subID string) []hiddenTierInvoice {
		var page struct {
			Items []hiddenTierInvoice `json:"items"`
		}
		e.MustDo(http.StatusOK, "GET", "/v1/invoices?limit=100&subscription_id="+subID, e.Acme.Key, nil, &page)
		return page.Items
	}
	checkLast := func(when, subID string, count int, planName string, amount int64) {
		t.Helper()
		invs := invoices(subID)
		if len(invs) != count {
			t.Fatalf("%s: %d invoices, want %d: %+v", when, len(invs), count, invs)
		}
		inv := invs[count-1]
		if len(inv.Lines) != 1 || inv.Lines[0].Quantity != 1 || inv.Lines[0].UnitAmount != amount ||
			!strings.HasPrefix(inv.Lines[0].Description, planName) || inv.Total != amount {
			t.Fatalf("%s: invoice %+v, want one line of quantity 1 and unit_amount %d (total %d) whose description starts with %q",
				when, inv, amount, amount, planName)
		}
	}
	noInvoices := func(when, subID string, want int) {
		t.Helper()
		if invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: subID}); len(invs) != want {
			t.Fatalf("%s: %d invoices, want %d (a period without usage is not invoiced): %+v", when, len(invs), want, invs)
		}
	}
	noInvoices("before the first renewal", subGrad, 0)
	noInvoices("before the first renewal", subVol, 0)

	e.Clock.Set(end)
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Fatalf("RenewDue: %v", err)
	}
	// 12,345 units: graduated 1000*5 + 9000*4 + 500 + 2345*3 + 1000 = 49535; volume 12345*3 + 1000 = 38035
	checkLast("the graduated plan's renewal for 12,345 units", subGrad, 1, "API calls", 49535)
	checkLast("the volume plan's renewal for 12,345 units", subVol, 1, "API volume", 38035)
	noInvoices("the renewal of a period without usage", subIdle, 0)
	for _, id := range []string{subGrad, subVol, subIdle} {
		var sub struct {
			CurrentPeriodStart time.Time `json:"current_period_start"`
		}
		e.MustDo(http.StatusOK, "GET", "/v1/subscriptions/"+id, e.Acme.Key, nil, &sub)
		if !sub.CurrentPeriodStart.Equal(end) {
			t.Fatalf("subscription %s did not renew: its current period starts %s, want %s", id, sub.CurrentPeriodStart.Format(time.RFC3339), end.Format(time.RFC3339))
		}
	}

	e.Clock.Set(time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC))
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Fatalf("RenewDue: %v", err)
	}
	checkLast("the graduated plan's second renewal, 99 units", subGrad, 2, "API calls", 495)
	noInvoices("the volume plan's second renewal, without usage", subVol, 1)
	checkLast("the second renewal of the subscription that had no usage before, 50 units", subIdle, 1, "API calls", 250)
}

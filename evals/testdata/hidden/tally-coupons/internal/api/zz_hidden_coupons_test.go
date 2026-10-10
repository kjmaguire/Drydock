package api_test

// Hidden acceptance test of the trial task tally-coupons (evals/tasks). The agents never see it.
//
// Everything goes through the HTTP API, with e.Clock and e.Subs.RenewDue for renewals. Renewals always run exactly when a
// period ends, on mid-month dates. Coupon codes are compared ignoring case (the prompt does not say whether a code keeps the
// case it was created with).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // apitest's organizations use IANA time zones; do not depend on the machine's zoneinfo

	"example.com/tally/internal/apitest"
)

type hiddenCouponView struct {
	ID               string     `json:"id"`
	Code             string     `json:"code"`
	PercentOff       *int       `json:"percent_off"`
	AmountOff        *int64     `json:"amount_off"`
	Currency         string     `json:"currency"`
	Duration         string     `json:"duration"`
	DurationInMonths *int       `json:"duration_in_months"`
	MaxRedemptions   *int       `json:"max_redemptions"`
	RedeemBy         *time.Time `json:"redeem_by"`
	TimesRedeemed    *int       `json:"times_redeemed"`
}

func (c hiddenCouponView) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

type hiddenCouponInvoice struct {
	ID          string     `json:"id"`
	Coupon      string     `json:"coupon"`
	Subtotal    int64      `json:"subtotal"`
	Discount    int64      `json:"discount"`
	Tax         int64      `json:"tax"`
	Total       int64      `json:"total"`
	Status      string     `json:"status"`
	PeriodStart *time.Time `json:"period_start"`
}

func hiddenCouponAt(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

func hiddenCouponCreate(t *testing.T, e *apitest.Env, key string, body map[string]any) hiddenCouponView {
	t.Helper()
	var c hiddenCouponView
	e.MustDo(http.StatusCreated, "POST", "/v1/coupons", key, body, &c)
	return c
}

func hiddenCouponGet(t *testing.T, e *apitest.Env, key, code string) hiddenCouponView {
	t.Helper()
	var c hiddenCouponView
	e.MustDo(http.StatusOK, "GET", "/v1/coupons/"+code, key, nil, &c)
	return c
}

func hiddenCouponTimesRedeemed(t *testing.T, e *apitest.Env, key, code string, want int) {
	t.Helper()
	c := hiddenCouponGet(t, e, key, code)
	if c.TimesRedeemed == nil || *c.TimesRedeemed != want {
		t.Fatalf("GET /v1/coupons/%s: times_redeemed %v, want %d\n%v", code, hiddenCouponInt(c.TimesRedeemed), want, c)
	}
}

func hiddenCouponInt(p *int) any {
	if p == nil {
		return "missing"
	}
	return *p
}

func hiddenCouponCustomer(t *testing.T, e *apitest.Env, key, email, pm string) string {
	t.Helper()
	body := map[string]any{"name": "Coupon " + email, "email": email}
	if pm != "" {
		body["default_payment_method"] = pm
	}
	var c struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/customers", key, body, &c)
	return c.ID
}

func hiddenCouponPlan(t *testing.T, e *apitest.Env, key string, body map[string]any) string {
	t.Helper()
	if body["interval"] == nil {
		body["interval"] = "month"
	}
	var p struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", key, body, &p)
	return p.ID
}

func hiddenCouponSubscribe(t *testing.T, e *apitest.Env, key, customerID, planID string, quantity, discount int) string {
	t.Helper()
	var s struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", key,
		map[string]any{"customer_id": customerID, "plan_id": planID, "quantity": quantity, "discount_percent": discount}, &s)
	return s.ID
}

func hiddenCouponApply(e *apitest.Env, key, subID, code string) *apitest.Response {
	return e.Do("POST", "/v1/subscriptions/"+subID+"/coupon", key, map[string]string{"code": code})
}

// hiddenCouponMustApply applies a coupon and expects 200 with the subscription showing the coupon.
func hiddenCouponMustApply(t *testing.T, e *apitest.Env, key, subID, code string) {
	t.Helper()
	r := hiddenCouponApply(e, key, subID, code)
	var sub struct {
		ID     string `json:"id"`
		Coupon string `json:"coupon"`
	}
	if r.Code == http.StatusOK {
		r.JSON(t, &sub)
	}
	if r.Code != http.StatusOK || sub.ID != subID || !strings.EqualFold(sub.Coupon, code) {
		t.Fatalf("POST /v1/subscriptions/%s/coupon {\"code\": %q}: want 200 with the subscription showing \"coupon\": %q, got %s", subID, code, code, r)
	}
	var got struct {
		Coupon string `json:"coupon"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/subscriptions/"+subID, key, nil, &got)
	if !strings.EqualFold(got.Coupon, code) {
		t.Fatalf("GET /v1/subscriptions/%s after applying %s: \"coupon\" is %q", subID, code, got.Coupon)
	}
}

// hiddenCouponRefused expects an application of a coupon to be refused with this status and, when errCode is set, error.
func hiddenCouponRefused(t *testing.T, e *apitest.Env, key, subID, code string, status int, errCode, why string) {
	t.Helper()
	r := hiddenCouponApply(e, key, subID, code)
	if r.Code != status || (errCode != "" && r.Map(t)["error"] != errCode) {
		want := fmt.Sprint(status)
		if errCode != "" {
			want += fmt.Sprintf(` {"error":%q}`, errCode)
		}
		t.Fatalf("applying %s (%s): want %s, got %s", code, why, want, r)
	}
}

func hiddenCouponRenewAt(t *testing.T, e *apitest.Env, at time.Time) {
	t.Helper()
	e.Clock.Set(at)
	if _, err := e.Subs.RenewDue(context.Background()); err != nil {
		t.Fatalf("RenewDue at %s: %v", at.Format(time.RFC3339), err)
	}
}

func hiddenCouponInvoices(t *testing.T, e *apitest.Env, key, subID string) []hiddenCouponInvoice {
	t.Helper()
	var page struct {
		Items []hiddenCouponInvoice `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices?limit=100&subscription_id="+subID, key, nil, &page)
	return page.Items
}

// hiddenCouponInvoiceFor returns the subscription's invoice for the period that starts at start.
func hiddenCouponInvoiceFor(t *testing.T, e *apitest.Env, key, subID string, start time.Time) hiddenCouponInvoice {
	t.Helper()
	var found []hiddenCouponInvoice
	invs := hiddenCouponInvoices(t, e, key, subID)
	for _, inv := range invs {
		if inv.PeriodStart != nil && inv.PeriodStart.Equal(start) {
			found = append(found, inv)
		}
	}
	if len(found) != 1 {
		t.Fatalf("subscription %s: want one invoice for the period starting %s, found %d among %+v", subID, start.Format(time.RFC3339), len(found), invs)
	}
	return found[0]
}

// hiddenCouponCheck checks an invoice's coupon and totals.
func hiddenCouponCheck(t *testing.T, when string, inv hiddenCouponInvoice, coupon string, subtotal, discount, tax, total int64) {
	t.Helper()
	couponOK := strings.EqualFold(inv.Coupon, coupon) && (coupon != "" || inv.Coupon == "")
	if !couponOK || inv.Subtotal != subtotal || inv.Discount != discount || inv.Tax != tax || inv.Total != total {
		t.Fatalf("%s: invoice %s shows coupon %q, subtotal %d, discount %d, tax %d, total %d; want coupon %q, subtotal %d, discount %d, tax %d, total %d",
			when, inv.ID, inv.Coupon, inv.Subtotal, inv.Discount, inv.Tax, inv.Total, coupon, subtotal, discount, tax, total)
	}
}

func TestHiddenCouponCreate(t *testing.T) {
	e := apitest.New(t)
	redeemBy := hiddenCouponAt(2026, 6, 30, 23, 59, 59)
	pct := hiddenCouponCreate(t, e, e.Acme.Key, map[string]any{"code": "Summer-25_x", "percent_off": 25, "duration": "repeating",
		"duration_in_months": 3, "max_redemptions": 5, "redeem_by": redeemBy.Format(time.RFC3339)})
	if !strings.EqualFold(pct.Code, "Summer-25_x") || pct.PercentOff == nil || *pct.PercentOff != 25 || pct.Duration != "repeating" ||
		pct.DurationInMonths == nil || *pct.DurationInMonths != 3 || pct.MaxRedemptions == nil || *pct.MaxRedemptions != 5 ||
		pct.RedeemBy == nil || !pct.RedeemBy.Equal(redeemBy) || pct.TimesRedeemed == nil || *pct.TimesRedeemed != 0 {
		t.Fatalf("POST /v1/coupons (percent_off 25, repeating 3 months, max 5, redeem_by): the answer is %v", pct)
	}
	amt := hiddenCouponCreate(t, e, e.Acme.Key, map[string]any{"code": "TENOFF", "amount_off": "10.50", "currency": "USD", "duration": "once"})
	if !strings.EqualFold(amt.Code, "TENOFF") || amt.AmountOff == nil || *amt.AmountOff != 1050 || amt.Currency != "USD" || amt.Duration != "once" ||
		amt.TimesRedeemed == nil || *amt.TimesRedeemed != 0 {
		t.Fatalf("POST /v1/coupons (amount_off \"10.50\" USD, once): the answer is %v, want amount_off 1050 (minor units) and times_redeemed 0", amt)
	}
	long := strings.Repeat("A", 15) + "-" + strings.Repeat("9", 15) + "_" // 32 characters
	valid := []map[string]any{
		{"code": "abc", "percent_off": 100, "duration": "forever"},
		{"code": long, "percent_off": 1, "duration": "repeating", "duration_in_months": 36},
		{"code": "EUR-5", "amount_off": "5", "currency": "EUR", "duration": "repeating", "duration_in_months": 1},
	}
	for _, body := range valid {
		c := hiddenCouponCreate(t, e, e.Acme.Key, body)
		if !strings.EqualFold(c.Code, body["code"].(string)) {
			t.Fatalf("POST /v1/coupons %v: the answer's code is %q", body, c.Code)
		}
	}
	if c := hiddenCouponGet(t, e, e.Acme.Key, "EUR-5"); c.AmountOff == nil || *c.AmountOff != 500 || c.Currency != "EUR" {
		t.Fatalf("a coupon of \"5\" EUR off: %v, want amount_off 500 and currency EUR", c)
	}

	for _, body := range []map[string]any{
		{"code": "SUMMER-25_X", "percent_off": 10, "duration": "once"},
		{"code": "tenoff", "amount_off": "1.00", "currency": "USD", "duration": "once"},
	} {
		if r := e.Do("POST", "/v1/coupons", e.Acme.Key, body); r.Code != http.StatusConflict || r.Map(t)["error"] != "code_taken" {
			t.Fatalf("POST /v1/coupons with the code %q (taken, ignoring case): want 409 {\"error\":\"code_taken\"}, got %s", body["code"], r)
		}
	}

	ok := map[string]any{"code": "VALID", "percent_off": 10, "duration": "once"}
	with := func(change map[string]any, drop ...string) map[string]any {
		b := map[string]any{}
		for k, v := range ok {
			b[k] = v
		}
		for _, k := range drop {
			delete(b, k)
		}
		for k, v := range change {
			b[k] = v
		}
		return b
	}
	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"a code of 2 characters", with(map[string]any{"code": "ab"})},
		{"a code of 33 characters", with(map[string]any{"code": long + "Z"})},
		{"a code with a space", with(map[string]any{"code": "has space"})},
		{"a code with a dot", with(map[string]any{"code": "a.b.c"})},
		{"a code with an exclamation mark", with(map[string]any{"code": "bad!"})},
		{"an empty code", with(map[string]any{"code": ""})},
		{"no code", with(nil, "code")},
		{"neither percent_off nor amount_off", with(nil, "percent_off")},
		{"both percent_off and amount_off", with(map[string]any{"amount_off": "1.00", "currency": "USD"})},
		{"percent_off 0", with(map[string]any{"percent_off": 0})},
		{"percent_off 101", with(map[string]any{"percent_off": 101})},
		{"percent_off -5", with(map[string]any{"percent_off": -5})},
		{"amount_off without currency", with(map[string]any{"amount_off": "1.00"}, "percent_off")},
		{"amount_off in an unknown currency", with(map[string]any{"amount_off": "1.00", "currency": "XYZ"}, "percent_off")},
		{"amount_off that is not an amount", with(map[string]any{"amount_off": "abc", "currency": "USD"}, "percent_off")},
		{"a negative amount_off", with(map[string]any{"amount_off": "-1.00", "currency": "USD"}, "percent_off")},
		{"no duration", with(nil, "duration")},
		{"an unknown duration", with(map[string]any{"duration": "weekly"})},
		{"repeating without duration_in_months", with(map[string]any{"duration": "repeating"})},
		{"repeating for 0 months", with(map[string]any{"duration": "repeating", "duration_in_months": 0})},
		{"repeating for 37 months", with(map[string]any{"duration": "repeating", "duration_in_months": 37})},
		{"once with duration_in_months", with(map[string]any{"duration": "once", "duration_in_months": 3})},
		{"forever with duration_in_months", with(map[string]any{"duration": "forever", "duration_in_months": 12})},
		{"max_redemptions -1", with(map[string]any{"max_redemptions": -1})},
	} {
		if r := e.Do("POST", "/v1/coupons", e.Acme.Key, c.body); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid" {
			raw, _ := json.Marshal(c.body)
			t.Errorf("POST /v1/coupons with %s: want 422 {\"error\":\"invalid\"}, got %s\nbody: %s", c.name, r, raw)
		}
	}
	// A redeem_by that is not a time is refused; a body field that does not decode may also be answered 400.
	if r := e.Do("POST", "/v1/coupons", e.Acme.Key, with(map[string]any{"redeem_by": "next tuesday"})); !(r.Code == http.StatusBadRequest ||
		(r.Code == http.StatusUnprocessableEntity && r.Map(t)["error"] == "invalid")) {
		t.Errorf("POST /v1/coupons with redeem_by \"next tuesday\": want 422 {\"error\":\"invalid\"}, got %s", r)
	}

	var list struct {
		Items []hiddenCouponView `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/coupons", e.Acme.Key, nil, &list)
	// They were all created at the same time, so their order is not checked here.
	var codes []string
	for _, c := range list.Items {
		codes = append(codes, strings.ToUpper(c.Code))
	}
	want := []string{"SUMMER-25_X", "TENOFF", "ABC", strings.ToUpper(long), "EUR-5"}
	sort.Strings(codes)
	sort.Strings(want)
	if strings.Join(codes, " ") != strings.Join(want, " ") {
		t.Fatalf("GET /v1/coupons after the refused requests: codes %v, want exactly the five created: %v", codes, want)
	}
}

func TestHiddenCouponListAndLookup(t *testing.T) {
	e := apitest.New(t)
	start := e.Clock.Now()
	for i, c := range []struct {
		org  apitest.Org
		code string
		pct  int
	}{
		{e.Acme, "ZETA", 11}, {e.Globex, "Globex1", 12}, {e.Acme, "alpha", 13}, {e.Globex, "zeta", 14}, {e.Acme, "Mid", 15},
	} {
		e.Clock.Set(start.Add(time.Duration(i) * time.Minute))
		hiddenCouponCreate(t, e, c.org.Key, map[string]any{"code": c.code, "percent_off": c.pct, "duration": "forever"})
	}
	check := func(org apitest.Org, name string, want ...string) {
		t.Helper()
		var list struct {
			Items []hiddenCouponView `json:"items"`
		}
		e.MustDo(http.StatusOK, "GET", "/v1/coupons", org.Key, nil, &list)
		var got []string
		for _, c := range list.Items {
			got = append(got, strings.ToLower(c.Code))
		}
		if strings.Join(got, " ") != strings.ToLower(strings.Join(want, " ")) {
			t.Fatalf("GET /v1/coupons as %s: codes %v, want %v (the organization's own, oldest first)", name, got, want)
		}
	}
	check(e.Acme, "Acme", "ZETA", "alpha", "Mid")
	check(e.Globex, "Globex", "Globex1", "zeta")
	for _, c := range []struct {
		code, want string
		pct        int
	}{{"zeta", "ZETA", 11}, {"MID", "Mid", 15}, {"Alpha", "alpha", 13}} {
		got := hiddenCouponGet(t, e, e.Acme.Key, c.code)
		if !strings.EqualFold(got.Code, c.want) || got.PercentOff == nil || *got.PercentOff != c.pct {
			t.Fatalf("GET /v1/coupons/%s as Acme: %v, want Acme's coupon %s (percent_off %d)", c.code, got, c.want, c.pct)
		}
	}
	if got := hiddenCouponGet(t, e, e.Globex.Key, "ZETA"); got.PercentOff == nil || *got.PercentOff != 14 {
		t.Fatalf("GET /v1/coupons/ZETA as Globex: %v, want Globex's zeta (percent_off 14)", got)
	}
	for _, code := range []string{"globex1", "nope"} {
		if r := e.Do("GET", "/v1/coupons/"+code, e.Acme.Key, nil); r.Code != http.StatusNotFound {
			t.Fatalf("GET /v1/coupons/%s as Acme (no such coupon in the organization): want 404, got %s", code, r)
		}
	}
}

func TestHiddenCouponApplyRefusals(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	cust := hiddenCouponCustomer(t, e, key, "refusals@acme-customers.example", "")
	usd := hiddenCouponPlan(t, e, key, map[string]any{"name": "Team", "currency": "USD", "price": "20.00"})
	eur := hiddenCouponPlan(t, e, key, map[string]any{"name": "Euro Team", "currency": "EUR", "price": "20.00"})
	subs := map[string]string{}
	for _, name := range []string{"usd1", "usd2", "usd3", "usd4", "usd5", "usd6", "usd7", "usd8"} {
		subs[name] = hiddenCouponSubscribe(t, e, key, cust, usd, 1, 0)
	}
	subs["eur1"] = hiddenCouponSubscribe(t, e, key, cust, eur, 1, 0)
	subs["eur2"] = hiddenCouponSubscribe(t, e, key, cust, eur, 1, 0)
	gcust := hiddenCouponCustomer(t, e, e.Globex.Key, "refusals@globex-customers.example", "")
	gplan := hiddenCouponPlan(t, e, e.Globex.Key, map[string]any{"name": "Globex Team", "currency": "USD", "price": "20.00"})
	gsub := hiddenCouponSubscribe(t, e, e.Globex.Key, gcust, gplan, 1, 0)

	now := e.Clock.Now()
	hiddenCouponCreate(t, e, key, map[string]any{"code": "SHORT", "percent_off": 10, "duration": "forever", "redeem_by": now.Add(time.Hour).Format(time.RFC3339)})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "TWICE", "percent_off": 10, "duration": "forever", "max_redemptions": 2})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "EUROFF", "amount_off": "5.00", "currency": "EUR", "duration": "forever"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "FIVEOFF", "amount_off": "5.00", "currency": "USD", "duration": "forever"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "PCT10", "percent_off": 10, "duration": "forever"})
	hiddenCouponCreate(t, e, e.Globex.Key, map[string]any{"code": "GLOBEXONLY", "percent_off": 10, "duration": "forever"})

	hiddenCouponRefused(t, e, key, subs["usd1"], "NOPE", http.StatusNotFound, "not_found", "no such coupon")
	hiddenCouponRefused(t, e, key, subs["usd1"], "GLOBEXONLY", http.StatusNotFound, "not_found", "another organization's coupon")
	hiddenCouponRefused(t, e, key, gsub, "PCT10", http.StatusNotFound, "not_found", "to another organization's subscription")
	hiddenCouponRefused(t, e, key, "sub_999999", "PCT10", http.StatusNotFound, "not_found", "to a subscription that does not exist")

	e.Clock.Set(now.Add(time.Hour - time.Second))
	hiddenCouponMustApply(t, e, key, subs["usd1"], "SHORT")
	e.Clock.Set(now.Add(time.Hour + time.Second))
	hiddenCouponRefused(t, e, key, subs["usd2"], "SHORT", http.StatusUnprocessableEntity, "coupon_expired", "after its redeem_by")

	hiddenCouponMustApply(t, e, key, subs["usd3"], "TWICE")
	hiddenCouponMustApply(t, e, key, subs["usd4"], "TWICE")
	hiddenCouponRefused(t, e, key, subs["usd5"], "TWICE", http.StatusUnprocessableEntity, "coupon_exhausted", "max_redemptions 2, already on two subscriptions")

	hiddenCouponRefused(t, e, key, subs["usd6"], "EUROFF", http.StatusUnprocessableEntity, "currency_mismatch", "EUR off a USD plan")
	hiddenCouponMustApply(t, e, key, subs["eur1"], "EUROFF")
	hiddenCouponMustApply(t, e, key, subs["eur2"], "PCT10") // a percentage works in any currency

	hiddenCouponMustApply(t, e, key, subs["usd7"], "PCT10")
	hiddenCouponRefused(t, e, key, subs["usd7"], "FIVEOFF", http.StatusConflict, "coupon_already_applied", "to a subscription that has a coupon")
	hiddenCouponRefused(t, e, key, subs["usd7"], "PCT10", http.StatusConflict, "coupon_already_applied", "again to the same subscription")

	e.MustDo(http.StatusOK, "POST", "/v1/subscriptions/"+subs["usd8"]+"/cancel", key, nil, nil)
	hiddenCouponRefused(t, e, key, subs["usd8"], "PCT10", http.StatusConflict, "", "to a canceled subscription")

	// a refused application leaves the subscription without a coupon
	hiddenCouponMustApply(t, e, key, subs["usd2"], "PCT10")
	hiddenCouponMustApply(t, e, key, subs["usd5"], "PCT10")
	hiddenCouponMustApply(t, e, key, subs["usd6"], "FIVEOFF")

	for code, n := range map[string]int{"SHORT": 1, "TWICE": 2, "EUROFF": 1, "FIVEOFF": 1, "PCT10": 4} {
		hiddenCouponTimesRedeemed(t, e, key, code, n)
	}
	hiddenCouponTimesRedeemed(t, e, e.Globex.Key, "GLOBEXONLY", 0)
}

func TestHiddenCouponDurations(t *testing.T) {
	e := apitest.New(t) // 2026-01-15 10:00 UTC: periods start on the 15th at 10:00
	key := e.Acme.Key
	team := hiddenCouponPlan(t, e, key, map[string]any{"name": "Team", "currency": "USD", "price": "20.00"})
	plain := hiddenCouponCustomer(t, e, key, "plain@acme-customers.example", "")
	card := hiddenCouponCustomer(t, e, key, "card@acme-customers.example", "pm_card_visa")
	once := hiddenCouponSubscribe(t, e, key, plain, team, 1, 10)
	forever := hiddenCouponSubscribe(t, e, key, card, team, 1, 10)
	repeating := hiddenCouponSubscribe(t, e, key, plain, team, 1, 0)
	hiddenCouponCreate(t, e, key, map[string]any{"code": "HALFONCE", "percent_off": 50, "duration": "once"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "QUARTER", "percent_off": 25, "duration": "forever"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "TWOMONTHS", "percent_off": 20, "duration": "repeating", "duration_in_months": 2})

	e.Clock.Set(hiddenCouponAt(2026, 1, 20, 12, 0, 0))
	hiddenCouponMustApply(t, e, key, once, "HALFONCE")
	hiddenCouponMustApply(t, e, key, forever, "QUARTER")
	hiddenCouponMustApply(t, e, key, repeating, "TWOMONTHS")

	// Invoices created before the coupon was applied keep their discount; another invoice of the same customer that is not
	// the subscription's is not discounted, and does not use up the coupon that lasts once.
	first := hiddenCouponAt(2026, 1, 15, 10, 0, 0)
	hiddenCouponCheck(t, "the first invoice, from before the coupon", hiddenCouponInvoiceFor(t, e, key, once, first), "", 2000, 200, 0, 1800)
	hiddenCouponCheck(t, "the first invoice, from before the coupon", hiddenCouponInvoiceFor(t, e, key, forever, first), "", 2000, 200, 0, 1800)
	e.Clock.Set(hiddenCouponAt(2026, 1, 25, 9, 0, 0))
	var manual hiddenCouponInvoice
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", key, map[string]any{"customer_id": plain, "currency": "USD",
		"lines": []map[string]any{{"description": "Setup", "quantity": 1, "unit_amount": 1000}}}, &manual)
	hiddenCouponCheck(t, "a manual invoice of the customer, after the coupons were applied", manual, "", 1000, 0, 0, 1000)
	var got hiddenCouponInvoice
	e.MustDo(http.StatusOK, "GET", "/v1/invoices/"+manual.ID, key, nil, &got)
	hiddenCouponCheck(t, "GET /v1/invoices/{id} of the manual invoice", got, "", 1000, 0, 0, 1000)

	months := []time.Time{hiddenCouponAt(2026, 2, 15, 10, 0, 0), hiddenCouponAt(2026, 3, 15, 10, 0, 0), hiddenCouponAt(2026, 4, 15, 10, 0, 0)}
	for _, at := range months {
		hiddenCouponRenewAt(t, e, at)
	}
	for i, at := range months {
		when := fmt.Sprintf("the renewal of %s", at.Format("2006-01-02"))
		inv := hiddenCouponInvoiceFor(t, e, key, once, at)
		if i == 0 {
			// the first invoice after the coupon: 50% instead of the subscription's 10%
			hiddenCouponCheck(t, when+" with HALFONCE (once)", inv, "HALFONCE", 2000, 1000, 0, 1000)
		} else {
			// the coupon is used up: the subscription's discount_percent applies again
			hiddenCouponCheck(t, when+" after HALFONCE (once) was used", inv, "", 2000, 200, 0, 1800)
		}
		inv = hiddenCouponInvoiceFor(t, e, key, forever, at)
		hiddenCouponCheck(t, when+" with QUARTER (forever)", inv, "QUARTER", 2000, 500, 0, 1500)
		if inv.Status != "paid" {
			t.Fatalf("%s with QUARTER: the invoice is %q, want paid (the customer has a card)", when, inv.Status)
		}
		var got hiddenCouponInvoice
		e.MustDo(http.StatusOK, "GET", "/v1/invoices/"+inv.ID, key, nil, &got)
		hiddenCouponCheck(t, "GET /v1/invoices/{id}, "+when+" with QUARTER", got, "QUARTER", 2000, 500, 0, 1500)
		inv = hiddenCouponInvoiceFor(t, e, key, repeating, at)
		if i < 2 {
			// redeemed on January 20 at 12:00: the periods of February 15 and March 15 start less than 2 months later
			hiddenCouponCheck(t, when+" with TWOMONTHS (repeating, 2 months)", inv, "TWOMONTHS", 2000, 400, 0, 1600)
		} else {
			hiddenCouponCheck(t, when+" after TWOMONTHS (repeating, 2 months) ended", inv, "", 2000, 0, 0, 2000)
		}
	}
	var amounts []int64
	for _, c := range e.Gateway.Charges() {
		amounts = append(amounts, c.Amount)
	}
	if fmt.Sprint(amounts) != "[1800 1500 1500 1500]" {
		t.Fatalf("charges to the card %v, want [1800 1500 1500 1500] (the discounted totals)", amounts)
	}
}

func TestHiddenCouponRepeatingWindowBoundary(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	e.Clock.Set(hiddenCouponAt(2026, 1, 1, 10, 0, 0))
	plan := hiddenCouponPlan(t, e, key, map[string]any{"name": "Trial Team", "currency": "USD", "price": "20.00", "trial_days": 45})
	cust := hiddenCouponCustomer(t, e, key, "boundary@acme-customers.example", "")
	exact := hiddenCouponSubscribe(t, e, key, cust, plan, 1, 0) // trialing until 2026-02-15 10:00
	later := hiddenCouponSubscribe(t, e, key, cust, plan, 1, 0)
	hiddenCouponCreate(t, e, key, map[string]any{"code": "BOUNDARY", "percent_off": 50, "duration": "repeating", "duration_in_months": 2})
	// Two months after 2026-01-15 10:00:00 is exactly when the period of March 15 starts: that period is not discounted.
	e.Clock.Set(hiddenCouponAt(2026, 1, 15, 10, 0, 0))
	hiddenCouponMustApply(t, e, key, exact, "BOUNDARY")
	// One second later, the period of March 15 starts less than two months after the redemption: it is discounted.
	e.Clock.Set(hiddenCouponAt(2026, 1, 15, 10, 0, 1))
	hiddenCouponMustApply(t, e, key, later, "BOUNDARY")
	starts := []time.Time{hiddenCouponAt(2026, 2, 15, 10, 0, 0), hiddenCouponAt(2026, 3, 15, 10, 0, 0), hiddenCouponAt(2026, 4, 15, 10, 0, 0)}
	for _, at := range starts {
		hiddenCouponRenewAt(t, e, at)
	}
	for i, at := range starts {
		discounted := map[string]bool{exact: i == 0, later: i <= 1}
		for _, s := range []struct{ name, id string }{{"redeemed 2026-01-15T10:00:00Z", exact}, {"redeemed 2026-01-15T10:00:01Z", later}} {
			inv := hiddenCouponInvoiceFor(t, e, key, s.id, at)
			when := fmt.Sprintf("the period starting %s, BOUNDARY (repeating, 2 months) %s", at.Format(time.RFC3339), s.name)
			if discounted[s.id] {
				hiddenCouponCheck(t, when, inv, "BOUNDARY", 2000, 1000, 0, 1000)
			} else {
				hiddenCouponCheck(t, when, inv, "", 2000, 0, 0, 2000)
			}
		}
	}
}

func TestHiddenCouponDiscountArithmetic(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	e.MustDo(http.StatusOK, "PATCH", "/v1/org", key, map[string]any{"tax_rate_bp": 825}, nil)
	team := hiddenCouponPlan(t, e, key, map[string]any{"name": "Team", "currency": "USD", "price": "20.00"})
	odd := hiddenCouponPlan(t, e, key, map[string]any{"name": "Odd", "currency": "USD", "price": "19.90"})
	cust := hiddenCouponCustomer(t, e, key, "arithmetic@acme-customers.example", "")
	fiveOne := hiddenCouponSubscribe(t, e, key, cust, team, 1, 20)  // subtotal 2000, its own discount 20%
	fiveThree := hiddenCouponSubscribe(t, e, key, cust, team, 3, 0) // subtotal 6000
	capped := hiddenCouponSubscribe(t, e, key, cust, team, 1, 0)    // subtotal 2000
	rounding := hiddenCouponSubscribe(t, e, key, cust, odd, 1, 10)  // subtotal 1990, its own discount 10%
	hiddenCouponCreate(t, e, key, map[string]any{"code": "FIVE", "amount_off": "5.00", "currency": "USD", "duration": "forever"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "BIG", "amount_off": "50.00", "currency": "USD", "duration": "forever"})
	hiddenCouponCreate(t, e, key, map[string]any{"code": "QUARTER25", "percent_off": 25, "duration": "forever"})
	e.Clock.Set(hiddenCouponAt(2026, 1, 20, 12, 0, 0))
	hiddenCouponMustApply(t, e, key, fiveOne, "FIVE")
	hiddenCouponMustApply(t, e, key, fiveThree, "FIVE")
	hiddenCouponMustApply(t, e, key, capped, "BIG")
	hiddenCouponMustApply(t, e, key, rounding, "QUARTER25")
	renewal := hiddenCouponAt(2026, 2, 15, 10, 0, 0)
	hiddenCouponRenewAt(t, e, renewal)
	// 5.00 off 2000 (the subscription's 20% does not apply); tax 8.25% of 1500 = 123.75 -> 124
	hiddenCouponCheck(t, "FIVE (5.00 off) on 2000", hiddenCouponInvoiceFor(t, e, key, fiveOne, renewal), "FIVE", 2000, 500, 124, 1624)
	// 5.00 off 6000; tax 8.25% of 5500 = 453.75 -> 454
	hiddenCouponCheck(t, "FIVE (5.00 off) on 6000", hiddenCouponInvoiceFor(t, e, key, fiveThree, renewal), "FIVE", 6000, 500, 454, 5954)
	// 50.00 off 2000 takes off the subtotal and no more
	hiddenCouponCheck(t, "BIG (50.00 off) on 2000", hiddenCouponInvoiceFor(t, e, key, capped, renewal), "BIG", 2000, 2000, 0, 0)
	// 25% of 1990 = 497.5 -> 498 (the subscription's 10% does not apply); tax 8.25% of 1492 = 123.09 -> 123
	hiddenCouponCheck(t, "QUARTER25 (25%) on 1990", hiddenCouponInvoiceFor(t, e, key, rounding, renewal), "QUARTER25", 1990, 498, 123, 1615)
}

func TestHiddenCouponOrganizationsAreSeparate(t *testing.T) {
	e := apitest.New(t)
	acust := hiddenCouponCustomer(t, e, e.Acme.Key, "isolation@acme-customers.example", "")
	aplan := hiddenCouponPlan(t, e, e.Acme.Key, map[string]any{"name": "Acme Team", "currency": "USD", "price": "20.00"})
	gcust := hiddenCouponCustomer(t, e, e.Globex.Key, "isolation@globex-customers.example", "")
	gplan := hiddenCouponPlan(t, e, e.Globex.Key, map[string]any{"name": "Globex Team", "currency": "USD", "price": "20.00"})
	asub := hiddenCouponSubscribe(t, e, e.Acme.Key, acust, aplan, 1, 0)
	asub2 := hiddenCouponSubscribe(t, e, e.Acme.Key, acust, aplan, 1, 0)
	gsub := hiddenCouponSubscribe(t, e, e.Globex.Key, gcust, gplan, 1, 0)
	gsub2 := hiddenCouponSubscribe(t, e, e.Globex.Key, gcust, gplan, 1, 0)
	hiddenCouponCreate(t, e, e.Acme.Key, map[string]any{"code": "SHARED", "percent_off": 10, "duration": "forever"})
	hiddenCouponCreate(t, e, e.Globex.Key, map[string]any{"code": "shared", "percent_off": 30, "duration": "forever"})
	hiddenCouponCreate(t, e, e.Acme.Key, map[string]any{"code": "ACMEONLY", "percent_off": 15, "duration": "forever"})
	hiddenCouponCreate(t, e, e.Acme.Key, map[string]any{"code": "LIMIT1", "percent_off": 5, "duration": "forever", "max_redemptions": 1})
	hiddenCouponCreate(t, e, e.Globex.Key, map[string]any{"code": "LIMIT1", "percent_off": 5, "duration": "forever", "max_redemptions": 1})
	e.Clock.Set(hiddenCouponAt(2026, 1, 20, 12, 0, 0))
	hiddenCouponRefused(t, e, e.Globex.Key, gsub, "ACMEONLY", http.StatusNotFound, "not_found", "Acme's code by Globex")
	hiddenCouponMustApply(t, e, e.Acme.Key, asub, "SHARED")
	hiddenCouponMustApply(t, e, e.Globex.Key, gsub, "SHARED")
	hiddenCouponMustApply(t, e, e.Acme.Key, asub2, "LIMIT1")
	hiddenCouponMustApply(t, e, e.Globex.Key, gsub2, "LIMIT1") // Acme's redemption does not count for Globex's coupon
	hiddenCouponTimesRedeemed(t, e, e.Acme.Key, "SHARED", 1)
	hiddenCouponTimesRedeemed(t, e, e.Globex.Key, "SHARED", 1)
	hiddenCouponTimesRedeemed(t, e, e.Acme.Key, "ACMEONLY", 0)
	if c := hiddenCouponGet(t, e, e.Globex.Key, "SHARED"); c.PercentOff == nil || *c.PercentOff != 30 {
		t.Fatalf("GET /v1/coupons/SHARED as Globex: %v, want Globex's coupon (percent_off 30)", c)
	}
	renewal := hiddenCouponAt(2026, 2, 15, 10, 0, 0)
	hiddenCouponRenewAt(t, e, renewal)
	hiddenCouponCheck(t, "Acme's SHARED (10%)", hiddenCouponInvoiceFor(t, e, e.Acme.Key, asub, renewal), "SHARED", 2000, 200, 0, 1800)
	hiddenCouponCheck(t, "Globex's SHARED (30%)", hiddenCouponInvoiceFor(t, e, e.Globex.Key, gsub, renewal), "SHARED", 2000, 600, 0, 1400)
}

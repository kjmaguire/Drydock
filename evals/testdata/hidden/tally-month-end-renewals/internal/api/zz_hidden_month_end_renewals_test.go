package api_test

// Hidden acceptance test of the trial task tally-month-end-renewals (evals/tasks). The agents never see it.
//
// Everything goes through the HTTP API, e.Subs.RenewDue and e.Clock. A period is written "start to end" in RFC 3339 (UTC),
// so a failure shows the periods expected and the periods that came back.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // apitest's organizations use IANA time zones; do not depend on the machine's zoneinfo

	"example.com/tally/internal/apitest"
)

type hiddenMERSub struct {
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
	CurrentPeriodStart time.Time  `json:"current_period_start"`
	CurrentPeriodEnd   time.Time  `json:"current_period_end"`
	TrialEnd           *time.Time `json:"trial_end"`
}

type hiddenMERInvoice struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	PeriodStart *time.Time `json:"period_start"`
	PeriodEnd   *time.Time `json:"period_end"`
	Lines       []struct {
		Description string `json:"description"`
		Quantity    int64  `json:"quantity"`
		UnitAmount  int64  `json:"unit_amount"`
	} `json:"lines"`
}

func hiddenMERAt(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

func hiddenMERFmt(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func hiddenMERPeriod(start, end time.Time) string {
	return hiddenMERFmt(start) + " to " + hiddenMERFmt(end)
}

// hiddenMERPeriods turns boundaries b0, b1, ..., bn into the periods [b0, b1], [b1, b2], ...
func hiddenMERPeriods(bounds []time.Time) []string {
	var out []string
	for i := 0; i+1 < len(bounds); i++ {
		out = append(out, hiddenMERPeriod(bounds[i], bounds[i+1]))
	}
	return out
}

type hiddenMEROpts struct {
	interval      string // "month" or "year"
	usageType     string // "" (licensed) or "metered"
	trialDays     int
	paymentMethod string // the customer's default payment method, "" for none
	email         string // the customer's email (unique per subscription of a test)
}

// hiddenMERSubscribe sets the clock to at and creates, through the API, an Acme customer, a 10.00 USD plan and a
// subscription of the customer to the plan.
func hiddenMERSubscribe(t *testing.T, e *apitest.Env, at time.Time, o hiddenMEROpts) hiddenMERSub {
	t.Helper()
	e.Clock.Set(at)
	if o.interval == "" {
		o.interval = "month"
	}
	if o.email == "" {
		o.email = "month-end@customers.example"
	}
	cust := map[string]any{"name": "Month End Customer", "email": o.email}
	if o.paymentMethod != "" {
		cust["default_payment_method"] = o.paymentMethod
	}
	var c struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/customers", e.Acme.Key, cust, &c)
	plan := map[string]any{"name": "Team " + o.email, "currency": "USD", "price": "10.00", "interval": o.interval, "trial_days": o.trialDays}
	if o.usageType != "" {
		plan["usage_type"] = o.usageType
	}
	var p struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, plan, &p)
	var sub hiddenMERSub
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": p.ID, "quantity": 1}, &sub)
	return sub
}

// hiddenMERRenewAt sets the clock to at and runs RenewDue.
func hiddenMERRenewAt(t *testing.T, e *apitest.Env, at time.Time) {
	t.Helper()
	e.Clock.Set(at)
	if _, err := e.Subs.RenewDue(context.Background()); err != nil {
		t.Fatalf("RenewDue at %s: %v", hiddenMERFmt(at), err)
	}
}

func hiddenMERGetSub(t *testing.T, e *apitest.Env, id string) hiddenMERSub {
	t.Helper()
	var sub hiddenMERSub
	e.MustDo(http.StatusOK, "GET", "/v1/subscriptions/"+id, e.Acme.Key, nil, &sub)
	return sub
}

func hiddenMERInvoices(t *testing.T, e *apitest.Env, subID string) []hiddenMERInvoice {
	t.Helper()
	var page struct {
		Items []hiddenMERInvoice `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices?limit=100&subscription_id="+subID, e.Acme.Key, nil, &page)
	return page.Items
}

// hiddenMERCheckSub checks the subscription's current period (through GET /v1/subscriptions/{id}).
func hiddenMERCheckSub(t *testing.T, e *apitest.Env, id, when string, start, end time.Time) hiddenMERSub {
	t.Helper()
	sub := hiddenMERGetSub(t, e, id)
	if got, want := hiddenMERPeriod(sub.CurrentPeriodStart, sub.CurrentPeriodEnd), hiddenMERPeriod(start, end); got != want {
		t.Fatalf("%s: the subscription's current period is %s, want %s", when, got, want)
	}
	return sub
}

// hiddenMERCheckInvoices checks that the subscription's invoices, oldest first, are exactly one per period of want, in order.
func hiddenMERCheckInvoices(t *testing.T, e *apitest.Env, subID, when string, want []string) []hiddenMERInvoice {
	t.Helper()
	invs := hiddenMERInvoices(t, e, subID)
	got := make([]string, 0, len(invs))
	for _, inv := range invs {
		if inv.PeriodStart == nil || inv.PeriodEnd == nil {
			got = append(got, fmt.Sprintf("invoice %s without a period", inv.ID))
			continue
		}
		got = append(got, hiddenMERPeriod(*inv.PeriodStart, *inv.PeriodEnd))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s: the subscription's invoices (oldest first) cover the periods\n  %s\nwant one invoice per period, in order:\n  %s",
			when, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	return invs
}

// hiddenMERStepThrough creates a subscription at bounds[0] and runs RenewDue one second before and then exactly at each later
// boundary, checking the current period and the invoices after every step.
func hiddenMERStepThrough(t *testing.T, interval string, bounds []time.Time) {
	t.Helper()
	e := apitest.New(t)
	sub := hiddenMERSubscribe(t, e, bounds[0], hiddenMEROpts{interval: interval})
	if got, want := hiddenMERPeriod(sub.CurrentPeriodStart, sub.CurrentPeriodEnd), hiddenMERPeriod(bounds[0], bounds[1]); got != want {
		t.Fatalf("POST /v1/subscriptions at %s: the first period is %s, want %s", hiddenMERFmt(bounds[0]), got, want)
	}
	all := hiddenMERPeriods(bounds)
	hiddenMERCheckInvoices(t, e, sub.ID, "after the subscription was created", all[:1])
	for i := 1; i+1 < len(bounds); i++ {
		before := bounds[i].Add(-time.Second)
		hiddenMERRenewAt(t, e, before)
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue one second before the period ends ("+hiddenMERFmt(before)+")", bounds[i-1], bounds[i])
		hiddenMERRenewAt(t, e, bounds[i])
		when := "RenewDue at " + hiddenMERFmt(bounds[i])
		hiddenMERCheckSub(t, e, sub.ID, when, bounds[i], bounds[i+1])
		hiddenMERCheckInvoices(t, e, sub.ID, when, all[:i+1])
	}
}

func TestHiddenMonthEndMonthlyAnchors(t *testing.T) {
	at := hiddenMERAt
	for _, c := range []struct {
		name   string
		bounds []time.Time // the anchor, then where each later period starts
	}{
		{"31st through a common year into a leap February", []time.Time{
			at(2027, 1, 31, 9, 30, 15), at(2027, 2, 28, 9, 30, 15), at(2027, 3, 31, 9, 30, 15), at(2027, 4, 30, 9, 30, 15),
			at(2027, 5, 31, 9, 30, 15), at(2027, 6, 30, 9, 30, 15), at(2027, 7, 31, 9, 30, 15), at(2027, 8, 31, 9, 30, 15),
			at(2027, 9, 30, 9, 30, 15), at(2027, 10, 31, 9, 30, 15), at(2027, 11, 30, 9, 30, 15), at(2027, 12, 31, 9, 30, 15),
			at(2028, 1, 31, 9, 30, 15), at(2028, 2, 29, 9, 30, 15), at(2028, 3, 31, 9, 30, 15), at(2028, 4, 30, 9, 30, 15)}},
		{"31st of January in a leap year", []time.Time{
			at(2028, 1, 31, 2, 30, 0), at(2028, 2, 29, 2, 30, 0), at(2028, 3, 31, 2, 30, 0), at(2028, 4, 30, 2, 30, 0)}},
		{"31st of August across the new year", []time.Time{
			at(2026, 8, 31, 18, 45, 30), at(2026, 9, 30, 18, 45, 30), at(2026, 10, 31, 18, 45, 30), at(2026, 11, 30, 18, 45, 30),
			at(2026, 12, 31, 18, 45, 30), at(2027, 1, 31, 18, 45, 30), at(2027, 2, 28, 18, 45, 30), at(2027, 3, 31, 18, 45, 30)}},
		{"31st of December into a leap year", []time.Time{
			at(2027, 12, 31, 12, 0, 0), at(2028, 1, 31, 12, 0, 0), at(2028, 2, 29, 12, 0, 0), at(2028, 3, 31, 12, 0, 0)}},
		{"30th", []time.Time{
			at(2027, 1, 30, 23, 59, 59), at(2027, 2, 28, 23, 59, 59), at(2027, 3, 30, 23, 59, 59), at(2027, 4, 30, 23, 59, 59),
			at(2027, 5, 30, 23, 59, 59)}},
		{"30th in a leap year", []time.Time{
			at(2028, 1, 30, 7, 7, 7), at(2028, 2, 29, 7, 7, 7), at(2028, 3, 30, 7, 7, 7), at(2028, 4, 30, 7, 7, 7)}},
		{"30th of April stays the 30th", []time.Time{
			at(2027, 4, 30, 14, 5, 0), at(2027, 5, 30, 14, 5, 0), at(2027, 6, 30, 14, 5, 0), at(2027, 7, 30, 14, 5, 0)}},
		{"29th", []time.Time{
			at(2027, 1, 29, 0, 0, 0), at(2027, 2, 28, 0, 0, 0), at(2027, 3, 29, 0, 0, 0), at(2027, 4, 29, 0, 0, 0)}},
		{"29th in a leap year", []time.Time{
			at(2028, 1, 29, 16, 20, 0), at(2028, 2, 29, 16, 20, 0), at(2028, 3, 29, 16, 20, 0)}},
		{"29th of February, monthly", []time.Time{
			at(2028, 2, 29, 11, 0, 0), at(2028, 3, 29, 11, 0, 0), at(2028, 4, 29, 11, 0, 0), at(2028, 5, 29, 11, 0, 0)}},
		{"28th of February stays the 28th", []time.Time{
			at(2027, 2, 28, 6, 0, 0), at(2027, 3, 28, 6, 0, 0), at(2027, 4, 28, 6, 0, 0)}},
	} {
		t.Run(c.name, func(t *testing.T) { hiddenMERStepThrough(t, "month", c.bounds) })
	}
}

func TestHiddenMonthEndYearlyLeapDay(t *testing.T) {
	at := hiddenMERAt
	bounds := []time.Time{at(2028, 2, 29, 12, 34, 56), at(2029, 2, 28, 12, 34, 56), at(2030, 2, 28, 12, 34, 56),
		at(2031, 2, 28, 12, 34, 56), at(2032, 2, 29, 12, 34, 56), at(2033, 2, 28, 12, 34, 56)}
	t.Run("one renewal at a time", func(t *testing.T) { hiddenMERStepThrough(t, "year", bounds) })
	t.Run("four years behind", func(t *testing.T) {
		e := apitest.New(t)
		sub := hiddenMERSubscribe(t, e, bounds[0], hiddenMEROpts{interval: "year"})
		hiddenMERRenewAt(t, e, at(2032, 3, 15, 8, 0, 0))
		when := "RenewDue first run on 2032-03-15, four renewals late"
		hiddenMERCheckSub(t, e, sub.ID, when, bounds[4], bounds[5])
		hiddenMERCheckInvoices(t, e, sub.ID, when, hiddenMERPeriods(bounds))
	})
}

func TestHiddenMonthEndTrialEndIsTheAnchor(t *testing.T) {
	at := hiddenMERAt
	// A 14-day trial from January 17 ends on January 31: that is the anchor, not the day the subscription started.
	started := at(2027, 1, 17, 8, 15, 0)
	bounds := []time.Time{at(2027, 1, 31, 8, 15, 0), at(2027, 2, 28, 8, 15, 0), at(2027, 3, 31, 8, 15, 0), at(2027, 4, 30, 8, 15, 0)}
	t.Run("one renewal at a time", func(t *testing.T) {
		e := apitest.New(t)
		sub := hiddenMERSubscribe(t, e, started, hiddenMEROpts{trialDays: 14})
		if sub.Status != "trialing" || sub.TrialEnd == nil || !sub.TrialEnd.Equal(bounds[0]) || !sub.CurrentPeriodEnd.Equal(bounds[0]) {
			t.Fatalf("a 14-day trial from %s: status %q, trial_end %v, current_period_end %s; want trialing until %s",
				hiddenMERFmt(started), sub.Status, sub.TrialEnd, hiddenMERFmt(sub.CurrentPeriodEnd), hiddenMERFmt(bounds[0]))
		}
		hiddenMERCheckInvoices(t, e, sub.ID, "during the trial", nil)
		hiddenMERRenewAt(t, e, bounds[0])
		got := hiddenMERCheckSub(t, e, sub.ID, "RenewDue at the trial's end", bounds[0], bounds[1])
		if got.Status != "active" {
			t.Fatalf("after the trial: status %q, want active", got.Status)
		}
		all := hiddenMERPeriods(bounds)
		hiddenMERCheckInvoices(t, e, sub.ID, "RenewDue at the trial's end", all[:1])
		for i := 1; i+1 < len(bounds); i++ {
			hiddenMERRenewAt(t, e, bounds[i])
			when := "RenewDue at " + hiddenMERFmt(bounds[i])
			hiddenMERCheckSub(t, e, sub.ID, when, bounds[i], bounds[i+1])
			hiddenMERCheckInvoices(t, e, sub.ID, when, all[:i+1])
		}
	})
	t.Run("three renewals behind", func(t *testing.T) {
		e := apitest.New(t)
		sub := hiddenMERSubscribe(t, e, started, hiddenMEROpts{trialDays: 14})
		hiddenMERRenewAt(t, e, at(2027, 4, 5, 9, 0, 0))
		when := "RenewDue first run on 2027-04-05, after the trial ended on 2027-01-31"
		got := hiddenMERCheckSub(t, e, sub.ID, when, bounds[2], bounds[3])
		if got.Status != "active" {
			t.Fatalf("%s: status %q, want active", when, got.Status)
		}
		hiddenMERCheckInvoices(t, e, sub.ID, when, hiddenMERPeriods(bounds))
	})
	t.Run("30-day trial ending on January 31 of a leap year", func(t *testing.T) {
		e := apitest.New(t)
		sub := hiddenMERSubscribe(t, e, at(2028, 1, 1, 0, 0, 0), hiddenMEROpts{trialDays: 30})
		leap := []time.Time{at(2028, 1, 31, 0, 0, 0), at(2028, 2, 29, 0, 0, 0), at(2028, 3, 31, 0, 0, 0), at(2028, 4, 30, 0, 0, 0)}
		for i := 0; i+1 < len(leap); i++ {
			hiddenMERRenewAt(t, e, leap[i])
			hiddenMERCheckSub(t, e, sub.ID, "RenewDue at "+hiddenMERFmt(leap[i]), leap[i], leap[i+1])
		}
		hiddenMERCheckInvoices(t, e, sub.ID, "after three renewals", hiddenMERPeriods(leap))
	})
}

func TestHiddenMonthEndLateRenewal(t *testing.T) {
	at := hiddenMERAt
	t.Run("mid-month anchor", func(t *testing.T) {
		e := apitest.New(t)
		anchor := at(2026, 1, 15, 10, 0, 0)
		sub := hiddenMERSubscribe(t, e, anchor, hiddenMEROpts{})
		bounds := []time.Time{anchor, at(2026, 2, 15, 10, 0, 0), at(2026, 3, 15, 10, 0, 0), at(2026, 4, 15, 10, 0, 0), at(2026, 5, 15, 10, 0, 0)}
		all := hiddenMERPeriods(bounds)
		hiddenMERRenewAt(t, e, at(2026, 2, 20, 16, 20, 0)) // five days late
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue run late on 2026-02-20", bounds[1], bounds[2])
		hiddenMERCheckInvoices(t, e, sub.ID, "RenewDue run late on 2026-02-20", all[:2])
		hiddenMERRenewAt(t, e, bounds[2]) // on time
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue on time on 2026-03-15", bounds[2], bounds[3])
		hiddenMERRenewAt(t, e, at(2026, 4, 16, 9, 59, 59)) // almost a day late
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue run late on 2026-04-16", bounds[3], bounds[4])
		hiddenMERCheckInvoices(t, e, sub.ID, "after three renewals", all)
	})
	t.Run("anchor on the 31st", func(t *testing.T) {
		e := apitest.New(t)
		bounds := []time.Time{at(2027, 1, 31, 10, 0, 0), at(2027, 2, 28, 10, 0, 0), at(2027, 3, 31, 10, 0, 0), at(2027, 4, 30, 10, 0, 0)}
		sub := hiddenMERSubscribe(t, e, bounds[0], hiddenMEROpts{})
		all := hiddenMERPeriods(bounds)
		hiddenMERRenewAt(t, e, at(2027, 3, 3, 10, 0, 0)) // where AddDate would have put the renewal
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue run late on 2027-03-03", bounds[1], bounds[2])
		hiddenMERCheckInvoices(t, e, sub.ID, "RenewDue run late on 2027-03-03", all[:2])
		hiddenMERRenewAt(t, e, at(2027, 4, 2, 7, 0, 0))
		hiddenMERCheckSub(t, e, sub.ID, "RenewDue run late on 2027-04-02", bounds[2], bounds[3])
		hiddenMERCheckInvoices(t, e, sub.ID, "RenewDue run late on 2027-04-02", all)
	})
}

func TestHiddenMonthEndCatchUpMissedPeriods(t *testing.T) {
	at := hiddenMERAt
	t.Run("licensed, paid by card, two subscriptions behind", func(t *testing.T) {
		e := apitest.New(t)
		a := []time.Time{at(2027, 1, 31, 10, 0, 0), at(2027, 2, 28, 10, 0, 0), at(2027, 3, 31, 10, 0, 0), at(2027, 4, 30, 10, 0, 0), at(2027, 5, 31, 10, 0, 0)}
		b := []time.Time{at(2027, 2, 15, 13, 0, 0), at(2027, 3, 15, 13, 0, 0), at(2027, 4, 15, 13, 0, 0), at(2027, 5, 15, 13, 0, 0), at(2027, 6, 15, 13, 0, 0)}
		subA := hiddenMERSubscribe(t, e, a[0], hiddenMEROpts{paymentMethod: "pm_card_visa", email: "a@customers.example"})
		subB := hiddenMERSubscribe(t, e, b[0], hiddenMEROpts{paymentMethod: "pm_card_visa", email: "b@customers.example"})
		if n := len(e.Gateway.Charges()); n != 2 {
			t.Fatalf("after creating two subscriptions with a card: %d charges, want 2", n)
		}
		run := at(2027, 5, 20, 12, 0, 0) // the job did not run from January to May 20
		hiddenMERRenewAt(t, e, run)
		when := "RenewDue first run on 2027-05-20"
		if got := hiddenMERCheckSub(t, e, subA.ID, when+" (subscription A, anchor January 31)", a[3], a[4]); got.Status != "active" {
			t.Fatalf("%s: subscription A has status %q, want active", when, got.Status)
		}
		if got := hiddenMERCheckSub(t, e, subB.ID, when+" (subscription B, anchor February 15)", b[3], b[4]); got.Status != "active" {
			t.Fatalf("%s: subscription B has status %q, want active", when, got.Status)
		}
		for _, s := range []struct {
			name   string
			id     string
			bounds []time.Time
		}{{"A", subA.ID, a}, {"B", subB.ID, b}} {
			invs := hiddenMERCheckInvoices(t, e, s.id, when+" (subscription "+s.name+")", hiddenMERPeriods(s.bounds))
			for _, inv := range invs {
				if inv.Status != "paid" {
					t.Fatalf("%s: invoice %s of subscription %s for %s is %q, want paid (the customer has a card)",
						when, inv.ID, s.name, hiddenMERPeriod(*inv.PeriodStart, *inv.PeriodEnd), inv.Status)
				}
			}
		}
		if n := len(e.Gateway.Charges()); n != 8 {
			t.Fatalf("%s: %d charges in all, want 8 (one per invoice)", when, n)
		}
		hiddenMERRenewAt(t, e, run) // nothing more is due
		hiddenMERCheckInvoices(t, e, subA.ID, "RenewDue run again at the same time", hiddenMERPeriods(a))
		hiddenMERCheckInvoices(t, e, subB.ID, "RenewDue run again at the same time", hiddenMERPeriods(b))
		if n := len(e.Gateway.Charges()); n != 8 {
			t.Fatalf("RenewDue run again at the same time: %d charges, want still 8", n)
		}
		next := at(2027, 6, 30, 10, 0, 0)
		hiddenMERRenewAt(t, e, a[4])
		hiddenMERCheckSub(t, e, subA.ID, "RenewDue on time on 2027-05-31", a[4], next)
	})
	t.Run("metered, each period bills its own usage", func(t *testing.T) {
		e := apitest.New(t)
		bounds := []time.Time{at(2027, 1, 31, 10, 0, 0), at(2027, 2, 28, 10, 0, 0), at(2027, 3, 31, 10, 0, 0), at(2027, 4, 30, 10, 0, 0), at(2027, 5, 31, 10, 0, 0)}
		sub := hiddenMERSubscribe(t, e, bounds[0], hiddenMEROpts{usageType: "metered"})
		for _, u := range []struct {
			q  int64
			at time.Time
		}{
			{5, at(2027, 2, 1, 0, 0, 0)},
			{7, at(2027, 3, 15, 0, 0, 0)},
			{11, at(2027, 4, 30, 9, 59, 59)},
			{13, at(2027, 4, 30, 10, 0, 0)}, // belongs to the period that starts then
		} {
			e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions/"+sub.ID+"/usage", e.Acme.Key, map[string]any{"quantity": u.q, "timestamp": u.at}, nil)
		}
		when := "RenewDue first run on 2027-05-02"
		hiddenMERRenewAt(t, e, at(2027, 5, 2, 0, 0, 0))
		hiddenMERCheckSub(t, e, sub.ID, when, bounds[3], bounds[4])
		invs := hiddenMERCheckInvoices(t, e, sub.ID, when, hiddenMERPeriods(bounds[:4]))
		for i, want := range []int64{5, 7, 11} {
			var got int64
			for _, l := range invs[i].Lines {
				got += l.Quantity
			}
			if len(invs[i].Lines) != 1 || got != want {
				t.Fatalf("%s: the invoice for %s bills %+v, want one line for the %d units used in that period",
					when, hiddenMERPeriod(bounds[i], bounds[i+1]), invs[i].Lines, want)
			}
		}
	})
}

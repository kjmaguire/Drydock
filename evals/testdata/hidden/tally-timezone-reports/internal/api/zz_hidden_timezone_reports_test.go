package api_test

// Hidden acceptance test of the trial task tally-timezone-reports (evals/tasks). The agents never see it.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

type hiddenTZBucket struct {
	Start    string           `json:"start"`
	Amounts  map[string]int64 `json:"amounts"`
	Payments int              `json:"payments"`
}

type hiddenTZReport struct {
	TimeZone string           `json:"time_zone"`
	Group    string           `json:"group"`
	Buckets  []hiddenTZBucket `json:"buckets"`
}

// hiddenTZPay is a payment made at an instant (RFC 3339, UTC).
type hiddenTZPay struct {
	at     string
	amount int64
	cur    money.Currency
	failed bool
}

// hiddenTZOrg adds an organization in a time zone with one customer and one invoice, and records the payments of that
// invoice through the store at the times given.
func hiddenTZOrg(t *testing.T, e *apitest.Env, name, zone string, pays []hiddenTZPay) apitest.Org {
	t.Helper()
	org := e.NewOrg(name, store.OrgTeam, zone)
	c, err := e.Store.CreateCustomer(org.ID, "Customer of "+name, "billing@"+strings.ToLower(name)+".example", e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := e.Store.CreateInvoice(store.Invoice{OrgID: org.ID, CustomerID: c.ID, Currency: money.USD,
		Lines: []store.Line{{Description: "Services", Quantity: 1, UnitAmount: 100}}}, e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pays {
		at, err := time.Parse(time.RFC3339, p.at)
		if err != nil {
			t.Fatal(err)
		}
		status := store.PaymentSucceeded
		if p.failed {
			status = store.PaymentFailed
		}
		if _, err := e.Store.CreatePayment(store.Payment{OrgID: org.ID, InvoiceID: inv.ID, Amount: p.amount, Currency: p.cur, Status: status}, at); err != nil {
			t.Fatal(err)
		}
	}
	return org
}

// hiddenTZShort is a response's status and the start of its body, for failure messages.
func hiddenTZShort(r *apitest.Response) string {
	if s := r.String(); len(s) > 700 {
		return s[:700] + "..."
	}
	return r.String()
}

// hiddenTZGet asks for a report and fails the test unless it answers 200.
func hiddenTZGet(t *testing.T, e *apitest.Env, org apitest.Org, query string) (hiddenTZReport, *apitest.Response) {
	t.Helper()
	r := e.Do("GET", "/v1/reports/revenue?"+query, org.Key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/reports/revenue?%s (%s): want 200, got %s", query, org.TimeZone, hiddenTZShort(r))
	}
	var rep hiddenTZReport
	r.JSON(t, &rep)
	return rep, r
}

func hiddenTZFormat(bs []hiddenTZBucket) string {
	var parts []string
	for _, b := range bs {
		amounts := "null"
		if b.Amounts != nil {
			amounts = fmt.Sprint(b.Amounts)
		}
		parts = append(parts, fmt.Sprintf("{%s %s %d}", b.Start, amounts, b.Payments))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// hiddenTZWant checks a report's time zone, group and buckets (a nil Amounts in want is {}).
func hiddenTZWant(t *testing.T, e *apitest.Env, org apitest.Org, query, group string, want []hiddenTZBucket) {
	t.Helper()
	rep, r := hiddenTZGet(t, e, org, query)
	if rep.TimeZone != org.TimeZone || rep.Group != group {
		t.Fatalf("GET /v1/reports/revenue?%s: time_zone %q, group %q; want %q, %q\n%s", query, rep.TimeZone, rep.Group, org.TimeZone, group, hiddenTZShort(r))
	}
	ok := len(rep.Buckets) == len(want)
	for i := 0; ok && i < len(want); i++ {
		g, w := rep.Buckets[i], want[i]
		ok = g.Start == w.Start && g.Payments == w.Payments && g.Amounts != nil && len(g.Amounts) == len(w.Amounts)
		for cur, amount := range w.Amounts {
			if got, has := g.Amounts[cur]; !has || got != amount {
				ok = false
			}
		}
	}
	if !ok {
		t.Fatalf("GET /v1/reports/revenue?%s (%s):\n got buckets %s\nwant buckets %s\n(amounts {} is an empty object, not null)\nbody: %s",
			query, org.TimeZone, hiddenTZFormat(rep.Buckets), hiddenTZFormat(want), hiddenTZShort(r))
	}
}

func hiddenTZError(t *testing.T, e *apitest.Env, org apitest.Org, query, code string) {
	t.Helper()
	r := e.Do("GET", "/v1/reports/revenue"+query, org.Key, nil)
	if r.Code != http.StatusBadRequest || r.Map(t)["error"] != code {
		t.Errorf("GET /v1/reports/revenue%s: want 400 {\"error\":%q}, got %s", query, code, hiddenTZShort(r))
	}
}

// New York's 23-hour day (daylight saving time starts on Sunday March 8, 2026) and its 25-hour day (it ends on Sunday
// November 1, 2026), with payments a minute either side of local midnight.
func TestHiddenRevenueReportDaysInNewYork(t *testing.T) {
	e := apitest.New(t)
	spring := hiddenTZOrg(t, e, "Spring", "America/New_York", []hiddenTZPay{
		{"2026-03-07T04:59:00Z", 100, money.USD, false},  // Fri March 6, 23:59 EST: before from
		{"2026-03-07T05:01:00Z", 200, money.USD, false},  // Sat March 7, 00:01 EST
		{"2026-03-08T04:59:00Z", 300, money.USD, false},  // Sat March 7, 23:59 EST
		{"2026-03-08T05:01:00Z", 400, money.USD, false},  // Sun March 8, 00:01 EST
		{"2026-03-08T07:30:00Z", 50, money.EUR, false},   // Sun March 8, 03:30 EDT
		{"2026-03-08T12:00:00Z", 999, money.USD, true},   // failed
		{"2026-03-09T03:59:00Z", 500, money.USD, false},  // Sun March 8, 23:59 EDT
		{"2026-03-09T04:00:00Z", 600, money.USD, false},  // Mon March 9, 00:00 EDT
		{"2026-03-09T04:01:00Z", 700, money.USD, false},  // Mon March 9, 00:01 EDT
		{"2026-03-09T16:00:00Z", 10, money.JPY, false},   // Mon March 9, 12:00 EDT
		{"2026-03-12T03:59:00Z", 800, money.USD, false},  // Wed March 11, 23:59 EDT
		{"2026-03-12T04:00:00Z", 1000, money.USD, false}, // Thu March 12, 00:00 EDT: after to
		{"2026-03-12T04:01:00Z", 900, money.USD, false},  // Thu March 12, 00:01 EDT: after to
	})
	// another organization's payment at the same time is not counted
	hiddenTZOrg(t, e, "Neighbor", "America/New_York", []hiddenTZPay{{"2026-03-08T12:00:00Z", 777, money.USD, false}})

	march := []hiddenTZBucket{
		{"2026-03-07", map[string]int64{"USD": 500}, 2},
		{"2026-03-08", map[string]int64{"USD": 900, "EUR": 50}, 3},
		{"2026-03-09", map[string]int64{"USD": 1300, "JPY": 10}, 3},
		{"2026-03-10", nil, 0},
		{"2026-03-11", map[string]int64{"USD": 800}, 1},
	}
	hiddenTZWant(t, e, spring, "from=2026-03-07&to=2026-03-11", "day", march)
	hiddenTZWant(t, e, spring, "from=2026-03-07&to=2026-03-11&group=day", "day", march)
	hiddenTZWant(t, e, spring, "from=2026-03-08&to=2026-03-08", "day", march[1:2])

	autumn := hiddenTZOrg(t, e, "Autumn", "America/New_York", []hiddenTZPay{
		{"2026-10-31T03:59:00Z", 1, money.USD, false},    // Fri October 30, 23:59 EDT: before from
		{"2026-10-31T04:01:00Z", 2, money.USD, false},    // Sat October 31, 00:01 EDT
		{"2026-11-01T03:59:00Z", 4, money.USD, false},    // Sat October 31, 23:59 EDT
		{"2026-11-01T04:01:00Z", 8, money.USD, false},    // Sun November 1, 00:01 EDT
		{"2026-11-01T05:30:00Z", 16, money.USD, false},   // Sun November 1, 01:30 EDT
		{"2026-11-01T06:30:00Z", 32, money.USD, false},   // Sun November 1, 01:30 EST (the hour repeats)
		{"2026-11-02T04:30:00Z", 64, money.USD, false},   // Sun November 1, 23:30 EST
		{"2026-11-02T04:59:00Z", 128, money.USD, false},  // Sun November 1, 23:59 EST
		{"2026-11-02T05:00:00Z", 256, money.USD, false},  // Mon November 2, 00:00 EST
		{"2026-11-03T04:59:00Z", 512, money.USD, false},  // Mon November 2, 23:59 EST
		{"2026-11-03T05:01:00Z", 1024, money.USD, false}, // Tue November 3, 00:01 EST: after to
	})
	hiddenTZWant(t, e, autumn, "from=2026-10-31&to=2026-11-02", "day", []hiddenTZBucket{
		{"2026-10-31", map[string]int64{"USD": 6}, 2},
		{"2026-11-01", map[string]int64{"USD": 248}, 5},
		{"2026-11-02", map[string]int64{"USD": 768}, 2},
	})
}

// Kathmandu is 5 hours 45 minutes ahead of UTC; Lord Howe Island moves its clocks by 30 minutes (on Sunday April 5, 2026,
// a day of 24.5 hours, and on Sunday October 4, 2026, a day of 23.5 hours).
func TestHiddenRevenueReportOddOffsets(t *testing.T) {
	e := apitest.New(t)
	kathmandu := hiddenTZOrg(t, e, "Himalaya", "Asia/Kathmandu", []hiddenTZPay{
		{"2026-05-09T18:14:00Z", 10, money.USD, false}, // May 9, 23:59 in Kathmandu
		{"2026-05-09T18:16:00Z", 20, money.USD, false}, // May 10, 00:01
		{"2026-05-10T18:14:00Z", 40, money.USD, false}, // May 10, 23:59
		{"2026-05-10T18:16:00Z", 80, money.USD, false}, // May 11, 00:01
	})
	hiddenTZWant(t, e, kathmandu, "from=2026-05-10&to=2026-05-10", "day", []hiddenTZBucket{{"2026-05-10", map[string]int64{"USD": 60}, 2}})
	hiddenTZWant(t, e, kathmandu, "from=2026-05-09&to=2026-05-11", "day", []hiddenTZBucket{
		{"2026-05-09", map[string]int64{"USD": 10}, 1},
		{"2026-05-10", map[string]int64{"USD": 60}, 2},
		{"2026-05-11", map[string]int64{"USD": 80}, 1},
	})

	lordHowe := hiddenTZOrg(t, e, "Island", "Australia/Lord_Howe", []hiddenTZPay{
		{"2026-04-04T12:59:00Z", 1, money.USD, false}, // Sat April 4, 23:59 (+11:00)
		{"2026-04-04T13:01:00Z", 2, money.USD, false}, // Sun April 5, 00:01 (+11:00)
		{"2026-04-05T13:29:00Z", 4, money.USD, false}, // Sun April 5, 23:59 (+10:30)
		{"2026-04-05T13:31:00Z", 8, money.USD, false}, // Mon April 6, 00:01 (+10:30)
		{"2026-10-03T13:29:00Z", 1, money.EUR, false}, // Sat October 3, 23:59 (+10:30)
		{"2026-10-03T13:31:00Z", 2, money.EUR, false}, // Sun October 4, 00:01 (+10:30)
		{"2026-10-04T12:59:00Z", 4, money.EUR, false}, // Sun October 4, 23:59 (+11:00)
		{"2026-10-04T13:01:00Z", 8, money.EUR, false}, // Mon October 5, 00:01 (+11:00)
	})
	hiddenTZWant(t, e, lordHowe, "from=2026-04-04&to=2026-04-06", "day", []hiddenTZBucket{
		{"2026-04-04", map[string]int64{"USD": 1}, 1},
		{"2026-04-05", map[string]int64{"USD": 6}, 2},
		{"2026-04-06", map[string]int64{"USD": 8}, 1},
	})
	hiddenTZWant(t, e, lordHowe, "from=2026-10-03&to=2026-10-05", "day", []hiddenTZBucket{
		{"2026-10-03", map[string]int64{"EUR": 1}, 1},
		{"2026-10-04", map[string]int64{"EUR": 6}, 2},
		{"2026-10-05", map[string]int64{"EUR": 8}, 1},
	})
	hiddenTZWant(t, e, lordHowe, "from=2026-04-05&to=2026-10-04&group=month", "month", []hiddenTZBucket{
		{"2026-04-01", map[string]int64{"USD": 14}, 3},
		{"2026-05-01", nil, 0},
		{"2026-06-01", nil, 0},
		{"2026-07-01", nil, 0},
		{"2026-08-01", nil, 0},
		{"2026-09-01", nil, 0},
		{"2026-10-01", map[string]int64{"EUR": 7}, 3},
	})
}

// Weeks start on Monday in the organization's time zone and are named by their Monday, even before from.
func TestHiddenRevenueReportWeeks(t *testing.T) {
	e := apitest.New(t)
	org := hiddenTZOrg(t, e, "Weekly", "America/New_York", []hiddenTZPay{
		{"2025-12-31T17:00:00Z", 3, money.USD, false},   // Wed December 31, 2025
		{"2026-01-05T04:59:00Z", 5, money.USD, false},   // Sun January 4, 23:59 EST
		{"2026-01-05T05:01:00Z", 7, money.USD, false},   // Mon January 5, 00:01 EST
		{"2026-03-03T17:00:00Z", 1, money.USD, false},   // Tue March 3: before from
		{"2026-03-04T05:01:00Z", 2, money.USD, false},   // Wed March 4, 00:01 EST
		{"2026-03-09T03:59:00Z", 4, money.USD, false},   // Sun March 8, 23:59 EDT
		{"2026-03-09T04:01:00Z", 8, money.USD, false},   // Mon March 9, 00:01 EDT
		{"2026-03-10T12:00:00Z", 999, money.USD, true},  // failed
		{"2026-03-12T15:00:00Z", 16, money.EUR, false},  // Thu March 12
		{"2026-03-23T04:00:00Z", 32, money.USD, false},  // Mon March 23, 00:00 EDT
		{"2026-03-26T03:59:00Z", 64, money.USD, false},  // Wed March 25, 23:59 EDT
		{"2026-03-26T04:01:00Z", 128, money.USD, false}, // Thu March 26, 00:01 EDT: after to
	})
	hiddenTZWant(t, e, org, "from=2026-03-04&to=2026-03-25&group=week", "week", []hiddenTZBucket{
		{"2026-03-02", map[string]int64{"USD": 6}, 2},
		{"2026-03-09", map[string]int64{"USD": 8, "EUR": 16}, 2},
		{"2026-03-16", nil, 0},
		{"2026-03-23", map[string]int64{"USD": 96}, 2},
	})
	hiddenTZWant(t, e, org, "from=2026-03-09&to=2026-03-15&group=week", "week", []hiddenTZBucket{
		{"2026-03-09", map[string]int64{"USD": 8, "EUR": 16}, 2},
	})
	hiddenTZWant(t, e, org, "from=2026-03-08&to=2026-03-08&group=week", "week", []hiddenTZBucket{
		{"2026-03-02", map[string]int64{"USD": 4}, 1},
	})
	hiddenTZWant(t, e, org, "from=2025-12-31&to=2026-01-06&group=week", "week", []hiddenTZBucket{
		{"2025-12-29", map[string]int64{"USD": 8}, 2},
		{"2026-01-05", map[string]int64{"USD": 7}, 1},
	})
}

// Months are calendar months in the organization's time zone, named by their first day, even before from.
func TestHiddenRevenueReportMonths(t *testing.T) {
	e := apitest.New(t)
	org := hiddenTZOrg(t, e, "Monthly", "America/New_York", []hiddenTZPay{
		{"2026-01-01T04:59:00Z", 3, money.USD, false},    // Wed December 31, 2025, 23:59 EST
		{"2026-01-01T05:01:00Z", 5, money.USD, false},    // Thu January 1, 00:01 EST
		{"2026-01-15T04:59:00Z", 1, money.USD, false},    // January 14, 23:59 EST: before from
		{"2026-01-15T05:01:00Z", 2, money.USD, false},    // January 15, 00:01 EST
		{"2026-02-01T04:59:00Z", 4, money.USD, false},    // January 31, 23:59 EST
		{"2026-03-08T08:00:00Z", 1250, money.KWD, false}, // March 8, 04:00 EDT
		{"2026-03-15T12:00:00Z", 999, money.USD, true},   // failed
		{"2026-04-01T03:59:00Z", 8, money.USD, false},    // March 31, 23:59 EDT
		{"2026-04-01T04:00:00Z", 16, money.USD, false},   // April 1, 00:00 EDT
		{"2026-04-11T03:59:00Z", 32, money.GBP, false},   // April 10, 23:59 EDT
		{"2026-04-11T04:01:00Z", 64, money.USD, false},   // April 11, 00:01 EDT: after to
	})
	hiddenTZWant(t, e, org, "from=2026-01-15&to=2026-04-10&group=month", "month", []hiddenTZBucket{
		{"2026-01-01", map[string]int64{"USD": 6}, 2},
		{"2026-02-01", nil, 0},
		{"2026-03-01", map[string]int64{"USD": 8, "KWD": 1250}, 2},
		{"2026-04-01", map[string]int64{"USD": 16, "GBP": 32}, 2},
	})
	hiddenTZWant(t, e, org, "from=2025-12-20&to=2026-01-05&group=month", "month", []hiddenTZBucket{
		{"2025-12-01", map[string]int64{"USD": 3}, 1},
		{"2026-01-01", map[string]int64{"USD": 5}, 1},
	})
	hiddenTZWant(t, e, org, "from=2026-02-14&to=2026-02-14&group=month", "month", []hiddenTZBucket{{"2026-02-01", nil, 0}})
}

// Long ranges: every day is a bucket, across both of New York's daylight saving changes; at most 366 days.
func TestHiddenRevenueReportLongRanges(t *testing.T) {
	e := apitest.New(t)
	org := hiddenTZOrg(t, e, "Yearly", "America/New_York", []hiddenTZPay{
		{"2026-11-02T04:30:00Z", 9, money.USD, false}, // Sun November 1, 23:30 EST
	})
	for _, c := range []struct {
		query, group string
		buckets      int
		first, last  string
	}{
		{"from=2026-01-01&to=2026-12-31", "day", 365, "2026-01-01", "2026-12-31"},
		{"from=2026-01-01&to=2027-01-01", "day", 366, "2026-01-01", "2027-01-01"},
		{"from=2025-03-08&to=2026-03-08", "day", 366, "2025-03-08", "2026-03-08"},
		{"from=2026-01-01&to=2027-01-01&group=week", "week", 53, "2025-12-29", "2026-12-28"},
		{"from=2026-01-01&to=2027-01-01&group=month", "month", 13, "2026-01-01", "2027-01-01"},
	} {
		rep, r := hiddenTZGet(t, e, org, c.query)
		if rep.Group != c.group || len(rep.Buckets) != c.buckets {
			t.Fatalf("GET /v1/reports/revenue?%s: group %q and %d buckets, want %q and %d\n%s", c.query, rep.Group, len(rep.Buckets), c.group, c.buckets, hiddenTZShort(r))
		}
		day, err := time.Parse(time.DateOnly, c.first)
		if err != nil {
			t.Fatal(err)
		}
		for i, b := range rep.Buckets {
			want := day.Format(time.DateOnly)
			if b.Start != want || b.Amounts == nil {
				t.Fatalf("GET /v1/reports/revenue?%s: bucket %d = %+v, want start %s and amounts an object", c.query, i, b, want)
			}
			wantPayments := 0
			if (c.group == "day" && want == "2026-11-01") || (c.group == "week" && want == "2026-10-26") || (c.group == "month" && want == "2026-11-01") {
				wantPayments = 1
			}
			if b.Payments != wantPayments || b.Amounts["USD"] != int64(9*wantPayments) {
				t.Fatalf("GET /v1/reports/revenue?%s: bucket %s = %+v, want %d payments", c.query, want, b, wantPayments)
			}
			switch c.group {
			case "day":
				day = day.AddDate(0, 0, 1)
			case "week":
				day = day.AddDate(0, 0, 7)
			case "month":
				day = day.AddDate(0, 1, 0)
			}
		}
		if last := rep.Buckets[len(rep.Buckets)-1].Start; last != c.last {
			t.Fatalf("GET /v1/reports/revenue?%s: the last bucket starts %s, want %s", c.query, last, c.last)
		}
	}
}

func TestHiddenRevenueReportErrors(t *testing.T) {
	e := apitest.New(t)
	org := hiddenTZOrg(t, e, "Errors", "America/New_York", nil)
	for _, q := range []string{"", "?to=2026-03-01", "?from=2026-03-01", "?from=&to=2026-03-01", "?from=2026/03/01&to=2026-03-05",
		"?from=yesterday&to=2026-03-05", "?from=2026-03-01&to=2026-02-30", "?from=2026-13-01&to=2026-12-01"} {
		hiddenTZError(t, e, org, q, "invalid_date")
	}
	for _, q := range []string{"?from=2026-03-05&to=2026-03-01", "?from=2026-01-01&to=2027-01-02", "?from=2026-01-01&to=2027-01-02&group=month",
		"?from=2025-03-08&to=2026-03-09"} { // 367 days, one of them 23 hours long
		hiddenTZError(t, e, org, q, "invalid_range")
	}
	for _, g := range []string{"year", "quarter", "days", "hour"} {
		hiddenTZError(t, e, org, "?from=2026-03-01&to=2026-03-05&group="+g, "invalid_group")
	}
}

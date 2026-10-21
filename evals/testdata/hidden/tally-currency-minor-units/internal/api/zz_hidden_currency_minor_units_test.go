package api_test

// Hidden acceptance test of the trial task tally-currency-minor-units (evals/tasks). The agents never see it.

import (
	"net/http"
	"testing"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

type hiddenCurrencyPlan struct {
	ID             string `json:"id"`
	Currency       string `json:"currency"`
	UnitAmount     int64  `json:"unit_amount"`
	Price          string `json:"price"`
	PriceFormatted string `json:"price_formatted"`
}

type hiddenCurrencyInvoice struct {
	ID             string `json:"id"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	Total          int64  `json:"total"`
	TotalFormatted string `json:"total_formatted"`
}

func hiddenCurrencyDo(t *testing.T, e *apitest.Env, want int, method, path, key string, body, out any) {
	t.Helper()
	r := e.Do(method, path, key, body)
	if r.Code != want {
		t.Fatalf("%s %s %v: want status %d, got %s", method, path, body, want, r)
	}
	if out != nil {
		r.JSON(t, out)
	}
}

func TestHiddenPlanPricesUseTheCurrencysDecimals(t *testing.T) {
	e := apitest.New(t)
	cases := []struct {
		currency, typed string
		want            hiddenCurrencyPlan
	}{
		// the prompt's examples
		{"JPY", "1000", hiddenCurrencyPlan{UnitAmount: 1000, Price: "1000", PriceFormatted: "¥1,000"}},
		{"KWD", "1.25", hiddenCurrencyPlan{UnitAmount: 1250, Price: "1.250", PriceFormatted: "KWD 1.250"}},
		{"USD", "12.50", hiddenCurrencyPlan{UnitAmount: 1250, Price: "12.50", PriceFormatted: "$12.50"}},
		// no decimals
		{"JPY", "1234567", hiddenCurrencyPlan{UnitAmount: 1234567, Price: "1234567", PriceFormatted: "¥1,234,567"}},
		{"JPY", "0", hiddenCurrencyPlan{UnitAmount: 0, Price: "0", PriceFormatted: "¥0"}},
		// three decimals
		{"KWD", "1.250", hiddenCurrencyPlan{UnitAmount: 1250, Price: "1.250", PriceFormatted: "KWD 1.250"}},
		{"KWD", "7", hiddenCurrencyPlan{UnitAmount: 7000, Price: "7.000", PriceFormatted: "KWD 7.000"}},
		{"KWD", "0.005", hiddenCurrencyPlan{UnitAmount: 5, Price: "0.005", PriceFormatted: "KWD 0.005"}},
		{"KWD", "1234.567", hiddenCurrencyPlan{UnitAmount: 1234567, Price: "1234.567", PriceFormatted: "KWD 1,234.567"}},
		// two decimals
		{"EUR", "1234.5", hiddenCurrencyPlan{UnitAmount: 123450, Price: "1234.50", PriceFormatted: "€1,234.50"}},
		{"GBP", "12", hiddenCurrencyPlan{UnitAmount: 1200, Price: "12.00", PriceFormatted: "£12.00"}},
	}
	check := func(what string, got hiddenCurrencyPlan, currency, typed string, want hiddenCurrencyPlan) {
		t.Helper()
		if got.Currency != currency || got.UnitAmount != want.UnitAmount || got.Price != want.Price || got.PriceFormatted != want.PriceFormatted {
			t.Errorf("%s: a %s plan priced %q: unit_amount %d, price %q, price_formatted %q; want %d, %q, %q", what, currency, typed,
				got.UnitAmount, got.Price, got.PriceFormatted, want.UnitAmount, want.Price, want.PriceFormatted)
		}
	}
	ids := map[string]int{}
	for i, c := range cases {
		var got hiddenCurrencyPlan
		hiddenCurrencyDo(t, e, http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, map[string]any{"name": "Plan", "currency": c.currency, "price": c.typed, "interval": "month"}, &got)
		check("POST /v1/plans", got, c.currency, c.typed, c.want)
		ids[got.ID] = i
		var again hiddenCurrencyPlan
		hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/plans/"+got.ID, e.Acme.Key, nil, &again)
		check("GET /v1/plans/{id}", again, c.currency, c.typed, c.want)
		var renamed hiddenCurrencyPlan
		hiddenCurrencyDo(t, e, http.StatusOK, "PATCH", "/v1/plans/"+got.ID, e.Acme.Key, map[string]any{"name": "Renamed"}, &renamed)
		check("PATCH /v1/plans/{id}", renamed, c.currency, c.typed, c.want)
	}
	var list struct {
		Items []hiddenCurrencyPlan `json:"items"`
	}
	hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/plans", e.Acme.Key, nil, &list)
	if len(list.Items) != len(cases) {
		t.Fatalf("GET /v1/plans: %d plans, want %d", len(list.Items), len(cases))
	}
	for _, got := range list.Items {
		c := cases[ids[got.ID]]
		check("GET /v1/plans", got, c.currency, c.typed, c.want)
	}

	for _, c := range []struct{ currency, typed string }{
		{"JPY", "1000.5"}, {"JPY", "1000.0"}, {"JPY", "1000."}, {"JPY", "1,000"}, {"JPY", "-1000"},
		{"KWD", "1.2345"}, {"KWD", "-1.250"}, {"KWD", "1 250"},
		{"USD", "12.345"}, {"EUR", "1,234.50"},
	} {
		r := e.Do("POST", "/v1/plans", e.Acme.Key, map[string]any{"name": "Plan", "currency": c.currency, "price": c.typed, "interval": "month"})
		if r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid_amount" {
			t.Errorf("POST /v1/plans: a %s plan priced %q: want 422 invalid_amount, got %s", c.currency, c.typed, r)
		}
	}
}

func TestHiddenInvoiceTotalsUseTheCurrencysDecimals(t *testing.T) {
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")

	// credits (negative totals), before the organization charges tax
	credits := map[money.Currency]string{money.JPY: "-¥500", money.KWD: "-KWD 0.005", money.USD: "-$5.00"}
	for cur, want := range credits {
		amount := map[money.Currency]int64{money.JPY: -500, money.KWD: -5, money.USD: -500}[cur]
		inv, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Acme.ID, CustomerID: c.ID, Currency: cur, Lines: []store.Line{{Description: "Credit", Quantity: 1, UnitAmount: amount}}}, e.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		var got hiddenCurrencyInvoice
		hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/invoices/"+inv.ID, e.Acme.Key, nil, &got)
		if got.Total != amount || got.TotalFormatted != want {
			t.Errorf("a %s credit of %d: total %d, total_formatted %q; want %d, %q", cur, amount, got.Total, got.TotalFormatted, amount, want)
		}
	}

	hiddenCurrencyDo(t, e, http.StatusOK, "PATCH", "/v1/org", e.Acme.Key, map[string]any{"tax_rate_bp": 1000}, nil)
	type invCase struct {
		currency string
		lines    []map[string]any
		total    int64
		want     string
		id       string
	}
	cases := []*invCase{
		// 3 x 1000 yen + 10% tax
		{currency: "JPY", lines: []map[string]any{{"description": "Seats", "quantity": 3, "unit_amount": 1000}}, total: 3300, want: "¥3,300"},
		// 2 x 1.250 + 0.005 dinars = 2.505, + 10% tax (0.2505, rounded to 0.251)
		{currency: "KWD", lines: []map[string]any{{"description": "Seats", "quantity": 2, "unit_amount": 1250}, {"description": "Setup", "quantity": 1, "unit_amount": 5}}, total: 2756, want: "KWD 2.756"},
		// 1,000 x 1.000 dinars + 10% tax
		{currency: "KWD", lines: []map[string]any{{"description": "Seats", "quantity": 1000, "unit_amount": 1000}}, total: 1100000, want: "KWD 1,100.000"},
		{currency: "USD", lines: []map[string]any{{"description": "Seats", "quantity": 1, "unit_amount": 1250}}, total: 1375, want: "$13.75"},
	}
	for _, ic := range cases {
		var got hiddenCurrencyInvoice
		hiddenCurrencyDo(t, e, http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": c.ID, "currency": ic.currency, "lines": ic.lines}, &got)
		if got.Total != ic.total || got.TotalFormatted != ic.want {
			t.Fatalf("POST /v1/invoices (%s): total %d, total_formatted %q; want %d, %q", ic.currency, got.Total, got.TotalFormatted, ic.total, ic.want)
		}
		ic.id = got.ID
		hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/invoices/"+got.ID, e.Acme.Key, nil, &got)
		if got.TotalFormatted != ic.want {
			t.Fatalf("GET /v1/invoices/%s (%s): total_formatted %q, want %q", got.ID, ic.currency, got.TotalFormatted, ic.want)
		}
	}
	var list struct {
		Items []hiddenCurrencyInvoice `json:"items"`
	}
	hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/invoices?limit=100", e.Acme.Key, nil, &list)
	listed := map[string]string{}
	for _, it := range list.Items {
		listed[it.ID] = it.TotalFormatted
	}
	for _, ic := range cases {
		if listed[ic.id] != ic.want {
			t.Fatalf("GET /v1/invoices: invoice %s (%s): total_formatted %q, want %q", ic.id, ic.currency, listed[ic.id], ic.want)
		}
	}

	var paid struct {
		Invoice hiddenCurrencyInvoice `json:"invoice"`
		Payment store.Payment         `json:"payment"`
	}
	hiddenCurrencyDo(t, e, http.StatusOK, "POST", "/v1/invoices/"+cases[0].id+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, &paid)
	if paid.Invoice.Status != "paid" || paid.Invoice.TotalFormatted != "¥3,300" || paid.Payment.Amount != 3300 || paid.Payment.Currency != money.JPY {
		t.Fatalf("pay the JPY invoice: invoice %+v, payment %+v; want it paid, total_formatted ¥3,300, a payment of 3300 JPY", paid.Invoice, paid.Payment)
	}
	if ch := e.Gateway.Charges(); len(ch) != 1 || ch[0].Amount != 3300 || ch[0].Currency != money.JPY {
		t.Fatalf("charges = %+v, want one of 3300 JPY", ch)
	}
	var voided hiddenCurrencyInvoice
	hiddenCurrencyDo(t, e, http.StatusOK, "POST", "/v1/invoices/"+cases[1].id+"/void", e.Acme.Key, nil, &voided)
	if voided.Status != "void" || voided.TotalFormatted != "KWD 2.756" {
		t.Fatalf("void the KWD invoice = %+v, want it void with total_formatted KWD 2.756", voided)
	}

	// a subscription to a JPY plan: 2 seats of 1,000 yen, + 10% tax
	plan := e.CreatePlan(e.Acme, "Yen plan", "JPY", "1000")
	if plan.UnitAmount != 1000 {
		t.Fatalf("a plan priced \"1000\" JPY has unit_amount %d, want 1000", plan.UnitAmount)
	}
	var sub store.Subscription
	hiddenCurrencyDo(t, e, http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": plan.ID, "quantity": 2}, &sub)
	var subInvs struct {
		Items []hiddenCurrencyInvoice `json:"items"`
	}
	hiddenCurrencyDo(t, e, http.StatusOK, "GET", "/v1/invoices?subscription_id="+sub.ID, e.Acme.Key, nil, &subInvs)
	if len(subInvs.Items) != 1 || subInvs.Items[0].Total != 2200 || subInvs.Items[0].TotalFormatted != "¥2,200" {
		t.Fatalf("the subscription's first invoice = %+v, want total 2200, total_formatted ¥2,200", subInvs.Items)
	}
}

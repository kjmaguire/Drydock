package api_test

// Hidden acceptance test of the trial task tally-invoice-list-performance (evals/tasks). The agents never see it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/apitest"
	"example.com/tally/internal/billing"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/money"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

// ---- the answers before the task, computed from the store's data

type hiddenPerfDo func(method, path, key string, body any) *apitest.Response

type hiddenPerfFilter struct{ customer, subscription, status string }

func (f hiddenPerfFilter) values() url.Values {
	v := url.Values{}
	if f.customer != "" {
		v.Set("customer_id", f.customer)
	}
	if f.subscription != "" {
		v.Set("subscription_id", f.subscription)
	}
	if f.status != "" {
		v.Set("status", f.status)
	}
	return v
}

func (f hiddenPerfFilter) match(inv store.Invoice) bool {
	return (f.customer == "" || inv.CustomerID == f.customer) && (f.subscription == "" || inv.SubscriptionID == f.subscription) &&
		(f.status == "" || string(inv.Status) == f.status)
}

type hiddenPerfPos struct {
	at time.Time
	id string
}

func (p hiddenPerfPos) before(q hiddenPerfPos) bool {
	if !p.at.Equal(q.at) {
		return p.at.Before(q.at)
	}
	return p.id < q.id
}

// hiddenPerfModel is what GET /v1/invoices answered before the task for an organization's data at one moment.
type hiddenPerfModel struct {
	order []store.Invoice           // the organization's invoices, oldest first, ties by ID
	items map[string]map[string]any // invoice ID -> the item as the API shows it
}

func hiddenPerfJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hiddenPerfDecode(t testing.TB, b []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("not the JSON expected: %v\n%.500s", err, b)
	}
}

// hiddenPerfSnapshot computes the answers from everything the store holds (its Export): an invoice is shown with its
// fields, its totals, total_formatted, a description (its first line and how many more) and its customer, null once the
// customer is deleted.
func hiddenPerfSnapshot(t testing.TB, st *store.Store, orgID string) hiddenPerfModel {
	t.Helper()
	state := st.Export()
	customers := map[string]store.Customer{}
	for _, c := range state.Customers {
		customers[c.ID] = c
	}
	m := hiddenPerfModel{items: map[string]map[string]any{}}
	for _, inv := range state.Invoices {
		if inv.OrgID != orgID {
			continue
		}
		m.order = append(m.order, inv)
		fields := map[string]json.RawMessage{}
		hiddenPerfDecode(t, hiddenPerfJSON(t, inv), &fields)
		totals := billing.Compute(inv)
		hiddenPerfDecode(t, hiddenPerfJSON(t, totals), &fields)
		fields["total_formatted"] = hiddenPerfJSON(t, money.Format(totals.Total, inv.Currency))
		desc := inv.Lines[0].Description
		if len(inv.Lines) > 1 {
			desc += fmt.Sprintf(" and %d more", len(inv.Lines)-1)
		}
		fields["description"] = hiddenPerfJSON(t, desc)
		fields["customer"] = json.RawMessage("null")
		if c, ok := customers[inv.CustomerID]; ok && c.OrgID == orgID {
			fields["customer"] = hiddenPerfJSON(t, map[string]string{"id": c.ID, "name": c.Name, "email": c.Email})
		}
		var item map[string]any
		hiddenPerfDecode(t, hiddenPerfJSON(t, fields), &item)
		m.items[inv.ID] = item
	}
	sort.Slice(m.order, func(i, j int) bool {
		return hiddenPerfPos{m.order[i].CreatedAt, m.order[i].ID}.before(hiddenPerfPos{m.order[j].CreatedAt, m.order[j].ID})
	})
	return m
}

// page is the page the API answered before the task: the first limit invoices that match f after the position after.
func (m hiddenPerfModel) page(f hiddenPerfFilter, after *hiddenPerfPos, limit int) (ids []string, more bool) {
	for _, inv := range m.order {
		if !f.match(inv) || (after != nil && !after.before(hiddenPerfPos{inv.CreatedAt, inv.ID})) {
			continue
		}
		if len(ids) == limit {
			return ids, true
		}
		ids = append(ids, inv.ID)
	}
	return ids, false
}

func hiddenPerfShort(r *apitest.Response) string {
	if s := r.String(); len(s) > 600 {
		return s[:600] + "..."
	}
	return r.String()
}

// hiddenPerfCheckPage checks one answer of GET /v1/invoices against the page that was expected, and returns next_cursor.
func hiddenPerfCheckPage(t *testing.T, what string, r *apitest.Response, m hiddenPerfModel, want []string, more bool) string {
	t.Helper()
	if r.Code != http.StatusOK {
		t.Fatalf("%s: want 200, got %s", what, hiddenPerfShort(r))
	}
	var raw map[string]json.RawMessage
	hiddenPerfDecode(t, r.Body, &raw)
	if items := strings.TrimSpace(string(raw["items"])); !strings.HasPrefix(items, "[") {
		t.Fatalf("%s: items is %s, want a list ([] when empty)", what, items)
	}
	var items []map[string]any
	hiddenPerfDecode(t, raw["items"], &items)
	var got []string
	for _, it := range items {
		id, _ := it["id"].(string)
		got = append(got, id)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: items %v, want %v (oldest first, ties by ID)", what, got, want)
	}
	for _, it := range items {
		id := it["id"].(string)
		for field, w := range m.items[id] {
			if g, ok := it[field]; !ok || !reflect.DeepEqual(g, w) {
				t.Fatalf("%s: invoice %s: %q is %s, want %s", what, id, field, hiddenPerfJSON(t, g), hiddenPerfJSON(t, w))
			}
		}
	}
	next, has := raw["next_cursor"]
	var cursor string
	if has {
		if err := json.Unmarshal(next, &cursor); err != nil {
			t.Fatalf("%s: next_cursor is %s", what, next)
		}
	}
	switch {
	case more && cursor == "":
		t.Fatalf("%s: no next_cursor, but more invoices follow", what)
	case !more && has:
		t.Fatalf("%s: next_cursor %s on the last page (it is absent there)", what, next)
	}
	return cursor
}

// hiddenPerfWalk pages through GET /v1/invoices with a filter and a limit (0: the default) and checks every page against
// what the API answered before the task for the data at that moment. between runs after each page but the last.
func hiddenPerfWalk(t *testing.T, what string, do hiddenPerfDo, st *store.Store, org apitest.Org, f hiddenPerfFilter, limit int, between func(page int)) []string {
	t.Helper()
	var seen []string
	var after *hiddenPerfPos
	cursor := ""
	size := limit
	if size == 0 {
		size = 20
	}
	for page := 1; page <= 1000; page++ {
		m := hiddenPerfSnapshot(t, st, org.ID)
		want, more := m.page(f, after, size)
		v := f.values()
		if limit != 0 {
			v.Set("limit", fmt.Sprint(limit))
		}
		if cursor != "" {
			v.Set("cursor", cursor)
		}
		path := "/v1/invoices"
		if len(v) > 0 {
			path += "?" + v.Encode()
		}
		r := do("GET", path, org.Key, nil)
		cursor = hiddenPerfCheckPage(t, fmt.Sprintf("%s: page %d: GET %s", what, page, path), r, m, want, more)
		seen = append(seen, want...)
		if !more {
			return seen
		}
		last := m.items[want[len(want)-1]]
		at, err := time.Parse(time.RFC3339Nano, last["created_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		after = &hiddenPerfPos{at, want[len(want)-1]}
		if between != nil {
			between(page)
		}
	}
	t.Fatalf("%s: more than 1000 pages", what)
	return nil
}

// hiddenPerfServe runs the API over a store of its own (for one built with store.FromState).
func hiddenPerfServe(t *testing.T, st *store.Store, clk *apitest.Env) hiddenPerfDo {
	hooks := webhooks.New(webhooks.Options{Store: st, Clock: clk.Clock, Doer: &apitest.FakeHTTP{}, Resolver: &apitest.FakeDNS{}})
	pay := &payments.Service{Store: st, Gateway: &payments.Fake{}, Clock: clk.Clock, Publisher: hooks}
	subs := &subscriptions.Service{Store: st, Payments: pay, Clock: clk.Clock, Publisher: hooks}
	srv := api.New(st, api.Options{Clock: clk.Clock, Payments: pay, Subscriptions: subs, Webhooks: hooks, Mail: &mail.Outbox{}})
	return func(method, path, key string, body any) *apitest.Response {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(hiddenPerfJSON(t, body))
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
}

// hiddenPerfSmall builds a small organization whose invoices cover what an answer shows: several customers (one renamed,
// one deleted), subscriptions, one to three lines, discounts, tax rates, currencies, statuses, periods, creation times out
// of creation order and shared by several invoices, and another organization's invoices beside them.
func hiddenPerfSmall(t *testing.T) (*apitest.Env, []store.Customer, []store.Subscription) {
	e := apitest.New(t)
	var cs []store.Customer
	for _, name := range []string{"Ann", "Bob", "Cid", "Dee", "Eve"} {
		cs = append(cs, e.CreateCustomer(e.Acme, name+" Example", strings.ToLower(name)+"@example.com"))
		e.Clock.Advance(time.Second)
	}
	g := e.CreateCustomer(e.Globex, "Gus", "gus@example.com")
	plan := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	var subs []store.Subscription
	for i, c := range cs[:2] {
		var sub store.Subscription
		e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": plan.ID, "quantity": i + 2}, &sub)
		subs = append(subs, sub)
	}
	base := apitest.Start.Add(-30 * 24 * time.Hour)
	currencies := []money.Currency{money.USD, money.EUR, money.GBP, money.JPY, money.KWD}
	for i := range 40 {
		if i == 20 {
			e.MustDo(http.StatusOK, "PATCH", "/v1/org", e.Acme.Key, map[string]any{"tax_rate_bp": 825}, nil)
		}
		inv := store.Invoice{OrgID: e.Acme.ID, CustomerID: cs[i%5].ID, Currency: currencies[i%5], DiscountPercent: (i % 4) * 5}
		for l := range i%3 + 1 {
			inv.Lines = append(inv.Lines, store.Line{Description: fmt.Sprintf("Item %d.%d", i, l), Quantity: int64(l + 1), UnitAmount: int64(1000*(l+1) + 37*i)})
		}
		if i%13 == 5 {
			inv.Lines = append(inv.Lines, store.Line{Description: "Credit", Quantity: 1, UnitAmount: -50000})
		}
		if i%6 == 0 {
			sub := subs[(i/6)%2]
			ps, pe := base.Add(time.Duration(i)*24*time.Hour), base.Add(time.Duration(i+30)*24*time.Hour)
			inv.SubscriptionID, inv.CustomerID, inv.PeriodStart, inv.PeriodEnd = sub.ID, sub.CustomerID, &ps, &pe
		}
		// (i*17)%40 visits 0..39 out of order; every two invoices share a creation time
		at := base.Add(time.Duration((i*17)%40/2) * time.Hour)
		if i%7 == 3 {
			at = at.Add(1500 * time.Millisecond) // a time with a fraction of a second
		}
		created, err := e.Store.CreateInvoice(inv, at)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case i%5 == 1:
			e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+created.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, nil)
		case i%9 == 2:
			e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+created.ID+"/void", e.Acme.Key, nil, nil)
		}
		if i%4 == 0 {
			if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Globex.ID, CustomerID: g.ID, Currency: money.EUR,
				Lines: []store.Line{{Description: "Globex work", Quantity: 1, UnitAmount: 999}}}, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+cs[2].ID, e.Acme.Key, map[string]any{"name": "Cid Renamed", "email": "cid.new@example.com"}, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+cs[3].ID, e.Acme.Key, nil, nil)
	return e, cs, subs
}

// Every page, with and without filters, answers exactly what it answered before the task.
func TestHiddenInvoiceListAnswersStayTheSame(t *testing.T) {
	e, cs, subs := hiddenPerfSmall(t)
	do := hiddenPerfDo(func(method, path, key string, body any) *apitest.Response { return e.Do(method, path, key, body) })
	all := hiddenPerfWalk(t, "all invoices", do, e.Store, e.Acme, hiddenPerfFilter{}, 0, nil)
	if len(all) != 42 {
		t.Fatalf("%d invoices listed, want 42", len(all))
	}
	for _, limit := range []int{1, 5, 7, 9, 14, 42, 100} {
		hiddenPerfWalk(t, fmt.Sprintf("limit %d", limit), do, e.Store, e.Acme, hiddenPerfFilter{}, limit, nil)
	}
	for _, f := range []hiddenPerfFilter{
		{customer: cs[0].ID}, {customer: cs[2].ID}, {customer: cs[3].ID}, {customer: cs[4].ID},
		{subscription: subs[0].ID}, {subscription: subs[1].ID},
		{status: "open"}, {status: "paid"}, {status: "void"},
		{customer: cs[1].ID, status: "paid"}, {customer: cs[0].ID, subscription: subs[0].ID}, {subscription: subs[1].ID, status: "open"},
		{customer: "cus_nope"}, {subscription: "sub_nope"},
	} {
		for _, limit := range []int{0, 2, 3, 4, 100} {
			hiddenPerfWalk(t, fmt.Sprintf("filter %+v, limit %d", f, limit), do, e.Store, e.Acme, f, limit, nil)
		}
	}
	// another organization's customer and invoices are never listed
	globexCus := e.Store.Export().Customers
	for _, c := range globexCus {
		if c.OrgID == e.Globex.ID {
			hiddenPerfWalk(t, "Acme asks for Globex's customer", do, e.Store, e.Acme, hiddenPerfFilter{customer: c.ID}, 0, nil)
		}
	}
	empty := e.NewOrg("Empty", store.OrgTeam, "UTC")
	hiddenPerfWalk(t, "an organization without invoices", do, e.Store, empty, hiddenPerfFilter{}, 0, nil)

	// after a restart: a store built from the same state answers the same
	loaded, err := store.FromState(e.Store.Export())
	if err != nil {
		t.Fatal(err)
	}
	reloaded := hiddenPerfServe(t, loaded, e)
	for _, f := range []hiddenPerfFilter{{}, {customer: cs[1].ID}, {subscription: subs[0].ID}, {status: "paid"}} {
		for _, limit := range []int{0, 7, 100} {
			hiddenPerfWalk(t, fmt.Sprintf("after store.FromState: filter %+v, limit %d", f, limit), reloaded, loaded, e.Acme, f, limit, nil)
		}
	}
}

// Paging keeps working while invoices are created, paid and voided between pages.
func TestHiddenInvoiceListPagingWhileInvoicesChange(t *testing.T) {
	e, cs, _ := hiddenPerfSmall(t)
	do := hiddenPerfDo(func(method, path, key string, body any) *apitest.Response { return e.Do(method, path, key, body) })
	create := func(customer string) string {
		var inv struct {
			ID string `json:"id"`
		}
		e.MustDo(http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": customer, "currency": "USD",
			"lines": []map[string]any{{"description": "New", "quantity": 1, "unit_amount": 100}}}, &inv)
		return inv.ID
	}
	var created []string
	seen := hiddenPerfWalk(t, "new invoices between pages", do, e.Store, e.Acme, hiddenPerfFilter{}, 10, func(page int) {
		switch page {
		case 1: // newer than everything: listed at the end
			e.Clock.Advance(time.Minute)
			created = append(created, create(cs[0].ID))
			e.Clock.Advance(time.Minute)
			created = append(created, create(cs[1].ID))
		case 2: // the clock went back: older than the pages already seen, so never listed by this walk
			now := e.Clock.Now()
			e.Clock.Set(apitest.Start.Add(-365 * 24 * time.Hour))
			create(cs[4].ID)
			e.Clock.Set(now)
		case 3: // at the same time as the newest invoice: listed after it
			created = append(created, create(cs[2].ID))
		}
	})
	if len(seen) != 45 {
		t.Fatalf("the walk listed %d invoices, want the 42 there were and the 3 created after them", len(seen))
	}
	if got := strings.Join(seen[42:], ","); got != strings.Join(created, ",") {
		t.Fatalf("the walk ended with %s, want the invoices created while it went: %s", got, strings.Join(created, ","))
	}
	dups := map[string]bool{}
	for _, id := range seen {
		if dups[id] {
			t.Fatalf("invoice %s was listed twice", id)
		}
		dups[id] = true
	}

	// open invoices, while the next ones are paid and voided
	hiddenPerfWalk(t, "open invoices while they are paid", do, e.Store, e.Acme, hiddenPerfFilter{status: "open"}, 4, func(page int) {
		m := hiddenPerfSnapshot(t, e.Store, e.Acme.ID)
		var open []string
		for _, inv := range m.order {
			if inv.Status == store.InvoiceOpen {
				open = append(open, inv.ID)
			}
		}
		// pay the first open invoice after this page and void the third
		if k := page * 4; k < len(open) {
			e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+open[k]+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, nil)
			if k+2 < len(open) {
				e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+open[k+2]+"/void", e.Acme.Key, nil, nil)
			}
		}
	})
	// a customer renamed and another deleted between pages
	hiddenPerfWalk(t, "customers change between pages", do, e.Store, e.Acme, hiddenPerfFilter{}, 6, func(page int) {
		switch page {
		case 1:
			e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+cs[0].ID, e.Acme.Key, map[string]any{"name": "Ann Renamed"}, nil)
		case 2:
			e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+cs[1].ID, e.Acme.Key, nil, nil)
		}
	})
}

func TestHiddenInvoiceListErrorsStayTheSame(t *testing.T) {
	e, cs, _ := hiddenPerfSmall(t)
	for _, c := range []struct{ query, code string }{
		{"limit=0", "invalid_limit"}, {"limit=101", "invalid_limit"}, {"limit=abc", "invalid_limit"}, {"limit=-1", "invalid_limit"},
		{"limit=2.5", "invalid_limit"},
		{"cursor=%21%21", "invalid_cursor"}, {"cursor=aGVsbG8", "invalid_cursor"}, {"cursor=e30", "invalid_cursor"},
		{"status=lost", "invalid_status"}, {"status=OPEN", "invalid_status"}, {"status=open,paid", "invalid_status"},
		{"customer_id=" + cs[0].ID + "&status=lost", "invalid_status"},
		{"limit=0&status=lost", "invalid_limit"}, {"cursor=%21%21&status=lost", "invalid_cursor"},
	} {
		r := e.Do("GET", "/v1/invoices?"+c.query, e.Acme.Key, nil)
		if r.Code != http.StatusBadRequest || r.Map(t)["error"] != c.code {
			t.Errorf("GET /v1/invoices?%s: want 400 {\"error\":%q}, got %s", c.query, c.code, hiddenPerfShort(r))
		}
	}
	if r := e.Do("GET", "/v1/invoices", "", nil); r.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/invoices without a key: want 401, got %s", hiddenPerfShort(r))
	}
}

// ---- speed

// A request is timed several times when it is slow, so that a busy machine (the acceptance check runs beside other work)
// cannot fail a fast solution: a case passes as soon as one try is within the budget, and fails when hiddenPerfTries tries
// all miss it, or when hiddenPerfHopeless tries each took longer than hiddenPerfHopelessTime.
const (
	hiddenPerfBudget       = 250 * time.Millisecond // per request, what the prompt's check allows
	hiddenPerfTries        = 10
	hiddenPerfHopeless     = 3
	hiddenPerfHopelessTime = time.Second
)

// hiddenPerfTime runs a request (prepare, untimed, before each try) until a try is within the budget, and fails the test
// when none is. It returns that try's response and time, and how many tries it took.
func hiddenPerfTime(t *testing.T, what string, prepare func(), run func() *apitest.Response) (*apitest.Response, time.Duration, int) {
	t.Helper()
	var slow []string
	hopeless := 0
	for try := 1; try <= hiddenPerfTries; try++ {
		if prepare != nil {
			prepare()
		}
		start := time.Now()
		r := run()
		d := time.Since(start)
		if r.Code != http.StatusOK && r.Code != http.StatusCreated {
			t.Fatalf("%s: want 200, got %s", what, hiddenPerfShort(r))
		}
		if d <= hiddenPerfBudget {
			return r, d, try
		}
		slow = append(slow, d.Round(time.Millisecond).String())
		if d > hiddenPerfHopelessTime {
			if hopeless++; hopeless == hiddenPerfHopeless {
				break
			}
		}
	}
	t.Fatalf("%s: too slow: %d tries took %s; a page must answer within %s (the prompt: well under 100 ms) for an organization "+
		"with 60,000 invoices and 5,000 customers", what, len(slow), strings.Join(slow, ", "), hiddenPerfBudget)
	return nil, 0, 0
}

type hiddenPerfPage struct {
	Items []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// GET /v1/invoices answers every page quickly for an organization with 60,000 invoices and 5,000 customers, with another
// organization's 40,000 invoices in the store too; creating, paying and voiding stay quick.
func TestHiddenInvoiceListIsFast(t *testing.T) {
	e := apitest.New(t)
	st := e.Store
	seedStart := time.Now()
	at := apitest.Start.Add(-400 * 24 * time.Hour)
	customers := func(org apitest.Org, n int) []store.Customer {
		out := make([]store.Customer, 0, n)
		for i := range n {
			c, err := st.CreateCustomer(org.ID, fmt.Sprintf("Customer %d of %s", i, org.Name), fmt.Sprintf("c%d@%s-customers.example", i, strings.ToLower(org.Name)), at)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	acmeCus, globexCus := customers(e.Acme, 5000), customers(e.Globex, 500)
	nobody, err := st.CreateCustomer(e.Acme.ID, "Nobody", "nobody@example.com", at)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := st.CreatePlan(store.Plan{OrgID: e.Acme.ID, Name: "Seat", Currency: money.USD, UnitAmount: 1500, Interval: store.Monthly}, at)
	if err != nil {
		t.Fatal(err)
	}
	var subs []store.Subscription
	for i := range 50 {
		sub, err := st.CreateSubscription(store.Subscription{OrgID: e.Acme.ID, CustomerID: acmeCus[i*97].ID, PlanID: plan.ID, Quantity: 1, Status: store.Active,
			StartedAt: at, CurrentPeriodStart: at, CurrentPeriodEnd: at.AddDate(0, 1, 0)}, at)
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, sub)
	}
	const n = 60000
	type rec struct {
		pos           hiddenPerfPos
		customer, sub string
		status        store.InvoiceStatus
	}
	var acme []rec
	for i := range n {
		// creation times mostly in creation order, but not quite: of every three invoices the first is a minute later than
		// the two after it (which share a minute), and every 1,000th is from long before all the others
		created := at.Add(time.Duration(i/3+1-min(i%3, 1)) * time.Minute)
		if i%1000 == 999 {
			created = at.Add(-time.Duration(i) * time.Hour)
		}
		inv := store.Invoice{OrgID: e.Acme.ID, CustomerID: acmeCus[(i*31)%5000].ID, Currency: money.USD, DiscountPercent: i % 3 * 5}
		for l := range 1 + i%3 {
			inv.Lines = append(inv.Lines, store.Line{Description: fmt.Sprintf("Work %d, part %d", i, l+1), Quantity: int64(1 + l), UnitAmount: 1000 + int64(i%7)*250})
		}
		if i%10 == 0 {
			sub := subs[(i/10)%50]
			inv.SubscriptionID, inv.CustomerID = sub.ID, sub.CustomerID
		}
		got, err := st.CreateInvoice(inv, created)
		if err != nil {
			t.Fatal(err)
		}
		status := store.InvoiceOpen
		switch {
		case i%3 == 1:
			status = store.InvoicePaid
		case i%29 == 0:
			status = store.InvoiceVoid
		}
		if status != store.InvoiceOpen {
			if _, err := st.UpdateInvoice(e.Acme.ID, got.ID, func(x *store.Invoice) error {
				x.Status = status
				if status == store.InvoicePaid {
					x.PaidAt = &created
				} else {
					x.VoidedAt = &created
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		acme = append(acme, rec{hiddenPerfPos{got.CreatedAt, got.ID}, inv.CustomerID, inv.SubscriptionID, status})
		if i%3 != 2 {
			if _, err := st.CreateInvoice(store.Invoice{OrgID: e.Globex.ID, CustomerID: globexCus[i%500].ID, Currency: money.EUR,
				Lines: []store.Line{{Description: "Globex work", Quantity: 1, UnitAmount: 500}}}, created); err != nil {
				t.Fatal(err)
			}
		}
	}
	sort.Slice(acme, func(i, j int) bool { return acme[i].pos.before(acme[j].pos) })
	t.Logf("seeded 60,000 + 40,000 invoices and 5,500 customers in %s", time.Since(seedStart).Round(time.Millisecond))
	runtime.GC() // the seeding's garbage is not the requests' to collect

	get := func(query string) func() *apitest.Response {
		return func() *apitest.Response { return e.Do("GET", "/v1/invoices?"+query, e.Acme.Key, nil) }
	}
	var slowest time.Duration
	requests, retried := 0, 0
	timed := func(what, query string, prepare func()) hiddenPerfPage {
		t.Helper()
		r, d, tries := hiddenPerfTime(t, what+": GET /v1/invoices?"+query, prepare, get(query))
		slowest = max(slowest, d)
		requests++
		if tries > 1 {
			retried++
		}
		var p hiddenPerfPage
		r.JSON(t, &p)
		return p
	}
	// walk pages through one list (a filter), timing every page, and checks the items against want, oldest first; it
	// stops after maxPages pages or, for a slow solution, once the walk has taken budget (it never fails for that alone:
	// each page has its own limit). It returns the cursors of the pages walked, by page.
	walk := func(what string, f hiddenPerfFilter, limit, maxPages int, budget time.Duration, want []string) []string {
		t.Helper()
		v := f.values()
		v.Set("limit", fmt.Sprint(limit))
		var cursors []string
		pos, cursor := 0, ""
		started := time.Now()
		for k := 1; k <= maxPages; k++ {
			q := v.Encode()
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}
			p := timed(fmt.Sprintf("%s, page %d", what, k), q, nil)
			for i, it := range p.Items {
				if pos+i >= len(want) || it.ID != want[pos+i] {
					t.Fatalf("%s, page %d (limit %d): item %d is %s, want %v (oldest first, ties by ID)", what, k, limit, i, it.ID, want[pos+i:min(pos+i+1, len(want))])
				}
			}
			if len(p.Items) != min(limit, len(want)-pos) {
				t.Fatalf("%s, page %d (limit %d): %d items, want %d", what, k, limit, len(p.Items), min(limit, len(want)-pos))
			}
			pos += len(p.Items)
			if (p.NextCursor != "") != (pos < len(want)) {
				t.Fatalf("%s, page %d: next_cursor %q after %d of %d invoices", what, k, p.NextCursor, pos, len(want))
			}
			if p.NextCursor == "" {
				break
			}
			cursors = append(cursors, p.NextCursor)
			cursor = p.NextCursor
			if time.Since(started) > budget {
				t.Logf("%s: stopped after %d pages, %s", what, k, time.Since(started).Round(time.Millisecond))
				break
			}
		}
		return cursors
	}
	ids := func(keep func(rec) bool) []string {
		var out []string
		for _, x := range acme {
			if keep(x) {
				out = append(out, x.pos.id)
			}
		}
		return out
	}
	all := ids(func(rec) bool { return true })

	// the first page, then every page of the list, 100 at a time
	timed("the first page", "", nil)
	walkStart := time.Now()
	cursors := walk("all invoices", hiddenPerfFilter{}, 100, 600, 30*time.Second, all)
	t.Logf("paged through %d invoices in %s", min(100*(len(cursors)+1), n), time.Since(walkStart).Round(time.Millisecond))
	middle, deepest := url.QueryEscape(cursors[(len(cursors)-1)/2]), url.QueryEscape(cursors[len(cursors)-1])

	// each filter: its first pages and, where the list is short enough, its middle and last pages
	customer := acmeCus[4321].ID
	sub := subs[17].ID
	walk("open invoices", hiddenPerfFilter{status: "open"}, 100, 10, time.Minute, ids(func(x rec) bool { return x.status == store.InvoiceOpen }))
	walk("paid invoices", hiddenPerfFilter{status: "paid"}, 100, 10, time.Minute, ids(func(x rec) bool { return x.status == store.InvoicePaid }))
	walk("void invoices", hiddenPerfFilter{status: "void"}, 100, 20, time.Minute, ids(func(x rec) bool { return x.status == store.InvoiceVoid }))
	walk("void invoices, 20 a page", hiddenPerfFilter{status: "void"}, 20, 3, time.Minute, ids(func(x rec) bool { return x.status == store.InvoiceVoid }))
	walk("one customer's invoices", hiddenPerfFilter{customer: customer}, 2, 20, time.Minute, ids(func(x rec) bool { return x.customer == customer }))
	walk("one customer's paid invoices", hiddenPerfFilter{customer: customer, status: "paid"}, 100, 2, time.Minute,
		ids(func(x rec) bool { return x.customer == customer && x.status == store.InvoicePaid }))
	walk("a customer without invoices", hiddenPerfFilter{customer: nobody.ID}, 20, 1, time.Minute, nil)
	walk("another organization's customer", hiddenPerfFilter{customer: globexCus[7].ID}, 20, 1, time.Minute, nil)
	subIDs := ids(func(x rec) bool { return x.sub == sub })
	if len(subIDs) != 120 {
		t.Fatalf("the subscription has %d invoices in the test's records, want 120", len(subIDs))
	}
	walk("one subscription's invoices", hiddenPerfFilter{subscription: sub}, 10, 20, time.Minute, subIDs)
	walk("one subscription's open invoices", hiddenPerfFilter{subscription: sub, status: "open"}, 100, 2, time.Minute,
		ids(func(x rec) bool { return x.sub == sub && x.status == store.InvoiceOpen }))

	// pages asked for right after an invoice is created, or paid
	newInvoice := func() {
		e.Clock.Advance(time.Second)
		e.MustDo(http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": customer, "currency": "USD",
			"lines": []map[string]any{{"description": "New", "quantity": 1, "unit_amount": 100}}}, nil)
	}
	timed("the first page after a new invoice", "", newInvoice)
	timed("a page from the middle after a new invoice", "limit=100&cursor="+middle, newInvoice)
	timed("the deepest page walked after a new invoice", "limit=100&cursor="+deepest, newInvoice)
	var toPay []string
	for _, x := range acme {
		if x.status == store.InvoiceOpen && len(toPay) < 20 {
			toPay = append(toPay, x.pos.id)
		}
	}
	payOne := func() {
		id := toPay[0]
		toPay = toPay[1:]
		e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+id+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, nil)
	}
	timed("open invoices after a payment", "status=open&limit=100", payOne)
	t.Logf("%d timed requests, %d of them tried more than once; the slowest try within the budget took %s", requests, retried, slowest.Round(100*time.Microsecond))

	// creating, paying and voiding do not become noticeably slower
	writes := func(what string, k int, run func(i int) *apitest.Response) {
		t.Helper()
		var ds []time.Duration
		for i := range k {
			start := time.Now()
			r := run(i)
			ds = append(ds, time.Since(start))
			if r.Code != http.StatusOK && r.Code != http.StatusCreated {
				t.Fatalf("%s: got %s", what, hiddenPerfShort(r))
			}
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		if median := ds[len(ds)/2]; median > hiddenPerfBudget {
			t.Fatalf("%s takes %s (the median of %d), want well within %s", what, median.Round(time.Millisecond), k, hiddenPerfBudget)
		}
	}
	var made []string
	writes("POST /v1/invoices", 20, func(i int) *apitest.Response {
		e.Clock.Advance(time.Second)
		r := e.Do("POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": acmeCus[i].ID, "currency": "USD",
			"lines": []map[string]any{{"description": "Timed", "quantity": 1, "unit_amount": 100}}})
		var inv struct {
			ID string `json:"id"`
		}
		if r.Code == http.StatusCreated {
			r.JSON(t, &inv)
			made = append(made, inv.ID)
		}
		return r
	})
	writes("POST /v1/invoices/{id}/pay", 10, func(i int) *apitest.Response {
		return e.Do("POST", "/v1/invoices/"+made[i]+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"})
	})
	writes("POST /v1/invoices/{id}/void", 10, func(i int) *apitest.Response {
		return e.Do("POST", "/v1/invoices/"+made[10+i]+"/void", e.Acme.Key, nil)
	})
}

package api_test

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

func TestAuthentication(t *testing.T) {
	e := apitest.New(t)
	if r := e.Do("GET", "/healthz", "", nil); r.Code != http.StatusOK {
		t.Fatalf("healthz: %s", r)
	}
	for _, key := range []string{"", "tly_nope"} {
		if r := e.Do("GET", "/v1/customers", key, nil); r.Code != http.StatusUnauthorized || r.Map(t)["error"] != "unauthorized" {
			t.Fatalf("key %q: %s", key, r)
		}
	}
	var me struct {
		User store.User `json:"user"`
		Org  store.Org  `json:"org"`
		Role string     `json:"role"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/me", e.Acme.Key, nil, &me)
	if me.User.ID != e.Acme.Owner.ID || me.Org.ID != e.Acme.ID || me.Role != "owner" {
		t.Fatalf("me = %+v", me)
	}
	if k, _ := e.Store.APIKeyByToken(e.Acme.Key); k.LastUsedAt == nil {
		t.Fatal("the key's last use was not recorded")
	}
}

func TestViewersOnlyRead(t *testing.T) {
	e := apitest.New(t)
	_, viewer := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	if r := e.Do("GET", "/v1/customers", viewer, nil); r.Code != http.StatusOK {
		t.Fatalf("viewer read: %s", r)
	}
	if r := e.Do("POST", "/v1/customers", viewer, map[string]string{"name": "X", "email": "x@example.com"}); r.Code != http.StatusForbidden {
		t.Fatalf("viewer write: %s", r)
	}
}

func TestRequestID(t *testing.T) {
	e := apitest.New(t)
	r := e.DoWith("GET", "/v1/org", e.Acme.Key, nil, http.Header{"X-Request-Id": {"abc-123"}})
	if got := r.Header.Get("X-Request-Id"); got != "abc-123" {
		t.Fatalf("request id %q", got)
	}
	r = e.DoWith("GET", "/v1/org", e.Acme.Key, nil, http.Header{"X-Request-Id": {"bad id with spaces"}})
	if got := r.Header.Get("X-Request-Id"); !regexp.MustCompile(`^req_[0-9a-f]{16}$`).MatchString(got) {
		t.Fatalf("generated request id %q", got)
	}
}

func TestCustomers(t *testing.T) {
	e := apitest.New(t)
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	if r := e.Do("POST", "/v1/customers", e.Acme.Key, map[string]string{"name": "Jane 2", "email": "JANE@example.com"}); r.Code != http.StatusConflict || r.Map(t)["error"] != "email_taken" {
		t.Fatalf("duplicate: %s", r)
	}
	if r := e.Do("POST", "/v1/customers", e.Acme.Key, map[string]string{"name": "", "email": "x@example.com"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no name: %s", r)
	}
	if r := e.Do("POST", "/v1/customers", e.Acme.Key, "{not json"); r.Code != http.StatusBadRequest || r.Map(t)["error"] != "invalid_json" {
		t.Fatalf("bad json: %s", r)
	}
	var got store.Customer
	e.MustDo(http.StatusOK, "GET", "/v1/customers/"+jane.ID, e.Acme.Key, nil, &got)
	if got.Email != "jane@example.com" {
		t.Fatalf("get = %+v", got)
	}
	if r := e.Do("GET", "/v1/customers/"+jane.ID, e.Globex.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("another org's customer: %s", r)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+jane.ID, e.Acme.Key, map[string]any{"name": "Jane Q. Doe", "metadata": map[string]string{"crm": "7"}}, &got)
	if got.Name != "Jane Q. Doe" || got.Metadata["crm"] != "7" || got.Email != "jane@example.com" {
		t.Fatalf("patch = %+v", got)
	}
	e.Clock.Advance(time.Minute)
	john := e.CreateCustomer(e.Acme, "John Roe", "john@example.com")
	e.Clock.Advance(time.Minute)
	e.CreateCustomer(e.Acme, "Ann Lee", "ann@other.example")

	var page struct {
		Items      []store.Customer `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/customers?limit=2", e.Acme.Key, nil, &page)
	if len(page.Items) != 2 || page.Items[0].ID != jane.ID || page.Items[1].ID != john.ID || page.NextCursor == "" {
		t.Fatalf("page 1 = %+v", page)
	}
	cur := page.NextCursor
	page.Items, page.NextCursor = nil, ""
	e.MustDo(http.StatusOK, "GET", "/v1/customers?limit=2&cursor="+url.QueryEscape(cur), e.Acme.Key, nil, &page)
	if len(page.Items) != 1 || page.Items[0].Name != "Ann Lee" || page.NextCursor != "" {
		t.Fatalf("page 2 = %+v", page)
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=%21%21"} {
		if r := e.Do("GET", "/v1/customers?"+q, e.Acme.Key, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("%s: %s", q, r)
		}
	}
	page.Items = nil
	e.MustDo(http.StatusOK, "GET", "/v1/customers?q=EXAMPLE.COM", e.Acme.Key, nil, &page)
	if len(page.Items) != 2 {
		t.Fatalf("search = %+v", page.Items)
	}
	page.Items = nil
	e.MustDo(http.StatusOK, "GET", "/v1/customers?email=John@Example.com", e.Acme.Key, nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != john.ID {
		t.Fatalf("by email = %+v", page.Items)
	}

	r := e.MustDo(http.StatusOK, "GET", "/v1/customers/export.csv", e.Acme.Key, nil, nil)
	if ct := r.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	rows, err := csv.NewReader(strings.NewReader(string(r.Body))).ReadAll()
	if err != nil || len(rows) != 4 || strings.Join(rows[0], ",") != "id,name,email,created_at" || rows[1][0] != jane.ID {
		t.Fatalf("csv = %v, %v", rows, err)
	}

	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+john.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/customers/"+john.ID, e.Acme.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("deleted customer: %s", r)
	}
}

func TestPlans(t *testing.T) {
	e := apitest.New(t)
	var p struct {
		store.Plan
		Price          string `json:"price"`
		PriceFormatted string `json:"price_formatted"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", e.Acme.Key, map[string]any{"name": "Team", "currency": "USD", "price": "12.50", "interval": "month"}, &p)
	if p.UnitAmount != 1250 || p.Price != "12.50" || p.PriceFormatted != "$12.50" || !p.Active || p.UsageType != store.Licensed {
		t.Fatalf("plan = %+v", p)
	}
	for _, bad := range []map[string]any{
		{"name": "X", "currency": "USD", "price": "abc", "interval": "month"},
		{"name": "X", "currency": "USD", "price": "-1", "interval": "month"},
	} {
		if r := e.Do("POST", "/v1/plans", e.Acme.Key, bad); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid_amount" {
			t.Fatalf("%v: %s", bad, r)
		}
	}
	if r := e.Do("POST", "/v1/plans", e.Acme.Key, map[string]any{"name": "X", "currency": "USD", "price": "1", "interval": "week"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("weekly: %s", r)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/plans/"+p.ID, e.Acme.Key, map[string]any{"active": false}, &p)
	if p.Active {
		t.Fatal("still active")
	}
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	if r := e.Do("POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": p.ID}); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "plan_inactive" {
		t.Fatalf("subscribe to an inactive plan: %s", r)
	}
}

func TestSubscriptionsAndInvoices(t *testing.T) {
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	p := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	var sub store.Subscription
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": c.ID, "plan_id": p.ID, "quantity": 2}, &sub)
	if sub.Status != store.Active || sub.Quantity != 2 {
		t.Fatalf("subscription = %+v", sub)
	}
	type invoice struct {
		store.Invoice
		Subtotal, Discount, Tax, Total int64
		TotalFormatted                 string `json:"total_formatted"`
		Description                    string `json:"description"`
		Customer                       *struct{ ID, Name, Email string }
	}
	var invs struct{ Items []invoice }
	e.MustDo(http.StatusOK, "GET", "/v1/invoices?customer_id="+c.ID, e.Acme.Key, nil, &invs)
	if len(invs.Items) != 1 || invs.Items[0].Total != 4000 || invs.Items[0].Status != store.InvoiceOpen || invs.Items[0].Customer == nil || invs.Items[0].Customer.Name != "Jane" {
		t.Fatalf("invoices = %+v", invs)
	}
	first := invs.Items[0]
	if !strings.HasPrefix(first.Description, "Team, ") || first.TotalFormatted != "$40.00" {
		t.Fatalf("invoice view = %+v", first)
	}

	var paid struct {
		Invoice invoice       `json:"invoice"`
		Payment store.Payment `json:"payment"`
	}
	if r := e.Do("POST", "/v1/invoices/"+first.ID+"/pay", e.Acme.Key, nil); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "no_payment_method" {
		t.Fatalf("pay without a method: %s", r)
	}
	r := e.Do("POST", "/v1/invoices/"+first.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_declined"})
	if m := r.Map(t); r.Code != http.StatusPaymentRequired || m["error"] != "payment_declined" || m["decline_code"] != "card_declined" {
		t.Fatalf("declined: %s", r)
	}
	if r := e.Do("POST", "/v1/invoices/"+first.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_gateway_down"}); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("gateway down: %s", r)
	}
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+first.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, &paid)
	if paid.Invoice.Status != store.InvoicePaid || paid.Payment.Amount != 4000 || paid.Payment.Status != store.PaymentSucceeded {
		t.Fatalf("paid = %+v", paid)
	}
	if r := e.Do("POST", "/v1/invoices/"+first.ID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}); r.Code != http.StatusConflict || r.Map(t)["error"] != "already_paid" {
		t.Fatalf("pay twice: %s", r)
	}
	if r := e.Do("POST", "/v1/invoices/"+first.ID+"/void", e.Acme.Key, nil); r.Code != http.StatusConflict {
		t.Fatalf("void a paid invoice: %s", r)
	}
	if n := len(e.Gateway.Charges()); n != 1 {
		t.Fatalf("%d charges", n)
	}

	var manual invoice
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": c.ID, "currency": "EUR", "discount_percent": 10,
		"lines": []map[string]any{{"description": "Consulting", "quantity": 3, "unit_amount": 15000}, {"description": "Travel", "quantity": 1, "unit_amount": 4250}}}, &manual)
	if manual.Subtotal != 49250 || manual.Discount != 4925 || manual.Total != 44325 || manual.Description != "Consulting and 1 more" || manual.TotalFormatted != "€443.25" {
		t.Fatalf("manual invoice = %+v", manual)
	}
	var lines struct {
		Items []struct {
			Description string
			Amount      int64
		}
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices/"+manual.ID+"/lines", e.Acme.Key, nil, &lines)
	if len(lines.Items) != 2 || lines.Items[0].Amount != 45000 {
		t.Fatalf("lines = %+v", lines)
	}
	if r := e.Do("POST", "/v1/invoices", e.Acme.Key, map[string]any{"customer_id": c.ID, "currency": "EUR", "lines": []any{}}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no lines: %s", r)
	}
	var voided invoice
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+manual.ID+"/void", e.Acme.Key, nil, &voided)
	if voided.Status != store.InvoiceVoid || voided.VoidedAt == nil {
		t.Fatalf("void = %+v", voided)
	}
	invs.Items = nil
	e.MustDo(http.StatusOK, "GET", "/v1/invoices?status=void", e.Acme.Key, nil, &invs)
	if len(invs.Items) != 1 || invs.Items[0].ID != manual.ID {
		t.Fatalf("void invoices = %+v", invs.Items)
	}
	if r := e.Do("GET", "/v1/invoices?status=lost", e.Acme.Key, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %s", r)
	}

	e.MustDo(http.StatusOK, "PATCH", "/v1/subscriptions/"+sub.ID, e.Acme.Key, map[string]any{"quantity": 5}, &sub)
	if sub.Quantity != 5 {
		t.Fatalf("quantity = %d", sub.Quantity)
	}
	if r := e.Do("POST", "/v1/subscriptions/"+sub.ID+"/usage", e.Acme.Key, map[string]any{"quantity": 3}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("usage on a licensed plan: %s", r)
	}
	e.MustDo(http.StatusOK, "POST", "/v1/subscriptions/"+sub.ID+"/cancel", e.Acme.Key, nil, &sub)
	if sub.Status != store.Canceled {
		t.Fatalf("cancel = %+v", sub)
	}
	if r := e.Do("PATCH", "/v1/subscriptions/"+sub.ID, e.Acme.Key, map[string]any{"quantity": 1}); r.Code != http.StatusConflict {
		t.Fatalf("change a canceled subscription: %s", r)
	}

	var report struct {
		Days []struct {
			Date     string
			Currency string
			Amount   int64
		}
	}
	e.MustDo(http.StatusOK, "GET", "/v1/reports/revenue?from=2026-01-01&to=2026-01-31", e.Acme.Key, nil, &report)
	if len(report.Days) != 1 || report.Days[0].Date != "2026-01-15" || report.Days[0].Amount != 4000 {
		t.Fatalf("report = %+v", report)
	}
	if r := e.Do("GET", "/v1/reports/revenue?from=2026-02-01&to=2026-01-01", e.Acme.Key, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("bad range: %s", r)
	}
}

func TestWebhookEndpoints(t *testing.T) {
	e := apitest.New(t)
	var ep store.WebhookEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/tally", "events": []string{"customer.created"}}, &ep)
	if !ep.Enabled || !strings.HasPrefix(ep.Secret, "whsec_") {
		t.Fatalf("endpoint = %+v", ep)
	}
	for _, bad := range []map[string]any{
		{"url": "https://does-not-resolve.example/", "events": []string{"*"}},
		{"url": "not a url", "events": []string{"*"}},
	} {
		if r := e.Do("POST", "/v1/webhook-endpoints", e.Acme.Key, bad); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid_url" {
			t.Fatalf("%v: %s", bad, r)
		}
	}
	if r := e.Do("POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/", "events": []string{"nope"}}); r.Code != http.StatusUnprocessableEntity || r.Map(t)["error"] != "invalid_events" {
		t.Fatalf("bad events: %s", r)
	}
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	if n, err := e.Webhooks.RunDue(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunDue = %d, %v", n, err)
	}
	if reqs := e.HTTP.Requests(); len(reqs) != 1 || !strings.Contains(string(reqs[0].Body), c.ID) {
		t.Fatalf("requests = %+v", reqs)
	}
	var ds struct{ Items []store.Delivery }
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+ep.ID+"/deliveries", e.Acme.Key, nil, &ds)
	if len(ds.Items) != 1 || ds.Items[0].Status != store.DeliverySucceeded {
		t.Fatalf("deliveries = %+v", ds)
	}
	var evs struct{ Items []store.Event }
	e.MustDo(http.StatusOK, "GET", "/v1/events?type=customer.created", e.Acme.Key, nil, &evs)
	if len(evs.Items) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, map[string]any{"enabled": false}, &ep)
	if ep.Enabled {
		t.Fatal("still enabled")
	}
	e.CreateCustomer(e.Acme, "John", "john@example.com")
	if n, _ := e.Webhooks.RunDue(context.Background()); n != 0 {
		t.Fatalf("a disabled endpoint got %d deliveries", n)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("deleted endpoint: %s", r)
	}
}

func TestTeam(t *testing.T) {
	e := apitest.New(t)
	var inv store.Invite
	e.MustDo(http.StatusCreated, "POST", "/v1/invites", e.Acme.Key, map[string]string{"email": "new@people.example", "role": "member"}, &inv)
	msgs := e.Mail.Messages()
	if len(msgs) != 1 || msgs[0].To != "new@people.example" || !strings.Contains(msgs[0].Subject, "Acme") {
		t.Fatalf("emails = %+v", msgs)
	}
	m := regexp.MustCompile(`token=([^\s]+)`).FindStringSubmatch(msgs[0].Body)
	if m == nil {
		t.Fatalf("no link in %q", msgs[0].Body)
	}
	token, _ := url.QueryUnescape(m[1])
	if r := e.Do("POST", "/v1/invites", e.Acme.Key, map[string]string{"email": "owner@acme.example", "role": "member"}); r.Code != http.StatusConflict {
		t.Fatalf("invite a member: %s", r)
	}
	var accepted struct {
		User   store.User   `json:"user"`
		APIKey store.APIKey `json:"api_key"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invites/accept", "", map[string]string{"token": token, "name": "New Person"}, &accepted)
	if accepted.User.Email != "new@people.example" || accepted.APIKey.Token == "" {
		t.Fatalf("accepted = %+v", accepted)
	}
	if r := e.Do("POST", "/v1/invites/accept", "", map[string]string{"token": "invite_nope", "name": "X"}); r.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %s", r)
	}
	newKey := accepted.APIKey.Token
	var me struct{ Role string }
	e.MustDo(http.StatusOK, "GET", "/v1/me", newKey, nil, &me)
	if me.Role != "member" {
		t.Fatalf("role = %s", me.Role)
	}
	if r := e.Do("POST", "/v1/invites", newKey, map[string]string{"email": "x@people.example", "role": "member"}); r.Code != http.StatusForbidden {
		t.Fatalf("a member invites: %s", r)
	}
	var members struct {
		Items []struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
	}
	e.MustDo(http.StatusOK, "GET", "/v1/members", e.Acme.Key, nil, &members)
	if len(members.Items) != 2 {
		t.Fatalf("members = %+v", members)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/members/"+accepted.User.ID, e.Acme.Key, map[string]string{"role": "billing"}, nil)
	e.MustDo(http.StatusOK, "GET", "/v1/me", newKey, nil, &me)
	if me.Role != "billing" {
		t.Fatalf("role after the change = %s", me.Role)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+accepted.User.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/me", newKey, nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("a removed member's key: %s", r)
	}

	var second store.Invite
	e.MustDo(http.StatusCreated, "POST", "/v1/invites", e.Acme.Key, map[string]string{"email": "later@people.example", "role": "viewer"}, &second)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+second.ID, e.Acme.Key, nil, nil)
	var pending struct{ Items []store.Invite }
	e.MustDo(http.StatusOK, "GET", "/v1/invites", e.Acme.Key, nil, &pending)
	if len(pending.Items) != 0 {
		t.Fatalf("pending invites = %+v", pending.Items)
	}
}

func TestAPIKeys(t *testing.T) {
	e := apitest.New(t)
	var k store.APIKey
	e.MustDo(http.StatusCreated, "POST", "/v1/api-keys", e.Acme.Key, map[string]string{"name": "CI"}, &k)
	if !strings.HasPrefix(k.Token, "tly_") || k.Name != "CI" {
		t.Fatalf("key = %+v", k)
	}
	e.MustDo(http.StatusOK, "GET", "/v1/org", k.Token, nil, nil)
	var keys struct{ Items []store.APIKey }
	e.MustDo(http.StatusOK, "GET", "/v1/api-keys", e.Acme.Key, nil, &keys)
	if len(keys.Items) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	if r := e.Do("DELETE", "/v1/api-keys/"+k.ID, e.Globex.Key, nil); r.Code != http.StatusNotFound {
		t.Fatalf("revoke another org's key: %s", r)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/api-keys/"+k.ID, e.Acme.Key, nil, nil)
	if r := e.Do("GET", "/v1/org", k.Token, nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %s", r)
	}
}

func TestOrgSettings(t *testing.T) {
	e := apitest.New(t)
	var o store.Org
	e.MustDo(http.StatusOK, "PATCH", "/v1/org", e.Acme.Key, map[string]any{"tax_rate_bp": 2000, "time_zone": "Asia/Tokyo"}, &o)
	if o.TaxRateBP != 2000 || o.TimeZone != "Asia/Tokyo" {
		t.Fatalf("org = %+v", o)
	}
	if r := e.Do("PATCH", "/v1/org", e.Acme.Key, map[string]any{"time_zone": "Not/AZone"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad time zone: %s", r)
	}
	_, member := e.AddMember(e.Acme, "m@acme.example", store.RoleMember)
	if r := e.Do("PATCH", "/v1/org", member, map[string]any{"name": "Hacked"}); r.Code != http.StatusForbidden {
		t.Fatalf("a member changes the org: %s", r)
	}
}

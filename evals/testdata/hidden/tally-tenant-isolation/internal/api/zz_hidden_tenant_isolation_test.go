package api_test

// Hidden acceptance test of the trial task tally-tenant-isolation (evals/tasks). The agents never see it.
//
// It checks each planted cross-organization leak through the API (Acme's objects, asked for with Globex's key), that the
// same requests still work inside one organization, and that every other endpoint keeps answering another organization's
// IDs exactly like unknown IDs.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

// hiddenUnknownID is an ID of the same kind as id that no object has.
func hiddenUnknownID(id string) string {
	prefix, _, _ := strings.Cut(id, "_")
	return prefix + "_999999"
}

// hiddenSameAnswer fails unless the answer to another organization's ID is the answer to an unknown ID: the same status and
// the same body once the two IDs are left out of it.
func hiddenSameAnswer(t *testing.T, what string, other, unknown *apitest.Response, otherID, unknownID string) {
	t.Helper()
	a := strings.TrimSpace(strings.ReplaceAll(string(other.Body), otherID, "<id>"))
	b := strings.TrimSpace(strings.ReplaceAll(string(unknown.Body), unknownID, "<id>"))
	if other.Code != unknown.Code || a != b {
		t.Errorf("%s: another organization's ID must be answered exactly like an unknown ID;\n another organization's ID (%s) got: %s\n an unknown ID (%s) got:              %s",
			what, otherID, other, unknownID, unknown)
	}
}

// hiddenWantNotFound fails unless r is 404 {"error":"not_found"}.
func hiddenWantNotFound(t *testing.T, what string, r *apitest.Response) bool {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if r.Code != http.StatusNotFound || json.Unmarshal(r.Body, &body) != nil || body.Error != "not_found" {
		t.Errorf("%s: want 404 {\"error\":\"not_found\"}, got %s", what, r)
		return false
	}
	return true
}

func hiddenIs2xx(code int) bool { return code >= 200 && code <= 299 }

type hiddenCustomerList struct {
	Items []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"items"`
}

// hiddenFindByEmail is GET /v1/customers?email= with org's key, and the IDs it found.
func hiddenFindByEmail(t *testing.T, e *apitest.Env, org apitest.Org, email string) []string {
	t.Helper()
	r := e.Do("GET", "/v1/customers?email="+email, org.Key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/customers?email=%s as %s: want 200, got %s", email, org.Name, r)
	}
	var page hiddenCustomerList
	r.JSON(t, &page)
	ids := []string{}
	for _, c := range page.Items {
		ids = append(ids, c.ID)
	}
	return ids
}

func hiddenWantIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: found %v, want %v", what, got, want)
	}
}

func hiddenCreateInvoice(t *testing.T, e *apitest.Env, org apitest.Org, customerID string, lines ...string) string {
	t.Helper()
	ls := []map[string]any{}
	for i, d := range lines {
		ls = append(ls, map[string]any{"description": d, "quantity": i + 1, "unit_amount": 1000 * (i + 1)})
	}
	var inv struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", org.Key, map[string]any{"customer_id": customerID, "currency": "USD", "lines": ls}, &inv)
	return inv.ID
}

// (a) GET /v1/invoices/{id}/lines read any organization's invoice.
func TestHiddenInvoiceLinesOfAnotherOrg(t *testing.T) {
	e := apitest.New(t)
	jane := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	invID := hiddenCreateInvoice(t, e, e.Acme, jane.ID, "Hidden secret consulting", "Hidden secret travel")

	// the organization's own invoice still answers its lines
	var lines struct {
		Items []struct {
			Description string `json:"description"`
			Amount      int64  `json:"amount"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invoices/"+invID+"/lines", e.Acme.Key, nil, &lines)
	if len(lines.Items) != 2 || lines.Items[0].Description != "Hidden secret consulting" || lines.Items[0].Amount != 1000 || lines.Items[1].Amount != 4000 {
		t.Fatalf("Acme's own invoice lines = %+v, want its 2 lines with amounts 1000 and 4000", lines.Items)
	}

	other := e.Do("GET", "/v1/invoices/"+invID+"/lines", e.Globex.Key, nil)
	hiddenWantNotFound(t, "GET /v1/invoices/{another organization's invoice}/lines", other)
	unknownID := hiddenUnknownID(invID)
	hiddenSameAnswer(t, "GET /v1/invoices/{id}/lines", other, e.Do("GET", "/v1/invoices/"+unknownID+"/lines", e.Globex.Key, nil), invID, unknownID)
}

// (b) POST /v1/subscriptions accepted a customer_id of another organization.
func TestHiddenSubscriptionForAnotherOrgsCustomer(t *testing.T) {
	e := apitest.New(t)
	acmeCustomer := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	acmePlan := e.CreatePlan(e.Acme, "Acme Team", "USD", "10.00")
	globexCustomer := e.CreateCustomer(e.Globex, "Hans", "hans@example.com")
	globexPlan := e.CreatePlan(e.Globex, "Globex Team", "EUR", "10.00")

	create := func(customerID, planID string) *apitest.Response {
		return e.Do("POST", "/v1/subscriptions", e.Globex.Key, map[string]any{"customer_id": customerID, "plan_id": planID})
	}
	other := create(acmeCustomer.ID, globexPlan.ID)
	if hiddenIs2xx(other.Code) {
		t.Errorf("POST /v1/subscriptions with another organization's customer_id was accepted: %s", other)
	}
	unknownCustomer := hiddenUnknownID(acmeCustomer.ID)
	hiddenSameAnswer(t, "POST /v1/subscriptions with customer_id", other, create(unknownCustomer, globexPlan.ID), acmeCustomer.ID, unknownCustomer)

	// a plan of another organization (already refused before the task) stays refused like an unknown one
	unknownPlan := hiddenUnknownID(acmePlan.ID)
	hiddenSameAnswer(t, "POST /v1/subscriptions with plan_id", create(globexCustomer.ID, acmePlan.ID), create(globexCustomer.ID, unknownPlan), acmePlan.ID, unknownPlan)

	for _, org := range []apitest.Org{e.Acme, e.Globex} {
		if subs := e.Store.Subscriptions(org.ID, store.SubscriptionFilter{}); len(subs) != 0 {
			t.Fatalf("refused requests created subscriptions in %s: %+v", org.Name, subs)
		}
		if invs := e.Store.ListInvoices(org.ID, store.InvoiceFilter{}); len(invs) != 0 {
			t.Fatalf("refused requests created invoices in %s: %+v", org.Name, invs)
		}
	}

	// within one organization it works as before
	var sub struct {
		ID         string `json:"id"`
		CustomerID string `json:"customer_id"`
		Status     string `json:"status"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Globex.Key, map[string]any{"customer_id": globexCustomer.ID, "plan_id": globexPlan.ID}, &sub)
	if sub.CustomerID != globexCustomer.ID || sub.Status != "active" {
		t.Fatalf("Globex's own subscription = %+v", sub)
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": acmeCustomer.ID, "plan_id": acmePlan.ID}, nil)
	if invs := e.Store.ListInvoices(e.Globex.ID, store.InvoiceFilter{SubscriptionID: sub.ID}); len(invs) != 1 || invs[0].CustomerID != globexCustomer.ID {
		t.Fatalf("the first invoice of Globex's own subscription = %+v", invs)
	}

	// changing a subscription's plan to another organization's plan is answered like an unknown plan
	patch := func(planID string) *apitest.Response {
		return e.Do("PATCH", "/v1/subscriptions/"+sub.ID, e.Globex.Key, map[string]any{"plan_id": planID})
	}
	hiddenSameAnswer(t, "PATCH /v1/subscriptions/{id} with plan_id", patch(acmePlan.ID), patch(unknownPlan), acmePlan.ID, unknownPlan)
	if got, _ := e.Store.Subscription(e.Globex.ID, sub.ID); got.PlanID != globexPlan.ID {
		t.Fatalf("the subscription's plan changed to %s", got.PlanID)
	}
}

// (e) POST /v1/invoices with another organization's customer_id answered 422 customer_org_mismatch, revealing that the
// customer exists.
func TestHiddenInvoiceForAnotherOrgsCustomer(t *testing.T) {
	e := apitest.New(t)
	acmeCustomer := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	globexCustomer := e.CreateCustomer(e.Globex, "Hans", "hans@example.com")
	create := func(customerID string) *apitest.Response {
		return e.Do("POST", "/v1/invoices", e.Globex.Key, map[string]any{"customer_id": customerID, "currency": "EUR",
			"lines": []map[string]any{{"description": "Consulting", "quantity": 1, "unit_amount": 5000}}})
	}
	other := create(acmeCustomer.ID)
	if hiddenIs2xx(other.Code) {
		t.Errorf("POST /v1/invoices with another organization's customer_id was accepted: %s", other)
	}
	unknownID := hiddenUnknownID(acmeCustomer.ID)
	hiddenSameAnswer(t, "POST /v1/invoices with customer_id", other, create(unknownID), acmeCustomer.ID, unknownID)
	for _, org := range []apitest.Org{e.Acme, e.Globex} {
		if invs := e.Store.ListInvoices(org.ID, store.InvoiceFilter{}); len(invs) != 0 {
			t.Fatalf("refused requests created invoices in %s: %+v", org.Name, invs)
		}
	}
	// within one organization it works as before
	if r := create(globexCustomer.ID); r.Code != http.StatusCreated {
		t.Fatalf("Globex invoices its own customer: want 201, got %s", r)
	}
}

// (c) Customers were indexed by email address alone, across organizations.
func TestHiddenCustomerEmailLookupIsPerOrg(t *testing.T) {
	e := apitest.New(t)
	jane := e.CreateCustomer(e.Acme, "Jane at Acme", "jane@example.com")

	// Globex does not find Acme's customer, and the answer is the one for an address nobody has
	hiddenWantIDs(t, "Globex looks up an address only Acme's customer has", hiddenFindByEmail(t, e, e.Globex, "jane@example.com"))
	hiddenSameAnswer(t, "GET /v1/customers?email=", e.Do("GET", "/v1/customers?email=jane@example.com", e.Globex.Key, nil),
		e.Do("GET", "/v1/customers?email=nobody@example.com", e.Globex.Key, nil), "jane@example.com", "nobody@example.com")
	if c, ok := e.Store.FindCustomerByEmail(e.Globex.ID, "jane@example.com"); ok {
		t.Fatalf("store.FindCustomerByEmail(Globex, Acme's customer's address) = %+v, want none", c)
	}
	for _, q := range []string{"/v1/customers?q=jane", "/v1/customers", "/v1/customers/export.csv"} {
		if r := e.MustDo(http.StatusOK, "GET", q, e.Globex.Key, nil, nil); strings.Contains(string(r.Body), jane.ID) {
			t.Fatalf("GET %s as Globex shows Acme's customer: %s", q, r)
		}
	}

	// Globex may have a customer with the same address (any case), and that does not reveal Acme's
	r := e.Do("POST", "/v1/customers", e.Globex.Key, map[string]string{"name": "Jane at Globex", "email": "JANE@Example.com"})
	if r.Code != http.StatusCreated {
		t.Fatalf("Globex creates a customer with an address Acme's customer has: want 201, got %s", r)
	}
	var globexJane store.Customer
	r.JSON(t, &globexJane)

	hiddenWantIDs(t, "Acme looks up its customer's address after Globex used it", hiddenFindByEmail(t, e, e.Acme, "jane@example.com"), jane.ID)
	hiddenWantIDs(t, "Globex looks up the address", hiddenFindByEmail(t, e, e.Globex, "Jane@example.com"), globexJane.ID)
	if c, ok := e.Store.FindCustomerByEmail(e.Acme.ID, "JANE@example.com"); !ok || c.ID != jane.ID {
		t.Fatalf("store.FindCustomerByEmail(Acme, ...) = %+v, %v, want %s", c, ok, jane.ID)
	}

	// each organization's addresses stay unique within it, ignoring case
	for _, x := range []struct {
		org   apitest.Org
		email string
	}{{e.Acme, "jane@example.com"}, {e.Acme, "JANE@EXAMPLE.COM"}, {e.Globex, "jane@example.com"}} {
		if r := e.Do("POST", "/v1/customers", x.org.Key, map[string]string{"name": "Duplicate", "email": x.email}); r.Code != http.StatusConflict {
			t.Fatalf("%s creates a second customer with %s: want 409 (the address is taken in the organization), got %s", x.org.Name, x.email, r)
		}
	}

	// a store loaded from the saved state (a restart) keeps the organizations apart
	reloaded, err := store.FromState(e.Store.Export())
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := reloaded.FindCustomerByEmail(e.Acme.ID, "jane@example.com"); !ok || c.ID != jane.ID {
		t.Fatalf("after a reload, Acme's lookup finds %+v, %v, want %s", c, ok, jane.ID)
	}
	if c, ok := reloaded.FindCustomerByEmail(e.Globex.ID, "jane@example.com"); !ok || c.ID != globexJane.ID {
		t.Fatalf("after a reload, Globex's lookup finds %+v, %v, want %s", c, ok, globexJane.ID)
	}
	for _, org := range []apitest.Org{e.Acme, e.Globex} {
		if _, err := reloaded.CreateCustomer(org.ID, "Duplicate", "jane@example.com", e.Clock.Now()); !errors.Is(err, store.ErrEmailTaken) {
			t.Fatalf("after a reload, a second customer with the address in %s: err = %v, want ErrEmailTaken", org.Name, err)
		}
	}

	// Globex changing or deleting its customer does not touch Acme's
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+globexJane.ID, e.Globex.Key, map[string]string{"email": "jane.globex@example.com"}, nil)
	hiddenWantIDs(t, "Acme's lookup after Globex changed its customer's address", hiddenFindByEmail(t, e, e.Acme, "jane@example.com"), jane.ID)
	hiddenWantIDs(t, "Globex's lookup of its old address", hiddenFindByEmail(t, e, e.Globex, "jane@example.com"))
	hiddenWantIDs(t, "Globex's lookup of its new address", hiddenFindByEmail(t, e, e.Globex, "jane.globex@example.com"), globexJane.ID)
	if r := e.Do("POST", "/v1/customers", e.Acme.Key, map[string]string{"name": "Duplicate", "email": "jane@example.com"}); r.Code != http.StatusConflict {
		t.Fatalf("Acme creates a second customer with its customer's address after Globex's change: want 409, got %s", r)
	}
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+globexJane.ID, e.Globex.Key, map[string]string{"email": "jane@example.com"}, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/customers/"+globexJane.ID, e.Globex.Key, nil, nil)
	hiddenWantIDs(t, "Acme's lookup after Globex deleted its customer", hiddenFindByEmail(t, e, e.Acme, "jane@example.com"), jane.ID)
	hiddenWantIDs(t, "Globex's lookup after deleting its customer", hiddenFindByEmail(t, e, e.Globex, "jane@example.com"))
	if r := e.Do("POST", "/v1/customers", e.Acme.Key, map[string]string{"name": "Duplicate", "email": "Jane@Example.com"}); r.Code != http.StatusConflict {
		t.Fatalf("Acme creates a second customer with its customer's address after Globex deleted its own: want 409, got %s", r)
	}
	globexJane2 := e.CreateCustomer(e.Globex, "Jane again", "jane@example.com") // free again in Globex

	// and the other way round
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+jane.ID, e.Acme.Key, map[string]string{"email": "jane.acme@example.com"}, nil)
	hiddenWantIDs(t, "Acme's lookup of its customer's old address", hiddenFindByEmail(t, e, e.Acme, "jane@example.com"))
	hiddenWantIDs(t, "Acme's lookup of its customer's new address", hiddenFindByEmail(t, e, e.Acme, "jane.acme@example.com"), jane.ID)
	hiddenWantIDs(t, "Globex's lookup after Acme changed its customer's address", hiddenFindByEmail(t, e, e.Globex, "jane@example.com"), globexJane2.ID)
	e.CreateCustomer(e.Acme, "New Jane", "jane@example.com") // free again in Acme
}

// (d) DELETE /v1/webhook-endpoints/{id} deleted any organization's endpoint.
func TestHiddenDeleteAnotherOrgsWebhookEndpoint(t *testing.T) {
	e := apitest.New(t)
	var ep store.WebhookEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/hidden", "events": []string{"*"}}, &ep)
	e.CreateCustomer(e.Acme, "Jane", "jane@example.com") // queues a delivery to the endpoint

	other := e.Do("DELETE", "/v1/webhook-endpoints/"+ep.ID, e.Globex.Key, nil)
	hiddenWantNotFound(t, "DELETE /v1/webhook-endpoints/{another organization's endpoint}", other)
	unknownID := hiddenUnknownID(ep.ID)
	hiddenSameAnswer(t, "DELETE /v1/webhook-endpoints/{id}", other, e.Do("DELETE", "/v1/webhook-endpoints/"+unknownID, e.Globex.Key, nil), ep.ID, unknownID)

	var got store.WebhookEndpoint
	if r := e.Do("GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil); r.Code != http.StatusOK {
		t.Fatalf("Acme's endpoint after Globex tried to delete it: want 200, got %s", r)
	} else {
		r.JSON(t, &got)
	}
	if got.URL != ep.URL || !got.Enabled {
		t.Fatalf("Acme's endpoint changed: %+v", got)
	}
	// its pending delivery is still there and goes out
	if _, err := e.Webhooks.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reqs := e.HTTP.Requests(); len(reqs) != 1 || reqs[0].URL != ep.URL {
		t.Fatalf("the pending delivery to Acme's endpoint was not sent: requests %+v", reqs)
	}

	// an organization still deletes its own endpoint
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, nil)
	hiddenWantNotFound(t, "GET a deleted endpoint", e.Do("GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil))
}

// hiddenOrgData is everything of one organization the store holds, by kind, for comparing before and after.
func hiddenOrgData(t *testing.T, e *apitest.Env, orgID string) map[string]string {
	t.Helper()
	type key struct {
		ID, UserID string
		Revoked    bool
	}
	keys := []key{}
	for _, k := range e.Store.APIKeys(orgID) {
		keys = append(keys, key{k.ID, k.UserID, k.RevokedAt != nil})
	}
	endpoints := e.Store.WebhookEndpoints(orgID)
	deliveries := [][]store.Delivery{}
	for _, ep := range endpoints {
		deliveries = append(deliveries, e.Store.Deliveries(orgID, ep.ID))
	}
	org, _ := e.Store.Org(orgID)
	out := map[string]string{}
	for name, v := range map[string]any{"organization": org, "customers": e.Store.Customers(orgID), "plans": e.Store.Plans(orgID),
		"subscriptions": e.Store.Subscriptions(orgID, store.SubscriptionFilter{}), "invoices": e.Store.ListInvoices(orgID, store.InvoiceFilter{}),
		"payments": e.Store.Payments(orgID, ""), "webhook endpoints": endpoints, "deliveries": deliveries, "events": e.Store.Events(orgID, ""),
		"members": e.Store.Members(orgID), "invites": e.Store.Invites(orgID), "API keys": keys} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(b)
	}
	return out
}

// TestHiddenOtherOrgIDsAnswerLikeUnknownIDs is the regression sweep: every endpoint that names an object in its path answers
// another organization's ID with 404 not_found, exactly like an unknown ID, and changes nothing; IDs in a query or body get
// the answer an unknown ID gets; lists show only the caller's organization's objects.
func TestHiddenOtherOrgIDsAnswerLikeUnknownIDs(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()

	// Acme's objects
	jane := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	plan := e.CreatePlan(e.Acme, "Acme Team", "USD", "20.00")
	var sub store.Subscription
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": jane.ID, "plan_id": plan.ID}, &sub)
	metered, err := e.Store.CreatePlan(store.Plan{OrgID: e.Acme.ID, Name: "Acme API calls", Currency: "USD", UnitAmount: 2, Interval: store.Monthly, UsageType: store.Metered}, e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	var meteredSub store.Subscription
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Acme.Key, map[string]any{"customer_id": jane.ID, "plan_id": metered.ID}, &meteredSub)
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions/"+meteredSub.ID+"/usage", e.Acme.Key, map[string]any{"quantity": 5}, nil)
	invID := hiddenCreateInvoice(t, e, e.Acme, jane.ID, "Acme consulting")
	paidID := hiddenCreateInvoice(t, e, e.Acme, jane.ID, "Acme training")
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+paidID+"/pay", e.Acme.Key, map[string]string{"payment_method": "pm_card_visa"}, nil)
	var ep store.WebhookEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/hidden", "events": []string{"*"}}, &ep)
	e.CreateCustomer(e.Acme, "John", "john@example.com")
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	events := e.Store.Events(e.Acme.ID, "")
	if len(events) == 0 {
		t.Fatal("Acme has no events")
	}
	evt := events[len(events)-1]
	member, memberKey := e.AddMember(e.Acme, "hidden.member@acme.example", store.RoleMember)
	memberAPIKey, ok := e.Store.APIKeyByToken(memberKey)
	if !ok {
		t.Fatal("the member's key is not found")
	}
	var invite struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invites", e.Acme.Key, map[string]string{"email": "hidden.invitee@people.example", "role": "viewer"}, &invite)

	// Globex's own objects, so that its lists are not empty
	hans := e.CreateCustomer(e.Globex, "Hans", "hans@example.com")
	globexPlan := e.CreatePlan(e.Globex, "Globex Team", "EUR", "10.00")
	var globexSub store.Subscription
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", e.Globex.Key, map[string]any{"customer_id": hans.ID, "plan_id": globexPlan.ID}, &globexSub)

	before := hiddenOrgData(t, e, e.Acme.ID)
	charges := len(e.Gateway.Calls())
	mails := len(e.Mail.Messages())

	for _, x := range []struct {
		method, path, id string
		body             any
	}{
		{"GET", "/v1/customers/{id}", jane.ID, nil},
		{"PATCH", "/v1/customers/{id}", jane.ID, map[string]any{"name": "Taken over", "metadata": map[string]string{"x": "y"}}},
		{"DELETE", "/v1/customers/{id}", jane.ID, nil},
		{"GET", "/v1/plans/{id}", plan.ID, nil},
		{"PATCH", "/v1/plans/{id}", plan.ID, map[string]any{"name": "Taken over", "active": false}},
		{"GET", "/v1/subscriptions/{id}", sub.ID, nil},
		{"PATCH", "/v1/subscriptions/{id}", sub.ID, map[string]any{"quantity": 7}},
		{"POST", "/v1/subscriptions/{id}/cancel", sub.ID, nil},
		{"POST", "/v1/subscriptions/{id}/usage", meteredSub.ID, map[string]any{"quantity": 3}},
		{"GET", "/v1/invoices/{id}", invID, nil},
		{"GET", "/v1/invoices/{id}/lines", invID, nil},
		{"POST", "/v1/invoices/{id}/pay", invID, map[string]string{"payment_method": "pm_card_visa"}},
		{"POST", "/v1/invoices/{id}/void", invID, nil},
		{"GET", "/v1/webhook-endpoints/{id}", ep.ID, nil},
		{"PATCH", "/v1/webhook-endpoints/{id}", ep.ID, map[string]any{"enabled": false}},
		{"DELETE", "/v1/webhook-endpoints/{id}", ep.ID, nil},
		{"GET", "/v1/webhook-endpoints/{id}/deliveries", ep.ID, nil},
		{"GET", "/v1/events/{id}", evt.ID, nil},
		{"PATCH", "/v1/members/{id}", member.ID, map[string]string{"role": "viewer"}},
		{"DELETE", "/v1/members/{id}", member.ID, nil},
		{"DELETE", "/v1/invites/{id}", invite.ID, nil},
		{"DELETE", "/v1/api-keys/{id}", memberAPIKey.ID, nil},
	} {
		what := x.method + " " + x.path
		unknownID := hiddenUnknownID(x.id)
		other := e.Do(x.method, strings.Replace(x.path, "{id}", x.id, 1), e.Globex.Key, x.body)
		if !hiddenWantNotFound(t, what+" with another organization's ID", other) {
			continue
		}
		unknown := e.Do(x.method, strings.Replace(x.path, "{id}", unknownID, 1), e.Globex.Key, x.body)
		hiddenSameAnswer(t, what, other, unknown, x.id, unknownID)
	}

	// IDs named in a query get the answer an unknown ID gets, and show nothing of Acme's
	for _, x := range []struct{ path, id string }{
		{"/v1/invoices?customer_id=", jane.ID},
		{"/v1/invoices?subscription_id=", sub.ID},
		{"/v1/subscriptions?customer_id=", jane.ID},
		{"/v1/payments?invoice_id=", paidID},
	} {
		unknownID := hiddenUnknownID(x.id)
		other := e.Do("GET", x.path+x.id, e.Globex.Key, nil)
		hiddenSameAnswer(t, "GET "+x.path, other, e.Do("GET", x.path+unknownID, e.Globex.Key, nil), x.id, unknownID)
	}

	// an ID named in a body gets the answer an unknown ID gets
	patchPlan := func(planID string) *apitest.Response {
		return e.Do("PATCH", "/v1/subscriptions/"+globexSub.ID, e.Globex.Key, map[string]any{"plan_id": planID})
	}
	hiddenSameAnswer(t, "PATCH /v1/subscriptions/{id} with another organization's plan_id", patchPlan(plan.ID), patchPlan(hiddenUnknownID(plan.ID)), plan.ID, hiddenUnknownID(plan.ID))

	// Globex's lists show nothing of Acme's
	acmeIDs := []string{jane.ID, plan.ID, metered.ID, sub.ID, meteredSub.ID, invID, paidID, ep.ID, evt.ID, member.ID, memberAPIKey.ID, invite.ID}
	for _, path := range []string{"/v1/customers", "/v1/customers?q=example", "/v1/customers/export.csv", "/v1/plans", "/v1/subscriptions",
		"/v1/invoices", "/v1/payments", "/v1/webhook-endpoints", "/v1/events", "/v1/members", "/v1/invites", "/v1/api-keys"} {
		r := e.MustDo(http.StatusOK, "GET", path, e.Globex.Key, nil, nil)
		for _, id := range acmeIDs {
			if strings.Contains(string(r.Body), id) {
				t.Errorf("GET %s as Globex shows Acme's %s: %s", path, id, r)
			}
		}
	}

	for kind, after := range hiddenOrgData(t, e, e.Acme.ID) {
		if after != before[kind] {
			t.Errorf("Globex's requests changed Acme's %s:\nbefore: %s\nafter:  %s", kind, before[kind], after)
		}
	}
	if n := len(e.Gateway.Calls()); n != charges {
		t.Errorf("Globex's requests made %d charges", n-charges)
	}
	if n := len(e.Mail.Messages()); n != mails {
		t.Errorf("Globex's requests sent %d emails", n-mails)
	}

	// Acme still reaches all of its own objects
	for _, path := range []string{"/v1/customers/" + jane.ID, "/v1/plans/" + plan.ID, "/v1/subscriptions/" + sub.ID, "/v1/invoices/" + invID,
		"/v1/invoices/" + invID + "/lines", "/v1/webhook-endpoints/" + ep.ID, "/v1/webhook-endpoints/" + ep.ID + "/deliveries", "/v1/events/" + evt.ID} {
		if r := e.Do("GET", path, e.Acme.Key, nil); r.Code != http.StatusOK {
			t.Errorf("GET %s as Acme: want 200, got %s", path, r)
		}
	}
	if r := e.Do("GET", "/v1/me", memberKey, nil); r.Code != http.StatusOK {
		t.Errorf("Acme's member's key after Globex tried to revoke it: want 200, got %s", r)
	}
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+invID+"/void", e.Acme.Key, nil, nil)
}

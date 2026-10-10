package api_test

// Hidden acceptance test of the trial task tally-role-permissions (evals/tasks). The agents never see it.
//
// Every role runs every action of the task's table against real objects of its organization: an allowed request must
// succeed (a 2xx answer), a refused one must answer 403 {"error":"forbidden"} and change nothing. Then the rules for members,
// roles, the last owner and API keys.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
)

var (
	hiddenRPOwner   = store.RoleOwner
	hiddenRPAdmin   = store.RoleAdmin
	hiddenRPBilling = store.RoleBilling
	hiddenRPMember  = store.RoleMember
	hiddenRPViewer  = store.RoleViewer

	hiddenRPEveryone = []store.Role{hiddenRPOwner, hiddenRPAdmin, hiddenRPBilling, hiddenRPMember, hiddenRPViewer}
	hiddenRPWriters  = []store.Role{hiddenRPOwner, hiddenRPAdmin, hiddenRPBilling, hiddenRPMember}
	hiddenRPFinance  = []store.Role{hiddenRPOwner, hiddenRPAdmin, hiddenRPBilling}
	hiddenRPManagers = []store.Role{hiddenRPOwner, hiddenRPAdmin}
)

// hiddenRPState is what the store holds (by kind, without the keys' last use times) and what went out (charges, emails),
// to tell whether a refused request changed anything.
func hiddenRPState(t testing.TB, e *apitest.Env) map[string]string {
	t.Helper()
	st := e.Store.Export()
	st.Seq = 0
	for i := range st.APIKeys {
		st.APIKeys[i].LastUsedAt = nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var parts map[string]json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{"gateway charges": strconv.Itoa(len(e.Gateway.Calls())), "emails sent": strconv.Itoa(len(e.Mail.Messages()))}
	for k, v := range parts {
		out[k] = string(v)
	}
	return out
}

// hiddenRPChanges lists what differs between two states: objects added, removed or changed, by kind.
func hiddenRPChanges(before, after map[string]string) []string {
	var out []string
	for kind, a := range after {
		if b := before[kind]; b != a {
			out = append(out, hiddenRPDiff(kind, b, a)...)
		}
	}
	sort.Strings(out)
	return out
}

func hiddenRPShort(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

func hiddenRPDiff(kind, before, after string) []string {
	var b, a []json.RawMessage
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		return []string{fmt.Sprintf("%s: %s, then %s", kind, hiddenRPShort(before), hiddenRPShort(after))}
	}
	key := func(raw json.RawMessage) string {
		var obj struct {
			ID     string `json:"id"`
			OrgID  string `json:"org_id"`
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal(raw, &obj)
		switch {
		case obj.ID != "":
			return obj.ID
		case obj.UserID != "":
			return obj.OrgID + "/" + obj.UserID
		}
		return string(raw)
	}
	old := map[string]string{}
	for _, raw := range b {
		old[key(raw)] = string(raw)
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range a {
		k := key(raw)
		seen[k] = true
		switch o, ok := old[k]; {
		case !ok:
			out = append(out, fmt.Sprintf("%s: added %s", kind, hiddenRPShort(string(raw))))
		case o != string(raw):
			out = append(out, fmt.Sprintf("%s: changed %s\n      to %s", kind, hiddenRPShort(o), hiddenRPShort(string(raw))))
		}
	}
	for _, raw := range b {
		if !seen[key(raw)] {
			out = append(out, fmt.Sprintf("%s: removed %s", kind, hiddenRPShort(string(raw))))
		}
	}
	return out
}

func hiddenRPErrorCode(r *apitest.Response) string {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &body)
	return body.Error
}

// hiddenRPAllowed sends a request the caller's role may make: it must succeed.
func hiddenRPAllowed(t testing.TB, e *apitest.Env, what, method, path, key string, body any) *apitest.Response {
	t.Helper()
	r := e.Do(method, path, key, body)
	if r.Code < 200 || r.Code > 299 {
		t.Errorf("%s (%s %s): allowed, so want a 2xx answer, got %s", what, method, path, r)
	}
	return r
}

// hiddenRPRefused sends a request that must be refused with one of the answers in want (status and error code), and must
// change nothing.
func hiddenRPRefused(t testing.TB, e *apitest.Env, want []string, what, method, path, key string, body any) *apitest.Response {
	t.Helper()
	before := hiddenRPState(t, e)
	r := e.Do(method, path, key, body)
	got := fmt.Sprintf("%d %s", r.Code, hiddenRPErrorCode(r))
	if !slices.Contains(want, got) {
		t.Errorf("%s (%s %s): want %s, got %s", what, method, path, strings.Join(want, " or "), r)
	}
	if changes := hiddenRPChanges(before, hiddenRPState(t, e)); len(changes) > 0 {
		t.Errorf("%s (%s %s) must change nothing, but (answered %d) it changed:\n  %s", what, method, path, r.Code, strings.Join(changes, "\n  "))
	}
	return r
}

// hiddenRPStop makes the helpers stop a test at its first failure: the scenario tests build each step on the last one.
type hiddenRPStop struct{ *testing.T }

func (s hiddenRPStop) Errorf(format string, args ...any) {
	s.T.Helper()
	s.T.Fatalf(format, args...)
}

var (
	hiddenRPForbidden = []string{"403 forbidden"}
	hiddenRPLastOwner = []string{"409 last_owner"}
)

// hiddenRPWorks fails unless key still works (every role may read the organization).
func hiddenRPWorks(t testing.TB, e *apitest.Env, what, key string) {
	t.Helper()
	if r := e.Do("GET", "/v1/org", key, nil); r.Code != http.StatusOK {
		t.Errorf("%s: want the key to work (GET /v1/org 200), got %s", what, r)
	}
}

// hiddenRPDead fails unless key no longer works.
func hiddenRPDead(t testing.TB, e *apitest.Env, what, key string) {
	t.Helper()
	if r := e.Do("GET", "/v1/org", key, nil); r.Code != http.StatusUnauthorized {
		t.Errorf("%s: want the key to stop working (GET /v1/org 401), got %s", what, r)
	}
}

func hiddenRPRole(t testing.TB, e *apitest.Env, userID string) store.Role {
	t.Helper()
	m, ok := e.Store.Member(e.Acme.ID, userID)
	if !ok {
		return ""
	}
	return m.Role
}

// hiddenRPOrg is one role's run of the matrix: a member with that role in Acme, and real objects to act on.
type hiddenRPOrg struct {
	t    *testing.T
	e    *apitest.Env
	role store.Role
	user store.User
	key  string
	n    int

	customer          store.Customer
	plan, meteredPlan store.Plan
	sub, meteredSub   store.Subscription
	invoice           store.Invoice
	endpoint          store.WebhookEndpoint
	event             store.Event
	invite            store.Invite
	paidInvoice       store.Invoice
}

func (x *hiddenRPOrg) next() int { x.n++; return x.n }

func (x *hiddenRPOrg) newCustomer() store.Customer {
	x.t.Helper()
	n := x.next()
	c, err := x.e.Store.CreateCustomer(x.e.Acme.ID, fmt.Sprintf("Customer %d", n), fmt.Sprintf("customer%d@example.com", n), x.e.Clock.Now())
	if err != nil {
		x.t.Fatal(err)
	}
	return c
}

func (x *hiddenRPOrg) newPlan(usage store.UsageType) store.Plan {
	x.t.Helper()
	p, err := x.e.Store.CreatePlan(store.Plan{OrgID: x.e.Acme.ID, Name: fmt.Sprintf("Plan %d", x.next()), Currency: money.USD, UnitAmount: 1200,
		Interval: store.Monthly, UsageType: usage}, x.e.Clock.Now())
	if err != nil {
		x.t.Fatal(err)
	}
	return p
}

func (x *hiddenRPOrg) newSub(planID string) store.Subscription {
	x.t.Helper()
	sub, err := x.e.Subs.Create(context.Background(), x.e.Acme.ID, subscriptions.CreateInput{CustomerID: x.customer.ID, PlanID: planID, Quantity: 1})
	if err != nil {
		x.t.Fatal(err)
	}
	return sub
}

func (x *hiddenRPOrg) newInvoice() store.Invoice {
	x.t.Helper()
	inv, err := x.e.Store.CreateInvoice(store.Invoice{OrgID: x.e.Acme.ID, CustomerID: x.customer.ID, Currency: money.USD,
		Lines: []store.Line{{Description: fmt.Sprintf("Work %d", x.next()), Quantity: 1, UnitAmount: 1500}}}, x.e.Clock.Now())
	if err != nil {
		x.t.Fatal(err)
	}
	return inv
}

func (x *hiddenRPOrg) newEndpoint() store.WebhookEndpoint {
	x.t.Helper()
	ep, err := x.e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: x.e.Acme.ID, URL: fmt.Sprintf("https://hooks.acme.example/hidden/%d", x.next()),
		Events: []string{"*"}, Secret: "whsec_hidden", Enabled: true}, x.e.Clock.Now())
	if err != nil {
		x.t.Fatal(err)
	}
	return ep
}

func (x *hiddenRPOrg) newInvite() store.Invite {
	x.t.Helper()
	inv, err := x.e.Store.CreateInvite(x.e.Acme.ID, fmt.Sprintf("invitee%d@people.example", x.next()), store.RoleMember, x.e.Acme.Owner.ID, x.e.Clock.Now())
	if err != nil {
		x.t.Fatal(err)
	}
	return inv
}

func (x *hiddenRPOrg) newMember(role store.Role) store.User {
	x.t.Helper()
	u, _ := x.e.AddMember(x.e.Acme, fmt.Sprintf("target%d@acme.example", x.next()), role)
	return u
}

func hiddenRPSetup(t *testing.T, role store.Role) *hiddenRPOrg {
	t.Helper()
	e := apitest.New(t)
	x := &hiddenRPOrg{t: t, e: e, role: role}
	if role == store.RoleOwner {
		x.user, x.key = e.Acme.Owner, e.Acme.Key
	} else {
		x.user, x.key = e.AddMember(e.Acme, "hidden."+string(role)+"@acme.example", role)
	}
	ctx := context.Background()
	x.endpoint = x.newEndpoint()
	x.customer = x.newCustomer()
	x.plan = x.newPlan(store.Licensed)
	x.meteredPlan = x.newPlan(store.Metered)
	x.sub = x.newSub(x.plan.ID)
	x.meteredSub = x.newSub(x.meteredPlan.ID)
	x.invoice = x.newInvoice()
	x.paidInvoice = x.newInvoice()
	if _, _, err := e.Payments.Pay(ctx, e.Acme.ID, x.paidInvoice.ID, "pm_card_visa"); err != nil {
		t.Fatal(err)
	}
	if err := e.Webhooks.Publish(ctx, e.Acme.ID, "customer.created", map[string]string{"id": x.customer.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	evs := e.Store.Events(e.Acme.ID, "")
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	x.event = evs[len(evs)-1]
	x.invite = x.newInvite()
	return x
}

type hiddenRPAction struct {
	name    string
	allowed []store.Role
	req     func(x *hiddenRPOrg) (method, path string, body any)
}

func hiddenRPGet(path string) func(x *hiddenRPOrg) (string, string, any) {
	return func(*hiddenRPOrg) (string, string, any) { return "GET", path, nil }
}

var hiddenRPActions = []hiddenRPAction{
	// read customers, plans, subscriptions, invoices, payments: everyone
	{"list customers", hiddenRPEveryone, hiddenRPGet("/v1/customers")},
	{"search customers", hiddenRPEveryone, hiddenRPGet("/v1/customers?q=customer")},
	{"read a customer", hiddenRPEveryone, func(x *hiddenRPOrg) (string, string, any) { return "GET", "/v1/customers/" + x.customer.ID, nil }},
	{"export customers", hiddenRPEveryone, hiddenRPGet("/v1/customers/export.csv")},
	{"list plans", hiddenRPEveryone, hiddenRPGet("/v1/plans")},
	{"read a plan", hiddenRPEveryone, func(x *hiddenRPOrg) (string, string, any) { return "GET", "/v1/plans/" + x.plan.ID, nil }},
	{"list subscriptions", hiddenRPEveryone, hiddenRPGet("/v1/subscriptions")},
	{"read a subscription", hiddenRPEveryone, func(x *hiddenRPOrg) (string, string, any) { return "GET", "/v1/subscriptions/" + x.sub.ID, nil }},
	{"list invoices", hiddenRPEveryone, hiddenRPGet("/v1/invoices")},
	{"read an invoice", hiddenRPEveryone, func(x *hiddenRPOrg) (string, string, any) { return "GET", "/v1/invoices/" + x.invoice.ID, nil }},
	{"read an invoice's lines", hiddenRPEveryone, func(x *hiddenRPOrg) (string, string, any) {
		return "GET", "/v1/invoices/" + x.invoice.ID + "/lines", nil
	}},
	{"list payments", hiddenRPEveryone, hiddenRPGet("/v1/payments")},

	// create, update or delete customers: all but viewers
	{"create a customer", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		n := x.next()
		return "POST", "/v1/customers", map[string]string{"name": fmt.Sprintf("New %d", n), "email": fmt.Sprintf("new%d@example.com", n)}
	}},
	{"update a customer", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/customers/" + x.customer.ID, map[string]string{"name": fmt.Sprintf("Renamed %d", x.next())}
	}},
	{"delete a customer", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "DELETE", "/v1/customers/" + x.newCustomer().ID, nil
	}},

	// create or update plans: owner, admin, billing
	{"create a plan", hiddenRPFinance, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/plans", map[string]any{"name": fmt.Sprintf("New plan %d", x.next()), "currency": "USD", "price": "9.00", "interval": "month"}
	}},
	{"update a plan", hiddenRPFinance, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/plans/" + x.plan.ID, map[string]any{"name": fmt.Sprintf("Renamed plan %d", x.next())}
	}},

	// create, update or cancel subscriptions, report usage: all but viewers
	{"create a subscription", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/subscriptions", map[string]any{"customer_id": x.customer.ID, "plan_id": x.plan.ID, "quantity": 2}
	}},
	{"update a subscription", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/subscriptions/" + x.sub.ID, map[string]any{"quantity": 2 + x.next()}
	}},
	{"cancel a subscription", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/subscriptions/" + x.newSub(x.plan.ID).ID + "/cancel", nil
	}},
	{"report usage", hiddenRPWriters, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/subscriptions/" + x.meteredSub.ID + "/usage", map[string]any{"quantity": 3}
	}},

	// create, pay or void invoices: owner, admin, billing
	{"create an invoice", hiddenRPFinance, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/invoices", map[string]any{"customer_id": x.customer.ID, "currency": "USD",
			"lines": []map[string]any{{"description": "Consulting", "quantity": 1, "unit_amount": 5000}}}
	}},
	{"pay an invoice", hiddenRPFinance, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/invoices/" + x.newInvoice().ID + "/pay", map[string]string{"payment_method": "pm_card_visa"}
	}},
	{"void an invoice", hiddenRPFinance, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/invoices/" + x.newInvoice().ID + "/void", nil
	}},

	// the revenue report: owner, admin, billing
	{"read the revenue report", hiddenRPFinance, hiddenRPGet("/v1/reports/revenue?from=2026-01-01&to=2026-01-31")},

	// webhook endpoints, their deliveries, and events: owner, admin
	{"list webhook endpoints", hiddenRPManagers, hiddenRPGet("/v1/webhook-endpoints")},
	{"create a webhook endpoint", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/webhook-endpoints", map[string]any{"url": fmt.Sprintf("https://hooks.acme.example/new/%d", x.next()), "events": []string{"*"}}
	}},
	{"read a webhook endpoint", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "GET", "/v1/webhook-endpoints/" + x.endpoint.ID, nil
	}},
	{"update a webhook endpoint", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/webhook-endpoints/" + x.newEndpoint().ID, map[string]any{"enabled": false}
	}},
	{"delete a webhook endpoint", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "DELETE", "/v1/webhook-endpoints/" + x.newEndpoint().ID, nil
	}},
	{"list an endpoint's deliveries", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "GET", "/v1/webhook-endpoints/" + x.endpoint.ID + "/deliveries", nil
	}},
	{"list events", hiddenRPManagers, hiddenRPGet("/v1/events")},
	{"read an event", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) { return "GET", "/v1/events/" + x.event.ID, nil }},

	// read the organization, members and pending invites: everyone
	{"read the organization", hiddenRPEveryone, hiddenRPGet("/v1/org")},
	{"list members", hiddenRPEveryone, hiddenRPGet("/v1/members")},
	{"list pending invites", hiddenRPEveryone, hiddenRPGet("/v1/invites")},

	// update the organization: owner, admin
	{"update the organization", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/org", map[string]any{"name": fmt.Sprintf("Acme %d", x.next())}
	}},

	// invite, revoke invites, change roles, remove members: owner, admin
	{"invite someone", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "POST", "/v1/invites", map[string]string{"email": fmt.Sprintf("new%d@people.example", x.next()), "role": "member"}
	}},
	{"revoke an invite", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) { return "DELETE", "/v1/invites/" + x.newInvite().ID, nil }},
	{"change a member's role", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "PATCH", "/v1/members/" + x.newMember(store.RoleMember).ID, map[string]string{"role": "billing"}
	}},
	{"remove a member", hiddenRPManagers, func(x *hiddenRPOrg) (string, string, any) {
		return "DELETE", "/v1/members/" + x.newMember(store.RoleMember).ID, nil
	}},
}

// TestHiddenRoleMatrix: every role against every action of the table.
func TestHiddenRoleMatrix(t *testing.T) {
	for _, role := range hiddenRPEveryone {
		t.Run(string(role), func(t *testing.T) {
			x := hiddenRPSetup(t, role)
			for _, a := range hiddenRPActions {
				method, path, body := a.req(x)
				what := fmt.Sprintf("%s may %s%s", role, map[bool]string{true: "", false: "not "}[slices.Contains(a.allowed, role)], a.name)
				if slices.Contains(a.allowed, role) {
					hiddenRPAllowed(t, x.e, what, method, path, x.key, body)
				} else {
					hiddenRPRefused(t, x.e, hiddenRPForbidden, what, method, path, x.key, body)
				}
			}
		})
	}
}

// TestHiddenRoleChangesApplyAtOnce: the role is read on every request, so a member's existing key follows a role change.
func TestHiddenRoleChangesApplyAtOnce(tt *testing.T) {
	t := hiddenRPStop{tt}
	e := apitest.New(t)
	u, key := e.AddMember(e.Acme, "changing@acme.example", store.RoleBilling)
	setRole := func(role string) {
		t.Helper()
		e.MustDo(http.StatusOK, "PATCH", "/v1/members/"+u.ID, e.Acme.Key, map[string]string{"role": role}, nil)
	}
	hiddenRPAllowed(t, e, "billing creates a plan", "POST", "/v1/plans", key, map[string]any{"name": "B", "currency": "USD", "price": "1.00", "interval": "month"})
	setRole("viewer")
	hiddenRPRefused(t, e, hiddenRPForbidden, "the same key as a viewer creates a customer", "POST", "/v1/customers", key, map[string]string{"name": "V", "email": "v@example.com"})
	hiddenRPRefused(t, e, hiddenRPForbidden, "the same key as a viewer reads the revenue report", "GET", "/v1/reports/revenue?from=2026-01-01&to=2026-01-31", key, nil)
	setRole("admin")
	hiddenRPAllowed(t, e, "the same key as an admin creates a webhook endpoint", "POST", "/v1/webhook-endpoints", key, map[string]any{"url": "https://hooks.acme.example/x", "events": []string{"*"}})
	setRole("member")
	hiddenRPRefused(t, e, hiddenRPForbidden, "the same key as a member lists webhook endpoints", "GET", "/v1/webhook-endpoints", key, nil)
	hiddenRPRefused(t, e, hiddenRPForbidden, "the same key as a member creates a plan", "POST", "/v1/plans", key, map[string]any{"name": "M", "currency": "USD", "price": "1.00", "interval": "month"})
	hiddenRPAllowed(t, e, "the same key as a member creates a customer", "POST", "/v1/customers", key, map[string]string{"name": "M", "email": "m@example.com"})
}

// TestHiddenRoleMemberRules: only an owner gives the owner or admin role or changes or removes owners and admins; nobody
// changes their own role.
func TestHiddenRoleMemberRules(tt *testing.T) {
	t := hiddenRPStop{tt}
	e := apitest.New(t)
	owner, ownerKey := e.Acme.Owner, e.Acme.Key
	owner2, owner2Key := e.AddMember(e.Acme, "owner2@acme.example", store.RoleOwner)
	admin, adminKey := e.AddMember(e.Acme, "admin@acme.example", store.RoleAdmin)
	admin2, admin2Key := e.AddMember(e.Acme, "admin2@acme.example", store.RoleAdmin)
	billing, billingKey := e.AddMember(e.Acme, "billing@acme.example", store.RoleBilling)
	member, memberKey := e.AddMember(e.Acme, "member@acme.example", store.RoleMember)
	viewer, viewerKey := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	role := func(r string) map[string]string { return map[string]string{"role": r} }

	// only an owner gives the owner or admin role
	for _, r := range []string{"admin", "owner"} {
		hiddenRPRefused(t, e, hiddenRPForbidden, "an admin gives a member the "+r+" role", "PATCH", "/v1/members/"+member.ID, adminKey, role(r))
		hiddenRPRefused(t, e, hiddenRPForbidden, "an admin gives a viewer the "+r+" role", "PATCH", "/v1/members/"+viewer.ID, adminKey, role(r))
	}
	// only an owner changes the role of, or removes, an owner or an admin (there are two owners, so neither is the only one)
	for _, target := range []struct {
		name string
		user store.User
	}{{"an owner", owner}, {"another owner", owner2}, {"another admin", admin2}} {
		hiddenRPRefused(t, e, hiddenRPForbidden, "an admin changes the role of "+target.name, "PATCH", "/v1/members/"+target.user.ID, adminKey, role("billing"))
		hiddenRPRefused(t, e, hiddenRPForbidden, "an admin removes "+target.name, "DELETE", "/v1/members/"+target.user.ID, adminKey, nil)
	}
	hiddenRPWorks(t, e, "the owners' and the other admin's keys after the refused changes", owner2Key)
	hiddenRPWorks(t, e, "the other admin's key after the refused changes", admin2Key)

	// nobody changes their own role, whatever the role and whatever the new role
	for _, self := range []struct {
		name string
		user store.User
		key  string
		to   []string
	}{
		{"an owner (with another owner)", owner, ownerKey, []string{"admin", "viewer"}},
		{"an admin", admin, adminKey, []string{"owner", "member"}},
		{"a billing member", billing, billingKey, []string{"admin", "viewer"}},
		{"a member", member, memberKey, []string{"owner", "billing"}},
		{"a viewer", viewer, viewerKey, []string{"admin", "member"}},
	} {
		for _, to := range self.to {
			hiddenRPRefused(t, e, hiddenRPForbidden, self.name+" changes their own role to "+to, "PATCH", "/v1/members/"+self.user.ID, self.key, role(to))
		}
	}

	// an admin changes roles among billing, member and viewer, and removes such members
	hiddenRPAllowed(t, e, "an admin makes a billing member a viewer", "PATCH", "/v1/members/"+billing.ID, adminKey, role("viewer"))
	if got := hiddenRPRole(t, e, billing.ID); got != store.RoleViewer {
		t.Errorf("after an admin made a billing member a viewer, the role is %q", got)
	}
	hiddenRPAllowed(t, e, "an admin makes a viewer a member", "PATCH", "/v1/members/"+viewer.ID, adminKey, role("member"))
	hiddenRPAllowed(t, e, "an admin makes a member billing", "PATCH", "/v1/members/"+member.ID, adminKey, role("billing"))
	if got := hiddenRPRole(t, e, member.ID); got != store.RoleBilling {
		t.Errorf("after an admin made a member billing, the role is %q", got)
	}
	hiddenRPAllowed(t, e, "an admin removes a viewer", "DELETE", "/v1/members/"+billing.ID, adminKey, nil) // billing is a viewer now
	hiddenRPDead(t, e, "the removed member's key", billingKey)

	// an owner gives the admin and owner roles, and changes and removes admins and owners
	x, xKey := e.AddMember(e.Acme, "x@acme.example", store.RoleMember)
	hiddenRPAllowed(t, e, "an owner makes a member an admin", "PATCH", "/v1/members/"+x.ID, ownerKey, role("admin"))
	hiddenRPAllowed(t, e, "the new admin updates the organization", "PATCH", "/v1/org", xKey, map[string]any{"name": "Acme X"})
	hiddenRPAllowed(t, e, "an owner makes an admin an owner", "PATCH", "/v1/members/"+x.ID, ownerKey, role("owner"))
	if got := hiddenRPRole(t, e, x.ID); got != store.RoleOwner {
		t.Errorf("after an owner made an admin an owner, the role is %q", got)
	}
	hiddenRPAllowed(t, e, "an owner makes an admin billing", "PATCH", "/v1/members/"+admin2.ID, ownerKey, role("billing"))
	hiddenRPAllowed(t, e, "an owner demotes another owner", "PATCH", "/v1/members/"+owner2.ID, ownerKey, role("admin"))
	if got := hiddenRPRole(t, e, owner2.ID); got != store.RoleAdmin {
		t.Errorf("after an owner demoted another owner to admin, the role is %q", got)
	}
	hiddenRPAllowed(t, e, "an owner removes another owner", "DELETE", "/v1/members/"+x.ID, ownerKey, nil)
	hiddenRPDead(t, e, "the removed owner's key", xKey)
	hiddenRPAllowed(t, e, "an owner removes an admin", "DELETE", "/v1/members/"+owner2.ID, ownerKey, nil) // owner2 is an admin now
	hiddenRPDead(t, e, "the removed admin's key", owner2Key)
}

// TestHiddenRoleAnyoneMayLeave: DELETE /v1/members/{own user_id} works for every role.
func TestHiddenRoleAnyoneMayLeave(tt *testing.T) {
	t := hiddenRPStop{tt}
	e := apitest.New(t)
	e.AddMember(e.Acme, "staying.owner@acme.example", store.RoleOwner) // so that the first owner is not the only one
	leavers := []struct {
		role store.Role
		user store.User
		key  string
	}{{store.RoleOwner, e.Acme.Owner, e.Acme.Key}}
	for _, r := range []store.Role{store.RoleAdmin, store.RoleBilling, store.RoleMember, store.RoleViewer} {
		u, k := e.AddMember(e.Acme, "leaving."+string(r)+"@acme.example", r)
		leavers = append(leavers, struct {
			role store.Role
			user store.User
			key  string
		}{r, u, k})
	}
	for _, l := range leavers {
		hiddenRPAllowed(t, e, "a member with the "+string(l.role)+" role leaves", "DELETE", "/v1/members/"+l.user.ID, l.key, nil)
		if _, ok := e.Store.Member(e.Acme.ID, l.user.ID); ok {
			t.Errorf("a member with the %s role left but is still a member", l.role)
		}
		hiddenRPDead(t, e, "the key of a member who left ("+string(l.role)+")", l.key)
	}
}

// TestHiddenRoleLastOwner: an organization always keeps at least one owner.
func TestHiddenRoleLastOwner(tt *testing.T) {
	t := hiddenRPStop{tt}
	e := apitest.New(t)
	owner, ownerKey := e.Acme.Owner, e.Acme.Key
	_, adminKey := e.AddMember(e.Acme, "admin@acme.example", store.RoleAdmin)

	hiddenRPRefused(t, e, hiddenRPLastOwner, "the only owner leaves", "DELETE", "/v1/members/"+owner.ID, ownerKey, nil)
	// Demoting oneself is refused by two rules (nobody changes their own role: 403; the last owner: 409); either answer.
	for _, to := range []string{"admin", "member"} {
		hiddenRPRefused(t, e, []string{"409 last_owner", "403 forbidden"}, "the only owner demotes themselves to "+to, "PATCH", "/v1/members/"+owner.ID, ownerKey, map[string]string{"role": to})
	}
	// an admin may not remove or demote the only owner either (403 as an admin, or 409 as the last owner)
	hiddenRPRefused(t, e, []string{"403 forbidden", "409 last_owner"}, "an admin removes the only owner", "DELETE", "/v1/members/"+owner.ID, adminKey, nil)
	hiddenRPRefused(t, e, []string{"403 forbidden", "409 last_owner"}, "an admin demotes the only owner", "PATCH", "/v1/members/"+owner.ID, adminKey, map[string]string{"role": "admin"})
	hiddenRPWorks(t, e, "the only owner's key", ownerKey)

	// with a second owner the first may be demoted by them... and the one left is again the only owner
	o2, o2Key := e.AddMember(e.Acme, "o2@acme.example", store.RoleOwner)
	hiddenRPAllowed(t, e, "an owner demotes the other owner", "PATCH", "/v1/members/"+owner.ID, o2Key, map[string]string{"role": "admin"})
	hiddenRPRefused(t, e, hiddenRPLastOwner, "the owner left after demoting the other leaves", "DELETE", "/v1/members/"+o2.ID, o2Key, nil)
	hiddenRPAllowed(t, e, "the only owner makes the demoted one an owner again", "PATCH", "/v1/members/"+owner.ID, o2Key, map[string]string{"role": "owner"})

	// a second owner may leave, after which the first is the only owner
	hiddenRPAllowed(t, e, "one of two owners leaves", "DELETE", "/v1/members/"+o2.ID, o2Key, nil)
	hiddenRPRefused(t, e, hiddenRPLastOwner, "the remaining owner leaves", "DELETE", "/v1/members/"+owner.ID, ownerKey, nil)

	// an owner may leave when another stays; the one who stays cannot
	o3, o3Key := e.AddMember(e.Acme, "o3@acme.example", store.RoleOwner)
	hiddenRPAllowed(t, e, "the first owner leaves while another stays", "DELETE", "/v1/members/"+owner.ID, ownerKey, nil)
	hiddenRPDead(t, e, "the key of the owner who left", ownerKey)
	hiddenRPRefused(t, e, hiddenRPLastOwner, "the owner who stayed leaves", "DELETE", "/v1/members/"+o3.ID, o3Key, nil)
	if got := hiddenRPRole(t, e, o3.ID); got != store.RoleOwner {
		t.Errorf("the only owner's role is %q", got)
	}
	hiddenRPAllowed(t, e, "the only owner still manages the organization", "PATCH", "/v1/org", o3Key, map[string]any{"name": "Acme Again"})
	hiddenRPWorks(t, e, "the admin's key", adminKey)
}

// TestHiddenRoleAPIKeys: every member creates and revokes their own keys; owners and admins revoke and see anyone's.
func TestHiddenRoleAPIKeys(tt *testing.T) {
	t := hiddenRPStop{tt}
	e := apitest.New(t)
	type person struct {
		role         store.Role
		user         store.User
		key          string
		keyID        string
		extra, extID string // a key the person creates through the API
	}
	keyID := func(token string) string {
		t.Helper()
		k, ok := e.Store.APIKeyByToken(token)
		if !ok {
			t.Fatalf("no key for a token apitest gave out")
		}
		return k.ID
	}
	people := []*person{{role: store.RoleOwner, user: e.Acme.Owner, key: e.Acme.Key}}
	for _, r := range []store.Role{store.RoleAdmin, store.RoleBilling, store.RoleMember, store.RoleViewer} {
		u, k := e.AddMember(e.Acme, "keys."+string(r)+"@acme.example", r)
		people = append(people, &person{role: r, user: u, key: k})
	}
	byRole := map[store.Role]*person{}
	for _, p := range people {
		p.keyID = keyID(p.key)
		byRole[p.role] = p
	}
	e.AddMember(e.Globex, "keys.globex@globex.example", store.RoleMember) // another organization's key, never listed

	// every member, viewers included, creates keys for themselves
	for _, p := range people {
		r := hiddenRPAllowed(t, e, string(p.role)+" creates an API key", "POST", "/v1/api-keys", p.key, map[string]string{"name": "Hidden " + string(p.role)})
		var k struct {
			ID     string `json:"id"`
			UserID string `json:"user_id"`
			Token  string `json:"token"`
		}
		if r.Code >= 200 && r.Code <= 299 {
			r.JSON(t, &k)
		}
		if k.UserID != p.user.ID || k.Token == "" || k.ID == "" {
			t.Fatalf("%s creates an API key: want a key of their own (user_id %s) with its token, got %s", p.role, p.user.ID, r)
		}
		p.extra, p.extID = k.Token, k.ID
		hiddenRPWorks(t, e, "the new key of "+string(p.role), p.extra)
	}

	// owners and admins list every key of the organization, everyone else only their own
	var all []string
	for _, k := range e.Store.APIKeys(e.Acme.ID) {
		all = append(all, k.ID)
	}
	sort.Strings(all)
	for _, p := range people {
		r := e.Do("GET", "/v1/api-keys", p.key, nil)
		if r.Code != http.StatusOK {
			t.Errorf("%s lists API keys: want 200, got %s", p.role, r)
			continue
		}
		var page struct {
			Items []struct {
				ID     string `json:"id"`
				UserID string `json:"user_id"`
			} `json:"items"`
		}
		r.JSON(t, &page)
		var got []string
		for _, k := range page.Items {
			got = append(got, k.ID)
		}
		sort.Strings(got)
		want := all
		if p.role != store.RoleOwner && p.role != store.RoleAdmin {
			want = []string{p.keyID, p.extID}
			sort.Strings(want)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s lists API keys: got %v, want %v", p.role, got, want)
		}
	}

	// billing, members and viewers may not revoke a key that is not theirs
	for _, p := range []*person{byRole[store.RoleBilling], byRole[store.RoleMember], byRole[store.RoleViewer]} {
		for _, other := range []*person{byRole[store.RoleOwner], byRole[store.RoleAdmin], byRole[store.RoleBilling], byRole[store.RoleViewer]} {
			if other == p {
				continue
			}
			hiddenRPRefused(t, e, hiddenRPForbidden, fmt.Sprintf("%s revokes a key of %s", p.role, other.role), "DELETE", "/v1/api-keys/"+other.extID, p.key, nil)
			hiddenRPWorks(t, e, fmt.Sprintf("the key of %s after %s tried to revoke it", other.role, p.role), other.extra)
		}
	}

	// everyone revokes their own keys
	for _, p := range people {
		hiddenRPAllowed(t, e, string(p.role)+" revokes their own key", "DELETE", "/v1/api-keys/"+p.extID, p.key, nil)
		hiddenRPDead(t, e, "a key "+string(p.role)+" revoked", p.extra)
		hiddenRPWorks(t, e, "the other key of "+string(p.role), p.key)
	}

	// owners and admins revoke anyone's key in the organization
	ownerSpare, err := e.Store.CreateAPIKey(e.Acme.ID, e.Acme.Owner.ID, "spare", e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		by, of store.Role
		id     string
		token  string
	}{
		{store.RoleAdmin, store.RoleMember, byRole[store.RoleMember].keyID, byRole[store.RoleMember].key},
		{store.RoleAdmin, store.RoleOwner, ownerSpare.ID, ownerSpare.Token},
		{store.RoleOwner, store.RoleViewer, byRole[store.RoleViewer].keyID, byRole[store.RoleViewer].key},
		{store.RoleOwner, store.RoleAdmin, byRole[store.RoleAdmin].keyID, byRole[store.RoleAdmin].key},
	} {
		hiddenRPAllowed(t, e, fmt.Sprintf("%s revokes a key of %s", c.by, c.of), "DELETE", "/v1/api-keys/"+c.id, byRole[c.by].key, nil)
		hiddenRPDead(t, e, fmt.Sprintf("a key of %s that %s revoked", c.of, c.by), c.token)
	}
}

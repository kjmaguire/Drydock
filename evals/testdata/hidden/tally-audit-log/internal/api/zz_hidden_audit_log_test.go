package api_test

// Hidden acceptance test of the trial task tally-audit-log (evals/tasks). The agents never see it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

type hiddenAuditEntry struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Actor  struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	} `json:"actor"`
	Target struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"target"`
	RequestID string                     `json:"request_id"`
	At        string                     `json:"at"`
	Changes   map[string]json.RawMessage `json:"changes"`
	raw       string                     // the entry as the API answered it
}

// hiddenAuditPage reads one page of GET /v1/audit-events.
func hiddenAuditPage(t *testing.T, e *apitest.Env, key, query string) ([]hiddenAuditEntry, string, *apitest.Response) {
	t.Helper()
	path := "/v1/audit-events"
	if query != "" {
		path += "?" + query
	}
	r := e.Do("GET", path, key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %s", path, r)
	}
	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"next_cursor"`
	}
	r.JSON(t, &page)
	out := []hiddenAuditEntry{}
	for _, raw := range page.Items {
		var ent hiddenAuditEntry
		if err := json.Unmarshal(raw, &ent); err != nil {
			t.Fatalf("GET %s: an entry is not the JSON expected (%v): %s", path, err, raw)
		}
		ent.raw = string(raw)
		out = append(out, ent)
	}
	return out, page.NextCursor, r
}

// hiddenAuditAll reads every entry GET /v1/audit-events lists with the query, following the pages (100 at a time), and the
// raw bodies of the pages.
func hiddenAuditAll(t *testing.T, e *apitest.Env, key, query string) ([]hiddenAuditEntry, string) {
	t.Helper()
	var all []hiddenAuditEntry
	var bodies strings.Builder
	cursor := ""
	for range 1000 {
		q := "limit=100"
		if query != "" {
			q += "&" + query
		}
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		page, next, r := hiddenAuditPage(t, e, key, q)
		all = append(all, page...)
		bodies.Write(r.Body)
		if next == "" {
			return all, bodies.String()
		}
		cursor = next
	}
	t.Fatalf("GET /v1/audit-events?%s: the pages never end", query)
	return nil, ""
}

func hiddenAuditIDs(entries []hiddenAuditEntry) []string {
	out := []string{}
	for _, ent := range entries {
		out = append(out, ent.ID)
	}
	return out
}

func hiddenAuditDump(entries []hiddenAuditEntry) string {
	var b strings.Builder
	for _, ent := range entries {
		b.WriteString("\n  " + ent.raw)
	}
	return b.String()
}

// hiddenAuditor makes audited requests and checks what each recorded.
type hiddenAuditor struct {
	t        *testing.T
	e        *apitest.Env
	reader   string            // an owner's key, to read the log
	keyIDs   map[string]string // API key token -> the key's ID
	userIDs  map[string]string // API key token -> its member's user ID
	n        int
	recorded []string // IDs of the entries recorded through do, oldest first
	bodies   strings.Builder
}

func hiddenNewAuditor(t *testing.T, e *apitest.Env, reader string) *hiddenAuditor {
	return &hiddenAuditor{t: t, e: e, reader: reader, keyIDs: map[string]string{}, userIDs: map[string]string{}}
}

// know learns the ID of the key with this token (GET /v1/me) and the user it acts for.
func (h *hiddenAuditor) know(token, userID string) string {
	h.t.Helper()
	var me struct {
		APIKeyID string `json:"api_key_id"`
	}
	h.e.MustDo(http.StatusOK, "GET", "/v1/me", token, nil, &me)
	if me.APIKeyID == "" {
		h.t.Fatalf("setting up: GET /v1/me shows no api_key_id")
	}
	h.keyIDs[token], h.userIDs[token] = me.APIKeyID, userID
	return me.APIKeyID
}

func (h *hiddenAuditor) all(query string) []hiddenAuditEntry {
	h.t.Helper()
	entries, bodies := hiddenAuditAll(h.t, h.e, h.reader, query)
	h.bodies.WriteString(bodies)
	return entries
}

// do sends a request one second after the previous one, with its own X-Request-Id, checks its status, and checks that it
// recorded exactly one entry: the action, the key and its member as the actor, the target (targetID "" means the "id" the
// response shows), the request ID, and the service's time. It returns the response and the entry.
func (h *hiddenAuditor) do(method, path, key string, body any, status int, action, targetType, targetID string) (*apitest.Response, hiddenAuditEntry) {
	h.t.Helper()
	t, e := h.t, h.e
	before := map[string]bool{}
	for _, id := range hiddenAuditIDs(h.all("")) {
		before[id] = true
	}
	e.Clock.Advance(time.Second)
	at := e.Clock.Now()
	h.n++
	reqID := fmt.Sprintf("hidden-audit-%03d", h.n)
	what := fmt.Sprintf("%s %s (X-Request-Id %s)", method, path, reqID)
	r := e.DoWith(method, path, key, body, http.Header{"X-Request-Id": {reqID}})
	if r.Code != status {
		t.Fatalf("%s: want %d, got %s", what, status, r)
	}
	if targetID == "" {
		var obj struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(r.Body, &obj); err != nil || obj.ID == "" {
			t.Fatalf("%s: the answer shows no id: %s", what, r)
		}
		targetID = obj.ID
	}
	var added []hiddenAuditEntry
	for _, ent := range h.all("") {
		if !before[ent.ID] {
			added = append(added, ent)
		}
	}
	if len(added) != 1 {
		t.Fatalf("%s answered %d and recorded %d audit entries, want exactly one (%s):%s", what, r.Code, len(added), action, hiddenAuditDump(added))
	}
	ent := added[0]
	wantActor := fmt.Sprintf(`{"type":"api_key","id":%q,"user_id":%q}`, h.keyIDs[key], h.userIDs[key])
	gotActor := fmt.Sprintf(`{"type":%q,"id":%q,"user_id":%q}`, ent.Actor.Type, ent.Actor.ID, ent.Actor.UserID)
	wantTarget := fmt.Sprintf(`{"type":%q,"id":%q}`, targetType, targetID)
	gotTarget := fmt.Sprintf(`{"type":%q,"id":%q}`, ent.Target.Type, ent.Target.ID)
	if ent.ID == "" || ent.Action != action || gotActor != wantActor || gotTarget != wantTarget || ent.RequestID != reqID {
		t.Fatalf("%s recorded\n  %s\nwant \"action\": %q, \"actor\": %s, \"target\": %s, \"request_id\": %q and an \"id\"",
			what, ent.raw, action, wantActor, wantTarget, reqID)
	}
	got, err := time.Parse(time.RFC3339, ent.At)
	if _, offset := got.Zone(); err != nil || !got.Equal(at) || offset != 0 {
		t.Fatalf("%s recorded \"at\": %q, want the service's clock in RFC 3339 UTC: %q\n  %s", what, ent.At, at.Format(time.RFC3339), ent.raw)
	}
	h.recorded = append(h.recorded, ent.ID)
	return r, ent
}

// hiddenNullOrEmpty stands for the old value of a field that was empty and left out of the object: null, "" or no "from".
type hiddenNullOrEmpty struct{}

// wantChanges checks that an entry's "changes" names exactly these fields, each with its old and new value.
func (h *hiddenAuditor) wantChanges(ent hiddenAuditEntry, want map[string][2]any) {
	h.t.Helper()
	fail := func(why string) {
		h.t.Helper()
		w, _ := json.Marshal(want)
		h.t.Fatalf("%s: %s\n  %s\nwant \"changes\" with exactly these fields (old, new): %s", ent.Action, why, ent.raw, w)
	}
	if ent.Changes == nil {
		fail("no \"changes\"")
	}
	var got, wanted []string
	for k := range ent.Changes {
		got = append(got, k)
	}
	for k := range want {
		wanted = append(wanted, k)
	}
	sort.Strings(got)
	sort.Strings(wanted)
	if strings.Join(got, ",") != strings.Join(wanted, ",") {
		fail(fmt.Sprintf("\"changes\" names the fields %v", got))
	}
	for k, fromTo := range want {
		var change map[string]json.RawMessage
		if err := json.Unmarshal(ent.Changes[k], &change); err != nil {
			fail(fmt.Sprintf("the change of %q is not an object", k))
		}
		for i, side := range []string{"from", "to"} {
			raw, ok := change[side]
			if _, nullOK := fromTo[i].(hiddenNullOrEmpty); nullOK {
				if ok && string(raw) != "null" && string(raw) != `""` {
					fail(fmt.Sprintf("the change of %q has %q: %s", k, side, raw))
				}
				continue
			}
			var v, wv any
			if !ok || json.Unmarshal(raw, &v) != nil {
				fail(fmt.Sprintf("the change of %q has no %q", k, side))
			}
			wb, _ := json.Marshal(fromTo[i])
			_ = json.Unmarshal(wb, &wv)
			if !reflect.DeepEqual(v, wv) {
				fail(fmt.Sprintf("the change of %q has %q: %s", k, side, raw))
			}
		}
	}
}

func hiddenIDOf(t *testing.T, r *apitest.Response) string {
	t.Helper()
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Body, &obj); err != nil || obj.ID == "" {
		t.Fatalf("no id in %s", r)
	}
	return obj.ID
}

// hiddenAuditActions are the actions the task lists.
var hiddenAuditActions = []string{
	"customer.created", "customer.updated", "customer.deleted", "plan.created", "plan.updated", "subscription.created",
	"subscription.updated", "subscription.canceled", "invoice.created", "invoice.paid", "invoice.voided", "api_key.created",
	"api_key.revoked", "member.role_changed", "member.removed", "invite.created", "invite.revoked", "webhook_endpoint.created",
	"webhook_endpoint.updated", "webhook_endpoint.deleted", "org.updated",
}

var hiddenInviteTokenRe = regexp.MustCompile(`token=([^\s&]+)`)

func TestHiddenAuditRecordsEveryChange(t *testing.T) {
	e := apitest.New(t)
	owner := e.Acme.Key
	adminUser, admin := e.AddMember(e.Acme, "admin@acme.example", store.RoleAdmin)
	memberUser, _ := e.AddMember(e.Acme, "member@acme.example", store.RoleMember)
	h := hiddenNewAuditor(t, e, owner)
	h.know(owner, e.Acme.Owner.ID)
	adminKeyID := h.know(admin, adminUser.ID)
	secrets := []string{owner, admin}

	r, _ := h.do("POST", "/v1/customers", owner, map[string]string{"name": "Jane Doe", "email": "jane@example.com"}, 201, "customer.created", "customer", "")
	cust := hiddenIDOf(t, r)
	_, ent := h.do("PATCH", "/v1/customers/"+cust, admin, map[string]any{"name": "Jane Q. Doe", "email": "jane@example.com"}, 200, "customer.updated", "customer", cust)
	h.wantChanges(ent, map[string][2]any{"name": {"Jane Doe", "Jane Q. Doe"}})
	_, ent = h.do("PATCH", "/v1/customers/"+cust, owner, map[string]any{"email": "jane.doe@example.com"}, 200, "customer.updated", "customer", cust)
	h.wantChanges(ent, map[string][2]any{"email": {"jane@example.com", "jane.doe@example.com"}})
	_, ent = h.do("PATCH", "/v1/customers/"+cust, owner, map[string]any{"default_payment_method": "pm_card_visa"}, 200, "customer.updated", "customer", cust)
	h.wantChanges(ent, map[string][2]any{"default_payment_method": {hiddenNullOrEmpty{}, "pm_card_visa"}})

	r, _ = h.do("POST", "/v1/plans", owner, map[string]any{"name": "Team", "currency": "USD", "price": "20.00", "interval": "month"}, 201, "plan.created", "plan", "")
	plan := hiddenIDOf(t, r)
	_, ent = h.do("PATCH", "/v1/plans/"+plan, owner, map[string]any{"name": "Team Plus", "active": true}, 200, "plan.updated", "plan", plan)
	h.wantChanges(ent, map[string][2]any{"name": {"Team", "Team Plus"}})
	_, ent = h.do("PATCH", "/v1/plans/"+plan, admin, map[string]any{"active": false}, 200, "plan.updated", "plan", plan)
	h.wantChanges(ent, map[string][2]any{"active": {true, false}})
	_, ent = h.do("PATCH", "/v1/plans/"+plan, owner, map[string]any{"active": true}, 200, "plan.updated", "plan", plan)
	h.wantChanges(ent, map[string][2]any{"active": {false, true}})
	r, _ = h.do("POST", "/v1/plans", admin, map[string]any{"name": "Team Max", "currency": "USD", "price": "30.00", "interval": "month"}, 201, "plan.created", "plan", "")
	plan2 := hiddenIDOf(t, r)

	// Starting a subscription also invoices and charges its first period: still one request, one entry.
	r, _ = h.do("POST", "/v1/subscriptions", owner, map[string]any{"customer_id": cust, "plan_id": plan, "quantity": 1}, 201, "subscription.created", "subscription", "")
	sub := hiddenIDOf(t, r)
	_, ent = h.do("PATCH", "/v1/subscriptions/"+sub, owner, map[string]any{"quantity": 3}, 200, "subscription.updated", "subscription", sub)
	h.wantChanges(ent, map[string][2]any{"quantity": {1, 3}})
	_, ent = h.do("PATCH", "/v1/subscriptions/"+sub, admin, map[string]any{"plan_id": plan2, "quantity": 3}, 200, "subscription.updated", "subscription", sub)
	h.wantChanges(ent, map[string][2]any{"plan_id": {plan, plan2}})
	h.do("POST", "/v1/subscriptions/"+sub+"/cancel", owner, map[string]any{"at_period_end": true}, 200, "subscription.canceled", "subscription", sub)
	r, _ = h.do("POST", "/v1/subscriptions", owner, map[string]any{"customer_id": cust, "plan_id": plan2}, 201, "subscription.created", "subscription", "")
	sub2 := hiddenIDOf(t, r)
	h.do("POST", "/v1/subscriptions/"+sub2+"/cancel", admin, nil, 200, "subscription.canceled", "subscription", sub2)

	line := []map[string]any{{"description": "Setup", "quantity": 1, "unit_amount": 5000}}
	r, _ = h.do("POST", "/v1/invoices", owner, map[string]any{"customer_id": cust, "currency": "USD", "lines": line}, 201, "invoice.created", "invoice", "")
	inv := hiddenIDOf(t, r)
	h.do("POST", "/v1/invoices/"+inv+"/pay", admin, map[string]string{"payment_method": "pm_card_visa"}, 200, "invoice.paid", "invoice", inv)
	r, _ = h.do("POST", "/v1/invoices", owner, map[string]any{"customer_id": cust, "currency": "USD", "lines": line}, 201, "invoice.created", "invoice", "")
	inv2 := hiddenIDOf(t, r)
	h.do("POST", "/v1/invoices/"+inv2+"/void", owner, nil, 200, "invoice.voided", "invoice", inv2)

	r, _ = h.do("POST", "/v1/api-keys", owner, map[string]string{"name": "CI"}, 201, "api_key.created", "api_key", "")
	var ci struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	r.JSON(t, &ci)
	if ci.Token == "" {
		t.Fatalf("setting up: POST /v1/api-keys shows no token: %s", r)
	}
	secrets = append(secrets, ci.Token)
	h.know(ci.Token, e.Acme.Owner.ID)
	h.do("POST", "/v1/customers", ci.Token, map[string]string{"name": "Made By CI", "email": "ci@example.com"}, 201, "customer.created", "customer", "")
	h.do("DELETE", "/v1/api-keys/"+ci.ID, owner, nil, 204, "api_key.revoked", "api_key", ci.ID)

	_, ent = h.do("PATCH", "/v1/members/"+memberUser.ID, admin, map[string]string{"role": "billing"}, 200, "member.role_changed", "member", memberUser.ID)
	h.wantChanges(ent, map[string][2]any{"role": {"member", "billing"}})
	h.do("DELETE", "/v1/members/"+memberUser.ID, owner, nil, 204, "member.removed", "member", memberUser.ID)

	r, _ = h.do("POST", "/v1/invites", admin, map[string]string{"email": "new@people.example", "role": "member"}, 201, "invite.created", "invite", "")
	invite := hiddenIDOf(t, r)
	var invBody struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(r.Body, &invBody)
	if invBody.Token != "" {
		secrets = append(secrets, invBody.Token)
	}
	for _, m := range e.Mail.Messages() {
		if match := hiddenInviteTokenRe.FindStringSubmatch(m.Body); match != nil {
			if tok, err := url.QueryUnescape(match[1]); err == nil {
				secrets = append(secrets, tok)
			}
		}
	}
	h.do("DELETE", "/v1/invites/"+invite, owner, nil, 204, "invite.revoked", "invite", invite)

	r, _ = h.do("POST", "/v1/webhook-endpoints", owner, map[string]any{"url": "https://hooks.acme.example/tally", "events": []string{"customer.created"}}, 201,
		"webhook_endpoint.created", "webhook_endpoint", "")
	ep := hiddenIDOf(t, r)
	var epBody struct {
		Secret string `json:"secret"`
	}
	r.JSON(t, &epBody)
	if epBody.Secret == "" {
		t.Fatalf("setting up: POST /v1/webhook-endpoints shows no secret: %s", r)
	}
	secrets = append(secrets, epBody.Secret)
	_, ent = h.do("PATCH", "/v1/webhook-endpoints/"+ep, owner, map[string]any{"enabled": false}, 200, "webhook_endpoint.updated", "webhook_endpoint", ep)
	h.wantChanges(ent, map[string][2]any{"enabled": {true, false}})
	_, ent = h.do("PATCH", "/v1/webhook-endpoints/"+ep, admin, map[string]any{"url": "https://hooks.acme.example/v2", "events": []string{"customer.created"}}, 200,
		"webhook_endpoint.updated", "webhook_endpoint", ep)
	h.wantChanges(ent, map[string][2]any{"url": {"https://hooks.acme.example/tally", "https://hooks.acme.example/v2"}})
	_, ent = h.do("PATCH", "/v1/webhook-endpoints/"+ep, owner, map[string]any{"events": []string{"*"}}, 200, "webhook_endpoint.updated", "webhook_endpoint", ep)
	h.wantChanges(ent, map[string][2]any{"events": {[]string{"customer.created"}, []string{"*"}}})
	h.do("DELETE", "/v1/webhook-endpoints/"+ep, owner, nil, 204, "webhook_endpoint.deleted", "webhook_endpoint", ep)

	_, ent = h.do("PATCH", "/v1/org", admin, map[string]any{"name": "Acme", "tax_rate_bp": 2000}, 200, "org.updated", "org", e.Acme.ID)
	h.wantChanges(ent, map[string][2]any{"tax_rate_bp": {0, 2000}})
	_, ent = h.do("PATCH", "/v1/org", owner, map[string]any{"time_zone": "UTC", "plan": "business"}, 200, "org.updated", "org", e.Acme.ID)
	h.wantChanges(ent, map[string][2]any{"time_zone": {"America/New_York", "UTC"}, "plan": {"team", "business"}})

	h.do("DELETE", "/v1/customers/"+cust, admin, nil, 204, "customer.deleted", "customer", cust)

	// The whole log, newest first.
	all := h.all("")
	want := make([]string, len(h.recorded))
	for i, id := range h.recorded {
		want[len(want)-1-i] = id
	}
	if got := hiddenAuditIDs(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("GET /v1/audit-events lists the entries %v, want %v (every entry recorded, newest first):%s", got, want, hiddenAuditDump(all))
	}
	seenAction := map[string]bool{}
	for _, ent := range all {
		seenAction[ent.Action] = true
	}
	for _, a := range hiddenAuditActions {
		if !seenAction[a] {
			t.Fatalf("no %s entry in the log", a)
		}
	}

	// Paging: 20 by default, any page size, the usual errors.
	first, next, _ := hiddenAuditPage(t, e, owner, "")
	if !reflect.DeepEqual(hiddenAuditIDs(first), want[:20]) || next == "" {
		t.Fatalf("GET /v1/audit-events: want the 20 newest entries and a next_cursor, got %d entries %v and next_cursor %q", len(first), hiddenAuditIDs(first), next)
	}
	var paged []string
	cursor := ""
	for page := 0; ; page++ {
		q := "limit=7"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		items, next, _ := hiddenAuditPage(t, e, owner, q)
		if len(items) > 7 {
			t.Fatalf("GET /v1/audit-events?%s: %d entries", q, len(items))
		}
		if page > len(want) {
			t.Fatalf("the pages of GET /v1/audit-events?limit=7 never end: after %d pages, still a next_cursor (the log has %d entries)", page, len(want))
		}
		paged = append(paged, hiddenAuditIDs(items)...)
		if next == "" {
			break
		}
		cursor = next
	}
	if !reflect.DeepEqual(paged, want) {
		t.Fatalf("the pages of GET /v1/audit-events?limit=7 list %v, want %v", paged, want)
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=%21%21"} {
		if r := e.Do("GET", "/v1/audit-events?"+q, owner, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("GET /v1/audit-events?%s: want 400, got %s", q, r)
		}
	}

	// Filters.
	filter := func(keep func(hiddenAuditEntry) bool) []string {
		out := []string{}
		for _, ent := range all {
			if keep(ent) {
				out = append(out, ent.ID)
			}
		}
		return out
	}
	ownerKeyID := h.keyIDs[owner]
	for _, f := range []struct {
		query string
		want  []string
	}{
		{"action=customer.updated", filter(func(x hiddenAuditEntry) bool { return x.Action == "customer.updated" })},
		{"action=invoice.paid", filter(func(x hiddenAuditEntry) bool { return x.Action == "invoice.paid" })},
		{"action=nothing.happened", []string{}},
		{"target_id=" + cust, filter(func(x hiddenAuditEntry) bool { return x.Target.ID == cust })},
		{"target_id=" + memberUser.ID, filter(func(x hiddenAuditEntry) bool { return x.Target.ID == memberUser.ID })},
		{"actor_id=" + adminUser.ID, filter(func(x hiddenAuditEntry) bool { return x.Actor.UserID == adminUser.ID })},
		{"actor_id=" + adminKeyID, filter(func(x hiddenAuditEntry) bool { return x.Actor.ID == adminKeyID })},
		{"actor_id=" + e.Acme.Owner.ID, filter(func(x hiddenAuditEntry) bool { return x.Actor.UserID == e.Acme.Owner.ID })},
		{"actor_id=" + ownerKeyID, filter(func(x hiddenAuditEntry) bool { return x.Actor.ID == ownerKeyID })},
		{"actor_id=" + ci.ID, filter(func(x hiddenAuditEntry) bool { return x.Actor.ID == ci.ID })},
	} {
		got, bodies := hiddenAuditAll(t, e, owner, f.query)
		h.bodies.WriteString(bodies)
		if ids := hiddenAuditIDs(got); !reflect.DeepEqual(ids, f.want) {
			t.Fatalf("GET /v1/audit-events?%s lists %v, want %v", f.query, ids, f.want)
		}
	}
	if n := len(filter(func(x hiddenAuditEntry) bool { return x.Target.ID == cust })); n != 5 {
		t.Fatalf("the log has %d entries about the deleted customer %s, want its creation, 3 updates and its deletion", n, cust)
	}
	if n := len(filter(func(x hiddenAuditEntry) bool { return x.Actor.ID == ci.ID })); n != 1 {
		t.Fatalf("the log has %d entries made with the revoked key %s, want 1", n, ci.ID)
	}
	updates := filter(func(x hiddenAuditEntry) bool { return x.Action == "customer.updated" })
	items, next, _ := hiddenAuditPage(t, e, owner, "action=customer.updated&limit=2")
	if !reflect.DeepEqual(hiddenAuditIDs(items), updates[:2]) || next == "" {
		t.Fatalf("GET /v1/audit-events?action=customer.updated&limit=2: want %v and a next_cursor, got %v, %q", updates[:2], hiddenAuditIDs(items), next)
	}
	items, next, _ = hiddenAuditPage(t, e, owner, "action=customer.updated&limit=2&cursor="+url.QueryEscape(next))
	if !reflect.DeepEqual(hiddenAuditIDs(items), updates[2:]) || next != "" {
		t.Fatalf("the second page of GET /v1/audit-events?action=customer.updated&limit=2: want %v, got %v, next_cursor %q", updates[2:], hiddenAuditIDs(items), next)
	}

	// Only owners and admins read the log.
	adminAll, bodies := hiddenAuditAll(t, e, admin, "")
	h.bodies.WriteString(bodies)
	if !reflect.DeepEqual(hiddenAuditIDs(adminAll), want) {
		t.Fatalf("an admin reads %v, want %v", hiddenAuditIDs(adminAll), want)
	}
	for _, role := range []store.Role{store.RoleBilling, store.RoleMember, store.RoleViewer} {
		_, key := e.AddMember(e.Acme, "reader-"+string(role)+"@acme.example", role)
		r := e.Do("GET", "/v1/audit-events", key, nil)
		var body struct {
			Error string `json:"error"`
		}
		if r.Code != http.StatusForbidden || json.Unmarshal(r.Body, &body) != nil || body.Error != "forbidden" {
			t.Fatalf("GET /v1/audit-events by a %s: want 403 {\"error\":\"forbidden\"}, got %s", role, r)
		}
	}

	// No secret is in any entry.
	log := h.bodies.String()
	for _, s := range secrets {
		parts := []string{s}
		if _, rest, ok := strings.Cut(s, "_"); ok && len(rest) >= 16 {
			parts = append(parts, rest)
		}
		for _, p := range parts {
			if strings.Contains(log, p) {
				t.Fatalf("the audit log shows a secret (an API key token, a webhook endpoint secret or an invite token): %q", p)
			}
		}
	}

	// No request changes or deletes an entry.
	target := all[len(all)/2].ID
	for _, req := range []struct{ method, path string }{
		{"POST", "/v1/audit-events"}, {"PUT", "/v1/audit-events"}, {"PATCH", "/v1/audit-events"}, {"DELETE", "/v1/audit-events"},
		{"PUT", "/v1/audit-events/" + target}, {"PATCH", "/v1/audit-events/" + target}, {"DELETE", "/v1/audit-events/" + target},
		{"POST", "/v1/audit-events/" + target},
	} {
		r := e.Do(req.method, req.path, owner, map[string]any{"action": "nothing", "id": target, "changes": map[string]any{}})
		if r.Code >= 200 && r.Code < 300 {
			t.Fatalf("%s %s: want it refused, got %s", req.method, req.path, r)
		}
	}
	after := h.all("")
	if len(after) != len(all) {
		t.Fatalf("requests to change or delete entries changed the log: %d entries, was %d", len(after), len(all))
	}
	for i := range all {
		var a, b any
		_ = json.Unmarshal([]byte(all[i].raw), &a)
		_ = json.Unmarshal([]byte(after[i].raw), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("an entry changed:\n  was %s\n  now %s", all[i].raw, after[i].raw)
		}
	}

	// Each organization sees only its own entries.
	globex, _ := hiddenAuditAll(t, e, e.Globex.Key, "")
	if len(globex) != 0 {
		t.Fatalf("Globex reads entries it did not make:%s", hiddenAuditDump(globex))
	}
	r = e.DoWith("POST", "/v1/customers", e.Globex.Key, map[string]string{"name": "Gus", "email": "gus@globex-customers.example"}, http.Header{"X-Request-Id": {"hidden-globex"}})
	if r.Code != http.StatusCreated {
		t.Fatalf("Globex creates a customer: %s", r)
	}
	globex, _ = hiddenAuditAll(t, e, e.Globex.Key, "")
	if len(globex) != 1 || globex[0].Action != "customer.created" || globex[0].RequestID != "hidden-globex" || globex[0].Actor.UserID != e.Globex.Owner.ID {
		t.Fatalf("Globex reads%s\nwant only the customer.created entry of its own request", hiddenAuditDump(globex))
	}
	if acme := h.all(""); len(acme) != len(all) {
		t.Fatalf("Acme's log has %d entries after Globex's request, want %d", len(acme), len(all))
	}
}

func TestHiddenAuditRecordsNothingElse(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	owner := e.Acme.Key
	_, viewer := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	memberUser, memberKey := e.AddMember(e.Acme, "member@acme.example", store.RoleMember)

	// Things to work on, made through the API (and so recorded).
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	e.MustDo(http.StatusOK, "PATCH", "/v1/customers/"+c.ID, owner, map[string]any{"default_payment_method": "pm_card_visa"}, nil)
	plan := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	var metered, inactive store.Plan
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", owner, map[string]any{"name": "Calls", "currency": "USD", "price": "0.01", "interval": "month", "usage_type": "metered"}, &metered)
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", owner, map[string]any{"name": "Old", "currency": "USD", "price": "1.00", "interval": "month"}, &inactive)
	e.MustDo(http.StatusOK, "PATCH", "/v1/plans/"+inactive.ID, owner, map[string]any{"active": false}, nil)
	var sub, msub, canceled store.Subscription
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", owner, map[string]any{"customer_id": c.ID, "plan_id": plan.ID}, &sub)
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", owner, map[string]any{"customer_id": c.ID, "plan_id": metered.ID}, &msub)
	e.MustDo(http.StatusCreated, "POST", "/v1/subscriptions", owner, map[string]any{"customer_id": c.ID, "plan_id": plan.ID}, &canceled)
	e.MustDo(http.StatusOK, "POST", "/v1/subscriptions/"+canceled.ID+"/cancel", owner, nil, nil)
	line := []map[string]any{{"description": "Setup", "quantity": 1, "unit_amount": 5000}}
	var open, paid struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", owner, map[string]any{"customer_id": c.ID, "currency": "USD", "lines": line}, &open)
	e.MustDo(http.StatusCreated, "POST", "/v1/invoices", owner, map[string]any{"customer_id": c.ID, "currency": "USD", "lines": line}, &paid)
	e.MustDo(http.StatusOK, "POST", "/v1/invoices/"+paid.ID+"/pay", owner, map[string]string{"payment_method": "pm_card_visa"}, nil)
	var ep store.WebhookEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", owner, map[string]any{"url": "https://hooks.acme.example/tally", "events": []string{"*"}}, &ep)
	e.MustDo(http.StatusCreated, "POST", "/v1/invites", owner, map[string]string{"email": "new@people.example", "role": "member"}, nil)
	token := ""
	for _, m := range e.Mail.Messages() {
		if match := hiddenInviteTokenRe.FindStringSubmatch(m.Body); match != nil && m.To == "new@people.example" {
			token, _ = url.QueryUnescape(match[1])
		}
	}
	if token == "" {
		t.Fatal("setting up: no invite email with a token")
	}

	count := func() int {
		t.Helper()
		entries, _ := hiddenAuditAll(t, e, owner, "")
		return len(entries)
	}
	n := count()
	if n == 0 {
		t.Fatal("the changes made to set up this test recorded no audit entry")
	}
	check := func(what string, r *apitest.Response, status int) {
		t.Helper()
		if r.Code != status {
			t.Fatalf("%s: want %d, got %s", what, status, r)
		}
		if got := count(); got != n {
			t.Fatalf("%s (answered %d) recorded %d audit entries, want none", what, r.Code, got-n)
		}
	}

	for _, path := range []string{"/v1/me", "/v1/org", "/v1/customers", "/v1/customers?q=jane", "/v1/customers/" + c.ID, "/v1/customers/export.csv",
		"/v1/plans", "/v1/plans/" + plan.ID, "/v1/subscriptions", "/v1/subscriptions/" + sub.ID, "/v1/invoices", "/v1/invoices/" + open.ID,
		"/v1/invoices/" + open.ID + "/lines", "/v1/payments", "/v1/members", "/v1/invites", "/v1/api-keys", "/v1/webhook-endpoints",
		"/v1/webhook-endpoints/" + ep.ID, "/v1/webhook-endpoints/" + ep.ID + "/deliveries", "/v1/events", "/v1/reports/revenue?from=2026-01-01&to=2026-01-31",
		"/v1/audit-events", "/healthz"} {
		check("GET "+path, e.Do("GET", path, owner, nil), http.StatusOK)
	}
	check("POST /v1/subscriptions/{id}/usage", e.Do("POST", "/v1/subscriptions/"+msub.ID+"/usage", owner, map[string]any{"quantity": 5}), http.StatusCreated)
	check("POST /v1/invites/accept", e.Do("POST", "/v1/invites/accept", "", map[string]string{"token": token, "name": "New Person"}), http.StatusCreated)

	// Background work is not a request.
	e.Clock.Advance(32 * 24 * time.Hour)
	if renewed, err := e.Subs.RenewDue(ctx); err != nil || renewed < 2 {
		t.Fatalf("setting up: RenewDue = %d, %v", renewed, err)
	}
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatalf("setting up: RunDue: %v", err)
	}
	if got := count(); got != n {
		t.Fatalf("renewing subscriptions and sending webhooks recorded %d audit entries, want none", got-n)
	}

	// Requests that fail record nothing.
	for _, f := range []struct {
		what, method, path, key string
		body                    any
		status                  int
	}{
		{"a customer without a name", "POST", "/v1/customers", owner, map[string]string{"name": "", "email": "x@example.com"}, 422},
		{"a customer with an address in use", "POST", "/v1/customers", owner, map[string]string{"name": "Jane 2", "email": "JANE@example.com"}, 409},
		{"a body that is not JSON", "POST", "/v1/customers", owner, "{not json", 400},
		{"no API key", "POST", "/v1/customers", "", map[string]string{"name": "X", "email": "x@example.com"}, 401},
		{"a viewer's change", "POST", "/v1/customers", viewer, map[string]string{"name": "X", "email": "x@example.com"}, 403},
		{"an unknown customer", "PATCH", "/v1/customers/cus_999999", owner, map[string]string{"name": "X"}, 404},
		{"an invalid email address", "PATCH", "/v1/customers/" + c.ID, owner, map[string]string{"email": "not-an-email"}, 422},
		{"deleting an unknown customer", "DELETE", "/v1/customers/cus_999999", owner, nil, 404},
		{"a plan with a bad price", "POST", "/v1/plans", owner, map[string]any{"name": "X", "currency": "USD", "price": "abc", "interval": "month"}, 422},
		{"a plan without a name", "PATCH", "/v1/plans/" + plan.ID, owner, map[string]any{"name": ""}, 422},
		{"an unknown plan", "PATCH", "/v1/plans/plan_999999", owner, map[string]any{"name": "X"}, 404},
		{"a subscription to an inactive plan", "POST", "/v1/subscriptions", owner, map[string]any{"customer_id": c.ID, "plan_id": inactive.ID}, 422},
		{"changing a canceled subscription", "PATCH", "/v1/subscriptions/" + canceled.ID, owner, map[string]any{"quantity": 2}, 409},
		{"canceling an unknown subscription", "POST", "/v1/subscriptions/sub_999999/cancel", owner, nil, 404},
		{"an invoice without lines", "POST", "/v1/invoices", owner, map[string]any{"customer_id": c.ID, "currency": "USD", "lines": []any{}}, 422},
		{"a declined payment", "POST", "/v1/invoices/" + open.ID + "/pay", owner, map[string]string{"payment_method": "pm_card_declined"}, 402},
		{"a payment while the gateway is down", "POST", "/v1/invoices/" + open.ID + "/pay", owner, map[string]string{"payment_method": "pm_gateway_down"}, 503},
		{"paying a paid invoice", "POST", "/v1/invoices/" + paid.ID + "/pay", owner, map[string]string{"payment_method": "pm_card_visa"}, 409},
		{"voiding a paid invoice", "POST", "/v1/invoices/" + paid.ID + "/void", owner, nil, 409},
		{"revoking an unknown key", "DELETE", "/v1/api-keys/key_999999", owner, nil, 404},
		{"an unknown role", "PATCH", "/v1/members/" + memberUser.ID, owner, map[string]string{"role": "superuser"}, 422},
		{"an unknown member", "PATCH", "/v1/members/usr_999999", owner, map[string]string{"role": "billing"}, 404},
		{"removing an unknown member", "DELETE", "/v1/members/usr_999999", owner, nil, 404},
		{"inviting a member", "POST", "/v1/invites", owner, map[string]string{"email": "owner@acme.example", "role": "member"}, 409},
		{"a member invites", "POST", "/v1/invites", memberKey, map[string]string{"email": "x@people.example", "role": "member"}, 403},
		{"revoking an unknown invite", "DELETE", "/v1/invites/inv_999999", owner, nil, 404},
		{"a webhook endpoint with a bad URL", "POST", "/v1/webhook-endpoints", owner, map[string]any{"url": "not a url", "events": []string{"*"}}, 422},
		{"an unknown webhook endpoint", "PATCH", "/v1/webhook-endpoints/we_999999", owner, map[string]any{"enabled": false}, 404},
		{"deleting an unknown webhook endpoint", "DELETE", "/v1/webhook-endpoints/we_999999", owner, nil, 404},
		{"an unknown time zone", "PATCH", "/v1/org", owner, map[string]any{"time_zone": "Not/AZone"}, 422},
		{"a member changes the organization", "PATCH", "/v1/org", memberKey, map[string]any{"name": "Hacked"}, 403},
	} {
		check(f.what+" ("+f.method+" "+f.path+")", e.Do(f.method, f.path, f.key, f.body), f.status)
	}
}

func TestHiddenAuditRequestIDOfARequestWithoutOne(t *testing.T) {
	e := apitest.New(t)
	r := e.Do("POST", "/v1/customers", e.Acme.Key, map[string]string{"name": "Jane", "email": "jane@example.com"})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/customers: %s", r)
	}
	id := r.Header.Get("X-Request-Id")
	entries, _ := hiddenAuditAll(t, e, e.Acme.Key, "")
	if id == "" || len(entries) != 1 || entries[0].RequestID != id {
		t.Fatalf("a request sent without an X-Request-Id was answered with X-Request-Id %q and recorded%s\nwant one entry with that \"request_id\"", id, hiddenAuditDump(entries))
	}
}

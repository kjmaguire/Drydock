package api_test

// Hidden acceptance test of the trial task tally-seat-limits (evals/tasks). The agents never see it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
)

// hiddenSeatsOf returns GET /v1/org's "seats": used, and limit (nil when null). "limit" must be there, null or a number.
func hiddenSeatsOf(t *testing.T, e *apitest.Env, key string) (int, *int) {
	t.Helper()
	r := e.Do("GET", "/v1/org", key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/org: want 200, got %s", r)
	}
	var body struct {
		Seats map[string]json.RawMessage `json:"seats"`
	}
	r.JSON(t, &body)
	rawUsed, okUsed := body.Seats["used"]
	rawLimit, okLimit := body.Seats["limit"]
	var used int
	var limit *int
	if !okUsed || !okLimit || json.Unmarshal(rawUsed, &used) != nil || json.Unmarshal(rawLimit, &limit) != nil {
		t.Fatalf("GET /v1/org: want \"seats\": {\"used\": <n>, \"limit\": <n, or null when unlimited>}, got %s", r)
	}
	return used, limit
}

func hiddenLimitText(limit *int) string {
	if limit == nil {
		return "null"
	}
	return fmt.Sprint(*limit)
}

// hiddenWantSeats checks GET /v1/org's seats; a negative limit means null (unlimited).
func hiddenWantSeats(t *testing.T, e *apitest.Env, key, what string, used, limit int) {
	t.Helper()
	gotUsed, gotLimit := hiddenSeatsOf(t, e, key)
	want := "null"
	if limit >= 0 {
		want = fmt.Sprint(limit)
	}
	if gotUsed != used || hiddenLimitText(gotLimit) != want {
		t.Fatalf("%s: GET /v1/org shows \"seats\": {\"used\": %d, \"limit\": %s}, want {\"used\": %d, \"limit\": %s}",
			what, gotUsed, hiddenLimitText(gotLimit), used, want)
	}
}

// hiddenWantLimitError checks a refusal: the status with {"error": code, "limit": limit, "used": used}.
func hiddenWantLimitError(t *testing.T, what string, r *apitest.Response, status int, code string, limit, used int) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
		Limit *int   `json:"limit"`
		Used  *int   `json:"used"`
	}
	if r.Code != status || json.Unmarshal(r.Body, &body) != nil || body.Error != code || body.Limit == nil || body.Used == nil ||
		*body.Limit != limit || *body.Used != used {
		t.Fatalf("%s: want %d {\"error\":%q,\"limit\":%d,\"used\":%d}, got %s", what, status, code, limit, used, r)
	}
}

func hiddenInvite(e *apitest.Env, key, email string, role store.Role) *apitest.Response {
	return e.Do("POST", "/v1/invites", key, map[string]string{"email": email, "role": string(role)})
}

func hiddenMustInvite(t *testing.T, e *apitest.Env, key, email string, role store.Role) store.Invite {
	t.Helper()
	r := hiddenInvite(e, key, email, role)
	if r.Code != http.StatusCreated {
		t.Fatalf("invite %s as %s: want 201, got %s", email, role, r)
	}
	var inv store.Invite
	r.JSON(t, &inv)
	return inv
}

var hiddenTokenRe = regexp.MustCompile(`token=([^\s&]+)`)

// hiddenAccept accepts the newest invite emailed to the address, with the token of its link.
func hiddenAccept(t *testing.T, e *apitest.Env, email string) *apitest.Response {
	t.Helper()
	token := ""
	for _, m := range e.Mail.Messages() {
		if strings.EqualFold(m.To, email) {
			if sub := hiddenTokenRe.FindStringSubmatch(m.Body); sub != nil {
				token, _ = url.QueryUnescape(sub[1])
			}
		}
	}
	if token == "" {
		t.Fatalf("setting up: no invite email with a token was sent to %s", email)
	}
	return e.Do("POST", "/v1/invites/accept", "", map[string]string{"token": token, "name": "New Person"})
}

func hiddenPatchRole(e *apitest.Env, key, userID string, role store.Role) *apitest.Response {
	return e.Do("PATCH", "/v1/members/"+userID, key, map[string]string{"role": string(role)})
}

func hiddenWant(t *testing.T, what string, r *apitest.Response, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("%s: want %d, got %s", what, status, r)
	}
}

// hiddenSeedMembers adds n members with a role straight through the store, the way the admin tool or data from before the
// limits can go over a plan's seats. It reports false, and the caller skips its checks of an organization over its limit,
// when the store refuses.
func hiddenSeedMembers(t *testing.T, e *apitest.Env, org apitest.Org, prefix string, n int, role store.Role) bool {
	t.Helper()
	now := e.Clock.Now()
	for i := range n {
		u, err := e.Store.CreateUser(fmt.Sprintf("%s%d@seeded.example", prefix, i), "Seeded", now)
		if err != nil {
			t.Fatalf("setting up: CreateUser: %v", err)
		}
		if _, err := e.Store.AddMember(org.ID, u.ID, role, now); err != nil {
			t.Logf("store.AddMember refuses a member beyond the plan's seats (%v); the checks of an organization over its limit are skipped", err)
			return false
		}
	}
	return true
}

func hiddenRoleOf(t *testing.T, e *apitest.Env, key, userID string) string {
	t.Helper()
	var members struct {
		Items []struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/members", key, nil, &members)
	for _, m := range members.Items {
		if m.UserID == userID {
			return m.Role
		}
	}
	return ""
}

func hiddenPendingEmails(t *testing.T, e *apitest.Env, key string) []string {
	t.Helper()
	var invs struct {
		Items []struct {
			Email string `json:"email"`
		} `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/invites", key, nil, &invs)
	out := []string{}
	for _, inv := range invs.Items {
		out = append(out, inv.Email)
	}
	sort.Strings(out)
	return out
}

func TestHiddenSeatsShownAndCounted(t *testing.T) {
	e := apitest.New(t)
	hiddenWantSeats(t, e, e.Acme.Key, "Acme (team plan) with only its owner", 1, 10)
	hiddenWantSeats(t, e, e.Globex.Key, "Globex (business plan) with only its owner", 1, -1)
	free := e.NewOrg("Initech", store.OrgFree, "UTC")
	hiddenWantSeats(t, e, free.Key, "a free organization with only its owner", 1, 3)

	key := e.Acme.Key
	admin, _ := e.AddMember(e.Acme, "admin@acme.example", store.RoleAdmin)
	e.AddMember(e.Acme, "billing@acme.example", store.RoleBilling)
	member, _ := e.AddMember(e.Acme, "member@acme.example", store.RoleMember)
	viewer, viewerKey := e.AddMember(e.Acme, "viewer@acme.example", store.RoleViewer)
	hiddenWantSeats(t, e, key, "an owner, an admin, a billing member, a member and a viewer", 4, 10)
	hiddenWantSeats(t, e, viewerKey, "the same, read by the viewer", 4, 10)

	hiddenMustInvite(t, e, key, "inv-member@people.example", store.RoleMember)
	hiddenWantSeats(t, e, key, "and a pending member invite", 5, 10)
	hiddenMustInvite(t, e, key, "inv-viewer@people.example", store.RoleViewer)
	hiddenWantSeats(t, e, key, "and a pending viewer invite", 5, 10)
	hiddenMustInvite(t, e, key, "inv-admin@people.example", store.RoleAdmin)
	billing := hiddenMustInvite(t, e, key, "inv-billing@people.example", store.RoleBilling)
	hiddenWantSeats(t, e, key, "and pending admin and billing invites", 7, 10)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+billing.ID, key, nil, nil)
	hiddenWantSeats(t, e, key, "after revoking the billing invite", 6, 10)

	hiddenMustInvite(t, e, key, "joiner@people.example", store.RoleMember)
	hiddenWantSeats(t, e, key, "and another pending member invite", 7, 10)
	hiddenWant(t, "accept a member invite", hiddenAccept(t, e, "joiner@people.example"), http.StatusCreated)
	hiddenWantSeats(t, e, key, "after the member invite was accepted (a member now, no longer a pending invite)", 7, 10)
	hiddenWant(t, "accept a viewer invite", hiddenAccept(t, e, "inv-viewer@people.example"), http.StatusCreated)
	hiddenWantSeats(t, e, key, "after the viewer invite was accepted", 7, 10)

	hiddenWant(t, "make the viewer a member", hiddenPatchRole(e, key, viewer.ID, store.RoleMember), http.StatusOK)
	hiddenWantSeats(t, e, key, "after a viewer was made a member", 8, 10)
	hiddenWant(t, "make a member a viewer", hiddenPatchRole(e, key, member.ID, store.RoleViewer), http.StatusOK)
	hiddenWantSeats(t, e, key, "after a member was made a viewer", 7, 10)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+member.ID, key, nil, nil)
	hiddenWantSeats(t, e, key, "after removing a viewer", 7, 10)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+admin.ID, key, nil, nil)
	hiddenWantSeats(t, e, key, "after removing the admin", 6, 10)
}

func TestHiddenInvitesNeedASeat(t *testing.T) {
	e := apitest.New(t)
	free := e.NewOrg("Initech", store.OrgFree, "UTC")
	hiddenMustInvite(t, e, free.Key, "a@people.example", store.RoleMember)
	billing := hiddenMustInvite(t, e, free.Key, "b@people.example", store.RoleBilling)
	for _, role := range []store.Role{store.RoleMember, store.RoleAdmin, store.RoleBilling} {
		hiddenWantLimitError(t, "invite a new "+string(role)+" to a free organization (3 seats) with its owner and two pending invites",
			hiddenInvite(e, free.Key, "late-"+string(role)+"@people.example", role), http.StatusPaymentRequired, "seat_limit_reached", 3, 3)
	}
	hiddenMustInvite(t, e, free.Key, "watcher@people.example", store.RoleViewer)
	hiddenWantSeats(t, e, free.Key, "a free organization with its owner, two pending invites and a pending viewer invite", 3, 3)
	if got, want := hiddenPendingEmails(t, e, free.Key), []string{"a@people.example", "b@people.example", "watcher@people.example"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("GET /v1/invites lists %v, want %v: a refused invite must not be created", got, want)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+billing.ID, free.Key, nil, nil)
	hiddenMustInvite(t, e, free.Key, "c@people.example", store.RoleMember)
	hiddenWantLimitError(t, "invite when the seat a revoked invite freed is taken again", hiddenInvite(e, free.Key, "d@people.example", store.RoleMember),
		http.StatusPaymentRequired, "seat_limit_reached", 3, 3)

	key := e.Acme.Key
	for i := range 9 {
		hiddenMustInvite(t, e, key, fmt.Sprintf("t%d@people.example", i), store.RoleMember)
	}
	hiddenWantLimitError(t, "invite to Acme (team, 10 seats) with its owner and 9 pending invites", hiddenInvite(e, key, "t9@people.example", store.RoleAdmin),
		http.StatusPaymentRequired, "seat_limit_reached", 10, 10)
	hiddenWantSeats(t, e, key, "Acme with its owner and 9 pending invites", 10, 10)
	if hiddenSeedMembers(t, e, e.Acme, "over", 2, store.RoleMember) {
		hiddenWantSeats(t, e, key, "Acme after two more members were added outside the API", 12, 10)
		hiddenWantLimitError(t, "invite when over the seat limit", hiddenInvite(e, key, "t10@people.example", store.RoleMember),
			http.StatusPaymentRequired, "seat_limit_reached", 10, 12)
		hiddenMustInvite(t, e, key, "viewer-over@people.example", store.RoleViewer)
	}

	for i := range 12 {
		hiddenMustInvite(t, e, e.Globex.Key, fmt.Sprintf("g%d@people.example", i), store.RoleMember)
	}
	hiddenWantSeats(t, e, e.Globex.Key, "Globex (business, unlimited) with its owner and 12 pending invites", 13, -1)
}

func TestHiddenAcceptingAnInviteNeverNeedsASeat(t *testing.T) {
	e := apitest.New(t)
	free := e.NewOrg("Initech", store.OrgFree, "UTC")
	hiddenMustInvite(t, e, free.Key, "a@people.example", store.RoleMember)
	hiddenMustInvite(t, e, free.Key, "b@people.example", store.RoleAdmin)
	hiddenWantSeats(t, e, free.Key, "a free organization with its owner and two pending invites", 3, 3)
	hiddenWant(t, "accept an invite while every seat is used (the invite holds one)", hiddenAccept(t, e, "a@people.example"), http.StatusCreated)
	hiddenWant(t, "accept the other invite", hiddenAccept(t, e, "b@people.example"), http.StatusCreated)
	hiddenWantSeats(t, e, free.Key, "after both invites were accepted", 3, 3)
	var members struct {
		Items []json.RawMessage `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/members", free.Key, nil, &members)
	if len(members.Items) != 3 {
		t.Fatalf("the free organization has %d members after two accepted invites, want 3", len(members.Items))
	}

	key := e.Acme.Key
	hiddenMustInvite(t, e, key, "pending@people.example", store.RoleBilling)
	if hiddenSeedMembers(t, e, e.Acme, "seat", 10, store.RoleMember) {
		hiddenWantSeats(t, e, key, "Acme with a pending invite and members added outside the API", 12, 10)
		hiddenWant(t, "accept an invite while the organization is over its seat limit", hiddenAccept(t, e, "pending@people.example"), http.StatusCreated)
		hiddenWantSeats(t, e, key, "after the invite was accepted", 12, 10)
	}
}

func TestHiddenPromotingAViewerNeedsASeat(t *testing.T) {
	e := apitest.New(t)
	free := e.NewOrg("Initech", store.OrgFree, "UTC")
	viewer, _ := e.AddMember(free, "viewer@initech.example", store.RoleViewer)
	viewer2, _ := e.AddMember(free, "viewer2@initech.example", store.RoleViewer)
	member, _ := e.AddMember(free, "member@initech.example", store.RoleMember)
	hiddenMustInvite(t, e, free.Key, "pending@people.example", store.RoleMember)
	for _, role := range []store.Role{store.RoleMember, store.RoleAdmin, store.RoleBilling} {
		hiddenWantLimitError(t, "PATCH a viewer to "+string(role)+" in a free organization (3 seats) with its owner, a member and a pending invite",
			hiddenPatchRole(e, free.Key, viewer.ID, role), http.StatusPaymentRequired, "seat_limit_reached", 3, 3)
	}
	hiddenWantSeats(t, e, free.Key, "the owner, a member, a pending invite and two viewers", 3, 3)
	if got := hiddenRoleOf(t, e, free.Key, viewer.ID); got != string(store.RoleViewer) {
		t.Fatalf("after refused role changes the viewer's role is %q, want viewer", got)
	}
	hiddenWant(t, "PATCH a viewer to viewer when every seat is used", hiddenPatchRole(e, free.Key, viewer2.ID, store.RoleViewer), http.StatusOK)
	hiddenWant(t, "PATCH a member to billing when every seat is used", hiddenPatchRole(e, free.Key, member.ID, store.RoleBilling), http.StatusOK)
	hiddenWantSeats(t, e, free.Key, "after changes that take no seat", 3, 3)
	hiddenWant(t, "PATCH a billing member to viewer", hiddenPatchRole(e, free.Key, member.ID, store.RoleViewer), http.StatusOK)
	hiddenWantSeats(t, e, free.Key, "after a billing member became a viewer", 2, 3)
	hiddenWant(t, "PATCH a viewer to admin with a seat free", hiddenPatchRole(e, free.Key, viewer.ID, store.RoleAdmin), http.StatusOK)
	hiddenWantSeats(t, e, free.Key, "after a viewer became an admin", 3, 3)
	if got := hiddenRoleOf(t, e, free.Key, viewer.ID); got != string(store.RoleAdmin) {
		t.Fatalf("the promoted viewer's role is %q, want admin", got)
	}
}

func TestHiddenPlanChangeNeedsTheSeats(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	var members []store.User
	for i := range 4 {
		u, _ := e.AddMember(e.Acme, fmt.Sprintf("m%d@acme.example", i), store.RoleMember)
		members = append(members, u)
	}
	var before store.Org
	e.MustDo(http.StatusOK, "GET", "/v1/org", key, nil, &before)
	hiddenWantLimitError(t, "PATCH /v1/org to the free plan with 5 seats used",
		e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "free", "name": "Acme Renamed", "tax_rate_bp": 500}),
		http.StatusConflict, "over_seat_limit", 3, 5)
	var after store.Org
	e.MustDo(http.StatusOK, "GET", "/v1/org", key, nil, &after)
	if after != before {
		t.Fatalf("a refused plan change changed the organization: %+v, was %+v", after, before)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+members[0].ID, key, nil, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/members/"+members[1].ID, key, nil, nil)
	r := e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "free"})
	hiddenWant(t, "PATCH /v1/org to the free plan with exactly 3 seats used", r, http.StatusOK)
	e.MustDo(http.StatusOK, "GET", "/v1/org", key, nil, &after)
	if after.Plan != store.OrgFree {
		t.Fatalf("after PATCH /v1/org to the free plan the plan is %q", after.Plan)
	}
	hiddenWantSeats(t, e, key, "Acme on the free plan", 3, 3)

	hiddenWant(t, "PATCH /v1/org to the business plan", e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "business"}), http.StatusOK)
	hiddenWantSeats(t, e, key, "Acme on the business plan", 3, -1)
	var invites []store.Invite
	for i := range 9 {
		invites = append(invites, hiddenMustInvite(t, e, key, fmt.Sprintf("b%d@people.example", i), store.RoleMember))
	}
	hiddenWantSeats(t, e, key, "Acme on the business plan with 9 pending invites", 12, -1)
	hiddenWantLimitError(t, "PATCH /v1/org to the team plan with 12 seats used", e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "team"}),
		http.StatusConflict, "over_seat_limit", 10, 12)
	hiddenWantLimitError(t, "PATCH /v1/org to the free plan with 12 seats used", e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "free"}),
		http.StatusConflict, "over_seat_limit", 3, 12)
	e.MustDo(http.StatusOK, "GET", "/v1/org", key, nil, &after)
	if after.Plan != store.OrgBusiness {
		t.Fatalf("after refused plan changes the plan is %q, want business", after.Plan)
	}
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+invites[0].ID, key, nil, nil)
	e.MustDo(http.StatusNoContent, "DELETE", "/v1/invites/"+invites[1].ID, key, nil, nil)
	hiddenWant(t, "PATCH /v1/org to the team plan with exactly 10 seats used", e.Do("PATCH", "/v1/org", key, map[string]any{"plan": "team"}), http.StatusOK)
	hiddenWantSeats(t, e, key, "Acme back on the team plan", 10, 10)
	if hiddenSeedMembers(t, e, e.Acme, "x", 2, store.RoleMember) {
		hiddenWant(t, "PATCH /v1/org without a plan change while over the seat limit",
			e.Do("PATCH", "/v1/org", key, map[string]any{"name": "Acme Corp", "tax_rate_bp": 700}), http.StatusOK)
		e.MustDo(http.StatusOK, "GET", "/v1/org", key, nil, &after)
		if after.Name != "Acme Corp" || after.TaxRateBP != 700 || after.Plan != store.OrgTeam {
			t.Fatalf("PATCH /v1/org of the name and tax rate: the organization is %+v", after)
		}
		hiddenWantSeats(t, e, key, "Acme over its seat limit", 12, 10)
	}
}

// hiddenHogStore takes the store's lock over and over, holding it 2 milliseconds each time (store.UpdateOrg calls its
// function with the lock held), until stop is closed. It changes nothing. It closes holding the first time it holds the
// lock.
//
// Two of these keep Go's mutex in its starvation mode, where it hands the lock to its waiters strictly in turn (every
// waiter has waited more than a millisecond): requests queued behind them then take turns at the lock, one store call each,
// so a seat check and an invite made under two separate holds of the lock interleave with the other requests' checks.
func hiddenHogStore(e *apitest.Env, orgID string, stop <-chan struct{}, holding chan<- struct{}) {
	first := true
	for {
		select {
		case <-stop:
			return
		default:
		}
		_, _ = e.Store.UpdateOrg(orgID, func(*store.Org) error {
			if first {
				first = false
				close(holding)
			}
			time.Sleep(2 * time.Millisecond)
			return nil
		})
	}
}

// TestHiddenConcurrentInvitesTakeTheLastSeat sends many invites at the same moment when one seat is left: exactly one may
// succeed. Every round starts the requests together while the store's lock is busy (hiddenHogStore), which makes a race
// between the seat check and the invite show.
func TestHiddenConcurrentInvitesTakeTheLastSeat(t *testing.T) {
	const rounds, racers = 10, 16
	for round := range rounds {
		e := apitest.New(t)
		for i := range 8 { // Acme (team, 10 seats): its owner and 8 members, one seat left
			e.AddMember(e.Acme, fmt.Sprintf("m%d@acme.example", i), store.RoleMember)
		}

		recs := make([]*httptest.ResponseRecorder, racers)
		reqs := make([]*http.Request, racers)
		for i := range racers {
			body := fmt.Sprintf(`{"email":"racer%d@people.example","role":"member"}`, i)
			reqs[i] = httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
			reqs[i].Header.Set("Authorization", "Bearer "+e.Acme.Key)
			reqs[i].Header.Set("Content-Type", "application/json")
			recs[i] = httptest.NewRecorder()
		}
		stopHogs := make(chan struct{})
		var hogs sync.WaitGroup
		for range 2 {
			holding := make(chan struct{})
			hogs.Add(1)
			go func() {
				defer hogs.Done()
				hiddenHogStore(e, e.Globex.ID, stopHogs, holding)
			}()
			select {
			case <-holding:
			case <-time.After(60 * time.Second):
				t.Fatalf("round %d: setting up: store.UpdateOrg did not call its function within 60 seconds", round)
			}
		}
		var wg sync.WaitGroup
		for i := range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.Server.ServeHTTP(recs[i], reqs[i])
			}()
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		timeout := time.After(60 * time.Second)
		select {
		case <-done:
		case <-timeout:
		}
		close(stopHogs)
		hogs.Wait()
		select {
		case <-done:
		default:
			t.Fatalf("round %d: %d invites sent at the same time did not all answer within 60 seconds", round, racers)
		}

		created := []string{}
		for i, rec := range recs {
			r := &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
			switch r.Code {
			case http.StatusCreated:
				created = append(created, fmt.Sprintf("racer%d@people.example", i))
			case http.StatusPaymentRequired:
			default:
				t.Fatalf("round %d: one of %d invites sent at the same time with one seat left: want 201 or 402, got %s", round, racers, r)
			}
		}
		if len(created) != 1 {
			t.Fatalf("round %d: %d invites sent at the same time with one seat left: %d succeeded %v, want exactly 1", round, racers, len(created), created)
		}
		for i, rec := range recs {
			if rec.Code == http.StatusPaymentRequired {
				r := &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
				hiddenWantLimitError(t, fmt.Sprintf("round %d: invite %d of %d sent at the same time with one seat left", round, i, racers), r,
					http.StatusPaymentRequired, "seat_limit_reached", 10, 10)
			}
		}
		if got := hiddenPendingEmails(t, e, e.Acme.Key); strings.Join(got, ",") != created[0] {
			t.Fatalf("round %d: pending invites %v, want only %s", round, got, created[0])
		}
		hiddenWantSeats(t, e, e.Acme.Key, fmt.Sprintf("round %d: after the race for the last seat", round), 10, 10)
	}
}

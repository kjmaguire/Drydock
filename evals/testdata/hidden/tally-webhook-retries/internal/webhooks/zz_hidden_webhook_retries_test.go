package webhooks_test

// Hidden acceptance test of the trial task tally-webhook-retries (evals/tasks). The agents never see it.
// Deliveries are read through the HTTP API (GET /v1/webhook-endpoints/{id}/deliveries); time only moves with e.Clock.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/store"
	"example.com/tally/internal/webhooks"
)

// hiddenRetryDelays are the waits the task names before the 2nd to the 7th attempt.
var hiddenRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 8 * time.Hour, 24 * time.Hour}

const hiddenRetrySubject = "Webhook endpoint failing: "

type hiddenRetryDelivery struct {
	raw            string
	fields         map[string]json.RawMessage
	ID             string
	EventID        string
	Status         string
	Attempts       int
	LastAttemptAt  *time.Time
	NextAttemptAt  *time.Time
	ResponseStatus int    // 0 when absent or null
	Error          string // "" when absent or null
}

func hiddenRetryEndpoint(t *testing.T, e *apitest.Env, org apitest.Org, url string) string {
	t.Helper()
	var ep struct {
		ID string `json:"id"`
	}
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", org.Key, map[string]any{"url": url, "events": []string{"*"}}, &ep)
	return ep.ID
}

func hiddenRetryDeliveries(t *testing.T, e *apitest.Env, org apitest.Org, endpointID string) []hiddenRetryDelivery {
	t.Helper()
	r := e.Do("GET", "/v1/webhook-endpoints/"+endpointID+"/deliveries", org.Key, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET deliveries of %s: want 200, got %s", endpointID, r)
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	r.JSON(t, &page)
	out := make([]hiddenRetryDelivery, 0, len(page.Items))
	for _, item := range page.Items {
		d := hiddenRetryDelivery{raw: string(item)}
		if err := json.Unmarshal(item, &d.fields); err != nil {
			t.Fatalf("delivery %s: %v", item, err)
		}
		get := func(name string, v any) {
			if raw, ok := d.fields[name]; ok && string(raw) != "null" {
				if err := json.Unmarshal(raw, v); err != nil {
					t.Fatalf("delivery field %s = %s: %v\n%s", name, raw, err, item)
				}
			}
		}
		get("id", &d.ID)
		get("event_id", &d.EventID)
		get("status", &d.Status)
		get("attempts", &d.Attempts)
		get("last_attempt_at", &d.LastAttemptAt)
		get("next_attempt_at", &d.NextAttemptAt)
		get("response_status", &d.ResponseStatus)
		get("error", &d.Error)
		out = append(out, d)
	}
	return out
}

// hiddenRetryOne returns the one delivery of the endpoint.
func hiddenRetryOne(t *testing.T, e *apitest.Env, endpointID string) hiddenRetryDelivery {
	t.Helper()
	ds := hiddenRetryDeliveries(t, e, e.Acme, endpointID)
	if len(ds) != 1 {
		t.Fatalf("want 1 delivery to %s, got %d", endpointID, len(ds))
	}
	return ds[0]
}

func hiddenRetryRequests(e *apitest.Env, url string) int {
	n := 0
	for _, r := range e.HTTP.Requests() {
		if r.URL == url {
			n++
		}
	}
	return n
}

func hiddenRetryRunDue(t *testing.T, e *apitest.Env, want int, what string) {
	t.Helper()
	n, err := e.Webhooks.RunDue(context.Background())
	if err != nil || n != want {
		t.Fatalf("%s: RunDue at %s = %d, %v; want %d attempts", what, e.Clock.Now().Format(time.RFC3339), n, err, want)
	}
}

func hiddenRetryPublish(t *testing.T, e *apitest.Env, org apitest.Org, n int) {
	t.Helper()
	if err := e.Webhooks.Publish(context.Background(), org.ID, "customer.updated", map[string]any{"n": n}); err != nil {
		t.Fatal(err)
	}
}

func hiddenRetryTime(p *time.Time) string {
	if p == nil {
		return "null"
	}
	return p.UTC().Format(time.RFC3339)
}

// hiddenRetryWantNoNext fails unless the delivery JSON shows "next_attempt_at": null.
func hiddenRetryWantNoNext(t *testing.T, d hiddenRetryDelivery, what string) {
	t.Helper()
	raw, ok := d.fields["next_attempt_at"]
	if !ok || !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		t.Fatalf("%s: want \"next_attempt_at\": null (no attempt is planned), got %s in\n%s", what, map[bool]string{true: string(raw), false: "no next_attempt_at field"}[ok], d.raw)
	}
}

func TestHiddenRetryScheduleCountsFromEachFailedAttempt(t *testing.T) {
	e := apitest.New(t)
	const url = "https://hooks.acme.example/retry-schedule"
	id := hiddenRetryEndpoint(t, e, e.Acme, url)

	// Each attempt fails in another way; some runs come late, and the next wait counts from the attempt that failed.
	steps := []struct {
		name   string
		status int // 0: no answer
		err    error
		late   time.Duration // how long after its due time the attempt is made
	}{
		{"500", 500, nil, 0},
		{"a connection error", 0, errors.New("dial tcp 93.184.216.34:443: connect: connection refused"), 20 * time.Second},
		{"a redirect (302, not followed)", 302, nil, 0},
		{"no answer in time", 0, fmt.Errorf("Post %q: %w", url, context.DeadlineExceeded), 3 * time.Minute},
		{"404", 404, nil, 0},
		{"503", 503, nil, 61 * time.Second},
		{"500 again", 500, nil, 0},
	}
	hiddenRetryPublish(t, e, e.Acme, 1)
	at := e.Clock.Now()
	for i, st := range steps {
		if i > 0 {
			due := at.Add(hiddenRetryDelays[i-1])
			e.Clock.Set(due.Add(-time.Second))
			before := hiddenRetryRequests(e, url)
			hiddenRetryRunDue(t, e, 0, fmt.Sprintf("a second before attempt %d is due (%s after attempt %d)", i+1, hiddenRetryDelays[i-1], i))
			if got := hiddenRetryRequests(e, url); got != before {
				t.Fatalf("attempt %d was sent before it was due", i+1)
			}
			at = due.Add(st.late)
		}
		e.Clock.Set(at)
		status, err := st.status, st.err
		e.HTTP.Respond = func(apitest.Request) (int, error) { return status, err }
		before := hiddenRetryRequests(e, url)
		hiddenRetryRunDue(t, e, 1, fmt.Sprintf("attempt %d", i+1))
		if got := hiddenRetryRequests(e, url); got != before+1 {
			t.Fatalf("attempt %d: want one request to the endpoint, got %d", i+1, got-before)
		}

		d := hiddenRetryOne(t, e, id)
		what := fmt.Sprintf("after attempt %d failed with %s", i+1, st.name)
		if d.Attempts != i+1 {
			t.Fatalf("%s: want attempts %d, got %d\n%s", what, i+1, d.Attempts, d.raw)
		}
		if d.LastAttemptAt == nil || !d.LastAttemptAt.Equal(at) {
			t.Fatalf("%s: want last_attempt_at %s, got %s\n%s", what, at.Format(time.RFC3339), hiddenRetryTime(d.LastAttemptAt), d.raw)
		}
		if st.status != 0 && d.ResponseStatus != st.status {
			t.Fatalf("%s: want response_status %d (the latest attempt's), got %d\n%s", what, st.status, d.ResponseStatus, d.raw)
		}
		if st.status == 0 && d.ResponseStatus != 0 {
			t.Fatalf("%s: the latest attempt got no answer, so response_status should be empty, got %d\n%s", what, d.ResponseStatus, d.raw)
		}
		if d.Error == "" {
			t.Fatalf("%s: want the latest attempt's error, got none\n%s", what, d.raw)
		}
		if i < len(steps)-1 {
			next := at.Add(hiddenRetryDelays[i])
			if d.Status != "pending" {
				t.Fatalf("%s: want status pending (it is tried again), got %q\n%s", what, d.Status, d.raw)
			}
			if d.NextAttemptAt == nil || !d.NextAttemptAt.Equal(next) {
				t.Fatalf("%s: want next_attempt_at %s (%s after this attempt), got %s\n%s", what, next.Format(time.RFC3339), hiddenRetryDelays[i], hiddenRetryTime(d.NextAttemptAt), d.raw)
			}
		} else {
			if d.Status != "failed" {
				t.Fatalf("%s: want status failed for good after 7 attempts, got %q\n%s", what, d.Status, d.raw)
			}
			hiddenRetryWantNoNext(t, d, what)
		}
	}

	before := hiddenRetryRequests(e, url)
	for _, later := range []time.Duration{time.Minute, 24 * time.Hour, 72 * time.Hour} {
		e.Clock.Set(at.Add(later))
		hiddenRetryRunDue(t, e, 0, fmt.Sprintf("%s after the 7th attempt", later))
	}
	if got := hiddenRetryRequests(e, url); got != before {
		t.Fatalf("a delivery that failed for good was sent again (%d more requests)", got-before)
	}
}

func TestHiddenRetrySucceedsOnALaterAttempt(t *testing.T) {
	e := apitest.New(t)
	const url = "https://hooks.acme.example/retry-recovers"
	id := hiddenRetryEndpoint(t, e, e.Acme, url)
	hiddenRetryPublish(t, e, e.Acme, 1)

	e.HTTP.Respond = func(apitest.Request) (int, error) { return http.StatusBadGateway, nil }
	hiddenRetryRunDue(t, e, 1, "attempt 1")
	e.Clock.Advance(time.Minute)
	e.HTTP.Respond = func(apitest.Request) (int, error) { return 0, errors.New("connection reset by peer") }
	hiddenRetryRunDue(t, e, 1, "attempt 2")
	e.Clock.Advance(5 * time.Minute)
	e.HTTP.Respond = func(apitest.Request) (int, error) { return http.StatusNoContent, nil }
	hiddenRetryRunDue(t, e, 1, "attempt 3")
	at := e.Clock.Now()

	d := hiddenRetryOne(t, e, id)
	what := "after a third attempt that got 204"
	if d.Status != "succeeded" || d.Attempts != 3 || d.ResponseStatus != http.StatusNoContent || d.Error != "" {
		t.Fatalf("%s: want status succeeded, attempts 3, response_status 204 and no error, got\n%s", what, d.raw)
	}
	if d.LastAttemptAt == nil || !d.LastAttemptAt.Equal(at) {
		t.Fatalf("%s: want last_attempt_at %s, got %s\n%s", what, at.Format(time.RFC3339), hiddenRetryTime(d.LastAttemptAt), d.raw)
	}
	hiddenRetryWantNoNext(t, d, what)
	e.Clock.Advance(48 * time.Hour)
	hiddenRetryRunDue(t, e, 0, "two days after the delivery succeeded")
	if got := hiddenRetryRequests(e, url); got != 3 {
		t.Fatalf("want 3 requests in all, got %d", got)
	}
	if msgs := hiddenRetryFailingEmails(e); len(msgs) != 0 {
		t.Fatalf("a delivery that succeeded sent failure emails: %+v", msgs)
	}
}

func TestHiddenRetryGoneDisablesTheEndpoint(t *testing.T) {
	e := apitest.New(t)
	const goneURL, okURL = "https://hooks.acme.example/retry-gone", "https://hooks.acme.example/retry-healthy"
	goneID := hiddenRetryEndpoint(t, e, e.Acme, goneURL)
	okID := hiddenRetryEndpoint(t, e, e.Acme, okURL)
	goneStatus := http.StatusInternalServerError
	e.HTTP.Respond = func(r apitest.Request) (int, error) {
		if r.URL == goneURL {
			return goneStatus, nil
		}
		return http.StatusOK, nil
	}

	// The first event's delivery to the gone endpoint fails and waits for its retry a minute later.
	t0 := e.Clock.Now()
	hiddenRetryPublish(t, e, e.Acme, 1)
	hiddenRetryRunDue(t, e, 2, "the first event")

	// Ten seconds later the endpoint answers 410 Gone to the second event.
	e.Clock.Set(t0.Add(10 * time.Second))
	goneStatus = http.StatusGone
	hiddenRetryPublish(t, e, e.Acme, 2)
	hiddenRetryRunDue(t, e, 2, "the second event")

	var ep map[string]any
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+goneID, e.Acme.Key, nil, &ep)
	if ep["enabled"] != false || ep["disabled_reason"] != "gone" {
		t.Fatalf("after a 410 the endpoint should be disabled with disabled_reason \"gone\", got %v", ep)
	}
	var answered hiddenRetryDelivery
	var shown []string
	for _, d := range hiddenRetryDeliveries(t, e, e.Acme, goneID) {
		shown = append(shown, d.raw)
		if d.ResponseStatus == http.StatusGone {
			answered = d
		}
	}
	if answered.ID == "" {
		t.Fatalf("no delivery to the gone endpoint shows response_status 410:\n%s", strings.Join(shown, "\n"))
	}
	if answered.Status != "failed" || answered.Attempts != 1 {
		t.Fatalf("the delivery answered 410: want status failed (for good) after 1 attempt, got\n%s", answered.raw)
	}
	hiddenRetryWantNoNext(t, answered, "the delivery answered 410")

	// The first event's retry is not sent, now or later; the endpoint answers 200 from now on, which would show if it were.
	goneStatus = http.StatusOK
	for _, d := range []time.Duration{time.Minute, 6 * time.Minute, 36 * time.Minute, 3 * time.Hour, 11 * time.Hour, 36 * time.Hour, 72 * time.Hour} {
		e.Clock.Set(t0.Add(d))
		if _, err := e.Webhooks.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	hiddenRetryPublish(t, e, e.Acme, 3)
	if _, err := e.Webhooks.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hiddenRetryRequests(e, goneURL); got != 2 {
		t.Fatalf("the gone endpoint got %d requests, want 2: after the 410 its other pending deliveries must not be sent", got)
	}
	for _, d := range hiddenRetryDeliveries(t, e, e.Acme, goneID) {
		if d.Status == "succeeded" {
			t.Fatalf("a delivery to the disabled endpoint succeeded, so it was sent:\n%s", d.raw)
		}
	}
	// The other endpoint is not affected.
	okDs := hiddenRetryDeliveries(t, e, e.Acme, okID)
	if len(okDs) != 3 || hiddenRetryRequests(e, okURL) != 3 {
		t.Fatalf("the healthy endpoint: want 3 deliveries and 3 requests, got %d and %d", len(okDs), hiddenRetryRequests(e, okURL))
	}
	for _, d := range okDs {
		if d.Status != "succeeded" {
			t.Fatalf("a delivery to the healthy endpoint did not succeed:\n%s", d.raw)
		}
	}
}

// hiddenRetryFailingEmails returns the "Webhook endpoint failing" emails sent so far.
func hiddenRetryFailingEmails(e *apitest.Env) []mail.Message {
	var out []mail.Message
	for _, m := range e.Mail.Messages() {
		if strings.HasPrefix(m.Subject, hiddenRetrySubject) {
			out = append(out, m)
		}
	}
	return out
}

// hiddenRetryRunUntil makes every attempt that falls due up to until, each at the moment it is due, then sets the clock to until.
func hiddenRetryRunUntil(t *testing.T, e *apitest.Env, endpointIDs []string, until time.Time) {
	t.Helper()
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("deliveries never stop being due")
		}
		var next *time.Time
		for _, id := range endpointIDs {
			for _, d := range hiddenRetryDeliveries(t, e, e.Acme, id) {
				if d.Status == "pending" && d.NextAttemptAt != nil && (next == nil || d.NextAttemptAt.Before(*next)) {
					n := *d.NextAttemptAt
					next = &n
				}
			}
		}
		if next == nil || next.After(until) {
			break
		}
		if next.After(e.Clock.Now()) {
			e.Clock.Set(*next)
		}
		if n, err := e.Webhooks.RunDue(context.Background()); err != nil || n == 0 {
			t.Fatalf("RunDue at %s = %d, %v; a delivery was due then", e.Clock.Now().Format(time.RFC3339), n, err)
		}
	}
	e.Clock.Set(until)
}

// hiddenRetryEmailCount checks that each owner got `each` emails about each URL, and that there are no others. Emails sent
// in the background get ten seconds to arrive.
func hiddenRetryEmailCount(t *testing.T, e *apitest.Env, what string, urls []string, owners []string, each int) {
	t.Helper()
	want := map[string]int{}
	for _, u := range urls {
		for _, o := range owners {
			want[o+" | "+hiddenRetrySubject+u] = each
		}
	}
	var msgs []mail.Message
	ok := false
	for deadline := time.Now().Add(10 * time.Second); ; {
		msgs = hiddenRetryFailingEmails(e)
		got := map[string]int{}
		for _, m := range msgs {
			got[m.To+" | "+m.Subject]++
		}
		ok = len(msgs) == len(urls)*len(owners)*each
		for k, n := range want {
			if got[k] != n {
				ok = false
			}
		}
		if ok || len(msgs) > len(urls)*len(owners)*each || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ok {
		var lines []string
		for _, m := range msgs {
			lines = append(lines, fmt.Sprintf("  to %s: %q", m.To, m.Subject))
		}
		t.Fatalf("%s: want %d email(s) per owner (%s) per failing endpoint (%s) with the subject %q, got %d:\n%s",
			what, each, strings.Join(owners, ", "), strings.Join(urls, ", "), hiddenRetrySubject+"<endpoint URL>", len(msgs), strings.Join(lines, "\n"))
	}
}

func TestHiddenRetryOwnersEmailedOncePerEndpointPer24Hours(t *testing.T) {
	e := apitest.New(t)
	e.AddMember(e.Acme, "cfo@acme.example", store.RoleOwner)
	e.AddMember(e.Acme, "admin@acme.example", store.RoleAdmin)
	e.AddMember(e.Acme, "books@acme.example", store.RoleBilling)
	e.AddMember(e.Acme, "dev@acme.example", store.RoleMember)
	e.AddMember(e.Acme, "auditor@acme.example", store.RoleViewer)
	owners := []string{"owner@acme.example", "cfo@acme.example"}
	urls := []string{"https://hooks.acme.example/retry-mail-a", "https://hooks.acme.example/retry-mail-b"}
	ids := []string{hiddenRetryEndpoint(t, e, e.Acme, urls[0]), hiddenRetryEndpoint(t, e, e.Acme, urls[1])}
	e.HTTP.Respond = func(apitest.Request) (int, error) { return http.StatusServiceUnavailable, nil }

	// The whole schedule takes 1m+5m+30m+2h+8h+24h = 34h36m.
	t0 := e.Clock.Now()
	final := t0.Add(34*time.Hour + 36*time.Minute)
	hiddenRetryPublish(t, e, e.Acme, 1) // fails for good at final
	hiddenRetryRunUntil(t, e, ids, t0.Add(time.Hour))
	hiddenRetryPublish(t, e, e.Acme, 2) // fails for good at final+1h: within 24 hours of the first email
	hiddenRetryRunUntil(t, e, ids, t0.Add(26*time.Hour))
	hiddenRetryPublish(t, e, e.Acme, 3) // fails for good at final+26h: more than 24 hours after the first email

	hiddenRetryRunUntil(t, e, ids, final.Add(-time.Second))
	hiddenRetryEmailCount(t, e, "before any delivery failed for good", urls, owners, 0)
	hiddenRetryRunUntil(t, e, ids, final)
	for _, id := range ids {
		for _, d := range hiddenRetryDeliveries(t, e, e.Acme, id) {
			if d.EventID != "" && d.Attempts == 7 && d.Status != "failed" {
				t.Fatalf("a delivery with 7 attempts is not failed:\n%s", d.raw)
			}
		}
	}
	hiddenRetryEmailCount(t, e, "when the first deliveries failed for good after 7 attempts", urls, owners, 1)

	hiddenRetryRunUntil(t, e, ids, final.Add(2*time.Hour))
	hiddenRetryEmailCount(t, e, "an hour later, when the second deliveries failed for good", urls, owners, 1)

	hiddenRetryRunUntil(t, e, ids, final.Add(27*time.Hour))
	hiddenRetryEmailCount(t, e, "26 hours after the first emails, when the third deliveries failed for good", urls, owners, 2)

	for _, id := range ids {
		ds := hiddenRetryDeliveries(t, e, e.Acme, id)
		if len(ds) != 3 {
			t.Fatalf("want 3 deliveries to %s, got %d", id, len(ds))
		}
		for _, d := range ds {
			if d.Status != "failed" || d.Attempts != 7 {
				t.Fatalf("want every delivery failed for good after 7 attempts, got\n%s", d.raw)
			}
		}
	}
}

// TestHiddenRetryMailOption builds a webhooks Service with its own mail.Sender through webhooks.Options.Mail.
func TestHiddenRetryMailOption(t *testing.T) {
	e := apitest.New(t)
	box := &mail.Outbox{}
	svc := webhooks.New(webhooks.Options{Store: e.Store, Clock: e.Clock, Doer: e.HTTP, Resolver: e.DNS, Mail: box})
	const url = "https://hooks.globex.example/retry-option"
	if _, err := e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: e.Globex.ID, URL: url, Events: []string{"*"}, Secret: "whsec_hidden", Enabled: true}, e.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.HTTP.Respond = func(apitest.Request) (int, error) { return http.StatusInternalServerError, nil }
	ctx := context.Background()
	if err := svc.Publish(ctx, e.Globex.ID, "invoice.paid", map[string]string{"invoice_id": "in_hidden"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for i := 0; i < 7; i++ {
		n, err := svc.RunDue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		attempts += n
		if i < 6 {
			e.Clock.Advance(hiddenRetryDelays[i])
		}
	}
	if attempts != 7 {
		t.Fatalf("want 7 attempts, got %d", attempts)
	}
	var msgs []mail.Message
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) { // emails sent in the background get ten seconds
		if msgs = box.Messages(); len(msgs) > 0 || time.Now().After(deadline) {
			break
		}
	}
	if len(msgs) != 1 || msgs[0].To != "owner@globex.example" || msgs[0].Subject != hiddenRetrySubject+url {
		t.Fatalf("the Service's own Mail should get one email to owner@globex.example with the subject %q, got %+v", hiddenRetrySubject+url, msgs)
	}
	if got := hiddenRetryFailingEmails(e); len(got) != 0 {
		t.Fatalf("the email went to another sender than Options.Mail: %+v", got)
	}
}

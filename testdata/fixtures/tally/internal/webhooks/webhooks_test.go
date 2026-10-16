package webhooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/store"
	"example.com/tally/internal/webhooks"
)

func TestSign(t *testing.T) {
	// HMAC-SHA256("key", "The quick brown fox jumps over the lazy dog")
	const want = "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got := webhooks.Sign("key", []byte("The quick brown fox jumps over the lazy dog")); got != want {
		t.Fatalf("Sign = %s", got)
	}
}

func TestCheckURL(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	for _, ok := range []string{"https://hooks.acme.example/tally", "https://93.184.216.34:8443/in"} {
		if err := e.Webhooks.CheckURL(ctx, ok); err != nil {
			t.Errorf("CheckURL(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "hooks.acme.example/x", "ftp://hooks.acme.example/", "https:///x", "https://unknown.example/x"} {
		if err := e.Webhooks.CheckURL(ctx, bad); !errors.Is(err, webhooks.ErrInvalidURL) {
			t.Errorf("CheckURL(%q) = %v, want ErrInvalidURL", bad, err)
		}
	}
	if err := webhooks.CheckEvents([]string{"invoice.paid", "*"}); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]string{nil, {"invoice.eaten"}} {
		if err := webhooks.CheckEvents(bad); !errors.Is(err, webhooks.ErrInvalidEvents) {
			t.Errorf("CheckEvents(%v) = %v", bad, err)
		}
	}
}

func TestPublishAndDeliver(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	now := e.Clock.Now()
	all, _ := e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: e.Acme.ID, URL: "https://hooks.acme.example/all", Events: []string{"*"}, Secret: "whsec_a", Enabled: true}, now)
	paid, _ := e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: e.Acme.ID, URL: "https://hooks.acme.example/paid", Events: []string{"invoice.paid"}, Secret: "whsec_b", Enabled: true}, now)
	_, _ = e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: e.Acme.ID, URL: "https://hooks.acme.example/off", Events: []string{"*"}, Secret: "whsec_c", Enabled: false}, now)
	_, _ = e.Store.CreateWebhookEndpoint(store.WebhookEndpoint{OrgID: e.Globex.ID, URL: "https://hooks.globex.example/", Events: []string{"*"}, Secret: "whsec_d", Enabled: true}, now)

	if err := e.Webhooks.Publish(ctx, e.Acme.ID, "customer.created", map[string]string{"id": "cus_1"}); err != nil {
		t.Fatal(err)
	}
	n, err := e.Webhooks.RunDue(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunDue = %d, %v (only the endpoint for every event wants customer.created)", n, err)
	}
	reqs := e.HTTP.Requests()
	if len(reqs) != 1 || reqs[0].URL != all.URL || reqs[0].Method != http.MethodPost {
		t.Fatalf("requests = %+v", reqs)
	}
	if got, want := reqs[0].Header.Get("Tally-Signature"), webhooks.Sign("whsec_a", reqs[0].Body); got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}
	var body webhooks.Body
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil || body.Type != "customer.created" || reqs[0].Header.Get("Tally-Event-Id") != body.ID {
		t.Fatalf("body = %s (%v)", reqs[0].Body, err)
	}
	ds := e.Store.Deliveries(e.Acme.ID, all.ID)
	if len(ds) != 1 || ds[0].Status != store.DeliverySucceeded || ds[0].Attempts != 1 || ds[0].ResponseStatus != 200 {
		t.Fatalf("deliveries = %+v", ds)
	}

	e.HTTP.Respond = func(r apitest.Request) (int, error) { return http.StatusInternalServerError, nil }
	_ = e.Webhooks.Publish(ctx, e.Acme.ID, "invoice.paid", map[string]string{"invoice_id": "in_1"})
	if n, _ := e.Webhooks.RunDue(ctx); n != 2 {
		t.Fatalf("RunDue = %d, want 2", n)
	}
	for _, ep := range []store.WebhookEndpoint{all, paid} {
		ds := e.Store.Deliveries(e.Acme.ID, ep.ID)
		last := ds[len(ds)-1]
		if last.Status != store.DeliveryFailed || last.ResponseStatus != 500 || last.Error == "" {
			t.Fatalf("delivery to %s = %+v", ep.URL, last)
		}
	}
	if n, _ := e.Webhooks.RunDue(ctx); n != 0 {
		t.Fatalf("a failed delivery was sent again: %d", n)
	}
}

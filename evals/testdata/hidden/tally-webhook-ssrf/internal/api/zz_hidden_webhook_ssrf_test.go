package api_test

// Hidden acceptance test of the trial task tally-webhook-ssrf (evals/tasks). The agents never see it.
//
// Webhook URLs are checked when an endpoint is created or its URL changes (https only, no user info, only public
// addresses, whether written as IP literals or resolved through the webhooks Service's Resolver, e.DNS here), and the host
// is resolved again before every delivery attempt.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"example.com/tally/internal/apitest"
)

// hiddenSSRFHosts are the DNS answers of the test: name -> addresses.
var hiddenSSRFHosts = map[string][]string{
	// not public
	"loopback.hidden.example":         {"127.0.0.1"},
	"loopback-edge.hidden.example":    {"127.255.255.254"},
	"localhost":                       {"127.0.0.1"},
	"ten.hidden.example":              {"10.1.2.3"},
	"ten-edge.hidden.example":         {"10.255.255.255"},
	"oneseventwo.hidden.example":      {"172.16.0.1"},
	"oneseventwo-edge.hidden.example": {"172.31.255.254"},
	"oneninetwo.hidden.example":       {"192.168.1.20"},
	"metadata.hidden.example":         {"169.254.169.254"},
	"linklocal.hidden.example":        {"169.254.0.1"},
	"cgnat.hidden.example":            {"100.64.0.1"},
	"cgnat-edge.hidden.example":       {"100.127.255.254"},
	"cgnat-metadata.hidden.example":   {"100.100.100.200"},
	"zero.hidden.example":             {"0.0.0.0"},
	"zero-net.hidden.example":         {"0.1.2.3"},
	"multicast.hidden.example":        {"224.0.0.251"},
	"multicast-edge.hidden.example":   {"239.255.255.250"},
	"broadcast.hidden.example":        {"255.255.255.255"},
	"v6-loopback.hidden.example":      {"::1"},
	"v6-unspecified.hidden.example":   {"::"},
	"v6-ula.hidden.example":           {"fd12:3456:789a::1"},
	"v6-ula-low.hidden.example":       {"fc00::1"},
	"v6-linklocal.hidden.example":     {"fe80::1"},
	"v6-multicast.hidden.example":     {"ff02::1"},
	"v6-mapped.hidden.example":        {"::ffff:10.9.8.7"},
	// several answers, one of them not public
	"mixed.hidden.example":          {"93.184.216.34", "10.0.0.5"},
	"mixed-first.hidden.example":    {"192.168.0.1", "93.184.216.34"},
	"mixed-v6.hidden.example":       {"2606:2800:220:1:248:1893:25c8:1946", "fe80::1"},
	"mixed-many.hidden.example":     {"8.8.8.8", "93.184.216.35", "1.1.1.1", "100.64.12.34"},
	"mixed-metadata.hidden.example": {"2001:4860:4860::8888", "169.254.169.254"},
	// public
	"multi.hidden.example":     {"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946", "8.8.8.8"},
	"v6-public.hidden.example": {"2001:4860:4860::8888"},
	"edges.hidden.example":     {"172.15.255.254", "172.32.0.1", "100.63.255.254", "100.128.0.1", "11.0.0.1", "192.169.0.1", "169.255.0.1"},
}

// hiddenSSRFRefused are URLs that must be refused with 422 invalid_url.
var hiddenSSRFRefused = []string{
	// scheme, host, user info
	"http://hooks.acme.example/tally",
	"ftp://hooks.acme.example/tally",
	"hooks.acme.example/tally",
	"",
	"https:///tally",
	"https://:8443/tally",
	"https://user:secret@hooks.acme.example/tally",
	"https://user@hooks.acme.example/tally",
	"https://:secret@hooks.acme.example/tally",
	"https://hooks.acme.example@10.0.0.1/tally",
	// the host must resolve, as before
	"https://does-not-resolve.hidden.example/tally",
	"https://2130706433/tally",
	"https://0x7f000001/tally",
	// IPv4 literals
	"https://127.0.0.1/tally",
	"https://127.0.0.1:8443/tally",
	"https://127.1.2.3/",
	"https://10.0.0.1/",
	"https://10.255.255.255/",
	"https://172.16.0.1/",
	"https://172.31.255.254/",
	"https://192.168.0.1/",
	"https://192.168.255.254:8443/",
	"https://169.254.169.254/latest/meta-data/",
	"https://169.254.0.1/",
	"https://100.64.0.1/",
	"https://100.127.255.254/",
	"https://0.0.0.0/",
	"https://0.255.255.255/",
	"https://224.0.0.1/",
	"https://239.255.255.255/",
	"https://255.255.255.255/",
	// IPv6 literals
	"https://[::1]/",
	"https://[::1]:8443/tally",
	"https://[::]/",
	"https://[fc00::1]/",
	"https://[fdff:ffff::1]/",
	"https://[fe80::1]/",
	"https://[febf:ffff::1]/",
	"https://[fe80::1%25eth0]/",
	"https://[ff02::1]/",
	"https://[ff0e::1]/",
	// IPv4 addresses written as IPv6
	"https://[::ffff:127.0.0.1]/",
	"https://[::ffff:7f00:1]/",
	"https://[::ffff:10.0.0.1]/",
	"https://[::ffff:169.254.169.254]/",
	"https://[::ffff:100.64.0.1]/",
	"https://[::ffff:192.168.1.1]:8443/",
	"https://[::ffff:0.0.0.0]/",
}

// hiddenSSRFAccepted are URLs that must be accepted (201).
var hiddenSSRFAccepted = []string{
	"https://hooks.acme.example/tally",
	"https://hooks.acme.example:8443/tally",
	"https://93.184.216.34/in",
	"https://93.184.216.34:8443/in",
	"https://[2606:2800:220:1:248:1893:25c8:1946]/in",
	"https://[2606:2800:220:1:248:1893:25c8:1946]:8443/in",
	"https://[::ffff:93.184.216.34]/in",
	"https://multi.hidden.example/hook",
	"https://v6-public.hidden.example/hook",
	"https://edges.hidden.example/hook",
	"https://172.15.255.254/", "https://172.32.0.1/", "https://100.63.255.254/", "https://100.128.0.1/", "https://9.255.255.254/",
	"https://11.0.0.1/", "https://192.167.255.254/", "https://192.169.0.1/", "https://169.253.255.254/", "https://169.255.0.1/",
	"https://126.255.255.254/", "https://1.0.0.1/", "https://223.255.254.1/",
}

func hiddenSSRFEnv(t *testing.T) *apitest.Env {
	t.Helper()
	e := apitest.New(t)
	for host, ips := range hiddenSSRFHosts {
		e.DNS.Set(host, ips...)
	}
	return e
}

func hiddenSSRFIsInvalidURL(r *apitest.Response) bool {
	var body struct {
		Error string `json:"error"`
	}
	return r.Code == http.StatusUnprocessableEntity && json.Unmarshal(r.Body, &body) == nil && body.Error == "invalid_url"
}

type hiddenSSRFEndpoint struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	Events         []string `json:"events"`
	Enabled        bool     `json:"enabled"`
	DisabledReason string   `json:"disabled_reason"`
}

func (ep hiddenSSRFEndpoint) String() string {
	return fmt.Sprintf("url %s, events %v, enabled %v, disabled_reason %q", ep.URL, ep.Events, ep.Enabled, ep.DisabledReason)
}

func hiddenSSRFEndpoints(t *testing.T, e *apitest.Env) []hiddenSSRFEndpoint {
	t.Helper()
	var page struct {
		Items []hiddenSSRFEndpoint `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints", e.Acme.Key, nil, &page)
	return page.Items
}

func TestHiddenSSRFCreate(t *testing.T) {
	e := hiddenSSRFEnv(t)
	urls := append([]string(nil), hiddenSSRFRefused...)
	for host := range hiddenSSRFHosts {
		if !strings.HasPrefix(host, "multi.") && host != "v6-public.hidden.example" && host != "edges.hidden.example" {
			urls = append(urls, "https://"+host+"/hook", "https://"+host+":8443/hook")
		}
	}
	for _, u := range urls {
		r := e.Do("POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": u, "events": []string{"*"}})
		if !hiddenSSRFIsInvalidURL(r) {
			t.Errorf("POST /v1/webhook-endpoints with url %q%s: want 422 {\"error\":\"invalid_url\"}, got %s", u, hiddenSSRFResolves(u), r)
		}
	}
	if eps := hiddenSSRFEndpoints(t, e); len(eps) != 0 {
		t.Errorf("refused URLs created endpoints: %+v", eps)
	}
	for _, u := range hiddenSSRFAccepted {
		r := e.Do("POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": u, "events": []string{"*"}})
		if r.Code != http.StatusCreated {
			t.Errorf("POST /v1/webhook-endpoints with url %q%s: a public https URL, want 201, got %s", u, hiddenSSRFResolves(u), r)
		}
	}
	if eps := hiddenSSRFEndpoints(t, e); len(eps) != len(hiddenSSRFAccepted) {
		t.Errorf("%d endpoints after creating %d with public URLs", len(eps), len(hiddenSSRFAccepted))
	}
}

// hiddenSSRFResolves says what the URL's host resolves to in the test, for failure messages.
func hiddenSSRFResolves(raw string) string {
	if ips, ok := hiddenSSRFHosts[hiddenSSRFHost(raw)]; ok {
		return fmt.Sprintf(" (its host resolves to %v)", ips)
	}
	if hiddenSSRFHost(raw) == "hooks.acme.example" {
		return " (its host resolves to [93.184.216.34])"
	}
	return ""
}

func hiddenSSRFHost(raw string) string {
	rest, ok := strings.CutPrefix(raw, "https://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	return host
}

func TestHiddenSSRFChangeURL(t *testing.T) {
	e := hiddenSSRFEnv(t)
	var ep hiddenSSRFEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/tally", "events": []string{"*"}}, &ep)
	for _, u := range []string{
		"http://hooks.acme.example/tally",
		"https://user:secret@hooks.acme.example/tally",
		"https://127.0.0.1/",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/",
		"https://[::ffff:10.0.0.1]/",
		"https://[fe80::1]:8443/",
		"https://metadata.hidden.example/",
		"https://cgnat.hidden.example/",
		"https://mixed.hidden.example/",
		"https://mixed-v6.hidden.example/",
		"https://does-not-resolve.hidden.example/",
	} {
		r := e.Do("PATCH", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, map[string]any{"url": u})
		if !hiddenSSRFIsInvalidURL(r) {
			t.Errorf("PATCH /v1/webhook-endpoints/{id} with url %q: want 422 {\"error\":\"invalid_url\"}, got %s", u, r)
		}
	}
	var got hiddenSSRFEndpoint
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, &got)
	if got.URL != "https://hooks.acme.example/tally" {
		t.Fatalf("refused URL changes changed the endpoint's URL to %q", got.URL)
	}
	for _, u := range []string{"https://hooks.acme.example:8443/v2", "https://[2606:2800:220:1:248:1893:25c8:1946]/v3", "https://multi.hidden.example/v4"} {
		e.MustDo(http.StatusOK, "PATCH", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, map[string]any{"url": u}, &got)
		if got.URL != u {
			t.Fatalf("PATCH with the public URL %q: the endpoint's URL is %q", u, got.URL)
		}
	}
}

type hiddenSSRFDelivery struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func hiddenSSRFDeliveries(t *testing.T, e *apitest.Env, endpointID string) []hiddenSSRFDelivery {
	t.Helper()
	var page struct {
		Items []hiddenSSRFDelivery `json:"items"`
	}
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+endpointID+"/deliveries", e.Acme.Key, nil, &page)
	return page.Items
}

func hiddenSSRFRequestsTo(e *apitest.Env, host string) int {
	n := 0
	for _, r := range e.HTTP.Requests() {
		if strings.Contains(r.URL, "://"+host+"/") || strings.Contains(r.URL, "://"+host+":") {
			n++
		}
	}
	return n
}

// TestHiddenSSRFDeliveryRecheck: the DNS answer of a saved endpoint changes; before each attempt the host is resolved
// again with the same Resolver, and an endpoint that resolves to an address that is not public gets nothing.
func TestHiddenSSRFDeliveryRecheck(t *testing.T) {
	e := hiddenSSRFEnv(t)
	ctx := context.Background()
	const host = "rebind.hidden.example"
	e.DNS.Set(host, "93.184.216.34")
	var ep, stable hiddenSSRFEndpoint
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://" + host + "/hook", "events": []string{"*"}}, &ep)
	e.MustDo(http.StatusCreated, "POST", "/v1/webhook-endpoints", e.Acme.Key, map[string]any{"url": "https://hooks.acme.example/stable", "events": []string{"*"}}, &stable)
	var before hiddenSSRFEndpoint
	e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, &before)

	sent := 0 // deliveries to the stable endpoint so far
	for i, answers := range [][]string{
		{"10.0.0.7"},
		{"127.0.0.1"},
		{"169.254.169.254"},
		{"100.100.100.200"},
		{"::1"},
		{"fd00::1"},
		{"93.184.216.34", "192.168.1.1"},
		{"2606:2800:220:1:248:1893:25c8:1946", "fe80::1"},
	} {
		e.DNS.Set(host, answers...)
		e.CreateCustomer(e.Acme, "Customer", "customer"+strings.Repeat("x", i)+"@example.com") // an event for both endpoints
		if _, err := e.Webhooks.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
		sent++
		if n := hiddenSSRFRequestsTo(e, host); n != 0 {
			t.Fatalf("after %s came to resolve to %v, the webhook was sent to it anyway (%d requests): %+v", host, answers, n, e.HTTP.Requests())
		}
		if n := hiddenSSRFRequestsTo(e, "hooks.acme.example"); n != sent {
			t.Fatalf("the endpoint whose address stayed public got %d deliveries, want %d", n, sent)
		}
		ds := hiddenSSRFDeliveries(t, e, ep.ID)
		if len(ds) != i+1 {
			t.Fatalf("after %d events the endpoint has %d deliveries: %+v", i+1, len(ds), ds)
		}
		last := ds[len(ds)-1]
		if last.Status == "succeeded" || !strings.Contains(last.Error, "not public") {
			t.Fatalf("the delivery attempt to %s (now resolving to %v): want it failed with an error that contains \"not public\", got %+v", host, answers, last)
		}
		var after hiddenSSRFEndpoint
		e.MustDo(http.StatusOK, "GET", "/v1/webhook-endpoints/"+ep.ID, e.Acme.Key, nil, &after)
		if after.String() != before.String() {
			t.Fatalf("the endpoint must be left as it is:\nbefore: %s\nafter:  %s", before, after)
		}
	}

	// once the host resolves to public addresses again, deliveries go out
	e.DNS.Set(host, "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946")
	e.CreateCustomer(e.Acme, "Customer", "back@example.com")
	if _, err := e.Webhooks.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := hiddenSSRFRequestsTo(e, host); n != 1 {
		t.Fatalf("after %s resolves to public addresses again: %d requests to it, want 1: %+v", host, n, e.HTTP.Requests())
	}
	ds := hiddenSSRFDeliveries(t, e, ep.ID)
	if last := ds[len(ds)-1]; last.Status != "succeeded" {
		t.Fatalf("the delivery after the host resolves to public addresses again = %+v, want succeeded", last)
	}
}

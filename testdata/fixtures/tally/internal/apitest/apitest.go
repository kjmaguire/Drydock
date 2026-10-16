// Package apitest builds a whole Tally service for tests: the store, a fake clock, the fake payment gateway, an email
// outbox, fake webhook receivers and DNS, the services and the API server, with two organizations to start from.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

// Start is the fake clock's time when an Env is created.
var Start = time.Date(2026, time.January, 15, 10, 0, 0, 0, time.UTC)

// Env is a running service and the fakes around it.
type Env struct {
	T        testing.TB
	Store    *store.Store
	Clock    *clock.Fake
	Gateway  *payments.Fake
	Mail     *mail.Outbox
	HTTP     *FakeHTTP // the organizations' webhook receivers
	DNS      *FakeDNS
	Webhooks *webhooks.Service
	Payments *payments.Service
	Subs     *subscriptions.Service
	Server   *api.Server

	// Acme (team plan, America/New_York) and Globex (business plan, Europe/Berlin) each have an owner with an API key.
	Acme, Globex Org
}

// Org is an organization with its owner and the owner's API key.
type Org struct {
	store.Org
	Owner store.User
	Key   string
}

// New builds a service for a test.
func New(t testing.TB) *Env {
	t.Helper()
	e := &Env{T: t, Store: store.New(), Clock: clock.NewFake(Start), Gateway: &payments.Fake{}, Mail: &mail.Outbox{}, HTTP: &FakeHTTP{}, DNS: &FakeDNS{}}
	e.DNS.Set("hooks.acme.example", "93.184.216.34")
	e.DNS.Set("hooks.globex.example", "93.184.216.35")
	e.Webhooks = webhooks.New(webhooks.Options{Store: e.Store, Clock: e.Clock, Doer: e.HTTP, Resolver: e.DNS})
	e.Payments = &payments.Service{Store: e.Store, Gateway: e.Gateway, Clock: e.Clock, Publisher: e.Webhooks}
	e.Subs = &subscriptions.Service{Store: e.Store, Payments: e.Payments, Clock: e.Clock, Publisher: e.Webhooks}
	e.Server = api.New(e.Store, api.Options{Clock: e.Clock, Payments: e.Payments, Subscriptions: e.Subs, Webhooks: e.Webhooks, Mail: e.Mail})
	e.Acme = e.NewOrg("Acme", store.OrgTeam, "America/New_York")
	e.Globex = e.NewOrg("Globex", store.OrgBusiness, "Europe/Berlin")
	return e
}

// NewOrg adds an organization with an owner (owner@<name>.example) and the owner's API key.
func (e *Env) NewOrg(name string, plan store.OrgPlan, timeZone string) Org {
	e.T.Helper()
	now := e.Clock.Now()
	o, err := e.Store.CreateOrg(name, plan, timeZone, 0, now)
	if err != nil {
		e.T.Fatalf("create org %s: %v", name, err)
	}
	u, err := e.Store.CreateUser("owner@"+strings.ToLower(name)+".example", name+" Owner", now)
	if err != nil {
		e.T.Fatalf("create owner of %s: %v", name, err)
	}
	if _, err := e.Store.AddMember(o.ID, u.ID, store.RoleOwner, now); err != nil {
		e.T.Fatal(err)
	}
	k, err := e.Store.CreateAPIKey(o.ID, u.ID, "Owner key", now)
	if err != nil {
		e.T.Fatal(err)
	}
	return Org{Org: o, Owner: u, Key: k.Token}
}

// AddMember adds a new user with this email address to org with a role, and returns them with an API key of theirs.
func (e *Env) AddMember(org Org, email string, role store.Role) (store.User, string) {
	e.T.Helper()
	now := e.Clock.Now()
	u, ok := e.Store.UserByEmail(email)
	if !ok {
		var err error
		if u, err = e.Store.CreateUser(email, strings.Split(email, "@")[0], now); err != nil {
			e.T.Fatal(err)
		}
	}
	if _, err := e.Store.AddMember(org.ID, u.ID, role, now); err != nil {
		e.T.Fatal(err)
	}
	k, err := e.Store.CreateAPIKey(org.ID, u.ID, "Key of "+email, now)
	if err != nil {
		e.T.Fatal(err)
	}
	return u, k.Token
}

// Response is the server's answer.
type Response struct {
	Code   int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into v, failing the test when it cannot.
func (r *Response) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("response %d is not the JSON expected: %v\n%s", r.Code, err, r.Body)
	}
}

// Map decodes the body as a JSON object.
func (r *Response) Map(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	r.JSON(t, &m)
	return m
}

// String is the status and body, for failure messages.
func (r *Response) String() string { return fmt.Sprintf("%d %s", r.Code, bytes.TrimSpace(r.Body)) }

// Do sends a request to the server with key as its API key ("" for none). A body that is a string or []byte is sent as it
// is; any other non-nil body is sent as JSON.
func (e *Env) Do(method, path, key string, body any) *Response {
	e.T.Helper()
	return e.DoWith(method, path, key, body, nil)
}

// DoWith is Do with extra request headers.
func (e *Env) DoWith(method, path, key string, body any, header http.Header) *Response {
	e.T.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.T.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	e.Server.ServeHTTP(rec, req)
	return &Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
}

// MustDo is Do that fails the test unless the answer has the status want, and decodes the body into out (when not nil).
func (e *Env) MustDo(want int, method, path, key string, body, out any) *Response {
	e.T.Helper()
	r := e.Do(method, path, key, body)
	if r.Code != want {
		e.T.Fatalf("%s %s: want %d, got %s", method, path, want, r)
	}
	if out != nil {
		r.JSON(e.T, out)
	}
	return r
}

// CreateCustomer creates a customer of org through the API.
func (e *Env) CreateCustomer(org Org, name, email string) store.Customer {
	e.T.Helper()
	var c store.Customer
	e.MustDo(http.StatusCreated, "POST", "/v1/customers", org.Key, map[string]string{"name": name, "email": email}, &c)
	return c
}

// CreatePlan creates a monthly licensed plan of org through the API, priced in major units ("12.50").
func (e *Env) CreatePlan(org Org, name, currency, price string) store.Plan {
	e.T.Helper()
	var p store.Plan
	e.MustDo(http.StatusCreated, "POST", "/v1/plans", org.Key, map[string]any{"name": name, "currency": currency, "price": price, "interval": "month"}, &p)
	return p
}

// ---- fakes

// FakeHTTP plays the organizations' webhook receivers: it records every request and answers 200, or what Respond returns
// when it is set (a status code, or an error for a request that never got an answer).
type FakeHTTP struct {
	// Respond, when set, answers each request. Set it before requests are made, or between them.
	Respond func(r Request) (int, error)

	mu       sync.Mutex
	requests []Request
}

// Request is a request a FakeHTTP received.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Do implements webhooks.Doer.
func (f *FakeHTTP) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	r := Request{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone(), Body: body}
	f.mu.Lock()
	f.requests = append(f.requests, r)
	respond := f.Respond
	f.mu.Unlock()
	status := http.StatusOK
	if respond != nil {
		var err error
		if status, err = respond(r); err != nil {
			return nil, err
		}
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

// Requests returns the requests received so far, oldest first.
func (f *FakeHTTP) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// FakeDNS answers host lookups from the addresses Set gave it. Other hosts do not resolve.
type FakeDNS struct {
	mu    sync.Mutex
	hosts map[string][]string
}

// Set makes host resolve to ips (none: the host does not resolve).
func (d *FakeDNS) Set(host string, ips ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hosts == nil {
		d.hosts = map[string][]string{}
	}
	d.hosts[strings.ToLower(host)] = ips
}

// LookupIPAddr implements webhooks.Resolver.
func (d *FakeDNS) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	d.mu.Lock()
	ips := d.hosts[strings.ToLower(host)]
	d.mu.Unlock()
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("FakeDNS: %q is not an IP address", s)
		}
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

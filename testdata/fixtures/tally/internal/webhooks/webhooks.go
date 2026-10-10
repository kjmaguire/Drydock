// Package webhooks sends an organization's events to its webhook endpoints.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"example.com/tally/internal/clock"
	"example.com/tally/internal/store"
)

// Doer sends HTTP requests (an *http.Client).
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Resolver looks up host names (a *net.Resolver).
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Options configure a Service.
type Options struct {
	Store *store.Store
	Clock clock.Clock
	// Doer sends the deliveries; nil means an HTTP client with a 10 second timeout that does not follow redirects.
	Doer Doer
	// Resolver checks endpoint hosts; nil means net.DefaultResolver.
	Resolver Resolver
}

// Service manages endpoints' URLs and sends deliveries.
type Service struct {
	store    *store.Store
	clock    clock.Clock
	doer     Doer
	resolver Resolver
}

// New returns a Service.
func New(opt Options) *Service {
	s := &Service{store: opt.Store, clock: opt.Clock, doer: opt.Doer, resolver: opt.Resolver}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	if s.doer == nil {
		s.doer = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if s.resolver == nil {
		s.resolver = net.DefaultResolver
	}
	return s
}

// EventTypes are the events Tally sends. An endpoint subscribes to some of them, or to "*" for all.
var EventTypes = []string{
	"customer.created", "customer.updated", "customer.deleted",
	"subscription.created", "subscription.updated", "subscription.canceled",
	"invoice.created", "invoice.paid", "invoice.payment_failed", "invoice.voided",
}

// Errors.
var (
	ErrInvalidURL    = errors.New("invalid webhook URL")
	ErrInvalidEvents = errors.New("invalid event types")
)

// CheckURL checks a webhook URL before it is saved: an absolute http or https URL whose host resolves.
func (s *Service) CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: use an absolute http or https URL", ErrInvalidURL)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return nil
	}
	addrs, err := s.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: the host %q does not resolve", ErrInvalidURL, host)
	}
	return nil
}

// CheckEvents checks an endpoint's event types.
func CheckEvents(types []string) error {
	if len(types) == 0 {
		return fmt.Errorf("%w: name at least one event type, or \"*\"", ErrInvalidEvents)
	}
	for _, t := range types {
		if t != "*" && !slices.Contains(EventTypes, t) {
			return fmt.Errorf("%w: unknown event type %q", ErrInvalidEvents, t)
		}
	}
	return nil
}

// NewSecret returns a new signing secret for an endpoint.
func NewSecret() string { return store.NewToken("whsec_") }

// Sign returns the signature of a delivery's body: the hex HMAC-SHA256 of the body, keyed with the endpoint's secret.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Publish records an event and queues a delivery of it to every enabled endpoint of the organization that subscribes to its
// type. The deliveries are sent by RunDue.
func (s *Service) Publish(ctx context.Context, orgID, eventType string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	e, err := s.store.AddEvent(orgID, eventType, raw, now)
	if err != nil {
		return err
	}
	for _, ep := range s.store.WebhookEndpoints(orgID) {
		if !ep.Enabled || !(slices.Contains(ep.Events, "*") || slices.Contains(ep.Events, eventType)) {
			continue
		}
		next := now
		if _, err := s.store.CreateDelivery(store.Delivery{OrgID: orgID, EndpointID: ep.ID, EventID: e.ID, NextAttemptAt: &next}, now); err != nil {
			return err
		}
	}
	return nil
}

// RunDue attempts every delivery that is due and returns how many it attempted. Each delivery is attempted once: an answer
// with a 2xx status marks it succeeded, anything else failed.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	n := 0
	for _, d := range s.store.DueDeliveries(s.clock.Now()) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		s.deliver(ctx, d)
		n++
	}
	return n, nil
}

// Body is what a delivery POSTs.
type Body struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

func (s *Service) deliver(ctx context.Context, d store.Delivery) {
	status, errText := 0, ""
	ep, ok := s.store.WebhookEndpoint(d.OrgID, d.EndpointID)
	e, eok := s.store.EventByID(d.EventID)
	switch {
	case !ok || !ep.Enabled:
		errText = "the endpoint is disabled or was deleted"
	case !eok:
		errText = "the event no longer exists"
	default:
		status, errText = s.send(ctx, ep, e)
	}
	now := s.clock.Now()
	_, _ = s.store.UpdateDelivery(d.ID, func(d *store.Delivery) error {
		d.Attempts++
		d.LastAttemptAt = &now
		d.NextAttemptAt = nil
		d.ResponseStatus = status
		d.Error = errText
		if errText == "" {
			d.Status = store.DeliverySucceeded
		} else {
			d.Status = store.DeliveryFailed
		}
		return nil
	})
}

// send POSTs the event to the endpoint and returns the response status and, when it failed, why.
func (s *Service) send(ctx context.Context, ep store.WebhookEndpoint, e store.Event) (int, string) {
	body, err := json.Marshal(Body{ID: e.ID, Type: e.Type, CreatedAt: e.CreatedAt, Data: e.Data})
	if err != nil {
		return 0, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Tally-Webhooks/1.0")
	req.Header.Set("Tally-Event-Id", e.ID)
	req.Header.Set("Tally-Event-Type", e.Type)
	req.Header.Set("Tally-Signature", Sign(ep.Secret, body))
	resp, err := s.doer.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Sprintf("the endpoint answered %d", resp.StatusCode)
	}
	return resp.StatusCode, ""
}

package store

import (
	"encoding/json"
	"sort"
	"time"
)

// CreateWebhookEndpoint stores an endpoint. The caller checks its URL and sets its secret.
func (s *Store) CreateWebhookEndpoint(ep WebhookEndpoint, now time.Time) (WebhookEndpoint, error) {
	if len(ep.Events) == 0 {
		return WebhookEndpoint{}, invalid("an endpoint needs at least one event type")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[ep.OrgID]; !ok {
		return WebhookEndpoint{}, ErrNotFound
	}
	ep.ID = s.newID("we")
	ep.CreatedAt = now.UTC()
	ep.Events = append([]string(nil), ep.Events...)
	s.endpoints[ep.ID] = ep
	return copyEndpoint(ep), nil
}

// WebhookEndpoint returns one of an organization's endpoints.
func (s *Store) WebhookEndpoint(orgID, id string) (WebhookEndpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ep, ok := s.endpoints[id]
	if !ok || ep.OrgID != orgID {
		return WebhookEndpoint{}, false
	}
	return copyEndpoint(ep), true
}

// WebhookEndpoints returns an organization's endpoints, oldest first.
func (s *Store) WebhookEndpoints(orgID string) []WebhookEndpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []WebhookEndpoint{}
	for _, ep := range s.endpoints {
		if ep.OrgID == orgID {
			out = append(out, copyEndpoint(ep))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpdateWebhookEndpoint changes an endpoint with f, which may refuse with an error.
func (s *Store) UpdateWebhookEndpoint(orgID, id string, f func(*WebhookEndpoint) error) (WebhookEndpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.endpoints[id]
	if !ok || old.OrgID != orgID {
		return WebhookEndpoint{}, ErrNotFound
	}
	ep := copyEndpoint(old)
	if err := f(&ep); err != nil {
		return WebhookEndpoint{}, err
	}
	ep.ID, ep.OrgID, ep.CreatedAt = old.ID, old.OrgID, old.CreatedAt
	if len(ep.Events) == 0 {
		return WebhookEndpoint{}, invalid("an endpoint needs at least one event type")
	}
	s.endpoints[id] = copyEndpoint(ep)
	return copyEndpoint(ep), nil
}

// DeleteWebhookEndpoint removes an endpoint and its pending deliveries.
func (s *Store) DeleteWebhookEndpoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.endpoints[id]; !ok {
		return ErrNotFound
	}
	delete(s.endpoints, id)
	for did, d := range s.delivs {
		if d.EndpointID == id && d.Status == DeliveryPending {
			delete(s.delivs, did)
		}
	}
	return nil
}

func copyEndpoint(ep WebhookEndpoint) WebhookEndpoint {
	ep.Events = append([]string(nil), ep.Events...)
	return ep
}

// AddEvent records an event.
func (s *Store) AddEvent(orgID, typ string, data json.RawMessage, now time.Time) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[orgID]; !ok {
		return Event{}, ErrNotFound
	}
	e := Event{ID: s.newID("evt"), OrgID: orgID, Type: typ, Data: append(json.RawMessage(nil), data...), CreatedAt: now.UTC()}
	s.events[e.ID] = e
	return e, nil
}

// Event returns one of an organization's events.
func (s *Store) Event(orgID, id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[id]
	if !ok || e.OrgID != orgID {
		return Event{}, false
	}
	return e, true
}

// EventByID returns an event of any organization (the webhook sender).
func (s *Store) EventByID(id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[id]
	return e, ok
}

// Events returns an organization's events, oldest first, only those of one type when typ is set.
func (s *Store) Events(orgID, typ string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for _, e := range s.events {
		if e.OrgID == orgID && (typ == "" || e.Type == typ) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CreateDelivery queues an event for an endpoint.
func (s *Store) CreateDelivery(d Delivery, now time.Time) (Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ep, ok := s.endpoints[d.EndpointID]
	if !ok || ep.OrgID != d.OrgID {
		return Delivery{}, ErrNotFound
	}
	if e, ok := s.events[d.EventID]; !ok || e.OrgID != d.OrgID {
		return Delivery{}, ErrNotFound
	}
	d.ID = s.newID("del")
	d.CreatedAt = now.UTC()
	if d.Status == "" {
		d.Status = DeliveryPending
	}
	s.delivs[d.ID] = copyDelivery(d)
	return copyDelivery(d), nil
}

// Delivery returns a delivery of any organization (the webhook sender).
func (s *Store) Delivery(id string) (Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delivs[id]
	return copyDelivery(d), ok
}

// Deliveries returns an organization's deliveries to one endpoint, oldest first.
func (s *Store) Deliveries(orgID, endpointID string) []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Delivery{}
	for _, d := range s.delivs {
		if d.OrgID == orgID && d.EndpointID == endpointID {
			out = append(out, copyDelivery(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DueDeliveries returns the pending deliveries of every organization whose next attempt is due by now, oldest first.
func (s *Store) DueDeliveries(now time.Time) []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Delivery{}
	for _, d := range s.delivs {
		if d.Status == DeliveryPending && d.NextAttemptAt != nil && !d.NextAttemptAt.After(now) {
			out = append(out, copyDelivery(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpdateDelivery changes a delivery with f.
func (s *Store) UpdateDelivery(id string, f func(*Delivery) error) (Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.delivs[id]
	if !ok {
		return Delivery{}, ErrNotFound
	}
	d := copyDelivery(old)
	if err := f(&d); err != nil {
		return Delivery{}, err
	}
	d.ID, d.OrgID, d.EndpointID, d.EventID, d.CreatedAt = old.ID, old.OrgID, old.EndpointID, old.EventID, old.CreatedAt
	s.delivs[id] = copyDelivery(d)
	return copyDelivery(d), nil
}

func copyDelivery(d Delivery) Delivery {
	for _, p := range []**time.Time{&d.NextAttemptAt, &d.LastAttemptAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return d
}

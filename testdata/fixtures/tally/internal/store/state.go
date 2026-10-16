package store

import (
	"fmt"
	"sort"
)

// State is everything a store holds, for snapshots (package snapshot). Slices are sorted by ID.
type State struct {
	Seq              int               `json:"seq"`
	Orgs             []Org             `json:"orgs"`
	Users            []User            `json:"users"`
	Members          []Member          `json:"members"`
	APIKeys          []APIKey          `json:"api_keys"`
	Invites          []Invite          `json:"invites"`
	Customers        []Customer        `json:"customers"`
	Plans            []Plan            `json:"plans"`
	Subscriptions    []Subscription    `json:"subscriptions"`
	Usage            []UsageRecord     `json:"usage"`
	Invoices         []Invoice         `json:"invoices"`
	Payments         []Payment         `json:"payments"`
	WebhookEndpoints []WebhookEndpoint `json:"webhook_endpoints"`
	Events           []Event           `json:"events"`
	Deliveries       []Delivery        `json:"deliveries"`
}

// Export returns a copy of everything in the store.
func (s *Store) Export() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Seq: s.seq}
	for _, o := range s.orgs {
		st.Orgs = append(st.Orgs, o)
	}
	for _, u := range s.users {
		st.Users = append(st.Users, u)
	}
	for _, m := range s.members {
		st.Members = append(st.Members, m)
	}
	for _, k := range s.apiKeys {
		st.APIKeys = append(st.APIKeys, k)
	}
	for _, i := range s.invites {
		st.Invites = append(st.Invites, i)
	}
	for _, c := range s.customers {
		st.Customers = append(st.Customers, copyCustomer(c))
	}
	for _, p := range s.plans {
		st.Plans = append(st.Plans, p)
	}
	for _, sub := range s.subs {
		st.Subscriptions = append(st.Subscriptions, copySub(sub))
	}
	st.Usage = append(st.Usage, s.usage...)
	for _, inv := range s.invoices {
		st.Invoices = append(st.Invoices, copyInvoice(inv))
	}
	for _, p := range s.payments {
		st.Payments = append(st.Payments, p)
	}
	for _, ep := range s.endpoints {
		st.WebhookEndpoints = append(st.WebhookEndpoints, copyEndpoint(ep))
	}
	for _, e := range s.events {
		st.Events = append(st.Events, e)
	}
	for _, d := range s.delivs {
		st.Deliveries = append(st.Deliveries, copyDelivery(d))
	}
	sort.Slice(st.Orgs, func(i, j int) bool { return st.Orgs[i].ID < st.Orgs[j].ID })
	sort.Slice(st.Users, func(i, j int) bool { return st.Users[i].ID < st.Users[j].ID })
	sort.Slice(st.Members, func(i, j int) bool {
		if st.Members[i].OrgID != st.Members[j].OrgID {
			return st.Members[i].OrgID < st.Members[j].OrgID
		}
		return st.Members[i].UserID < st.Members[j].UserID
	})
	sort.Slice(st.APIKeys, func(i, j int) bool { return st.APIKeys[i].ID < st.APIKeys[j].ID })
	sort.Slice(st.Invites, func(i, j int) bool { return st.Invites[i].ID < st.Invites[j].ID })
	sort.Slice(st.Customers, func(i, j int) bool { return st.Customers[i].ID < st.Customers[j].ID })
	sort.Slice(st.Plans, func(i, j int) bool { return st.Plans[i].ID < st.Plans[j].ID })
	sort.Slice(st.Subscriptions, func(i, j int) bool { return st.Subscriptions[i].ID < st.Subscriptions[j].ID })
	sort.Slice(st.Usage, func(i, j int) bool { return st.Usage[i].ID < st.Usage[j].ID })
	sort.Slice(st.Invoices, func(i, j int) bool { return st.Invoices[i].ID < st.Invoices[j].ID })
	sort.Slice(st.Payments, func(i, j int) bool { return st.Payments[i].ID < st.Payments[j].ID })
	sort.Slice(st.WebhookEndpoints, func(i, j int) bool { return st.WebhookEndpoints[i].ID < st.WebhookEndpoints[j].ID })
	sort.Slice(st.Events, func(i, j int) bool { return st.Events[i].ID < st.Events[j].ID })
	sort.Slice(st.Deliveries, func(i, j int) bool { return st.Deliveries[i].ID < st.Deliveries[j].ID })
	return st
}

// FromState builds a store holding st.
func FromState(st State) (*Store, error) {
	s := New()
	s.seq = st.Seq
	dup := func(kind, id string, seen map[string]bool) error {
		if id == "" || seen[id] {
			return fmt.Errorf("%w: %s %q is missing or repeated", ErrInvalid, kind, id)
		}
		seen[id] = true
		return nil
	}
	seen := map[string]bool{}
	for _, o := range st.Orgs {
		if err := dup("organization", o.ID, seen); err != nil {
			return nil, err
		}
		s.orgs[o.ID] = o
	}
	for _, u := range st.Users {
		if err := dup("user", u.ID, seen); err != nil {
			return nil, err
		}
		s.users[u.ID] = u
		s.usersByEmail[normEmail(u.Email)] = u.ID
	}
	for _, m := range st.Members {
		if _, ok := s.orgs[m.OrgID]; !ok {
			return nil, fmt.Errorf("%w: member of unknown organization %q", ErrInvalid, m.OrgID)
		}
		if _, ok := s.users[m.UserID]; !ok {
			return nil, fmt.Errorf("%w: member is unknown user %q", ErrInvalid, m.UserID)
		}
		s.members[memberKey{m.OrgID, m.UserID}] = m
	}
	for _, k := range st.APIKeys {
		if err := dup("API key", k.ID, seen); err != nil {
			return nil, err
		}
		s.apiKeys[k.ID] = k
		if k.Token != "" {
			s.keysByToken[k.Token] = k.ID
		}
	}
	for _, i := range st.Invites {
		if err := dup("invite", i.ID, seen); err != nil {
			return nil, err
		}
		s.invites[i.ID] = i
	}
	for _, c := range st.Customers {
		if err := dup("customer", c.ID, seen); err != nil {
			return nil, err
		}
		s.customers[c.ID] = copyCustomer(c)
		s.customersByEmail[normEmail(c.Email)] = c.ID
	}
	for _, p := range st.Plans {
		if err := dup("plan", p.ID, seen); err != nil {
			return nil, err
		}
		s.plans[p.ID] = p
	}
	for _, sub := range st.Subscriptions {
		if err := dup("subscription", sub.ID, seen); err != nil {
			return nil, err
		}
		s.subs[sub.ID] = copySub(sub)
	}
	s.usage = append(s.usage, st.Usage...)
	for _, inv := range st.Invoices {
		if err := dup("invoice", inv.ID, seen); err != nil {
			return nil, err
		}
		s.invoices[inv.ID] = copyInvoice(inv)
	}
	for _, p := range st.Payments {
		if err := dup("payment", p.ID, seen); err != nil {
			return nil, err
		}
		s.payments[p.ID] = p
	}
	for _, ep := range st.WebhookEndpoints {
		if err := dup("webhook endpoint", ep.ID, seen); err != nil {
			return nil, err
		}
		s.endpoints[ep.ID] = copyEndpoint(ep)
	}
	for _, e := range st.Events {
		if err := dup("event", e.ID, seen); err != nil {
			return nil, err
		}
		s.events[e.ID] = e
	}
	for _, d := range st.Deliveries {
		if err := dup("delivery", d.ID, seen); err != nil {
			return nil, err
		}
		s.delivs[d.ID] = copyDelivery(d)
	}
	return s, nil
}

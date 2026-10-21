package store

import (
	"sort"
	"strings"
	"time"
)

// CreateCustomer adds a customer to an organization. A customer's email address is unique within the organization,
// ignoring case.
func (s *Store) CreateCustomer(orgID, name, email string, now time.Time) (Customer, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || !validEmail(email) {
		return Customer{}, invalid("a customer needs a name and a valid email address")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[orgID]; !ok {
		return Customer{}, ErrNotFound
	}
	key := normEmail(email)
	if id, ok := s.customersByEmail[key]; ok && s.customers[id].OrgID == orgID {
		return Customer{}, ErrEmailTaken
	}
	c := Customer{ID: s.newID("cus"), OrgID: orgID, Name: name, Email: email, CreatedAt: now.UTC()}
	s.customers[c.ID] = c
	s.customersByEmail[key] = c.ID
	return c, nil
}

// Customer returns one of an organization's customers.
func (s *Store) Customer(orgID, id string) (Customer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.customers[id]
	if !ok || c.OrgID != orgID {
		return Customer{}, false
	}
	return copyCustomer(c), true
}

// CustomerByID returns a customer of any organization (background jobs and the admin tool).
func (s *Store) CustomerByID(id string) (Customer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.customers[id]
	return copyCustomer(c), ok
}

// Customers returns an organization's customers, oldest first.
func (s *Store) Customers(orgID string) []Customer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Customer{}
	for _, c := range s.customers {
		if c.OrgID == orgID {
			out = append(out, copyCustomer(c))
		}
	}
	sortCustomers(out)
	return out
}

// SearchCustomers returns an organization's customers whose name or email contains q, ignoring case, oldest first.
func (s *Store) SearchCustomers(orgID, q string) []Customer {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []Customer{}
	for _, c := range s.Customers(orgID) {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Email), q) {
			out = append(out, c)
		}
	}
	return out
}

// FindCustomerByEmail returns the organization's customer with this email address, ignoring case.
func (s *Store) FindCustomerByEmail(orgID, email string) (Customer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.customersByEmail[normEmail(email)]
	if !ok {
		return Customer{}, false
	}
	return copyCustomer(s.customers[id]), true
}

// UpdateCustomer changes a customer with f, which may refuse with an error. The ID and organization cannot change.
func (s *Store) UpdateCustomer(orgID, id string, f func(*Customer) error) (Customer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.customers[id]
	if !ok || old.OrgID != orgID {
		return Customer{}, ErrNotFound
	}
	c := copyCustomer(old)
	if err := f(&c); err != nil {
		return Customer{}, err
	}
	c.ID, c.OrgID, c.CreatedAt = old.ID, old.OrgID, old.CreatedAt
	c.Name, c.Email = strings.TrimSpace(c.Name), strings.TrimSpace(c.Email)
	if c.Name == "" || !validEmail(c.Email) {
		return Customer{}, invalid("a customer needs a name and a valid email address")
	}
	if c.DefaultPaymentMethod != "" && !strings.HasPrefix(c.DefaultPaymentMethod, "pm_") {
		return Customer{}, invalid("a payment method looks like pm_...")
	}
	if newKey := normEmail(c.Email); newKey != normEmail(old.Email) {
		if other, ok := s.customersByEmail[newKey]; ok && s.customers[other].OrgID == orgID {
			return Customer{}, ErrEmailTaken
		}
		delete(s.customersByEmail, normEmail(old.Email))
		s.customersByEmail[newKey] = c.ID
	}
	s.customers[id] = c
	return copyCustomer(c), nil
}

// DeleteCustomer removes a customer.
func (s *Store) DeleteCustomer(orgID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.customers[id]
	if !ok || c.OrgID != orgID {
		return ErrNotFound
	}
	delete(s.customers, id)
	if s.customersByEmail[normEmail(c.Email)] == id {
		delete(s.customersByEmail, normEmail(c.Email))
	}
	return nil
}

func copyCustomer(c Customer) Customer {
	if c.Metadata != nil {
		m := make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			m[k] = v
		}
		c.Metadata = m
	}
	return c
}

func sortCustomers(cs []Customer) {
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].CreatedAt.Equal(cs[j].CreatedAt) {
			return cs[i].CreatedAt.Before(cs[j].CreatedAt)
		}
		return cs[i].ID < cs[j].ID
	})
}

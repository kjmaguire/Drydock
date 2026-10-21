// Package store keeps the API's data in memory. It is safe for concurrent use.
package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plan is an account's or a customer's subscription plan.
type Plan string

const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
)

// Valid reports whether p is a known plan.
func (p Plan) Valid() bool { return p == PlanFree || p == PlanPro }

// Account is one tenant of the product.
type Account struct {
	ID   string
	Name string
	Plan Plan
	// TaxRateBP is the tax rate charged on this account's invoices, in basis points (1% = 100).
	TaxRateBP int
}

// Customer is one of an account's customers.
type Customer struct {
	ID        string    `json:"id"`
	AccountID string    `json:"-"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Plan      Plan      `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

// LineItem is one line of an invoice.
type LineItem struct {
	Description string `json:"description"`
	Quantity    int    `json:"quantity"`
	UnitCents   int64  `json:"unit_cents"`
}

// Invoice is a bill to a customer.
type Invoice struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"-"`
	CustomerID string     `json:"customer_id"`
	Lines      []LineItem `json:"lines"`
	// CouponPercent is a percentage discount (0 to 100).
	CouponPercent int `json:"coupon_percent"`
	// CreditCents is a fixed amount taken off the invoice.
	CreditCents int64     `json:"credit_cents"`
	TaxRateBP   int       `json:"tax_rate_bp"`
	CreatedAt   time.Time `json:"created_at"`
}

// Errors.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
)

// Store holds everything.
type Store struct {
	mu        sync.Mutex
	accounts  map[string]Account
	keys      map[string]string // API key -> account ID
	customers map[string]Customer
	invoices  map[string]Invoice
	seq       int
}

// New returns an empty store.
func New() *Store {
	return &Store{accounts: map[string]Account{}, keys: map[string]string{}, customers: map[string]Customer{}, invoices: map[string]Invoice{}}
}

// AddAccount adds or replaces an account.
func (s *Store) AddAccount(a Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[a.ID] = a
}

// AddKey gives an account an API key.
func (s *Store) AddKey(key, accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = accountID
}

// AccountForKey returns the account an API key belongs to.
func (s *Store) AccountForKey(key string) (Account, bool) {
	if key == "" {
		return Account{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.keys[key]
	if !ok {
		return Account{}, false
	}
	a, ok := s.accounts[id]
	return a, ok
}

func (s *Store) next(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%05d", prefix, s.seq)
}

// CreateCustomer adds a customer to an account.
func (s *Store) CreateCustomer(accountID, name, email string, plan Plan, now time.Time) (Customer, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || !strings.Contains(email, "@") {
		return Customer{}, fmt.Errorf("%w: a customer needs a name and an email address", ErrInvalid)
	}
	if plan == "" {
		plan = PlanFree
	}
	if !plan.Valid() {
		return Customer{}, fmt.Errorf("%w: unknown plan %q", ErrInvalid, plan)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accountID]; !ok {
		return Customer{}, ErrNotFound
	}
	c := Customer{ID: s.next("cus"), AccountID: accountID, Name: name, Email: email, Plan: plan, CreatedAt: now}
	s.customers[c.ID] = c
	return c, nil
}

// Customer returns one of an account's customers.
func (s *Store) Customer(accountID, id string) (Customer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.customers[id]
	if !ok || c.AccountID != accountID {
		return Customer{}, false
	}
	return c, true
}

// Customers returns an account's customers sorted by ID.
func (s *Store) Customers(accountID string) []Customer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Customer{}
	for _, c := range s.customers {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CreateInvoice stores an invoice for one of the account's customers, with the account's tax rate.
func (s *Store) CreateInvoice(inv Invoice, now time.Time) (Invoice, error) {
	if len(inv.Lines) == 0 {
		return Invoice{}, fmt.Errorf("%w: an invoice needs at least one line", ErrInvalid)
	}
	for _, l := range inv.Lines {
		if l.Quantity <= 0 || l.UnitCents < 0 {
			return Invoice{}, fmt.Errorf("%w: a line needs a positive quantity and a price that is not negative", ErrInvalid)
		}
	}
	if inv.CouponPercent < 0 || inv.CouponPercent > 100 || inv.CreditCents < 0 {
		return Invoice{}, fmt.Errorf("%w: the coupon is 0 to 100 percent and the credit is not negative", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[inv.AccountID]
	if !ok {
		return Invoice{}, ErrNotFound
	}
	if c, ok := s.customers[inv.CustomerID]; !ok || c.AccountID != inv.AccountID {
		return Invoice{}, ErrNotFound
	}
	inv.ID = s.next("inv")
	inv.TaxRateBP = a.TaxRateBP
	inv.CreatedAt = now
	inv.Lines = append([]LineItem(nil), inv.Lines...)
	s.invoices[inv.ID] = inv
	return inv, nil
}

// Invoice returns one of an account's invoices.
func (s *Store) Invoice(accountID, id string) (Invoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[id]
	if !ok || inv.AccountID != accountID {
		return Invoice{}, false
	}
	return inv, true
}

// Invoices returns an account's invoices, oldest first (ties by ID).
func (s *Store) Invoices(accountID string) []Invoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Invoice{}
	for _, inv := range s.invoices {
		if inv.AccountID == accountID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

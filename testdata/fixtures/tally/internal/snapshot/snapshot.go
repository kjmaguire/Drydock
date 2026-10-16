// Package snapshot saves the store to a JSON file and loads it back, so the server keeps its data across restarts. The
// file carries a version; files written by older versions of Tally are migrated when they are loaded.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

// Version is the version Save writes.
const Version = 2

// ErrUnsupportedVersion is returned for a file of a version Load cannot read.
var ErrUnsupportedVersion = errors.New("unsupported snapshot version")

type fileV2 struct {
	Version int         `json:"version"`
	SavedAt time.Time   `json:"saved_at"`
	State   store.State `json:"state"`
}

// Save writes everything in st to path.
func Save(st *store.Store, path string, now time.Time) error {
	b, err := json.MarshalIndent(fileV2{Version: Version, SavedAt: now.UTC(), State: st.Export()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Load reads a snapshot written by Save, or by an older version of Tally, and returns a store holding it.
func Load(path string) (*store.Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", path, err)
	}
	var st store.State
	switch head.Version {
	case 1:
		if st, err = migrateV1(b); err != nil {
			return nil, fmt.Errorf("snapshot %s: version 1: %w", path, err)
		}
	case 2:
		var f fileV2
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", path, err)
		}
		st = f.State
	default:
		return nil, fmt.Errorf("snapshot %s: %w %d", path, ErrUnsupportedVersion, head.Version)
	}
	return store.FromState(st)
}

// Version 1 (Tally 0.x) kept the state at the top level. Organizations had no plan or time zone, customers had a
// full_name, and plans had a price in US cents (price_cents) and were all licensed.
type fileV1 struct {
	Seq           int                  `json:"seq"`
	Orgs          []orgV1              `json:"orgs"`
	Users         []store.User         `json:"users"`
	Members       []store.Member       `json:"members"`
	APIKeys       []store.APIKey       `json:"api_keys"`
	Customers     []customerV1         `json:"customers"`
	Plans         []planV1             `json:"plans"`
	Subscriptions []store.Subscription `json:"subscriptions"`
	Invoices      []store.Invoice      `json:"invoices"`
	Payments      []store.Payment      `json:"payments"`
}

type orgV1 struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TaxRateBP int       `json:"tax_rate_bp"`
	CreatedAt time.Time `json:"created_at"`
}

type customerV1 struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	FullName  string    `json:"full_name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type planV1 struct {
	ID         string         `json:"id"`
	OrgID      string         `json:"org_id"`
	Name       string         `json:"name"`
	PriceCents int64          `json:"price_cents"`
	Interval   store.Interval `json:"interval"`
	TrialDays  int            `json:"trial_days"`
	CreatedAt  time.Time      `json:"created_at"`
}

func migrateV1(b []byte) (store.State, error) {
	var f fileV1
	if err := json.Unmarshal(b, &f); err != nil {
		return store.State{}, err
	}
	st := store.State{Seq: f.Seq, Users: f.Users, Members: f.Members, APIKeys: f.APIKeys, Subscriptions: f.Subscriptions, Invoices: f.Invoices, Payments: f.Payments}
	for _, o := range f.Orgs {
		st.Orgs = append(st.Orgs, store.Org{ID: o.ID, Name: o.Name, Plan: store.OrgFree, TimeZone: "UTC", TaxRateBP: o.TaxRateBP, CreatedAt: o.CreatedAt})
	}
	for _, c := range f.Customers {
		st.Customers = append(st.Customers, store.Customer{ID: c.ID, OrgID: c.OrgID, Name: c.FullName, Email: c.Email, CreatedAt: c.CreatedAt})
	}
	for _, p := range f.Plans {
		st.Plans = append(st.Plans, store.Plan{ID: p.ID, OrgID: p.OrgID, Name: p.Name, Currency: money.USD, UnitAmount: p.PriceCents, Interval: p.Interval,
			UsageType: store.Licensed, TrialDays: p.TrialDays, Active: true, CreatedAt: p.CreatedAt})
	}
	return st, nil
}

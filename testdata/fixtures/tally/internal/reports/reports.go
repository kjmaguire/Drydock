// Package reports computes the dashboard's reports.
package reports

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

// DayRevenue is what an organization took on one day in one currency.
type DayRevenue struct {
	Date     string         `json:"date"` // YYYY-MM-DD
	Currency money.Currency `json:"currency"`
	Amount   int64          `json:"amount"`
	Payments int            `json:"payments"`
}

// Errors.
var (
	ErrInvalidDate  = errors.New("invalid date")
	ErrInvalidRange = errors.New("invalid date range")
)

// MaxDays is the longest range a report covers.
const MaxDays = 366

// Revenue adds up the organization's succeeded payments per day and currency, from the day from to the day to (YYYY-MM-DD,
// both included). Days are UTC days. The result is in date order, currencies in alphabetical order within a day; days
// without payments are left out.
func Revenue(st *store.Store, orgID, from, to string) ([]DayRevenue, error) {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return nil, fmt.Errorf("%w: from %q", ErrInvalidDate, from)
	}
	last, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return nil, fmt.Errorf("%w: to %q", ErrInvalidDate, to)
	}
	if last.Before(start) {
		return nil, fmt.Errorf("%w: to is before from", ErrInvalidRange)
	}
	if last.Sub(start) >= MaxDays*24*time.Hour {
		return nil, fmt.Errorf("%w: at most %d days", ErrInvalidRange, MaxDays)
	}
	end := last.AddDate(0, 0, 1)
	type key struct {
		day string
		cur money.Currency
	}
	sums := map[key]*DayRevenue{}
	for _, p := range st.Payments(orgID, "") {
		if p.Status != store.PaymentSucceeded || p.CreatedAt.Before(start) || !p.CreatedAt.Before(end) {
			continue
		}
		k := key{p.CreatedAt.UTC().Format(time.DateOnly), p.Currency}
		d, ok := sums[k]
		if !ok {
			d = &DayRevenue{Date: k.day, Currency: k.cur}
			sums[k] = d
		}
		d.Amount += p.Amount
		d.Payments++
	}
	out := make([]DayRevenue, 0, len(sums))
	for _, d := range sums {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Currency < out[j].Currency
	})
	return out, nil
}

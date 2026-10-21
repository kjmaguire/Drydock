// Package billing is invoice arithmetic and billing periods. It holds no state.
package billing

import (
	"time"

	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

// Totals are the amounts shown on an invoice, in the invoice currency's minor unit.
type Totals struct {
	Subtotal int64 `json:"subtotal"`
	Discount int64 `json:"discount"`
	Tax      int64 `json:"tax"`
	Total    int64 `json:"total"`
}

// Compute returns an invoice's totals: the sum of its lines, the discount (DiscountPercent of the subtotal), and tax at the
// invoice's rate on the subtotal after the discount. The discount and the tax are each rounded half away from zero to a
// whole minor unit.
func Compute(inv store.Invoice) Totals {
	var subtotal int64
	for _, l := range inv.Lines {
		subtotal += l.Amount()
	}
	discount := money.Percent(subtotal, inv.DiscountPercent)
	taxable := subtotal - discount
	tax := money.BasisPoints(taxable, inv.TaxRateBP)
	return Totals{Subtotal: subtotal, Discount: discount, Tax: tax, Total: taxable + tax}
}

// AddInterval returns t moved forward by one billing interval: a month or a year.
func AddInterval(t time.Time, iv store.Interval) time.Time {
	if iv == store.Yearly {
		return t.AddDate(1, 0, 0)
	}
	return t.AddDate(0, 1, 0)
}

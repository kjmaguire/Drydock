// Package billing computes invoice totals. All amounts are integer cents.
package billing

import "example.com/saasapi/internal/store"

// Totals are the amounts shown on an invoice.
type Totals struct {
	SubtotalCents int64 `json:"subtotal_cents"`
	DiscountCents int64 `json:"discount_cents"`
	TaxCents      int64 `json:"tax_cents"`
	TotalCents    int64 `json:"total_cents"`
}

// Compute returns the totals of an invoice: the coupon applies to the subtotal first, then the credit, never below zero, then
// tax on what is left. Percentages round half up to a whole cent.
func Compute(inv store.Invoice) Totals {
	var subtotal int64
	for _, l := range inv.Lines {
		subtotal += int64(l.Quantity) * l.UnitCents
	}
	coupon := roundDiv(subtotal*int64(inv.CouponPercent), 100)
	discounted := subtotal - coupon - inv.CreditCents
	if discounted < 0 {
		discounted = 0
	}
	tax := roundDiv(discounted*int64(inv.TaxRateBP), 10000)
	return Totals{SubtotalCents: subtotal, DiscountCents: subtotal - discounted, TaxCents: tax, TotalCents: discounted + tax}
}

// roundDiv is n/d rounded half up, for n >= 0 and d > 0.
func roundDiv(n, d int64) int64 { return (n + d/2) / d }

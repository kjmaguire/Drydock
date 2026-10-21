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

// Compute returns the totals of an invoice: the subtotal of its lines, the credit and the coupon, then tax.
func Compute(inv store.Invoice) Totals {
	var subtotal int64
	for _, l := range inv.Lines {
		subtotal += int64(l.Quantity) * l.UnitCents
	}
	afterCredit := subtotal - inv.CreditCents
	coupon := afterCredit * int64(inv.CouponPercent) / 100
	discounted := afterCredit - coupon
	tax := discounted * int64(inv.TaxRateBP) / 10000
	return Totals{SubtotalCents: subtotal, DiscountCents: subtotal - discounted, TaxCents: tax, TotalCents: discounted + tax}
}

package billing_test

// Hidden acceptance test of the trial task saasapi-invoice-rules (evals/tasks). The agents never see it.

import (
	"testing"

	"example.com/saasapi/internal/billing"
	"example.com/saasapi/internal/store"
)

func TestHiddenInvoiceRules(t *testing.T) {
	line := func(q int, cents int64) []store.LineItem {
		return []store.LineItem{{Description: "x", Quantity: q, UnitCents: cents}}
	}
	cases := []struct {
		name string
		inv  store.Invoice
		want billing.Totals
	}{
		{"coupon before credit", store.Invoice{Lines: line(1, 10000), CouponPercent: 10, CreditCents: 500},
			billing.Totals{SubtotalCents: 10000, DiscountCents: 1500, TaxCents: 0, TotalCents: 8500}},
		{"coupon rounds half up", store.Invoice{Lines: line(1, 999), CouponPercent: 15},
			billing.Totals{SubtotalCents: 999, DiscountCents: 150, TaxCents: 0, TotalCents: 849}},
		{"exactly half a cent rounds up", store.Invoice{Lines: line(1, 50), CouponPercent: 1},
			billing.Totals{SubtotalCents: 50, DiscountCents: 1, TaxCents: 0, TotalCents: 49}},
		{"credit larger than the invoice", store.Invoice{Lines: line(1, 1000), CreditCents: 5000, TaxRateBP: 2000},
			billing.Totals{SubtotalCents: 1000, DiscountCents: 1000, TaxCents: 0, TotalCents: 0}},
		{"tax rounds half up", store.Invoice{Lines: line(1, 1999), TaxRateBP: 825},
			billing.Totals{SubtotalCents: 1999, DiscountCents: 0, TaxCents: 165, TotalCents: 2164}},
		{"tax on the discounted amount", store.Invoice{Lines: line(3, 3333), CouponPercent: 20, CreditCents: 100, TaxRateBP: 750},
			// 9999; coupon 1999.8 -> 2000; 7999 - 100 = 7899; tax 592.425 -> 592
			billing.Totals{SubtotalCents: 9999, DiscountCents: 2100, TaxCents: 592, TotalCents: 8491}},
		{"a full coupon", store.Invoice{Lines: line(2, 4500), CouponPercent: 100, CreditCents: 100, TaxRateBP: 825},
			billing.Totals{SubtotalCents: 9000, DiscountCents: 9000, TaxCents: 0, TotalCents: 0}},
		{"several lines", store.Invoice{Lines: []store.LineItem{{Quantity: 2, UnitCents: 1250}, {Quantity: 1, UnitCents: 99}}, CouponPercent: 5, TaxRateBP: 1000},
			// 2599; coupon 129.95 -> 130; 2469; tax 246.9 -> 247
			billing.Totals{SubtotalCents: 2599, DiscountCents: 130, TaxCents: 247, TotalCents: 2716}},
		{"nothing to discount", store.Invoice{Lines: line(1, 10000), TaxRateBP: 825},
			billing.Totals{SubtotalCents: 10000, DiscountCents: 0, TaxCents: 825, TotalCents: 10825}},
	}
	for _, c := range cases {
		if got := billing.Compute(c.inv); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

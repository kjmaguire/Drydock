package billing

import (
	"testing"
	"time"

	"example.com/tally/internal/store"
)

func TestCompute(t *testing.T) {
	inv := store.Invoice{
		Lines:           []store.Line{{Description: "Seats", Quantity: 3, UnitAmount: 1500}, {Description: "Setup", Quantity: 1, UnitAmount: 999}},
		DiscountPercent: 10,
		TaxRateBP:       825,
	}
	got := Compute(inv)
	// subtotal 5499; discount 549.9 -> 550; taxable 4949; tax 408.2925 -> 408
	want := Totals{Subtotal: 5499, Discount: 550, Tax: 408, Total: 5357}
	if got != want {
		t.Fatalf("Compute = %+v, want %+v", got, want)
	}
}

func TestComputeWithoutDiscountOrTax(t *testing.T) {
	got := Compute(store.Invoice{Lines: []store.Line{{Description: "x", Quantity: 2, UnitAmount: 250}}})
	if got != (Totals{Subtotal: 500, Total: 500}) {
		t.Fatalf("Compute = %+v", got)
	}
}

func TestAddInterval(t *testing.T) {
	start := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	if got := AddInterval(start, store.Monthly); !got.Equal(time.Date(2026, 4, 14, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("monthly: %v", got)
	}
	if got := AddInterval(start, store.Yearly); !got.Equal(time.Date(2027, 3, 14, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("yearly: %v", got)
	}
}

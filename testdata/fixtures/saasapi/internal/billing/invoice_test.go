package billing

import (
	"testing"

	"example.com/saasapi/internal/store"
)

func TestASimpleInvoice(t *testing.T) {
	got := Compute(store.Invoice{Lines: []store.LineItem{{Description: "Seats", Quantity: 2, UnitCents: 1000}}})
	want := Totals{SubtotalCents: 2000, TotalCents: 2000}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTaxIsAddedToTheSubtotal(t *testing.T) {
	got := Compute(store.Invoice{Lines: []store.LineItem{{Description: "Pro plan", Quantity: 1, UnitCents: 10000}}, TaxRateBP: 825})
	want := Totals{SubtotalCents: 10000, TaxCents: 825, TotalCents: 10825}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

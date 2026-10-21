package reports

import (
	"errors"
	"testing"
	"time"

	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

func TestRevenue(t *testing.T) {
	st := store.New()
	day := func(d, h int) time.Time { return time.Date(2026, 3, d, h, 0, 0, 0, time.UTC) }
	o, _ := st.CreateOrg("A", store.OrgTeam, "UTC", 0, day(1, 0))
	c, _ := st.CreateCustomer(o.ID, "Jane", "jane@example.com", day(1, 0))
	inv, _ := st.CreateInvoice(store.Invoice{OrgID: o.ID, CustomerID: c.ID, Currency: money.USD, Lines: []store.Line{{Description: "x", Quantity: 1, UnitAmount: 1}}}, day(1, 0))
	pay := func(amount int64, cur money.Currency, status store.PaymentStatus, at time.Time) {
		if _, err := st.CreatePayment(store.Payment{OrgID: o.ID, InvoiceID: inv.ID, Amount: amount, Currency: cur, Status: status}, at); err != nil {
			t.Fatal(err)
		}
	}
	pay(1000, money.USD, store.PaymentSucceeded, day(2, 9))
	pay(500, money.USD, store.PaymentSucceeded, day(2, 15))
	pay(700, money.EUR, store.PaymentSucceeded, day(2, 12))
	pay(999, money.USD, store.PaymentFailed, day(2, 13))
	pay(300, money.USD, store.PaymentSucceeded, day(4, 12))
	pay(100, money.USD, store.PaymentSucceeded, day(6, 12)) // after the range
	got, err := Revenue(st, o.ID, "2026-03-01", "2026-03-05")
	if err != nil {
		t.Fatal(err)
	}
	want := []DayRevenue{
		{Date: "2026-03-02", Currency: money.EUR, Amount: 700, Payments: 1},
		{Date: "2026-03-02", Currency: money.USD, Amount: 1500, Payments: 2},
		{Date: "2026-03-04", Currency: money.USD, Amount: 300, Payments: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("Revenue = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("day %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRevenueRange(t *testing.T) {
	st := store.New()
	for _, c := range []struct {
		from, to string
		want     error
	}{
		{"2026-03-01", "2026/03/05", ErrInvalidDate},
		{"yesterday", "2026-03-05", ErrInvalidDate},
		{"2026-03-05", "2026-03-01", ErrInvalidRange},
		{"2026-01-01", "2027-01-02", ErrInvalidRange},
	} {
		if _, err := Revenue(st, "org_x", c.from, c.to); !errors.Is(err, c.want) {
			t.Errorf("Revenue(%s, %s) = %v, want %v", c.from, c.to, err, c.want)
		}
	}
	if got, err := Revenue(st, "org_x", "2026-01-01", "2026-12-31"); err != nil || len(got) != 0 {
		t.Fatalf("a full year: %v, %v", got, err)
	}
}

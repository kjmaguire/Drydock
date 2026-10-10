package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/tally/internal/clock"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

type recorder struct{ types []string }

func (r *recorder) Publish(ctx context.Context, orgID, typ string, data any) error {
	r.types = append(r.types, typ)
	return nil
}

func setup(t *testing.T, lines ...store.Line) (*Service, *Fake, *recorder, store.Invoice) {
	t.Helper()
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	st := store.New()
	o, _ := st.CreateOrg("A", store.OrgTeam, "UTC", 1000, now)
	c, _ := st.CreateCustomer(o.ID, "Jane", "jane@example.com", now)
	inv, err := st.CreateInvoice(store.Invoice{OrgID: o.ID, CustomerID: c.ID, Currency: money.USD, Lines: lines}, now)
	if err != nil {
		t.Fatal(err)
	}
	gw, pub := &Fake{}, &recorder{}
	return &Service{Store: st, Gateway: gw, Clock: clock.NewFake(now), Publisher: pub}, gw, pub, inv
}

func TestPaySucceeds(t *testing.T) {
	s, gw, pub, inv := setup(t, store.Line{Description: "x", Quantity: 2, UnitAmount: 500})
	paid, p, err := s.Pay(context.Background(), inv.OrgID, inv.ID, "pm_card_visa")
	if err != nil {
		t.Fatal(err)
	}
	if paid.Status != store.InvoicePaid || paid.PaidAt == nil {
		t.Fatalf("invoice = %+v", paid)
	}
	if p.Status != store.PaymentSucceeded || p.Amount != 1100 || p.GatewayRef == "" { // 1000 + 10% tax
		t.Fatalf("payment = %+v", p)
	}
	if cs := gw.Charges(); len(cs) != 1 || cs[0].Amount != 1100 || cs[0].Currency != money.USD {
		t.Fatalf("charges = %+v", cs)
	}
	if len(pub.types) != 1 || pub.types[0] != "invoice.paid" {
		t.Fatalf("events = %v", pub.types)
	}
	if _, _, err := s.Pay(context.Background(), inv.OrgID, inv.ID, "pm_card_visa"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("second pay: %v", err)
	}
}

func TestPayDeclined(t *testing.T) {
	s, gw, pub, inv := setup(t, store.Line{Description: "x", Quantity: 1, UnitAmount: 500})
	_, p, err := s.Pay(context.Background(), inv.OrgID, inv.ID, "pm_card_declined")
	var d *DeclineError
	if !errors.As(err, &d) || d.Code != "card_declined" {
		t.Fatalf("err = %v", err)
	}
	if p.Status != store.PaymentFailed || p.FailureCode != "card_declined" {
		t.Fatalf("payment = %+v", p)
	}
	if got, _ := s.Store.Invoice(inv.OrgID, inv.ID); got.Status != store.InvoiceOpen {
		t.Fatalf("invoice = %+v", got)
	}
	if len(gw.Charges()) != 0 || len(gw.Calls()) != 1 {
		t.Fatalf("calls %d, charges %d", len(gw.Calls()), len(gw.Charges()))
	}
	if len(pub.types) != 1 || pub.types[0] != "invoice.payment_failed" {
		t.Fatalf("events = %v", pub.types)
	}
}

func TestPayGatewayDown(t *testing.T) {
	s, _, _, inv := setup(t, store.Line{Description: "x", Quantity: 1, UnitAmount: 500})
	if _, _, err := s.Pay(context.Background(), inv.OrgID, inv.ID, "pm_gateway_down"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if ps := s.Store.Payments(inv.OrgID, inv.ID); len(ps) != 0 {
		t.Fatalf("a payment was recorded: %+v", ps)
	}
}

func TestPayNeedsAPaymentMethod(t *testing.T) {
	s, _, _, inv := setup(t, store.Line{Description: "x", Quantity: 1, UnitAmount: 500})
	if _, _, err := s.Pay(context.Background(), inv.OrgID, inv.ID, ""); !errors.Is(err, ErrNoPaymentMethod) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Store.UpdateCustomer(inv.OrgID, inv.CustomerID, func(c *store.Customer) error { c.DefaultPaymentMethod = "pm_card_visa"; return nil }); err != nil {
		t.Fatal(err)
	}
	if paid, _, err := s.Pay(context.Background(), inv.OrgID, inv.ID, ""); err != nil || paid.Status != store.InvoicePaid {
		t.Fatalf("pay with the default method: %+v, %v", paid, err)
	}
}

func TestPayVoidAndFree(t *testing.T) {
	s, gw, _, inv := setup(t, store.Line{Description: "free", Quantity: 1, UnitAmount: 0})
	paid, _, err := s.Pay(context.Background(), inv.OrgID, inv.ID, "")
	if err != nil || paid.Status != store.InvoicePaid || len(gw.Calls()) != 0 {
		t.Fatalf("a zero invoice: %+v, %v, %d calls", paid, err, len(gw.Calls()))
	}
	s2, _, _, inv2 := setup(t, store.Line{Description: "x", Quantity: 1, UnitAmount: 100})
	_, _ = s2.Store.UpdateInvoice(inv2.OrgID, inv2.ID, func(i *store.Invoice) error { i.Status = store.InvoiceVoid; return nil })
	if _, _, err := s2.Pay(context.Background(), inv2.OrgID, inv2.ID, "pm_card_visa"); !errors.Is(err, ErrInvoiceVoid) {
		t.Fatalf("void: %v", err)
	}
}

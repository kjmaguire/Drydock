package subscriptions_test

import (
	"context"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
)

func TestCreateChargesTheFirstPeriod(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	_, _ = e.Store.UpdateCustomer(e.Acme.ID, c.ID, func(c *store.Customer) error { c.DefaultPaymentMethod = "pm_card_visa"; return nil })
	p := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	sub, err := e.Subs.Create(ctx, e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: p.ID, Quantity: 3})
	if err != nil {
		t.Fatal(err)
	}
	now := e.Clock.Now()
	if sub.Status != store.Active || !sub.CurrentPeriodStart.Equal(now) || !sub.CurrentPeriodEnd.Equal(now.AddDate(0, 1, 0)) {
		t.Fatalf("subscription = %+v", sub)
	}
	invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID})
	if len(invs) != 1 || invs[0].Status != store.InvoicePaid || invs[0].Lines[0].Quantity != 3 || invs[0].Lines[0].UnitAmount != 2000 {
		t.Fatalf("invoices = %+v", invs)
	}
	if cs := e.Gateway.Charges(); len(cs) != 1 || cs[0].Amount != 6000 {
		t.Fatalf("charges = %+v", cs)
	}
}

func TestDeclinedFirstChargeIsPastDue(t *testing.T) {
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	_, _ = e.Store.UpdateCustomer(e.Acme.ID, c.ID, func(c *store.Customer) error { c.DefaultPaymentMethod = "pm_card_declined"; return nil })
	p := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	sub, err := e.Subs.Create(context.Background(), e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: p.ID, Quantity: 1})
	if err != nil || sub.Status != store.PastDue {
		t.Fatalf("subscription = %+v, %v", sub, err)
	}
}

func TestTrialThenRenewal(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	p, err := e.Store.CreatePlan(store.Plan{OrgID: e.Acme.ID, Name: "Pro", Currency: money.EUR, UnitAmount: 4900, Interval: store.Monthly, TrialDays: 14}, e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.Subs.Create(ctx, e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: p.ID, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	trialEnd := e.Clock.Now().AddDate(0, 0, 14)
	if sub.Status != store.Trialing || sub.TrialEnd == nil || !sub.TrialEnd.Equal(trialEnd) || !sub.CurrentPeriodEnd.Equal(trialEnd) {
		t.Fatalf("subscription = %+v", sub)
	}
	if invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{}); len(invs) != 0 {
		t.Fatalf("a trial was invoiced: %+v", invs)
	}
	e.Clock.Set(trialEnd)
	if n, err := e.Subs.RenewDue(ctx); err != nil || n != 1 {
		t.Fatalf("RenewDue = %d, %v", n, err)
	}
	got, _ := e.Store.Subscription(e.Acme.ID, sub.ID)
	if got.Status != store.Active || !got.CurrentPeriodStart.Equal(trialEnd) || !got.CurrentPeriodEnd.Equal(trialEnd.AddDate(0, 1, 0)) {
		t.Fatalf("after the trial = %+v", got)
	}
	invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID})
	if len(invs) != 1 || invs[0].Status != store.InvoiceOpen || invs[0].PeriodStart == nil || !invs[0].PeriodStart.Equal(trialEnd) {
		t.Fatalf("invoices = %+v (no payment method: the invoice stays open)", invs)
	}
	if n, _ := e.Subs.RenewDue(ctx); n != 0 {
		t.Fatalf("renewed twice: %d", n)
	}
}

func TestCancelAtPeriodEnd(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	p := e.CreatePlan(e.Acme, "Team", "USD", "20.00")
	sub, _ := e.Subs.Create(ctx, e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: p.ID, Quantity: 1})
	sub, err := e.Subs.Cancel(ctx, e.Acme.ID, sub.ID, true)
	if err != nil || sub.Status != store.Active || !sub.CancelAtPeriodEnd {
		t.Fatalf("cancel = %+v, %v", sub, err)
	}
	e.Clock.Set(sub.CurrentPeriodEnd)
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Store.Subscription(e.Acme.ID, sub.ID)
	if got.Status != store.Canceled || got.CanceledAt == nil || !got.CanceledAt.Equal(sub.CurrentPeriodEnd) {
		t.Fatalf("after the period = %+v", got)
	}
	if invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID}); len(invs) != 1 {
		t.Fatalf("invoices = %d, want only the first", len(invs))
	}
}

func TestMeteredBillsUsageInArrears(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	c := e.CreateCustomer(e.Acme, "Jane", "jane@example.com")
	p, _ := e.Store.CreatePlan(store.Plan{OrgID: e.Acme.ID, Name: "API calls", Currency: money.USD, UnitAmount: 2, Interval: store.Monthly, UsageType: store.Metered}, e.Clock.Now())
	sub, err := e.Subs.Create(ctx, e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: p.ID, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{}); len(invs) != 0 {
		t.Fatalf("a metered plan was invoiced in advance: %+v", invs)
	}
	start, end := sub.CurrentPeriodStart, sub.CurrentPeriodEnd
	for _, u := range []struct {
		q  int64
		at time.Time
	}{{100, start}, {250, start.Add(48 * time.Hour)}, {7, end}} {
		if _, err := e.Store.AddUsage(e.Acme.ID, sub.ID, u.q, u.at, e.Clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	e.Clock.Set(end)
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Fatal(err)
	}
	invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID})
	if len(invs) != 1 || len(invs[0].Lines) != 1 || invs[0].Lines[0].Quantity != 350 || !invs[0].PeriodEnd.Equal(end) {
		t.Fatalf("invoices = %+v (usage at the period's end belongs to the next period)", invs)
	}
}

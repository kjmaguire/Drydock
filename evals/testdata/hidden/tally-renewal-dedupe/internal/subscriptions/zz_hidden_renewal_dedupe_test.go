package subscriptions_test

// Hidden acceptance test of the trial task tally-renewal-dedupe (evals/tasks). The agents never see it.
// Several server instances are several subscriptions.Service (and payments.Service) values over the same store. Waits for
// things that should take milliseconds get many seconds before they fail.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/tally/internal/apitest"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/money"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
)

const hiddenRenewWait = 60 * time.Second

func hiddenRenewPlan(t *testing.T, e *apitest.Env, name string, amount int64, iv store.Interval, usage store.UsageType) store.Plan {
	t.Helper()
	p, err := e.Store.CreatePlan(store.Plan{OrgID: e.Acme.ID, Name: name, Currency: money.USD, UnitAmount: amount, Interval: iv, UsageType: usage}, e.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hiddenRenewSetMethod(t *testing.T, e *apitest.Env, customerID, method string) {
	t.Helper()
	if _, err := e.Store.UpdateCustomer(e.Acme.ID, customerID, func(c *store.Customer) error { c.DefaultPaymentMethod = method; return nil }); err != nil {
		t.Fatal(err)
	}
}

// hiddenRenewSubscribe starts a subscription now for a new customer who pays with first; the customer then switches to later.
func hiddenRenewSubscribe(t *testing.T, e *apitest.Env, email string, plan store.Plan, first, later string) store.Subscription {
	t.Helper()
	c := e.CreateCustomer(e.Acme, "Hidden "+email, email)
	hiddenRenewSetMethod(t, e, c.ID, first)
	sub, err := e.Subs.Create(context.Background(), e.Acme.ID, subscriptions.CreateInput{CustomerID: c.ID, PlanID: plan.ID, Quantity: 1})
	if err != nil {
		t.Fatalf("create the subscription of %s: %v", email, err)
	}
	hiddenRenewSetMethod(t, e, c.ID, later)
	return sub
}

func hiddenRenewSub(t *testing.T, e *apitest.Env, id string) store.Subscription {
	t.Helper()
	sub, ok := e.Store.Subscription(e.Acme.ID, id)
	if !ok {
		t.Fatalf("subscription %s is gone", id)
	}
	return sub
}

func hiddenRenewDesc(inv store.Invoice) string {
	ps := "none"
	if inv.PeriodStart != nil {
		ps = inv.PeriodStart.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s (period start %s, %s, created %s)", inv.ID, ps, inv.Status, inv.CreatedAt.UTC().Format(time.RFC3339))
}

// hiddenRenewInvoices returns the subscription's invoices by period start (RFC 3339), failing when a period has more
// than one.
func hiddenRenewInvoices(t *testing.T, e *apitest.Env, sub store.Subscription, what string) map[string]store.Invoice {
	t.Helper()
	invs := e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID})
	groups := map[string][]store.Invoice{}
	for _, inv := range invs {
		k := "none"
		if inv.PeriodStart != nil {
			k = inv.PeriodStart.UTC().Format(time.RFC3339Nano)
		}
		groups[k] = append(groups[k], inv)
	}
	out := map[string]store.Invoice{}
	var dup []string
	for k, g := range groups {
		if len(g) > 1 {
			var ds []string
			for _, inv := range g {
				ds = append(ds, hiddenRenewDesc(inv))
			}
			dup = append(dup, fmt.Sprintf("  %d invoices for the period starting %s: %s", len(g), k, strings.Join(ds, "; ")))
		}
		out[k] = g[0]
	}
	if len(dup) > 0 {
		sort.Strings(dup)
		t.Fatalf("%s: subscription %s has more than one invoice for a period (a period is invoiced at most once):\n%s", what, sub.ID, strings.Join(dup, "\n"))
	}
	return out
}

// hiddenRenewInvoiceFor returns the subscription's one invoice for the period starting at start.
func hiddenRenewInvoiceFor(t *testing.T, e *apitest.Env, sub store.Subscription, start time.Time, what string) store.Invoice {
	t.Helper()
	byStart := hiddenRenewInvoices(t, e, sub, what)
	inv, ok := byStart[start.UTC().Format(time.RFC3339Nano)]
	if !ok {
		var have []string
		for _, inv := range e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID}) {
			have = append(have, hiddenRenewDesc(inv))
		}
		t.Fatalf("%s: subscription %s has no invoice for the period starting %s; its invoices: %s", what, sub.ID, start.UTC().Format(time.RFC3339), strings.Join(have, "; "))
	}
	return inv
}

func hiddenRenewWantCount(t *testing.T, e *apitest.Env, sub store.Subscription, want int, what string) {
	t.Helper()
	hiddenRenewInvoices(t, e, sub, what)
	if got := len(e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{SubscriptionID: sub.ID})); got != want {
		t.Fatalf("%s: subscription %s has %d invoices, want %d", what, sub.ID, got, want)
	}
}

func hiddenRenewWantPeriod(t *testing.T, e *apitest.Env, id string, start, end time.Time, what string) store.Subscription {
	t.Helper()
	sub := hiddenRenewSub(t, e, id)
	if !sub.CurrentPeriodStart.Equal(start) || !sub.CurrentPeriodEnd.Equal(end) {
		t.Fatalf("%s: subscription %s is in the period %s to %s, want %s to %s", what, id,
			sub.CurrentPeriodStart.UTC().Format(time.RFC3339), sub.CurrentPeriodEnd.UTC().Format(time.RFC3339), start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	}
	return sub
}

// hiddenRenewMoneyOnce checks that no invoice of the organization was paid more than once.
func hiddenRenewMoneyOnce(t *testing.T, e *apitest.Env, what string) {
	t.Helper()
	for _, inv := range e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{}) {
		n := 0
		for _, p := range e.Store.Payments(e.Acme.ID, inv.ID) {
			if p.Status == store.PaymentSucceeded {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("%s: invoice %s was charged %d times, want at most once", what, hiddenRenewDesc(inv), n)
		}
	}
}

// hiddenBarrierClock tells the test the first time its server instance reads the time (RenewDue starts by doing so).
type hiddenBarrierClock struct {
	base    clock.Clock
	once    sync.Once
	arrived func()
}

func (c *hiddenBarrierClock) Now() time.Time {
	c.once.Do(c.arrived)
	return c.base.Now()
}

// hiddenRenewConcurrently calls RenewDue from many goroutines at once: perInstance calls on each of `instances` new server
// instances and two on e.Subs. Every charge waits (in the gateway) until each new instance has started its RenewDue, so
// that the instances look at their due subscriptions while the first renewal is still being charged. It returns the
// errors RenewDue returned.
func hiddenRenewConcurrently(t *testing.T, e *apitest.Env, instances, perInstance int) []error {
	t.Helper()
	ready := make(chan struct{})
	var readyOnce sync.Once
	open := func() { readyOnce.Do(func() { close(ready) }) }
	var mu sync.Mutex
	arrived := 0
	e.Gateway.OnCharge = func(payments.ChargeRequest) {
		select {
		case <-ready:
		case <-time.After(5 * time.Second): // an implementation that lets one instance at a time read the time is not stuck
			open()
		}
		time.Sleep(20 * time.Millisecond) // widen the window in which another instance could charge the same period
	}

	var runners []*subscriptions.Service
	for i := 0; i < instances; i++ {
		pay := &payments.Service{Store: e.Store, Gateway: e.Gateway, Clock: e.Clock, Publisher: e.Webhooks}
		clk := &hiddenBarrierClock{base: e.Clock, arrived: func() {
			mu.Lock()
			defer mu.Unlock()
			if arrived++; arrived == instances {
				open()
			}
		}}
		svc := &subscriptions.Service{Store: e.Store, Payments: pay, Clock: clk, Publisher: e.Webhooks}
		for j := 0; j < perInstance; j++ {
			runners = append(runners, svc)
		}
	}
	runners = append(runners, e.Subs, e.Subs)

	start := make(chan struct{})
	errs := make(chan error, len(runners))
	var wg sync.WaitGroup
	for _, svc := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.RenewDue(context.Background())
			errs <- err
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(hiddenRenewWait):
		open()
		t.Fatalf("%d simultaneous RenewDue calls did not all return within %s", len(runners), hiddenRenewWait)
	}
	close(errs)
	var out []error
	for err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

func TestHiddenConcurrentRenewalsInvoiceAndChargeOnce(t *testing.T) {
	e := apitest.New(t)
	t0 := e.Clock.Now()
	monthly := hiddenRenewPlan(t, e, "Hidden Team", 2000, store.Monthly, store.Licensed)
	yearly := hiddenRenewPlan(t, e, "Hidden Annual", 20000, store.Yearly, store.Licensed)
	metered := hiddenRenewPlan(t, e, "Hidden API calls", 3, store.Monthly, store.Metered)

	var paying []store.Subscription
	for i := 1; i <= 4; i++ {
		paying = append(paying, hiddenRenewSubscribe(t, e, fmt.Sprintf("visa%d@hidden.example", i), monthly, "pm_card_visa", "pm_card_visa"))
	}
	usage := hiddenRenewSubscribe(t, e, "usage@hidden.example", metered, "pm_card_visa", "pm_card_visa")
	if _, err := e.Store.AddUsage(e.Acme.ID, usage.ID, 1000, t0.Add(time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	annual := hiddenRenewSubscribe(t, e, "annual@hidden.example", yearly, "pm_card_visa", "pm_card_visa")
	down := hiddenRenewSubscribe(t, e, "down@hidden.example", monthly, "pm_card_visa", "pm_gateway_down")
	declined := hiddenRenewSubscribe(t, e, "declined@hidden.example", monthly, "pm_card_visa", "pm_card_declined")
	pastDue := hiddenRenewSubscribe(t, e, "pastdue@hidden.example", monthly, "pm_card_declined", "pm_gateway_down")
	if pastDue.Status != store.PastDue {
		t.Fatalf("setup: a subscription whose first charge was declined is %s, want past_due", pastDue.Status)
	}
	chargesBefore := len(e.Gateway.Charges())

	t1 := paying[0].CurrentPeriodEnd
	t2 := t1.AddDate(0, 1, 0)
	e.Clock.Set(t1)
	errs := hiddenRenewConcurrently(t, e, 4, 3)
	for _, err := range errs {
		t.Logf("RenewDue returned: %v", err)
	}

	// First what the race is about: no period invoiced twice, no invoice charged twice.
	monthlyDue := append(append([]store.Subscription{}, paying...), usage, down, declined, pastDue)
	for _, sub := range append(append([]store.Subscription{}, monthlyDue...), annual) {
		hiddenRenewInvoices(t, e, sub, "after the simultaneous renewals")
	}
	hiddenRenewMoneyOnce(t, e, "after the simultaneous renewals")
	// The four paying subscriptions and the metered one were each charged once more.
	if got, want := len(e.Gateway.Charges()), chargesBefore+5; got != want {
		t.Fatalf("after the simultaneous renewals the gateway took %d charges, want %d (each renewal charged once)", got, want)
	}
	for _, sub := range monthlyDue {
		hiddenRenewWantPeriod(t, e, sub.ID, t1, t2, "after the simultaneous renewals")
	}
	for _, sub := range paying {
		inv := hiddenRenewInvoiceFor(t, e, sub, t1, "after the simultaneous renewals")
		hiddenRenewWantCount(t, e, sub, 2, "after the simultaneous renewals (the first invoice and the renewal's)")
		if inv.Status != store.InvoicePaid {
			t.Fatalf("the renewal invoice %s of %s is %s, want paid", inv.ID, sub.ID, inv.Status)
		}
	}
	usageInv := hiddenRenewInvoiceFor(t, e, usage, t0, "the metered subscription after the simultaneous renewals")
	hiddenRenewWantCount(t, e, usage, 1, "the metered subscription after the simultaneous renewals")
	if usageInv.Status != store.InvoicePaid {
		t.Fatalf("the metered subscription's invoice %s is %s, want paid", usageInv.ID, usageInv.Status)
	}
	for _, sub := range []store.Subscription{down, pastDue} {
		inv := hiddenRenewInvoiceFor(t, e, sub, t1, "with the gateway down, after the simultaneous renewals")
		hiddenRenewWantCount(t, e, sub, 2, "with the gateway down, after the simultaneous renewals")
		if inv.Status != store.InvoiceOpen {
			t.Fatalf("with the gateway down, the renewal invoice %s of %s is %s, want open", inv.ID, sub.ID, inv.Status)
		}
		if got := hiddenRenewSub(t, e, sub.ID); got.Status != sub.Status {
			t.Fatalf("with the gateway down, subscription %s became %s; it keeps its status %s", sub.ID, got.Status, sub.Status)
		}
	}
	hiddenRenewInvoiceFor(t, e, declined, t1, "the declined subscription after the simultaneous renewals")
	hiddenRenewWantCount(t, e, declined, 2, "the declined subscription after the simultaneous renewals")
	hiddenRenewWantPeriod(t, e, annual.ID, annual.CurrentPeriodStart, annual.CurrentPeriodEnd, "a yearly subscription that is not due")
	hiddenRenewWantCount(t, e, annual, 1, "a yearly subscription that is not due")

	// Another round at the same time finds nothing to do.
	invoicesBefore := len(e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{}))
	errs = hiddenRenewConcurrently(t, e, 3, 2)
	for _, err := range errs {
		t.Logf("second round: RenewDue returned: %v", err)
	}
	if got := len(e.Store.ListInvoices(e.Acme.ID, store.InvoiceFilter{})); got != invoicesBefore {
		t.Fatalf("a second round of renewals at the same time created %d more invoices", got-invoicesBefore)
	}
	if got, want := len(e.Gateway.Charges()), chargesBefore+5; got != want {
		t.Fatalf("a second round of renewals at the same time took %d more charges", got-want)
	}
	for _, sub := range monthlyDue {
		hiddenRenewWantPeriod(t, e, sub.ID, t1, t2, "after a second round")
	}
}

func TestHiddenRenewalWithTheGatewayDown(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	monthly := hiddenRenewPlan(t, e, "Hidden Team", 2000, store.Monthly, store.Licensed)
	active := hiddenRenewSubscribe(t, e, "active@hidden.example", monthly, "pm_card_visa", "pm_gateway_down")
	pastDue := hiddenRenewSubscribe(t, e, "pastdue@hidden.example", monthly, "pm_card_declined", "pm_gateway_down")
	if active.Status != store.Active || pastDue.Status != store.PastDue {
		t.Fatalf("setup: statuses %s and %s, want active and past_due", active.Status, pastDue.Status)
	}
	charges := len(e.Gateway.Charges())
	t1 := active.CurrentPeriodEnd
	t2 := t1.AddDate(0, 1, 0)
	e.Clock.Set(t1)

	for round := 1; round <= 4; round++ {
		what := fmt.Sprintf("with the gateway down, after RenewDue call %d at the period's end", round)
		if _, err := e.Subs.RenewDue(ctx); err != nil {
			t.Logf("%s: RenewDue returned %v", what, err)
		}
		for _, sub := range []store.Subscription{active, pastDue} {
			got := hiddenRenewWantPeriod(t, e, sub.ID, t1, t2, what)
			if got.Status != sub.Status {
				t.Fatalf("%s: subscription %s is %s; it keeps its status %s", what, sub.ID, got.Status, sub.Status)
			}
			inv := hiddenRenewInvoiceFor(t, e, sub, t1, what)
			if inv.Status != store.InvoiceOpen {
				t.Fatalf("%s: the renewal invoice %s is %s, want open (it can be paid later)", what, inv.ID, inv.Status)
			}
			hiddenRenewWantCount(t, e, sub, 2, what)
		}
		if got := len(e.Gateway.Charges()); got != charges {
			t.Fatalf("%s: the gateway took %d charges, want none", what, got-charges)
		}
	}

	// The open invoice is paid later, by hand; renewals still do not invoice the period again.
	inv := hiddenRenewInvoiceFor(t, e, active, t1, "before paying by hand")
	hiddenRenewSetMethod(t, e, active.CustomerID, "pm_card_visa")
	if r := e.Do("POST", "/v1/invoices/"+inv.ID+"/pay", e.Acme.Key, nil); r.Code != http.StatusOK {
		t.Fatalf("paying the open renewal invoice later: want 200, got %s", r)
	}
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Logf("after paying by hand: RenewDue returned %v", err)
	}
	hiddenRenewWantCount(t, e, active, 2, "after paying the open invoice by hand")
	if got := len(e.Gateway.Charges()); got != charges+1 {
		t.Fatalf("after paying the open invoice by hand: %d charges, want 1", got-charges)
	}

	// The next period is renewed once, and charged once now that the customer's card works.
	e.Clock.Set(t2)
	for i := 0; i < 2; i++ {
		if _, err := e.Subs.RenewDue(ctx); err != nil {
			t.Logf("at the next period's end: RenewDue returned %v", err)
		}
	}
	hiddenRenewWantPeriod(t, e, active.ID, t2, t2.AddDate(0, 1, 0), "at the next period's end")
	next := hiddenRenewInvoiceFor(t, e, active, t2, "at the next period's end")
	hiddenRenewWantCount(t, e, active, 3, "at the next period's end")
	if next.Status != store.InvoicePaid {
		t.Fatalf("at the next period's end, the renewal invoice %s is %s, want paid", next.ID, next.Status)
	}
	if got := len(e.Gateway.Charges()); got != charges+2 {
		t.Fatalf("at the next period's end: %d charges in all since the gateway went down, want 2", got-charges)
	}
	hiddenRenewMoneyOnce(t, e, "at the next period's end")
}

func TestHiddenRenewDueGoesOnAfterAFailure(t *testing.T) {
	e := apitest.New(t)
	ctx := context.Background()
	monthly := hiddenRenewPlan(t, e, "Hidden Team", 2000, store.Monthly, store.Licensed)
	// Two subscriptions whose renewal fails, due before a healthy one: the renewal invoice of the first cannot be created
	// because its customer was deleted, that of the second because its discount is out of range.
	gone := hiddenRenewSubscribe(t, e, "gone@hidden.example", monthly, "pm_card_visa", "pm_card_visa")
	if err := e.Store.DeleteCustomer(e.Acme.ID, gone.CustomerID); err != nil {
		t.Fatal(err)
	}
	broken := hiddenRenewSubscribe(t, e, "broken@hidden.example", monthly, "pm_card_visa", "pm_card_visa")
	if _, err := e.Store.UpdateSubscription(e.Acme.ID, broken.ID, func(x *store.Subscription) error { x.DiscountPercent = 150; return nil }); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(time.Hour)
	healthy := hiddenRenewSubscribe(t, e, "healthy@hidden.example", monthly, "pm_card_visa", "pm_card_visa")
	charges := len(e.Gateway.Charges())

	t1 := healthy.CurrentPeriodEnd
	e.Clock.Set(t1)
	_, err := e.Subs.RenewDue(ctx)
	if err == nil {
		t.Fatal("RenewDue returned no error although two renewals failed")
	}
	t.Logf("RenewDue returned: %v", err)
	hiddenRenewWantPeriod(t, e, healthy.ID, t1, t1.AddDate(0, 1, 0), "the healthy subscription, due after two failing ones")
	inv := hiddenRenewInvoiceFor(t, e, healthy, t1, "the healthy subscription, due after two failing ones")
	hiddenRenewWantCount(t, e, healthy, 2, "the healthy subscription, due after two failing ones")
	if inv.Status != store.InvoicePaid || len(e.Gateway.Charges()) != charges+1 {
		t.Fatalf("the healthy subscription's renewal invoice %s is %s after %d charges, want paid after 1", inv.ID, inv.Status, len(e.Gateway.Charges())-charges)
	}
	if _, err := e.Subs.RenewDue(ctx); err != nil {
		t.Logf("the second run: %v", err)
	}
	hiddenRenewWantCount(t, e, healthy, 2, "after a second run")
	hiddenRenewMoneyOnce(t, e, "after a second run")
}

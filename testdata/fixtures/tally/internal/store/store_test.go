package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"example.com/tally/internal/money"
)

var t0 = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

func newOrg(t *testing.T, s *Store, name string) Org {
	t.Helper()
	o, err := s.CreateOrg(name, OrgTeam, "UTC", 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOrgValidation(t *testing.T) {
	s := New()
	for _, c := range []struct {
		name, tz string
		plan     OrgPlan
		tax      int
	}{
		{"", "UTC", OrgFree, 0},
		{"X", "Mars/Olympus", OrgFree, 0},
		{"X", "UTC", "gold", 0},
		{"X", "UTC", OrgFree, 10001},
	} {
		if _, err := s.CreateOrg(c.name, c.plan, c.tz, c.tax, t0); !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateOrg(%+v) = %v, want ErrInvalid", c, err)
		}
	}
	o := newOrg(t, s, "Acme")
	if _, err := s.UpdateOrg(o.ID, func(o *Org) error { o.TimeZone = "Nowhere"; return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad time zone accepted: %v", err)
	}
	if got, _ := s.Org(o.ID); got.TimeZone != "UTC" {
		t.Fatalf("a refused update changed the org: %+v", got)
	}
}

func TestUsersAndMembers(t *testing.T) {
	s := New()
	o := newOrg(t, s, "Acme")
	u, err := s.CreateUser("Ann@Example.com", "Ann", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("ann@example.COM", "Other Ann", t0); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: %v", err)
	}
	if got, ok := s.UserByEmail("ANN@example.com"); !ok || got.ID != u.ID {
		t.Fatalf("UserByEmail = %+v %v", got, ok)
	}
	if _, err := s.AddMember(o.ID, u.ID, RoleAdmin, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMember(o.ID, u.ID, RoleViewer, t0); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("second membership: %v", err)
	}
	if _, err := s.SetMemberRole(o.ID, u.ID, "superuser"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown role: %v", err)
	}
	if m, err := s.SetMemberRole(o.ID, u.ID, RoleBilling); err != nil || m.Role != RoleBilling {
		t.Fatalf("SetMemberRole = %+v, %v", m, err)
	}
	if err := s.RemoveMember(o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Member(o.ID, u.ID); ok {
		t.Fatal("still a member")
	}
}

func TestAPIKeys(t *testing.T) {
	s := New()
	o := newOrg(t, s, "Acme")
	u, _ := s.CreateUser("ann@example.com", "Ann", t0)
	if _, err := s.CreateAPIKey(o.ID, u.ID, "k", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a key for a non-member: %v", err)
	}
	_, _ = s.AddMember(o.ID, u.ID, RoleMember, t0)
	k, err := s.CreateAPIKey(o.ID, u.ID, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "API key" || len(k.Token) != len("tly_")+32 {
		t.Fatalf("key = %+v", k)
	}
	if got, ok := s.APIKeyByToken(k.Token); !ok || got.ID != k.ID {
		t.Fatalf("APIKeyByToken = %+v %v", got, ok)
	}
	if _, ok := s.APIKeyByToken(""); ok {
		t.Fatal("the empty token found a key")
	}
	other := newOrg(t, s, "Other")
	if _, err := s.RevokeAPIKey(other.ID, k.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked through another org: %v", err)
	}
	r, err := s.RevokeAPIKey(o.ID, k.ID, t0.Add(time.Hour))
	if err != nil || r.RevokedAt == nil || !r.RevokedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("revoke = %+v, %v", r, err)
	}
}

func TestCustomers(t *testing.T) {
	s := New()
	a, b := newOrg(t, s, "A"), newOrg(t, s, "B")
	c1, err := s.CreateCustomer(a.ID, "Jane Doe", "jane@example.com", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomer(a.ID, "Jane Again", "JANE@example.com", t0); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email in one org: %v", err)
	}
	if _, err := s.CreateCustomer(a.ID, "", "x@example.com", t0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no name: %v", err)
	}
	c2, _ := s.CreateCustomer(a.ID, "John Roe", "john@example.com", t0.Add(time.Minute))
	if _, ok := s.Customer(b.ID, c1.ID); ok {
		t.Fatal("another org's customer is visible")
	}
	if got := s.Customers(a.ID); len(got) != 2 || got[0].ID != c1.ID || got[1].ID != c2.ID {
		t.Fatalf("Customers = %+v", got)
	}
	if got := s.SearchCustomers(a.ID, "ROE"); len(got) != 1 || got[0].ID != c2.ID {
		t.Fatalf("SearchCustomers = %+v", got)
	}
	if got, ok := s.FindCustomerByEmail(a.ID, "Jane@Example.com"); !ok || got.ID != c1.ID {
		t.Fatalf("FindCustomerByEmail = %+v %v", got, ok)
	}
	up, err := s.UpdateCustomer(a.ID, c1.ID, func(c *Customer) error {
		c.Email = "jane.doe@example.com"
		c.Metadata = map[string]string{"crm": "42"}
		return nil
	})
	if err != nil || up.Email != "jane.doe@example.com" {
		t.Fatalf("update = %+v, %v", up, err)
	}
	up.Metadata["crm"] = "changed"
	if got, _ := s.Customer(a.ID, c1.ID); got.Metadata["crm"] != "42" {
		t.Fatal("a returned customer shares its metadata with the store")
	}
	if _, err := s.UpdateCustomer(a.ID, c2.ID, func(c *Customer) error { c.Email = "jane.doe@example.com"; return nil }); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("email taken on update: %v", err)
	}
	if err := s.DeleteCustomer(b.ID, c1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted through another org: %v", err)
	}
	if err := s.DeleteCustomer(a.ID, c1.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Customer(a.ID, c1.ID); ok {
		t.Fatal("deleted customer still there")
	}
}

func TestPlansAndSubscriptions(t *testing.T) {
	s := New()
	a := newOrg(t, s, "A")
	c, _ := s.CreateCustomer(a.ID, "Jane", "jane@example.com", t0)
	if _, err := s.CreatePlan(Plan{OrgID: a.ID, Name: "Pro", Currency: "XYZ", UnitAmount: 100, Interval: Monthly}, t0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad currency: %v", err)
	}
	p, err := s.CreatePlan(Plan{OrgID: a.ID, Name: "Pro", Currency: money.USD, UnitAmount: 2000, Interval: Monthly}, t0)
	if err != nil || !p.Active || p.UsageType != Licensed {
		t.Fatalf("CreatePlan = %+v, %v", p, err)
	}
	if up, err := s.UpdatePlan(a.ID, p.ID, func(p *Plan) error { p.UnitAmount = 1; p.Active = false; return nil }); err != nil || up.UnitAmount != 2000 || up.Active {
		t.Fatalf("UpdatePlan = %+v, %v (the price must not change)", up, err)
	}
	sub, err := s.CreateSubscription(Subscription{OrgID: a.ID, CustomerID: c.ID, PlanID: p.ID, Quantity: 2, Status: Active,
		StartedAt: t0, CurrentPeriodStart: t0, CurrentPeriodEnd: t0.AddDate(0, 1, 0)}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.DueSubscriptions(t0.AddDate(0, 1, 0).Add(-time.Second)); len(got) != 0 {
		t.Fatalf("due too early: %+v", got)
	}
	if got := s.DueSubscriptions(t0.AddDate(0, 1, 0)); len(got) != 1 || got[0].ID != sub.ID {
		t.Fatalf("DueSubscriptions = %+v", got)
	}
	if _, err := s.UpdateSubscription(a.ID, sub.ID, func(x *Subscription) error { x.Quantity = 0; return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("quantity 0: %v", err)
	}
}

func TestInvoicesAndPayments(t *testing.T) {
	s := New()
	a := newOrg(t, s, "A")
	_, _ = s.UpdateOrg(a.ID, func(o *Org) error { o.TaxRateBP = 2000; return nil })
	c, _ := s.CreateCustomer(a.ID, "Jane", "jane@example.com", t0)
	inv, err := s.CreateInvoice(Invoice{OrgID: a.ID, CustomerID: c.ID, Currency: money.EUR, Lines: []Line{{Description: "x", Quantity: 1, UnitAmount: 100}}, TaxRateBP: 5}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if inv.TaxRateBP != 2000 || inv.Status != InvoiceOpen {
		t.Fatalf("invoice = %+v (the org's tax rate applies)", inv)
	}
	inv.Lines[0].UnitAmount = 1
	if got, _ := s.Invoice(a.ID, inv.ID); got.Lines[0].UnitAmount != 100 {
		t.Fatal("a returned invoice shares its lines with the store")
	}
	later := t0.Add(time.Minute)
	inv2, _ := s.CreateInvoice(Invoice{OrgID: a.ID, CustomerID: c.ID, Currency: money.EUR, Lines: []Line{{Description: "y", Quantity: 2, UnitAmount: 50}}}, later)
	if got := s.ListInvoices(a.ID, InvoiceFilter{}); len(got) != 2 || got[0].ID != inv.ID || got[1].ID != inv2.ID {
		t.Fatalf("ListInvoices = %+v", got)
	}
	if _, err := s.UpdateInvoice(a.ID, inv.ID, func(i *Invoice) error { i.Status = InvoicePaid; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := s.ListInvoices(a.ID, InvoiceFilter{Status: InvoiceOpen}); len(got) != 1 || got[0].ID != inv2.ID {
		t.Fatalf("open invoices = %+v", got)
	}
	p, err := s.CreatePayment(Payment{OrgID: a.ID, InvoiceID: inv.ID, Amount: 120, Currency: money.EUR, Status: PaymentSucceeded}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Payments(a.ID, inv.ID); len(got) != 1 || got[0].ID != p.ID {
		t.Fatalf("Payments = %+v", got)
	}
}

func TestExportAndFromState(t *testing.T) {
	s := New()
	a := newOrg(t, s, "A")
	u, _ := s.CreateUser("ann@example.com", "Ann", t0)
	_, _ = s.AddMember(a.ID, u.ID, RoleOwner, t0)
	k, _ := s.CreateAPIKey(a.ID, u.ID, "k", t0)
	c, _ := s.CreateCustomer(a.ID, "Jane", "jane@example.com", t0)
	_, _ = s.CreateInvoice(Invoice{OrgID: a.ID, CustomerID: c.ID, Currency: money.USD, Lines: []Line{{Description: "x", Quantity: 1, UnitAmount: 100}}}, t0)
	st := s.Export()
	s2, err := FromState(st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s2.Export(), st) {
		t.Fatalf("round trip changed the state:\n%+v\n%+v", s2.Export(), st)
	}
	if got, ok := s2.APIKeyByToken(k.Token); !ok || got.ID != k.ID {
		t.Fatal("the key does not work after FromState")
	}
	next, _ := s2.CreateCustomer(a.ID, "John", "john@example.com", t0)
	if next.ID == c.ID {
		t.Fatal("IDs restart after FromState")
	}
	st.Customers = append(st.Customers, st.Customers[0])
	if _, err := FromState(st); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a repeated ID was accepted: %v", err)
	}
}

package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

func TestSaveAndLoad(t *testing.T) {
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	st := store.New()
	o, _ := st.CreateOrg("A", store.OrgTeam, "Europe/Berlin", 1900, now)
	u, _ := st.CreateUser("ann@example.com", "Ann", now)
	_, _ = st.AddMember(o.ID, u.ID, store.RoleOwner, now)
	k, _ := st.CreateAPIKey(o.ID, u.ID, "k", now)
	c, _ := st.CreateCustomer(o.ID, "Jane", "jane@example.com", now)
	_, _ = st.CreateInvoice(store.Invoice{OrgID: o.ID, CustomerID: c.ID, Currency: money.EUR, Lines: []store.Line{{Description: "x", Quantity: 2, UnitAmount: 150}}}, now)

	path := filepath.Join(t.TempDir(), "tally.json")
	if err := Save(st, path, now); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Export(), st.Export()) {
		t.Fatal("the loaded store differs from the saved one")
	}
	if _, ok := got.APIKeyByToken(k.Token); !ok {
		t.Fatal("API keys do not work after a reload")
	}
}

func TestLoadVersion1(t *testing.T) {
	st, err := Load(filepath.Join("testdata", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := st.Org("org_000001")
	if !ok || o.Plan != store.OrgFree || o.TimeZone != "UTC" || o.TaxRateBP != 825 {
		t.Fatalf("org = %+v", o)
	}
	c, ok := st.Customer(o.ID, "cus_000004")
	if !ok || c.Name != "Jane Doe" || c.Email != "jane@example.com" {
		t.Fatalf("customer = %+v", c)
	}
	p, ok := st.Plan(o.ID, "plan_000005")
	if !ok || p.Currency != money.USD || p.UnitAmount != 1999 || p.UsageType != store.Licensed || !p.Active {
		t.Fatalf("plan = %+v", p)
	}
	if k, ok := st.APIKeyByToken("tly_00000000000000000000000000000003"); !ok || k.UserID != "usr_000002" {
		t.Fatalf("key = %+v", k)
	}
	inv, ok := st.Invoice(o.ID, "in_000007")
	if !ok || inv.Lines[0].UnitAmount != 1999 || inv.Status != store.InvoicePaid {
		t.Fatalf("invoice = %+v", inv)
	}
	next, err := st.CreateCustomer(o.ID, "New", "new@example.com", time.Now())
	if err != nil || next.ID == "cus_000004" || next.ID <= "cus_000008" {
		t.Fatalf("the next ID %q reuses an old one: %v", next.ID, err)
	}
}

func TestLoadRefusesUnknownVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.json")
	if err := os.WriteFile(path, []byte(`{"version": 9, "state": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

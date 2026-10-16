package snapshot_test

// Hidden acceptance test of the trial task tally-snapshot-v3 (evals/tasks). The agents never see it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/tally/internal/api"
	"example.com/tally/internal/apitest"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/money"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/snapshot"
	"example.com/tally/internal/store"
	"example.com/tally/internal/subscriptions"
	"example.com/tally/internal/webhooks"
)

// hiddenSnapFixture is one of the old snapshot files of this test, and what loading it must give.
type hiddenSnapFixture struct {
	name     string // file under testdata/hidden_snapshot_v3
	version  int
	acmeKey  string
	acmeCus  string
	globex   string // Globex's API key
	globeCus string
	// invoice ID -> number, per organization in creation order (created_at, then ID)
	acme, globexNumbers  map[string]string
	acmeNext, globexNext string
}

var hiddenSnapV1 = hiddenSnapFixture{
	name: "v1.json", version: 1,
	acmeKey: "tly_a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", acmeCus: "cus_000007",
	globex: "tly_b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", globeCus: "cus_000008",
	// the file lists the invoices in no particular order; in_000011 and in_000015 were created at the same time, as were
	// in_000012 and in_000014
	acme:          map[string]string{"in_000010": "INV-000001", "in_000009": "INV-000002", "in_000011": "INV-000003", "in_000015": "INV-000004"},
	globexNumbers: map[string]string{"in_000012": "INV-000001", "in_000014": "INV-000002", "in_000013": "INV-000003"},
	acmeNext:      "INV-000005", globexNext: "INV-000004",
}

var hiddenSnapV2 = hiddenSnapFixture{
	name: "v2.json", version: 2,
	acmeKey: "tly_a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2", acmeCus: "cus_000007",
	globex: "tly_b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", globeCus: "cus_000008",
	// in_000010, in_000012 and in_000016 were created at the same time, as were in_000013 and in_000017
	acme:          map[string]string{"in_000014": "INV-000001", "in_000011": "INV-000002", "in_000010": "INV-000003", "in_000012": "INV-000004", "in_000016": "INV-000005"},
	globexNumbers: map[string]string{"in_000015": "INV-000001", "in_000013": "INV-000002", "in_000017": "INV-000003"},
	acmeNext:      "INV-000006", globexNext: "INV-000004",
}

// hiddenSnapCopy copies one of this test's old snapshot files to a new folder and returns its path there and its bytes.
func hiddenSnapCopy(t *testing.T, name string) (string, []byte) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hidden_snapshot_v3", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tally.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, b
}

type hiddenSnapAPI func(method, path, key string, body any) *apitest.Response

// hiddenSnapServe runs the API over a store (a loaded snapshot).
func hiddenSnapServe(t *testing.T, st *store.Store) hiddenSnapAPI {
	clk := clock.NewFake(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	hooks := webhooks.New(webhooks.Options{Store: st, Clock: clk, Doer: &apitest.FakeHTTP{}, Resolver: &apitest.FakeDNS{}})
	pay := &payments.Service{Store: st, Gateway: &payments.Fake{}, Clock: clk, Publisher: hooks}
	subs := &subscriptions.Service{Store: st, Payments: pay, Clock: clk, Publisher: hooks}
	srv := api.New(st, api.Options{Clock: clk, Payments: pay, Subscriptions: subs, Webhooks: hooks, Mail: &mail.Outbox{}})
	return func(method, path, key string, body any) *apitest.Response {
		var rd io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		clk.Advance(time.Second)
		return &apitest.Response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
}

type hiddenSnapInvoice struct {
	ID     string `json:"id"`
	Number string `json:"number"`
}

// hiddenSnapNumbers lists an organization's invoices through the API: invoice ID -> number.
func hiddenSnapNumbers(t *testing.T, do hiddenSnapAPI, key string) map[string]string {
	t.Helper()
	out := map[string]string{}
	cursor := ""
	for range 100 {
		path := "/v1/invoices?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := do("GET", path, key, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("GET %s: want 200, got %s", path, r)
		}
		var page struct {
			Items      []hiddenSnapInvoice `json:"items"`
			NextCursor string              `json:"next_cursor"`
		}
		r.JSON(t, &page)
		for _, it := range page.Items {
			out[it.ID] = it.Number
		}
		if cursor = page.NextCursor; cursor == "" {
			return out
		}
	}
	t.Fatal("GET /v1/invoices: more than 100 pages")
	return nil
}

// hiddenSnapCreate creates an invoice through the API and returns its number.
func hiddenSnapCreate(t *testing.T, do hiddenSnapAPI, key, customerID string) string {
	t.Helper()
	r := do("POST", "/v1/invoices", key, map[string]any{"customer_id": customerID, "currency": "USD",
		"lines": []map[string]any{{"description": "New work", "quantity": 1, "unit_amount": 700}}})
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /v1/invoices: want 201, got %s", r)
	}
	var inv hiddenSnapInvoice
	r.JSON(t, &inv)
	return inv.Number
}

func hiddenSnapSame(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: invoices and numbers %v, want %v", what, got, want)
	}
	for id, n := range want {
		if got[id] != n {
			t.Fatalf("%s: invoices and numbers %v, want %v (invoice %s: %q, want %q)", what, got, want, id, got[id], n)
		}
	}
}

// hiddenSnapCheckOld checks a store loaded from one of the old files: numbers per organization in creation order, and new
// invoices continue after the highest number.
func hiddenSnapCheckOld(t *testing.T, what string, st *store.Store, f hiddenSnapFixture) {
	t.Helper()
	do := hiddenSnapServe(t, st)
	hiddenSnapSame(t, what+": Acme", hiddenSnapNumbers(t, do, f.acmeKey), f.acme)
	hiddenSnapSame(t, what+": Globex", hiddenSnapNumbers(t, do, f.globex), f.globexNumbers)
	for id, n := range f.acme {
		r := do("GET", "/v1/invoices/"+id, f.acmeKey, nil)
		var inv hiddenSnapInvoice
		if r.Code == http.StatusOK {
			r.JSON(t, &inv)
		}
		if r.Code != http.StatusOK || inv.Number != n {
			t.Fatalf("%s: GET /v1/invoices/%s: want 200 with number %q, got %s", what, id, n, r)
		}
	}
	if got := hiddenSnapCreate(t, do, f.acmeKey, f.acmeCus); got != f.acmeNext {
		t.Fatalf("%s: Acme's next invoice has the number %q, want %q", what, got, f.acmeNext)
	}
	if got := hiddenSnapCreate(t, do, f.globex, f.globeCus); got != f.globexNext {
		t.Fatalf("%s: Globex's next invoice has the number %q, want %q", what, got, f.globexNext)
	}
}

// hiddenSnapVersion is the version a snapshot file says it is.
func hiddenSnapVersion(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil || head.Version == nil {
		t.Fatalf("%s is not a snapshot with a version (%v): %.300s", path, err, b)
	}
	return *head.Version
}

// hiddenSnapFiles lists the names in a folder.
func hiddenSnapFiles(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// hiddenSnapIdentity is the identity of the file at path now, for os.SameFile. It is read through a handle, which is
// closed again: on Windows a FileInfo from os.Stat only looks its file up when os.SameFile compares it (by then the path
// may name another file), while File.Stat reads the identity at once; and Windows cannot rename over an open file.
func hiddenSnapIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func hiddenSnapOnly(t *testing.T, what, dir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := hiddenSnapFiles(t, dir); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s: the folder holds %q, want only %q (no temporary file may stay behind)", what, got, want)
	}
}

func TestHiddenSnapshotLoadsOldVersionsWithNumbers(t *testing.T) {
	for _, f := range []hiddenSnapFixture{hiddenSnapV1, hiddenSnapV2} {
		path, _ := hiddenSnapCopy(t, f.name)
		st, err := snapshot.Load(path)
		if err != nil {
			t.Fatalf("Load(version %d): %v", f.version, err)
		}
		hiddenSnapCheckOld(t, "Load(version "+f.name+")", st, f)
	}
	// the rest of a version 1 file is migrated as before
	path, _ := hiddenSnapCopy(t, "v1.json")
	st, err := snapshot.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := st.Org("org_000001"); !ok || o.Plan != store.OrgFree || o.TimeZone != "UTC" || o.TaxRateBP != 825 {
		t.Fatalf("version 1: org = %+v", o)
	}
	if c, ok := st.Customer("org_000002", "cus_000008"); !ok || c.Name != "Hans Muster" {
		t.Fatalf("version 1: customer = %+v", c)
	}
	if inv, ok := st.Invoice("org_000001", "in_000011"); !ok || inv.Status != store.InvoiceVoid || inv.DiscountPercent != 10 || len(inv.Lines) != 1 {
		t.Fatalf("version 1: invoice = %+v", inv)
	}
}

func TestHiddenSnapshotSaveWritesVersion3AndKeepsNumbers(t *testing.T) {
	e := apitest.New(t)
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	gus := e.CreateCustomer(e.Globex, "Gus", "gus@example.com")
	add := func(org apitest.Org, customer string, at time.Time) {
		t.Helper()
		if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: org.ID, CustomerID: customer, Currency: money.USD,
			Lines: []store.Line{{Description: "Work", Quantity: 1, UnitAmount: 1000}}}, at); err != nil {
			t.Fatal(err)
		}
	}
	t0 := apitest.Start
	add(e.Acme, jane.ID, t0)
	add(e.Globex, gus.ID, t0)
	add(e.Acme, jane.ID, t0.Add(time.Minute))
	add(e.Acme, jane.ID, t0.Add(-time.Hour)) // created third, though its time is the earliest: its number is INV-000003
	add(e.Globex, gus.ID, t0.Add(time.Minute))
	add(e.Acme, jane.ID, t0.Add(time.Minute)) // the same time as the second

	acme := hiddenSnapNumbers(t, hiddenSnapServe(t, e.Store), e.Acme.Key)
	globex := hiddenSnapNumbers(t, hiddenSnapServe(t, e.Store), e.Globex.Key)
	var acmeNumbers []string
	for _, n := range acme {
		acmeNumbers = append(acmeNumbers, n)
	}
	sort.Strings(acmeNumbers)
	if strings.Join(acmeNumbers, ",") != "INV-000001,INV-000002,INV-000003,INV-000004" {
		t.Fatalf("Acme's invoices have the numbers %v before any snapshot, want INV-000001 to INV-000004", acme)
	}

	path := filepath.Join(t.TempDir(), "tally.json")
	if err := snapshot.Save(e.Store, path, t0); err != nil {
		t.Fatal(err)
	}
	if v := hiddenSnapVersion(t, path); v != 3 {
		t.Fatalf("Save wrote version %d, want 3", v)
	}
	st, err := snapshot.Load(path)
	if err != nil {
		t.Fatalf("Load of a file Save wrote: %v", err)
	}
	hiddenSnapSame(t, "after Save and Load: Acme", hiddenSnapNumbers(t, hiddenSnapServe(t, st), e.Acme.Key), acme)
	// the loaded store is saved and loaded once more
	if err := snapshot.Save(st, path, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if v := hiddenSnapVersion(t, path); v != 3 {
		t.Fatalf("Save wrote version %d, want 3", v)
	}
	if st, err = snapshot.Load(path); err != nil {
		t.Fatalf("Load of a file Save wrote: %v", err)
	}
	do := hiddenSnapServe(t, st)
	hiddenSnapSame(t, "after two Saves and Loads: Acme", hiddenSnapNumbers(t, do, e.Acme.Key), acme)
	hiddenSnapSame(t, "after two Saves and Loads: Globex", hiddenSnapNumbers(t, do, e.Globex.Key), globex)
	if got := hiddenSnapCreate(t, do, e.Acme.Key, jane.ID); got != "INV-000005" {
		t.Fatalf("after Save and Load: Acme's next invoice has the number %q, want INV-000005", got)
	}
	if got := hiddenSnapCreate(t, do, e.Globex.Key, gus.ID); got != "INV-000003" {
		t.Fatalf("after Save and Load: Globex's next invoice has the number %q, want INV-000003", got)
	}
}

func TestHiddenSnapshotUpgrade(t *testing.T) {
	for _, f := range []hiddenSnapFixture{hiddenSnapV1, hiddenSnapV2} {
		path, original := hiddenSnapCopy(t, f.name)
		dir := filepath.Dir(path)
		if err := snapshot.Upgrade(path); err != nil {
			t.Fatalf("Upgrade(version %d): %v", f.version, err)
		}
		backup := path + ".v" + strconv.Itoa(f.version) + ".bak"
		b, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("Upgrade(version %d): no backup %s: %v (the folder holds %q)", f.version, filepath.Base(backup), err, hiddenSnapFiles(t, dir))
		}
		if !bytes.Equal(b, original) {
			t.Fatalf("Upgrade(version %d): the backup is not the original byte for byte", f.version)
		}
		if v := hiddenSnapVersion(t, path); v != 3 {
			t.Fatalf("Upgrade(version %d) left version %d, want 3", f.version, v)
		}
		hiddenSnapOnly(t, "after Upgrade", dir, "tally.json", filepath.Base(backup))
		upgraded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		st, err := snapshot.Load(path)
		if err != nil {
			t.Fatalf("Load after Upgrade(version %d): %v", f.version, err)
		}
		direct, err := snapshot.Load(filepath.Join("testdata", "hidden_snapshot_v3", f.name))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := st.Export(), direct.Export(); !hiddenSnapEqualState(got, want) {
			t.Fatalf("Upgrade(version %d) changed the data: loading the upgraded file gives a store that differs from loading the original", f.version)
		}
		hiddenSnapCheckOld(t, "Load after Upgrade(version "+f.name+")", st, f)

		// a second Upgrade finds version 3 and leaves the file alone, without a backup
		before := hiddenSnapIdentity(t, path)
		if err := snapshot.Upgrade(path); err != nil {
			t.Fatalf("Upgrade of the upgraded file: %v", err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); !bytes.Equal(b, upgraded) || !os.SameFile(before, after) {
			t.Fatal("Upgrade of a version 3 file rewrote it")
		}
		hiddenSnapOnly(t, "after a second Upgrade", dir, "tally.json", filepath.Base(backup))
	}

	// a file Save wrote is left alone, without a backup
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Acme.ID, CustomerID: c.ID, Currency: money.USD,
		Lines: []store.Line{{Description: "Work", Quantity: 1, UnitAmount: 1000}}}, apitest.Start); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tally.json")
	if err := snapshot.Save(e.Store, path, apitest.Start); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	before := hiddenSnapIdentity(t, path)
	if err := snapshot.Upgrade(path); err != nil {
		t.Fatalf("Upgrade of a version 3 file: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, saved) || !os.SameFile(before, after) {
		t.Fatal("Upgrade of a version 3 file changed it")
	}
	hiddenSnapOnly(t, "after Upgrade of a version 3 file", dir, "tally.json")
}

// hiddenSnapEqualState compares two states through JSON (both come from Export).
func hiddenSnapEqualState(a, b store.State) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

func TestHiddenSnapshotRefusesNewerVersions(t *testing.T) {
	for v, content := range map[int][]byte{
		4:  []byte(`{"version": 4, "saved_at": "2027-01-01T00:00:00Z", "state": {"seq": 1, "orgs": []}, "next": {"hello": true}}` + "\n"),
		99: []byte(`{"version": 99, "saved_at": "2030-01-01T00:00:00Z", "data": {"format": "unknown"}}` + "\n"),
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "tally.json")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		before := hiddenSnapIdentity(t, path)
		if st, err := snapshot.Load(path); !errors.Is(err, snapshot.ErrUnsupportedVersion) {
			t.Fatalf("Load(version %d) = %v, %v; want an error matching ErrUnsupportedVersion", v, st != nil, err)
		}
		if err := snapshot.Upgrade(path); !errors.Is(err, snapshot.ErrUnsupportedVersion) {
			t.Fatalf("Upgrade(version %d) = %v, want an error matching ErrUnsupportedVersion", v, err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); !bytes.Equal(b, content) || !os.SameFile(before, after) {
			t.Fatalf("Upgrade(version %d) touched the file: %s", v, b)
		}
		hiddenSnapOnly(t, "after Upgrade of a newer version", dir, "tally.json")
	}
}

func TestHiddenSnapshotSaveIsAtomic(t *testing.T) {
	e := apitest.New(t)
	c := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	dir := t.TempDir()
	path := filepath.Join(dir, "tally.json")
	if err := snapshot.Save(e.Store, path, apitest.Start); err != nil {
		t.Fatal(err)
	}
	hiddenSnapOnly(t, "after the first Save", dir, "tally.json")

	for i := range 5 {
		old, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before := hiddenSnapIdentity(t, path)
		var held *os.File
		if runtime.GOOS != "windows" { // Windows cannot rename over an open file
			// the old file stays open while it is replaced: what it holds must stay whole
			if held, err = os.Open(path); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.Store.CreateInvoice(store.Invoice{OrgID: e.Acme.ID, CustomerID: c.ID, Currency: money.USD,
			Lines: []store.Line{{Description: "Work", Quantity: int64(i + 1), UnitAmount: 1000}}}, apitest.Start); err != nil {
			t.Fatal(err)
		}
		err = snapshot.Save(e.Store, path, apitest.Start.Add(time.Duration(i+1)*time.Minute))
		var kept []byte
		if held != nil {
			kept, _ = io.ReadAll(held)
			_ = held.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(before, after) {
			t.Fatalf("Save %d wrote over the old file in place; it must write a temporary file and rename it over the old one", i+2)
		}
		if held != nil && !bytes.Equal(kept, old) {
			t.Fatalf("Save %d changed the old snapshot's file instead of replacing it", i+2)
		}
		hiddenSnapOnly(t, "after a Save over an old snapshot", dir, "tally.json")
		st, err := snapshot.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(st.ListInvoices(e.Acme.ID, store.InvoiceFilter{})); n != i+1 {
			t.Fatalf("the snapshot holds %d invoices, want %d", n, i+1)
		}
	}

	// a Save that cannot replace the file fails and leaves nothing behind
	blocked := filepath.Join(dir, "blocked.json")
	if err := os.MkdirAll(filepath.Join(blocked, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Save(e.Store, blocked, apitest.Start); err == nil {
		t.Fatal("Save over a folder succeeded")
	}
	hiddenSnapOnly(t, "after a Save that failed", dir, "tally.json", "blocked.json")
}

var hiddenSnapNumberRe = regexp.MustCompile(`^INV-[0-9]{6,}$`)

func TestHiddenSnapshotNumbersLookRight(t *testing.T) {
	path, _ := hiddenSnapCopy(t, "v2.json")
	st, err := snapshot.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for id, n := range hiddenSnapNumbers(t, hiddenSnapServe(t, st), hiddenSnapV2.acmeKey) {
		if !hiddenSnapNumberRe.MatchString(n) {
			t.Fatalf("invoice %s has the number %q", id, n)
		}
	}
}

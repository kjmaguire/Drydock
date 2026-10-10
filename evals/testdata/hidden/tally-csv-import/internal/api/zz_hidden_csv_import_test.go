package api_test

// Hidden acceptance test of the trial task tally-csv-import (evals/tasks). The agents never see it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"example.com/tally/internal/apitest"
)

// hiddenBOM is a UTF-8 byte order mark.
const hiddenBOM = "\xEF\xBB\xBF"

func hiddenImport(e *apitest.Env, key, query, body string) *apitest.Response {
	path := "/v1/customers/import"
	if query != "" {
		path += "?" + query
	}
	return e.DoWith("POST", path, key, body, http.Header{"Content-Type": {"text/csv"}})
}

type hiddenImported struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Email                string            `json:"email"`
	DefaultPaymentMethod string            `json:"default_payment_method"`
	Metadata             map[string]string `json:"metadata"`
}

// hiddenSameCustomer compares everything but the ID; a line break inside a field may come back as CRLF or LF.
func hiddenSameCustomer(got, want hiddenImported) bool {
	norm := func(c hiddenImported) hiddenImported {
		c.ID = ""
		c.Name = strings.ReplaceAll(c.Name, "\r\n", "\n")
		if len(c.Metadata) == 0 {
			c.Metadata = nil
		}
		return c
	}
	return reflect.DeepEqual(norm(got), norm(want))
}

// hiddenAllCustomers returns every customer GET /v1/customers lists, oldest first.
func hiddenAllCustomers(t *testing.T, e *apitest.Env, key string) []hiddenImported {
	t.Helper()
	out := []hiddenImported{}
	cursor := ""
	for range 1000 {
		path := "/v1/customers?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items      []hiddenImported `json:"items"`
			NextCursor string           `json:"next_cursor"`
		}
		e.MustDo(http.StatusOK, "GET", path, key, nil, &page)
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
	t.Fatal("GET /v1/customers: the pages never end")
	return nil
}

// hiddenCreatedEvents returns the data of every customer.created event of the organization.
func hiddenCreatedEvents(t *testing.T, e *apitest.Env, key string) []string {
	t.Helper()
	out := []string{}
	cursor := ""
	for range 1000 {
		path := "/v1/events?type=customer.created&limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items []struct {
				Data json.RawMessage `json:"data"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		e.MustDo(http.StatusOK, "GET", path, key, nil, &page)
		for _, ev := range page.Items {
			out = append(out, string(ev.Data))
		}
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
	t.Fatal("GET /v1/events: the pages never end")
	return nil
}

func hiddenWantImportError(t *testing.T, what string, r *apitest.Response, status int, code string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if r.Code != status || json.Unmarshal(r.Body, &body) != nil || body.Error != code {
		t.Fatalf("%s: want %d {\"error\":%q}, got %s", what, status, code, r)
	}
}

type hiddenRowProblem struct {
	Row   int    `json:"row"`
	Error string `json:"error"`
}

func hiddenWantRows(t *testing.T, what string, r *apitest.Response, want []hiddenRowProblem) {
	t.Helper()
	var body struct {
		Error string             `json:"error"`
		Rows  []hiddenRowProblem `json:"rows"`
	}
	if r.Code != http.StatusUnprocessableEntity || json.Unmarshal(r.Body, &body) != nil || body.Error != "invalid_rows" || !reflect.DeepEqual(body.Rows, want) {
		w, _ := json.Marshal(want)
		t.Fatalf("%s: want 422 {\"error\":\"invalid_rows\",\"rows\":%s}, got %s", what, w, r)
	}
}

// hiddenWantNothingCreated checks that the organization still has exactly the customers ids and sent no other
// customer.created event than the events it had.
func hiddenWantNothingCreated(t *testing.T, e *apitest.Env, key, what string, ids []string, events int) {
	t.Helper()
	got := []string{}
	for _, c := range hiddenAllCustomers(t, e, key) {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("%s: the organization's customers are %v, want %v: nothing may be created", what, got, ids)
	}
	if n := len(hiddenCreatedEvents(t, e, key)); n != events {
		t.Fatalf("%s: %d customer.created events, want %d: nothing may be created", what, n, events)
	}
}

// hiddenGoodFile has a header in another case and order, with surrounding spaces, an ignored column and two metadata
// columns, then four customers with quoted fields (a comma, quotes, a line break) and blank lines.
func hiddenGoodFile(nl string) string {
	return strings.Join([]string{
		"Email , NAME ,Default_Payment_Method,phone,METADATA.crm, metadata.tier ",
		"jane@example.com,Jane Doe,pm_card_visa,555-1234,C-1,gold",
		`"doe.john@example.com","Doe, John",,555,C-2,silver`,
		"",
		`q@example.com,"The ""Best"" Co",pm_x,,C-3,bronze`,
		`multi@example.com,"Line one` + nl + `Line two",,,C-4,gold`,
		"",
		"",
	}, nl)
}

var hiddenGoodWant = []hiddenImported{
	{Name: "Jane Doe", Email: "jane@example.com", DefaultPaymentMethod: "pm_card_visa", Metadata: map[string]string{"crm": "C-1", "tier": "gold"}},
	{Name: "Doe, John", Email: "doe.john@example.com", Metadata: map[string]string{"crm": "C-2", "tier": "silver"}},
	{Name: `The "Best" Co`, Email: "q@example.com", DefaultPaymentMethod: "pm_x", Metadata: map[string]string{"crm": "C-3", "tier": "bronze"}},
	{Name: "Line one\nLine two", Email: "multi@example.com", Metadata: map[string]string{"crm": "C-4", "tier": "gold"}},
}

func TestHiddenImportCreatesTheCustomersInFileOrder(t *testing.T) {
	for _, v := range []struct{ name, body string }{
		{"LF line ends", hiddenGoodFile("\n")},
		{"CRLF line ends and a byte order mark", hiddenBOM + hiddenGoodFile("\r\n")},
	} {
		t.Run(v.name, func(t *testing.T) {
			e := apitest.New(t)
			key := e.Acme.Key
			existing := e.CreateCustomer(e.Acme, "Existing", "existing@example.com")
			r := hiddenImport(e, key, "", v.body)
			if r.Code != http.StatusCreated {
				t.Fatalf("import of a valid file (%s): want 201, got %s\nfile: %q", v.name, r, v.body)
			}
			var out struct {
				Created *int             `json:"created"`
				Items   []hiddenImported `json:"items"`
			}
			r.JSON(t, &out)
			if out.Created == nil || *out.Created != len(hiddenGoodWant) || len(out.Items) != len(hiddenGoodWant) {
				t.Fatalf("import of 4 customers: want {\"created\": 4, \"items\": [4 customers]}, got %s", r)
			}
			for i, want := range hiddenGoodWant {
				if got := out.Items[i]; got.ID == "" || !hiddenSameCustomer(got, want) {
					t.Fatalf("import: item %d is %+v, want %+v (in file order)", i, got, want)
				}
			}
			all := hiddenAllCustomers(t, e, key)
			if len(all) != 1+len(hiddenGoodWant) || all[0].ID != existing.ID {
				t.Fatalf("after the import GET /v1/customers lists %+v, want the existing customer and the 4 imported ones", all)
			}
			for i, want := range hiddenGoodWant {
				if got := all[i+1]; got.ID != out.Items[i].ID || !hiddenSameCustomer(got, want) {
					t.Fatalf("after the import customer %d of GET /v1/customers is %+v, want %+v (created in file order)", i+1, got, want)
				}
			}
			events := hiddenCreatedEvents(t, e, key)
			for _, it := range out.Items {
				sent := false
				for _, data := range events {
					if strings.Contains(data, it.ID) {
						sent = true
					}
				}
				if !sent {
					t.Fatalf("no customer.created event for the imported customer %s", it.ID)
				}
			}
			if others := hiddenAllCustomers(t, e, e.Globex.Key); len(others) != 0 {
				t.Fatalf("Acme's import created customers of Globex: %+v", others)
			}
		})
	}
}

// hiddenBadFile has a bad record of every kind, records with several problems, and good records, with the line where each
// record starts: blank lines and a field with a line break count.
func hiddenBadFile(nl string) string {
	return strings.Join([]string{
		"name,email,default_payment_method,metadata.crm", // 1
		"Ann,ann@example.com,,A1",                        // 2 fine
		",nobody@example.com,,N1",                        // 3 missing_name
		"",                                               // 4
		"Bob,not-an-email,,B1",                           // 5 invalid_email
		`"Multi`,                                         // 6 starts here
		`Line",multi@example.com,card_123,M1`,            // 7 (row 6) invalid_payment_method
		"Jane Again,JANE@Example.com,,J1",                // 8 email_taken (an existing customer)
		"Ann Two,ANN@EXAMPLE.COM,,A2",                    // 9 duplicate_email (line 2)
		`"   ",spaces@example.com,card_1,S1`,             // 10 missing_name (blank), before invalid_payment_method
		",also-bad,card_2,X1",                            // 11 missing_name, before invalid_email
		"Carl,carl@,card_3,C1",                           // 12 invalid_email, before invalid_payment_method
		"Dave,jane@example.com,card_4,D1",                // 13 invalid_payment_method, before email_taken
		"Ann Three,ann@example.com,card_5,A3",            // 14 invalid_payment_method, before duplicate_email
		"Jane Three,jane@EXAMPLE.com,,J3",                // 15 email_taken, before duplicate_email
		`"Quote ""Q"" Co",q@example.com,pm_ok,Q1`,        // 16 fine
		"Eve,eve@example.com,pm_card_visa,E1",            // 17 fine
		"Eve Two,EVE@example.com,,E2",                    // 18 duplicate_email (line 17)
		"Fay,@example.com,,F1",                           // 19 invalid_email
		"",
	}, nl)
}

var hiddenBadWant = []hiddenRowProblem{
	{3, "missing_name"}, {5, "invalid_email"}, {6, "invalid_payment_method"}, {8, "email_taken"}, {9, "duplicate_email"},
	{10, "missing_name"}, {11, "missing_name"}, {12, "invalid_email"}, {13, "invalid_payment_method"},
	{14, "invalid_payment_method"}, {15, "email_taken"}, {18, "duplicate_email"}, {19, "invalid_email"},
}

func TestHiddenImportRejectsTheWholeFileForBadRecords(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	jane := e.CreateCustomer(e.Acme, "Jane Doe", "jane@example.com")
	events := len(hiddenCreatedEvents(t, e, key))
	for _, v := range []struct{ name, query, body string }{
		{"LF line ends", "", hiddenBadFile("\n")},
		{"CRLF line ends and a byte order mark", "", hiddenBOM + hiddenBadFile("\r\n")},
		{"a dry run", "dry_run=true", hiddenBadFile("\n")},
	} {
		hiddenWantRows(t, "import of a file with bad records ("+v.name+")", hiddenImport(e, key, v.query, v.body), hiddenBadWant)
		hiddenWantNothingCreated(t, e, key, "after an import with bad records ("+v.name+")", []string{jane.ID}, events)
	}
	body := "name,email\nAnn,ann@example.com\nBob,bob@example.com\nCarl,Jane@Example.com\nDee,dee@example.com\n"
	hiddenWantRows(t, "import of good records and one whose email address an existing customer has", hiddenImport(e, key, "", body),
		[]hiddenRowProblem{{4, "email_taken"}})
	hiddenWantNothingCreated(t, e, key, "after an import with one bad record among good ones", []string{jane.ID}, events)
}

func TestHiddenImportDryRun(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	body := "name,email\nAnn,ann@example.com\n\nBob,bob@example.com\nCid,cid@example.com\n"
	r := hiddenImport(e, key, "dry_run=true", body)
	var dry struct {
		Created     *int `json:"created"`
		WouldCreate *int `json:"would_create"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body, &dry) != nil || dry.Created == nil || *dry.Created != 0 || dry.WouldCreate == nil || *dry.WouldCreate != 3 {
		t.Fatalf("dry run of 3 good records: want 200 {\"created\": 0, \"would_create\": 3}, got %s", r)
	}
	hiddenWantNothingCreated(t, e, key, "after a dry run", []string{}, 0)

	r = hiddenImport(e, key, "", body)
	var out struct {
		Created *int             `json:"created"`
		Items   []hiddenImported `json:"items"`
	}
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body, &out) != nil || out.Created == nil || *out.Created != 3 || len(out.Items) != 3 {
		t.Fatalf("import after a dry run: want 201 {\"created\": 3, ...}, got %s", r)
	}
	hiddenWantRows(t, "dry run of records that now exist", hiddenImport(e, key, "dry_run=true", body),
		[]hiddenRowProblem{{2, "email_taken"}, {4, "email_taken"}, {5, "email_taken"}})
}

func TestHiddenImportNeedsAHeaderWithNameAndEmail(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	for _, body := range []string{
		"",
		hiddenBOM,
		"\n\n",
		"name,mail\nJane,jane@example.com\n",
		"email,full name\njane@example.com,Jane\n",
		"metadata.name,metadata.email\nJane,jane@example.com\n",
		"Jane Doe,jane@example.com\nJohn Roe,john@example.com\n",
	} {
		for _, q := range []string{"", "dry_run=true"} {
			hiddenWantImportError(t, fmt.Sprintf("import of %q (query %q)", body, q), hiddenImport(e, key, q, body), http.StatusUnprocessableEntity, "invalid_header")
		}
	}
	hiddenWantNothingCreated(t, e, key, "after imports without a usable header", []string{}, 0)
}

func TestHiddenImportRefusesInvalidCSV(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	for _, body := range []string{
		"name,email\nAnn,ann@example.com\n\"Jane,jane@example.com\nBob,bob@example.com\n", // a quoted field that never ends
		"name,email\nAnn,ann@example.com\n\"Jane\"x,jane@example.com\n",                   // text after the closing quote
		"name,email\nAnn,ann@example.com\nJa\"ne,jane@example.com\n",                      // a quote in a field that is not quoted
	} {
		for _, q := range []string{"", "dry_run=true"} {
			hiddenWantImportError(t, fmt.Sprintf("import of %q (query %q)", body, q), hiddenImport(e, key, q, body), http.StatusBadRequest, "invalid_csv")
		}
	}
	hiddenWantNothingCreated(t, e, key, "after imports of invalid CSV", []string{}, 0)
}

func TestHiddenImportRowLimit(t *testing.T) {
	e := apitest.New(t)
	key := e.Acme.Key
	file := func(n int) string {
		var b strings.Builder
		b.WriteString("name,email\n")
		for i := range n {
			if i%2500 == 0 {
				b.WriteString("\n") // blank lines are not records
			}
			fmt.Fprintf(&b, "Customer %05d,c%05d@example.com\n", i, i)
		}
		return b.String()
	}
	tooMany := file(10001)
	for _, q := range []string{"", "dry_run=true"} {
		hiddenWantImportError(t, fmt.Sprintf("import of 10,001 records (query %q)", q), hiddenImport(e, key, q, tooMany), http.StatusRequestEntityTooLarge, "too_many_rows")
	}
	hiddenWantNothingCreated(t, e, key, "after an import of too many records", []string{}, 0)

	most := file(10000)
	r := hiddenImport(e, key, "dry_run=true", most)
	var dry struct {
		Created     *int `json:"created"`
		WouldCreate *int `json:"would_create"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body, &dry) != nil || dry.Created == nil || *dry.Created != 0 || dry.WouldCreate == nil || *dry.WouldCreate != 10000 {
		t.Fatalf("dry run of 10,000 records (and blank lines): want 200 {\"created\": 0, \"would_create\": 10000}, got %.300s", r)
	}
	r = hiddenImport(e, key, "", most)
	var out struct {
		Created *int             `json:"created"`
		Items   []hiddenImported `json:"items"`
	}
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body, &out) != nil || out.Created == nil || *out.Created != 10000 || len(out.Items) != 10000 {
		t.Fatalf("import of 10,000 records (and blank lines): want 201 {\"created\": 10000, \"items\": [...]}, got %.300s", r)
	}
	if out.Items[0].Email != "c00000@example.com" || out.Items[9999].Email != "c09999@example.com" {
		t.Fatalf("import of 10,000 records: the first and last items are %+v and %+v", out.Items[0], out.Items[9999])
	}
	if n := len(hiddenAllCustomers(t, e, key)); n != 10000 {
		t.Fatalf("after importing 10,000 records GET /v1/customers lists %d customers", n)
	}
}

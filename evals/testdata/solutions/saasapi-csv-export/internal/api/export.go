package api

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"example.com/saasapi/internal/store"
)

// exportCustomers writes the account's customers as CSV (RFC 4180, CRLF), with fields that a spreadsheet would read as a
// formula prefixed by a single quote.
func (s *Server) exportCustomers(w http.ResponseWriter, r *http.Request, acct store.Account) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="customers.csv"`)
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	_ = cw.Write([]string{"id", "name", "email", "plan", "created_at"})
	for _, c := range s.store.Customers(acct.ID) {
		_ = cw.Write([]string{safe(c.ID), safe(c.Name), safe(c.Email), safe(string(c.Plan)), c.CreatedAt.UTC().Format(time.RFC3339)})
	}
	cw.Flush()
}

// safe neutralises a field a spreadsheet would run as a formula.
func safe(v string) string {
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

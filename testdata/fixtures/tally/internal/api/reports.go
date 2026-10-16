package api

import (
	"errors"
	"net/http"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/reports"
)

// revenueReport answers the money taken per day: ?from=YYYY-MM-DD&to=YYYY-MM-DD (both included).
func (s *Server) revenueReport(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	q := r.URL.Query()
	days, err := reports.Revenue(s.store, p.OrgID, q.Get("from"), q.Get("to"))
	switch {
	case errors.Is(err, reports.ErrInvalidDate):
		writeError(w, http.StatusBadRequest, "invalid_date", err.Error())
		return
	case errors.Is(err, reports.ErrInvalidRange):
		writeError(w, http.StatusBadRequest, "invalid_range", err.Error())
		return
	case err != nil:
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

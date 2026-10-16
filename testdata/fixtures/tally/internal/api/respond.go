package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"example.com/tally/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers {"error": code, "message": message}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

// decode reads a JSON body (at most 1 MiB) into v, or answers 400 and returns false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "the body is not valid JSON: "+err.Error())
		return false
	}
	return true
}

// storeError answers for an error from the store or a service.
func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such object")
	case errors.Is(err, store.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", err.Error())
	case errors.Is(err, store.ErrAlreadyMember):
		writeError(w, http.StatusConflict, "already_member", err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, store.ErrCustomerOrgMismatch):
		writeError(w, http.StatusUnprocessableEntity, "customer_org_mismatch", err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	default:
		s.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

// list is the shape of every list response. NextCursor is set when there is another page.
type list struct {
	Items      any    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// page is a request for one page of a list: ?limit= (1 to 100, default 20) and ?cursor= (from the previous page).
type page struct {
	limit int
	after *cursor
}

// cursor is where a page starts: after the item with this creation time and ID.
type cursor struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

func (c cursor) less(o cursor) bool {
	if !c.T.Equal(o.T) {
		return c.T.Before(o.T)
	}
	return c.ID < o.ID
}

// parsePage reads ?limit and ?cursor, or answers 400 and returns false.
func parsePage(w http.ResponseWriter, r *http.Request) (page, bool) {
	p := page{limit: 20}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit is a whole number from 1 to 100")
			return page{}, false
		}
		p.limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		b, err := base64.RawURLEncoding.DecodeString(v)
		var c cursor
		if err != nil || json.Unmarshal(b, &c) != nil || c.ID == "" {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "the cursor is not one this API gave out")
			return page{}, false
		}
		p.after = &c
	}
	return p, true
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// pageOf returns one page of items, which are sorted by key (creation time, then ID), and the cursor of the next page.
func pageOf[T any](items []T, key func(T) cursor, p page) ([]T, string) {
	start := 0
	if p.after != nil {
		start = len(items)
		for i, it := range items {
			if p.after.less(key(it)) {
				start = i
				break
			}
		}
	}
	end := min(start+p.limit, len(items))
	out := items[start:end]
	next := ""
	if end < len(items) && len(out) > 0 {
		next = encodeCursor(key(out[len(out)-1]))
	}
	return out, next
}

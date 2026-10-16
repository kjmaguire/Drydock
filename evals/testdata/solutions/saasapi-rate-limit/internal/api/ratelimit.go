package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"example.com/saasapi/internal/store"
)

const window = 60 * time.Second

// limits are the requests per window of each plan.
var limits = map[store.Plan]int{store.PlanFree: 60, store.PlanPro: 600}

// limiter keeps, per API key, the times of the requests it allowed in the last window.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// allow records a request at now and reports whether it is allowed; when it is not, it says how long until it would be.
func (l *limiter) allow(key string, limit int, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.hits[key]
	i := 0
	for i < len(hits) && !hits[i].After(now.Add(-window)) {
		i++
	}
	hits = hits[i:]
	if len(hits) >= limit {
		l.hits[key] = hits
		return false, hits[0].Add(window).Sub(now)
	}
	l.hits[key] = append(hits, now)
	return true, 0
}

// limited wraps an authenticated handler with the key's rate limit.
func (s *Server) limited(h handler) handler {
	return func(w http.ResponseWriter, r *http.Request, acct store.Account) {
		limit, ok := limits[acct.Plan]
		if !ok {
			limit = limits[store.PlanFree]
		}
		if allowed, wait := s.limiter.allow(r.Header.Get("X-API-Key"), limit, s.now()); !allowed {
			secs := int64((wait + time.Second - 1) / time.Second)
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		h(w, r, acct)
	}
}

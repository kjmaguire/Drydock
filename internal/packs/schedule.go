package packs

import (
	"context"
	"github.com/kjmaguire/drydock/internal/safe"
	"time"
)

// failureRetry is how soon a scheduled pack whose refresh failed is tried again (instead of waiting a whole interval).
const failureRetry = time.Hour

// Due returns the packs whose scheduled refresh is due at now: refresh.mode scheduled, not live, not running, and either
// never refreshed or past its interval (a failed refresh is retried after an hour).
func (s *Store) Due(ctx context.Context, now time.Time) ([]string, error) {
	packs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var due []string
	for _, p := range packs {
		m := p.Manifest
		if m == nil || p.Kind == KindLive || m.Refresh.Mode != "scheduled" || p.Status == "refreshing" {
			continue
		}
		wait := m.Refresh.Interval.D()
		if p.Status == "failed" && failureRetry < wait {
			wait = failureRetry
		}
		if p.LastRefreshAt.IsZero() || now.Sub(p.LastRefreshAt) >= wait {
			due = append(due, p.ID)
		}
	}
	return due, nil
}

// RunDue refreshes every due pack, one after the other, as "system". It returns the per-pack results; errors are kept per
// pack so one failing source does not stop the others.
func (s *Store) RunDue(ctx context.Context) (map[string]RefreshResult, map[string]error) {
	res := map[string]RefreshResult{}
	errs := map[string]error{}
	due, err := s.Due(ctx, s.now())
	if err != nil {
		errs[""] = err
		return res, errs
	}
	for _, id := range due {
		if ctx.Err() != nil {
			break
		}
		r, err := s.Refresh(ctx, "system", id, RefreshOptions{})
		if err != nil {
			errs[id] = err
			continue
		}
		res[id] = r
	}
	s.CheckStale(ctx)
	return res, errs
}

// CheckStale emits pack.stale once per pack and version when its last successful refresh is older than stale_after.
func (s *Store) CheckStale(ctx context.Context) []string {
	packs, err := s.List(ctx)
	if err != nil {
		return nil
	}
	var stale []string
	for _, p := range packs {
		if p.Freshness != "stale" {
			continue
		}
		stale = append(stale, p.ID)
		key := p.ID + "@" + p.Version
		if _, seen := s.staleSeen.LoadOrStore(key, true); !seen {
			s.emit(ctx, EventStale, map[string]any{"pack": p.ID, "version": p.Version, "ageDays": p.AgeDays, "refreshSuggested": true})
		}
	}
	return stale
}

// Start runs RunDue every interval while the app is open (there is no background service) and once right away. The returned
// function stops it and waits for a running refresh to finish.
func (s *Store) Start(ctx context.Context, every time.Duration) (stop func()) {
	if every <= 0 {
		every = 15 * time.Minute
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	safe.Go("packs.Store.Start", func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			_, errs := s.RunDue(ctx)
			for id, e := range errs {
				s.o.Logf("packs: scheduled refresh of %q: %v", id, e)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})
	return func() { cancel(); <-done }
}

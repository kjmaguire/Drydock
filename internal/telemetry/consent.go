package telemetry

// Usage counters, a diagnostics switch of their own (decision P121, docs/packaging.md section 9): a person's decision, stored
// with who made it and when. They count how often each feature (a method of the core the window called) was used: in memory,
// written to the local sink as one counter point per feature at each flush, and sent with everything else when OTLP export is
// on. Off by default; turning them off drops what was counted and not yet written. (P121's second switch here was removed by
// decision P126; a setting a person stored for it is ignored.)

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func human(by string) bool { return strings.HasPrefix(by, "human:") && len(by) > len("human:") }

func (s *Sink) consent(ctx context.Context, on bool, byKey, atKey string) Consent {
	c := Consent{Enabled: on}
	c.ChangedBy, _ = s.setting(ctx, byKey)
	if v, _ := s.setting(ctx, atKey); v != "" {
		_, _ = fmt.Sscan(v, &c.ChangedAt) // a malformed setting reads as "not recorded"
	}
	return c
}

func (s *Sink) putConsent(ctx context.Context, key, byKey, atKey string, on bool, by string) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for k, val := range map[string]string{key: v, byKey: by, atKey: fmt.Sprint(s.opt.Now().UnixMilli())} {
			if err := s.put(ctx, tx, k, val); err != nil {
				return err
			}
		}
		return nil
	})
}

// UsageCountersEnabled reports whether usage counters are counted.
func (s *Sink) UsageCountersEnabled() bool { return s.usageOn.Load() }

// SetUsageCounters turns the usage counters on or off. Only a person may. Turning them off drops what was counted and not yet
// written.
func (s *Sink) SetUsageCounters(ctx context.Context, on bool, by string) error {
	if !human(by) {
		return errors.New("telemetry: only a person may turn usage counters on or off")
	}
	if err := s.putConsent(ctx, keyUsage, keyUsageBy, keyUsageAt, on, by); err != nil {
		return err
	}
	s.usageOn.Store(on)
	if !on {
		s.usageMu.Lock()
		s.usage = map[string]int64{}
		s.usageMu.Unlock()
	}
	return nil
}

// MaxFeatures bounds how many different features are counted between two flushes (the names come from the core's own
// method table, so this is only a guard).
const MaxFeatures = 512

// CountUse counts one use of a feature while the usage counters are on. The name must be identifier-shaped (a method name such
// as task.create); anything else is not counted.
func (s *Sink) CountUse(feature string) {
	if !s.usageOn.Load() || !identValue.MatchString(feature) {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if _, ok := s.usage[feature]; !ok && len(s.usage) >= MaxFeatures {
		return
	}
	s.usage[feature]++
}

func (s *Sink) usageWaiting() int64 {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	var n int64
	for _, v := range s.usage {
		n += v
	}
	return n
}

// FlushUsage writes what was counted since the last flush as one counter point per feature (a delta) and starts counting
// again. It writes nothing while the usage counters are off. It returns how many points it wrote.
func (s *Sink) FlushUsage(ctx context.Context) (int, error) {
	if !s.usageOn.Load() {
		return 0, nil
	}
	s.usageMu.Lock()
	counts := s.usage
	s.usage = map[string]int64{}
	s.usageMu.Unlock()
	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	now := s.opt.Now().UnixMilli()
	var errs []error
	n := 0
	for _, f := range names {
		if err := s.storeMetric(ctx, Metric{Name: MetricFeatureUses, Kind: "counter", Unit: "{use}", Value: float64(counts[f]), TimeMS: now,
			Attrs: map[string]any{AttrFeature: f}}); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

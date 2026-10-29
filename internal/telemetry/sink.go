package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// Settings keys.
const (
	keyEnabled   = "telemetry.enabled"
	keyExported  = "telemetry.exported_through" // ids at or below this have been exported (the preview shows what is after it)
	keyEnabledBy = "telemetry.enabled_by"
	keyEnabledAt = "telemetry.enabled_at"
	// content export (decision P121): its own switch, off by default
	keyContent   = "telemetry.content.enabled"
	keyContentBy = "telemetry.content.changed_by"
	keyContentAt = "telemetry.content.changed_at"
	// usage counters (decision P121): a diagnostics category of their own, off by default
	keyUsage   = "diagnostics.usage_counters"
	keyUsageBy = "diagnostics.usage_counters.changed_by"
	keyUsageAt = "diagnostics.usage_counters.changed_at"
)

// Schema names the export document.
const Schema = "drydock.telemetry/v1"

// Options configures a Sink.
type Options struct {
	// Redact is the secret scanner's Redact: string values it would change are dropped. May be nil.
	Redact func(string) string
	Now    func() time.Time
	// Retention bounds how long spans and metrics are kept (default 30 days).
	Retention time.Duration
}

// Sink writes spans and metrics to the local database when enabled.
type Sink struct {
	db     *storage.DB
	opt    Options
	mu     sync.RWMutex
	on     bool
	dropMu sync.Mutex
	drop   map[string]int
	// exp, when set, is offered every span and metric once it is stored (the OTLP exporter, decision P113).
	exp atomic.Pointer[Exporter]
	// content is content export (P121); usage is the usage counters' switch and counts what was used since the last flush.
	content atomic.Bool
	usageOn atomic.Bool
	usageMu sync.Mutex
	usage   map[string]int64
}

// New builds a Sink and reads whether telemetry is enabled (default off).
func New(ctx context.Context, db *storage.DB, o Options) (*Sink, error) {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Retention <= 0 {
		o.Retention = 30 * 24 * time.Hour
	}
	s := &Sink{db: db, opt: o, drop: map[string]int{}, usage: map[string]int64{}}
	v, err := s.setting(ctx, keyEnabled)
	if err != nil {
		return nil, err
	}
	s.on = v == "1"
	c, _ := s.setting(ctx, keyContent)
	s.content.Store(c == "1")
	u, _ := s.setting(ctx, keyUsage)
	s.usageOn.Store(u == "1")
	return s, nil
}

func (s *Sink) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Sink) put(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// Enabled reports whether anything is recorded.
func (s *Sink) Enabled() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.on }

// SetEnabled turns recording on or off. It is a human decision; turning it off keeps what was recorded until Purge.
func (s *Sink) SetEnabled(ctx context.Context, on bool, by string) error {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return errors.New("telemetry: only a human: identity may turn telemetry on or off")
	}
	v := "0"
	if on {
		v = "1"
	}
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.put(ctx, tx, keyEnabled, v); err != nil {
			return err
		}
		if err := s.put(ctx, tx, keyEnabledBy, by); err != nil {
			return err
		}
		return s.put(ctx, tx, keyEnabledAt, fmt.Sprint(s.opt.Now().UnixMilli()))
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.on = on
	s.mu.Unlock()
	if x := s.exp.Load(); x != nil && !on {
		x.revoke() // revoking consent stops sending at once, a send in flight included
	}
	return nil
}

// Span is one recorded operation.
type Span struct {
	TraceID  string         `json:"traceId"`
	SpanID   string         `json:"spanId"`
	ParentID string         `json:"parentSpanId,omitempty"`
	Name     string         `json:"name"`
	StartMS  int64          `json:"startTimeUnixMs"`
	EndMS    int64          `json:"endTimeUnixMs"`
	Status   string         `json:"status"` // ok | error
	Attrs    map[string]any `json:"attributes"`
}

// Metric is one recorded data point.
type Metric struct {
	Name   string         `json:"name"`
	Kind   string         `json:"kind"` // counter | histogram | gauge
	Unit   string         `json:"unit,omitempty"`
	Value  float64        `json:"value"`
	TimeMS int64          `json:"timeUnixMs"`
	Attrs  map[string]any `json:"attributes"`
}

func (s *Sink) note(dropped []string) {
	if len(dropped) == 0 {
		return
	}
	s.dropMu.Lock()
	for _, d := range dropped {
		s.drop[d]++
	}
	s.dropMu.Unlock()
}

// TraceID derives a stable 128-bit trace id from a run id, so every span of a run shares one trace.
func TraceID(runID string) string {
	h := sha256.Sum256([]byte("drydock-trace:" + runID))
	return hex.EncodeToString(h[:16])
}

// NewSpanID returns a random 64-bit span id.
func NewSpanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RecordSpan stores a span if telemetry is enabled. Attributes outside the allowlist, or with values that are not
// identifier-shaped, are dropped (and counted by name), never stored; the content attributes are kept, redacted, only while
// content export is on. The stored span, and only it, is what an exporter sends.
func (s *Sink) RecordSpan(ctx context.Context, sp Span) error {
	if !s.Enabled() {
		return nil
	}
	clean, dropped := sanitize(sp.Attrs, s.opt.Redact, s.ContentEnabled())
	s.note(dropped)
	if sp.Status != "error" {
		sp.Status = "ok"
	}
	if !identValue.MatchString(sp.Name) {
		sp.Name = "unnamed"
	}
	sp.Attrs = clean
	id, err := s.insertSpan(ctx, sp)
	if err != nil {
		return err
	}
	if x := s.exp.Load(); x != nil {
		x.offer(queued{id: id, span: &sp})
	}
	return nil
}

// insertSpan writes a span that is already clean and returns its row id.
func (s *Sink) insertSpan(ctx context.Context, sp Span) (int64, error) {
	attrs, _ := json.Marshal(sp.Attrs)
	var id int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `INSERT INTO telemetry_spans(trace_id,span_id,parent_id,name,start_ms,end_ms,status,attrs) VALUES(?,?,?,?,?,?,?,?)`,
			sp.TraceID, sp.SpanID, sp.ParentID, sp.Name, sp.StartMS, sp.EndMS, sp.Status, string(attrs))
		if err == nil {
			id, err = r.LastInsertId()
		}
		return err
	})
	return id, err
}

// RecordMetric stores a data point if telemetry is enabled.
func (s *Sink) RecordMetric(ctx context.Context, m Metric) error {
	if !s.Enabled() {
		return nil
	}
	return s.storeMetric(ctx, m)
}

// storeMetric writes a data point (telemetry, or for a usage counter its own switch, decided that it is recorded) and offers it
// to the exporter.
func (s *Sink) storeMetric(ctx context.Context, m Metric) error {
	clean, dropped := Sanitize(m.Attrs, s.opt.Redact)
	s.note(dropped)
	if m.TimeMS == 0 {
		m.TimeMS = s.opt.Now().UnixMilli()
	}
	if !identValue.MatchString(m.Name) {
		return nil
	}
	switch m.Kind {
	case "counter", "histogram", "gauge":
	default:
		m.Kind = "gauge"
	}
	m.Unit, m.Attrs = onlyIdent(m.Unit), clean
	attrs, _ := json.Marshal(clean)
	var id int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `INSERT INTO telemetry_metrics(ts,name,kind,unit,value,attrs) VALUES(?,?,?,?,?,?)`, m.TimeMS, m.Name, m.Kind, m.Unit, m.Value, string(attrs))
		if err == nil {
			id, err = r.LastInsertId()
		}
		return err
	})
	if x := s.exp.Load(); err == nil && x != nil {
		x.offer(queued{id: id, metric: &m})
	}
	return err
}

func onlyIdent(s string) string {
	if identValue.MatchString(s) {
		return s
	}
	return ""
}

// Status is what the settings screen shows.
type Status struct {
	Enabled         bool           `json:"enabled"`
	EnabledBy       string         `json:"enabledBy,omitempty"`
	EnabledAt       int64          `json:"enabledAt,omitempty"`
	Spans           int64          `json:"spans"`
	Metrics         int64          `json:"metrics"`
	Pending         int64          `json:"pending"` // rows not yet exported
	DroppedByKey    map[string]int `json:"droppedAttributeKeys"`
	Destination     string         `json:"destination"`
	NetworkExporter bool           `json:"networkExporter"`
	// OTLP is the exporter's state, when there is an exporter.
	OTLP *OTLPStatus `json:"otlp,omitempty"`
	// Content is content export's switch (decision P121).
	Content Consent `json:"content"`
	// UsageCounters is the usage counters' switch (decision P121) and the counts not yet written.
	UsageCounters UsageStatus `json:"usageCounters"`
}

// Consent is one switch a person turned on or off, and who did it when.
type Consent struct {
	Enabled   bool   `json:"enabled"`
	ChangedBy string `json:"changedBy,omitempty"`
	ChangedAt int64  `json:"changedAt,omitempty"`
}

// UsageStatus is the usage counters' state: the switch, and how many uses wait to be written as points.
type UsageStatus struct {
	Consent
	Waiting int64 `json:"waiting"`
}

// Status reports the sink's state.
func (s *Sink) Status(ctx context.Context) (Status, error) {
	st := Status{Enabled: s.Enabled(), DroppedByKey: map[string]int{}, Destination: "this device only (local SQLite); export on demand", NetworkExporter: false}
	if x := s.exp.Load(); x != nil {
		o := x.Status()
		st.OTLP = &o
		if o.Active {
			st.NetworkExporter = true
			st.Destination = "this device (local SQLite), and sent to " + o.Endpoint + " over OTLP/HTTP (" + o.Encoding + ")"
		}
	}
	st.Content = s.consent(ctx, s.ContentEnabled(), keyContentBy, keyContentAt)
	st.UsageCounters = UsageStatus{Consent: s.consent(ctx, s.UsageCountersEnabled(), keyUsageBy, keyUsageAt), Waiting: s.usageWaiting()}
	st.EnabledBy, _ = s.setting(ctx, keyEnabledBy)
	if v, _ := s.setting(ctx, keyEnabledAt); v != "" {
		_, _ = fmt.Sscan(v, &st.EnabledAt) // a malformed setting reads as "not recorded"
	}
	through := s.exportedThrough(ctx)
	r := s.db.Reader()
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_spans`).Scan(&st.Spans); err != nil {
		return st, err
	}
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_metrics`).Scan(&st.Metrics); err != nil {
		return st, err
	}
	var ps, pm int64
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_spans WHERE id>?`, through.spans).Scan(&ps); err != nil {
		return st, err
	}
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_metrics WHERE id>?`, through.metrics).Scan(&pm); err != nil {
		return st, err
	}
	st.Pending = ps + pm
	s.dropMu.Lock()
	for k, v := range s.drop {
		st.DroppedByKey[k] = v
	}
	s.dropMu.Unlock()
	return st, nil
}

type watermark struct{ spans, metrics int64 }

func (s *Sink) exportedThrough(ctx context.Context) watermark {
	var w watermark
	if v, _ := s.setting(ctx, keyExported); v != "" {
		_, _ = fmt.Sscanf(v, "%d,%d", &w.spans, &w.metrics) // a malformed watermark reads as nothing exported
	}
	return w
}

// Document is the export format. Resource names the producer; nothing else about the machine or the project is in it.
type Document struct {
	Schema     string         `json:"schema"`
	ExportedAt int64          `json:"exportedAtUnixMs"`
	Resource   map[string]any `json:"resource"`
	Spans      []Span         `json:"spans"`
	Metrics    []Metric       `json:"metrics"`
	Allowlist  []string       `json:"attributeAllowlist"`
	Note       string         `json:"note"`

	throughSpans, throughMetrics int64
}

// Preview returns exactly what Export would write for the rows not yet exported ("what would be sent"), without marking
// anything exported. limit bounds each list (0 = 200).
func (s *Sink) Preview(ctx context.Context, limit int) (Document, error) {
	if limit <= 0 {
		limit = 200
	}
	return s.build(ctx, limit, false)
}

// Export returns the document of everything not yet exported and marks it exported, so the next export carries only
// what is new. Nothing leaves the machine by itself: the caller decides what to do with the document.
func (s *Sink) Export(ctx context.Context) (Document, error) { return s.build(ctx, 0, true) }

func (s *Sink) build(ctx context.Context, limit int, mark bool) (Document, error) {
	through := s.exportedThrough(ctx)
	d := Document{Schema: Schema, ExportedAt: s.opt.Now().UnixMilli(), Resource: map[string]any{"service.name": "drydock"}, Spans: []Span{}, Metrics: []Metric{},
		Allowlist: Allowlist(), Note: "Contains only allowlisted identifier-shaped attributes; no prompts, code, tool output, file contents or secrets can be recorded.",
		throughSpans: through.spans, throughMetrics: through.metrics}
	if s.ContentEnabled() {
		d.Allowlist = append(d.Allowlist, ContentKeys()...)
		d.Note = "Content export is on: the LLM calls' spans also hold the prompts and completions (" + strings.Join(ContentKeys(), ", ") +
			"), with secrets masked. Every other attribute is identifier-shaped."
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,trace_id,span_id,parent_id,name,start_ms,end_ms,status,attrs FROM telemetry_spans WHERE id>? ORDER BY id`+lim, through.spans)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var id int64
		var sp Span
		var attrs string
		if err := rows.Scan(&id, &sp.TraceID, &sp.SpanID, &sp.ParentID, &sp.Name, &sp.StartMS, &sp.EndMS, &sp.Status, &attrs); err != nil {
			rows.Close()
			return d, err
		}
		_ = json.Unmarshal([]byte(attrs), &sp.Attrs)
		d.Spans = append(d.Spans, sp)
		d.throughSpans = id
	}
	rows.Close()
	rows, err = s.db.Reader().QueryContext(ctx, `SELECT id,ts,name,kind,unit,value,attrs FROM telemetry_metrics WHERE id>? ORDER BY id`+lim, through.metrics)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var id int64
		var m Metric
		var attrs string
		if err := rows.Scan(&id, &m.TimeMS, &m.Name, &m.Kind, &m.Unit, &m.Value, &attrs); err != nil {
			rows.Close()
			return d, err
		}
		_ = json.Unmarshal([]byte(attrs), &m.Attrs)
		d.Metrics = append(d.Metrics, m)
		d.throughMetrics = id
	}
	rows.Close()
	if mark && (len(d.Spans) > 0 || len(d.Metrics) > 0) {
		err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
			return s.put(ctx, tx, keyExported, fmt.Sprintf("%d,%d", d.throughSpans, d.throughMetrics))
		})
	}
	return d, err
}

// ExportToFile writes the export document under dir and returns its path. The file name is generated, never taken from
// the caller, so a caller cannot choose where Drydock writes.
func (s *Sink) ExportToFile(ctx context.Context, dir string) (string, Document, error) {
	d, err := s.Export(ctx)
	if err != nil {
		return "", d, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", d, err
	}
	path := filepath.Join(dir, fmt.Sprintf("telemetry-%s.json", s.opt.Now().Format("20060102-150405.000")))
	b, _ := json.MarshalIndent(d, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", d, err
	}
	return path, d, nil
}

// Purge deletes recorded telemetry older than the retention window (or everything when all is true).
func (s *Sink) Purge(ctx context.Context, all bool) error {
	cut := s.opt.Now().Add(-s.opt.Retention).UnixMilli()
	if all {
		cut = 1 << 62
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM telemetry_spans WHERE end_ms<?`, cut); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM telemetry_metrics WHERE ts<?`, cut)
		return err
	})
}

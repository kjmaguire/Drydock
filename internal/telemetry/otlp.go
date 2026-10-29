package telemetry

// The OTLP exporter (decision P113, D12): off by default, turned on by a person who gives a collector's endpoint. It sends what
// the local sink records, as it records it, so the local rows stay the log of everything sent (docs/packaging.md, diagnostics)
// and the preview shows exactly what leaves. Spans and metrics wait in a bounded queue, go out in batches over OTLP/HTTP (JSON
// or protobuf), are retried with backoff when the collector says to try again, and are dropped and counted when the queue is
// full or the retries run out. Header values (an API key) are kept in the credential store, never in settings, the status, a
// log or an event. Plain http is allowed only to this computer (the connection itself is checked, not only the name); anything
// else must be https. Turning it off, or turning telemetry off, empties the queue and stops a send in flight.
//
// Since decision P121 the bodies are gzip-compressed (a setting; by default on for https, where the bytes cross a network, and
// off for a collector on this computer), each row a collector accepted is marked sent in the local sink, and a person who turns
// sending on can choose to also send what was recorded before (bounded by a date and a number of rows, through the same queue).

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
)

// Encodings of the OTLP/HTTP body.
const (
	EncodingProtobuf = "protobuf"
	EncodingJSON     = "json"
)

// Settings keys of the exporter. None holds a secret: header values live in the credential store under headerRef.
const (
	keyOTLPEnabled   = "telemetry.otlp.enabled"
	keyOTLPEndpoint  = "telemetry.otlp.endpoint"
	keyOTLPEncoding  = "telemetry.otlp.encoding"
	keyOTLPHeaders   = "telemetry.otlp.headers" // JSON list of header names
	keyOTLPBy        = "telemetry.otlp.changed_by"
	keyOTLPLastOK    = "telemetry.otlp.last_success_at"
	keyOTLPLastErr   = "telemetry.otlp.last_error"
	keyOTLPLastErrAt = "telemetry.otlp.last_error_at"
	keyOTLPGzip      = "telemetry.otlp.compression"   // gzip | none | "" (automatic: gzip for https)
	keyOTLPBackfill  = "telemetry.otlp.last_backfill" // JSON BackfillResult
)

// Compression settings of the body.
const (
	CompressionAuto = ""
	CompressionGzip = "gzip"
	CompressionNone = "none"
)

// Backfill bounds: rows of at most the retention window, and at most MaxBackfillRows of them (they go through the queue, which
// holds 2048).
const (
	DefaultBackfillRows = 500
	MaxBackfillRows     = 1000
)

// MaxOTLPHeaders bounds how many headers a person can give the exporter.
const MaxOTLPHeaders = 8

// headerRef is the credential store reference of a header's value.
func headerRef(name string) string { return "telemetry:otlp:header:" + strings.ToLower(name) }

// SecretStore is the credential store (providers.SecretStore's shape).
type SecretStore interface {
	Put(ctx context.Context, ref, value string) error
	Get(ctx context.Context, ref string) (string, error)
	Delete(ctx context.Context, ref string) error
}

// ExporterOptions configures an Exporter. Zero values take the defaults.
type ExporterOptions struct {
	// Secrets keeps the header values; without it no header can be set.
	Secrets SecretStore
	// Known is told each header value once it is set or read, so the secret scanner redacts it wherever it appears; Forget when
	// it is removed. Either may be nil.
	Known  func(name, value string)
	Forget func(name string)
	// HTTPS is the transport for https endpoints (default: http.DefaultTransport's settings). Tests pass their TLS stand-in's.
	HTTPS       http.RoundTripper
	QueueSize   int           // default 2048 spans and metrics together
	BatchSize   int           // default 256 per request
	Interval    time.Duration // how long a partial batch waits, default 5s
	MaxAttempts int           // tries per batch, default 5
	BaseDelay   time.Duration // first backoff, doubled each try, default 1s
	MaxDelay    time.Duration // longest wait between tries (a longer Retry-After is cut to it), default 30s
	Timeout     time.Duration // one request, default 10s
	Sleep       func(ctx context.Context, d time.Duration) error
}

// OTLPConfig is what a person chose.
type OTLPConfig struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Encoding string `json:"encoding"`
	// Headers are the names of the headers sent with each request; their values are in the credential store and never shown.
	Headers []string `json:"headers"`
	// Compression is what the person chose: gzip, none, or "" for the default (gzip for https, none for this computer).
	Compression string `json:"compression"`
}

// gzipOn reports whether request bodies are compressed.
func (c OTLPConfig) gzipOn() bool {
	switch c.Compression {
	case CompressionGzip:
		return true
	case CompressionNone:
		return false
	}
	return strings.HasPrefix(strings.ToLower(c.Endpoint), "https://")
}

// Backfill is "Also send what was recorded before", chosen when sending is turned on: the spans and metrics recorded since
// SinceMS that no collector has had, the newest MaxRows of them (0: DefaultBackfillRows).
type Backfill struct {
	SinceMS int64 `json:"sinceMs"`
	MaxRows int   `json:"maxRows,omitempty"`
}

// BackfillResult is what the last backfill queued.
type BackfillResult struct {
	At      int64  `json:"at"`
	SinceMS int64  `json:"sinceMs"`
	MaxRows int    `json:"maxRows"`
	Queued  int    `json:"queued"`
	Left    int    `json:"left"` // rows in the window the cap left out
	By      string `json:"by,omitempty"`
}

// OTLPChange is a person's edit of the configuration. A nil field is left as it is.
type OTLPChange struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	Endpoint *string `json:"endpoint,omitempty"`
	Encoding *string `json:"encoding,omitempty"`
	// SetHeaders stores (or replaces) each header's value; RemoveHeaders forgets headers by name.
	SetHeaders    map[string]string `json:"setHeaders,omitempty"`
	RemoveHeaders []string          `json:"removeHeaders,omitempty"`
	// Compression is gzip, none or "" (automatic).
	Compression *string `json:"compression,omitempty"`
	// Backfill, with a change that turns sending on, also queues rows recorded before (default: only what is recorded from now).
	Backfill *Backfill `json:"backfill,omitempty"`
}

// OTLPStatus is the exporter's state for the settings screen. LastError is Drydock's own description of what went wrong (the
// collector's status code, or that it could not be reached), with any header value and secret taken out.
type OTLPStatus struct {
	OTLPConfig
	ChangedBy string `json:"changedBy,omitempty"`
	// Active is true when spans and metrics are being sent: the exporter is on, has an endpoint, and telemetry records.
	Active          bool   `json:"active"`
	Queued          int    `json:"queued"`
	QueueSize       int    `json:"queueSize"`
	SentSpans       int64  `json:"sentSpans"`
	SentMetrics     int64  `json:"sentMetrics"`
	DroppedOverflow int64  `json:"droppedOverflow"` // the queue was full
	DroppedFailed   int64  `json:"droppedFailed"`   // the collector refused them, or the retries ran out
	Rejected        int64  `json:"rejected"`        // accepted by the request but rejected by the collector (partial success)
	LastSuccessAt   int64  `json:"lastSuccessAt,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	LastErrorAt     int64  `json:"lastErrorAt,omitempty"`
	CanKeepHeaders  bool   `json:"canKeepHeaders"` // there is a credential store
	// Gzip is whether bodies are compressed now (Compression, or its default for the endpoint).
	Gzip bool `json:"gzip"`
	// LastBackfill is what the last "Also send what was recorded before" queued.
	LastBackfill *BackfillResult `json:"lastBackfill,omitempty"`
}

// TestResult is what "Send a test span" says.
type TestResult struct {
	OK     bool       `json:"ok"`
	Error  string     `json:"error,omitempty"`
	Status OTLPStatus `json:"status"`
}

type queued struct {
	id     int64 // the row in the local sink, marked sent once a collector accepted it
	span   *Span
	metric *Metric
}

// Exporter sends the sink's spans and metrics to an OTLP/HTTP collector.
type Exporter struct {
	s   *Sink
	opt ExporterOptions

	mu       sync.Mutex
	cfg      OTLPConfig
	by       string
	queue    []queued
	sendCtx  context.Context
	cancel   context.CancelFunc
	sent     [2]int64 // spans, metrics
	overflow int64
	failed   int64
	rejected int64
	lastOK   int64
	lastErr  string
	lastErrT int64
	backfill *BackfillResult

	wake    chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

// NewExporter reads the stored configuration and attaches the exporter to s: from then on what s records is offered to it.
// Start begins sending.
func NewExporter(ctx context.Context, s *Sink, o ExporterOptions) (*Exporter, error) {
	if o.QueueSize <= 0 {
		o.QueueSize = 2048
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 256
	}
	if o.Interval <= 0 {
		o.Interval = 5 * time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = time.Second
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 30 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	x := &Exporter{s: s, opt: o, wake: make(chan struct{}, 1), stop: make(chan struct{}), stopped: make(chan struct{})}
	x.sendCtx, x.cancel = context.WithCancel(context.Background())
	if err := x.load(ctx); err != nil {
		return nil, err
	}
	if o.Known != nil {
		for _, h := range x.cfg.Headers {
			if v, err := x.header(ctx, h); err == nil {
				o.Known(h, v)
			}
		}
	}
	s.exp.Store(x)
	return x, nil
}

func (x *Exporter) load(ctx context.Context) error {
	get := func(k string) string { v, _ := x.s.setting(ctx, k); return v }
	if _, err := x.s.setting(ctx, keyOTLPEnabled); err != nil {
		return err
	}
	x.cfg = OTLPConfig{Enabled: get(keyOTLPEnabled) == "1", Endpoint: get(keyOTLPEndpoint), Encoding: get(keyOTLPEncoding), Headers: []string{},
		Compression: get(keyOTLPGzip)}
	if x.cfg.Compression != CompressionGzip && x.cfg.Compression != CompressionNone {
		x.cfg.Compression = CompressionAuto
	}
	if v := get(keyOTLPBackfill); v != "" {
		var b BackfillResult
		if json.Unmarshal([]byte(v), &b) == nil {
			x.backfill = &b
		}
	}
	if x.cfg.Encoding != EncodingJSON {
		x.cfg.Encoding = EncodingProtobuf
	}
	if v := get(keyOTLPHeaders); v != "" {
		_ = json.Unmarshal([]byte(v), &x.cfg.Headers) // a malformed list reads as no headers
	}
	x.by = get(keyOTLPBy)
	x.lastErr = get(keyOTLPLastErr)
	_, _ = fmt.Sscan(get(keyOTLPLastOK), &x.lastOK)
	_, _ = fmt.Sscan(get(keyOTLPLastErrAt), &x.lastErrT)
	return nil
}

// Start runs the sending loop until Close.
func (x *Exporter) Start() {
	safe.Go("telemetry.Exporter", func() {
		defer close(x.stopped)
		t := time.NewTicker(x.opt.Interval)
		defer t.Stop()
		for {
			select {
			case <-x.stop:
				return
			case <-x.wake:
			case <-t.C:
			}
			x.flush()
		}
	})
}

// Close stops the loop and any send in flight. What is still queued is not sent (it stays in the local sink).
func (x *Exporter) Close() {
	x.once.Do(func() {
		close(x.stop)
		x.mu.Lock()
		x.cancel()
		x.mu.Unlock()
	})
}

// Wait waits for the loop started by Start to end after Close.
func (x *Exporter) Wait() { <-x.stopped }

func (x *Exporter) activeLocked() bool {
	return x.cfg.Enabled && x.cfg.Endpoint != "" && x.s.Enabled()
}

// offer queues a recorded span or metric when the exporter is active, dropping (and counting) it when the queue is full. It
// reports whether it was queued.
func (x *Exporter) offer(q queued) bool {
	x.mu.Lock()
	if !x.activeLocked() {
		x.mu.Unlock()
		return false
	}
	if len(x.queue) >= x.opt.QueueSize {
		x.overflow++
		x.mu.Unlock()
		return false
	}
	x.queue = append(x.queue, q)
	full := len(x.queue) >= x.opt.BatchSize
	x.mu.Unlock()
	if full {
		x.kick()
	}
	return true
}

func (x *Exporter) kick() {
	select {
	case x.wake <- struct{}{}:
	default:
	}
}

// Flush sends everything queued now (the loop does this on its own; tests and shutdown call it).
func (x *Exporter) Flush() { x.flush() }

func (x *Exporter) flush() {
	for {
		x.mu.Lock()
		if !x.activeLocked() {
			x.queue = nil
			x.mu.Unlock()
			return
		}
		n := min(len(x.queue), x.opt.BatchSize)
		if n == 0 {
			x.mu.Unlock()
			return
		}
		batch := append([]queued(nil), x.queue[:n]...)
		x.queue = append(x.queue[:0], x.queue[n:]...)
		cfg, ctx := x.cfg, x.sendCtx
		x.mu.Unlock()

		var spans []Span
		var metrics []Metric
		var spanIDs, metricIDs []int64
		for _, q := range batch {
			if q.span != nil {
				spans, spanIDs = append(spans, *q.span), append(spanIDs, q.id)
			} else if q.metric != nil {
				metrics, metricIDs = append(metrics, *q.metric), append(metricIDs, q.id)
			}
		}
		if len(spans) > 0 && x.deliver(ctx, cfg, "/v1/traces", spans, nil) {
			x.markSent("telemetry_spans", spanIDs)
		}
		if len(metrics) > 0 && x.deliver(ctx, cfg, "/v1/metrics", nil, metrics) {
			x.markSent("telemetry_metrics", metricIDs)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// markSent records in the local sink that the collector accepted these rows (the items a partial success rejected cannot be
// told apart, so the whole request counts).
func (x *Exporter) markSent(table string, ids []int64) {
	var keep []any
	for _, id := range ids {
		if id > 0 {
			keep = append(keep, id)
		}
	}
	if len(keep) == 0 {
		return
	}
	now := x.s.opt.Now().UnixMilli()
	q := `UPDATE ` + table + ` SET otlp_sent_at=? WHERE id IN (?` + strings.Repeat(",?", len(keep)-1) + `)`
	_ = x.s.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, append([]any{now}, keep...)...)
		return err
	})
}

// deliver sends one request, retrying what the OTLP specification says to retry, records the outcome and reports whether the
// collector accepted it.
func (x *Exporter) deliver(ctx context.Context, cfg OTLPConfig, path string, spans []Span, metrics []Metric) bool {
	n := int64(len(spans) + len(metrics))
	var err error
	for attempt := 1; ; attempt++ {
		var retry bool
		var after time.Duration
		var rejected int64
		rejected, retry, after, err = x.post(ctx, cfg, path, spans, metrics)
		if ctx.Err() != nil {
			return false // turned off or closed: not a failure, and nothing more is sent
		}
		if err == nil {
			x.succeeded(len(spans) > 0, n, rejected)
			return true
		}
		if !retry || attempt >= x.opt.MaxAttempts {
			break
		}
		wait := x.opt.BaseDelay << (attempt - 1)
		if after > wait {
			wait = after
		}
		if wait > x.opt.MaxDelay || wait <= 0 {
			wait = x.opt.MaxDelay
		}
		if x.opt.Sleep(ctx, wait) != nil {
			return false
		}
	}
	x.mu.Lock()
	x.failed += n
	x.mu.Unlock()
	x.failedWith(err)
	return false
}

func (x *Exporter) succeeded(spans bool, n, rejected int64) {
	now := x.s.opt.Now().UnixMilli()
	x.mu.Lock()
	if spans {
		x.sent[0] += n - rejected
	} else {
		x.sent[1] += n - rejected
	}
	x.rejected += rejected
	x.lastOK = now
	x.mu.Unlock()
	_ = x.s.db.WriteTx(context.Background(), func(tx *sql.Tx) error { return x.s.put(context.Background(), tx, keyOTLPLastOK, fmt.Sprint(now)) })
	if rejected > 0 {
		x.failedWith(fmt.Errorf("the collector accepted the request but rejected %d of its items", rejected))
	}
}

func (x *Exporter) failedWith(err error) {
	msg := x.scrub(err.Error())
	now := x.s.opt.Now().UnixMilli()
	x.mu.Lock()
	x.lastErr, x.lastErrT = msg, now
	x.mu.Unlock()
	_ = x.s.db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if err := x.s.put(context.Background(), tx, keyOTLPLastErr, msg); err != nil {
			return err
		}
		return x.s.put(context.Background(), tx, keyOTLPLastErrAt, fmt.Sprint(now))
	})
}

// scrub takes every header value and anything the secret scanner recognizes out of a message before it is kept or shown.
func (x *Exporter) scrub(msg string) string {
	x.mu.Lock()
	names := append([]string(nil), x.cfg.Headers...)
	x.mu.Unlock()
	for _, h := range names {
		if v, err := x.header(context.Background(), h); err == nil && v != "" {
			msg = strings.ReplaceAll(msg, v, "[header value]")
		}
	}
	if x.s.opt.Redact != nil {
		msg = x.s.opt.Redact(msg)
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

func (x *Exporter) header(ctx context.Context, name string) (string, error) {
	if x.opt.Secrets == nil {
		return "", errors.New("there is no credential store")
	}
	return x.opt.Secrets.Get(ctx, headerRef(name))
}

// errCollector is a collector's answer that is not a success.
type errCollector struct{ status int }

func (e errCollector) Error() string {
	return fmt.Sprintf("the collector answered %d %s", e.status, http.StatusText(e.status))
}

// post sends one OTLP/HTTP request. It reports how many items a partial success rejected, whether a failure may be retried
// (a transport failure, 429, 502, 503, 504) and the wait the collector asked for.
func (x *Exporter) post(ctx context.Context, cfg OTLPConfig, path string, spans []Span, metrics []Metric) (rejected int64, retry bool, after time.Duration, err error) {
	u, err := endpointURL(cfg.Endpoint, path)
	if err != nil {
		return 0, false, 0, err
	}
	var body []byte
	ct := "application/x-protobuf"
	switch {
	case cfg.Encoding == EncodingJSON && spans != nil:
		body, ct = tracesJSON(spans), "application/json"
	case cfg.Encoding == EncodingJSON:
		body, ct = metricsJSON(metrics), "application/json"
	case spans != nil:
		body = tracesProto(spans)
	default:
		body = metricsProto(metrics)
	}
	gz := cfg.gzipOn()
	if gz {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body) // a bytes.Buffer does not fail
		_ = zw.Close()
		body = buf.Bytes()
	}
	rctx, cancel := context.WithTimeout(ctx, x.opt.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, false, 0, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("User-Agent", "Drydock-OTLP")
	if gz {
		req.Header.Set("Content-Encoding", "gzip") // OTLP/HTTP's compression
	}
	for _, h := range cfg.Headers {
		v, herr := x.header(ctx, h)
		if herr != nil {
			return 0, false, 0, fmt.Errorf("the value of the %s header could not be read from the credential store", h)
		}
		req.Header.Set(h, v)
	}
	client := &http.Client{Transport: x.transport(u.Scheme), CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // a redirect would carry the headers elsewhere: it is reported as the collector's answer
	}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, false, 0, ctx.Err()
		}
		return 0, true, 0, fmt.Errorf("could not reach the collector: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if strings.Contains(resp.Header.Get("Content-Type"), "json") {
			return rejectedJSON(b), false, 0, nil
		}
		return rejectedProto(b), false, 0, nil
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return 0, true, retryAfter(resp.Header.Get("Retry-After"), x.s.opt.Now()), errCollector{resp.StatusCode}
	}
	return 0, false, 0, errCollector{resp.StatusCode}
}

// unwrapURL drops net/http's "Post <url>:" prefix: the endpoint is shown next to the error already.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return t.Sub(now)
	}
	return 0
}

// transport is the HTTPS transport, or for plain http one whose every connection must be to a loopback address.
func (x *Exporter) transport(scheme string) http.RoundTripper {
	if scheme == "https" {
		if x.opt.HTTPS != nil {
			return x.opt.HTTPS
		}
		return http.DefaultTransport
	}
	d := &net.Dialer{Timeout: x.opt.Timeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("plain http goes only to this computer, and %s is not on it (use https)", host)
		}
		return nil
	}}
	return &http.Transport{DialContext: d.DialContext, Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
}

// endpointURL is the URL of one signal: the endpoint's path with /v1/traces or /v1/metrics after it (OTLP/HTTP's rule for a
// base endpoint).
func endpointURL(endpoint, path string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("the endpoint is not a URL")
	}
	v := *u
	v.Path = strings.TrimRight(u.Path, "/") + path
	v.RawPath = ""
	return &v, nil
}

// CheckEndpoint says why an endpoint cannot be used, or nil. It must be http or https with a host; plain http only to this
// computer (localhost or a loopback address); no user name, password, query or fragment, because the endpoint is stored as a
// setting and shown, and a key belongs in a header.
func CheckEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errors.New("the endpoint must be a URL such as https://collector.example.com:4318")
	}
	if u.User != nil {
		return errors.New("the endpoint must not hold a user name or password: put a key in a header, which is kept in the credential store")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("the endpoint must not have a query or a fragment: put a key in a header, which is kept in the credential store")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if strings.EqualFold(host, "localhost") {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return errors.New("plain http is allowed only to this computer (localhost or 127.0.0.1); use https for any other collector")
	}
	return errors.New("the endpoint must start with https:// (or http:// for a collector on this computer)")
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// reserved are headers Drydock sets itself or that HTTP owns.
var reserved = map[string]bool{"content-type": true, "content-length": true, "content-encoding": true, "host": true, "connection": true,
	"transfer-encoding": true, "user-agent": true, "te": true, "upgrade": true, "trailer": true, "keep-alive": true, "proxy-authorization": true}

func checkHeader(name, value string) error {
	if !headerName.MatchString(name) {
		return fmt.Errorf("%q is not a header name (letters, digits and dashes)", name)
	}
	if reserved[strings.ToLower(name)] {
		return fmt.Errorf("the %s header is set by Drydock or by HTTP and cannot be given", name)
	}
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("the value of the %s header must be one line of at most 4096 characters", name)
	}
	return nil
}

// Configure applies a person's change and returns the new state. Turning the exporter off (or changing where it sends) empties
// the queue and stops a send in flight: nothing more goes to the old endpoint.
func (x *Exporter) Configure(ctx context.Context, ch OTLPChange, by string) (OTLPStatus, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return OTLPStatus{}, errors.New("telemetry: only a person may change where telemetry is sent")
	}
	x.mu.Lock()
	next := x.cfg
	next.Headers = append([]string(nil), x.cfg.Headers...)
	x.mu.Unlock()
	if ch.Endpoint != nil {
		next.Endpoint = strings.TrimSpace(*ch.Endpoint)
		if next.Endpoint != "" {
			if err := CheckEndpoint(next.Endpoint); err != nil {
				return OTLPStatus{}, err
			}
		}
	}
	if ch.Encoding != nil {
		switch *ch.Encoding {
		case EncodingJSON, EncodingProtobuf:
			next.Encoding = *ch.Encoding
		default:
			return OTLPStatus{}, fmt.Errorf("the encoding is %s or %s, not %q", EncodingProtobuf, EncodingJSON, *ch.Encoding)
		}
	}
	if ch.Compression != nil {
		switch *ch.Compression {
		case CompressionAuto, CompressionGzip, CompressionNone:
			next.Compression = *ch.Compression
		default:
			return OTLPStatus{}, fmt.Errorf("compression is %s, %s or automatic (empty), not %q", CompressionGzip, CompressionNone, *ch.Compression)
		}
	}
	wasOn := next.Enabled
	if ch.Enabled != nil {
		next.Enabled = *ch.Enabled
	}
	if next.Enabled && next.Endpoint == "" {
		return OTLPStatus{}, errors.New("give the collector's endpoint before turning sending on")
	}
	var bf *Backfill
	if ch.Backfill != nil {
		if wasOn || !next.Enabled {
			return OTLPStatus{}, errors.New("sending what was recorded before is chosen when sending is turned on")
		}
		b, err := x.checkBackfill(*ch.Backfill)
		if err != nil {
			return OTLPStatus{}, err
		}
		bf = &b
	}
	// the rows "recorded before" are those stored now: whatever is recorded once sending is on is offered as it comes
	var through watermark
	if bf != nil {
		var err error
		if through, err = x.s.maxIDs(ctx); err != nil {
			return OTLPStatus{}, err
		}
	}
	remove := map[string]bool{}
	for _, h := range ch.RemoveHeaders {
		remove[strings.ToLower(h)] = true
	}
	set := map[string]string{}
	for h, v := range ch.SetHeaders {
		if err := checkHeader(h, v); err != nil {
			return OTLPStatus{}, err
		}
		set[h] = v
	}
	if len(set) > 0 && x.opt.Secrets == nil {
		return OTLPStatus{}, errors.New("this computer has no credential store to keep a header value in; Drydock will not store it anywhere else")
	}
	var names []string
	seen := map[string]bool{}
	for _, h := range next.Headers {
		if !remove[strings.ToLower(h)] && !seen[strings.ToLower(h)] {
			names, seen[strings.ToLower(h)] = append(names, h), true
		}
	}
	for h := range set {
		if !seen[strings.ToLower(h)] {
			names, seen[strings.ToLower(h)] = append(names, h), true
		}
	}
	sort.Strings(names)
	if len(names) > MaxOTLPHeaders {
		return OTLPStatus{}, fmt.Errorf("at most %d headers", MaxOTLPHeaders)
	}
	next.Headers = append([]string{}, names...)

	// the values first: a header is listed only once its value is in the credential store
	for h, v := range set {
		if err := x.opt.Secrets.Put(ctx, headerRef(h), v); err != nil {
			return OTLPStatus{}, fmt.Errorf("could not keep the %s header in the credential store: %w", h, err)
		}
		if x.opt.Known != nil {
			x.opt.Known(h, v)
		}
	}
	hb, _ := json.Marshal(next.Headers)
	on := "0"
	if next.Enabled {
		on = "1"
	}
	err := x.s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		for k, v := range map[string]string{keyOTLPEnabled: on, keyOTLPEndpoint: next.Endpoint, keyOTLPEncoding: next.Encoding, keyOTLPHeaders: string(hb), keyOTLPBy: by,
			keyOTLPGzip: next.Compression} {
			if err := x.s.put(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return OTLPStatus{}, err
	}
	for h := range remove {
		if seen[h] || x.opt.Secrets == nil {
			continue // set again in this same change
		}
		_ = x.opt.Secrets.Delete(ctx, headerRef(h)) // the list no longer names it; a value left behind is never sent
		if x.opt.Forget != nil {
			x.opt.Forget(h)
		}
	}
	x.mu.Lock()
	moved := next.Endpoint != x.cfg.Endpoint || next.Encoding != x.cfg.Encoding
	x.cfg, x.by = next, by
	if !next.Enabled || moved {
		x.revokeLocked()
	}
	x.mu.Unlock()
	if bf != nil {
		if err := x.queueBackfill(ctx, *bf, through, by); err != nil {
			return x.Status(), fmt.Errorf("sending is on, but what was recorded before could not be queued: %w", err)
		}
		x.kick()
	}
	return x.Status(), nil
}

// revoke empties the queue and stops a send in flight (telemetry, or sending, was turned off).
func (x *Exporter) revoke() {
	x.mu.Lock()
	x.revokeLocked()
	x.mu.Unlock()
}

func (x *Exporter) revokeLocked() {
	x.queue = nil
	x.cancel()
	x.sendCtx, x.cancel = context.WithCancel(context.Background())
}

// Status reports the exporter's state.
func (x *Exporter) Status() OTLPStatus {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := OTLPStatus{OTLPConfig: x.cfg, ChangedBy: x.by, Active: x.activeLocked(), Queued: len(x.queue), QueueSize: x.opt.QueueSize,
		SentSpans: x.sent[0], SentMetrics: x.sent[1], DroppedOverflow: x.overflow, DroppedFailed: x.failed, Rejected: x.rejected,
		LastSuccessAt: x.lastOK, LastError: x.lastErr, LastErrorAt: x.lastErrT, CanKeepHeaders: x.opt.Secrets != nil,
		Gzip: x.cfg.gzipOn()}
	st.Headers = append([]string{}, x.cfg.Headers...)
	if x.backfill != nil {
		b := *x.backfill
		st.LastBackfill = &b
	}
	return st
}

// SendTest sends one span now, outside the queue and without retries, to the configured endpoint (whether or not sending is on),
// and records it in the local sink first, so the local rows still hold everything that was sent. The span carries only the
// outcome attribute.
func (x *Exporter) SendTest(ctx context.Context, by string) (TestResult, error) {
	if !strings.HasPrefix(by, "human:") || len(by) == len("human:") {
		return TestResult{}, errors.New("telemetry: only a person may send a test span")
	}
	x.mu.Lock()
	cfg := x.cfg
	x.mu.Unlock()
	if cfg.Endpoint == "" {
		return TestResult{}, errors.New("give the collector's endpoint first")
	}
	now := x.s.opt.Now().UnixMilli()
	sp := Span{TraceID: newTraceID(), SpanID: NewSpanID(), Name: "drydock.telemetry.test", StartMS: now, EndMS: now, Status: "ok",
		Attrs: map[string]any{AttrOutcome: "ok"}}
	id, err := x.s.insertSpan(ctx, sp)
	if err != nil {
		return TestResult{}, err
	}
	rejected, _, _, err := x.post(ctx, cfg, "/v1/traces", []Span{sp}, nil)
	if err == nil && rejected > 0 {
		err = errors.New("the collector accepted the request but rejected the span")
	}
	if err != nil {
		x.failedWith(err)
		st := x.Status()
		return TestResult{OK: false, Error: st.LastError, Status: st}, nil
	}
	x.succeeded(true, 1, 0)
	x.markSent("telemetry_spans", []int64{id})
	return TestResult{OK: true, Status: x.Status()}, nil
}

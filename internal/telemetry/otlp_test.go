package telemetry_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

// The exporter is tested against collectorStandIn: a TEST DOUBLE for an OpenTelemetry collector, a local HTTP server that
// decodes every OTLP/HTTP request it receives (JSON or protobuf) into spans and metrics, so the tests read what really went
// over the wire. It is not a collector and implements nothing else of one.

const apiKey = "otlp-key-7f3a9c1e5b2d4f6a8c0e"

type gotSpan struct {
	TraceID, SpanID, Name string
	Kind, StatusCode      int
	Start, End            uint64
	Attrs                 map[string]any
}

type gotPoint struct {
	Attrs map[string]any
	Value float64
	Count uint64
	Time  uint64
}

type gotMetric struct {
	Name, Unit, Type string // Type: sum, gauge or histogram
	Temporality      int
	Monotonic        bool
	Points           []gotPoint
}

type gotRequest struct {
	Path, ContentType string
	Header            http.Header
	Body              []byte
	Resource          map[string]any
	Spans             []gotSpan
	Metrics           []gotMetric
}

type collectorStandIn struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	got []gotRequest
	// answer, when set, decides the status of each request (n counts from 1); otherwise 200.
	answer func(n int, w http.ResponseWriter) int
	hold   chan struct{} // when set, a request waits for it (or for the client to give up)
}

func newCollector(t *testing.T, tls bool) *collectorStandIn {
	c := &collectorStandIn{t: t}
	h := http.HandlerFunc(c.serve)
	if tls {
		c.srv = httptest.NewTLSServer(h)
	} else {
		c.srv = httptest.NewServer(h)
	}
	t.Cleanup(c.srv.Close)
	return c
}

func (c *collectorStandIn) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if c.hold != nil {
		select {
		case <-c.hold:
		case <-r.Context().Done():
			return
		}
	}
	g := gotRequest{Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Header: r.Header.Clone(), Body: body}
	var err error
	switch {
	case g.ContentType == "application/json" && r.URL.Path == "/v1/traces":
		g.Resource, g.Spans, err = decodeTracesJSON(body)
	case g.ContentType == "application/json" && r.URL.Path == "/v1/metrics":
		g.Resource, g.Metrics, err = decodeMetricsJSON(body)
	case g.ContentType == "application/x-protobuf" && r.URL.Path == "/v1/traces":
		g.Resource, g.Spans, err = decodeTracesProto(body)
	case g.ContentType == "application/x-protobuf" && r.URL.Path == "/v1/metrics":
		g.Resource, g.Metrics, err = decodeMetricsProto(body)
	default:
		err = errors.New("unexpected " + r.URL.Path + " " + g.ContentType)
	}
	if err != nil {
		c.t.Errorf("the stand-in could not decode the request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.got = append(c.got, g)
	n := len(c.got)
	c.mu.Unlock()
	status := http.StatusOK
	if c.answer != nil {
		status = c.answer(n, w)
	}
	w.Header().Set("Content-Type", g.ContentType)
	w.WriteHeader(status)
	if status == http.StatusOK && g.ContentType == "application/json" {
		_, _ = w.Write([]byte(`{}`))
	}
}

func (c *collectorStandIn) requests() []gotRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]gotRequest(nil), c.got...)
}

// --- the stand-in's JSON decoding (OTLP's JSON mapping) ---

type jsonAttr struct {
	Key   string `json:"key"`
	Value struct {
		StringValue *string  `json:"stringValue"`
		IntValue    *string  `json:"intValue"`
		DoubleValue *float64 `json:"doubleValue"`
		BoolValue   *bool    `json:"boolValue"`
	} `json:"value"`
}

func jsonAttrMap(as []jsonAttr) (map[string]any, error) {
	m := map[string]any{}
	for _, a := range as {
		switch v := a.Value; {
		case v.StringValue != nil:
			m[a.Key] = *v.StringValue
		case v.IntValue != nil:
			n, err := strconv.ParseInt(*v.IntValue, 10, 64)
			if err != nil {
				return nil, err
			}
			m[a.Key] = n
		case v.DoubleValue != nil:
			m[a.Key] = *v.DoubleValue
		case v.BoolValue != nil:
			m[a.Key] = *v.BoolValue
		default:
			return nil, errors.New("attribute " + a.Key + " has no value")
		}
	}
	return m, nil
}

func decodeTracesJSON(b []byte) (map[string]any, []gotSpan, error) {
	var req struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []jsonAttr `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					TraceID    string     `json:"traceId"`
					SpanID     string     `json:"spanId"`
					Name       string     `json:"name"`
					Kind       int        `json:"kind"`
					Start      string     `json:"startTimeUnixNano"`
					End        string     `json:"endTimeUnixNano"`
					Attributes []jsonAttr `json:"attributes"`
					Status     struct {
						Code int `json:"code"`
					} `json:"status"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, nil, err
	}
	var res map[string]any
	var out []gotSpan
	for _, rs := range req.ResourceSpans {
		res, _ = jsonAttrMap(rs.Resource.Attributes)
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				a, err := jsonAttrMap(s.Attributes)
				if err != nil {
					return nil, nil, err
				}
				st, _ := strconv.ParseUint(s.Start, 10, 64)
				en, _ := strconv.ParseUint(s.End, 10, 64)
				out = append(out, gotSpan{TraceID: s.TraceID, SpanID: s.SpanID, Name: s.Name, Kind: s.Kind, StatusCode: s.Status.Code, Start: st, End: en, Attrs: a})
			}
		}
	}
	return res, out, nil
}

func decodeMetricsJSON(b []byte) (map[string]any, []gotMetric, error) {
	type point struct {
		Attributes []jsonAttr `json:"attributes"`
		Time       string     `json:"timeUnixNano"`
		AsDouble   *float64   `json:"asDouble"`
		Count      string     `json:"count"`
		Sum        *float64   `json:"sum"`
	}
	type data struct {
		DataPoints  []point `json:"dataPoints"`
		Temporality int     `json:"aggregationTemporality"`
		Monotonic   bool    `json:"isMonotonic"`
	}
	var req struct {
		ResourceMetrics []struct {
			Resource struct {
				Attributes []jsonAttr `json:"attributes"`
			} `json:"resource"`
			ScopeMetrics []struct {
				Metrics []struct {
					Name      string `json:"name"`
					Unit      string `json:"unit"`
					Sum       *data  `json:"sum"`
					Gauge     *data  `json:"gauge"`
					Histogram *data  `json:"histogram"`
				} `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, nil, err
	}
	var res map[string]any
	var out []gotMetric
	for _, rm := range req.ResourceMetrics {
		res, _ = jsonAttrMap(rm.Resource.Attributes)
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				g := gotMetric{Name: m.Name, Unit: m.Unit}
				var d *data
				switch {
				case m.Sum != nil:
					g.Type, d = "sum", m.Sum
				case m.Gauge != nil:
					g.Type, d = "gauge", m.Gauge
				case m.Histogram != nil:
					g.Type, d = "histogram", m.Histogram
				default:
					return nil, nil, errors.New("metric " + m.Name + " has no data")
				}
				g.Temporality, g.Monotonic = d.Temporality, d.Monotonic
				for _, p := range d.DataPoints {
					a, err := jsonAttrMap(p.Attributes)
					if err != nil {
						return nil, nil, err
					}
					gp := gotPoint{Attrs: a}
					gp.Time, _ = strconv.ParseUint(p.Time, 10, 64)
					if p.AsDouble != nil {
						gp.Value = *p.AsDouble
					}
					if p.Sum != nil {
						gp.Value = *p.Sum
						gp.Count, _ = strconv.ParseUint(p.Count, 10, 64)
					}
					g.Points = append(g.Points, gp)
				}
				out = append(out, g)
			}
		}
	}
	return res, out, nil
}

// --- the stand-in's protobuf decoding: a generic wire reader and the field numbers of opentelemetry-proto ---

type pbf struct {
	num, wire int
	v         uint64 // varint, fixed64
	b         []byte // length-delimited
}

func pbRead(b []byte) ([]pbf, error) {
	var out []pbf
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errors.New("bad key")
		}
		b = b[n:]
		f := pbf{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, m := binary.Uvarint(b)
			if m <= 0 {
				return nil, errors.New("bad varint")
			}
			f.v, b = v, b[m:]
		case 1:
			if len(b) < 8 {
				return nil, errors.New("short fixed64")
			}
			f.v, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, m := binary.Uvarint(b)
			if m <= 0 || uint64(len(b)-m) < l {
				return nil, errors.New("short bytes")
			}
			f.b, b = b[m:m+int(l)], b[m+int(l):]
		default:
			return nil, errors.New("unexpected wire type " + strconv.Itoa(f.wire))
		}
		out = append(out, f)
	}
	return out, nil
}

func pbKeyValues(fields []pbf, num int) (map[string]any, error) {
	m := map[string]any{}
	for _, f := range fields {
		if f.num != num {
			continue
		}
		kv, err := pbRead(f.b)
		if err != nil {
			return nil, err
		}
		var key string
		var val any
		for _, x := range kv {
			switch x.num {
			case 1:
				key = string(x.b)
			case 2:
				av, err := pbRead(x.b)
				if err != nil || len(av) != 1 {
					return nil, errors.New("bad AnyValue")
				}
				switch a := av[0]; a.num {
				case 1:
					val = string(a.b)
				case 2:
					val = a.v == 1
				case 3:
					val = int64(a.v)
				case 4:
					val = math.Float64frombits(a.v)
				default:
					return nil, errors.New("unexpected AnyValue field")
				}
			}
		}
		m[key] = val
	}
	return m, nil
}

func pbSub(fields []pbf, num int) [][]pbf {
	var out [][]pbf
	for _, f := range fields {
		if f.num == num && f.wire == 2 {
			sub, err := pbRead(f.b)
			if err == nil {
				out = append(out, sub)
			}
		}
	}
	return out
}

func pbGet(fields []pbf, num int) (pbf, bool) {
	for _, f := range fields {
		if f.num == num {
			return f, true
		}
	}
	return pbf{}, false
}

func pbResource(top []pbf) map[string]any {
	for _, r := range pbSub(top, 1) {
		for _, res := range pbSub(r, 1) {
			m, _ := pbKeyValues(res, 1)
			return m
		}
	}
	return nil
}

func decodeTracesProto(b []byte) (map[string]any, []gotSpan, error) {
	top, err := pbRead(b)
	if err != nil {
		return nil, nil, err
	}
	var out []gotSpan
	for _, rs := range pbSub(top, 1) { // ExportTraceServiceRequest.resource_spans
		for _, ss := range pbSub(rs, 2) { // ResourceSpans.scope_spans
			for _, s := range pbSub(ss, 2) { // ScopeSpans.spans
				g := gotSpan{}
				if f, ok := pbGet(s, 1); ok {
					g.TraceID = hex.EncodeToString(f.b)
				}
				if f, ok := pbGet(s, 2); ok {
					g.SpanID = hex.EncodeToString(f.b)
				}
				if f, ok := pbGet(s, 5); ok {
					g.Name = string(f.b)
				}
				if f, ok := pbGet(s, 6); ok {
					g.Kind = int(f.v)
				}
				if f, ok := pbGet(s, 7); ok {
					g.Start = f.v
				}
				if f, ok := pbGet(s, 8); ok {
					g.End = f.v
				}
				for _, st := range pbSub(s, 15) {
					if f, ok := pbGet(st, 3); ok {
						g.StatusCode = int(f.v)
					}
				}
				if g.Attrs, err = pbKeyValues(s, 9); err != nil {
					return nil, nil, err
				}
				out = append(out, g)
			}
		}
	}
	return pbResource(top), out, nil
}

func decodeMetricsProto(b []byte) (map[string]any, []gotMetric, error) {
	top, err := pbRead(b)
	if err != nil {
		return nil, nil, err
	}
	var out []gotMetric
	for _, rm := range pbSub(top, 1) {
		for _, sm := range pbSub(rm, 2) {
			for _, m := range pbSub(sm, 2) {
				g := gotMetric{}
				if f, ok := pbGet(m, 1); ok {
					g.Name = string(f.b)
				}
				if f, ok := pbGet(m, 3); ok {
					g.Unit = string(f.b)
				}
				var data []pbf
				for num, typ := range map[int]string{5: "gauge", 7: "sum", 9: "histogram"} {
					if ds := pbSub(m, num); len(ds) == 1 {
						g.Type, data = typ, ds[0]
					}
				}
				if g.Type == "" {
					return nil, nil, errors.New("metric " + g.Name + " has no data")
				}
				if f, ok := pbGet(data, 2); ok {
					g.Temporality = int(f.v)
				}
				if f, ok := pbGet(data, 3); ok {
					g.Monotonic = f.v == 1
				}
				for _, dp := range pbSub(data, 1) {
					p := gotPoint{}
					if f, ok := pbGet(dp, 3); ok {
						p.Time = f.v
					}
					attrField := 7
					if g.Type == "histogram" {
						attrField = 9
						if f, ok := pbGet(dp, 4); ok {
							p.Count = f.v
						}
						if f, ok := pbGet(dp, 5); ok {
							p.Value = math.Float64frombits(f.v)
						}
					} else if f, ok := pbGet(dp, 4); ok {
						p.Value = math.Float64frombits(f.v)
					}
					if p.Attrs, err = pbKeyValues(dp, attrField); err != nil {
						return nil, nil, err
					}
					g.Points = append(g.Points, p)
				}
				out = append(out, g)
			}
		}
	}
	return pbResource(top), out, nil
}

// --- the exporter's rig ---

// memSecrets is a credential store in memory (the tests' stand-in for Credential Manager).
type memSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSecrets) Put(_ context.Context, ref, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[ref] = v
	return nil
}
func (s *memSecrets) Get(_ context.Context, ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (s *memSecrets) Delete(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, ref)
	return nil
}

type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleeps) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func newExporter(t *testing.T, r *rig, mutate func(*telemetry.ExporterOptions)) (*telemetry.Exporter, *memSecrets, *sleeps) {
	t.Helper()
	sec, sl := &memSecrets{}, &sleeps{}
	o := telemetry.ExporterOptions{Secrets: sec, Sleep: sl.sleep, Interval: time.Hour, BaseDelay: time.Second, MaxDelay: 30 * time.Second, Timeout: 5 * time.Second}
	if mutate != nil {
		mutate(&o)
	}
	x, err := telemetry.NewExporter(r.ctx, r.sink, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.Close)
	return x, sec, sl
}

func ptr[T any](v T) *T { return &v }

func configure(t *testing.T, x *telemetry.Exporter, ch telemetry.OTLPChange) telemetry.OTLPStatus {
	t.Helper()
	st, err := x.Configure(context.Background(), ch, "human:kyle")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// anLLMCall is one Wrapper call as the Wrapper reports it to its tracer.
func anLLMCall() llm.CallTrace {
	start := time.Now().Add(-1500 * time.Millisecond)
	return llm.CallTrace{Purpose: "summarize", Provider: "prov_openrouter", RequestModel: "vendor/model-x", ResponseModel: "vendor/model-x-0901",
		ServedBy: "prov_openrouter", ProjectID: "prj_1", RunID: "run_7", Start: start, End: start.Add(1200 * time.Millisecond),
		Usage: llm.Usage{Input: 900, Output: 120, Cached: 400}, Cost: llm.Cost{USD: 0.0021, Source: llm.CostReported, Known: true}}
}

func forbiddenIn(t *testing.T, what string, b []byte) {
	t.Helper()
	for _, f := range []string{promptText, "refactor", codeText, "chargeCard", toolOutput, "secrets.env", fakeToken(), "Z9q8W7e6", apiKey} {
		if strings.Contains(string(b), f) {
			t.Errorf("%q reached %s", f, what)
		}
	}
}

func TestOTLPSendsExactlyWhatTheSinkRecordsInBothEncodings(t *testing.T) {
	for _, enc := range []string{telemetry.EncodingProtobuf, telemetry.EncodingJSON} {
		t.Run(enc, func(t *testing.T) {
			r := newRig(t)
			col := newCollector(t, false)
			x, sec, _ := newExporter(t, r, nil)
			if st := x.Status(); st.Enabled || st.Active || st.Endpoint != "" {
				t.Fatalf("the exporter is off and has no endpoint until a person gives one: %+v", st)
			}
			if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
				t.Fatal(err)
			}
			r.runLeakyWorkflow() // recorded, not sent: sending is off
			x.Flush()
			if n := len(col.requests()); n != 0 {
				t.Fatalf("sending is off by default, and %d requests reached the collector", n)
			}
			before, _ := r.sink.Status(r.ctx)
			if _, err := x.Configure(r.ctx, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)}, "agent:worker"); err == nil {
				t.Fatal("an agent must not turn sending on")
			}
			st := configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL + "/"), Encoding: ptr(enc), SetHeaders: map[string]string{"X-Api-Key": apiKey}})
			if !st.Active || strings.Join(st.Headers, ",") != "X-Api-Key" || st.ChangedBy != "human:kyle" {
				t.Fatalf("%+v", st)
			}
			r.runLeakyWorkflow()
			r.sink.TraceCall(r.ctx, anLLMCall())
			x.Flush()

			reqs := col.requests()
			if len(reqs) != 2 {
				t.Fatalf("one batch of spans and one of metrics: %d requests", len(reqs))
			}
			allowed := map[string]bool{}
			for _, k := range telemetry.Allowlist() {
				allowed[k] = true
			}
			var spans []gotSpan
			var metrics []gotMetric
			for _, q := range reqs {
				forbiddenIn(t, "the collector", q.Body)
				if q.Header.Get("X-Api-Key") != apiKey {
					t.Errorf("the header was not sent: %v", q.Header)
				}
				if q.Resource["service.name"] != "drydock" || len(q.Resource) != 1 {
					t.Errorf("the resource names the producer and nothing else: %v", q.Resource)
				}
				spans, metrics = append(spans, q.Spans...), append(metrics, q.Metrics...)
			}
			names := map[string]gotSpan{}
			for _, sp := range spans {
				names[sp.Name] = sp
				if len(sp.TraceID) != 32 || len(sp.SpanID) != 16 || sp.End < sp.Start || sp.Start == 0 {
					t.Errorf("span ids and times: %+v", sp)
				}
				for k := range sp.Attrs {
					if !allowed[k] {
						t.Errorf("span attribute %q is not on the allowlist", k)
					}
				}
			}
			for _, want := range []string{"drydock.node.agent_task", "drydock.node.check", "drydock.llm.chat"} {
				if _, ok := names[want]; !ok {
					t.Errorf("no %s span among %v", want, names)
				}
			}
			call := names["drydock.llm.chat"]
			if call.Kind != 3 || call.Attrs[telemetry.AttrGenAIRequestModel] != "vendor/model-x" || call.Attrs[telemetry.AttrGenAIInputTokens] != int64(900) ||
				call.Attrs[telemetry.AttrCostUSD] != 0.0021 || call.Attrs[telemetry.AttrLLMCacheHit] != false || call.Attrs[telemetry.AttrPurpose] != "summarize" {
				t.Errorf("the LLM call's span: %+v", call)
			}
			if names["drydock.node.agent_task"].Kind != 1 || names["drydock.node.agent_task"].TraceID != telemetry.TraceID(fmt0(names["drydock.node.agent_task"].Attrs[telemetry.AttrRunID])) {
				t.Errorf("a node's span is internal and in its run's trace: %+v", names["drydock.node.agent_task"])
			}
			var input float64
			points := 0
			for _, m := range metrics {
				for _, p := range m.Points {
					points++
					for k := range p.Attrs {
						if !allowed[k] {
							t.Errorf("metric attribute %q is not on the allowlist", k)
						}
					}
					if m.Name == telemetry.MetricTokenUsage && p.Attrs[telemetry.AttrGenAITokenType] == "input" {
						input += p.Value
					}
				}
				switch m.Name {
				case telemetry.MetricTokenUsage, telemetry.MetricCostUSD:
					if m.Type != "sum" || m.Temporality != 1 || !m.Monotonic {
						t.Errorf("%s is a monotonic delta sum: %+v", m.Name, m)
					}
				case telemetry.MetricNodeTime, telemetry.MetricLLMTime:
					if m.Type != "histogram" || m.Temporality != 1 || m.Unit != "ms" || m.Points[0].Count != 1 {
						t.Errorf("%s is a delta histogram of one observation per row: %+v", m.Name, m)
					}
				}
			}
			// 6000 from the agent's turn (input with cached tokens, D12) and 900 from the LLM call
			if input != 6900 {
				t.Errorf("input tokens sent: %v", input)
			}
			// what was sent is what the sink holds: the local rows are the log of everything sent
			lst, _ := r.sink.Status(r.ctx)
			if int64(len(spans)) != lst.Spans-before.Spans || int64(points) != lst.Metrics-before.Metrics {
				t.Errorf("the sink recorded %d spans and %d metrics since sending was turned on; %d spans and %d points were sent",
					lst.Spans-before.Spans, lst.Metrics-before.Metrics, len(spans), points)
			}
			st = x.Status()
			if st.SentSpans != int64(len(spans)) || st.SentMetrics != int64(points) || st.LastSuccessAt == 0 || st.LastError != "" || st.Queued != 0 {
				t.Errorf("status %+v (sent %d spans, %d points)", st, len(spans), points)
			}
			if !lst.NetworkExporter || !strings.Contains(lst.Destination, col.srv.URL) || lst.OTLP == nil || !lst.OTLP.Active {
				t.Errorf("the telemetry status says where it goes: %+v", lst)
			}
			// the key is in the credential store, and nowhere in the settings or the status
			if v, _ := sec.Get(r.ctx, "telemetry:otlp:header:x-api-key"); v != apiKey {
				t.Errorf("the header value is not in the credential store: %q", v)
			}
			var settings string
			r.db.Reader().QueryRow(`SELECT group_concat(key||'='||value, ' ') FROM settings`).Scan(&settings)
			sb, _ := json.Marshal(lst)
			forbiddenIn(t, "the settings", []byte(settings))
			forbiddenIn(t, "the status", sb)
		})
	}
}

func fmt0(v any) string { s, _ := v.(string); return s }

func TestOTLPRetriesWhatTheCollectorAsksToRetryWithBackoff(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, false)
	col.answer = func(n int, w http.ResponseWriter) int {
		if n <= 2 {
			w.Header().Set("Retry-After", "3")
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}
	x, _, sl := newExporter(t, r, nil)
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL), Encoding: ptr(telemetry.EncodingJSON)})
	r.sink.TraceCall(r.ctx, llm.CallTrace{Purpose: "classify", Provider: "p1", RequestModel: "m1", Start: time.Now(), End: time.Now(), ErrorType: "rate_limit"})
	x.Flush()
	// spans: 503, 503, then 200; the waits are the Retry-After (3s) over the backoff (1s, 2s)
	if got := sl.all(); len(got) != 2 || got[0] != 3*time.Second || got[1] != 3*time.Second {
		t.Fatalf("waits %v", got)
	}
	st := x.Status()
	if st.SentSpans != 1 || st.DroppedFailed != 0 || st.LastSuccessAt == 0 {
		t.Fatalf("%+v", st)
	}
	spans := 0
	for _, q := range col.requests() {
		for _, sp := range q.Spans {
			spans++
			if sp.StatusCode != 2 || sp.Attrs[telemetry.AttrErrorType] != "rate_limit" || sp.Attrs[telemetry.AttrGenAIInputTokens] != nil {
				t.Errorf("a failed call is an error span with its class and no usage: %+v", sp)
			}
		}
	}
	if spans != 3 {
		t.Fatalf("the span was sent three times (two refused): %d", spans)
	}
}

func TestOTLPDropsAndCountsWhatCannotBeSent(t *testing.T) {
	t.Run("the retries run out", func(t *testing.T) {
		r := newRig(t)
		col := newCollector(t, false)
		col.answer = func(int, http.ResponseWriter) int { return http.StatusBadGateway }
		x, _, sl := newExporter(t, r, func(o *telemetry.ExporterOptions) { o.MaxAttempts = 3 })
		r.sink.SetEnabled(r.ctx, true, "human:kyle")
		configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)})
		r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
		x.Flush()
		if got := sl.all(); len(got) != 2 || got[0] != time.Second || got[1] != 2*time.Second {
			t.Fatalf("exponential backoff: %v", got)
		}
		st := x.Status()
		if st.DroppedFailed != 1 || st.SentSpans != 0 || st.LastError != "the collector answered 502 Bad Gateway" || st.LastErrorAt == 0 {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("the collector refuses and its answer holds the key", func(t *testing.T) {
		r := newRig(t)
		col := newCollector(t, false)
		col.answer = func(_ int, w http.ResponseWriter) int {
			w.Header().Set("X-Echo", apiKey)
			return http.StatusUnauthorized
		}
		x, _, sl := newExporter(t, r, nil)
		r.sink.SetEnabled(r.ctx, true, "human:kyle")
		configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL), SetHeaders: map[string]string{"Authorization": "Bearer " + apiKey}})
		r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
		x.Flush()
		st := x.Status()
		if len(sl.all()) != 0 || st.DroppedFailed != 1 || st.LastError != "the collector answered 401 Unauthorized" {
			t.Fatalf("a refusal is not retried: %v %+v", sl.all(), st)
		}
		if b, _ := json.Marshal(st); strings.Contains(string(b), apiKey) {
			t.Fatal("the key reached the status")
		}
	})
	t.Run("the queue is full", func(t *testing.T) {
		r := newRig(t)
		col := newCollector(t, false)
		x, _, _ := newExporter(t, r, func(o *telemetry.ExporterOptions) { o.QueueSize, o.BatchSize = 3, 100 })
		r.sink.SetEnabled(r.ctx, true, "human:kyle")
		configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)})
		for i := 0; i < 5; i++ {
			r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
		}
		if st := x.Status(); st.Queued != 3 || st.DroppedOverflow != 2 {
			t.Fatalf("%+v", st)
		}
		x.Flush()
		if st := x.Status(); st.SentSpans != 3 || st.Queued != 0 {
			t.Fatalf("%+v", st)
		}
		if st, _ := r.sink.Status(r.ctx); st.Spans != 5 {
			t.Fatalf("what was dropped from the queue is still recorded locally: %+v", st)
		}
	})
	t.Run("a dead endpoint", func(t *testing.T) {
		r := newRig(t)
		col := newCollector(t, false)
		url := col.srv.URL
		col.srv.Close()
		x, _, _ := newExporter(t, r, func(o *telemetry.ExporterOptions) { o.MaxAttempts = 2 })
		r.sink.SetEnabled(r.ctx, true, "human:kyle")
		configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(url)})
		r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
		x.Flush()
		if st := x.Status(); st.DroppedFailed != 1 || !strings.HasPrefix(st.LastError, "could not reach the collector: ") {
			t.Fatalf("%+v", st)
		}
	})
}

func TestOTLPRevokingConsentStopsSendingAtOnce(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, false)
	col.hold = make(chan struct{})
	x, _, _ := newExporter(t, r, nil)
	x.Start()
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)})
	r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
	done := make(chan struct{})
	go func() { x.Flush(); close(done) }()
	time.Sleep(100 * time.Millisecond) // the request is now held by the stand-in
	for i := 0; i < 3; i++ {
		r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
	}
	if err := r.sink.SetEnabled(r.ctx, false, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("turning telemetry off did not stop the send in flight")
	}
	close(col.hold)
	if st := x.Status(); st.Queued != 0 || st.Active || st.SentSpans != 0 || st.DroppedFailed != 0 {
		t.Fatalf("the queue is emptied and the cut send is not counted as a failure: %+v", st)
	}
	r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
	x.Flush()
	if n := len(col.requests()); n != 0 {
		t.Fatalf("%d requests completed after consent was revoked", n)
	}
	// the exporter's own switch does the same
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	r.sink.RecordSpan(r.ctx, telemetry.Span{TraceID: telemetry.TraceID("r"), SpanID: telemetry.NewSpanID(), Name: "drydock.node.x", StartMS: 1, EndMS: 2})
	if st := configure(t, x, telemetry.OTLPChange{Enabled: ptr(false)}); st.Queued != 0 || st.Active {
		t.Fatalf("%+v", st)
	}
	x.Close()
	x.Wait()
}

func TestOTLPEndpointAndHeaderRules(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:4318", "http://localhost:4318/otlp", "http://[::1]:4318", "https://collector.example.com:4318", "https://10.0.0.5/otlp"} {
		if err := telemetry.CheckEndpoint(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for bad, want := range map[string]string{
		"http://10.0.0.5:4318":          "plain http is allowed only to this computer",
		"http://collector.example.com":  "plain http is allowed only to this computer",
		"https://user:pw@collector.com": "user name or password",
		"https://collector.com/?key=1":  "query",
		"ftp://collector.com":           "must start with https://",
		"collector.com:4318":            "must",
		"":                              "must be a URL",
	} {
		if err := telemetry.CheckEndpoint(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v (want %q)", bad, err, want)
		}
	}
	r := newRig(t)
	x, sec, _ := newExporter(t, r, nil)
	for _, ch := range []telemetry.OTLPChange{
		{Enabled: ptr(true)}, // no endpoint
		{Endpoint: ptr("http://192.0.2.10:4318")},
		{Encoding: ptr("yaml")},
		{SetHeaders: map[string]string{"Content-Type": "text/plain"}},
		{SetHeaders: map[string]string{"X-Key": "one\r\nX-Other: two"}},
		{SetHeaders: map[string]string{"bad header": "v"}},
	} {
		if _, err := x.Configure(r.ctx, ch, "human:kyle"); err == nil {
			t.Errorf("accepted %+v", ch)
		}
	}
	configure(t, x, telemetry.OTLPChange{Endpoint: ptr("https://collector.example.com"), SetHeaders: map[string]string{"X-Key": "v1-abcdefghijkl", "X-Tenant": "t1"}})
	st := configure(t, x, telemetry.OTLPChange{RemoveHeaders: []string{"x-key"}})
	if strings.Join(st.Headers, ",") != "X-Tenant" {
		t.Fatalf("%v", st.Headers)
	}
	if _, err := sec.Get(r.ctx, "telemetry:otlp:header:x-key"); err == nil {
		t.Fatal("a removed header's value stays in the credential store")
	}
	// the configuration survives a restart; the values stay where they are
	again, err := telemetry.NewExporter(r.ctx, r.sink, telemetry.ExporterOptions{Secrets: sec})
	if err != nil {
		t.Fatal(err)
	}
	if st := again.Status(); st.Endpoint != "https://collector.example.com" || strings.Join(st.Headers, ",") != "X-Tenant" || st.Encoding != telemetry.EncodingProtobuf {
		t.Fatalf("%+v", st)
	}
	// without a credential store no header can be given
	bare, _ := telemetry.NewExporter(r.ctx, r.sink, telemetry.ExporterOptions{})
	if _, err := bare.Configure(r.ctx, telemetry.OTLPChange{SetHeaders: map[string]string{"X-Key": "v"}}, "human:kyle"); err == nil || !strings.Contains(err.Error(), "credential store") {
		t.Fatalf("%v", err)
	}
}

func TestOTLPTestSpanIsSentNowAndKeptLocally(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, true) // https, through the stand-in's own certificate
	x, _, _ := newExporter(t, r, func(o *telemetry.ExporterOptions) { o.HTTPS = col.srv.Client().Transport })
	if _, err := x.SendTest(r.ctx, "human:kyle"); err == nil {
		t.Fatal("a test span needs an endpoint")
	}
	configure(t, x, telemetry.OTLPChange{Endpoint: ptr(col.srv.URL), SetHeaders: map[string]string{"X-Api-Key": apiKey}})
	// sending is off and telemetry is off: the test is a person's own request, so it goes anyway
	res, err := x.SendTest(r.ctx, "human:kyle")
	if err != nil || !res.OK || res.Status.LastSuccessAt == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	reqs := col.requests()
	if len(reqs) != 1 || len(reqs[0].Spans) != 1 || reqs[0].Spans[0].Name != "drydock.telemetry.test" || reqs[0].Header.Get("X-Api-Key") != apiKey {
		t.Fatalf("%+v", reqs)
	}
	if st, _ := r.sink.Status(r.ctx); st.Spans != 1 {
		t.Fatalf("the test span is in the local log of what was sent: %+v", st)
	}
	col.answer = func(int, http.ResponseWriter) int { return http.StatusInternalServerError }
	res, err = x.SendTest(r.ctx, "human:kyle")
	if err != nil || res.OK || res.Error != "the collector answered 500 Internal Server Error" || res.Status.LastError != res.Error {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := x.SendTest(r.ctx, "agent:worker"); err == nil {
		t.Fatal("an agent must not send a test span")
	}
}

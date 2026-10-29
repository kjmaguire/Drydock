package telemetry

// The OTLP/HTTP request bodies, written by hand (decision P113) rather than through the OpenTelemetry SDK and its generated
// protobuf types: the spans and metrics already exist in the sink, so only their wire form is needed. Both encodings follow
// opentelemetry-proto (collector/trace/v1 ExportTraceServiceRequest, collector/metrics/v1 ExportMetricsServiceRequest): the JSON
// one with hex trace and span ids, 64-bit integers as decimal strings and enums as numbers, as the OTLP specification asks.
//
// Every attribute is put through Sanitize again here, so whatever reaches an encoder, only allowlisted identifier-shaped values
// can be written.

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// OTLP enum values (opentelemetry-proto trace.proto and metrics.proto).
const (
	spanKindInternal       = 1
	spanKindClient         = 3
	statusCodeError        = 2
	aggregationDelta       = 1
	scopeName              = "drydock"
	serviceNameResourceKey = "service.name"
)

// otlpAttr is one attribute in its OTLP shape.
type otlpAttr struct {
	key  string
	kind valueKind
	s    string
	i    int64
	f    float64
	b    bool
}

func otlpAttrs(in map[string]any) []otlpAttr {
	clean, _ := Sanitize(in, nil)
	keys := make([]string, 0, len(clean))
	for k := range clean {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]otlpAttr, 0, len(keys))
	for _, k := range keys {
		a := otlpAttr{key: k, kind: allowed[k]}
		switch v := clean[k].(type) {
		case string:
			a.s = v
		case int64:
			a.i = v
		case float64:
			a.f = v
		case bool:
			a.b = v
		}
		out = append(out, a)
	}
	return out
}

func (a otlpAttr) json() map[string]any {
	var v map[string]any
	switch a.kind {
	case kindInt:
		v = map[string]any{"intValue": strconv.FormatInt(a.i, 10)}
	case kindFloat:
		v = map[string]any{"doubleValue": a.f}
	case kindBool:
		v = map[string]any{"boolValue": a.b}
	default:
		v = map[string]any{"stringValue": a.s}
	}
	return map[string]any{"key": a.key, "value": v}
}

func jsonAttrs(in map[string]any) []map[string]any {
	as := otlpAttrs(in)
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, a.json())
	}
	return out
}

// hexID returns id when it is n hex characters, otherwise a stable id of that length derived from it (a span the sink holds
// always has proper ids; this keeps a malformed one from making the whole request invalid).
func hexID(id string, n int) string {
	if len(id) == n {
		if _, err := hex.DecodeString(id); err == nil {
			return strings.ToLower(id)
		}
	}
	t := TraceID("malformed:" + id)
	return t[:n]
}

func spanKind(name string) int {
	if strings.HasPrefix(name, "drydock.llm.") {
		return spanKindClient // a call to a model provider
	}
	return spanKindInternal
}

func nanos(ms int64) uint64 {
	if ms <= 0 {
		return 0
	}
	return uint64(ms) * 1_000_000
}

func resourceJSON() map[string]any {
	return map[string]any{"attributes": []map[string]any{{"key": serviceNameResourceKey, "value": map[string]any{"stringValue": "drydock"}}}}
}

// tracesJSON is an ExportTraceServiceRequest in OTLP's JSON encoding.
func tracesJSON(spans []Span) []byte {
	out := make([]map[string]any, 0, len(spans))
	for _, sp := range spans {
		m := map[string]any{"traceId": hexID(sp.TraceID, 32), "spanId": hexID(sp.SpanID, 16), "name": onlyIdentOr(sp.Name, "unnamed"),
			"kind": spanKind(sp.Name), "startTimeUnixNano": strconv.FormatUint(nanos(sp.StartMS), 10), "endTimeUnixNano": strconv.FormatUint(nanos(sp.EndMS), 10),
			"attributes": jsonAttrs(sp.Attrs), "status": map[string]any{}}
		if sp.ParentID != "" {
			m["parentSpanId"] = hexID(sp.ParentID, 16)
		}
		if sp.Status == "error" {
			m["status"] = map[string]any{"code": statusCodeError}
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"resource": resourceJSON(),
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": scopeName}, "spans": out}}}}})
	return b
}

// metricGroup is the data points of one metric (name, kind and unit).
type metricGroup struct {
	name, kind, unit string
	points           []Metric
}

func groupMetrics(ms []Metric) []metricGroup {
	var out []metricGroup
	idx := map[string]int{}
	for _, m := range ms {
		if !identValue.MatchString(m.Name) {
			continue
		}
		kind := m.Kind
		switch kind {
		case "counter", "histogram", "gauge":
		default:
			kind = "gauge"
		}
		k := m.Name + "\x00" + kind + "\x00" + onlyIdent(m.Unit)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, metricGroup{name: m.Name, kind: kind, unit: onlyIdent(m.Unit)})
		}
		out[i].points = append(out[i].points, m)
	}
	return out
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// metricsJSON is an ExportMetricsServiceRequest in OTLP's JSON encoding. Each recorded row is one delta data point: a counter
// row is a monotonic delta sum, a histogram row one observation (a single bucket holding it, with its sum, min and max).
func metricsJSON(ms []Metric) []byte {
	var out []map[string]any
	for _, g := range groupMetrics(ms) {
		var pts []map[string]any
		for _, p := range g.points {
			t := strconv.FormatUint(nanos(p.TimeMS), 10)
			v := finite(p.Value)
			pt := map[string]any{"attributes": jsonAttrs(p.Attrs), "startTimeUnixNano": t, "timeUnixNano": t}
			if g.kind == "histogram" {
				pt["count"], pt["sum"], pt["min"], pt["max"], pt["bucketCounts"], pt["explicitBounds"] = "1", v, v, v, []string{"1"}, []float64{}
			} else {
				pt["asDouble"] = v
			}
			pts = append(pts, pt)
		}
		m := map[string]any{"name": g.name}
		if g.unit != "" {
			m["unit"] = g.unit
		}
		switch g.kind {
		case "counter":
			m["sum"] = map[string]any{"dataPoints": pts, "aggregationTemporality": aggregationDelta, "isMonotonic": true}
		case "histogram":
			m["histogram"] = map[string]any{"dataPoints": pts, "aggregationTemporality": aggregationDelta}
		default:
			m["gauge"] = map[string]any{"dataPoints": pts}
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(map[string]any{"resourceMetrics": []any{map[string]any{"resource": resourceJSON(),
		"scopeMetrics": []any{map[string]any{"scope": map[string]any{"name": scopeName}, "metrics": out}}}}})
	return b
}

func onlyIdentOr(s, or string) string {
	if identValue.MatchString(s) {
		return s
	}
	return or
}

// --- protobuf ---

// pb is a protobuf writer for the few wire types OTLP uses.
type pb struct{ b []byte }

const (
	wireVarint = 0
	wireI64    = 1
	wireLen    = 2
)

func (p *pb) tag(field, wire int) { p.b = binary.AppendUvarint(p.b, uint64(field)<<3|uint64(wire)) }
func (p *pb) uvarint(field int, v uint64) {
	p.tag(field, wireVarint)
	p.b = binary.AppendUvarint(p.b, v)
}
func (p *pb) fixed64(field int, v uint64) {
	p.tag(field, wireI64)
	p.b = binary.LittleEndian.AppendUint64(p.b, v)
}
func (p *pb) double(field int, v float64) { p.fixed64(field, math.Float64bits(v)) }
func (p *pb) bytes(field int, b []byte) {
	p.tag(field, wireLen)
	p.b = binary.AppendUvarint(p.b, uint64(len(b)))
	p.b = append(p.b, b...)
}
func (p *pb) str(field int, s string) { p.bytes(field, []byte(s)) }
func (p *pb) msg(field int, f func(q *pb)) {
	var q pb
	f(&q)
	p.bytes(field, q.b)
}

// common.proto: KeyValue{key=1, value=2}; AnyValue{string_value=1, bool_value=2, int_value=3, double_value=4}.
func (p *pb) attrs(field int, in map[string]any) {
	for _, a := range otlpAttrs(in) {
		p.msg(field, func(kv *pb) {
			kv.str(1, a.key)
			kv.msg(2, func(v *pb) {
				switch a.kind {
				case kindInt:
					v.uvarint(3, uint64(a.i))
				case kindFloat:
					v.double(4, a.f)
				case kindBool:
					b := uint64(0)
					if a.b {
						b = 1
					}
					v.uvarint(2, b)
				default:
					v.str(1, a.s)
				}
			})
		})
	}
}

// resource.proto: Resource{attributes=1}, holding service.name only (it is not an allowlisted span attribute, so it is written
// here directly); common.proto: InstrumentationScope{name=1}.
func (p *pb) resource(field int) {
	p.msg(field, func(r *pb) {
		r.msg(1, func(kv *pb) { kv.str(1, serviceNameResourceKey); kv.msg(2, func(v *pb) { v.str(1, "drydock") }) })
	})
}

func (p *pb) scope(field int) { p.msg(field, func(s *pb) { s.str(1, scopeName) }) }

// tracesProto is an ExportTraceServiceRequest{resource_spans=1}: ResourceSpans{resource=1, scope_spans=2}, ScopeSpans{scope=1,
// spans=2}, Span{trace_id=1, span_id=2, parent_span_id=4, name=5, kind=6, start_time_unix_nano=7, end_time_unix_nano=8,
// attributes=9, status=15}, Status{code=3}.
func tracesProto(spans []Span) []byte {
	var p pb
	p.msg(1, func(rs *pb) {
		rs.resource(1)
		rs.msg(2, func(ss *pb) {
			ss.scope(1)
			for _, sp := range spans {
				ss.msg(2, func(s *pb) {
					tid, _ := hex.DecodeString(hexID(sp.TraceID, 32))
					sid, _ := hex.DecodeString(hexID(sp.SpanID, 16))
					s.bytes(1, tid)
					s.bytes(2, sid)
					if sp.ParentID != "" {
						pid, _ := hex.DecodeString(hexID(sp.ParentID, 16))
						s.bytes(4, pid)
					}
					s.str(5, onlyIdentOr(sp.Name, "unnamed"))
					s.uvarint(6, uint64(spanKind(sp.Name)))
					s.fixed64(7, nanos(sp.StartMS))
					s.fixed64(8, nanos(sp.EndMS))
					s.attrs(9, sp.Attrs)
					s.msg(15, func(st *pb) {
						if sp.Status == "error" {
							st.uvarint(3, statusCodeError)
						}
					})
				})
			}
		})
	})
	return p.b
}

// metricsProto is an ExportMetricsServiceRequest{resource_metrics=1}: ResourceMetrics{resource=1, scope_metrics=2},
// ScopeMetrics{scope=1, metrics=2}, Metric{name=1, unit=3, gauge=5, sum=7, histogram=9}, Gauge{data_points=1},
// Sum{data_points=1, aggregation_temporality=2, is_monotonic=3}, Histogram{data_points=1, aggregation_temporality=2},
// NumberDataPoint{start_time_unix_nano=2, time_unix_nano=3, as_double=4, attributes=7}, HistogramDataPoint{
// start_time_unix_nano=2, time_unix_nano=3, count=4, sum=5, bucket_counts=6, explicit_bounds=7, attributes=9, min=11, max=12}.
func metricsProto(ms []Metric) []byte {
	var p pb
	p.msg(1, func(rm *pb) {
		rm.resource(1)
		rm.msg(2, func(sm *pb) {
			sm.scope(1)
			for _, g := range groupMetrics(ms) {
				sm.msg(2, func(m *pb) {
					m.str(1, g.name)
					if g.unit != "" {
						m.str(3, g.unit)
					}
					field := map[string]int{"gauge": 5, "counter": 7, "histogram": 9}[g.kind]
					m.msg(field, func(d *pb) {
						for _, pt := range g.points {
							t := nanos(pt.TimeMS)
							v := finite(pt.Value)
							d.msg(1, func(dp *pb) {
								dp.fixed64(2, t)
								dp.fixed64(3, t)
								if g.kind == "histogram" {
									dp.fixed64(4, 1)
									dp.double(5, v)
									var one pb
									one.b = binary.LittleEndian.AppendUint64(one.b, 1)
									dp.bytes(6, one.b) // packed: one bucket, holding the observation
									dp.attrs(9, pt.Attrs)
									dp.double(11, v)
									dp.double(12, v)
								} else {
									dp.double(4, v)
									dp.attrs(7, pt.Attrs)
								}
							})
						}
						switch g.kind {
						case "counter":
							d.uvarint(2, aggregationDelta)
							d.uvarint(3, 1)
						case "histogram":
							d.uvarint(2, aggregationDelta)
						}
					})
				})
			}
		})
	})
	return p.b
}

// rejectedProto reads partial_success (field 1) of an Export*ServiceResponse: rejected_spans or rejected_data_points (field 1).
func rejectedProto(b []byte) int64 {
	inner, ok := pbField(b, 1, wireLen)
	if !ok {
		return 0
	}
	v, ok := pbField(inner, 1, wireVarint)
	if !ok {
		return 0
	}
	n, _ := binary.Uvarint(v)
	return int64(n)
}

// pbField returns the raw value of the first occurrence of field with the given wire type (a varint's bytes, a length-delimited
// field's content). It skips every other field and stops at anything malformed.
func pbField(b []byte, field, wire int) ([]byte, bool) {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, false
		}
		b = b[n:]
		f, w := int(key>>3), int(key&7)
		var val []byte
		switch w {
		case wireVarint:
			_, m := binary.Uvarint(b)
			if m <= 0 {
				return nil, false
			}
			val, b = b[:m], b[m:]
		case wireI64:
			if len(b) < 8 {
				return nil, false
			}
			val, b = b[:8], b[8:]
		case wireLen:
			l, m := binary.Uvarint(b)
			if m <= 0 || uint64(len(b)-m) < l {
				return nil, false
			}
			val, b = b[m:m+int(l)], b[m+int(l):]
		case 5: // I32
			if len(b) < 4 {
				return nil, false
			}
			val, b = b[:4], b[4:]
		default:
			return nil, false
		}
		if f == field && w == wire {
			return val, true
		}
	}
	return nil, false
}

// rejectedJSON reads partialSuccess of an Export*ServiceResponse in JSON (the counts may be strings or numbers).
func rejectedJSON(b []byte) int64 {
	var r struct {
		PartialSuccess struct {
			RejectedSpans      json.RawMessage `json:"rejectedSpans"`
			RejectedDataPoints json.RawMessage `json:"rejectedDataPoints"`
		} `json:"partialSuccess"`
	}
	if json.Unmarshal(b, &r) != nil {
		return 0
	}
	for _, raw := range []json.RawMessage{r.PartialSuccess.RejectedSpans, r.PartialSuccess.RejectedDataPoints} {
		if n, err := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

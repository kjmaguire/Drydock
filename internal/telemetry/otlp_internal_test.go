package telemetry

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Plain http is checked where the connection is made, not only on the name: a "localhost" that resolved to another computer,
// or any other address, is refused before a byte is sent. 192.0.2.1 is TEST-NET-1 (RFC 5737): nothing answers there.
func TestPlainHTTPConnectsOnlyToThisComputer(t *testing.T) {
	x := &Exporter{opt: ExporterOptions{Timeout: 2 * time.Second}}
	req, _ := http.NewRequest(http.MethodPost, "http://192.0.2.1:4318/v1/traces", nil)
	_, err := x.transport("http").RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "plain http goes only to this computer") {
		t.Fatalf("%v", err)
	}
}

func TestEachSignalHasItsPathAfterTheEndpoint(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://collector.example.com:4318":       "https://collector.example.com:4318/v1/traces",
		"https://collector.example.com:4318/":      "https://collector.example.com:4318/v1/traces",
		"https://gateway.example.com/otlp/":        "https://gateway.example.com/otlp/v1/traces",
		"http://127.0.0.1:4318/a%20b":              "http://127.0.0.1:4318/a%20b/v1/traces",
		"https://collector.example.com:4318/v1/ok": "https://collector.example.com:4318/v1/ok/v1/traces",
	} {
		u, err := endpointURL(endpoint, "/v1/traces")
		if err != nil || u.String() != want {
			t.Errorf("%s: %v %v, want %s", endpoint, u, err, want)
		}
	}
}

func TestRetryAfterReadsSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{"": 0, "7": 7 * time.Second, "soon": 0, "-3": 0,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("%q: %s, want %s", v, got, want)
		}
	}
}

// The protobuf writer's output, read back with the response reader's own field walker (the field numbers are
// opentelemetry-proto's; the stand-in collector in otlp_test.go decodes whole requests).
func TestPartialSuccessIsReadInBothEncodings(t *testing.T) {
	var p pb
	p.msg(1, func(ps *pb) { ps.uvarint(1, 4); ps.str(2, "some were bad") })
	if n := rejectedProto(p.b); n != 4 {
		t.Fatalf("protobuf: %d", n)
	}
	if n := rejectedProto(nil); n != 0 {
		t.Fatalf("an empty answer rejects nothing: %d", n)
	}
	for body, want := range map[string]int64{`{}`: 0, `{"partialSuccess":{"rejectedSpans":"3"}}`: 3, `{"partialSuccess":{"rejectedDataPoints":2}}`: 2, `not json`: 0} {
		if n := rejectedJSON([]byte(body)); n != want {
			t.Errorf("%s: %d, want %d", body, n, want)
		}
	}
}

package telemetry_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

// Decision P126 removed P121's content export. The rule is absolute again: a span of a Wrapper call is never stored or
// encoded with prompt or completion text, whatever is turned on. This runs the real Wrapper (against the llmtest PROTOCOL
// FIXTURE) with telemetry on, OTLP sending on in both encodings, "Also send what was recorded before" chosen, and the
// setting rows a build with P121 may have stored for the removed switch, then reads the stored rows and what the collector
// stand-in (a TEST DOUBLE that decodes every request) received.

var identShaped = regexp.MustCompile(`^[A-Za-z0-9._:/@+#-]{1,96}$`)

func TestWrapperSpansHoldOnlyAllowlistedIdentifiersWhateverIsOn(t *testing.T) {
	const system = "You summarize a task's room."
	answer := codeText + " " + toolOutput
	allowed := map[string]bool{}
	for _, k := range telemetry.Allowlist() {
		allowed[k] = true
	}
	onlyIdentifiers := func(t *testing.T, where string, attrs map[string]any) {
		t.Helper()
		for k, v := range attrs {
			if !allowed[k] {
				t.Errorf("%s: %q is not an allowlisted attribute", where, k)
			}
			if s, ok := v.(string); ok && !identShaped.MatchString(s) {
				t.Errorf("%s: %s is not identifier-shaped: %q", where, k, s)
			}
		}
	}
	noText := func(t *testing.T, where string, b []byte) {
		t.Helper()
		forbiddenIn(t, where, b)
		for _, f := range []string{system, "summarize a task", "gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions"} {
			if strings.Contains(string(b), f) {
				t.Errorf("%q reached %s", f, where)
			}
		}
	}

	for _, enc := range []string{telemetry.EncodingProtobuf, telemetry.EncodingJSON} {
		t.Run(enc, func(t *testing.T) {
			r := newRig(t)
			// what a build with P121 stored when a person turned the removed switch on: read by nothing now
			for k, v := range map[string]string{"telemetry.content.enabled": "1", "telemetry.content.changed_by": "human:kyle", "telemetry.content.changed_at": "1761760000000"} {
				if _, err := r.db.Writer().Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
					t.Fatal(err)
				}
			}
			sink, err := telemetry.New(r.ctx, r.db, telemetry.Options{Redact: scanner(t).Redact})
			if err != nil {
				t.Fatal(err)
			}
			r.sink = sink
			if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
				t.Fatal(err)
			}
			col := newCollector(t, false)
			x, _, _ := newExporter(t, r, nil)
			w, req := wrapperRig(t, r, answer)
			req.System = []llm.Block{{Text: system}}
			req.Messages = []llm.Message{{Role: "user", Content: promptText},
				{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"code":` + jsonString(codeText) + `}`}}},
				{Role: "tool", ToolCallID: "c1", Content: toolOutput}}

			// recorded before sending is turned on: a call, and one the secret guard refuses
			if _, err := w.Complete(r.ctx, req); err != nil {
				t.Fatal(err)
			}
			leak := req
			leak.Messages = []llm.Message{{Role: "user", Content: promptText + " " + fakeToken()}}
			if _, err := w.Complete(r.ctx, leak); err == nil {
				t.Fatal("the scanner let a token out")
			}
			// sending on, with what was recorded before
			configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL), Encoding: ptr(enc),
				Backfill: &telemetry.Backfill{SinceMS: time.Now().Add(-time.Hour).UnixMilli()}})
			x.Flush()
			// recorded while sending: a call and a streamed call
			if _, err := w.Complete(r.ctx, req); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Stream(r.ctx, req, func(llm.Delta) error { return nil }); err != nil {
				t.Fatal(err)
			}
			x.Flush()

			var calls int
			for _, q := range col.requests() {
				noText(t, "the collector", q.Body)
				noText(t, "the wire", q.Wire)
				for _, sp := range q.Spans {
					onlyIdentifiers(t, "a sent span", sp.Attrs)
					if sp.Name == "drydock.llm.chat" {
						calls++
					}
				}
				for _, m := range q.Metrics {
					for _, p := range m.Points {
						onlyIdentifiers(t, "a sent point", p.Attrs)
					}
				}
			}
			if calls != 4 {
				t.Fatalf("the four calls' spans, two of them recorded before sending was on: %d", calls)
			}

			// the stored rows, which are what an exporter sends, and the preview of them
			rows, err := r.db.Reader().Query(`SELECT attrs FROM telemetry_spans`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				var raw string
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				noText(t, "a stored span", []byte(raw))
				var attrs map[string]any
				if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
					t.Fatal(err)
				}
				onlyIdentifiers(t, "a stored span", attrs)
				n++
			}
			if n != 4 {
				t.Fatalf("%d stored spans", n)
			}
			doc, err := r.sink.Preview(r.ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(doc)
			noText(t, "the preview", raw)
			if !strings.Contains(doc.Note, "no prompts") || strings.Join(doc.Allowlist, ",") != strings.Join(telemetry.Allowlist(), ",") {
				t.Fatalf("the preview says what it can hold: %s %v", doc.Note, doc.Allowlist)
			}
			st, _ := r.sink.Status(r.ctx)
			b, _ := json.Marshal(st)
			if strings.Contains(string(b), `"content"`) {
				t.Fatalf("the status still reports a content switch: %s", b)
			}
		})
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

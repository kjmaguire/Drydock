package telemetry_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/backends"
	"github.com/kjmaguire/drydock/internal/llm/llmtest"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security/secretadapt"
	"github.com/kjmaguire/drydock/internal/telemetry"
)

// Decision P121: gzip bodies, "Also send what was recorded before", content export and usage counters, each against the
// collector stand-in (a TEST DOUBLE that gunzips and decodes every request) and the real sink.

func TestOTLPGzipIsOnForHTTPSByDefaultAndASetting(t *testing.T) {
	r := newRig(t)
	if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	tlsCol := newCollector(t, true)
	x, _, _ := newExporter(t, r, func(o *telemetry.ExporterOptions) { o.HTTPS = tlsCol.srv.Client().Transport })
	st := configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(tlsCol.srv.URL)})
	if st.Compression != telemetry.CompressionAuto || !st.Gzip {
		t.Fatalf("https compresses by default: %+v", st)
	}
	for i := 0; i < 40; i++ {
		r.sink.TraceCall(r.ctx, anLLMCall())
	}
	x.Flush()
	reqs := tlsCol.requests()
	if len(reqs) != 2 {
		t.Fatalf("one request of spans and one of metrics: %d", len(reqs))
	}
	spans := 0
	for _, q := range reqs {
		if !q.Gzip || q.Header.Get("Content-Encoding") != "gzip" || len(q.Wire) >= len(q.Body) {
			t.Errorf("%s: gzip=%v, %d bytes on the wire for a %d byte body", q.Path, q.Gzip, len(q.Wire), len(q.Body))
		}
		spans += len(q.Spans)
	}
	if spans != 40 {
		t.Fatalf("every span arrives whole after gunzip: %d", spans)
	}

	// a collector on this computer: off by default, on when chosen, and off again when chosen for https
	col := newCollector(t, false)
	if st := configure(t, x, telemetry.OTLPChange{Endpoint: ptr(col.srv.URL)}); st.Gzip {
		t.Fatalf("plain http to this computer is not compressed by default: %+v", st)
	}
	r.sink.TraceCall(r.ctx, anLLMCall())
	x.Flush()
	if q := col.requests(); len(q) == 0 || q[0].Gzip {
		t.Fatalf("%+v", q)
	}
	if st := configure(t, x, telemetry.OTLPChange{Compression: ptr(telemetry.CompressionGzip)}); !st.Gzip || st.Compression != "gzip" {
		t.Fatalf("%+v", st)
	}
	r.sink.TraceCall(r.ctx, anLLMCall())
	x.Flush()
	q := col.requests()
	if last := q[len(q)-1]; !last.Gzip || len(last.Metrics)+len(last.Spans) == 0 {
		t.Fatalf("chosen gzip: %+v", last)
	}
	configure(t, x, telemetry.OTLPChange{Endpoint: ptr(tlsCol.srv.URL), Compression: ptr(telemetry.CompressionNone)})
	before := len(tlsCol.requests())
	r.sink.TraceCall(r.ctx, anLLMCall())
	x.Flush()
	if q := tlsCol.requests(); len(q) == before || q[len(q)-1].Gzip {
		t.Fatalf("chosen none, over https too: %+v", q[before:])
	}
	if _, err := x.Configure(r.ctx, telemetry.OTLPChange{Compression: ptr("zstd")}, "human:kyle"); err == nil {
		t.Fatal("only gzip, none or automatic")
	}
	// the setting survives a restart
	x.Close()
	y, _, _ := newExporter(t, r, nil)
	if st := y.Status(); st.Compression != telemetry.CompressionNone || st.Gzip {
		t.Fatalf("after a restart: %+v", st)
	}
}

// spanAt records an LLM call that ended at end, and returns its run id (each span names one).
func spanAt(t *testing.T, r *rig, run string, end time.Time) {
	t.Helper()
	c := anLLMCall()
	c.RunID, c.Start, c.End = run, end.Add(-time.Second), end
	r.sink.TraceCall(r.ctx, c)
}

func sentRuns(col *collectorStandIn) map[string]int {
	out := map[string]int{}
	for _, q := range col.requests() {
		for _, sp := range q.Spans {
			out[fmt0(sp.Attrs[telemetry.AttrRunID])]++
		}
	}
	return out
}

func TestOTLPSendsWhatWasRecordedBeforeOnlyWhenChosenAndBounded(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, false)
	x, _, _ := newExporter(t, r, nil)
	if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	spanAt(t, r, "run_old", now.Add(-20*24*time.Hour)) // before the chosen date
	for _, run := range []string{"run_a", "run_b", "run_c"} {
		spanAt(t, r, run, now.Add(-2*time.Hour))
	}
	spanAt(t, r, "run_d", now.Add(-time.Hour))

	// the default: only what is recorded from now on
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)})
	x.Flush()
	if n := len(col.requests()); n != 0 {
		t.Fatalf("nothing recorded before is sent unless chosen: %d requests", n)
	}
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(false)})

	// the bounds are checked: the change must turn sending on, the date must be within the retention window and not ahead,
	// the cap at most MaxBackfillRows, and telemetry on
	since := now.Add(-7 * 24 * time.Hour).UnixMilli()
	for name, ch := range map[string]telemetry.OTLPChange{
		"not turning it on":  {Backfill: &telemetry.Backfill{SinceMS: since}},
		"no date":            {Enabled: ptr(true), Backfill: &telemetry.Backfill{}},
		"past retention":     {Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: now.Add(-31 * 24 * time.Hour).UnixMilli()}},
		"in the future":      {Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: now.Add(time.Hour).UnixMilli()}},
		"too many rows":      {Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since, MaxRows: telemetry.MaxBackfillRows + 1}},
		"a negative row cap": {Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since, MaxRows: -1}},
	} {
		if _, err := x.Configure(r.ctx, ch, "human:kyle"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if st := x.Status(); st.Enabled {
		t.Fatalf("a refused change turned nothing on: %+v", st)
	}
	if _, err := x.Configure(r.ctx, telemetry.OTLPChange{Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since}}, "agent:worker"); err == nil {
		t.Fatal("an agent must not send what was recorded before")
	}

	// chosen: since a week ago, at most the 6 newest rows (spans and metrics together)
	st := configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since, MaxRows: 6}})
	if st.LastBackfill == nil || st.LastBackfill.Queued != 6 || st.LastBackfill.MaxRows != 6 || st.LastBackfill.SinceMS != since || st.LastBackfill.By != "human:kyle" {
		t.Fatalf("%+v", st.LastBackfill)
	}
	// each call records a span and four metrics (duration, input and output tokens, cost); four calls are in the window: 4 spans
	// and 16 metrics, so the cap leaves 14 out
	if st.LastBackfill.Left != 14 {
		t.Fatalf("rows in the window the cap left out: %+v", st.LastBackfill)
	}
	x.Flush()
	runs := sentRuns(col)
	if runs["run_old"] != 0 || runs["run_d"] != 1 {
		t.Fatalf("the newest rows since the date, never older: %v", runs)
	}
	sent := 0
	for _, q := range col.requests() {
		sent += len(q.Spans)
		for _, m := range q.Metrics {
			sent += len(m.Points)
		}
	}
	if sent != 6 {
		t.Fatalf("the cap: %d rows sent", sent)
	}
	// the rows the collector accepted are marked sent; a second backfill never sends them again
	var marked int
	r.db.Reader().QueryRow(`SELECT (SELECT COUNT(*) FROM telemetry_spans WHERE otlp_sent_at IS NOT NULL)+(SELECT COUNT(*) FROM telemetry_metrics WHERE otlp_sent_at IS NOT NULL)`).Scan(&marked)
	if marked != 6 {
		t.Fatalf("rows marked sent: %d", marked)
	}
	spanAt(t, r, "run_e", now) // recorded while on: sent as it comes, and marked
	x.Flush()
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(false)})
	before := len(col.requests())
	st = configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since, MaxRows: telemetry.MaxBackfillRows}})
	x.Flush()
	if st.LastBackfill.Queued != 14 || st.LastBackfill.Left != 0 {
		t.Fatalf("only the rows no collector had: %+v", st.LastBackfill)
	}
	again := map[string]int{}
	for _, q := range col.requests()[before:] {
		for _, sp := range q.Spans {
			again[fmt0(sp.Attrs[telemetry.AttrRunID])]++
		}
	}
	if again["run_d"] != 0 || again["run_e"] != 0 || again["run_old"] != 0 {
		t.Fatalf("a row is never sent twice, nor one older than the date: %v", again)
	}

	// telemetry off: nothing to send, so the choice is refused
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(false)})
	r.sink.SetEnabled(r.ctx, false, "human:kyle")
	if _, err := x.Configure(r.ctx, telemetry.OTLPChange{Enabled: ptr(true), Backfill: &telemetry.Backfill{SinceMS: since}}, "human:kyle"); err == nil || !strings.Contains(err.Error(), "telemetry is off") {
		t.Fatalf("%v", err)
	}
}

func TestOTLPBackfillGoesThroughTheAllowlistAgain(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, false)
	x, _, _ := newExporter(t, r, nil)
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	// a row as an older build, or a hand edit, might have left it: an attribute that is not allowed and a free-text value
	now := time.Now().UnixMilli()
	attrs, _ := json.Marshal(map[string]any{"drydock.prompt": promptText, telemetry.AttrPurpose: "summarize", telemetry.AttrTier: codeText,
		telemetry.AttrGenAIInputMessages: `[{"role":"user","parts":[{"type":"text","content":"` + promptText + `"}]}]`})
	if _, err := r.db.Writer().Exec(`INSERT INTO telemetry_spans(trace_id,span_id,name,start_ms,end_ms,attrs) VALUES(?,?,?,?,?,?)`,
		strings.Repeat("a", 32), strings.Repeat("b", 16), "drydock.llm.chat", now-10, now, string(attrs)); err != nil {
		t.Fatal(err)
	}
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL), Backfill: &telemetry.Backfill{SinceMS: now - 60_000}})
	x.Flush()
	reqs := col.requests()
	if len(reqs) != 1 || len(reqs[0].Spans) != 1 {
		t.Fatalf("%+v", reqs)
	}
	forbiddenIn(t, "the collector", reqs[0].Body)
	if a := reqs[0].Spans[0].Attrs; len(a) != 1 || a[telemetry.AttrPurpose] != "summarize" {
		t.Fatalf("only the allowlisted identifier-shaped attribute, and no content while content export is off: %v", a)
	}
}

// wrapperRig is the real Wrapper against an OpenAI-shaped PROTOCOL FIXTURE (llmtest), with the sink as its tracer.
func wrapperRig(t *testing.T, r *rig, answer string) (*llm.Wrapper, llm.Request) {
	t.Helper()
	cost := 0.001
	srv := llmtest.New(t, func(c llmtest.Call, w http.ResponseWriter, _ *http.Request) {
		llmtest.Respond(w, c, llmtest.Completion{Model: "vendor/model-x-0901", Content: answer, Usage: &llmtest.Usage{PromptTokens: 50, CompletionTokens: 6, Cost: &cost}})
	})
	reg := providers.NewRegistry(r.db, providers.RegistryOptions{Secrets: providers.NewMemorySecretStoreForTests(), Bus: r.bus})
	p, err := reg.Create(r.ctx, providers.NewProvider{Name: "or", Kind: providers.KindAggregator, Flavor: providers.FlavorOpenRouter, BaseURL: srv.URL + "/v1",
		APIKey: "sk-test", DataClass: providers.DataPrivate, Tier: providers.TierCheap})
	if err != nil {
		t.Fatal(err)
	}
	w, err := llm.NewWrapper(llm.Options{Registry: reg, DB: r.db, Factory: backends.Factory, Scanner: secretadapt.LLM(scanner(t)), Bus: r.bus, Tracer: r.sink})
	if err != nil {
		t.Fatal(err)
	}
	return w, llm.Request{Provider: p.ID, Model: "vendor/model-x", Purpose: "summarize", ProjectID: "prj_1", RunID: "run_1", NoCache: true,
		Manifest: &llm.Manifest{Entries: []llm.ManifestEntry{{SourceType: "task", Sensitivity: llm.SensitivityPublicDocs}}},
		System:   []llm.Block{{Text: "You summarize a task's room."}}, Messages: []llm.Message{{Role: "user", Content: promptText}}}
}

type genaiMessage struct {
	Role  string `json:"role"`
	Parts []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"parts"`
	FinishReason string `json:"finish_reason"`
}

func TestContentExportIsAnOptInOfItsOwnAndNeverCarriesASecret(t *testing.T) {
	for _, enc := range []string{telemetry.EncodingProtobuf, telemetry.EncodingJSON} {
		t.Run(enc, func(t *testing.T) {
			r := newRig(t)
			col := newCollector(t, false)
			x, _, _ := newExporter(t, r, nil)
			r.sink.SetEnabled(r.ctx, true, "human:kyle")
			configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL), Encoding: ptr(enc)})
			w, req := wrapperRig(t, r, "The worker fixed the rounding.")

			// off by default: the span has no content
			if st, _ := r.sink.Status(r.ctx); st.Content.Enabled || x.Status().Content || r.sink.WantsContent() {
				t.Fatalf("content export is off by default: %+v", st.Content)
			}
			if _, err := w.Complete(r.ctx, req); err != nil {
				t.Fatal(err)
			}
			x.Flush()
			forbiddenIn(t, "the collector (content export off)", col.requests()[0].Body)

			// only a person, and only after acknowledging what it sends
			if err := r.sink.SetContent(r.ctx, true, true, "agent:worker"); err == nil {
				t.Fatal("an agent must not turn content export on")
			}
			if err := r.sink.SetContent(r.ctx, true, false, "human:kyle"); err == nil {
				t.Fatal("content export needs the acknowledgement")
			}
			if err := r.sink.SetContent(r.ctx, true, true, "human:kyle"); err != nil {
				t.Fatal(err)
			}
			st, _ := r.sink.Status(r.ctx)
			if !st.Content.Enabled || st.Content.ChangedBy != "human:kyle" || st.Content.ChangedAt == 0 || !x.Status().Content {
				t.Fatalf("%+v", st.Content)
			}
			before := len(col.requests())
			if _, err := w.Complete(r.ctx, req); err != nil {
				t.Fatal(err)
			}
			// the Wrapper's secret guard refuses a prompt with a token in it: that call's content is never recorded
			leak := req
			leak.Messages = []llm.Message{{Role: "user", Content: "deploy with " + fakeToken()}}
			if _, err := w.Complete(r.ctx, leak); err == nil {
				t.Fatal("the scanner let a token out")
			}
			x.Flush()
			var calls []gotSpan
			for _, q := range col.requests()[before:] {
				if strings.Contains(string(q.Body), fakeToken()) || strings.Contains(string(q.Body), "Z9q8W7e6") {
					t.Fatal("a secret reached the collector")
				}
				for _, sp := range q.Spans {
					if sp.Name == "drydock.llm.chat" {
						calls = append(calls, sp)
					}
				}
			}
			if len(calls) != 2 {
				t.Fatalf("two calls: %+v", calls)
			}
			ok, refused := calls[0], calls[1]
			var in, out []genaiMessage
			var sys []struct{ Type, Content string }
			if err := json.Unmarshal([]byte(fmt0(ok.Attrs[telemetry.AttrGenAIInputMessages])), &in); err != nil {
				t.Fatalf("input messages: %v %v", err, ok.Attrs)
			}
			json.Unmarshal([]byte(fmt0(ok.Attrs[telemetry.AttrGenAIOutputMessages])), &out)
			json.Unmarshal([]byte(fmt0(ok.Attrs[telemetry.AttrGenAISystemInstructions])), &sys)
			if len(in) != 1 || in[0].Role != "user" || in[0].Parts[0].Type != "text" || in[0].Parts[0].Content != promptText ||
				len(out) != 1 || out[0].Role != "assistant" || out[0].Parts[0].Content != "The worker fixed the rounding." || out[0].FinishReason == "" ||
				len(sys) != 1 || sys[0].Content != "You summarize a task's room." {
				t.Fatalf("the GenAI content attributes: in %+v out %+v sys %+v", in, out, sys)
			}
			for _, k := range telemetry.ContentKeys() {
				if _, has := refused.Attrs[k]; has {
					t.Errorf("the refused call's span holds %s", k)
				}
			}
			// the local rows hold what was sent (the log), and the preview says content is in it
			doc, _ := r.sink.Preview(r.ctx, 0)
			if !strings.Contains(doc.Note, "Content export is on") || !strings.Contains(strings.Join(doc.Allowlist, ","), telemetry.AttrGenAIInputMessages) {
				t.Fatalf("the preview: %s %v", doc.Note, doc.Allowlist)
			}

			// off again: content is no longer recorded, and a row recorded with it is sent without it
			if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Complete(r.ctx, req); err != nil { // queued with its content
				t.Fatal(err)
			}
			if err := r.sink.SetContent(r.ctx, false, false, "human:kyle"); err != nil {
				t.Fatal(err)
			}
			before = len(col.requests())
			if _, err := w.Complete(r.ctx, req); err != nil {
				t.Fatal(err)
			}
			x.Flush()
			for _, q := range col.requests()[before:] {
				forbiddenIn(t, "the collector (content export turned off)", q.Body)
			}
			var stored int
			r.db.Reader().QueryRow(`SELECT COUNT(*) FROM telemetry_spans WHERE attrs LIKE '%gen_ai.input.messages%'`).Scan(&stored)
			if stored != 2 {
				t.Fatalf("content is recorded only while content export is on: %d rows", stored)
			}
		})
	}
}

func TestContentIsRedactedAndBoundedBeforeItIsStored(t *testing.T) {
	r := newRig(t)
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	if err := r.sink.SetContent(r.ctx, true, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	c := anLLMCall()
	huge := strings.Repeat("é", telemetry.MaxContentBytes) // two bytes each: twice the bound
	c.Content = &llm.CallContent{System: []string{"key " + fakeToken()}, Messages: []llm.Message{{Role: "user", Content: "use " + fakeToken() + " now"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a.go"}`}}}, {Role: "tool", ToolCallID: "c1", Content: huge}},
		Output: "done", FinishReason: "stop"}
	r.sink.TraceCall(r.ctx, c)
	doc, _ := r.sink.Preview(r.ctx, 0)
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), fakeToken()) || strings.Contains(string(raw), "Z9q8W7e6") {
		t.Fatal("a secret was stored")
	}
	sp := doc.Spans[0]
	for _, k := range telemetry.ContentKeys() {
		v := fmt0(sp.Attrs[k])
		if v == "" || len(v) > telemetry.MaxContentBytes || !json.Valid([]byte(v)) {
			t.Errorf("%s: %d bytes, valid=%v", k, len(v), json.Valid([]byte(v)))
		}
	}
	in := fmt0(sp.Attrs[telemetry.AttrGenAIInputMessages])
	if !strings.Contains(in, "[cut]") || !strings.Contains(in, `"type":"tool_call"`) || !strings.Contains(in, `"type":"tool_call_response"`) {
		t.Fatalf("the tool output is cut to fit, the parts keep their kinds: %.300s", in)
	}
	// content attributes cannot be smuggled in another way: Sanitize drops them, and SanitizeContent only keeps JSON
	if clean, _ := telemetry.Sanitize(map[string]any{telemetry.AttrGenAIInputMessages: `[]`}, nil); len(clean) != 0 {
		t.Fatalf("%v", clean)
	}
	if clean, dropped := telemetry.SanitizeContent(map[string]any{telemetry.AttrGenAIInputMessages: promptText}, nil); len(clean) != 0 || len(dropped) != 1 {
		t.Fatalf("free text that is not a JSON document: %v %v", clean, dropped)
	}
}

func TestUsageCountersAreACategoryOfTheirOwn(t *testing.T) {
	r := newRig(t)
	col := newCollector(t, false)
	x, _, _ := newExporter(t, r, nil)
	// off by default: nothing is counted
	r.sink.CountUse("task.create")
	if n, _ := r.sink.FlushUsage(r.ctx); n != 0 {
		t.Fatalf("counted while off: %d", n)
	}
	if err := r.sink.SetUsageCounters(r.ctx, true, "agent:worker"); err == nil {
		t.Fatal("an agent must not turn usage counters on")
	}
	if err := r.sink.SetUsageCounters(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.sink.CountUse("task.create")
	}
	r.sink.CountUse("telemetry.status")
	r.sink.CountUse(promptText) // not a feature name: never counted
	st, _ := r.sink.Status(r.ctx)
	if !st.UsageCounters.Enabled || st.UsageCounters.Waiting != 4 || st.UsageCounters.ChangedBy != "human:kyle" || st.Enabled {
		t.Fatalf("on by its own switch, telemetry still off: %+v", st)
	}
	// written to the local sink as one counter point per feature, even with telemetry off (its own category)
	if n, err := r.sink.FlushUsage(r.ctx); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	doc, _ := r.sink.Preview(r.ctx, 0)
	got := map[string]float64{}
	for _, m := range doc.Metrics {
		if m.Name == telemetry.MetricFeatureUses && m.Kind == "counter" {
			got[fmt0(m.Attrs[telemetry.AttrFeature])] += m.Value
		}
	}
	if len(got) != 2 || got["task.create"] != 3 || got["telemetry.status"] != 1 {
		t.Fatalf("%v", got)
	}
	// sent with everything else once telemetry and sending are on
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	configure(t, x, telemetry.OTLPChange{Enabled: ptr(true), Endpoint: ptr(col.srv.URL)})
	r.sink.CountUse("budget.set")
	r.sink.FlushUsage(r.ctx)
	x.Flush()
	reqs := col.requests()
	if len(reqs) != 1 || len(reqs[0].Metrics) != 1 || reqs[0].Metrics[0].Name != telemetry.MetricFeatureUses || reqs[0].Metrics[0].Points[0].Attrs[telemetry.AttrFeature] != "budget.set" ||
		reqs[0].Metrics[0].Type != "sum" {
		t.Fatalf("%+v", reqs)
	}
	// off: what was counted and not written is dropped, and nothing more is counted
	r.sink.CountUse("budget.set")
	if err := r.sink.SetUsageCounters(r.ctx, false, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	r.sink.CountUse("budget.set")
	if n, _ := r.sink.FlushUsage(r.ctx); n != 0 {
		t.Fatalf("written while off: %d", n)
	}
	if st, _ := r.sink.Status(r.ctx); st.UsageCounters.Enabled || st.UsageCounters.Waiting != 0 {
		t.Fatalf("%+v", st.UsageCounters)
	}
	// the switch is stored
	y, err := telemetry.New(r.ctx, r.db, telemetry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if y.UsageCountersEnabled() || y.ContentEnabled() {
		t.Fatal("both switches read back off")
	}
	r.sink.SetUsageCounters(r.ctx, true, "human:kyle")
	if y, _ = telemetry.New(r.ctx, r.db, telemetry.Options{}); !y.UsageCountersEnabled() {
		t.Fatal("the usage counters' switch is stored")
	}
}

package telemetry_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/telemetry"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Real SQLite, the real workflow engine and the real gitleaks-backed scanner. The leak tests feed the engine node
// inputs, outputs and stream chunks full of prompt text, code, tool output and secrets and then read the exported JSON.

const (
	promptText = "Please refactor the payment module and keep the customer list private"
	codeText   = "func chargeCard(number string) { http.Post(\"https://pay.example/charge\", number) }"
	toolOutput = "total 48 drwxr-xr-x 2 root root 4096 secrets.env"
)

func fakeToken() string { return "gh" + "p_" + "Z9q8W7e6R5t4Y3u2I1o0P9a8S7d6F5g4H3j2" }

type rig struct {
	t    *testing.T
	ctx  context.Context
	db   *storage.DB
	bus  *events.SQLBus
	sink *telemetry.Sink
	eng  *workflow.Engine
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	sink, err := telemetry.New(ctx, db, telemetry.Options{Redact: sc.Redact})
	if err != nil {
		t.Fatal(err)
	}
	eng := workflow.New(workflow.Config{DB: db, Bus: bus, DataDir: t.TempDir(), LeaseTTL: 2 * time.Second})
	eng.Use(workflow.KindAgentTask, sink.Middleware())
	eng.Use(workflow.KindCheck, sink.Middleware())
	eng.Observe(sink.Observer())
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })
	return &rig{t: t, ctx: ctx, db: db, bus: bus, sink: sink, eng: eng}
}

// runLeakyWorkflow runs a graph whose input, chunks and outputs carry everything telemetry must never record.
func (r *rig) runLeakyWorkflow() {
	r.t.Helper()
	token := fakeToken()
	g := workflow.NewGraph("leaky").
		Add(workflow.AgentTask("work", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			in.Emit(workflow.NewChunk(workflow.ChunkText, workflow.TextDelta{Text: promptText + " " + token}))
			in.Emit(workflow.NewChunk(workflow.ChunkToolCall, workflow.ToolCall{ID: "t1", Name: "Bash", Args: json.RawMessage(`{"command":"cat secrets.env"}`)}))
			in.Emit(workflow.NewChunk(workflow.ChunkToolResult, workflow.ToolResult{ID: "t1", Output: toolOutput + " " + codeText}))
			in.Emit(workflow.NewChunk(workflow.ChunkUsage, workflow.UsageChunk{Model: "claude-sonnet-x", InputTokens: 1000, OutputTokens: 200, CachedTokens: 5000, CacheWriteTokens: 300, CostUSD: 0.0123, Source: "reported"}))
			v, _ := json.Marshal(codeText)
			return workflow.NodeOutput{Value: v}, nil
		}).With("note", promptText),
			workflow.Check("verify", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
				v, _ := json.Marshal(map[string]any{"name": "test", "passed": true, "output": toolOutput})
				return workflow.NodeOutput{Value: v, Route: "pass"}, nil
			})).
		Edge("work", "verify").Start("work")
	if err := r.eng.Register(g); err != nil {
		r.t.Fatal(err)
	}
	run, err := r.eng.Start(r.ctx, "leaky", map[string]string{"prompt": promptText, "token": token, "code": codeText}, workflow.StartOptions{ProjectID: "prj_1", TaskID: "task_1"})
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	info, err := r.eng.Await(ctx, run, func(i workflow.RunInfo) bool { return i.Status.Terminal() })
	if err != nil || info.Status != workflow.StatusCompleted {
		r.t.Fatalf("run %s %s %v", info.Status, info.FailedReason, err)
	}
}

func TestOffByDefaultRecordsNothingAndOnlyAHumanTurnsItOn(t *testing.T) {
	r := newRig(t)
	if r.sink.Enabled() {
		t.Fatal("telemetry must be off by default")
	}
	r.runLeakyWorkflow()
	st, _ := r.sink.Status(r.ctx)
	if st.Spans != 0 || st.Metrics != 0 || st.NetworkExporter {
		t.Fatalf("a disabled sink stores nothing: %+v", st)
	}
	if err := r.sink.SetEnabled(r.ctx, true, "agent:worker"); err == nil {
		t.Fatal("an agent identity must not enable telemetry")
	}
	if r.sink.Enabled() {
		t.Fatal("still off")
	}
	if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	st, _ = r.sink.Status(r.ctx)
	if !st.Enabled || st.EnabledBy != "human:kyle" {
		t.Fatalf("%+v", st)
	}
	// the setting survives a restart
	again, err := telemetry.New(r.ctx, r.db, telemetry.Options{})
	if err != nil || !again.Enabled() {
		t.Fatalf("persisted: %v %v", again, err)
	}
}

func TestNoPromptCodeToolOutputOrSecretReachesTheExport(t *testing.T) {
	r := newRig(t)
	if err := r.sink.SetEnabled(r.ctx, true, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	r.runLeakyWorkflow()

	prev, err := r.sink.Preview(r.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := r.sink.Export(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	prevRaw, _ := json.Marshal(prev)
	for _, forbidden := range []string{promptText, "refactor", "payment module", "customer list", codeText, "chargeCard", "pay.example", toolOutput, "secrets.env", "drwxr", fakeToken(), "Z9q8W7e6", "cat secrets"} {
		if strings.Contains(string(raw), forbidden) || strings.Contains(string(prevRaw), forbidden) {
			t.Errorf("%q reached the telemetry document", forbidden)
		}
	}
	// and the rows in the database itself
	for _, q := range []string{`SELECT group_concat(attrs||name||trace_id||span_id, ' ') FROM telemetry_spans`, `SELECT group_concat(attrs||name||unit, ' ') FROM telemetry_metrics`} {
		var all string
		r.db.Reader().QueryRow(q).Scan(&all)
		if strings.Contains(all, "refactor") || strings.Contains(all, "Z9q8W7e6") || strings.Contains(all, "chargeCard") {
			t.Errorf("content in stored telemetry: %s", all)
		}
	}
	if len(doc.Spans) != 2 {
		t.Fatalf("one span per node execution: %d", len(doc.Spans))
	}
	allowed := map[string]bool{}
	for _, k := range telemetry.Allowlist() {
		allowed[k] = true
	}
	check := func(attrs map[string]any) {
		for k := range attrs {
			if !allowed[k] {
				t.Errorf("attribute %q is not in the allowlist", k)
			}
		}
	}
	for _, sp := range doc.Spans {
		check(sp.Attrs)
		if sp.TraceID == "" || sp.Attrs[telemetry.AttrNodeKind] == nil || sp.Attrs[telemetry.AttrOutcome] != "ok" {
			t.Errorf("span %+v", sp)
		}
	}
	var tokens = map[string]float64{}
	var cost float64
	for _, m := range doc.Metrics {
		check(m.Attrs)
		if m.Name == telemetry.MetricTokenUsage {
			tokens[m.Attrs[telemetry.AttrGenAITokenType].(string)] += m.Value
		}
		if m.Name == telemetry.MetricCostUSD {
			cost += m.Value
			if m.Attrs[telemetry.AttrCostSource] != "reported" {
				t.Errorf("cost source %v", m.Attrs)
			}
		}
	}
	// D12: the OTel view of input includes cached tokens
	if tokens["input"] != 6000 || tokens["output"] != 200 || cost != 0.0123 {
		t.Fatalf("token/cost metrics: %v cost %v", tokens, cost)
	}
	// a second export carries only what is new
	doc2, _ := r.sink.Export(r.ctx)
	if len(doc2.Spans) != 0 || len(doc2.Metrics) != 0 {
		t.Fatalf("export marks what it exported: %d spans %d metrics again", len(doc2.Spans), len(doc2.Metrics))
	}
}

func TestSanitizeDropsEverythingThatIsNotAnAllowlistedIdentifier(t *testing.T) {
	sc, _ := security.NewScanner()
	in := map[string]any{
		telemetry.AttrTaskID:            "task_01HZ",
		telemetry.AttrGenAIRequestModel: "vendor/model-x:free",
		telemetry.AttrAttempt:           2,
		telemetry.AttrCostUSD:           0.5,
		telemetry.AttrCheckPass:         true,
		"prompt":                        promptText, // not allowlisted
		"gen_ai.prompt":                 promptText,
		"tool.output":                   toolOutput,
		"file.content":                  codeText,
		"api_key":                       fakeToken(),
		telemetry.AttrWorkflow:          promptText,          // allowlisted key, sentence value
		telemetry.AttrRunID:             "run\nwith newline", // whitespace
		telemetry.AttrNodeID:            strings.Repeat("a", 200),
		telemetry.AttrPurpose:           fakeToken(), // identifier-shaped but the scanner recognizes it
		telemetry.AttrOutcome:           42,          // wrong type
	}
	clean, dropped := telemetry.Sanitize(in, sc.Redact)
	if len(clean) != 5 {
		t.Fatalf("kept: %v", clean)
	}
	for _, k := range []string{"prompt", "gen_ai.prompt", "tool.output", "file.content", "api_key", telemetry.AttrWorkflow, telemetry.AttrRunID, telemetry.AttrNodeID, telemetry.AttrPurpose, telemetry.AttrOutcome} {
		found := false
		for _, d := range dropped {
			found = found || d == k
		}
		if !found {
			t.Errorf("%s should have been dropped", k)
		}
	}
	b, _ := json.Marshal(clean)
	if strings.Contains(string(b), "Z9q8W7e6") || strings.Contains(string(b), "refactor") {
		t.Fatalf("%s", b)
	}
}

func TestPreviewShowsWhatWouldBeSentAndExportToFileUsesAGeneratedName(t *testing.T) {
	r := newRig(t)
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	r.runLeakyWorkflow()
	st, _ := r.sink.Status(r.ctx)
	if st.Pending == 0 || st.Pending != st.Spans+st.Metrics {
		t.Fatalf("everything is pending before the first export: %+v", st)
	}
	prev, _ := r.sink.Preview(r.ctx, 1)
	if len(prev.Spans) != 1 || len(prev.Metrics) != 1 || prev.Schema != telemetry.Schema || len(prev.Allowlist) == 0 {
		t.Fatalf("limited preview: %+v", prev)
	}
	if st2, _ := r.sink.Status(r.ctx); st2.Pending != st.Pending {
		t.Fatal("a preview must not mark anything exported")
	}
	dir := t.TempDir()
	path, doc, err := r.sink.ExportToFile(r.ctx, dir)
	if err != nil || filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "telemetry-") {
		t.Fatalf("%s %v", path, err)
	}
	b, _ := os.ReadFile(path)
	var back telemetry.Document
	if json.Unmarshal(b, &back) != nil || len(back.Spans) != len(doc.Spans) {
		t.Fatal("the file is the export document")
	}
	if st3, _ := r.sink.Status(r.ctx); st3.Pending != 0 {
		t.Fatalf("pending after export: %d", st3.Pending)
	}
	if err := r.sink.Purge(r.ctx, true); err != nil {
		t.Fatal(err)
	}
	if st4, _ := r.sink.Status(r.ctx); st4.Spans != 0 || st4.Metrics != 0 {
		t.Fatalf("%+v", st4)
	}
}

func TestPausedAndFailedNodesAreRecordedWithOutcomeOnly(t *testing.T) {
	r := newRig(t)
	r.sink.SetEnabled(r.ctx, true, "human:kyle")
	g := workflow.NewGraph("outcomes").
		Add(workflow.AgentTask("park", func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "approval", Title: promptText}}, nil
		})).Start("park")
	r.eng.Register(g)
	run, _ := r.eng.Start(r.ctx, "outcomes", nil, workflow.StartOptions{TaskID: "task_9"})
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	r.eng.Await(ctx, run, func(i workflow.RunInfo) bool { return i.Status == workflow.StatusWaitingHuman || i.Status.Terminal() })
	doc, _ := r.sink.Preview(r.ctx, 0)
	if len(doc.Spans) != 1 || doc.Spans[0].Attrs[telemetry.AttrOutcome] != "paused" {
		t.Fatalf("%+v", doc.Spans)
	}
	if b, _ := json.Marshal(doc); strings.Contains(string(b), "refactor") {
		t.Fatal("a request title (free text) reached telemetry")
	}
}

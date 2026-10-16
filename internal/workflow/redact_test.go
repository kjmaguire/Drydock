package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	wf "github.com/kjmaguire/drydock/internal/workflow"
)

const leakyKey = "sk-live-AbCdEfGhIjKlMnOpQrStUvWx"

func maskKey(s string) string { return strings.ReplaceAll(s, leakyKey, "[REDACTED:key]") }

// Everything a run writes down (the chunk file of a node, its recorded output and error, the run's result and the checkpoint
// state) is free of a key a tool printed or the model repeated, whether it arrived whole or a few characters at a time
// (audit 2026-10-16, H7).
func TestARunRecordsNoSecretThatAnAgentOrToolPrinted(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	eng := wf.New(wf.Config{DB: db, Bus: bus, DataDir: dir, LeaseTTL: leaseTTL, Redact: maskKey})
	t.Cleanup(func() { eng.Close(); bus.Close(); db.Close() })

	work := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		text := "The key is " + leakyKey + " and I will not use it. "
		for i := 0; i < len(text); i += 4 {
			in.Emit(wf.Chunk{Kind: wf.ChunkText, Data: val2(wf.TextDelta{Text: text[i:min(i+4, len(text))]})})
		}
		in.Emit(wf.Chunk{Kind: "tool_result", Data: json.RawMessage(`{"id":"t1","content":"OPENAI_API_KEY=` + leakyKey + `\nPATH=/bin"}`)})
		in.Emit(wf.Chunk{Kind: wf.ChunkText, Data: val2(wf.TextDelta{Text: "last words"})})
		return wf.NodeOutput{Value: json.RawMessage(`{"text":"my key is ` + leakyKey + `","files":3}`)}, nil
	}
	fail := func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
		return wf.NodeOutput{}, errors.New("request failed with token " + leakyKey)
	}
	g := wf.NewGraph("leaky").Add(wf.AgentTask("W", work), wf.AgentTask("F", fail)).Edge("W", "F").Start("W")
	if err := eng.Register(g); err != nil {
		t.Fatal(err)
	}
	id, err := eng.Start(context.Background(), "leaky", map[string]string{"task": "x"}, wf.StartOptions{ProjectID: "p", TaskID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := eng.Await(ctx, id, func(r wf.RunInfo) bool { return r.Status.Terminal() }); err != nil {
		t.Fatal(err)
	}

	// 1. the chunk file
	var stream []byte
	_ = filepath.Walk(filepath.Join(dir, "runs"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, "stream.jsonl") && strings.Contains(p, string(filepath.Separator)+"W"+string(filepath.Separator)) {
			b, _ := os.ReadFile(p)
			stream = append(stream, b...)
		}
		return nil
	})
	if len(stream) == 0 {
		t.Fatal("no chunk file was written")
	}
	if strings.Contains(string(stream), "sk-live") || strings.Contains(string(stream), "AbCdEf") {
		t.Fatalf("a key is in the chunk file:\n%s", stream)
	}
	var text strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(string(stream)), "\n") {
		var c wf.Chunk
		if json.Unmarshal([]byte(l), &c) != nil {
			t.Fatalf("a line of the chunk file is not JSON: %q", l)
		}
		if c.Kind == wf.ChunkText {
			var d wf.TextDelta
			_ = json.Unmarshal(c.Data, &d)
			text.WriteString(d.Text)
		}
	}
	if got := text.String(); got != "The key is [REDACTED:key] and I will not use it. last words" {
		t.Fatalf("the text in the chunk file: %q", got)
	}
	if !strings.Contains(string(stream), "OPENAI_API_KEY=[REDACTED:key]") || !strings.Contains(string(stream), "PATH=/bin") {
		t.Fatalf("the tool result was not redacted in place:\n%s", stream)
	}

	// 2. the database: outputs, errors, results, checkpoints
	for _, q := range []string{
		`SELECT COALESCE(group_concat(CAST(output AS TEXT) || error, ' '), '') FROM node_executions`,
		`SELECT COALESCE(group_concat(CAST(state AS TEXT), ' '), '') FROM workflow_checkpoints`,
		`SELECT COALESCE(group_concat(COALESCE(CAST(result AS TEXT),'') || failed_reason, ' '), '') FROM workflow_runs`,
	} {
		var blob string
		if err := db.Reader().QueryRow(q).Scan(&blob); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if strings.Contains(blob, "sk-live") || strings.Contains(blob, "AbCdEf") {
			t.Fatalf("a key is stored by %s:\n%s", q, blob)
		}
	}
	var out string
	_ = db.Reader().QueryRow(`SELECT CAST(output AS TEXT) FROM node_executions WHERE node_id='W'`).Scan(&out)
	if !strings.Contains(out, "my key is [REDACTED:key]") || !strings.Contains(out, `"files":3`) {
		t.Fatalf("the recorded output: %s", out)
	}
	var reason string
	_ = db.Reader().QueryRow(`SELECT failed_reason FROM workflow_runs WHERE id=?`, string(id)).Scan(&reason)
	if !strings.Contains(reason, "[REDACTED:key]") {
		t.Fatalf("the failure reason: %q", reason)
	}
}

func val2(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

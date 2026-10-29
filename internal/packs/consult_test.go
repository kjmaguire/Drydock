package packs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scriptedAnswerer is a LABELED test double: it stands in for a model so the validation boundary around model output can be
// tested (a mock is allowed only to test a failure boundary). It records what it was asked.
type scriptedAnswerer struct {
	mu    sync.Mutex
	calls []AnswerRequest
	res   AnswerResult
	err   error
}

func (a *scriptedAnswerer) Answer(_ context.Context, req AnswerRequest) (AnswerResult, error) {
	a.mu.Lock()
	a.calls = append(a.calls, req)
	a.mu.Unlock()
	return a.res, a.err
}

func consultEnv(t *testing.T, vis string, ans Answerer) *env {
	e := newEnv(t, func(o *Options) { o.Answerer = ans })
	dir := filepath.Join(e.dir, "docs")
	writeFiles(t, dir, wranglerDocs)
	m := localManifest("cf", dir, "")
	if vis == "public" {
		s := string(m)
		s = strings.Replace(s, "visibility: private", "visibility: public\nlicense: {content: CC-BY-4.0, attribution_required: true, attribution: \"Fixture CC BY 4.0\"}", 1)
		m = []byte(s)
	}
	e.install(m)
	e.refresh("cf")
	return e
}

func TestConsultRetrievalOnlyNeedsNoModelAndCites(t *testing.T) {
	e := consultEnv(t, "private", nil)
	c, err := e.s.Consult(context.Background(), ConsultRequest{PackIDs: []string{"cf"}, Question: "How do I preview a Worker with wrangler?"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Found || c.Route != RouteRetrievalOnly || len(c.Citations) == 0 || c.Confidence != ConfidenceHigh || c.Model != "" {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(c.Answer, "versions upload") || !strings.Contains(c.Answer, "[1]") || !strings.Contains(c.Answer, "local:workers/wrangler.md") {
		t.Fatalf("answer must be assembled from the chunks with markers and sources:\n%s", c.Answer)
	}
	if len(c.Sources) != len(c.Citations) || c.Citations[0].ContentHash == "" || c.Citations[0].HeadingPath == "" {
		t.Fatalf("citations %+v", c.Citations)
	}
	ev := e.events("pack.consulted")
	if len(ev) != 1 {
		t.Fatal("pack.consulted")
	}
	d := evData(t, ev[0])
	if d["found"] != true || d["route"] != RouteRetrievalOnly || d["question_hash"] == "" {
		t.Fatalf("%v", d)
	}
	if strings.Contains(string(ev[0].Data), "preview a Worker") {
		t.Fatal("the question text must not be in the event (hash only)")
	}
}

func TestConsultNothingFoundSaysSoWithoutCallingAModel(t *testing.T) {
	ans := &scriptedAnswerer{res: AnswerResult{Text: "invented", Used: []int{1}}}
	e := consultEnv(t, "private", ans)
	c, err := e.s.Consult(context.Background(), ConsultRequest{Question: "kubernetes helm chart ingress"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Found || c.Answer != "nothing found in cf" || len(c.Citations) != 0 || c.Route != RouteNone || len(ans.calls) != 0 {
		t.Fatalf("%+v calls=%d", c, len(ans.calls))
	}
	if _, err := e.s.Consult(context.Background(), ConsultRequest{Question: "  "}); err == nil {
		t.Fatal("empty question")
	}
	if _, err := e.s.Consult(context.Background(), ConsultRequest{PackIDs: []string{"zzz"}, Question: "x"}); !errors.Is(err, ErrUnknownPack) {
		t.Fatalf("%v", err)
	}
}

// A model that is set up but cannot be reached (a local server that was stopped) leaves the cited passages, and the notice says
// so in the same words as every other case without a written answer.
func TestConsultAModelThatFailsLeavesTheCitedPassagesAndSaysSo(t *testing.T) {
	ans := &scriptedAnswerer{err: errors.New("llm: provider prov_1 returned HTTP 0 (network): connection refused")}
	e := consultEnv(t, "private", ans)
	c, err := e.s.Consult(context.Background(), ConsultRequest{PackIDs: []string{"cf"}, Question: "How do I preview a Worker with wrangler?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.calls) != 1 || !c.Found || c.Route != RouteRetrievalOnly || c.Model != "" || !strings.Contains(c.Answer, "versions upload") {
		t.Fatalf("%+v calls=%d", c, len(ans.calls))
	}
	if want := "The model could not answer (llm: provider prov_1 returned HTTP 0 (network): connection refused), so the answer is the cited passages"; c.Degraded != want {
		t.Fatalf("notice %q, want %q", c.Degraded, want)
	}
}

func TestConsultAnswererOutputIsValidatedAndCitationsComeFromStoredMetadata(t *testing.T) {
	ans := &scriptedAnswerer{res: AnswerResult{Text: "Use versions upload [1]. Visit https://attacker.example/docs", Used: []int{1}, Model: "cheap-1", Route: RoutePrivate, Usage: Usage{InputTokens: 900, OutputTokens: 40, CostUSD: 0.0004}}}
	e := consultEnv(t, "private", ans)
	ctx := context.Background()
	c, _ := e.s.Consult(ctx, ConsultRequest{PackIDs: []string{"cf"}, Question: "how to preview with wrangler", CallerRunID: "run_9"})
	if c.Route != RoutePrivate || c.Model != "cheap-1" || c.Usage.InputTokens != 900 || len(c.Citations) != 1 || strings.Contains(c.Citations[0].URL, "attacker") {
		t.Fatalf("%+v", c)
	}
	if c.Citations[0].URL != "local:workers/wrangler.md" {
		t.Fatalf("citation must come from stored metadata: %+v", c.Citations[0])
	}
	// the prompt is only the fixed instruction, the question and wrapped sources
	call := ans.calls[0]
	if call.Instruction != AnswerInstruction || call.AllowTrainable || len(call.Sources) == 0 || !strings.Contains(call.Sources[0].Text, "<untrusted source=\"pack:cf\"") {
		t.Fatalf("%+v", call)
	}
	// the cited version is pinned for the caller's run
	var n int
	e.db.Reader().QueryRow(`SELECT COUNT(*) FROM pack_pins`).Scan(&n)
	if n != 1 {
		t.Fatalf("pins %d", n)
	}
	for name, res := range map[string]AnswerResult{
		"out of range":     {Text: "x [9]", Used: []int{9}, Route: RoutePrivate},
		"zero":             {Text: "x", Used: []int{0}, Route: RoutePrivate},
		"no citation":      {Text: "answer without a citation", Used: nil, Route: RoutePrivate},
		"empty text":       {Text: " ", Used: []int{1}, Route: RoutePrivate},
		"trainable denied": {Text: "x", Used: []int{1}, Route: RouteTrainable},
	} {
		ans.res = res
		c, _ := e.s.Consult(ctx, ConsultRequest{PackIDs: []string{"cf"}, Question: "how to preview with wrangler"})
		if c.Route != RouteRetrievalOnly || c.Degraded == "" || !c.Found || len(c.Citations) == 0 || strings.Contains(c.Answer, "x [9]") {
			t.Errorf("%s: must degrade to retrieval-only: %+v", name, c)
		}
	}
	ans.res, ans.err = AnswerResult{}, ErrNoRoute
	c, _ = e.s.Consult(ctx, ConsultRequest{PackIDs: []string{"cf"}, Question: "how to preview with wrangler"})
	if c.Route != RouteRetrievalOnly || !strings.Contains(c.Degraded, "no provider route") {
		t.Fatalf("%+v", c)
	}
}

func TestTrainableRoutingRules(t *testing.T) {
	ctx := context.Background()
	sc := sharedScanner(t)
	pub := &Manifest{ID: "p", Visibility: VisibilityPublic, Consult: ConsultConfig{Route: RouteTrainableOK}}
	pubPrivRoute := &Manifest{ID: "q", Visibility: VisibilityPublic, Consult: ConsultConfig{Route: RoutePrivateOnly}}
	priv := &Manifest{ID: "r", Visibility: VisibilityPrivate, Consult: ConsultConfig{Route: RoutePrivateOnly}}
	cases := []struct {
		name string
		q    string
		m    []*Manifest
		ok   bool
	}{
		{"public short", "how do I preview a worker?", []*Manifest{pub}, true},
		{"private pack", "short", []*Manifest{pub, priv}, false},
		{"route forbids", "short", []*Manifest{pubPrivRoute}, false},
		{"too long", strings.Repeat("a", 601), []*Manifest{pub}, false},
		{"code fence", "what is wrong with ```go\nx := 1\n```", []*Manifest{pub}, false},
		{"secret", "why does token ghp_1a2B3c4D5e6F7g8H9i0J1k2L3m4N5o6P7q8R fail", []*Manifest{pub}, false},
		{"no packs", "x", nil, false},
	}
	for _, c := range cases {
		if ok, why := TrainableEligible(ctx, sc, c.q, c.m); ok != c.ok {
			t.Errorf("%s: ok=%v (%s)", c.name, ok, why)
		}
	}
	// end to end: the Answerer is told whether trainable is allowed
	ans := &scriptedAnswerer{res: AnswerResult{Text: "ok [1]", Used: []int{1}, Model: "free-1", Route: RouteTrainable}}
	e := consultEnv(t, "public", ans)
	c, _ := e.s.Consult(ctx, ConsultRequest{PackIDs: []string{"cf"}, Question: "how to preview with wrangler"})
	if !ans.calls[0].AllowTrainable || c.Route != RouteTrainable || c.Model != "free-1" {
		t.Fatalf("public pack and short question may use a trainable route: %+v", c)
	}
	c, _ = e.s.Consult(ctx, ConsultRequest{PackIDs: []string{"cf"}, Question: "how to preview ```x``` with wrangler"})
	if ans.calls[1].AllowTrainable || c.Route != RouteRetrievalOnly {
		t.Fatalf("a code fence closes the trainable route: %+v", c)
	}
	if len(c.Attributions) != 1 {
		t.Fatalf("attribution line: %v", c.Attributions)
	}
}

func TestParseModelAnswer(t *testing.T) {
	txt, used, err := ParseModelAnswer("Sure!\n```json\n{\"answer\": \"A [1]\", \"used\": [1, 2]}\n```")
	if err != nil || txt != "A [1]" || len(used) != 2 {
		t.Fatalf("%q %v %v", txt, used, err)
	}
	if _, _, err := ParseModelAnswer("no json here"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAttachInstructionsAllowanceAndWorkspaceProvider(t *testing.T) {
	e := consultEnv(t, "private", nil)
	ctx := context.Background()
	p := NewWorkspaceProvider(e.s)
	run := func(agent, role string) mcpRun { return mcpRun{ProjectID: "prj1", AgentID: agent, Role: role} }
	// nothing attached: search_pack refuses with an explanation rather than searching everything
	if _, err := p.Search(ctx, run("agt1", "Backend").info(), "preview", nil, 5); err == nil || !strings.Contains(err.Error(), "no expert packs are attached") {
		t.Fatalf("%v", err)
	}
	if err := e.s.Attach(ctx, human, "prj1", "cf", "", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Attach(ctx, human, "prj1", "nope", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := e.s.Attach(ctx, human, "nope", "cf", "", ""); err == nil {
		t.Fatal("unknown project")
	}
	hits, err := p.Search(ctx, run("agt1", "Backend").info(), "preview wrangler", nil, 3)
	if err != nil || len(hits) == 0 {
		t.Fatalf("%v %v", err, hits)
	}
	h := hits[0]
	if h.Pack != "cf" || h.Version != "20261008.1" || h.URL != "local:workers/wrangler.md" || len(h.HeadingPath) < 2 || !strings.Contains(h.ChunkID, "#") || h.FetchedAt.IsZero() {
		t.Fatalf("hit %+v", h)
	}
	// another role does not see it
	if _, err := p.Search(ctx, run("agt2", "Frontend").info(), "preview", nil, 3); err == nil {
		t.Fatal("a pack attached to Backend must not be searchable by Frontend")
	}
	if _, err := p.Search(ctx, run("agt1", "Backend").info(), "preview", []string{"other"}, 3); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("%v", err)
	}
	ins, _ := e.s.Instructions(ctx, "prj1", "agt1", "Backend")
	if len(ins) != 1 || !strings.Contains(ins[0].Text, "search_pack") || !strings.Contains(ins[0].Text, `"cf"`) {
		t.Fatalf("%+v", ins)
	}
	al, _ := e.s.AllowFor(ctx, "prj1", "agt1", "Backend")
	var pol policyFragment
	al.Apply(&pol.p)
	if !al.SearchPack || len(pol.p.Allow["workspace"]) != 1 || pol.p.Allow["workspace"][0] != "search_pack" {
		t.Fatalf("%+v", pol.p)
	}
	if a2, _ := e.s.AllowFor(ctx, "prj1", "agt2", "Frontend"); a2.SearchPack {
		t.Fatal("no allowance without an attachment")
	}
	// agent-specific attachment
	e.s.Attach(ctx, human, "prj1", "cf", "agt9", "")
	if ids, _ := e.s.AttachedIDs(ctx, "prj1", "agt9", "Reviewer"); len(ids) != 1 {
		t.Fatal("agent attachment")
	}
	if len(e.events("pack.attached")) != 2 {
		t.Fatalf("pack.attached events")
	}
	atts, _ := e.s.Attachments(ctx, "prj1")
	if len(atts) != 2 {
		t.Fatalf("%+v", atts)
	}
	if err := e.s.Detach(ctx, human, "prj1", "cf", "agt9", ""); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.s.AttachedIDs(ctx, "prj1", "agt9", "Reviewer"); len(ids) != 0 {
		t.Fatal("detach")
	}
}

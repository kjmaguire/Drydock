package router

import (
	"strings"
	"testing"
)

// A decision records where its level came from (decision P118), and "Why this model?" says it: the project's own level, or the
// workspace's when the project sets none. A request that does not say (route.preview, the evals) shows no level line.
func TestTheDecisionSaysWhichLevelItRanAtAndWhere(t *testing.T) {
	w := newWorld(t)
	r := w.router()
	q := req()
	q.Level, q.LevelSource = LevelQuality, LevelFromProject
	d := mustDecide(t, r, q)
	if d.Facts.Level != LevelQuality || d.Facts.LevelSource != LevelFromProject {
		t.Fatalf("facts %+v", d.Facts)
	}
	if text := r.Explain(d); !strings.Contains(text, "- Level: Quality, the project's own level (its settings).\n") {
		t.Fatalf("explain:\n%s", text)
	}
	// stored and loaded again, the explanation is the same
	_, back, err := w.rec.Load(w.ctx, d.ID)
	if err != nil || back.Facts.LevelSource != LevelFromProject || r.Explain(back) != r.Explain(d) {
		t.Fatalf("loaded %+v %v", back.Facts, err)
	}
	q.Level, q.LevelSource = LevelMaximumQuality, LevelFromWorkspace
	if text := r.Explain(mustDecide(t, r, q)); !strings.Contains(text, "- Level: Maximum Quality, the workspace's level (the project sets none of its own).\n") {
		t.Fatalf("explain:\n%s", text)
	}
	// a task's own level, and its template's (decision P127)
	q.Level, q.LevelSource = LevelCheapest, LevelFromTask
	if text := r.Explain(mustDecide(t, r, q)); !strings.Contains(text, "- Level: Cheapest, this task's own level (set on its page).\n") {
		t.Fatalf("explain:\n%s", text)
	}
	q.Level, q.LevelSource, q.LevelTemplate = LevelQuality, LevelFromTemplate, "Security"
	d = mustDecide(t, r, q)
	if text := r.Explain(d); !strings.Contains(text, "- Level: Quality, the level the Security template declares (the task sets none of its own).\n") {
		t.Fatalf("explain:\n%s", text)
	}
	if _, back, err := w.rec.Load(w.ctx, d.ID); err != nil || back.Facts.LevelTemplate != "Security" || r.Explain(back) != r.Explain(d) {
		t.Fatalf("loaded %+v %v", back.Facts, err)
	}
	q.LevelSource = ""
	if text := r.Explain(mustDecide(t, r, q)); strings.Contains(text, "- Level:") {
		t.Fatalf("no source, no line:\n%s", text)
	}
	for _, l := range Levels {
		if !ValidLevel(l) || LevelWords(l) == string(l) {
			t.Errorf("level %q", l)
		}
	}
	if ValidLevel("turbo") || ValidLevel("") {
		t.Error("not levels")
	}
}

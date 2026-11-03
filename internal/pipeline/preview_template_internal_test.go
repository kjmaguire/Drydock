package pipeline

import (
	"context"
	"slices"
	"testing"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// "Start a preview before approval" runs only in the Full Stack SaaS template (X311): a task of another template is not counted
// as running the preview check, so a criterion that names it is a finding there.
func TestOnlyTheSaaSTemplateCountsThePreviewAsAProjectCheck(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 64)
	t.Cleanup(func() { bus.Close(); db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p1','demo','/x',1)`); err != nil {
		t.Fatal(err)
	}
	if err := SaveDevSettings(ctx, db, "p1", DevSettings{Preview: PreviewSetup{Enabled: true}}, "human:me"); err != nil {
		t.Fatal(err)
	}
	d := Deps{Tasks: &tasks.Service{DB: db, Bus: bus}}
	for tmpl, want := range map[string]bool{"full-stack-saas": true, "full-stack-saas" + PRSuffix: true, "feature": false, "": false, "bugfix": false} {
		got := slices.Contains(d.projectChecks(ctx, tasks.Task{ProjectID: "p1", Template: tmpl}, nil), "preview")
		if got != want {
			t.Errorf("template %q: preview counted %v, want %v", tmpl, got, want)
		}
	}
}

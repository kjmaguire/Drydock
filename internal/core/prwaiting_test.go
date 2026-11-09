package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// H12: a collaborator's change request posted while a pull request's review and merge waits for the person is read by the
// core's check (a LABELED PROTOCOL FIXTURE of GitHub's review endpoints answers it), turns the Inbox item into a change-request
// item, and the request's detail carries the words to send back; a second check finds nothing new.
func TestChangeRequestsWhileWaitingReachTheInbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/repos/o/r") {
		case "/pulls/7/reviews":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 41, "state": "CHANGES_REQUESTED", "body": "Please handle negative numbers.", "user": map[string]string{"login": "kyle"}, "author_association": "MEMBER", "submitted_at": "2026-10-08T10:00:00Z"}})
		case "/pulls/7/comments", "/issues/7/comments":
			json.NewEncoder(w).Encode([]any{})
		default:
			t.Errorf("unexpected GitHub call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(dir, "d"), User: "kyle", LeaseTTL: 3 * time.Second,
		Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
		GitHub: func(ctx context.Context, p core.Project) (pipeline.PRConfig, error) {
			return pipeline.PRConfig{GitHub: host.GitHub{API: host.HTTPAPI{BaseURL: srv.URL}, Owner: "o", Repo: "r"}, BaseBranch: p.DefaultBranch, MergeStrategy: host.MergeSquash}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	a.call("project.update", map[string]any{"id": p.ID, "mode": "pr_flow", "mergeStrategy": "squash"}, &p)
	var task tasks.Task
	a.call("task.create", map[string]any{"projectId": p.ID, "title": "Implement Subtract"}, &task)
	ref := "o/r#7"
	if _, err := c.Tasks.Patch(context.Background(), task.ID, tasks.Update{PRRef: &ref}); err != nil {
		t.Fatal(err)
	}
	// the run's review and merge, waiting for the person
	if _, err := c.DB.Writer().Exec(`INSERT INTO approvals(id,run_id,task_id,project_id,kind,title,phase,status,created_at) VALUES('apr_m','run_1',?,?,'review','Review and merge: Implement Subtract','merge','REQUESTED',?)`,
		task.ID, p.ID, storage.Now()-1000); err != nil {
		t.Fatal(err)
	}
	n, err := c.CheckWaitingPRReviews(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("found %d, %v", n, err)
	}
	var items []room.NeedsYouItem
	a.call("needsyou.list", map[string]any{"projectId": p.ID}, &items)
	var item *room.NeedsYouItem
	for i := range items {
		if items[i].Key == "approval:apr_m" {
			item = &items[i]
		}
	}
	if item == nil || item.Kind != room.NeedsPRReview || !strings.Contains(item.Detail, "from kyle") {
		t.Fatalf("items %+v", items)
	}
	var d core.ApprovalDetail
	a.call("approval.get", map[string]string{"id": "apr_m"}, &d)
	if d.PRReview == nil || d.PRReview.Author != "kyle" || !strings.Contains(d.PRReview.Feedback, "Please handle negative numbers.") || !strings.Contains(d.PRReview.Feedback, "quoted data") {
		t.Fatalf("detail %+v", d.PRReview)
	}
	// the task page's decision card reads the same note
	var td core.TaskDetail
	a.call("task.get", map[string]string{"id": task.ID}, &td)
	if td.PRReview == nil || td.PRReview.Feedback != d.PRReview.Feedback {
		t.Fatalf("task detail note %+v", td.PRReview)
	}
	if again, err := c.CheckWaitingPRReviews(context.Background()); err != nil || again != 0 {
		t.Fatalf("a second check found %d, %v", again, err)
	}
	// once the person answered, the note is gone
	if _, err := c.DB.Writer().Exec(`UPDATE approvals SET status = 'REJECTED' WHERE id = 'apr_m'`); err != nil {
		t.Fatal(err)
	}
	td = core.TaskDetail{}
	a.call("task.get", map[string]string{"id": task.ID}, &td)
	if td.PRReview != nil {
		t.Fatalf("an answered review still shows the note: %+v", td.PRReview)
	}
}

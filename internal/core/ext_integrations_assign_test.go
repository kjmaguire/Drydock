package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
	"github.com/kjmaguire/drydock/internal/providers"
)

// Decision P42 through the UI methods: connect without a project, see the fixed identity, assign, sync into the project,
// unassign. GitHub joins through the user's gh (a REST fixture stands in for gh api here).
func TestWorkspaceConnectionsAndAssignmentsThroughTheUIMethods(t *testing.T) {
	ghTok := "gho_fixture_token_for_the_test_double"
	gh := itfixture.NewGitHub(ghTok, "acme/web")
	gts, gurl := itfixture.Serve(gh.Server())
	t.Cleanup(gts.Close)
	gh.Set([]itfixture.GitHubIssue{{Number: 7, Title: "Fix the flaky login test", Updated: time.Now().Add(-time.Hour)}})

	c, err := core.Open(context.Background(), core.Config{DataDir: filepath.Join(t.TempDir(), "data"), User: "kyle", LeaseTTL: 3 * time.Second,
		Credentials: providers.NewMemoryCredentialStoreForTests(), GitHubAPI: host.HTTPAPI{BaseURL: gurl, Token: ghTok}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a := &app{t: t, c: c}
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)

	// the screen is built from the catalog: fixed names, how each one connects, what an assignment asks for
	var kinds []integrations.KindInfo
	a.call("integration.kinds", nil, &kinds)
	if len(kinds) != 5 || kinds[0].Label != "Linear" || kinds[4].Kind != "github" || kinds[1].APIVersion != "Notion-Version "+integrations.NotionVersion {
		t.Fatalf("%+v", kinds)
	}

	lin := itfixture.NewLinear(itLinearToken)
	lts, lurl := itfixture.Serve(lin.Server())
	t.Cleanup(lts.Close)
	lin.Put(itfixture.LinearIssue{ID: "u1", Identifier: "ENG-1", Title: "First", State: "Todo", Team: "ENG", Created: time.Now(), Updated: time.Now()})
	var out struct {
		Connection integrations.ConnectionView `json:"connection"`
		Test       integrations.TestReport     `json:"test"`
	}
	a.call("integration.connect", map[string]any{"kind": "linear", "token": itLinearToken, "baseUrl": lurl}, &out)
	if !out.Test.OK || out.Connection.Name != "Linear" || out.Connection.Identity.Account != "Kyle Fixture" || len(out.Connection.Assignments) != 0 {
		t.Fatalf("%+v", out)
	}
	id := out.Connection.ID
	var teams []integrations.Choice
	a.call("integration.choices", map[string]any{"integrationId": id, "key": "team"}, &teams)
	if len(teams) != 2 {
		t.Fatalf("%+v", teams)
	}
	var asg integrations.AssignmentView
	a.call("integration.assign", map[string]any{"integrationId": id, "projectId": p.ID, "config": map[string]string{"team": "ENG"}}, &asg)
	if asg.ProjectID != p.ID || asg.Config["team"] != "ENG" {
		t.Fatalf("%+v", asg)
	}
	var sr integrations.SyncResult
	a.call("integration.sync", map[string]any{"assignmentId": asg.ID}, &sr)
	var items []integrations.Item
	a.call("integration.items", map[string]any{"projectId": p.ID}, &items)
	if sr.New != 1 || len(items) != 1 || items[0].ProjectID != p.ID {
		t.Fatalf("%+v %+v", sr, items)
	}
	a.call("integration.unassign", map[string]string{"id": asg.ID}, nil)
	var v integrations.ConnectionView
	a.call("integration.get", map[string]string{"id": id}, &v)
	if len(v.Assignments) != 0 || !v.HasCredential {
		t.Fatalf("%+v", v)
	}

	// GitHub: no token is taken; the gh login is the identity; issues of the assigned repository import as #n tasks
	var gout struct {
		Connection integrations.ConnectionView `json:"connection"`
		Test       integrations.TestReport     `json:"test"`
	}
	a.call("integration.connect", map[string]any{"kind": "github"}, &gout)
	if !gout.Test.OK || gout.Connection.Identity.Account != "kyle-fixture" || gout.Connection.AuthKind != "gh" {
		t.Fatalf("%+v", gout)
	}
	if err := a.callErr("integration.assign", map[string]any{"integrationId": gout.Connection.ID, "projectId": p.ID}); err == nil || !strings.Contains(err.Error(), "origin remote") {
		t.Fatalf("a repository without an origin remote: %v", err)
	}
	var gasg integrations.AssignmentView
	a.call("integration.assign", map[string]any{"integrationId": gout.Connection.ID, "projectId": p.ID, "config": map[string]string{"repo": "acme/web"}}, &gasg)
	a.call("integration.sync", map[string]any{"id": gout.Connection.ID}, &sr)
	a.call("integration.items", map[string]any{"assignmentId": gasg.ID}, &items)
	if len(items) != 1 || items[0].IssueRef != "#7" {
		t.Fatalf("%+v", items)
	}
	var imp integrations.TaskResult
	a.call("integration.import", map[string]any{"itemId": items[0].ID}, &imp)
	var detail core.TaskDetail
	a.call("task.get", map[string]string{"id": imp.TaskID}, &detail)
	if detail.Task.IssueRef != "#7" || detail.Task.ProjectID != p.ID {
		t.Fatalf("%+v", detail.Task)
	}
}

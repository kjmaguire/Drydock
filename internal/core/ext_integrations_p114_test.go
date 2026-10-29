package core_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/integrations/itfixture"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/tasks"
)

// Decision P114 through the UI methods against the Linear protocol fixture: the webhook receiver (off, then on at 127.0.0.1 on
// the chosen port, a signed delivery read through the API), mappings, an upstream status the person applies to a real task,
// auto-approve classes and the weekly sweep setting.
func TestIntegrationsP114ThroughTheUIMethods(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	a := openIntegrations(t, dataDir, providers.NewMemoryCredentialStoreForTests())
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	lin := itfixture.NewLinear(itLinearToken)
	lts, lurl := itfixture.Serve(lin.Server())
	t.Cleanup(lts.Close)
	base := time.Now().UTC().Add(-time.Hour)
	lin.Put(itfixture.LinearIssue{ID: "u1", Identifier: "ENG-1", Title: "First", State: "Todo", Team: "ENG", Created: base, Updated: base})
	var out struct {
		Connection integrations.ConnectionView `json:"connection"`
	}
	a.call("integration.connect", map[string]any{"kind": "linear", "projectId": p.ID, "token": itLinearToken, "baseUrl": lurl, "config": map[string]string{"team": "ENG"}}, &out)
	id := out.Connection.ID
	asg := out.Connection.Assignments[0].ID
	var sr integrations.SyncResult
	a.call("integration.sync", map[string]any{"id": id}, &sr)
	var items []integrations.Item
	items = nil
	a.call("integration.items", map[string]any{"integrationId": id}, &items)
	var tr integrations.TaskResult
	a.call("integration.import", map[string]any{"itemId": items[0].ID}, &tr)

	// the receiver is off by default; on, at the chosen port on 127.0.0.1
	var ws integrations.WebhookStatus
	a.call("integration.webhooks.get", nil, &ws)
	if ws.Enabled || ws.Listening {
		t.Fatalf("%+v", ws)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	a.call("integration.webhooks.set", map[string]any{"enabled": true, "port": port}, &ws)
	a.call("integration.webhooks.secret", map[string]any{"integrationId": id, "secret": "lin_wh_Sk7Pq2Tv9Lm4Xw1Ra8Bn5Yd3CjHs6U"}, &ws)
	if !ws.Listening || ws.Endpoints[0].URL != fmt.Sprintf("http://127.0.0.1:%d/hooks/linear", port) || !ws.Endpoints[0].HasSecret {
		t.Fatalf("%+v", ws)
	}
	lin.Put(itfixture.LinearIssue{ID: "u2", Identifier: "ENG-2", Title: "From the webhook", State: "Todo", Team: "ENG", Created: base, Updated: base.Add(time.Minute)})
	body, hdr := itfixture.LinearWebhook("lin_wh_Sk7Pq2Tv9Lm4Xw1Ra8Bn5Yd3CjHs6U", "d-1", "Issue", "create", map[string]any{"id": "u2"}, time.Now())
	req, _ := http.NewRequest("POST", ws.Endpoints[0].URL, bytes.NewReader(body))
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		items = nil
		a.call("integration.items", map[string]any{"integrationId": id}, &items)
		if len(items) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(items) != 2 {
		t.Fatalf("the webhook's item was not read: %+v", items)
	}

	// mappings, and the upstream state the person applies to the real task
	var ms []integrations.Mapping
	a.call("integration.mappings.set", map[string]any{"assignmentId": asg, "scope": "status", "mappings": []map[string]string{{"remote": "Canceled", "local": "CANCELLED", "direction": "from_vendor"}}}, &ms)
	if len(ms) != 1 {
		t.Fatalf("%+v", ms)
	}
	a.call("integration.mappings.list", map[string]any{"assignmentId": asg}, &ms)
	var fields []integrations.MappingField
	a.call("integration.mappings.fields", map[string]any{"assignmentId": asg, "scope": "status"}, &fields)
	if len(ms) != 1 || len(fields) != 3 {
		t.Fatalf("%+v %+v", ms, fields)
	}
	is := lin.Get("u1")
	is.State, is.Updated = "Canceled", base.Add(2*time.Minute)
	lin.Put(is)
	a.call("integration.sync", map[string]any{"id": id}, &sr)
	items = nil
	a.call("integration.items", map[string]any{"integrationId": id}, &items)
	var linked integrations.Item
	for _, it := range items {
		if it.TaskID == tr.TaskID {
			linked = it
		}
	}
	if linked.StatusSuggestion == nil || linked.StatusSuggestion.To != "CANCELLED" {
		t.Fatalf("%+v\n%+v\n%s", linked, items, tr.TaskID)
	}
	a.call("integration.status.apply", map[string]any{"itemId": linked.ID}, &linked)
	task, err := a.c.Tasks.Get(context.Background(), tr.TaskID)
	if err != nil || task.Status != tasks.Cancelled {
		t.Fatalf("%+v %v", task, err)
	}

	// auto-approve classes are off until the person turns one on; the sweep is on until turned off
	var classes []integrations.AutoApprove
	a.call("integration.autoApprove.list", map[string]any{"integrationId": id, "projectId": p.ID}, &classes)
	if len(classes) != 2 || classes[0].Enabled || classes[1].Enabled {
		t.Fatalf("%+v", classes)
	}
	var aa integrations.AutoApprove
	a.call("integration.autoApprove.set", map[string]any{"integrationId": id, "projectId": p.ID, "class": "status_sync", "enabled": true}, &aa)
	if !aa.Enabled || aa.CreatedBy != "human:kyle" {
		t.Fatalf("%+v", aa)
	}
	var sw integrations.SweepSettings
	a.call("integration.sweep.get", nil, &sw)
	if !sw.Enabled {
		t.Fatal("the weekly sweep is off by default")
	}
	a.call("integration.sweep.set", map[string]any{"enabled": false}, &sw)
	if sw.Enabled {
		t.Fatal(sw)
	}
	a.call("integration.webhooks.set", map[string]any{"enabled": false}, &ws)
	if ws.Listening {
		t.Fatal("still listening")
	}
	for _, m := range []string{"integration.outbound.retry", "integration.webhooks.get", "integration.webhooks.set", "integration.webhooks.secret", "integration.webhooks.useNotionToken",
		"integration.mappings.list", "integration.mappings.set", "integration.mappings.fields", "integration.status.apply", "integration.autoApprove.list", "integration.autoApprove.set",
		"integration.sweep.get", "integration.sweep.set", "integration.attachments.list", "integration.attachments.refresh", "integration.attachments.download", "integration.attachments.text"} {
		if _, ok := a.c.Methods()[m]; !ok {
			t.Errorf("method %s is not registered", m)
		}
	}
}

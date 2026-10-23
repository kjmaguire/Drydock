package core_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/scan/scantest"
)

// Decision P95: the Security page no longer shows the tools, so a rules folder or a program chosen there in an earlier
// version would apply where nobody can see, change or clear it. The next start forgets them: the scan runs Drydock's own
// tools and the built-in rules, and the stored choices are gone.
func TestChoicesFromAnEarlierVersionAreForgottenAtStart(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	open := func() *app {
		c, err := core.Open(context.Background(), core.Config{DataDir: data, User: "kyle", LeaseTTL: 3 * time.Second,
			Adapters:  map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}},
			ScanTools: scan.Paths{OSV: scan.Tool{Path: os.Args[0], Args: scantest.Args("osv-scanner")}, Opengrep: scan.Tool{Path: os.Args[0], Args: scantest.Args("opengrep")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &app{t: t, c: c}
	}
	a := open()
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	rules := filepath.Join(t.TempDir(), "rules") // a folder that is not there: a scan would say it cannot be used
	gone := filepath.Join(t.TempDir(), "opengrep.exe")
	// what an earlier version stored when a person chose them on the Security page
	if err := a.c.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		for k, v := range map[string]string{"security:rules:" + p.ID: rules, "security:workspace-rules": rules,
			"security:toolpath:opengrep": gone, "security:toolpath:osv-scanner": gone, "security:other": "kept"} {
			if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a.c.Close()
	a = open()
	t.Cleanup(a.c.Close)
	for _, id := range []string{p.ID, ""} {
		var tools scan.Tools
		a.call("security.tools", map[string]any{"projectId": id}, &tools)
		if tools.RulesDir != "" || tools.OSV.Chosen != "" || tools.Opengrep.Chosen != "" || !tools.OSV.Found || !tools.Opengrep.Found || !tools.Ready || !tools.BuiltinRules {
			t.Fatalf("project %q: %+v", id, tools)
		}
	}
	var left int
	if err := a.c.DB.Reader().QueryRow(`SELECT count(*) FROM settings WHERE key IN (?,?,?,?)`, "security:rules:"+p.ID, "security:workspace-rules",
		"security:toolpath:opengrep", "security:toolpath:osv-scanner").Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d choices left (%v)", left, err)
	}
	var other string
	if err := a.c.DB.Reader().QueryRow(`SELECT value FROM settings WHERE key='security:other'`).Scan(&other); err != nil || other != "kept" {
		t.Fatalf("another setting went too: %q %v", other, err)
	}
	// the methods that stored them are gone
	for _, m := range []string{"security.settings.set", "security.tool.set", "security.tool.choose", "security.tool.remove"} {
		if err := a.callErr(m, map[string]any{"projectId": p.ID, "tool": "opengrep"}); err == nil {
			t.Fatalf("%s still answers", m)
		}
	}
}

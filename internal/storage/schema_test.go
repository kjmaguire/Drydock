package storage

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestSchemaVersions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := OpenTemp(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	latest := LatestVersion()
	if latest < 80 {
		t.Fatalf("latest embedded migration %d", latest)
	}
	v, err := db.SchemaVersion(ctx)
	if err != nil || v != latest {
		t.Fatalf("database at %d (%v), want %d", v, err, latest)
	}
	// a file can be inspected without opening it as a Drydock database (read-only, no migration)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fv, err := FileSchemaVersion(ctx, filepath.Join(dir, "drydock.db"))
	if err != nil || fv != latest {
		t.Fatalf("file at %d (%v)", fv, err)
	}
	junk := filepath.Join(dir, "junk.db")
	os.WriteFile(junk, []byte("this is not sqlite at all, just text, long enough to be read as a header......"), 0o644)
	if _, err := FileSchemaVersion(ctx, junk); err == nil {
		t.Fatal("a non-database file reported a version")
	}
}

// Migration 00109 starts a skill that was given to agents (agent_skills) as a skill for chosen agents, so it keeps reaching
// exactly the agents it reached: before it, any assignment narrowed a skill to its agents. Every other skill is for the team.
func TestSkillAudienceMigrationKeepsGivenSkillsWithTheirAgents(t *testing.T) {
	ctx := context.Background()
	db, err := OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db.w, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 108); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects(id,name,repo_path,created_at) VALUES('p','p','/x',1)`,
		`INSERT INTO agents(id,project_id,name,adapter,role,created_at) VALUES('a','p','a','claude','Backend',1)`,
		`INSERT INTO skills(id,project_id,name,body,origin,content_hash,status,created_at,updated_at) VALUES('s1','p','given','b','registry','h','accepted',1,1)`,
		`INSERT INTO skills(id,project_id,name,body,origin,content_hash,status,created_at,updated_at) VALUES('s2','p','shared','b','repo','h','accepted',1,1)`,
		`INSERT INTO agent_skills(project_id,agent_id,skill_name,assigned_at) VALUES('p','a','given',1)`,
	} {
		if _, err := db.w.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, err := db.r.QueryContext(ctx, `SELECT name, audience FROM skills ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n, a string
		if err := rows.Scan(&n, &a); err != nil {
			t.Fatal(err)
		}
		got[n] = a
	}
	rows.Close()
	if got["given"] != "agents" || got["shared"] != "team" {
		t.Fatalf("audiences after the migration: %v", got)
	}
	if _, err := db.w.ExecContext(ctx, `UPDATE skills SET audience='everyone' WHERE name='shared'`); err == nil {
		t.Fatal("an unknown audience was stored")
	}
}

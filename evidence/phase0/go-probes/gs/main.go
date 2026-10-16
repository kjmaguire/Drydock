package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var emb embed.FS

func main() {
	dir, _ := os.MkdirTemp("", "gs")
	defer os.RemoveAll(dir)
	db, err := sql.Open("sqlite", "file:"+dir+"/a.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		panic(err)
	}
	sub, _ := fs.Sub(emb, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	res, err := p.Up(ctx)
	if err != nil {
		panic(err)
	}
	for _, r := range res {
		fmt.Println("applied:", r.Source.Path, r.Duration)
	}
	v, _ := p.GetDBVersion(ctx)
	fmt.Println("db version:", v)
	db.Exec("insert into runs(name) values('hello world')")
	var n int
	db.QueryRow("select count(*) from run_fts where run_fts match 'hello'").Scan(&n)
	fmt.Println("fts rows via trigger:", n)
	res2, err := p.DownTo(ctx, 0)
	fmt.Println("down:", len(res2), err)
}

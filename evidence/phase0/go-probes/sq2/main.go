package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	dir, _ := os.MkdirTemp("", "sq")
	defer os.RemoveAll(dir)
	db, _ := sql.Open("sqlite", "file:"+dir+"/t.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2000)")
	db.SetMaxOpenConns(4)
	db.Exec("create table t(a)")
	db.Exec("insert into t values(1)")
	ctx := context.Background()
	c1, _ := db.Conn(ctx)
	c2, _ := db.Conn(ctx)
	// c1 starts a read tx (deferred), c2 writes and commits, c1 then tries to write -> stale snapshot
	c1.ExecContext(ctx, "begin")
	var n int
	c1.QueryRowContext(ctx, "select count(*) from t").Scan(&n)
	_, err := c2.ExecContext(ctx, "insert into t values(2)")
	fmt.Println("c2 write:", err)
	_, err = c1.ExecContext(ctx, "insert into t values(3)")
	fmt.Println("c1 read-then-write upgrade after other writer committed:", err)
	c1.ExecContext(ctx, "rollback")
	// BEGIN IMMEDIATE avoids it
	c1.ExecContext(ctx, "begin immediate")
	_, err = c2.ExecContext(ctx, "insert into t values(4)")
	fmt.Println("c2 write while c1 holds BEGIN IMMEDIATE (waits busy_timeout=2s then):", err)
	c1.ExecContext(ctx, "commit")
	// _txlock
	db2, _ := sql.Open("sqlite", "file:"+dir+"/t.db?_txlock=immediate&_pragma=busy_timeout(2000)")
	tx, err := db2.Begin()
	fmt.Println("begin with _txlock=immediate:", err)
	tx.Rollback()
}

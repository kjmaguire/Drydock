package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	dir, _ := os.MkdirTemp("", "sq")
	defer os.RemoveAll(dir)
	dsn := "file:" + dir + "/t.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	must(err)
	defer db.Close()
	var v, jm string
	must(db.QueryRow("select sqlite_version()").Scan(&v))
	must(db.QueryRow("pragma journal_mode").Scan(&jm))
	fmt.Println("sqlite_version:", v, "journal_mode:", jm)
	// compile options
	rows, _ := db.Query("pragma compile_options")
	var opts []string
	for rows.Next() {
		var o string
		rows.Scan(&o)
		opts = append(opts, o)
	}
	rows.Close()
	for _, o := range opts {
		if o == "ENABLE_FTS5" || o == "ENABLE_JSON1" || o == "THREADSAFE=1" || o == "ENABLE_RTREE" {
			fmt.Println("compile_option:", o)
		}
	}
	// FTS5 + bm25
	_, err = db.Exec(`create virtual table docs using fts5(title, body, tokenize='porter unicode61')`)
	must(err)
	_, err = db.Exec(`insert into docs(title, body) values
	 ('auth', 'refresh tokens expire after thirty days'),
	 ('db', 'sqlite wal mode allows concurrent readers'),
	 ('misc', 'the quick brown fox jumps over the lazy dog')`)
	must(err)
	r, err := db.Query(`select title, bm25(docs) from docs where docs match 'reader*' order by bm25(docs)`)
	must(err)
	for r.Next() {
		var t string
		var s float64
		r.Scan(&t, &s)
		fmt.Printf("fts5 hit: %s bm25=%.4f\n", t, s)
	}
	r.Close()
	r, err = db.Query(`select title, snippet(docs,1,'[',']','...',5) from docs where docs match 'tokens' `)
	must(err)
	for r.Next() {
		var t, s string
		r.Scan(&t, &s)
		fmt.Println("fts5 snippet:", t, s)
	}
	r.Close()
	// check json
	var js string
	must(db.QueryRow(`select json_extract('{"a":{"b":5}}','$.a.b')`).Scan(&js))
	fmt.Println("json_extract:", js)

	// events table benchmark
	_, err = db.Exec(`create table events(id integer primary key, run_id text not null, seq integer not null, ts integer not null, type text not null, payload text not null)`)
	must(err)
	_, err = db.Exec(`create index events_run on events(run_id, seq)`)
	must(err)
	N := 100000
	payload := `{"tool":"read_file","path":"src/main.go","bytes":1234,"ok":true,"note":"lorem ipsum dolor sit amet consectetur"}`
	for _, batch := range []int{1000, 100} {
		_, _ = db.Exec("delete from events")
		start := time.Now()
		for i := 0; i < N; i += batch {
			tx, err := db.Begin()
			must(err)
			st, err := tx.Prepare(`insert into events(run_id,seq,ts,type,payload) values(?,?,?,?,?)`)
			must(err)
			for j := 0; j < batch; j++ {
				_, err = st.Exec(fmt.Sprintf("run-%d", (i+j)%50), i+j, time.Now().UnixNano(), "tool_call", payload)
				must(err)
			}
			st.Close()
			must(tx.Commit())
		}
		el := time.Since(start)
		fmt.Printf("BENCH insert %d rows batch=%d (prepared, 1 tx per batch, WAL, sync=NORMAL, 1 index): %v => %.0f rows/s\n", N, batch, el, float64(N)/el.Seconds())
	}
	// unbatched: autocommit single row inserts, 5000 rows
	_, _ = db.Exec("delete from events")
	start := time.Now()
	for i := 0; i < 5000; i++ {
		_, err = db.Exec(`insert into events(run_id,seq,ts,type,payload) values(?,?,?,?,?)`, "r", i, 1, "x", payload)
		must(err)
	}
	el := time.Since(start)
	fmt.Printf("BENCH autocommit single-row insert 5000 rows: %v => %.0f rows/s\n", el, 5000/el.Seconds())

	// concurrent readers + single writer
	_, _ = db.Exec("delete from events")
	db.SetMaxOpenConns(8)
	var wDone atomic.Bool
	var reads, busy, wrote int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !wDone.Load() {
				var c int
				if err := db.QueryRow("select count(*) from events").Scan(&c); err != nil {
					atomic.AddInt64(&busy, 1)
				} else {
					atomic.AddInt64(&reads, 1)
				}
			}
		}()
	}
	start = time.Now()
	for i := 0; i < 20000; i += 500 {
		tx, err := db.Begin()
		must(err)
		for j := 0; j < 500; j++ {
			_, err = tx.Exec(`insert into events(run_id,seq,ts,type,payload) values(?,?,?,?,?)`, "r", i+j, 1, "x", payload)
			must(err)
		}
		must(tx.Commit())
		wrote += 500
	}
	wDone.Store(true)
	wg.Wait()
	fmt.Printf("CONCURRENT: writer inserted %d rows in %v while 4 reader goroutines completed %d reads, %d read errors\n", wrote, time.Since(start), reads, busy)

	// two writers contention
	var wg2 sync.WaitGroup
	var werr int64
	var wok int64
	for w := 0; w < 4; w++ {
		wg2.Add(1)
		go func(w int) {
			defer wg2.Done()
			for k := 0; k < 200; k++ {
				tx, err := db.Begin()
				if err != nil {
					atomic.AddInt64(&werr, 1)
					continue
				}
				_, err = tx.Exec(`insert into events(run_id,seq,ts,type,payload) values(?,?,?,?,?)`, "w", k, 1, "x", payload)
				if err == nil {
					err = tx.Commit()
				} else {
					tx.Rollback()
				}
				if err != nil {
					atomic.AddInt64(&werr, 1)
				} else {
					atomic.AddInt64(&wok, 1)
				}
			}
		}(w)
	}
	wg2.Wait()
	fmt.Printf("4 concurrent writers x200 tx w/ busy_timeout=5000 (deferred BEGIN): ok=%d err=%d\n", wok, werr)
	// upgrade-deadlock case: read tx then write -> SQLITE_BUSY_SNAPSHOT?
	conn1, _ := db.Conn(context.Background())
	_ = conn1
}

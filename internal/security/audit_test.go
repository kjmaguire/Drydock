package security

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func appendN(t *testing.T, a *Audit, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := a.Append(context.Background(), AuditEntry{Actor: "system", Action: "test.event", Subject: "s", Detail: map[string]any{"i": i, "text": "hello"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuditChain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if v, err := r.audit.Verify(ctx); err != nil || !v.OK || v.Rows != 0 || v.Head != GenesisHash {
		t.Fatalf("empty chain: %+v %v", v, err)
	}
	appendN(t, r.audit, 5)
	v, err := r.audit.Verify(ctx)
	if err != nil || !v.OK || v.Rows != 5 {
		t.Fatalf("verify: %+v %v", v, err)
	}
	rows, _ := r.audit.List(ctx, 0, 0)
	if len(rows) != 5 || rows[0].PrevHash != GenesisHash || rows[1].PrevHash != rows[0].Hash || rows[4].Hash != v.Head {
		t.Fatalf("chain links wrong: %+v", rows)
	}
	for i, row := range rows {
		if row.Seq != int64(i+1) {
			t.Fatalf("seq %d at index %d", row.Seq, i)
		}
	}
	seq, head, _ := r.audit.Head(ctx)
	if seq != 5 || head != v.Head {
		t.Fatalf("head %d %s", seq, head)
	}
}

func TestAuditAppendOnlyTriggers(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	appendN(t, r.audit, 2)
	if _, err := r.db.Writer().ExecContext(ctx, `UPDATE audit_log SET actor='x' WHERE seq=1`); err == nil {
		t.Fatal("UPDATE succeeded on an append-only table")
	}
	if _, err := r.db.Writer().ExecContext(ctx, `DELETE FROM audit_log WHERE seq=2`); err == nil {
		t.Fatal("DELETE succeeded on an append-only table")
	}
}

// tamper drops the triggers (an attacker with file access can) and runs one statement.
func tamper(t *testing.T, r *rig, stmt string, args ...any) {
	t.Helper()
	ctx := context.Background()
	for _, s := range []string{`DROP TRIGGER audit_log_no_update`, `DROP TRIGGER audit_log_no_delete`} {
		if _, err := r.db.Writer().ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Writer().ExecContext(ctx, stmt, args...); err != nil {
		t.Fatal(err)
	}
}

func TestAuditTamperDetection(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		stmt string
		args []any
		seq  int64
		why  string
	}{
		{"edited actor", `UPDATE audit_log SET actor='human:mallory' WHERE seq=3`, nil, 3, "row hash mismatch"},
		{"edited detail", `UPDATE audit_log SET detail='{"i":99}' WHERE seq=2`, nil, 2, "row hash mismatch"},
		{"edited action", `UPDATE audit_log SET action='approval.approved' WHERE seq=4`, nil, 4, "row hash mismatch"},
		{"edited ts", `UPDATE audit_log SET ts=ts+1 WHERE seq=1`, nil, 1, "row hash mismatch"},
		{"deleted middle row", `DELETE FROM audit_log WHERE seq=3`, nil, 4, "sequence gap"},
		{"deleted first row", `DELETE FROM audit_log WHERE seq=1`, nil, 2, "sequence gap"},
		{"forged prev_hash", `UPDATE audit_log SET prev_hash='` + strings.Repeat("a", 64) + `' WHERE seq=3`, nil, 3, "prev_hash"},
		{"edit with recomputed hash only", `UPDATE audit_log SET actor='x', hash='` + strings.Repeat("b", 64) + `' WHERE seq=2`, nil, 2, "row hash mismatch"},
		{"non canonical detail", `UPDATE audit_log SET detail=' {"i":1,"text":"hello"}' WHERE seq=5`, nil, 5, "canonical"},
		{"inserted row", `INSERT INTO audit_log(seq,ts,actor,action,subject,detail,prev_hash,hash) VALUES(6,1,'a','b','','{}','` + strings.Repeat("c", 64) + `','` + strings.Repeat("d", 64) + `')`, nil, 6, "prev_hash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			appendN(t, r.audit, 5)
			if v, err := r.audit.Verify(ctx); err != nil || !v.OK {
				t.Fatalf("clean chain does not verify: %+v %v", v, err)
			}
			tamper(t, r, c.stmt, c.args...)
			v, err := r.audit.Verify(ctx)
			if !errors.Is(err, ErrChainBroken) || v.OK {
				t.Fatalf("tampering not detected: %+v %v", v, err)
			}
			if v.BadSeq != c.seq || !strings.Contains(v.Reason, c.why) {
				t.Fatalf("detected at seq %d (%s), want seq %d (%s)", v.BadSeq, v.Reason, c.seq, c.why)
			}
		})
	}
}

func TestAuditTailTruncationNeedsRecordedHead(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	appendN(t, r.audit, 4)
	_, head, _ := r.audit.Head(ctx)
	tamper(t, r, `DELETE FROM audit_log WHERE seq=4`)
	v, err := r.audit.Verify(ctx)
	if err != nil || !v.OK {
		t.Fatalf("a truncated tail is a valid prefix: %+v %v", v, err)
	}
	if v.Head == head {
		t.Fatal("head did not change after truncation, so a recorded head detects it")
	}
}

func TestAuditRedactsSecrets(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	key, aws := fakeGitHubToken(), fakeAWSKey()
	if err := r.scan.RegisterSecret("audit-test", fakeProviderKey()); err != nil {
		t.Fatal(err)
	}
	defer r.scan.UnregisterSecret("audit-test")
	row, err := r.audit.Append(ctx, AuditEntry{
		Actor: "agent_1", Action: "x", Subject: "cmd " + key,
		Detail: map[string]any{"cmd": "curl -H 'Authorization: token " + key + "'", "nested": []any{map[string]any{"k": aws}}, "provider": "key=" + fakeProviderKey(), "n": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	all := string(row.Detail) + row.Subject
	for _, s := range []string{key, aws, fakeProviderKey()} {
		if strings.Contains(all, s) {
			t.Fatalf("secret stored in audit row: %s", all)
		}
	}
	if !strings.Contains(all, "REDACTED") || !strings.Contains(all, `"n":3`) {
		t.Fatalf("unexpected detail %s", all)
	}
	var stored string
	_ = r.db.Reader().QueryRowContext(ctx, `SELECT detail||subject FROM audit_log WHERE seq=?`, row.Seq).Scan(&stored)
	if strings.Contains(stored, key) {
		t.Fatal("secret in database")
	}
	if v, err := r.audit.Verify(ctx); err != nil || !v.OK {
		t.Fatalf("verify after redaction: %+v %v", v, err)
	}
}

func TestAuditConcurrentAppends(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			appendN(t, r.audit, 10)
		}()
	}
	wg.Wait()
	v, err := r.audit.Verify(ctx)
	if err != nil || !v.OK || v.Rows != 80 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestAuditRejectsEmptyEntry(t *testing.T) {
	r := newRig(t)
	if _, err := r.audit.Append(context.Background(), AuditEntry{Action: "x"}); err == nil {
		t.Fatal("entry without actor accepted")
	}
}

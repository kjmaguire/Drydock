package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The D1 notice is shown until a person acknowledges the version they were shown; only a person can, an older version does
// not count, and the acknowledgement survives a restart.
func TestTheClaudeTermsNoticeIsAcknowledgedOnceByAPerson(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := Open(ctx, Config{DataDir: dir, User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	status := func(c *Core) NoticeState {
		t.Helper()
		res, err := c.Call(ctx, "notice.status", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Notices []NoticeState `json:"notices"`
		}
		if err := json.Unmarshal(res, &out); err != nil || len(out.Notices) != 1 {
			t.Fatalf("notice.status %s %v", res, err)
		}
		return out.Notices[0]
	}
	if st := status(c); st.ID != noticeClaudeTerms || st.Acknowledged {
		t.Fatalf("a new install: %+v", st)
	}
	if _, err := c.AcknowledgeNotice(ctx, noticeClaudeTerms, noticeClaudeTermsVersion, "agent:worker"); err == nil || !strings.Contains(err.Error(), "only a person") {
		t.Fatalf("an agent acknowledged it: %v", err)
	}
	if _, err := c.Call(ctx, "notice.acknowledge", json.RawMessage(`{"id":"claude-terms","version":0}`)); err == nil {
		t.Fatal("an older version was accepted")
	}
	if _, err := c.Call(ctx, "notice.acknowledge", json.RawMessage(`{"id":"claude-terms","version":1}`)); err != nil {
		t.Fatal(err)
	}
	if st := status(c); !st.Acknowledged || st.At == 0 {
		t.Fatalf("after acknowledging: %+v", st)
	}
	c.Close()
	c2, err := Open(ctx, Config{DataDir: dir, User: "kyle"})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if st := status(c2); !st.Acknowledged {
		t.Fatalf("after a restart: %+v", st)
	}
}

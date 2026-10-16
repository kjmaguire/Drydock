package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Audit 2026-10-16, H8: the text a person approves from.

func reviewOf(t *testing.T, v any) Review {
	t.Helper()
	rv, err := ReviewInput(input(t, v))
	if err != nil {
		t.Fatal(err)
	}
	return rv
}

func TestAnInputIsShownWithItsDestinationFirstAndWhole(t *testing.T) {
	content := strings.Repeat("line of file content\n", 2000) // 42 KB: it fits
	rv := reviewOf(t, map[string]any{"content": content, "file_path": "/etc/hosts", "mode": "0600"})
	if rv.Shortened || len(rv.Text) < len(content) || !strings.Contains(rv.Text, strings.TrimSuffix(strings.ReplaceAll(content, "\n", `\n`), `\n`)) {
		t.Fatalf("an input that fits must be shown whole (shortened=%v, %d bytes)", rv.Shortened, len(rv.Text))
	}
	// the destination first, the short fields next, the long one last (the canonical order puts "content" first)
	if !strings.HasPrefix(rv.Text, `{"file_path":"/etc/hosts","mode":"0600","content":"line of file content`) {
		t.Fatalf("order: %.120s", rv.Text)
	}
	if len(rv.Targets) != 1 || rv.Targets[0].Path != "/etc/hosts" {
		t.Fatalf("targets %+v", rv.Targets)
	}
	// the same input in another key order reads the same, so the text is a function of the input
	other := reviewOf(t, map[string]any{"mode": "0600", "file_path": "/etc/hosts", "content": content})
	if other.Text != rv.Text {
		t.Fatal("the text depends on key order")
	}
	if u := reviewOf(t, map[string]any{"url": "https://example.com/x", "prompt": "p"}); len(u.Targets) != 1 || u.Targets[0].Remote != "https://example.com/x" || u.Targets[0].Path != "" {
		t.Fatalf("an address is a remote target: %+v", u.Targets)
	}
}

func TestALongInputIsShortenedInTheMiddleAndSaysSo(t *testing.T) {
	head, tail := strings.Repeat("H", 5000), strings.Repeat("T", 5000)
	content := head + strings.Repeat("-middle-", 40000) + tail // 330 KB
	rv := reviewOf(t, map[string]any{"content": content, "file_path": "C:\\Users\\kyle\\.ssh\\authorized_keys"})
	if !rv.Shortened {
		t.Fatal("not marked shortened")
	}
	if len(rv.Text) > MaxReviewBytes+MaxReviewBytes/16 {
		t.Fatalf("%d bytes shown", len(rv.Text))
	}
	if !strings.HasPrefix(rv.Text, `{"file_path":"C:\\Users\\kyle\\.ssh\\authorized_keys","content":"`+strings.Repeat("H", 100)) || !strings.HasSuffix(rv.Text, strings.Repeat("T", 100)+`"}`) {
		t.Fatalf("the destination, the start and the end of the content must show: %.100s ... %s", rv.Text, rv.Text[len(rv.Text)-60:])
	}
	var gap int
	if i := strings.Index(rv.Text, "...["); i < 0 {
		t.Fatalf("no marker in %.200s", rv.Text)
	} else if _, err := fmt.Sscanf(rv.Text[i:], "...[%d bytes not shown]...", &gap); err != nil || gap < len(content)-MaxReviewBytes || gap > len(content) {
		t.Fatalf("the marker names a gap of %d bytes (%v) for a %d byte value", gap, err, len(content))
	}
	if rv.Bytes < len(content) {
		t.Fatalf("Bytes %d is not the size of the whole input", rv.Bytes)
	}
	if !utf8.ValidString(rv.Text) {
		t.Fatal("not valid UTF-8")
	}
	// the text is still JSON a reader can parse
	var back map[string]string
	if err := json.Unmarshal([]byte(rv.Text), &back); err != nil || back["file_path"] == "" {
		t.Fatalf("not JSON: %v", err)
	}
	// characters of more than one byte are not cut in two
	multi := reviewOf(t, map[string]any{"file_path": "/x", "content": strings.Repeat("é☃😀", 40000)})
	if !multi.Shortened || !utf8.ValidString(multi.Text) {
		t.Fatalf("multi-byte text: shortened=%v valid=%v", multi.Shortened, utf8.ValidString(multi.Text))
	}
}

// An input padded with big fields cannot push the destination out of sight, and one that cannot be fitted is refused.
func TestPaddingCannotHideTheDestinationAndAnUnfittableInputIsRefused(t *testing.T) {
	pad := func(n int) map[string]any {
		m := map[string]any{"url": "https://attacker.example/collect"}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("a_padding_%03d", i)] = strings.Repeat("x", 2000)
		}
		return m
	}
	rv := reviewOf(t, pad(40)) // 80 KB of padding
	if !rv.Shortened || !strings.HasPrefix(rv.Text, `{"url":"https://attacker.example/collect"`) || len(rv.Text) > MaxReviewBytes+MaxReviewBytes/16 {
		t.Fatalf("padding hid the address or grew the text (%d bytes): %.100s", len(rv.Text), rv.Text)
	}
	if _, err := ReviewInput(input(t, pad(400))); !errors.Is(err, ErrUnreviewable) {
		t.Fatalf("an input of 400 large fields was not refused: %v", err)
	}
	// a destination too long to read is refused, not cut
	if _, err := ReviewInput(input(t, map[string]any{"file_path": "/" + strings.Repeat("a/", 3000), "content": "x"})); !errors.Is(err, ErrUnreviewable) {
		t.Fatalf("a 6 KB path was not refused: %v", err)
	}
	// an input of many small fields that together do not fit and cannot be shortened is refused as well
	many := map[string]any{"file_path": "/x"}
	for i := 0; i < 3000; i++ {
		many[fmt.Sprintf("k%05d", i)] = strings.Repeat("y", 100)
	}
	if _, err := ReviewInput(input(t, many)); !errors.Is(err, ErrUnreviewable) {
		t.Fatalf("3000 fields of 100 bytes were not refused: %v", err)
	}
}

func TestReviewOfInputsThatAreNotObjectsOrAreEmpty(t *testing.T) {
	if rv, err := ReviewInput(nil); err != nil || rv.Text != "null" || rv.Shortened {
		t.Fatalf("%+v %v", rv, err)
	}
	if rv := reviewOf(t, []string{"a", "b"}); rv.Text != `["a","b"]` {
		t.Fatalf("%+v", rv)
	}
	big := reviewOf(t, []string{strings.Repeat("z", 200000)})
	if !big.Shortened || len(big.Text) > MaxReviewBytes+MaxReviewBytes/16 || !strings.Contains(big.Text, "bytes not shown") {
		t.Fatalf("a long array: shortened=%v, %d bytes", big.Shortened, len(big.Text))
	}
	if _, err := ReviewInput(json.RawMessage(`{"a":`)); err == nil {
		t.Fatal("broken JSON was accepted")
	}
	// a nested long value is shortened as text and the object stays readable
	nested := reviewOf(t, map[string]any{"path": "/x", "edits": []any{map[string]any{"old": strings.Repeat("o", 90000), "new": "n"}}})
	if !nested.Shortened || !strings.HasPrefix(nested.Text, `{"path":"/x","edits":"`) {
		t.Fatalf("nested: %.100s", nested.Text)
	}
}

// A permission request whose input is more than a person is shown still binds the whole input: the text a person saw is the
// same for two inputs that differ only in the part left out, but the approval is not.
func TestAShortenedApprovalStillBindsTheWholeInput(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "elsewhere", "notes.txt") // not under the worktree
	body := strings.Repeat("A", 120000)
	req := func(tailByte string) ToolRequest {
		return ToolRequest{Tool: "Write", Input: input(t, map[string]any{"file_path": outside, "content": body + tailByte}), Root: r.root}
	}
	a, err := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, req("1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ClassifyToolRequest(ctx, r.gate.Analyzer, nil, req("2"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Verdict != VerdictAsk || a.Kind != KindFSOutsideWorktree || !a.Shortened || !a.Action.Shortened {
		t.Fatalf("decision %+v", a.Verdict)
	}
	if !strings.HasPrefix(a.Display, `{"file_path":`) || len(a.Action.Targets) != 1 || !strings.HasSuffix(filepath.ToSlash(a.Action.Targets[0].Path), "elsewhere/notes.txt") {
		t.Fatalf("what the person sees: %.140s / %+v", a.Display, a.Action.Targets)
	}
	if a.Action.ArgvDigest == b.Action.ArgvDigest || a.Action.Hash() == b.Action.Hash() {
		t.Fatal("two inputs that differ only in the part left out share an approval")
	}
	id, err := r.apr.Request(ctx, r.gate.bind(permScope, a.Action), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if err := r.apr.Consume(ctx, id, r.gate.bind(permScope, b.Action)); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("the approval of one input covered another: %v", err)
	}
	if err := r.apr.Consume(ctx, id, r.gate.bind(permScope, a.Action)); err != nil {
		t.Fatalf("the approved input could not be consumed: %v", err)
	}
}

// The gate asks a person about a large write, shows its destination in the list and the whole thing as an approval, and refuses
// an input no person can be shown.
func TestTheGateShowsALargeWritesDestinationAndRefusesWhatNoOneCanRead(t *testing.T) {
	r := newPermRig(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "elsewhere", "notes.txt")
	req := ToolRequest{Tool: "Write", Input: input(t, map[string]any{"content": strings.Repeat("B", 90000), "file_path": outside}), Root: r.root}
	ch := r.decideAsync(t, req)
	id := r.waitRequested(t)
	info, err := r.apr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(info.Title, "notes.txt") || len(info.Title) > 260 || !info.Action.Shortened || !strings.HasPrefix(info.Action.Command, `{"file_path":`) {
		t.Fatalf("title %q, shortened %v, command %.80s", info.Title, info.Action.Shortened, info.Action.Command)
	}
	if err := r.apr.Decide(ctx, id, DecisionApprove, "human:kyle"); err != nil {
		t.Fatal(err)
	}
	if o := await(t, ch); !o.Allow || o.Outcome != PermissionApproved {
		t.Fatalf("approved: %+v", o)
	}

	// no person can read this destination, so none is asked
	long := ToolRequest{Tool: "Write", Input: input(t, map[string]any{"content": "x", "file_path": filepath.Join(t.TempDir(), strings.Repeat("d/", 3000), "f")}), Root: r.root}
	o := r.gate.Decide(ctx, permScope, long)
	if o.Allow || o.Outcome != PermissionDenied || !strings.Contains(o.Message, "too large or too long") {
		t.Fatalf("an unreadable destination: %+v", o)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("the gate wrote the file")
	}
}

package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeKey is shaped like a token a scanner would catch; the test's redactor stands in for the real scanner.
const fakeKey = "sk-live-AbCdEfGhIjKlMnOpQrStUvWx"

func mask(s string) string { return strings.ReplaceAll(s, fakeKey, "[REDACTED:key]") }

func TestJSONRedactsEveryStringAndKeepsTheStructure(t *testing.T) {
	in := `{"kind":"tool_result","n":12345678901234567890,"ok":true,"data":{"content":"API_KEY=` + fakeKey + `\nHOME=/home/u","list":["a","` + fakeKey + `",3],"<tag>":"x&y"},"nil":null}`
	out := JSON([]byte(in), mask)
	if strings.Contains(string(out), fakeKey) {
		t.Fatalf("the key survived: %s", out)
	}
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("the result is not JSON: %v\n%s", err, out)
	}
	if got["kind"] != "tool_result" || got["ok"] != true || got["nil"] != nil || got["n"].(json.Number).String() != "12345678901234567890" {
		t.Fatalf("structure or numbers changed: %v", got)
	}
	data := got["data"].(map[string]any)
	if c := data["content"].(string); c != "API_KEY=[REDACTED:key]\nHOME=/home/u" {
		t.Fatalf("content %q", c)
	}
	if data["<tag>"] != "x&y" {
		t.Fatalf("html characters were escaped: %v", data)
	}
	// nothing to redact: the very same bytes
	clean := []byte(`{"a":"nothing here at all","b":[1,2]}`)
	if got := JSON(clean, mask); string(got) != string(clean) {
		t.Fatalf("clean input rewritten: %s", got)
	}
	// not JSON: scanned as text; empty and nil redactor: untouched
	if got := JSON([]byte("plain "+fakeKey), mask); strings.Contains(string(got), fakeKey) {
		t.Fatalf("plain text: %s", got)
	}
	if got := JSON([]byte(in), nil); string(got) != in {
		t.Fatal("nil redactor changed the input")
	}
	if got := JSON(nil, mask); got != nil {
		t.Fatal("nil input")
	}
}

func TestLinesRedactsEachLineAndKeepsTheRest(t *testing.T) {
	in := "ok 1\nexport TOKEN=" + fakeKey + "\nlast line without newline " + fakeKey
	got := Lines(in, mask)
	if got != "ok 1\nexport TOKEN=[REDACTED:key]\nlast line without newline [REDACTED:key]" {
		t.Fatalf("%q", got)
	}
	if Lines("", mask) != "" || Lines("short", mask) != "short" || Lines(in, nil) != in {
		t.Fatal("edge cases")
	}
}

// A key printed token by token is never split across two outputs that are each clean.
func TestAStreamedKeyIsRedactedWhenItArrivesInPieces(t *testing.T) {
	text := "The key is " + fakeKey + " and that is all."
	for _, step := range []int{1, 2, 3, 5, 7, 13} {
		s := NewStream(mask)
		var out strings.Builder
		for i := 0; i < len(text); i += step {
			end := min(i+step, len(text))
			out.WriteString(s.Write(text[i:end]))
		}
		out.WriteString(s.Flush())
		if got := out.String(); got != "The key is [REDACTED:key] and that is all." {
			t.Fatalf("step %d: %q", step, got)
		}
	}
	// the unfinished word waits: nothing of it is emitted until it is whole
	s := NewStream(mask)
	if got := s.Write("password sk-live-AbCd"); got != "password " || s.Held() != len("sk-live-AbCd") {
		t.Fatalf("held %d, emitted %q", s.Held(), got)
	}
	if got := s.Write("EfGhIjKlMnOpQrStUvWx "); got != "[REDACTED:key] " {
		t.Fatalf("%q", got)
	}
	// a word longer than MaxHold is let go
	long := NewStream(nil)
	long.MaxHold = 10
	if got := long.Write(strings.Repeat("x", 30)); got == "" {
		t.Fatal("an endless word was held forever")
	}
}

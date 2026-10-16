package room

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseMarkers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []Marker
	}{
		{"none", "just chat", nil},
		{"decision", "[DECISION] Use SQLite", []Marker{{"DECISION", 1, "Use SQLite"}}},
		{"colon form", "[TODO]: write tests", []Marker{{"TODO", 1, "write tests"}}},
		{"all four", "intro\n[DECISION] a\n[TODO] b\n[STATUS] c\n[RESULT] d", []Marker{{"DECISION", 2, "a"}, {"TODO", 3, "b"}, {"STATUS", 4, "c"}, {"RESULT", 5, "d"}}},
		{"must be at line start", "see [DECISION] later", nil},
		{"indented is not a marker", "  [DECISION] x", nil},
		{"case sensitive", "[decision] x", nil},
		{"fenced code ignored", "```\n[DECISION] in code\n```\n[TODO] real", []Marker{{"TODO", 4, "real"}}},
		{"tilde fence ignored", "~~~\n[TODO] no\n~~~", nil},
		{"unterminated fence ignores the rest", "```\n[TODO] no\n[DECISION] no", nil},
		{"quote ignored", "> [DECISION] quoted\n[TODO] mine", []Marker{{"TODO", 2, "mine"}}},
		{"untrusted block ignored", "<untrusted source=\"x\">\n[DECISION] injected\n</untrusted>\n[STATUS] ok", []Marker{{"STATUS", 4, "ok"}}},
		{"inline code later in the line keeps the marker", "[RESULT] built `make all`", []Marker{{"RESULT", 1, "built `make all`"}}},
		{"crlf", "[TODO] a\r\n[TODO] b\r\n", []Marker{{"TODO", 1, "a"}, {"TODO", 2, "b"}}},
		{"empty text is recorded", "[DECISION]", []Marker{{"DECISION", 1, ""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseMarkers(c.body); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
	var many strings.Builder
	for i := 0; i < 50; i++ {
		many.WriteString("[TODO] x\n")
	}
	if n := len(ParseMarkers(many.String())); n != maxMarkers {
		t.Fatalf("marker cap: %d", n)
	}
}

func TestParseMentions(t *testing.T) {
	type m struct{ sigil, name string }
	cases := []struct {
		name string
		body string
		want []m
	}{
		{"agent", "hey @alice please look", []m{{"@", "alice"}}},
		{"start of body", "@alice go", []m{{"@", "alice"}}},
		{"role", "@role:Reviewer check", []m{{"@role:", "Reviewer"}}},
		{"tool", "run @Tests now", []m{{"@", "Tests"}}},
		{"room and message", "see #task-12-fix-login and ^msg_0a1b2c3d4e5f", []m{{"#", "task-12-fix-login"}, {"^", "msg_0a1b2c3d4e5f"}}},
		{"email is not a mention", "mail me at kyle@example.com", nil},
		{"trailing punctuation", "thanks @alice. And @bob, yes", []m{{"@", "alice"}, {"@", "bob"}}},
		{"dotted handle", "ping @a.b-c", []m{{"@", "a.b-c"}}},
		{"duplicates once", "@alice @Alice @alice", []m{{"@", "alice"}}},
		{"inline code ignored", "use `@alice` literally", nil},
		{"fenced code ignored", "```\n@alice\n```\n@bob", []m{{"@", "bob"}}},
		{"quote ignored", "> @alice said\n@bob", []m{{"@", "bob"}}},
		{"untrusted block ignored", "<untrusted>\n@alice\n</untrusted>", nil},
		{"url fragment is not a room", "https://x.test/a#section", nil},
		{"double at", "@@alice", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []m
			for _, r := range ParseMentions(c.body) {
				got = append(got, m{r.Sigil, r.Name})
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
	body := "hello @alice there"
	rm := ParseMentions(body)
	if len(rm) != 1 || body[rm[0].Span[0]:rm[0].Span[1]] != "@alice" {
		t.Fatalf("span: %+v", rm)
	}
}

func TestHelpers(t *testing.T) {
	if got := handleOf(" Claude  Worker "); got != "claude-worker" {
		t.Fatal(got)
	}
	if got := slug("Fix the Login Bug!!", 40); got != "fix-the-login-bug" {
		t.Fatal(got)
	}
	if got := slug("abcdefghij", 5); got != "abcde" {
		t.Fatal(got)
	}
	if got := TaskChannelName("task_abc", 12, "Fix login"); got != "task-12-fix-login" {
		t.Fatal(got)
	}
	if got := TaskChannelName("task_abc", 0, ""); got != "task-abc" {
		t.Fatal(got)
	}
	if got := Human("kyle").Key(); got != "human:kyle" {
		t.Fatal(got)
	}
	for _, k := range []string{"system", "human:kyle", "tool:tests", "agent_1"} {
		if ParseAuthor(k).Key() != k {
			t.Fatalf("roundtrip %s", k)
		}
	}
	for _, a := range []Author{{Kind: AuthorAgent, ID: "human:x"}, {Kind: AuthorAgent, ID: ""}, {Kind: AuthorHuman, ID: "a b"}, {Kind: "robot", ID: "x"}} {
		if a.Validate() == nil {
			t.Fatalf("%+v should be invalid", a)
		}
	}
}

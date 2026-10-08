package pipeline_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
)

func TestParseSpecAcceptsOneLineAndGherkinCriteria(t *testing.T) {
	text := `# Shopping cart totals
## Problem
Totals are wrong when a coupon is applied, and nobody can tell why.
## Acceptance criteria
- AC1: Given a cart with two items, when the coupon SAVE10 is applied, then the total drops by ten percent
2. **Given** an empty cart, **when** a coupon is applied, **then** an error is shown
* Given a signed-in user, when they open the cart, then their last coupon is offered

Given a cart over 100 dollars
And the user is a member
When they check out
And they choose express shipping
Then shipping is free
And the receipt says so

## Open questions
none`
	sp, problems := pipeline.ParseSpec(text)
	if len(problems) != 0 {
		t.Fatalf("%v", problems)
	}
	if sp.Title != "Shopping cart totals" || len(sp.Criteria) != 4 {
		t.Fatalf("%+v", sp)
	}
	c := sp.Criteria
	if c[0].ID != "AC1" || c[0].Given != "a cart with two items" || !strings.HasPrefix(c[0].Then, "the total drops") {
		t.Fatalf("%+v", c[0])
	}
	if c[1].Given != "an empty cart" || c[1].When != "a coupon is applied" {
		t.Fatalf("bold keywords: %+v", c[1])
	}
	if c[3].Given != "a cart over 100 dollars and the user is a member" || c[3].When != "they check out and they choose express shipping" || c[3].Then != "shipping is free and the receipt says so" {
		t.Fatalf("gherkin with And: %+v", c[3])
	}
	if got := pipeline.CriteriaStrings(c); got[3] != "AC4: Given a cart over 100 dollars and the user is a member, when they check out and they choose express shipping, then shipping is free and the receipt says so" {
		t.Fatalf("%q", got[3])
	}
}

func TestParseSpecRefusesWhatCannotBeBuiltFrom(t *testing.T) {
	long := "# x\n" + strings.Repeat("filler text for the spec ", 10) + "\n"
	cases := map[string]string{
		"empty":              "",
		"too short":          "# Tiny",
		"prose only":         long + "Users should be able to multiply numbers and it should be fast.",
		"given without then": long + "Given a user, when they click, they are happy",
		"gherkin incomplete": long + "Given a user\nWhen they click\n",
		"only when/then":     long + "When they click\nThen it works\n",
		"huge":               "# x\n" + strings.Repeat("a", 50<<10) + "\n- Given x, when y, then z",
	}
	for name, in := range cases {
		if _, problems := pipeline.ParseSpec(in); len(problems) == 0 {
			t.Errorf("%s must be refused", name)
		}
	}
	// duplicates are flagged, not silently merged
	_, problems := pipeline.ParseSpec(long + "- Given a, when b, then c\n- Given a, when b, then c\n")
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, ";"), "repeats") {
		t.Fatalf("%v", problems)
	}
	// CRLF text parses like LF text
	if sp, pr := pipeline.ParseSpec(strings.ReplaceAll(long+"- Given a, when b, then c\n", "\n", "\r\n")); len(pr) != 0 || len(sp.Criteria) != 1 {
		t.Fatalf("%v %+v", pr, sp)
	}
	// the human's typed criteria are validated line by line
	cs, pr := pipeline.ParseCriteriaLines([]string{"Given a, when b, then c", "Given only a given"})
	if len(cs) != 1 || len(pr) == 0 {
		t.Fatalf("%v %v", cs, pr)
	}
}

func TestScanRegistryIsPluggableByName(t *testing.T) {
	r := pipeline.NewScanRegistry()
	if len(r.Names()) != 0 {
		t.Fatal("a new registry is empty")
	}
	r.Register("sast", nil)
	r.Register("a-first", nil)
	if got := strings.Join(r.Names(), ","); got != "a-first,sast" {
		t.Fatal(got)
	}
	if got := strings.Join(pipeline.DefaultScans.Names(), ","); got != "deps,secrets" {
		t.Fatalf("default scans %s", got)
	}
}

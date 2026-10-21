package tsgen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type inner struct {
	Content string `json:"content"`
	Status  string `json:"status,omitempty"`
}

type base struct {
	Seq int64 `json:"seq"`
}

type sample struct {
	base
	Type    string            `json:"type"`
	Name    string            `json:"name,omitempty"`
	Count   int               `json:"count"`
	Ok      bool              `json:"ok"`
	Raw     json.RawMessage   `json:"raw,omitempty"`
	Items   []inner           `json:"items"`
	Ptr     *inner            `json:"ptr"`
	Labels  map[string]string `json:"labels,omitempty"`
	Skipped string            `json:"-"`
	hidden  string
}

func TestItWritesInterfacesAsEncodingJSONSeesThem(t *testing.T) {
	_ = sample{}.hidden
	out, err := Generate(File{
		Header: "// generated\n",
		Consts: SortedConsts(map[string]string{"B": "b", "A": "it's"}),
		Decls:  []Decl{{Name: "Sample", Doc: "A sample.", Type: reflect.TypeOf(sample{}), Literal: map[string]string{"type": "SAMPLE"}}},
		Unions: []Union{{Name: "Any", Members: []string{"Sample", "Inner"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `// generated

export const A = 'it\'s' as const;
export const B = 'b' as const;

/** A sample. */
export interface Sample {
  seq: number;
  type: 'SAMPLE';
  name?: string;
  count: number;
  ok: boolean;
  raw?: unknown;
  items: inner[];
  ptr?: inner;
  labels?: Record<string, string>;
}

export interface inner {
  content: string;
  status?: string;
}

export type Any =
  | Sample
  | Inner;
`
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestItRefusesWhatJSONCannotSayInTypeScript(t *testing.T) {
	type bad struct {
		M map[int]string `json:"m"`
	}
	if _, err := Generate(File{Decls: []Decl{{Name: "Bad", Type: reflect.TypeOf(bad{})}}}); err == nil || !strings.Contains(err.Error(), "map key") {
		t.Fatalf("err %v", err)
	}
	type lit struct {
		N int `json:"n"`
	}
	if _, err := Generate(File{Decls: []Decl{{Name: "Lit", Type: reflect.TypeOf(lit{}), Literal: map[string]string{"n": "x"}}}}); err == nil {
		t.Fatal("a literal on a number field must be refused")
	}
	if _, err := Generate(File{Decls: []Decl{{Name: "S", Type: reflect.TypeOf("")}}}); err == nil {
		t.Fatal("a non-struct declaration must be refused")
	}
}

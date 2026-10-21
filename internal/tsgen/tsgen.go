// Package tsgen writes TypeScript declarations for Go types that cross to the UI as JSON, so the UI reads the same shapes
// the core writes (decision P87). It reads the types with reflection, as encoding/json sees them: the json tag names a field,
// omitempty or a pointer makes it optional, "-" leaves it out, json.RawMessage is unknown. A struct becomes an interface; a
// tagged union is a list of interfaces whose discriminator field has a fixed string.
package tsgen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Decl is one interface to write. Name is its TypeScript name; Literal, when set, fixes the type of one string field (the
// discriminator of a union: Literal["type"] = "RUN_STARTED").
type Decl struct {
	Name    string
	Doc     string
	Type    reflect.Type
	Literal map[string]string
}

// Union is a type alias over declared interfaces.
type Union struct {
	Name    string
	Doc     string
	Members []string
}

// File is everything one generated file holds.
type File struct {
	Header string // the comment at the top: what the file is and how to regenerate it
	Consts []Const
	Decls  []Decl
	Unions []Union
	// Refs names Go types written by a TypeScript name declared elsewhere in the file (a union), never as interfaces.
	Refs map[reflect.Type]string
}

// Const is an exported string constant (`export const RUN_STARTED = 'RUN_STARTED' as const;`).
type Const struct {
	Name, Value string
}

var rawMessage = reflect.TypeOf(json.RawMessage(nil))

// Generate returns the TypeScript source. Struct types reached from a declaration that are not themselves declared are
// written as their own interfaces, named after the Go type.
func Generate(f File) (string, error) {
	var b strings.Builder
	b.WriteString(f.Header)
	b.WriteString("\n")
	for _, c := range f.Consts {
		fmt.Fprintf(&b, "export const %s = %s as const;\n", c.Name, quote(c.Value))
	}
	if len(f.Consts) > 0 {
		b.WriteString("\n")
	}
	declared := map[reflect.Type]string{}
	for t, n := range f.Refs {
		declared[t] = n
	}
	for _, d := range f.Decls {
		if _, ok := declared[d.Type]; !ok { // the first declaration of a Go type names it where other types refer to it
			declared[d.Type] = d.Name
		}
	}
	extra := map[string]reflect.Type{}
	var order []string
	for _, d := range f.Decls {
		if err := writeDecl(&b, d, declared, extra, &order); err != nil {
			return "", err
		}
	}
	for i := 0; i < len(order); i++ { // nested structs found while writing, in the order they were met
		t := extra[order[i]]
		if err := writeDecl(&b, Decl{Name: order[i], Type: t}, declared, extra, &order); err != nil {
			return "", err
		}
	}
	for _, u := range f.Unions {
		if u.Doc != "" {
			fmt.Fprintf(&b, "/** %s */\n", u.Doc)
		}
		fmt.Fprintf(&b, "export type %s =\n", u.Name)
		for i, m := range u.Members {
			end := ""
			if i == len(u.Members)-1 {
				end = ";"
			}
			fmt.Fprintf(&b, "  | %s%s\n", m, end)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func writeDecl(b *strings.Builder, d Decl, declared map[reflect.Type]string, extra map[string]reflect.Type, order *[]string) error {
	t := d.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("tsgen: %s is %s, not a struct", d.Name, t.Kind())
	}
	if d.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", d.Doc)
	}
	fmt.Fprintf(b, "export interface %s {\n", d.Name)
	fields, err := fieldsOf(t)
	if err != nil {
		return fmt.Errorf("tsgen: %s: %w", d.Name, err)
	}
	for _, f := range fields {
		var ts string
		if lit, ok := d.Literal[f.name]; ok {
			if f.typ.Kind() != reflect.String {
				return fmt.Errorf("tsgen: %s.%s: a literal needs a string field", d.Name, f.name)
			}
			ts = quote(lit)
			f.optional = false
		} else {
			ts, err = tsType(f.typ, declared, extra, order)
			if err != nil {
				return fmt.Errorf("tsgen: %s.%s: %w", d.Name, f.name, err)
			}
		}
		opt := ""
		if f.optional {
			opt = "?"
		}
		fmt.Fprintf(b, "  %s%s: %s;\n", f.name, opt, ts)
	}
	b.WriteString("}\n\n")
	return nil
}

type field struct {
	name     string
	typ      reflect.Type
	optional bool
}

// fieldsOf lists a struct's JSON fields as encoding/json writes them, embedded structs flattened.
func fieldsOf(t reflect.Type) ([]field, error) {
	var out []field
	seen := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		embedded := sf.Anonymous && sf.Type.Kind() == reflect.Struct
		if !sf.IsExported() && !embedded { // encoding/json flattens an embedded struct even when its type is unexported
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if embedded && name == "" {
			inner, err := fieldsOf(sf.Type)
			if err != nil {
				return nil, err
			}
			for _, f := range inner {
				if !seen[f.name] {
					seen[f.name] = true
					out = append(out, f)
				}
			}
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if seen[name] {
			return nil, fmt.Errorf("field %q twice", name)
		}
		seen[name] = true
		optional := strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,") || sf.Type.Kind() == reflect.Pointer
		out = append(out, field{name: name, typ: sf.Type, optional: optional})
	}
	return out, nil
}

func tsType(t reflect.Type, declared map[reflect.Type]string, extra map[string]reflect.Type, order *[]string) (string, error) {
	if t == rawMessage {
		return "unknown", nil
	}
	if n, ok := declared[t]; ok {
		return n, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		return tsType(t.Elem(), declared, extra, order)
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Slice, reflect.Array:
		inner, err := tsType(t.Elem(), declared, extra, order)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(inner, " |") {
			inner = "(" + inner + ")"
		}
		return inner + "[]", nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("map key %s is not a string", t.Key())
		}
		inner, err := tsType(t.Elem(), declared, extra, order)
		if err != nil {
			return "", err
		}
		return "Record<string, " + inner + ">", nil
	case reflect.Interface:
		return "unknown", nil
	case reflect.Struct:
		name := t.Name()
		if name == "" {
			return "", fmt.Errorf("an anonymous struct has no name to declare")
		}
		if prev, ok := extra[name]; ok && prev != t {
			return "", fmt.Errorf("two Go types are named %s", name)
		}
		if _, ok := extra[name]; !ok {
			extra[name] = t
			*order = append(*order, name)
		}
		return name, nil
	}
	return "", fmt.Errorf("type %s has no TypeScript form", t)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return "'" + strings.ReplaceAll(strings.Trim(string(b), `"`), "'", `\'`) + "'"
}

// SortedConsts returns constants sorted by name, for a stable file.
func SortedConsts(m map[string]string) []Const {
	out := make([]Const, 0, len(m))
	for k, v := range m {
		out = append(out, Const{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalJSON encodes v as canonical JSON: object keys sorted bytewise, UTF-8, no insignificant whitespace, HTML
// characters unescaped. v is first marshaled with encoding/json (so struct tags apply), then re-encoded by this
// package's own encoder. Numbers keep the text produced by the first pass.
func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return canonicalizeRaw(raw)
}

func canonicalizeRaw(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("security: trailing data after JSON value")
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, x); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, x any) error {
	switch t := x.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(t.String())
	case string:
		writeCanonicalString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("security: unsupported canonical type %T", x)
	}
	return nil
}

// writeCanonicalString writes a JSON string: only the mandatory escapes, everything else verbatim UTF-8.
func writeCanonicalString(buf *bytes.Buffer, s string) {
	s = strings.ToValidUTF8(s, "�")
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case ' ':
			buf.WriteString(` `)
		case ' ':
			buf.WriteString(` `)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

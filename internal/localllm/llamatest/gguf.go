package llamatest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// GGUF writes a tiny GGUF file (version 3) with the metadata given and no tensors: a LABELLED FIXTURE written from the format's
// specification (https://github.com/ggml-org/ggml/blob/master/docs/gguf.md), not a model. general.architecture is written
// first, the other keys in name order. A value is written by its Go type: string, uint32, int32, uint64, float32, bool,
// []uint32, []int32 or []string (an array). Other types panic: the fixture is for tests.
func GGUF(arch string, meta map[string]any) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) {
		w(uint64(len(s)))
		b.WriteString(s)
	}
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(0)) // tensors
	w(uint64(len(meta) + 1))
	str("general.architecture")
	w(uint32(8))
	str(arch)
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		str(k)
		switch v := meta[k].(type) {
		case string:
			w(uint32(8))
			str(v)
		case uint32:
			w(uint32(4))
			w(v)
		case int32:
			w(uint32(5))
			w(v)
		case float32:
			w(uint32(6))
			w(v)
		case bool:
			w(uint32(7))
			w(v)
		case uint64:
			w(uint32(10))
			w(v)
		case []uint32:
			w(uint32(9))
			w(uint32(4))
			w(uint64(len(v)))
			w(v)
		case []int32:
			w(uint32(9))
			w(uint32(5))
			w(uint64(len(v)))
			w(v)
		case []string:
			w(uint32(9))
			w(uint32(8))
			w(uint64(len(v)))
			for _, s := range v {
				str(s)
			}
		default:
			panic(fmt.Sprintf("llamatest.GGUF: %s has type %T", k, v))
		}
	}
	return b.Bytes()
}

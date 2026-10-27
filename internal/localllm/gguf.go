package localllm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// The KV cache estimate (decision P97): how much memory the attention cache of a downloaded model takes at the context size
// and cache type the server is started with, so the page can say whether the model and its cache fit the GPU or the RAM, and
// draw the memory bar. It is read from the model's GGUF header, which names the layers, the attention heads that keep keys and
// values, and their sizes (https://github.com/ggml-org/ggml/blob/master/docs/gguf.md). Only the header is read; nothing of
// the model runs.

// GGUFInfo is what the estimate reads from a GGUF header. HeadCountKV and HeadCount hold one value per layer when the model
// gives them per layer (one value otherwise).
type GGUFInfo struct {
	Architecture    string   `json:"architecture"`
	BlockCount      int      `json:"blockCount"`
	ContextLength   int      `json:"contextLength"`
	EmbeddingLength int      `json:"embeddingLength"`
	HeadCount       []uint64 `json:"headCount"`
	HeadCountKV     []uint64 `json:"headCountKv"`
	KeyLength       int      `json:"keyLength"`
	ValueLength     int      `json:"valueLength"`
	// SlidingWindow is set when some layers attend to a window of tokens only (their cache is smaller than counted); MLA when
	// the model keeps a compressed cache (multi-head latent attention). The estimate then is an upper bound.
	SlidingWindow int  `json:"slidingWindow,omitempty"`
	MLA           bool `json:"mla,omitempty"`
}

// Limits of what the header reader accepts: a header beyond them is not a model's.
const (
	ggufMaxKV     = 1 << 20
	ggufMaxKey    = 1 << 16
	ggufMaxString = 64 << 20
	ggufMaxArray  = 1 << 28
	ggufMaxRead   = 1 << 30 // the header with its tokenizer arrays comes well before this
	ggufMaxKept   = 1 << 17 // values kept over all the wanted keys (a model has one per layer for a few keys)
)

// GGUF value types.
const (
	ggufUint8 = iota
	ggufInt8
	ggufUint16
	ggufInt16
	ggufUint32
	ggufInt32
	ggufFloat32
	ggufBool
	ggufString
	ggufArray
	ggufUint64
	ggufInt64
	ggufFloat64
)

var errNotGGUF = errors.New("the file is not a GGUF model")

// ReadGGUF reads the parts of a GGUF file's header the KV estimate needs (versions 2 and 3).
func ReadGGUF(path string) (GGUFInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return GGUFInfo{}, err
	}
	defer f.Close()
	return parseGGUF(bufio.NewReaderSize(io.LimitReader(f, ggufMaxRead), 1<<16))
}

type ggufReader struct{ r *bufio.Reader }

func (g ggufReader) u32() (uint32, error) {
	var b [4]byte
	_, err := io.ReadFull(g.r, b[:])
	return binary.LittleEndian.Uint32(b[:]), err
}

func (g ggufReader) u64() (uint64, error) {
	var b [8]byte
	_, err := io.ReadFull(g.r, b[:])
	return binary.LittleEndian.Uint64(b[:]), err
}

func (g ggufReader) str(max uint64) (string, error) {
	n, err := g.u64()
	if err != nil {
		return "", err
	}
	if n > max {
		return "", fmt.Errorf("%w: a string of %d bytes", errNotGGUF, n)
	}
	b := make([]byte, n)
	_, err = io.ReadFull(g.r, b)
	return string(b), err
}

func (g ggufReader) skip(n uint64) error {
	_, err := g.r.Discard(int(n))
	return err
}

// scalarSize is the bytes of a fixed-size value type (0 for a string or an array).
func scalarSize(t uint32) uint64 {
	switch t {
	case ggufUint8, ggufInt8, ggufBool:
		return 1
	case ggufUint16, ggufInt16:
		return 2
	case ggufUint32, ggufInt32, ggufFloat32:
		return 4
	case ggufUint64, ggufInt64, ggufFloat64:
		return 8
	}
	return 0
}

// integer reads one integer value of type t (a float is truncated; a negative value is 0).
func (g ggufReader) integer(t uint32) (uint64, error) {
	var b [8]byte
	n := scalarSize(t)
	if n == 0 {
		return 0, fmt.Errorf("%w: value type %d", errNotGGUF, t)
	}
	if _, err := io.ReadFull(g.r, b[:n]); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(b[:])
	switch t {
	case ggufInt8:
		return clampNeg(int64(int8(v))), nil
	case ggufInt16:
		return clampNeg(int64(int16(v))), nil
	case ggufInt32:
		return clampNeg(int64(int32(v))), nil
	case ggufInt64:
		return clampNeg(int64(v)), nil
	case ggufFloat32:
		return clampNeg(int64(math.Float32frombits(uint32(v)))), nil
	case ggufFloat64:
		return clampNeg(int64(math.Float64frombits(v))), nil
	}
	return v, nil
}

func clampNeg(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// wanted says whether a key is one the estimate reads (by its suffix after the architecture's name).
func wanted(key string) bool {
	if key == "general.architecture" {
		return true
	}
	for _, s := range []string{".block_count", ".context_length", ".embedding_length", ".attention.head_count", ".attention.head_count_kv",
		".attention.key_length", ".attention.value_length", ".attention.sliding_window", ".attention.kv_lora_rank"} {
		if strings.HasSuffix(key, s) {
			return true
		}
	}
	return false
}

func parseGGUF(r *bufio.Reader) (GGUFInfo, error) {
	g := ggufReader{r}
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "GGUF" {
		return GGUFInfo{}, errNotGGUF
	}
	version, err := g.u32()
	if err != nil {
		return GGUFInfo{}, errNotGGUF
	}
	if version != 2 && version != 3 {
		return GGUFInfo{}, fmt.Errorf("GGUF version %d is not one Drydock reads (2 or 3)", version)
	}
	if _, err := g.u64(); err != nil { // tensor count
		return GGUFInfo{}, errNotGGUF
	}
	nkv, err := g.u64()
	if err != nil || nkv > ggufMaxKV {
		return GGUFInfo{}, errNotGGUF
	}
	ints := map[string][]uint64{}
	arch := ""
	// the values kept, over all keys: a header can name the wanted suffixes under any number of keys of other architectures,
	// each with an array of 65,536 values, which kept as uint64 would take gigabytes (audit 2026-10-27)
	kept := uint64(0)
	for i := uint64(0); i < nkv; i++ {
		key, err := g.str(ggufMaxKey)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("%w: %v", errNotGGUF, err)
		}
		t, err := g.u32()
		if err != nil {
			return GGUFInfo{}, errNotGGUF
		}
		// once the architecture is known (it comes first in every file llama.cpp writes) only its own keys are read
		want := wanted(key) && (arch == "" || strings.HasPrefix(key, arch+"."))
		switch t {
		case ggufString:
			if key == "general.architecture" {
				if arch, err = g.str(256); err != nil {
					return GGUFInfo{}, errNotGGUF
				}
				continue
			}
			n, err := g.u64()
			if err != nil || n > ggufMaxString || g.skip(n) != nil {
				return GGUFInfo{}, errNotGGUF
			}
		case ggufArray:
			et, err := g.u32()
			if err != nil {
				return GGUFInfo{}, errNotGGUF
			}
			n, err := g.u64()
			if err != nil || n > ggufMaxArray {
				return GGUFInfo{}, errNotGGUF
			}
			size := scalarSize(et)
			switch {
			case want && size > 0 && n <= 1<<16 && kept+n <= ggufMaxKept:
				kept += n
				vals := make([]uint64, 0, n)
				for j := uint64(0); j < n; j++ {
					v, err := g.integer(et)
					if err != nil {
						return GGUFInfo{}, errNotGGUF
					}
					vals = append(vals, v)
				}
				ints[key] = vals
			case size > 0:
				if g.skip(n*size) != nil {
					return GGUFInfo{}, errNotGGUF
				}
			case et == ggufString:
				for j := uint64(0); j < n; j++ {
					l, err := g.u64()
					if err != nil || l > ggufMaxString || g.skip(l) != nil {
						return GGUFInfo{}, errNotGGUF
					}
				}
			default: // an array of arrays is not in the format
				return GGUFInfo{}, fmt.Errorf("%w: array of type %d", errNotGGUF, et)
			}
		default:
			if want {
				v, err := g.integer(t)
				if err != nil {
					return GGUFInfo{}, errNotGGUF
				}
				ints[key] = []uint64{v}
			} else if n := scalarSize(t); n == 0 || g.skip(n) != nil {
				return GGUFInfo{}, errNotGGUF
			}
		}
	}
	if arch == "" {
		return GGUFInfo{}, fmt.Errorf("%w: no general.architecture", errNotGGUF)
	}
	one := func(k string) int {
		if v := ints[arch+"."+k]; len(v) > 0 && v[0] < 1<<31 {
			return int(v[0])
		}
		return 0
	}
	info := GGUFInfo{Architecture: arch, BlockCount: one("block_count"), ContextLength: one("context_length"), EmbeddingLength: one("embedding_length"),
		HeadCount: ints[arch+".attention.head_count"], HeadCountKV: ints[arch+".attention.head_count_kv"], KeyLength: one("attention.key_length"),
		ValueLength: one("attention.value_length"), SlidingWindow: one("attention.sliding_window"), MLA: one("attention.kv_lora_rank") > 0}
	return info, nil
}

// KVCache is the estimate of a model's KV cache at a context size and cache type.
type KVCache struct {
	// Known is false when the estimate cannot be made; Note then says why.
	Known bool   `json:"known"`
	Bytes uint64 `json:"bytes,omitempty"`
	// ContextSize is the tokens the estimate is for (the setting, or the model's own when the setting is 0).
	ContextSize int    `json:"contextSize"`
	CacheType   string `json:"cacheType"`
	Note        string `json:"note,omitempty"`
}

// cacheBytes is the bytes per stored value of each cache type llama.cpp offers (ggml's block sizes: q8_0 keeps 32 values in
// 34 bytes, q4_0 in 18, q4_1 in 20, q5_0 in 22, q5_1 in 24, iq4_nl in 18). The TurboQuant fork's turbo3 and turbo4 are not
// listed: the fork does not document their size per value, so their estimate is unknown.
var cacheBytes = map[string]float64{
	"f32": 4, "f16": 2, "bf16": 2,
	"q8_0": 34.0 / 32, "q4_0": 18.0 / 32, "q4_1": 20.0 / 32, "q5_0": 22.0 / 32, "q5_1": 24.0 / 32, "iq4_nl": 18.0 / 32,
}

// The largest sizes the KV estimate takes from a header: well beyond any model (the largest have a few hundred layers, a few
// hundred heads, keys of a few thousand values and contexts of a few million tokens).
const (
	kvMaxLayers  = 4096
	kvMaxHeads   = 1 << 14
	kvMaxLength  = 1 << 16
	kvMaxContext = 1 << 26
)

// EstimateKV is the KV cache of a model at contextSize tokens (0: the model's own context length) and cacheType: for every
// layer, the heads that keep keys and values times the key and value lengths, times the tokens, times the bytes a value takes.
// A key length the header does not give is the embedding length over the head count, as llama.cpp takes it.
func EstimateKV(info GGUFInfo, contextSize int, cacheType string) KVCache {
	kv := KVCache{ContextSize: contextSize, CacheType: cacheType}
	if kv.ContextSize <= 0 {
		kv.ContextSize = info.ContextLength
	}
	bpe, ok := cacheBytes[cacheType]
	switch {
	case !ok && strings.HasPrefix(cacheType, "turbo"):
		kv.Note = "The TurboQuant build does not say how much memory its " + cacheType + " cache takes, so it is not estimated."
		return kv
	case !ok:
		kv.Note = "The cache type " + cacheType + " is not one Drydock can size."
		return kv
	case kv.ContextSize <= 0:
		kv.Note = "The model does not say its context length, and no context size is set."
		return kv
	case info.BlockCount <= 0 || len(info.HeadCount) == 0:
		kv.Note = "The model's header does not describe attention layers (it may keep a recurrent state instead), so its cache is not estimated."
		return kv
	case info.BlockCount > kvMaxLayers || kv.ContextSize > kvMaxContext || info.KeyLength > kvMaxLength || info.ValueLength > kvMaxLength:
		// a header no model has (a crafted file): estimating it would loop for seconds and overflow (audit 2026-10-27)
		kv.Note = "The model's header gives sizes no model has, so its cache is not estimated."
		return kv
	}
	at := func(v []uint64, l int) uint64 {
		switch {
		case len(v) == 0:
			return 0
		case len(v) == 1:
			return v[0]
		case l < len(v):
			return v[l]
		}
		return v[len(v)-1]
	}
	var perToken float64 // values per token over all layers
	for l := 0; l < info.BlockCount; l++ {
		heads := at(info.HeadCount, l)
		kvHeads := heads
		if len(info.HeadCountKV) > 0 {
			kvHeads = at(info.HeadCountKV, l)
		}
		if heads == 0 || kvHeads == 0 {
			continue // a layer without attention keeps no cache
		}
		if heads > kvMaxHeads || kvHeads > kvMaxHeads {
			kv.Note = "The model's header gives sizes no model has, so its cache is not estimated."
			return kv
		}
		k, v := uint64(info.KeyLength), uint64(info.ValueLength)
		if k == 0 {
			if info.EmbeddingLength <= 0 {
				kv.Note = "The model's header gives neither the key length nor the embedding length."
				return kv
			}
			k = uint64(info.EmbeddingLength) / heads
		}
		if v == 0 {
			v = k
		}
		perToken += float64(kvHeads * (k + v))
	}
	total := perToken * float64(kv.ContextSize) * bpe
	if total >= 1<<60 {
		kv.Note = "The model's header gives sizes no model has, so its cache is not estimated."
		return kv
	}
	kv.Bytes = uint64(total)
	kv.Known = true
	switch {
	case info.MLA:
		kv.Note = "The model keeps a compressed cache (multi-head latent attention); this counts it uncompressed, so it is an upper bound."
	case info.SlidingWindow > 0:
		kv.Note = fmt.Sprintf("Some layers of the model attend to the last %d tokens only; this counts every layer at the full context, so it is an upper bound.", info.SlidingWindow)
	}
	if contextSize <= 0 {
		kv.Note = strings.TrimSpace(fmt.Sprintf("For the model's own context of %d tokens. %s", kv.ContextSize, kv.Note))
	}
	return kv
}

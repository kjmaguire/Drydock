package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// What a person reads before they approve a tool call (audit 2026-10-16, H8).
//
// An approval binds the hash of the whole input, but the person decides on the text in front of them. That text used to be the
// canonical JSON of the input, which sorts its keys, cut at 4,000 bytes: a Write's `content` sorts before its `file_path`, so
// for any ordinary large write the person saw `{"content":"...[N more bytes]` and not where it goes, while Allow once covered
// the whole input and its destination. Three rules now hold for the text of an input that is not a shell command:
//
//   - the short fields come first (where a call goes: a path, an address, a name, a flag) and the long ones last, so an input
//     padded with large fields cannot push the destination out of sight;
//   - an input is shown whole up to MaxReviewBytes; beyond that the long strings are shortened, in the middle, with the number
//     of bytes left out written in the text, and Shortened says so (the UI says it too, and the person may still refuse);
//   - the paths and addresses the input names are listed apart (Targets), each whole: an input whose destination is too long to
//     be read, or that cannot be fitted at all, is refused, not asked about.

const (
	// MaxReviewBytes is the most input text a person is asked to read for one call.
	MaxReviewBytes = 64 << 10
	// shortFieldBytes is the size up to which a field is listed before the long ones.
	shortFieldBytes = 512
	// minLongShown is the least a shortened field keeps.
	minLongShown = 512
	// maxTargetBytes bounds a path or address, which is always shown whole.
	maxTargetBytes = 4 << 10
	// markerRoom is the room left for the "bytes not shown" marker when a text is cut.
	markerRoom = 48
)

// ErrUnreviewable is returned for an input that cannot be put in front of a person within the limits.
var ErrUnreviewable = errors.New("security: the input is too large to show a person")

// targetKeys name the fields whose string value says where a call goes, in the order they are listed. A key ending in path is a
// file; url and uri are addresses.
var targetKeys = []struct {
	key    string
	remote bool
}{{"file_path", false}, {"notebook_path", false}, {"path", false}, {"url", true}, {"uri", true}}

// Review is an input prepared for a person to read.
type Review struct {
	// Text is the input as JSON, the short fields first. Long strings may be shortened (Shortened).
	Text string
	// Targets are the paths and addresses the input names, as written.
	Targets []Target
	// Shortened is set when a long value is cut in Text.
	Shortened bool
	// Bytes is the size of the whole canonical input.
	Bytes int
}

// ReviewInput prepares input (the JSON a tool is called with) for a person. It returns ErrUnreviewable, wrapped, when a
// destination is longer than maxTargetBytes or the input cannot be fitted into MaxReviewBytes.
func ReviewInput(input json.RawMessage) (Review, error) {
	raw := bytes.TrimSpace(input)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	canon, err := canonicalizeRaw(raw)
	if err != nil {
		return Review{}, err
	}
	rv := Review{Bytes: len(canon)}
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return Review{}, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		rv.Text, rv.Shortened = cutMiddle(string(canon), MaxReviewBytes)
		return rv, nil
	}

	type field struct {
		key  []byte // the key as JSON
		val  []byte // the value as canonical JSON
		str  string // the value, when it is a string
		isS  bool
		long bool
		rank int // 0..len(targetKeys)-1: a target; then short fields; then long ones
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := make([]*field, 0, len(keys))
	short := len(targetKeys)
	for _, k := range keys {
		f := &field{}
		var kb, vb bytes.Buffer
		writeCanonicalString(&kb, k)
		if err := writeCanonical(&vb, obj[k]); err != nil {
			return Review{}, err
		}
		f.key, f.val = kb.Bytes(), vb.Bytes()
		f.str, f.isS = obj[k].(string)
		f.rank = short
		for i, tk := range targetKeys {
			if f.isS && k == tk.key {
				if len(f.str) > maxTargetBytes {
					return Review{}, fmt.Errorf("%w: %s is %d bytes long", ErrUnreviewable, k, len(f.str))
				}
				f.rank = i
				break
			}
		}
		if f.rank == short && len(f.val) > shortFieldBytes {
			f.long, f.rank = true, short+1
		}
		fields = append(fields, f)
	}
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].rank < fields[j].rank })
	for _, f := range fields {
		if f.rank < len(targetKeys) && f.str != "" {
			t := Target{Path: f.str}
			if targetKeys[f.rank].remote {
				t = Target{Remote: f.str}
			}
			rv.Targets = append(rv.Targets, t)
		}
	}

	size := 2 // the braces
	longSum, nLong := 0, 0
	for i, f := range fields {
		size += len(f.key) + 1 + len(f.val)
		if i > 0 {
			size++
		}
		if f.long {
			longSum += len(f.val)
			nLong++
		}
	}
	if size > MaxReviewBytes {
		// the long values share what the rest leaves; the smaller ones stay whole
		room := MaxReviewBytes - (size - longSum)
		if nLong == 0 || room < nLong*minLongShown {
			return Review{}, fmt.Errorf("%w: %d bytes in %d fields", ErrUnreviewable, rv.Bytes, len(fields))
		}
		longs := make([]*field, 0, nLong)
		for _, f := range fields {
			if f.long {
				longs = append(longs, f)
			}
		}
		sort.SliceStable(longs, func(i, j int) bool { return len(longs[i].val) < len(longs[j].val) })
		left := room
		for i, f := range longs {
			share := left / (len(longs) - i)
			if len(f.val) <= share {
				left -= len(f.val)
				continue
			}
			text := f.str
			if !f.isS {
				text = string(f.val) // a nested value is shown as its JSON text, cut
			}
			var enc bytes.Buffer
			budget := share
			for try := 0; try < 8; try++ {
				cut, _ := cutMiddle(text, budget-2)
				enc.Reset()
				writeCanonicalString(&enc, cut)
				if enc.Len() <= share {
					break
				}
				budget -= enc.Len() - share + 1 // escapes made it longer than the share: keep less
				if budget < minLongShown/2 {
					budget = minLongShown / 2
				}
			}
			f.val = append([]byte(nil), enc.Bytes()...)
			left -= len(f.val)
			rv.Shortened = true
		}
	}

	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(f.key)
		out.WriteByte(':')
		out.Write(f.val)
	}
	out.WriteByte('}')
	if out.Len() > MaxReviewBytes+MaxReviewBytes/16 { // the escapes of a shortened text may add a little; never a lot
		return Review{}, fmt.Errorf("%w: %d bytes", ErrUnreviewable, rv.Bytes)
	}
	rv.Text = out.String()
	return rv, nil
}

// cutMiddle returns s unchanged when it fits in max bytes, else its beginning and its end with a marker between them that says how
// many bytes were left out. It cuts at character boundaries.
func cutMiddle(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	keep := max - markerRoom
	if keep < 0 {
		keep = 0
	}
	head, tail := keep*3/4, keep-keep*3/4
	for head > 0 && head < len(s) && !utf8.RuneStart(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	if start < head {
		start = head
	}
	return s[:head] + fmt.Sprintf("...[%d bytes not shown]...", start-head) + s[start:], true
}

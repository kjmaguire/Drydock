package terminal

import (
	"bytes"
	"testing"
)

// A flood of output is kept as its last Keep..keepMax bytes, the offsets stay true, and after the buffer has grown to its size
// the cut allocates nothing (it used to copy a megabyte into a new array for every read) (audit 2026-10-16, L9).
func TestTheBufferIsCutInPlaceAndKeepsOffsetsTrue(t *testing.T) {
	var buf []byte
	var start int64
	var total int64
	chunk := make([]byte, 32<<10)
	for i := 0; i < 400; i++ { // about 12.5 MiB
		for j := range chunk {
			chunk[j] = byte(total + int64(j)) // the byte at offset n is byte(n), so the content says where it is
		}
		buf, start = keepTail(buf, start, chunk)
		total += int64(len(chunk))
		if len(buf) > keepMax || (start > 0 && len(buf) < Keep) {
			t.Fatalf("after %d bytes the buffer holds %d (between %d and %d once it is cut)", total, len(buf), Keep, keepMax)
		}
		if start+int64(len(buf)) != total {
			t.Fatalf("offsets drifted: start %d + %d != %d", start, len(buf), total)
		}
	}
	if start == 0 {
		t.Fatal("nothing was ever cut")
	}
	for _, off := range []int{0, 1, len(buf) / 2, len(buf) - 1} {
		if buf[off] != byte(start+int64(off)) {
			t.Fatalf("the byte at offset %d is %d, want %d: the cut moved the wrong bytes", start+int64(off), buf[off], byte(start+int64(off)))
		}
	}
	// at full size, a cut moves the bytes inside the same array: the buffer stays where it is however often it is cut
	small := bytes.Repeat([]byte("x"), 32<<10)
	for cap(buf) < keepMax+len(small) {
		buf, start = keepTail(buf, start, small)
	}
	first := &buf[0]
	cuts := 0
	for i := 0; i < 200; i++ {
		before := start
		buf, start = keepTail(buf, start, small)
		if start != before {
			cuts++
		}
		if &buf[0] != first {
			t.Fatalf("the buffer was copied into a new array at read %d", i)
		}
	}
	if cuts < 3 {
		t.Fatalf("only %d cuts in 200 reads: the check proved little", cuts)
	}
}

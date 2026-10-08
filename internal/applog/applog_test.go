package applog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriterScrubsRotatesAndKeepsOneLinePerEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w := &Writer{Dir: dir, MaxBytes: 300, Keep: 2,
		Scrub: func(s string) (string, error) {
			if strings.Contains(s, "UNSCANNABLE") {
				return "", errors.New("scan failed")
			}
			return strings.ReplaceAll(s, "s3cret-value", "[REDACTED]"), nil
		},
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }}
	w.Printf("started with key %s", "s3cret-value")
	w.Write("two\nlines\r\nhere")
	w.Write("UNSCANNABLE content")
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if strings.Contains(txt, "s3cret-value") || !strings.Contains(txt, "[REDACTED]") || !strings.Contains(txt, "2026-01-02T03:04:05.000Z") {
		t.Fatalf("log %q", txt)
	}
	if strings.Contains(txt, "UNSCANNABLE") || !strings.Contains(txt, "withheld") {
		t.Fatalf("an unscannable line was written: %q", txt)
	}
	if strings.Count(txt, "\n") != 3 || !strings.Contains(txt, "two | lines | here") {
		t.Fatalf("lines %q", txt)
	}
	for i := 0; i < 40; i++ {
		w.Printf("padding line number %d to force rotation", i)
	}
	files := Files(dir, 2)
	if len(files) != 3 {
		t.Fatalf("expected the active file and 2 rotated, have %v", files)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".3")); err == nil {
		t.Fatal("more rotated files than Keep")
	}
	for _, f := range files {
		if st, _ := os.Stat(f); st.Size() > 400 {
			t.Errorf("%s is %d bytes", f, st.Size())
		}
	}
}

func TestWriterIsSafeForConcurrentUseAndNilWriters(t *testing.T) {
	var nilW *Writer
	nilW.Write("ignored")
	w := &Writer{Dir: t.TempDir(), MaxBytes: 1 << 20}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				w.Printf("worker %d line %d", i, j)
			}
		}(i)
	}
	wg.Wait()
	b, _ := os.ReadFile(filepath.Join(w.Dir, FileName))
	if n := strings.Count(string(b), "\n"); n != 400 {
		t.Fatalf("%d lines", n)
	}
}

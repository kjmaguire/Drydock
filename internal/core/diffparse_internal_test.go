package core

import (
	"reflect"
	"testing"
)

const samplePatch = `diff --git a/calc.go b/calc.go
index 1111111..2222222 100644
--- a/calc.go
+++ b/calc.go
@@ -1,4 +1,4 @@
 package gocalc
 
-func Subtract(a, b int) int { return 0 }
+func Subtract(a, b int) int { return a - b }
 // end
@@ -10,2 +10,3 @@ func Add
 x
+y
 z
diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/a b.txt b/c d.txt
similarity index 90%
rename from a b.txt
rename to c d.txt
diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ
diff --git "a/tab\there.txt" "b/tab\there.txt"
--- "a/tab\there.txt"
+++ "b/tab\there.txt"
@@ -1 +1 @@
-+++ looks like a header
+--- still content
`

func TestParseUnifiedDiff(t *testing.T) {
	fs := parseUnifiedDiff(samplePatch)
	want := []DiffFile{
		{Path: "calc.go", Status: "modified", Additions: 2, Deletions: 1},
		{Path: "new.txt", Status: "added", Additions: 1},
		{Path: "old.txt", Status: "deleted", Deletions: 1},
		{Path: "c d.txt", OldPath: "a b.txt", Status: "renamed"},
		{Path: "img.png", Status: "modified", Binary: true},
		{Path: "tab\there.txt", Status: "modified", Additions: 1, Deletions: 1},
	}
	if len(fs) != len(want) {
		t.Fatalf("got %d files: %+v", len(fs), fs)
	}
	for i, w := range want {
		g := fs[i]
		g.hunks = nil
		if !reflect.DeepEqual(g, w) {
			t.Errorf("file %d: got %+v, want %+v", i, g, w)
		}
	}
}

func TestFromHunksMarksTheGaps(t *testing.T) {
	o, m := fromHunks(parseUnifiedDiff(samplePatch)[0].hunks)
	if o != "package gocalc\n\nfunc Subtract(a, b int) int { return 0 }\n// end\n⋯\nx\nz\n" {
		t.Fatalf("original %q", o)
	}
	if m != "package gocalc\n\nfunc Subtract(a, b int) int { return a - b }\n// end\n⋯\nx\ny\nz\n" {
		t.Fatalf("modified %q", m)
	}
}

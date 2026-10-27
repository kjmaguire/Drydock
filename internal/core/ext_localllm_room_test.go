package core

import "testing"

// A download that does not fit says what it needs, what Drydock leaves free and what the drive has, in tenths of a GB.
func TestADownloadThatDoesNotFitSaysWhatItNeedsAndWhatIsKeptFree(t *testing.T) {
	const gb = 1 << 30
	if err := roomFor("acme/tiny-GGUF:Q4_0", gb, 4*gb); err != nil {
		t.Fatal(err)
	}
	err := roomFor("acme/tiny-GGUF:Q4_0", 4*gb/10, 19*gb/10)
	if err == nil || err.Error() != "acme/tiny-GGUF:Q4_0 needs 0.4 GB, and Drydock leaves 2.0 GB free on the drive; it has 1.9 GB free" {
		t.Fatalf("%v", err)
	}
}

package core

import "testing"

// A Needs-you title is the notice's first sentence: a dot inside a path, a file name or a version does not cut it.
func TestNoticeTitleEndsAtTheFirstSentence(t *testing.T) {
	for body, want := range map[string]string{
		`The scheduled backup failed: cannot write C:\Users\k.m\Backups\drydock-2026.10.09.zip. Open Backups to fix it.`: `The scheduled backup failed: cannot write C:\Users\k.m\Backups\drydock-2026.10.09.zip.`,
		"License 1.2 expired.":            "License 1.2 expired.",
		"First line\nsecond line":         "First line",
		"no full stop at all":             "no full stop at all",
		"Restored. The old one was kept.": "Restored.",
	} {
		if got := noticeTitle(body); got != want {
			t.Errorf("noticeTitle(%q) = %q, want %q", body, got, want)
		}
	}
}

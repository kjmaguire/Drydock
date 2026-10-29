package backup

import (
	"os"
	"time"
)

// inUseWait is how long a move of the database waits for Windows to let go of it. Another handle on the file (an antivirus scan
// of a file that just changed, the app that has just closed, a test's previous core) makes a rename fail with a sharing
// violation for a moment; at a start that applies a staged restore, that refusal would leave the restore unapplied.
const inUseWait = 5 * time.Second

// renameFile is os.Rename, tried again while the file is in use by another process (inUse; never off Windows).
var renameFile = os.Rename

func renameWhenFree(from, to string) error {
	deadline := time.Now().Add(inUseWait)
	pause := 25 * time.Millisecond
	for {
		err := renameFile(from, to)
		if err == nil || !inUse(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(pause)
		if pause < 400*time.Millisecond {
			pause *= 2
		}
	}
}

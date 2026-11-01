// Package inuse retries a file operation that Windows refused because another process has the file open for a moment: an
// antivirus scan of a file that just changed, git replacing its config while Drydock reads it, the app that has just closed.
// Off Windows nothing is refused that way, and the operation runs once.
package inuse

import "time"

// Wait is how long Do keeps trying.
const Wait = 5 * time.Second

// is reports a refusal because the file is in use by another process (inuse_windows.go; never off Windows).
var is = isInUse

// Is reports whether err is Windows refusing a file another process has open (a sharing or lock violation).
func Is(err error) bool { return is(err) }

// Do runs op, and again with a growing pause while it fails because the file is in use, for up to Wait. Any other error, and
// the last one when the time is up, is returned as it is.
func Do(op func() error) error {
	deadline := time.Now().Add(Wait)
	pause := 25 * time.Millisecond
	for {
		err := op()
		if err == nil || !is(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(pause)
		if pause < 400*time.Millisecond {
			pause *= 2
		}
	}
}

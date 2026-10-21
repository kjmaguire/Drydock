//go:build windows

package conformance

// processGone cannot probe a PID portably on Windows without racing PID reuse; tree-kill semantics are asserted by the
// Job Object tests in internal/proc (decision P1).
func processGone(pid int) (gone, known bool) { return false, false }

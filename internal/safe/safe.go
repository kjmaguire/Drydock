// Package safe keeps a panic in one goroutine from ending the whole application (audit 2026-10-16, H9).
//
// Drydock is one process that runs every agent, every workflow and every background loop; Go ends the process when any goroutine
// panics and nothing recovers it, and nothing in the code did. A hostile agent, a hostile web page or a file cut by a power loss
// was enough: a slice bound an agent chose, a robots.txt that is not UTF-8, an empty hash file each stopped everything. Every
// goroutine the application starts goes through Go, every handler a library calls on a goroutine of its own goes through Do or a
// deferred Recover, and a panic becomes a report (Reporter) and, for a call, an error. A goroutine that panicked has ended: a
// loop is not restarted behind the report, because what it was doing is in an unknown state.
package safe

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync/atomic"
)

// Panic describes a panic that was recovered.
type Panic struct {
	// Name says where it happened (the name passed to Go, Do or Recover).
	Name string
	// Value is what was passed to panic.
	Value any
	// Stack is the stack of the goroutine at the moment of the panic.
	Stack string
}

// Error is a panic that was turned into the error of a call.
type Error struct{ Panic }

func (e *Error) Error() string { return fmt.Sprintf("internal error in %s: %v", e.Name, e.Value) }

var reporter atomic.Pointer[func(Panic)]

// SetReporter installs the function that is told of every recovered panic, on the goroutine that panicked. The application sets
// it to log the panic and publish an event; until then a panic is written to standard error. Nil restores that. The function
// must not panic itself; if it does the panic is swallowed.
func SetReporter(f func(Panic)) {
	if f == nil {
		reporter.Store(nil)
		return
	}
	reporter.Store(&f)
}

func report(p Panic) {
	defer func() { _ = recover() }() // a reporter that panics must not take the goroutine down after all
	if f := reporter.Load(); f != nil {
		(*f)(p)
		return
	}
	fmt.Fprintf(os.Stderr, "drydock: panic in %s: %v\n%s\n", p.Name, p.Value, p.Stack)
}

// Recover is for a deferred call: it recovers a panic of the function that deferred it, reports it, and when errp is not nil
// stores it there as an *Error unless the function already failed with another error. It does nothing when nothing panicked.
//
//	func (h *Handler) Read(...) (res Result, err error) {
//		defer safe.Recover("acp.Read", &err)
func Recover(name string, errp *error) {
	v := recover()
	if v == nil {
		return
	}
	p := Panic{Name: name, Value: v, Stack: string(debug.Stack())}
	report(p)
	if errp != nil && *errp == nil {
		*errp = &Error{p}
	}
}

// Go runs f on a new goroutine and contains a panic in it.
func Go(name string, f func()) {
	go func() {
		defer Recover(name, nil)
		f()
	}()
}

// Do runs f on the calling goroutine and returns a panic in it as an error (nil when f returned normally).
func Do(name string, f func()) (err error) {
	defer Recover(name, &err)
	f()
	return nil
}

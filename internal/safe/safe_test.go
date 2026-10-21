package safe

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func capture(t *testing.T) (*[]Panic, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var got []Panic
	SetReporter(func(p Panic) { mu.Lock(); got = append(got, p); mu.Unlock() })
	t.Cleanup(func() { SetReporter(nil) })
	return &got, &mu
}

func TestGoContainsAPanicAndReportsIt(t *testing.T) {
	got, mu := capture(t)
	done := make(chan struct{})
	Go("test.loop", func() {
		defer close(done)
		var s []int
		_ = s[len(s)-1+1] // index out of range
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine did not end")
	}
	// the process is still here, which is the point; the report arrives from the same goroutine
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(*got)
		mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 || (*got)[0].Name != "test.loop" || !strings.Contains((*got)[0].Stack, "safe_test.go") || !strings.Contains(((*got)[0].Value).(error).Error(), "index out of range") {
		t.Fatalf("reports %+v", *got)
	}
}

func TestDoReturnsThePanicAsAnErrorAndLeavesAnEarlierErrorAlone(t *testing.T) {
	got, mu := capture(t)
	err := Do("test.call", func() { panic("boom") })
	var se *Error
	if !errors.As(err, &se) || se.Name != "test.call" || se.Value != "boom" || !strings.Contains(err.Error(), "internal error in test.call: boom") {
		t.Fatalf("%v", err)
	}
	if err := Do("test.fine", func() {}); err != nil {
		t.Fatal(err)
	}
	// a function that already failed keeps its own error
	first := errors.New("first")
	f := func() (err error) {
		defer Recover("test.keep", &err)
		err = first
		panic("later")
	}
	if err := f(); err != first {
		t.Fatalf("the earlier error was replaced: %v", err)
	}
	// Recover with nowhere to put the error still contains the panic
	func() {
		defer Recover("test.nowhere", nil)
		panic("quiet")
	}()
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 3 {
		t.Fatalf("%d reports", len(*got))
	}
}

func TestAReporterThatPanicsIsContained(t *testing.T) {
	SetReporter(func(Panic) { panic("the reporter failed") })
	t.Cleanup(func() { SetReporter(nil) })
	if err := Do("test.reporter", func() { panic("x") }); err == nil {
		t.Fatal("the panic was lost")
	}
}

func TestWithoutAReporterAPanicGoesToStandardErrorAndIsStillContained(t *testing.T) {
	SetReporter(nil)
	if err := Do("test.default", func() { panic("default") }); err == nil {
		t.Fatal("the panic was lost")
	}
}

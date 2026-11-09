package runners

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"
)

// A 5xx answer and a network error are tried again, up to the waits given; a 404 or a wrong digest is not.
func TestRetryingTriesAPassingFailureAgain(t *testing.T) {
	waits := []time.Duration{time.Millisecond, time.Millisecond}
	run := func(errs ...error) (int, error) {
		n := 0
		err := Retrying(context.Background(), waits, nil, func() error {
			n++
			if n <= len(errs) {
				return errs[n-1]
			}
			return nil
		})
		return n, err
	}
	e504 := fmt.Errorf("could not download a.zip: HTTP 504")
	if n, err := run(e504, e504); err != nil || n != 3 {
		t.Fatalf("two 504s then the file: %d tries, %v", n, err)
	}
	if n, err := run(e504, e504, e504); err == nil || n != 3 {
		t.Fatalf("a 504 every time: %d tries, %v", n, err)
	}
	netErr := fmt.Errorf("could not download a.zip: %w", &url.Error{Op: "Get", URL: "https://x", Err: errors.New("connection reset")})
	if n, err := run(netErr); err != nil || n != 2 {
		t.Fatalf("a dropped connection: %d tries, %v", n, err)
	}
	for _, e := range []error{fmt.Errorf("could not download a.zip: HTTP 404"), fmt.Errorf("the archive a.zip does not match its SHA-256")} {
		if n, err := run(e); err == nil || n != 1 {
			t.Fatalf("%v: %d tries, %v", e, n, err)
		}
	}
	if Passing(nil) {
		t.Fatal("no error is not a failure")
	}
}

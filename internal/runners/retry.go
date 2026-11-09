package runners

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"time"
)

// RetryWaits are the pauses before each new try of a download that failed on the host's side. CI's downloads failed several
// times on 2026-11-09 with HTTP 504 from GitHub's release host, on files that were there.
var RetryWaits = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}

// passingHTTP is a 5xx answer at the end of a download error ("could not download x: HTTP 504").
var passingHTTP = regexp.MustCompile(`: HTTP 5\d\d$`)

// Passing says whether an error is a passing failure of the host or the way to it (a 5xx answer, a network error) that a new
// try can get past. A refused address, a 404, a wrong digest or a file over its limit is not.
func Passing(err error) bool {
	if err == nil {
		return false
	}
	var ue *url.Error
	var ne net.Error
	return passingHTTP.MatchString(err.Error()) || errors.As(err, &ue) || errors.As(err, &ne)
}

// Retrying runs do, and runs it again after each of waits while it fails with a passing error; said tells what is tried again.
// For tools and tests that fetch releases (cmd/drydock-bundle, the real-release test); the app's own downloads offer the person
// Retry instead.
func Retrying(ctx context.Context, waits []time.Duration, said func(err error, wait time.Duration), do func() error) error {
	for try := 0; ; try++ {
		err := do()
		if err == nil || try >= len(waits) || !Passing(err) {
			return err
		}
		if said != nil {
			said(err, waits[try])
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(waits[try]):
		}
	}
}

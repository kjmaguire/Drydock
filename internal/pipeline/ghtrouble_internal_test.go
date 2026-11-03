package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// C4: the fix a GitHub failure gets: a refusal of the credentials is a sign-in, GitHub or the network not answering is a
// network trouble, anything else names no fix.
func TestGitHubFixSaysWhatFixesTheFailure(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&host.APIError{Status: 401, Message: "Bad credentials"}, FixGHSignIn},
		{errors.New("git push: fatal: could not read Username for 'https://github.com'"), FixGHSignIn},
		{errors.New("gh: To get started with GitHub CLI, please run: gh auth login"), FixGHSignIn},
		{&host.APIError{Status: 502, Message: "Bad Gateway"}, FixNetwork},
		{errors.New("dial tcp: lookup api.github.com: no such host"), FixNetwork},
		{errors.New("git push: fatal: unable to access: Could not resolve host: github.com"), FixNetwork},
		{&host.APIError{Status: 422, Message: "Validation Failed"}, ""},
		{&host.APIError{Status: 403, Message: "Resource not accessible by integration"}, ""},
	} {
		if got := githubFix(c.err); got != c.want {
			t.Errorf("githubFix(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// C4: a transient failure is tried again, at most three times in all; a refusal is not tried again.
func TestRetryGitHubTriesATransientFailureAgainAndARefusalOnce(t *testing.T) {
	ctx := context.Background()
	n := 0
	err := retryGitHub(ctx, 0, time.Millisecond, func() error {
		n++
		if n < 2 {
			return &host.APIError{Status: 503}
		}
		return nil
	})
	if err != nil || n != 2 {
		t.Fatalf("one 503 then success: err %v after %d tries", err, n)
	}
	n = 0
	err = retryGitHub(ctx, 0, time.Millisecond, func() error { n++; return &host.APIError{Status: 502} })
	if err == nil || n != 3 {
		t.Fatalf("GitHub down: err %v after %d tries, want 3", err, n)
	}
	n = 0
	err = retryGitHub(ctx, 0, time.Millisecond, func() error { n++; return &host.APIError{Status: 401} })
	if err == nil || n != 1 {
		t.Fatalf("a refusal was tried %d times", n)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	n = 0
	_ = retryGitHub(cctx, 0, time.Hour, func() error { n++; return &host.APIError{Status: 502} })
	if n != 1 {
		t.Fatalf("a cancelled step was tried %d times", n)
	}
}

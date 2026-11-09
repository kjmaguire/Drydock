package pipeline

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// What a person can do about a GitHub failure of a pull-request step (the "fix" of a "github" gate's payload), worked out by the
// core so the task page and the Inbox offer the right button next to Try again and Stop.
const (
	// FixGHSignIn: GitHub refused the credentials (gh signed out, a revoked or expired token): sign in to GitHub again.
	FixGHSignIn = "gh_signin"
	// FixNetwork: GitHub or the network did not answer (an outage, a timeout, a dropped connection): try again later.
	FixNetwork = "network"
	// FixReviewAgain: the pull request's branch changed after the review: review the new commits before anything is merged.
	FixReviewAgain = "review_again"
)

// GitHubGate is the gate of a question about a GitHub failure of a pull-request step: its answers are Try again ("answered",
// the same step runs again; nothing paid is redone) and Stop ("rejected").
const GitHubGate = "github"

// githubTries and githubRetryDelay bound the retries of a transient GitHub failure before a person is asked (PRConfig can
// change both): 3 tries, about 2 s and then 4 s apart, with jitter.
const (
	githubTries      = 3
	githubRetryDelay = 2 * time.Second
)

// githubFix says what fixes a GitHub failure: FixGHSignIn, FixNetwork, or "" when nothing in particular is known.
func githubFix(err error) string {
	if err == nil {
		return ""
	}
	var ae *host.APIError
	if errors.As(err, &ae) && ae.Status == 401 {
		return FixGHSignIn
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"gh auth login", "bad credentials", "authentication failed", "not logged in", "requires authentication", "could not read username", "permission denied (publickey)", "invalid username or password"} {
		if strings.Contains(msg, s) {
			return FixGHSignIn
		}
	}
	if transientGitHub(err) {
		return FixNetwork
	}
	return ""
}

// transientGitHub says whether a GitHub failure may pass by itself: GitHub answering 5xx, or the network not answering. A
// refusal (401, 403, 404, 405, 409, 422) is not: trying again changes nothing until a person acts.
func transientGitHub(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *host.APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"connection reset", "connection refused", "connection timed out", "could not resolve host", "no such host", "timeout", "temporarily unavailable", "the remote end hung up unexpectedly", "unexpected disconnect", "early eof", "tls handshake", "502 bad gateway", "503 service unavailable", "504 gateway"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// retryGitHub runs a read of GitHub or a push of the reviewed branch, and runs it again on a transient failure, at most tries
// times in all, waiting delay, then twice that, each with up to half of it again as jitter. It returns the last error. It never
// retries a refusal (transientGitHub), and stops when ctx ends.
func retryGitHub(ctx context.Context, tries int, delay time.Duration, fn func() error) error {
	if tries <= 0 {
		tries = githubTries
	}
	if delay <= 0 {
		delay = githubRetryDelay
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = fn(); err == nil || !transientGitHub(err) || i == tries-1 {
			return err
		}
		wait := delay<<i + time.Duration(rand.Int64N(int64(delay)/2+1))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
	return err
}

func (c PRConfig) retry(ctx context.Context, fn func() error) error {
	return retryGitHub(ctx, c.GitHubTries, c.GitHubRetry, fn)
}

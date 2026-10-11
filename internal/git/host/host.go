// Package host is the GitHub integration behind a GitHost-style interface (brief section 13b). It drives the user's
// `gh` CLI (so Drydock never stores a GitHub token) over the REST API: pull requests, check runs and their logs,
// reviews and comments (all untrusted text), issues, merges, releases. REST rather than `gh pr view --json` because the
// porcelain has no review threads and uses GraphQL; REST also supports conditional requests (ETags) for cheap polling.
// Review-thread resolution is the one operation that needs GraphQL (`ResolveThread`).
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Errors.
var (
	ErrNotModified = errors.New("host: not modified (304)")
	ErrRateLimited = errors.New("host: GitHub rate limit")
	ErrAuth        = errors.New("host: not authenticated: run `gh auth login`")
	ErrNotFound    = errors.New("host: not found")
)

// APIError is a non-2xx answer.
type APIError struct {
	Status  int
	Message string
	Path    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github %d on %s: %s", e.Status, e.Path, e.Message)
}

// API performs REST calls. GhCLI shells out to `gh api`; HTTPAPI talks HTTP directly (used for polling with a token the
// caller holds in memory, and by protocol-fixture tests).
type API interface {
	// Do sends the request; body (if any) is JSON-encoded; out (if non-nil) receives the JSON response.
	// etag is the previous ETag for a conditional GET ("" for none); the response's ETag is returned.
	Do(ctx context.Context, method, path string, body any, out any, etag string) (newETag string, err error)
	// Raw returns a response body as text (job logs), following redirects.
	Raw(ctx context.Context, path string) (string, error)
}

// RateLimitError is GitHub refusing a call for its rate limits: the primary limit (429, or 403 with no requests remaining) or
// a secondary limit (403 or 429 with Retry-After, or a message naming it). errors.Is(err, ErrRateLimited) holds for it.
type RateLimitError struct {
	Reset      time.Time     // when the primary limit resets (X-RateLimit-Reset); zero when not given
	RetryAfter time.Duration // how long GitHub asked to wait (Retry-After); zero when not given
	Secondary  bool
}

func (e *RateLimitError) Error() string {
	kind := "rate limit"
	if e.Secondary {
		kind = "secondary rate limit"
	}
	switch {
	case e.RetryAfter > 0:
		return fmt.Sprintf("host: GitHub %s (retry after %s)", kind, e.RetryAfter)
	case !e.Reset.IsZero():
		return fmt.Sprintf("host: GitHub %s (resets %s)", kind, e.Reset.UTC().Format(time.RFC3339))
	}
	return "host: GitHub " + kind
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// Wait is how long to wait before calling again: what GitHub asked for, or until the reset, else fallback; never more than
// limit (when limit > 0).
func (e *RateLimitError) Wait(now time.Time, fallback, limit time.Duration) time.Duration {
	d := fallback
	switch {
	case e.RetryAfter > 0:
		d = e.RetryAfter
	case !e.Reset.IsZero() && e.Reset.After(now):
		d = e.Reset.Sub(now) + time.Second
	}
	if limit > 0 && d > limit {
		d = limit
	}
	return d
}

// GhCLI is the API implementation that uses the user's `gh`.
type GhCLI struct {
	Bin      string // default "gh"
	Hostname string // default github.com
	// Timeout bounds one call (default 60 s; a job log, Raw, gets three times as long). A gh that hangs (a stalled network,
	// a credential prompt that slipped through) must not hold a run and its agent slot forever.
	Timeout time.Duration
}

func (g GhCLI) bin() string {
	if g.Bin != "" {
		return g.Bin
	}
	return "gh"
}

func (g GhCLI) timeout() time.Duration {
	if g.Timeout > 0 {
		return g.Timeout
	}
	return 60 * time.Second
}

// command builds the gh process for one call, bounded by d: the context ends it, and WaitDelay stops waiting for output a
// child of gh might still hold open.
func (g GhCLI) command(ctx context.Context, d time.Duration, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, d)
	cmd := exec.CommandContext(ctx, g.bin(), args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	cmd.WaitDelay = 5 * time.Second
	return cmd, cancel
}

// maxRawBytes is the most of a raw response (a job log) kept: its end, where a failure shows. Callers truncate further.
const maxRawBytes = 1 << 20

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max     int
	buf     []byte
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
		t.dropped = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t.dropped {
		return "[earlier output dropped]\n" + string(t.buf)
	}
	return string(t.buf)
}

func (g GhCLI) Do(ctx context.Context, method, path string, body any, out any, etag string) (string, error) {
	args := []string{"api", "-i", "-X", method, "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28"}
	if g.Hostname != "" {
		args = append(args, "--hostname", g.Hostname)
	}
	if etag != "" {
		args = append(args, "-H", "If-None-Match: "+etag)
	}
	var in []byte
	if body != nil {
		var err error
		if in, err = json.Marshal(body); err != nil {
			return "", err
		}
		args = append(args, "--input", "-")
	}
	args = append(args, path)
	cmd, cancel := g.command(ctx, g.timeout(), args...)
	defer cancel()
	if in != nil {
		cmd.Stdin = bytes.NewReader(in)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	status, headers, payload := parseHTTP(stdout.Bytes())
	if status == 0 && runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if notSignedIn(runErr, msg) {
			return "", ErrAuth
		}
		return "", fmt.Errorf("gh api %s: %v: %s", path, runErr, msg)
	}
	return finish(status, headers, payload, path, out)
}

func (g GhCLI) Raw(ctx context.Context, path string) (string, error) {
	args := []string{"api", "-H", "Accept: application/vnd.github+json", path}
	if g.Hostname != "" {
		args = append([]string{"api", "--hostname", g.Hostname}, args[1:]...)
	}
	cmd, cancel := g.command(ctx, 3*g.timeout(), args...)
	defer cancel()
	stdout := &tailBuffer{max: maxRawBytes}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		if notSignedIn(err, stderr.String()) {
			return stdout.String(), ErrAuth
		}
		return stdout.String(), fmt.Errorf("gh api %s: %v: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ghExitAuth is gh's exit code when a command needs a sign-in that is not there (contract GH-AUTH-01).
const ghExitAuth = 4

// notSignedIn reports whether a failed gh call failed for want of a sign-in: gh's exit code says so, whatever its message
// (in a GitHub Actions job it asks for GH_TOKEN instead of naming gh auth login), and older messages are recognised too.
func notSignedIn(err error, stderr string) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == ghExitAuth {
		return true
	}
	return strings.Contains(stderr, "gh auth login") || strings.Contains(stderr, "authentication")
}

// parseHTTP splits `gh api -i` output ("HTTP/2.0 200 OK\r\nHeader: v\r\n\r\nbody") into status, headers, body.
func parseHTTP(b []byte) (int, http.Header, []byte) {
	h := http.Header{}
	s := string(b)
	if !strings.HasPrefix(s, "HTTP/") {
		return 0, h, b
	}
	sep := strings.Index(s, "\r\n\r\n")
	adv := 4
	if sep < 0 {
		sep = strings.Index(s, "\n\n")
		adv = 2
	}
	if sep < 0 {
		return 0, h, b
	}
	head, body := s[:sep], s[sep+adv:]
	lines := strings.Split(strings.ReplaceAll(head, "\r\n", "\n"), "\n")
	var status int
	// "HTTP/2.0 200 OK": the second field is the status
	if f := strings.Fields(lines[0]); len(f) >= 2 {
		status, _ = strconv.Atoi(f[1])
	}
	for _, l := range lines[1:] {
		if i := strings.Index(l, ":"); i > 0 {
			h.Add(strings.TrimSpace(l[:i]), strings.TrimSpace(l[i+1:]))
		}
	}
	return status, h, []byte(body)
}

func finish(status int, h http.Header, payload []byte, path string, out any) (string, error) {
	etag := h.Get("Etag")
	switch {
	case status == 304:
		return etag, ErrNotModified
	case status == 401:
		return "", ErrAuth
	case status == 404:
		return "", fmt.Errorf("%w: %s", ErrNotFound, path)
	case status == 429 || status == 403 && (h.Get("X-Ratelimit-Remaining") == "0" || h.Get("Retry-After") != "" ||
		strings.Contains(strings.ToLower(string(payload)), "secondary rate limit")):
		return "", rateLimit(h, payload)
	case status < 200 || status >= 300:
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(payload))
		}
		return "", &APIError{Status: status, Message: e.Message, Path: path}
	}
	if out != nil && len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return etag, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return etag, nil
}

func rateLimit(h http.Header, payload []byte) *RateLimitError {
	e := &RateLimitError{}
	var secs int64
	if _, err := fmt.Sscan(h.Get("Retry-After"), &secs); err == nil && secs > 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	if _, err := fmt.Sscan(h.Get("X-Ratelimit-Reset"), &secs); err == nil && secs > 0 {
		e.Reset = time.Unix(secs, 0)
	}
	e.Secondary = e.RetryAfter > 0 && h.Get("X-Ratelimit-Remaining") != "0" || strings.Contains(strings.ToLower(string(payload)), "secondary rate limit")
	return e
}

// HTTPAPI talks to a GitHub-compatible REST endpoint directly.
type HTTPAPI struct {
	BaseURL string // https://api.github.com
	Token   string // held in memory only
	Client  *http.Client
}

func (a HTTPAPI) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (a HTTPAPI) Do(ctx context.Context, method, path string, body any, out any, etag string) (string, error) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequestWithContext(ctx, method, strings.TrimRight(a.BaseURL, "/")+path, rd)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, strings.TrimRight(a.BaseURL, "/")+path, nil)
	}
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return finish(resp.StatusCode, resp.Header, buf.Bytes(), path, out)
}

func (a HTTPAPI) Raw(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(a.BaseURL, "/")+path, nil)
	if err != nil {
		return "", err
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := &tailBuffer{max: maxRawBytes}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(buf.String()), Path: path}
	}
	return buf.String(), nil
}

// GraphQL runs a GraphQL query through `gh api graphql` (used only for review-thread resolution).
func (g GhCLI) GraphQL(ctx context.Context, query string, vars map[string]string) (json.RawMessage, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, v := range vars {
		args = append(args, "-f", k+"="+v)
	}
	cmd, cancel := g.command(ctx, g.timeout(), args...)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if notSignedIn(err, stderr.String()) {
			return nil, ErrAuth
		}
		return nil, fmt.Errorf("gh api graphql: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

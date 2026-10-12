package itfixture

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OAuth is an OAuth 2.0 server fixture for a public client: GET /authorize (authorization code with PKCE S256: it redirects
// at once to redirect_uri with a code and the state, standing in for the person approving), POST /token (grant types
// authorization_code with code_verifier, refresh_token, and urn:ietf:params:oauth:grant-type:device_code), POST /device/code
// (RFC 8628: the first poll answers authorization_pending, the second issues the token). No client secret is accepted or
// needed, as for Linear's PKCE sign-in and Sentry's device flow.
type OAuth struct {
	mu         sync.Mutex
	ClientID   string
	ExpiresIn  int // seconds an access token lives (default 3600)
	challenges map[string]string
	refresh    map[string]bool
	polls      map[string]int
	n          int
	Issued     []string // access tokens, oldest first
	Refreshes  int
}

// NewOAuth creates the fixture for a client id.
func NewOAuth(clientID string) *OAuth {
	return &OAuth{ClientID: clientID, ExpiresIn: 3600, challenges: map[string]string{}, refresh: map[string]bool{}, polls: map[string]int{}}
}

func (o *OAuth) issue(w http.ResponseWriter) {
	o.n++
	acc := "oat_fixture_" + strconv.Itoa(o.n) + "_Kp7Vb2Nq"
	ref := "ort_fixture_" + strconv.Itoa(o.n) + "_Hs4Lw9Dz"
	o.refresh[ref] = true
	o.Issued = append(o.Issued, acc)
	writeJSON(w, 200, map[string]any{"access_token": acc, "refresh_token": ref, "token_type": "Bearer", "expires_in": o.ExpiresIn, "scope": "read write"})
}

// Server returns the HTTP handler.
func (o *OAuth) Server() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/authorize":
			q := r.URL.Query()
			if q.Get("client_id") != o.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
				http.Error(w, "bad authorization request", 400)
				return
			}
			o.n++
			code := "code-" + strconv.Itoa(o.n)
			o.challenges[code] = q.Get("code_challenge")
			u, err := url.Parse(q.Get("redirect_uri"))
			if err != nil {
				http.Error(w, "bad redirect", 400)
				return
			}
			v := u.Query()
			v.Set("code", code)
			v.Set("state", q.Get("state"))
			u.RawQuery = v.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
		case "/device/code":
			if r.PostForm.Get("client_id") != o.ClientID {
				writeJSON(w, 401, map[string]any{"error": "invalid_client"})
				return
			}
			o.n++
			dc := "dev-" + strconv.Itoa(o.n)
			o.polls[dc] = 0
			writeJSON(w, 200, map[string]any{"device_code": dc, "user_code": "ABCD-EFGH", "verification_uri": "http://" + r.Host + "/device",
				"verification_uri_complete": "http://" + r.Host + "/device?user_code=ABCD-EFGH", "expires_in": 600, "interval": 1})
		case "/token":
			f := r.PostForm
			if f.Get("client_id") != o.ClientID {
				writeJSON(w, 401, map[string]any{"error": "invalid_client"})
				return
			}
			switch f.Get("grant_type") {
			case "authorization_code":
				ch, ok := o.challenges[f.Get("code")]
				sum := sha256.Sum256([]byte(f.Get("code_verifier")))
				if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != ch {
					writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
					return
				}
				delete(o.challenges, f.Get("code"))
				o.issue(w)
			case "refresh_token":
				if !o.refresh[f.Get("refresh_token")] {
					writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
					return
				}
				delete(o.refresh, f.Get("refresh_token")) // rotating refresh tokens
				o.Refreshes++
				o.issue(w)
			case "urn:ietf:params:oauth:grant-type:device_code":
				dc := f.Get("device_code")
				n, ok := o.polls[dc]
				if !ok {
					writeJSON(w, 400, map[string]any{"error": "expired_token"})
					return
				}
				o.polls[dc] = n + 1
				if n == 0 {
					writeJSON(w, 400, map[string]any{"error": "authorization_pending"})
					return
				}
				delete(o.polls, dc)
				o.issue(w)
			default:
				writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
			}
		default:
			http.NotFound(w, r)
		}
	})
}

// ---------------------------------------------------------------- GitHub (REST, through host.HTTPAPI in tests)

// GitHubIssue is one issue of the GitHub fixture.
type GitHubIssue struct {
	Number      int
	Title, Body string
	Labels      []string
	Updated     time.Time
	PullRequest bool
	// Closed marks a closed issue. Closing an issue on GitHub updates its updated_at, so set Updated to the close time.
	Closed bool
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.EqualFold(strings.TrimSpace(w), h) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// GitHub is the GitHub REST fixture for the calls gh makes on Drydock's behalf: GET /user, GET /user/repos, GET
// /repos/{owner}/{repo}/issues (state, sort=updated, direction, since, labels, per_page, page). Tests reach it through
// host.HTTPAPI, which speaks the same REST API that gh api does.
type GitHub struct {
	Common
	mu2    sync.Mutex
	Login  string
	Repo   string
	issues []GitHubIssue
}

// NewGitHub creates the fixture for one repository.
func NewGitHub(token, repo string) *GitHub {
	g := &GitHub{Login: "kyle-fixture", Repo: repo}
	g.Scheme, g.Token = "Bearer", token
	return g
}

// Set replaces the issues.
func (g *GitHub) Set(is []GitHubIssue) {
	g.mu2.Lock()
	g.issues = is
	g.mu2.Unlock()
}

// Server returns the HTTP handler.
func (g *GitHub) Server() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.guard(w, r) {
			return
		}
		switch r.URL.Path {
		case "/user":
			g.count("user", nil, false)
			writeJSON(w, 200, map[string]any{"login": g.Login, "name": "Kyle Fixture"})
		case "/user/repos":
			g.count("repos", nil, false)
			writeJSON(w, 200, []any{map[string]any{"full_name": g.Repo, "name": g.Repo, "owner": map[string]any{"login": g.Login}}})
		case "/repos/" + g.Repo + "/issues":
			q := r.URL.Query()
			args := map[string]any{"since": q.Get("since"), "labels": q.Get("labels"), "page": q.Get("page"), "sort": q.Get("sort"), "state": q.Get("state")}
			state := q.Get("state")
			if state == "" {
				state = "open" // GitHub's default
			}
			if f := g.count("issues", args, true); f != nil {
				writeFail(w, f)
				return
			}
			per, _ := strconv.Atoi(q.Get("per_page"))
			pg, _ := strconv.Atoi(q.Get("page"))
			if per <= 0 {
				per = 30
			}
			if pg <= 0 {
				pg = 1
			}
			g.mu2.Lock()
			var out []any
			for _, is := range g.issues {
				if s := q.Get("since"); s != "" {
					if t, err := time.Parse(time.RFC3339, s); err == nil && is.Updated.Before(t) {
						continue
					}
				}
				st := "open"
				if is.Closed {
					st = "closed"
				}
				if state != "all" && state != st {
					continue
				}
				if l := q.Get("labels"); l != "" && !hasAll(is.Labels, strings.Split(l, ",")) {
					continue
				}
				labels := []any{}
				for _, l := range is.Labels {
					labels = append(labels, map[string]any{"name": l})
				}
				m := map[string]any{"number": is.Number, "title": is.Title, "body": is.Body, "state": st, "labels": labels,
					"html_url": "https://github.com/" + g.Repo + "/issues/" + strconv.Itoa(is.Number), "user": map[string]any{"login": "someone"},
					"created_at": is.Updated.Add(-time.Hour).UTC().Format(time.RFC3339), "updated_at": is.Updated.UTC().Format(time.RFC3339)}
				if is.PullRequest {
					m["pull_request"] = map[string]any{"url": "x"}
				}
				if is.Closed {
					m["closed_at"] = is.Updated.UTC().Format(time.RFC3339)
				} else {
					m["closed_at"] = nil
				}
				out = append(out, m)
			}
			g.mu2.Unlock()
			start := (pg - 1) * per
			if start > len(out) {
				start = len(out)
			}
			end := start + per
			if end > len(out) {
				end = len(out)
			}
			writeJSON(w, 200, append([]any{}, out[start:end]...))
		default:
			writeJSON(w, 404, map[string]any{"message": "Not Found"})
		}
	})
}

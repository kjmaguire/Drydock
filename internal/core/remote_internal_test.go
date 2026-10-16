package core

import "testing"

// Only real github.com remote URLs count for pull requests: a local folder or another host whose path merely contains
// "github.com/owner/repo" does not.
func TestParseGitHubRemote(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/kjmaguire/Drydock":     "kjmaguire/Drydock",
		"https://github.com/kjmaguire/Drydock.git": "kjmaguire/Drydock",
		"https://x-token@github.com/o/r.git":       "o/r",
		"git@github.com:o/r.git":                   "o/r",
		"ssh://git@github.com/o/r":                 "o/r",
		"ssh://git@github.com:22/o/r.git":          "o/r",
		"https://github.com/o/r/":                  "o/r",
		"/home/me/mirrors/github.com/o/r":          "",
		`C:\src\github.com\o\r`:                    "",
		"https://gitlab.com/github.com/o/r":        "",
		"https://evil.example/github.com/o/r.git":  "",
		"https://github.com.evil.example/o/r.git":  "",
		"https://github.com/o":                     "",
	} {
		o, n, ok := parseGitHubRemote(url)
		got := ""
		if ok {
			got = o + "/" + n
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", url, got, want)
		}
	}
}

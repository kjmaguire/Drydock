package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// stallingAPI is a labeled test double of the GitHub API: it never answers (an outage, a black-holed network).
type stallingAPI struct{}

func (stallingAPI) Do(ctx context.Context, _, _ string, _ any, _ any, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (stallingAPI) Raw(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type authFailAPI struct{}

func (authFailAPI) Do(context.Context, string, string, any, any, string) (string, error) {
	return "", errors.Join(host.ErrAuth, errors.New("gh: not logged in"))
}
func (authFailAPI) Raw(context.Context, string) (string, error) { return "", host.ErrAuth }

// GitHub that does not answer is reported as such, never as "not signed in" (gh may well be signed in); a real sign-in
// failure still says to sign in.
func TestGitHubStatusTellsATimeoutFromASignedOutGh(t *testing.T) {
	old := githubStatusTimeout
	githubStatusTimeout = 50 * time.Millisecond
	defer func() { githubStatusTimeout = old }()
	st := (&Core{cfg: Config{GitHubAPI: stallingAPI{}}}).GitHubAccountStatus(context.Background())
	if !st.Installed || st.LoggedIn || !st.Unreachable || strings.Contains(st.Error, "not signed in") || !strings.Contains(st.Error, "did not answer") {
		t.Fatalf("timeout: %+v", st)
	}
	st = (&Core{cfg: Config{GitHubAPI: authFailAPI{}}}).GitHubAccountStatus(context.Background())
	if st.Unreachable || !strings.Contains(st.Error, "gh is not signed in") {
		t.Fatalf("signed out: %+v", st)
	}
}

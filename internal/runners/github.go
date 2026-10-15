package runners

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// Repo is a GitHub repository, "owner/name".
type Repo struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ParseRepo reads "owner/name", or a github.com address of the repository.
func ParseRepo(s string) (Repo, error) {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if !strings.EqualFold(u.Host, "github.com") && !strings.EqualFold(u.Host, "www.github.com") {
			return Repo{}, fmt.Errorf("%s is not a github.com repository", s)
		}
		s = strings.Trim(u.Path, "/")
	} else if rest, ok := strings.CutPrefix(s, "git@github.com:"); ok {
		s = rest
	}
	s = strings.TrimSuffix(s, ".git")
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !ownerRe.MatchString(owner) || !nameRe.MatchString(name) || name == "." || name == ".." {
		return Repo{}, fmt.Errorf("%q is not a repository: write it as owner/name", s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// ErrPublic means the repository is not private. A self-hosted runner on a public repository would run the workflows of
// anyone's fork on this PC, so Drydock refuses it (GitHub's own advice).
var ErrPublic = errors.New("not a private repository")

// RepoInfo is what Drydock checks before serving a repository.
type RepoInfo struct {
	FullName   string `json:"full_name"`
	Private    bool   `json:"private"`
	Visibility string `json:"visibility"`
	Archived   bool   `json:"archived"`
	Perms      struct {
		Admin bool `json:"admin"`
	} `json:"permissions"`
}

// CheckRepo reads the repository and refuses one that is public or internal (ErrPublic), archived, or that the signed-in
// account does not administer (registering runners needs that).
func CheckRepo(ctx context.Context, api host.API, r Repo) (RepoInfo, error) {
	var info RepoInfo
	if _, err := api.Do(ctx, "GET", "/repos/"+r.String(), nil, &info, ""); err != nil {
		var ae *host.APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			return info, fmt.Errorf("GitHub does not show %s to the signed-in account", r)
		}
		return info, fmt.Errorf("could not read %s: %w", r, err)
	}
	if !info.Private || (info.Visibility != "" && info.Visibility != "private") {
		vis := info.Visibility
		if vis == "" {
			vis = "public"
		}
		return info, fmt.Errorf("%w: %s is %s, and a self-hosted runner there would run anyone's fork's workflows on this PC", ErrPublic, r, vis)
	}
	if info.Archived {
		return info, fmt.Errorf("%s is archived", r)
	}
	if !info.Perms.Admin {
		return info, fmt.Errorf("the signed-in GitHub account does not administer %s; adding runners needs that", r)
	}
	return info, nil
}

// JIT is a one-job runner registration. Encoded is a credential: it is passed to the runner in its environment and never
// written to a file, the database, a log or an event.
type JIT struct {
	RunnerID int64  `json:"runnerId"`
	Name     string `json:"name"`
	Encoded  string `json:"-"`
}

// RegisterJIT asks GitHub for a just-in-time configuration of an ephemeral runner with these labels.
func RegisterJIT(ctx context.Context, api host.API, r Repo, name string, labels []string) (JIT, error) {
	body := map[string]any{"name": name, "runner_group_id": 1, "labels": labels, "work_folder": "_work"}
	var out struct {
		Runner struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"runner"`
		Encoded string `json:"encoded_jit_config"`
	}
	if _, err := api.Do(ctx, "POST", "/repos/"+r.String()+"/actions/runners/generate-jitconfig", body, &out, ""); err != nil {
		return JIT{}, fmt.Errorf("GitHub did not register a runner for %s: %w", r, err)
	}
	if out.Encoded == "" || out.Runner.ID == 0 {
		return JIT{}, fmt.Errorf("GitHub's answer for %s carried no runner configuration", r)
	}
	return JIT{RunnerID: out.Runner.ID, Name: out.Runner.Name, Encoded: out.Encoded}, nil
}

// RemoveRunner deletes a runner registration. One that is already gone counts as removed.
func RemoveRunner(ctx context.Context, api host.API, r Repo, id int64) error {
	_, err := api.Do(ctx, "DELETE", fmt.Sprintf("/repos/%s/actions/runners/%d", r, id), nil, nil, "")
	var ae *host.APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return nil
	}
	return err
}

// Registered is a runner GitHub lists for the repository.
type Registered struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // online | offline
	Busy   bool   `json:"busy"`
}

// ListRunners lists the repository's self-hosted runners whose name starts with prefix.
func ListRunners(ctx context.Context, api host.API, r Repo, prefix string) ([]Registered, error) {
	var out []Registered
	for page := 1; page <= 20; page++ {
		var p struct {
			Total   int          `json:"total_count"`
			Runners []Registered `json:"runners"`
		}
		if _, err := api.Do(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runners?per_page=100&page=%d", r, page), nil, &p, ""); err != nil {
			return out, err
		}
		for _, x := range p.Runners {
			if strings.HasPrefix(x.Name, prefix) {
				out = append(out, x)
			}
		}
		if len(p.Runners) < 100 {
			break
		}
	}
	return out, nil
}

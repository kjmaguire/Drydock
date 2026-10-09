package host_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/git/host"
)

// Protocol fixtures: the REST shapes GitHub documents for /user, /user/orgs and /user/repos.
func TestAccountListing(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"kyle","name":"Kyle"}`)
		case "/user/orgs":
			fmt.Fprint(w, `[{"login":"zeta"},{"login":"acme"}]`)
		case "/user/repos":
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("affiliation") != "owner,collaborator,organization_member" || r.URL.Query().Get("sort") != "pushed" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("page") == "1" {
				items := []string{}
				for i := 0; i < 100; i++ {
					items = append(items, fmt.Sprintf(`{"full_name":"kyle/r%d","name":"r%d","owner":{"login":"kyle"},"default_branch":"main","private":true}`, i, i))
				}
				fmt.Fprint(w, "["+strings.Join(items, ",")+"]")
				return
			}
			fmt.Fprint(w, `[{"full_name":"acme/shop","name":"shop","description":"Storefront on Workers","owner":{"login":"acme"},"default_branch":"trunk","html_url":"https://github.com/acme/shop"},
			{"full_name":"acme/old","name":"old","owner":{"login":"acme"},"archived":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	api := host.HTTPAPI{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()
	who, err := host.Viewer(ctx, api)
	if err != nil || who.Login != "kyle" {
		t.Fatalf("viewer %+v %v", who, err)
	}
	orgs, err := host.Orgs(ctx, api)
	if err != nil || strings.Join(orgs, ",") != "acme,zeta" {
		t.Fatalf("orgs %v %v", orgs, err)
	}
	all, err := host.ListRepos(ctx, api, "", "")
	if err != nil || len(all) != 101 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("all: %d %v pages %v", len(all), err, pages)
	}
	pages = nil
	acme, _ := host.ListRepos(ctx, api, "ACME", "")
	if len(acme) != 1 || acme[0].FullName != "acme/shop" || acme[0].DefaultBranch != "trunk" {
		t.Fatalf("archived repos are left out, owner matches case-insensitively: %+v", acme)
	}
	pages = nil
	found, _ := host.ListRepos(ctx, api, "", "workers")
	if len(found) != 1 || found[0].Name != "shop" {
		t.Fatalf("query matches the description: %+v", found)
	}
}

func TestValidFullName(t *testing.T) {
	for s, want := range map[string]bool{
		"acme/shop": true, "kyle/my.site_v2": true, "a/b": true,
		"acme/../x": false, "acme/..": false, "https://github.com/a/b": false, "a/b/c": false, "-a/b": false, "acme/shop.git": false, "acme/": false, "/x": false, "a b/c": false,
	} {
		if host.ValidFullName(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

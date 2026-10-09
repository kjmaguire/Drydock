package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// The built-in catalog is a shelf the human picks from: adding a server connects it in the library like a typed one (guard on,
// the catalog's read-only tools declared), idempotently by URL. No server is contacted.
func TestMCPCatalogServersAreAddedToTheLibraryOnce(t *testing.T) {
	a := open(t, "")
	var cat []core.MCPCatalogEntry
	a.call("mcplib.catalog", nil, &cat)
	byID := map[string]core.MCPCatalogEntry{}
	var ids []string
	for _, e := range cat {
		byID[e.ID] = e
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "mslearn,cfdocs,gh,ctx7,linear,notion" {
		t.Fatalf("addable entries (http, an auth the library can send): %v", ids)
	}
	if ml := byID["mslearn"]; ml.Auth != "none" || ml.URL != "https://learn.microsoft.com/api/mcp" || ml.Description == "" || len(ml.ReadOnlyTools) != 3 || ml.InLibrary || len(ml.RecommendedRoles) == 0 {
		t.Fatalf("%+v", ml)
	}
	if byID["ctx7"].Auth != "bearer" || !byID["ctx7"].AuthOptional || byID["gh"].Auth != "bearer" || byID["gh"].AuthOptional || byID["notion"].Auth != "oauth" || byID["linear"].Auth != "oauth" {
		t.Fatalf("auth kinds: %+v", cat)
	}

	var added core.MCPCatalogAdded
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "mslearn"}, &added)
	s := added.Server
	if !added.Created || added.Next != "" || s.ID != "mslearn" || s.Transport != "http" || s.URL != "https://learn.microsoft.com/api/mcp" || !s.Guard || s.HasToken || s.OAuth ||
		strings.Join(s.ReadOnlyTools, ",") != "microsoft_code_sample_search,microsoft_docs_fetch,microsoft_docs_search" || len(s.Tools) != 3 || s.DiscoveredAt != 0 {
		t.Fatalf("added %+v", added)
	}
	// idempotent: the same server comes back, nothing new is stored
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "mslearn"}, &added)
	if added.Created || added.Server.ID != "mslearn" {
		t.Fatalf("second add %+v", added)
	}
	var lib []core.MCPLibraryServer
	a.call("mcplib.list", nil, &lib)
	if len(lib) != 1 {
		t.Fatalf("library %+v", lib)
	}

	// a server the human connected by hand at the same address counts as the same server
	a.call("mcplib.save", core.MCPServerInput{ID: "cfd", Display: "My Cloudflare docs", Transport: "http", URL: "https://docs.mcp.cloudflare.com/mcp/"}, nil)
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "cfdocs"}, &added)
	if added.Created || added.Server.ID != "cfd" {
		t.Fatalf("same URL by hand: %+v", added)
	}
	// a different server already called ctx7 makes the catalog one ctx72
	a.call("mcplib.save", core.MCPServerInput{ID: "ctx7", Display: "Something else", Transport: "http", URL: "https://example.com/mcp"}, nil)
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "ctx7"}, &added)
	if !added.Created || added.Server.ID != "ctx72" || !strings.Contains(added.Next, "without a token") {
		t.Fatalf("id clash: %+v", added)
	}
	// what is still owed is said plainly
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "notion"}, &added)
	if !added.Server.OAuth || !strings.Contains(added.Next, "sign in") {
		t.Fatalf("notion: %+v", added)
	}
	a.call("mcplib.addFromCatalog", map[string]string{"catalogId": "gh"}, &added)
	if added.Server.HasToken || !strings.Contains(added.Next, "token") || !added.Server.Guard {
		t.Fatalf("gh: %+v", added)
	}
	for _, bad := range []string{"sentry", "aikido", "nope", ""} {
		if err := a.callErr("mcplib.addFromCatalog", map[string]string{"catalogId": bad}); err == nil {
			t.Errorf("%q was added", bad)
		}
	}
	a.call("mcplib.catalog", nil, &cat)
	for _, e := range cat {
		want := map[string]string{"mslearn": "mslearn", "cfdocs": "cfd", "ctx7": "ctx72", "notion": "notion", "gh": "gh"}[e.ID]
		if e.LibraryID != want || e.InLibrary != (want != "") {
			t.Errorf("%s: in library %v as %q, want %q", e.ID, e.InLibrary, e.LibraryID, want)
		}
	}
}

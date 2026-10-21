package core

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/repomap"
)

// An empty map says why: no source files it reads, or files read but nothing fitting the budget (the agent should ask
// for a bigger one, not conclude the repository has no code).
func TestAnEmptyRepoMapSaysWhy(t *testing.T) {
	if got := emptyMapText(repomap.Result{Files: 0, Budget: 2048}); !strings.Contains(got, "no source files") {
		t.Fatal(got)
	}
	got := emptyMapText(repomap.Result{Files: 12, Budget: 256})
	if strings.Contains(got, "no source files") || !strings.Contains(got, "12 source files were read") || !strings.Contains(got, "256 tokens") || !strings.Contains(got, "token_budget") {
		t.Fatal(got)
	}
}

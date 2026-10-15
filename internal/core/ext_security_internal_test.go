package core

import (
	"context"
	"strings"
	"testing"
)

// The rules folder decides which rules read the whole project: only a person chooses it (decision P44).
func TestOnlyAPersonChoosesTheRulesFolder(t *testing.T) {
	s := &securityExt{}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		if _, err := s.setRulesDir(context.Background(), "prj", "C:/rules", by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
	}
}

// Only a person starts a scan, of a project or of a folder (decision P61), and chooses the folder scans' rules folder.
func TestOnlyAPersonStartsAScan(t *testing.T) {
	s := &securityExt{}
	for _, by := range []string{"", "human:", "agent:bot", "system"} {
		for _, p := range []scanTarget{{Folder: "C:/code/shop"}, {ProjectID: "prj"}} {
			if _, err := s.start(context.Background(), p, by); err == nil || !strings.Contains(err.Error(), "only a person") {
				t.Fatalf("%q %+v: %v", by, p, err)
			}
		}
		if _, err := s.setWorkspaceRulesDir(context.Background(), "C:/rules", by); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Fatalf("%q: %v", by, err)
		}
	}
}

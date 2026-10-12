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

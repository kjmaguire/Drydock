package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// A relative path would resolve against the folder Drydock runs in, not one the person chose: every place that takes a path
// from the person refuses it and says a full path is needed.
func TestRelativePathsAreRefused(t *testing.T) {
	a := open(t, "")
	var p core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": newRepo(t)}, &p)
	var later core.Project
	a.call("project.create", map[string]any{"name": "later"}, &later)
	for name, c := range map[string]struct {
		method string
		params map[string]any
	}{
		"project repository":  {"project.create", map[string]any{"name": "rel", "repoPath": "code/app"}},
		"attached repository": {"project.attachRepo", map[string]any{"id": later.ID, "repoPath": "./app"}},
		"export file":         {"project.export", map[string]any{"projectId": p.ID, "dest": "out.drydock-project.zip"}},
		"import preview":      {"project.importPreview", map[string]any{"path": "in.drydock-project.zip"}},
		"import":              {"project.import", map[string]any{"path": "in.drydock-project.zip"}},
		"support bundle file": {"supportbundle.create", map[string]any{"dest": "bundle.zip"}},
	} {
		err := a.callErr(c.method, c.params)
		if err == nil || !strings.Contains(err.Error(), "must be a full path") {
			t.Errorf("%s: a relative path was not refused plainly: %v", name, err)
		}
	}
}

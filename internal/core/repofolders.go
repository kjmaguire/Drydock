package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The repository's folders for a join rule (X17): "tasks that touch <folder>" is chosen from the folders the repository has, not
// typed blind. Only directory names are read (never a file's content), two levels deep, hidden folders (.git, .github...) and
// dependency or build folders left out, symbolic links never followed. Nothing leaves the PC.

// MaxRepoFolders is the most folders repo.folders lists.
const MaxRepoFolders = 400

// skipFolder names folders that are never a useful rule: dependencies, build output, caches.
var skipFolder = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true, "bin": true, "obj": true, "__pycache__": true}

// RepoFolders lists the folders under root to depth levels, as slash paths ending in "/" ("cmd/", "cmd/drydock/"), sorted,
// at most max of them. A folder that cannot be read is left out; a root that cannot be read is an error.
func RepoFolders(root string, depth, max int) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("this project has no folder")
	}
	var out []string
	var walk func(dir, rel string, level int) error
	walk = func(dir, rel string, level int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if len(out) >= max {
				return nil
			}
			name := e.Name()
			// a DirEntry of a symbolic link is not a directory: links are never followed
			if !e.IsDir() || strings.HasPrefix(name, ".") || skipFolder[strings.ToLower(name)] {
				continue
			}
			p := rel + name + "/"
			out = append(out, p)
			if level < depth {
				_ = walk(filepath.Join(dir, name), p, level+1) // an unreadable folder is left out
			}
		}
		return nil
	}
	if err := walk(root, "", 1); err != nil {
		return nil, err
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func init() {
	RegisterExtension(Extension{Name: "repofolders", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"repo.folders": bind(func(ctx context.Context, p struct {
				ProjectID string `json:"projectId"`
			}) (any, error) {
				pr, err := c.GetProject(ctx, p.ProjectID)
				if err != nil {
					return nil, err
				}
				return RepoFolders(pr.RepoPath, 2, MaxRepoFolders)
			}),
		}, nil, nil
	}})
}

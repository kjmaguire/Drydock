package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Agent CLIs read configuration from the folder they work in, and some of it runs: a hook, an MCP server, a plugin, a custom
// command. The folder is an agent's worktree, so the configuration is whatever the repository contains and whatever an earlier
// turn of the agent wrote there. Claude is started so that it ignores it (--setting-sources user, --restricted); the others have
// no such switch, and some must be told to trust the folder at all before they run without a person at a terminal (Gemini CLI
// refuses to start headless in a folder it does not trust, and in one it trusts it loads the folder's settings). So Drydock looks
// first: an adapter that lists the paths its CLI acts on (the manifest's repo_config) is not started in a folder that has any of
// them until a person has read the files and trusted exactly those (audit 2026-10-16, M3).

// RepoConfigReader is implemented by an adapter whose CLI acts on configuration files in its working folder.
type RepoConfigReader interface {
	// RepoConfigPaths lists the paths, relative to the working folder with "/" separators, a trailing "/" for a folder.
	RepoConfigPaths() []string
}

// RepoConfigError is the refusal to start a CLI in a folder that carries configuration nobody trusted.
type RepoConfigError struct {
	Agent       string   // the adapter
	Files       []string // what was found, relative to the working folder
	Fingerprint string   // trusting this value trusts exactly these files as they are now
}

func (e *RepoConfigError) Error() string {
	shown, more := e.Files, ""
	if len(shown) > 6 {
		more = fmt.Sprintf(" and %d more", len(shown)-6)
		shown = shown[:6]
	}
	return fmt.Sprintf("%s reads configuration that comes with the repository (%s%s), which can run programs or send what you ask it elsewhere, so Drydock did not start it. "+
		"Read those files, then trust them in the project's settings, or remove them", e.Agent, strings.Join(shown, ", "), more)
}

const (
	maxRepoConfigFiles     = 500
	maxRepoConfigFileBytes = 1 << 20
)

// ScanRepoConfig looks for the given paths under root without following a link out of it. It returns the files found and a
// fingerprint of them: the names, what kind of entry each is, their sizes and the digest of their content, so that a file that
// changed is a different fingerprint. A path that exists but cannot be read counts as found. Nothing found gives "" for both.
func ScanRepoConfig(root string, paths []string) (files []string, fingerprint string, err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = r.Close() }()
	fsys := r.FS()
	var lines []string
	add := func(rel, kind string, size int64, sum string) {
		files = append(files, rel)
		lines = append(lines, rel+"\t"+kind+"\t"+strconv.FormatInt(size, 10)+"\t"+sum)
	}
	digest := func(rel string) string {
		f, err := fsys.Open(rel)
		if err != nil {
			return "unreadable"
		}
		defer func() { _ = f.Close() }()
		h := sha256.New()
		if _, err := io.Copy(h, io.LimitReader(f, maxRepoConfigFileBytes)); err != nil {
			return "unreadable"
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	for _, p := range paths {
		rel := strings.TrimSuffix(p, "/")
		fi, lerr := r.Lstat(rel)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			continue
		case lerr != nil:
			add(rel, "unreadable", 0, "") // present, but not somewhere it can be looked at safely: a link out of the folder, say
			continue
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, _ := r.Readlink(rel)
			add(rel, "link", 0, target)
		case fi.Mode().IsRegular():
			add(rel, "file", fi.Size(), digest(rel))
		case fi.IsDir():
			count := 0
			werr := fs.WalkDir(fsys, rel, func(name string, d fs.DirEntry, err error) error {
				if err != nil {
					add(name, "unreadable", 0, "")
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if count++; count > maxRepoConfigFiles {
					add(rel+"/(more)", "truncated", int64(count), "")
					return fs.SkipAll
				}
				info, ierr := d.Info()
				switch {
				case ierr != nil:
					add(name, "unreadable", 0, "")
				case info.Mode()&fs.ModeSymlink != 0:
					target, _ := r.Readlink(name)
					add(name, "link", 0, target)
				case info.Mode().IsRegular():
					add(name, "file", info.Size(), digest(name))
				default:
					add(name, "special", 0, "")
				}
				return nil
			})
			if werr != nil {
				add(rel, "unreadable", 0, "")
			}
		default:
			add(rel, "special", 0, "")
		}
	}
	if len(files) == 0 {
		return nil, "", nil
	}
	sort.Strings(files)
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return files, hex.EncodeToString(sum[:]), nil
}

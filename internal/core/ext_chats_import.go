package core

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kjmaguire/drydock/internal/chats"
	"github.com/kjmaguire/drydock/internal/wsl"
)

// The import of Claude Code's own chats (decision P109; the service is internal/chats, claudeimport.go): where Claude Code
// keeps its session transcripts on this machine, which project a session's folder belongs to, and which sessions Drydock
// started itself. Only Claude Code's projects folder is handed to the importer; its sign-in and settings beside it are never
// named (decision D1).

// wslReadPath is how this process reaches a file inside a WSL distro: the distro's own file share, \\wsl.localhost\<distro>\...
// (the path the chats of a distro are stored under too). Tests on a Linux host, whose stand-in distro's files are the host's
// own, replace it.
var wslReadPath = wsl.UNC

// claudeConfigDir is Claude Code's config folder on Windows: CLAUDE_CONFIG_DIR when set (the adapter passes it on to every
// claude it starts, so the sessions it continues are there too), else %USERPROFILE%\.claude.
func claudeConfigDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// claudeSources is chats.Deps.ClaudeSources: Claude Code on Windows, and Claude Code in each WSL distro where the Connections
// page finds it installed (its ~/.claude in the distro user's home; a CLAUDE_CONFIG_DIR set inside the distro is not known
// here and so not followed).
func (c *Core) claudeSources(ctx context.Context) []chats.ClaudeSource {
	var out []chats.ClaudeSource
	if dir, err := claudeConfigDir(); err == nil {
		out = append(out, chats.ClaudeSource{Projects: filepath.Join(dir, "projects"), Folder: func(cwd string) (string, bool) {
			if !filepath.IsAbs(cwd) || strings.ContainsAny(cwd, "\x00\r\n") {
				return "", false
			}
			return filepath.Clean(cwd), true
		}})
	} else {
		out = append(out, chats.ClaudeSource{Err: "Your home folder is not known: " + err.Error()})
	}
	if c.wsl == nil {
		return out
	}
	for _, d := range c.Connections.InstalledWSL(ctx, "claude") {
		in, err := c.wslInfo(ctx, d)
		if err != nil || !strings.HasPrefix(in.Home, "/") {
			out = append(out, chats.ClaudeSource{WSLDistro: d, Err: "The WSL distro " + d + " did not say where its user's home is."})
			continue
		}
		paths := in.Paths
		out = append(out, chats.ClaudeSource{WSLDistro: d, Projects: wslReadPath(d, path.Join(in.Home, ".claude", "projects")), Folder: func(cwd string) (string, bool) {
			if !strings.HasPrefix(cwd, "/") || strings.HasPrefix(cwd, "//") || strings.ContainsAny(cwd, "\x00\r\n") {
				return "", false
			}
			return paths.ToWindows(cwd) // a Windows drive under the mount root, else the distro's own file
		}})
	}
	return out
}

// sameChatFolder compares two folders as Windows does (without case) on Windows, exactly elsewhere; a distro's files compare their
// distro without case and their Linux path exactly.
func sameChatFolder(a, b string) bool {
	da, la, okA := wsl.ParseUNC(a)
	db, lb, okB := wsl.ParseUNC(b)
	if okA || okB {
		return okA && okB && strings.EqualFold(da, db) && la == lb
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// chatProjectFor is chats.Deps.ProjectFor: the project whose folder (the one its chats run in) is dir. A session that ran in a
// subfolder belongs to no project: a project's chat runs in the project's folder, and Claude Code keeps a session to the
// folder it ran in.
func (c *Core) chatProjectFor(ctx context.Context, _ string, dir string) string {
	ps, err := c.Projects(ctx)
	if err != nil {
		return ""
	}
	for _, p := range ps {
		if p.RepoPath != "" && sameChatFolder(p.RepoPath, dir) {
			return p.ID
		}
	}
	return ""
}

// drydockSession is chats.Deps.DrydockSession: a session of an agent's run, or one in a folder Drydock made (its data folder:
// task worktrees, chats' private folders; a distro chat's private folder; worktrees next to a repository in a distro).
func (c *Core) drydockSession(ctx context.Context, sessionID, cwd string) bool {
	var n int
	if c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE session_id=?`, sessionID).Scan(&n) == nil && n > 0 {
		return true
	}
	slash := strings.ReplaceAll(cwd, `\`, "/")
	if strings.Contains(slash, "/.drydock-worktrees/") || strings.Contains(slash, "/"+wslChatsDir+"/") {
		return true
	}
	if filepath.IsAbs(cwd) && c.cfg.DataDir != "" {
		if rel, err := filepath.Rel(filepath.Clean(c.cfg.DataDir), filepath.Clean(cwd)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

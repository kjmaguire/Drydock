package proc

import (
	"errors"
	"strconv"
	"strings"
)

// InTree reports whether pid is root or a process root started, directly or through others (decision P122: the MCP gateway's
// peer check asks it about the process on the other end of a connection). On Windows a process Drydock started is in its own Job
// Object and membership is the job's (IsProcessInJob), so a child that outlived its parent still counts and nothing outside the
// job does; for any other root, and on Linux, the chain of parent processes is followed.

// maxTreeDepth bounds the walk up the parents.
const maxTreeDepth = 64

// errNoParent is a parent lookup's answer for a process that is gone or cannot be read.
var errNoParent = errors.New("proc: the parent process is not known")

// inTreeByParents walks from pid up through parent(pid) until it meets root (true), reaches the top or a process it cannot read
// (false), or a cycle a reused pid could make.
func inTreeByParents(root, pid int, parent func(int) (int, error)) bool {
	if root <= 0 || pid <= 0 {
		return false
	}
	seen := map[int]bool{}
	for i := 0; i < maxTreeDepth && pid > 0 && !seen[pid]; i++ {
		if pid == root {
			return true
		}
		seen[pid] = true
		p, err := parent(pid)
		if err != nil {
			return false
		}
		pid = p
	}
	return false
}

// parseStatParent reads the parent pid from a Linux /proc/<pid>/stat line (kept here, built everywhere, so it is tested on every
// platform).
func parseStatParent(s string) (int, error) {
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, errNoParent
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, errNoParent
	}
	p, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, errNoParent
	}
	return p, nil
}

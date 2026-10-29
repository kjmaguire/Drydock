//go:build !windows

package proc

import (
	"os"
	"strconv"
)

// InTree follows the parents through /proc/<pid>/stat (Linux; elsewhere no parent is known, so only root itself is in its tree).
func InTree(root, pid int) (bool, error) {
	return inTreeByParents(root, pid, procParent), nil
}

// procParent reads the parent's pid from /proc/<pid>/stat: the field after the state, after the parenthesised command name
// (which may itself hold spaces and parentheses, so the last ")" ends it).
func procParent(pid int) (int, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, errNoParent
	}
	return parseStatParent(string(b))
}

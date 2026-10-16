//go:build !windows

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// AttachTree returns a function that kills pid and everything it started. pid must lead its own session (a process started
// on a pseudo-terminal does). Its process group is signalled, and on Linux every process of its session too: an interactive
// shell puts each background job in a group of its own.
func AttachTree(pid int) (kill func() error, err error) {
	return func() error {
		for _, p := range sessionMembers(pid) {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}, nil
}

// sessionMembers lists the processes whose session id is sid, from /proc (Linux only; empty elsewhere).
func sessionMembers(sid int) []int {
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	var out []int
	for _, f := range stats {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// fields after the parenthesised command name: state ppid pgrp session ...
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		fs := strings.Fields(s[i+1:])
		if len(fs) < 4 {
			continue
		}
		if sess, _ := strconv.Atoi(fs[3]); sess == sid {
			if p, err := strconv.Atoi(filepath.Base(filepath.Dir(f))); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

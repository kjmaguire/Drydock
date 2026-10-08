//go:build !windows

package mcp

import "syscall"

// treeKillAssertable is false on the development host: the portable proc fallback kills the direct child only
// (decisions.md P1), so grandchild death is asserted on Windows only.
const treeKillAssertable = false

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func killPID(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

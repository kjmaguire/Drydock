package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	noHooksOnce sync.Once
	noHooks     string
)

// NoHooksDir is an empty directory of Drydock's own that git calls use as core.hooksPath, so no hook of the repository (planted
// or the user's own) runs inside Drydock's git calls and the git of the tools it starts (docs/security.md s3; safe points and
// commits never run user hooks, brief s13). If it cannot be created, a path that does not exist serves the same purpose.
func NoHooksDir() string {
	noHooksOnce.Do(func() {
		d, err := os.MkdirTemp("", "drydock-no-hooks-")
		if err != nil {
			d = filepath.Join(os.TempDir(), fmt.Sprintf("drydock-no-hooks-%d-%d", os.Getpid(), time.Now().UnixNano()))
		}
		noHooks = filepath.ToSlash(d)
	})
	return noHooks
}

// GitConfigEnv is the environment that makes any git a tool starts (a scanner that asks `git ls-files`, a build that runs
// `git describe`) run no hook, no fsmonitor program, and ask no submodule or embedded repository whether it is modified. Git
// reads these settings as command-line configuration, which outranks every configuration file, including the one an agent may
// have changed in the repository. Append it to an environment built with ScrubEnv or ToolEnv, which keep no GIT_CONFIG_*
// variable of their own.
func GitConfigEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=" + NoHooksDir(),
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=diff.ignoreSubmodules", "GIT_CONFIG_VALUE_2=all",
	}
}

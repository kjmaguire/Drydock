// fixture is a stand-in for an agent CLI that spawns grandchildren (like a CLI running `npm test`).
// Usage: fixture parent <pidfile>  -> spawns two `sleep` children (which each spawn one grandchild) and waits.
//        fixture sleep [spawn]     -> sleeps 120s; with "spawn" first starts one `sleep` grandchild.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	self, _ := os.Executable()
	switch os.Args[1] {
	case "parent":
		pidfile := os.Args[2]
		var pids []int
		pids = append(pids, os.Getpid())
		for i := 0; i < 2; i++ {
			c := exec.Command(self, "sleep", "spawn", pidfile)
			c.Stdout, c.Stderr = nil, nil
			if err := c.Start(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			pids = append(pids, c.Process.Pid)
		}
		appendPIDs(pidfile, pids)
		time.Sleep(120 * time.Second)
	case "sleep":
		if len(os.Args) > 2 && os.Args[2] == "spawn" {
			c := exec.Command(self, "sleep")
			if err := c.Start(); err == nil {
				appendPIDs(os.Args[3], []int{c.Process.Pid})
			}
		}
		time.Sleep(120 * time.Second)
	}
}

func appendPIDs(path string, pids []int) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	for _, p := range pids {
		fmt.Fprintln(f, p)
	}
}

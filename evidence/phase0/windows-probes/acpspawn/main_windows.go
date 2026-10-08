//go:build windows

// acpspawn starts an ACP agent command (often an npx.cmd / .cmd shim) inside a KILL_ON_JOB_CLOSE job, performs the ACP
// initialize handshake over stdio, prints the result summary, closes the job and reports orphans (by image name).
// Usage: acpspawn <timeout-seconds> <command> [args...]
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	secs, _ := strconv.Atoi(os.Args[1])
	argv := os.Args[2:]

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		fmt.Println("job:", err)
		os.Exit(2)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		fmt.Println("setinfo:", err)
		os.Exit(2)
	}
	// Put this probe process itself in the job (children inherit): the "selfjob" design from D7.
	cur, _ := windows.GetCurrentProcess()
	if err := windows.AssignProcessToJobObject(job, cur); err != nil {
		fmt.Println("assign self (continuing):", err)
	}

	path, lerr := exec.LookPath(argv[0])
	fmt.Printf("LookPath(%q) = %q err=%v\n", argv[0], path, lerr)
	c := exec.Command(argv[0], argv[1:]...)
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		fmt.Println("START FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("started pid", c.Process.Pid)

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": true, "writeTextFile": true}, "terminal": true},
		"clientInfo":         map[string]string{"name": "drydock-win-probe", "version": "0"},
	}}
	b, _ := json.Marshal(req)
	fmt.Fprintf(stdin, "%s\n", b)

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil && m["id"] != nil {
				got <- sc.Text()
				return
			}
		}
		got <- ""
	}()
	ok := false
	select {
	case line := <-got:
		if line != "" {
			var m struct {
				Result struct {
					AgentInfo         map[string]any `json:"agentInfo"`
					ProtocolVersion   any            `json:"protocolVersion"`
					AgentCapabilities map[string]any `json:"agentCapabilities"`
				} `json:"result"`
			}
			_ = json.Unmarshal([]byte(line), &m)
			cb, _ := json.Marshal(m.Result.AgentCapabilities)
			fmt.Printf("INIT OK agentInfo=%v protocol=%v caps=%s\n", m.Result.AgentInfo, m.Result.ProtocolVersion, truncate(string(cb), 600))
			ok = true
		} else {
			fmt.Println("INIT FAILED: stdout closed with no response")
		}
	case <-time.After(time.Duration(secs) * time.Second):
		fmt.Println("INIT TIMEOUT")
	}

	// Kill the tree: closing the job handle kills everything including this process, so print first and flush.
	imgBefore := countImages()
	fmt.Printf("node/cmd processes before kill: %s\n", imgBefore)
	os.Stdout.Sync()
	if ok {
		os.Exit(0) // exit code reflects handshake only; orphan check is done by the caller step after we exit
	}
	os.Exit(1)
}

func countImages() string {
	out, _ := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	n := map[string]int{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Split(l, ",")
		if len(f) > 0 {
			name := strings.Trim(f[0], "\" \r")
			switch strings.ToLower(name) {
			case "node.exe", "cmd.exe", "npx.cmd", "codex.exe", "opencode.exe", "gemini.exe", "goose.exe":
				n[strings.ToLower(name)]++
			}
		}
	}
	return fmt.Sprint(n)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

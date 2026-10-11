package proc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a test: it is the child program re-executed by the tests below.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("DRYDOCK_PROC_HELPER")
	if mode == "" {
		t.Skip("helper only")
	}
	switch mode {
	case "echo": // echo stdin lines to stdout prefixed, exit 3 on EOF
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Println("echo:" + sc.Text())
		}
		os.Exit(3)
	case "sleep":
		time.Sleep(60 * time.Second)
	case "spawn": // start two grandchildren that sleep, print their pids, then sleep
		self, _ := os.Executable()
		for i := 0; i < 2; i++ {
			c := exec.Command(self, "-test.run=TestHelperProcess")
			c.Env = append(os.Environ(), "DRYDOCK_PROC_HELPER=sleep")
			if err := c.Start(); err != nil {
				fmt.Println("spawn error:", err)
				os.Exit(2)
			}
			fmt.Println("child:" + strconv.Itoa(c.Process.Pid))
		}
		time.Sleep(60 * time.Second)
	}
	os.Exit(0)
}

func helperSpec(mode string) Spec {
	self, _ := os.Executable()
	return Spec{Path: self, Args: []string{"-test.run=TestHelperProcess"}, Env: append(os.Environ(), "DRYDOCK_PROC_HELPER="+mode), Label: "test:" + mode}
}

func TestStartEchoAndExitCode(t *testing.T) {
	p, err := New().Start(context.Background(), helperSpec("echo"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(p.Stdin(), "hello")
	r := bufio.NewReader(p.Stdout())
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "echo:hello" {
		t.Fatalf("got %q err=%v", line, err)
	}
	p.Stdin().Close()
	ei, ok := WaitTimeout(p, 10*time.Second)
	if !ok {
		t.Fatal("process did not exit after stdin closed")
	}
	if ei.Code != 3 || ei.Killed {
		t.Fatalf("exit %+v", ei)
	}
}

func TestKillSingleProcess(t *testing.T) {
	p, err := New().Start(context.Background(), helperSpec("sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(true); err != nil {
		t.Fatal(err)
	}
	ei, ok := WaitTimeout(p, 10*time.Second)
	if !ok {
		t.Fatal("process survived Kill")
	}
	if !ei.Killed {
		t.Fatalf("expected Killed, got %+v", ei)
	}
}

func TestContextCancelKills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := New().Start(ctx, helperSpec("sleep"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, ok := WaitTimeout(p, 10*time.Second); !ok {
		t.Fatal("context cancel did not stop the process")
	}
}

func TestStartMissingExecutable(t *testing.T) {
	if _, err := New().Start(context.Background(), Spec{Path: "definitely-not-a-real-binary-xyz"}); err == nil {
		t.Fatal("expected error")
	}
}

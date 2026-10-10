package winverify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestParseTasklistAndPS(t *testing.T) {
	tasklist := "\"drydock-e2e-verify-42.exe\",\"4242\",\"Console\",\"1\",\"35,124 K\"\r\n" +
		"\"drydock-e2e-verify-42.exe\",\"5150\",\"Console\",\"1\",\"12,000 K\"\r\n" +
		"\"other.exe\",\"77\",\"Console\",\"1\",\"1 K\"\r\n"
	ps, err := parseTasklist([]byte(tasklist), "DRYDOCK-E2E-VERIFY-42.EXE")
	if err != nil || len(ps) != 2 || ps[0].PID != 4242 || ps[1].PID != 5150 {
		t.Fatalf("%v %+v", err, ps)
	}
	none, err := parseTasklist([]byte("INFO: No tasks are running which match the specified criteria.\r\n"), "x.exe")
	if err != nil || len(none) != 0 {
		t.Fatalf("%v %+v", err, none)
	}
	psOut := "    1 /sbin/init\n  300 /tmp/s/drydock-e2e-verify-42 -ui /tmp/s/ui\n  301 /tmp/s/drydock-e2e-verify-42\n" +
		"  302 /tmp/s/drydock-e2e-verify-421 -x\n  303 vi /tmp/s/drydock-e2e-verify-42\n"
	got := parsePS([]byte(psOut), "/tmp/s/drydock-e2e-verify-42")
	if len(got) != 2 || got[0].PID != 300 || got[1].PID != 301 {
		t.Fatalf("only the program itself counts, not a longer name or an argument: %+v", got)
	}
}

// TestHelperSleep is not a test: it is the process TestRunningInstancesFindsAProgramUntilItStops starts.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("DRYDOCK_VERIFY_HELPER") != "sleep" {
		t.Skip("helper only")
	}
	time.Sleep(2 * time.Minute)
}

// The leftover-process check sees a running copy of a program (with tasklist on Windows, ps elsewhere) and stops seeing
// it once it has exited.
func TestRunningInstancesFindsAProgramUntilItStops(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "drydock-verify-helper-"+strconv.Itoa(os.Getpid())+exeSuffix(runtime.GOOS))
	if err := copyFile(os.Args[0], exe); err != nil {
		t.Fatal(err)
	}
	os.Chmod(exe, 0o755)
	cmd := exec.Command(exe, "-test.run=^TestHelperSleep$")
	cmd.Env = append(os.Environ(), "DRYDOCK_VERIFY_HELPER=sleep")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var found []RunningProcess
	for i := 0; i < 40 && len(found) == 0; i++ {
		var err error
		if found, err = RunningInstances(context.Background(), exe); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(found) != 1 || found[0].PID != cmd.Process.Pid {
		t.Fatalf("want the helper %d, found %+v", cmd.Process.Pid, found)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if left, err := RunningInstances(context.Background(), exe); err != nil || len(left) != 0 {
		t.Fatalf("after it exited: %v %+v", err, left)
	}
}

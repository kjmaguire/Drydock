package winverify

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// RunningProcess is a process found by RunningInstances.
type RunningProcess struct {
	PID  int
	Line string // what the listing showed
}

// RunningInstances lists the processes started from the executable exe. On Windows it asks tasklist for the image name
// (give the executable a name no other program has); elsewhere it reads `ps` and matches the full path of the program.
func RunningInstances(ctx context.Context, exe string) ([]RunningProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		out, err := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq "+filepath.Base(exe), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return nil, fmt.Errorf("tasklist: %w", err)
		}
		return parseTasklist(out, filepath.Base(exe))
	}
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parsePS(out, exe), nil
}

// parseTasklist reads `tasklist /FO CSV /NH` output: "Image Name","PID","Session Name","Session#","Mem Usage". When
// nothing matches, tasklist prints an INFO line instead of rows.
func parseTasklist(out []byte, image string) ([]RunningProcess, error) {
	var ps []RunningProcess
	r := csv.NewReader(bytes.NewReader(out))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("tasklist output: %w", err)
	}
	for _, rec := range recs {
		if len(rec) < 2 || !strings.EqualFold(strings.TrimSpace(rec[0]), image) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(rec[1]))
		if err != nil {
			continue
		}
		ps = append(ps, RunningProcess{PID: pid, Line: strings.Join(rec, " ")})
	}
	return ps, nil
}

// parsePS reads `ps -eo pid=,args=` output and keeps the processes whose program (first argument) is exe.
func parsePS(out []byte, exe string) []RunningProcess {
	var ps []RunningProcess
	for _, line := range strings.Split(string(out), "\n") {
		pidText, args, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		args = strings.TrimSpace(args)
		if args != exe && !strings.HasPrefix(args, exe+" ") {
			continue
		}
		pid, err := strconv.Atoi(pidText)
		if err != nil {
			continue
		}
		ps = append(ps, RunningProcess{PID: pid, Line: strings.TrimSpace(line)})
	}
	return ps
}

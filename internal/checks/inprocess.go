package checks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/proc"
)

// runInProcess runs an InProcess check and gives it the same on-disk log and truncated output as a command check.
func runInProcess(ctx context.Context, pm proc.Manager, spec Spec, onLine func(string)) (Result, error) {
	start := time.Now()
	res, err := spec.InProcess(ctx, pm, spec)
	if err != nil {
		return res, err
	}
	res.Name, res.Argv = spec.Name, spec.Argv
	res.Duration = time.Since(start)
	logDir := spec.LogDir
	if logDir == "" {
		d, err := os.MkdirTemp("", "drydock-check-*")
		if err != nil {
			return res, err
		}
		logDir = d
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return res, err
	}
	logPath := filepath.Join(logDir, ids.New(spec.Name)+".log")
	if err := os.WriteFile(logPath, []byte(res.Output), 0o644); err != nil {
		return res, err
	}
	res.LogRef = logPath
	if onLine != nil {
		for _, l := range strings.Split(strings.TrimRight(res.Output, "\n"), "\n") {
			onLine(l)
		}
	}
	maxCtx := spec.MaxContext
	if maxCtx <= 0 {
		maxCtx = 6000
	}
	res.Output = Truncate(res.Output, maxCtx, logPath)
	return res, nil
}

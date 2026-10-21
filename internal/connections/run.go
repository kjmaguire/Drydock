package connections

import (
	"context"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// runResult is the outcome of one probe command.
type runResult = proc.ProbeResult

// runProbe runs path with args through the process manager (proc.Probe): an argv array, stdin closed, a throwaway working
// directory, the whole tree killed if it outlives the timeout. This is how every detection and auth probe runs
// (docs/connections.md section 2).
func runProbe(ctx context.Context, pm proc.Manager, path string, args, env []string, timeout time.Duration) runResult {
	return proc.Probe(ctx, pm, proc.Spec{Path: path, Args: args, Env: env, Label: "probe"}, timeout)
}

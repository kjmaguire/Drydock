package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
)

// postCheck reports a deterministic check in the task channel as the owning tool member, at zero model cost.
func (c *Core) postCheck(ctx context.Context, t tasks.Task, r checks.Result) {
	rm, err := c.Room.RoomByTask(ctx, t.ID)
	if err != nil {
		return
	}
	kind := r.Name
	if _, ok := room.ToolForCheck(kind); !ok {
		return // a check kind with no tool member (custom checks) is evidence only
	}
	status := room.CheckPass
	if !r.Passed {
		status = room.CheckFail
	}
	sig := ""
	if !r.Passed {
		sum := sha256.Sum256([]byte(strings.Join(r.FailingTests, ",") + r.DiffHash))
		sig = hex.EncodeToString(sum[:4])
	}
	summary := ""
	if !r.Passed {
		summary = r.Output
	}
	if _, err := c.Room.PostCheckResult(ctx, rm.ID, room.CheckResult{Kind: kind, Status: status, DurationMS: r.Duration.Milliseconds(),
		CommandDigest: digest(strings.Join(r.Argv, " ")), FailureSignature: sig, Summary: summary, LogRef: nonEmpty(r.LogRef, "(no log)"), DiffHash: r.DiffHash, TaskID: t.ID}); err != nil {
		c.logf("post check: %v", err)
	}
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// watchdogNotify tells the human when the circuit breaker acts: a SYSTEM message in the task channel and an event for the
// notification UI. The watchdog never decides anything the human did not configure; this only reports.
func (c *Core) watchdogNotify(ctx context.Context, typ string, tr watchdog.TripRecord, msg string) {
	c.publish(ctx, typ, "", map[string]any{"taskId": tr.TaskID, "signal": tr.Signal, "stage": tr.Stage, "message": msg})
	if rm, err := c.Room.RoomByTask(ctx, tr.TaskID); err == nil {
		if _, err := c.Room.Post(ctx, room.PostParams{RoomID: rm.ID, Type: room.TypeSystem, Author: room.System, Body: msg, TaskID: tr.TaskID, RunID: tr.RunID}); err != nil {
			c.logf("watchdog note: %v", err)
		}
	}
}

var ghRemote = regexp.MustCompile(`github\.com[:/]+([^/\s]+)/([^/\s]+?)(?:\.git)?$`)

// remoteRepo reads the GitHub owner and repository from the project's origin remote.
func remoteRepo(ctx context.Context, repo *git.Repo) (owner, name string, err error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repo.Root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("pr_flow needs an 'origin' remote on GitHub: %w", err)
	}
	m := ghRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", "", fmt.Errorf("origin %q is not a github.com remote", strings.TrimSpace(string(out)))
	}
	return m[1], m[2], nil
}

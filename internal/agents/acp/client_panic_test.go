package acp

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/safe"
)

// The ACP SDK calls these handlers on goroutines of its own and does not recover: a panic in one ended the whole application
// (audit 2026-10-16, H9). Each now answers the agent with an error. A client without a session stands in for any bug that makes a
// handler dereference something that is not there.
func TestAClientHandlerThatPanicsAnswersWithAnErrorInsteadOfEndingTheApplication(t *testing.T) {
	var reported []safe.Panic
	safe.SetReporter(func(p safe.Panic) { reported = append(reported, p) })
	t.Cleanup(func() { safe.SetReporter(nil) })
	c := &client{} // no session: every handler that reaches for it panics
	ctx := context.Background()
	calls := map[string]func() error{
		"SessionUpdate":     func() error { return c.SessionUpdate(ctx, acp.SessionNotification{}) },
		"RequestPermission": func() error { _, err := c.RequestPermission(ctx, acp.RequestPermissionRequest{}); return err },
		"ReadTextFile":      func() error { _, err := c.ReadTextFile(ctx, acp.ReadTextFileRequest{Path: "x"}); return err },
		"WriteTextFile":     func() error { _, err := c.WriteTextFile(ctx, acp.WriteTextFileRequest{Path: "x"}); return err },
		"CreateTerminal":    func() error { _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{}); return err },
		"KillTerminal":      func() error { _, err := c.KillTerminal(ctx, acp.KillTerminalRequest{}); return err },
		"TerminalOutput":    func() error { _, err := c.TerminalOutput(ctx, acp.TerminalOutputRequest{}); return err },
		"ReleaseTerminal":   func() error { _, err := c.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{}); return err },
		"WaitForTerminalExit": func() error {
			_, err := c.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{})
			return err
		},
	}
	for name, call := range calls {
		err := call()
		var se *safe.Error
		if !errors.As(err, &se) {
			t.Errorf("%s: a panic came back as %v", name, err)
		}
	}
	if len(reported) != len(calls) {
		t.Fatalf("%d panics reported for %d handlers", len(reported), len(calls))
	}
}

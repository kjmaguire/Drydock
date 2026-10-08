package mcp

import (
	"context"
	"fmt"
	"os"
)

// ShimSubcommand is the argument that turns the Drydock executable into the stdio shim (Options.ShimCommand is
// {os.Executable(), ShimSubcommand}).
const ShimSubcommand = "mcp-shim"

// MaybeRunShim runs the stdio shim and returns (exit code, true) when args (os.Args) ask for it; otherwise it returns false and
// does nothing. Call it first in main(), before any window or database is opened: the process is spawned by an agent CLI, must
// speak only MCP frames on stdout, and must not touch the application's data.
func MaybeRunShim(args []string) (int, bool) {
	if len(args) < 2 || args[1] != ShimSubcommand {
		return 0, false
	}
	if err := RunShim(context.Background(), os.Stdin, os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2, true
	}
	return 0, true
}

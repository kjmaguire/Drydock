// Command fakeagent runs the scripted fake agent as a standalone process (see package fakeagent).
package main

import (
	"os"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
)

func main() { os.Exit(fakeagent.Run(os.Args[1:], os.Stdin, os.Stdout)) }

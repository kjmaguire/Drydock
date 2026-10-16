package checks

import (
	"slices"
	"strings"
)

// workerCommands is every command line a worker can ever be allowed when the project switched "let workers run this
// project's checks" on (docs/worker-commands.md, tier 1): the check commands Detect produces, nothing appended, no prefixes.
// A worker always gets the whole list, so a go.mod, package.json script or Taskfile target it creates during the turn is
// runnable too; one that does not apply to the project just fails.
//
// These commands run the project's own code (tests, build scripts, package.json scripts), which the worker can edit in the
// same turn, so they are not a boundary on what can run: they only keep the worker from typing other commands directly.
var workerCommands = []string{
	"go build ./...", "go test ./...", "go vet ./...",
	"npm run build --silent", "npm run test --silent", "npm run lint --silent", "npm run typecheck --silent",
	"task build", "task test", "task lint", "task typecheck",
}

// WorkerRules returns the Claude Code allow rules for every worker command, one exact command line each
// ("Bash(go test ./...)").
func WorkerRules() []string {
	out := make([]string, len(workerCommands))
	for i, c := range workerCommands {
		out[i] = "Bash(" + c + ")"
	}
	return out
}

// WorkerCommands returns the command lines of the specs that a worker may run, in order. Specs outside the list (a custom
// check, go run, npx, a shell) are left out: Drydock still runs them after the turn, the worker cannot.
func WorkerCommands(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		for _, c := range workerCommands {
			if slices.Equal(s.Argv, strings.Fields(c)) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

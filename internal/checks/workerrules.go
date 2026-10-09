package checks

import "strings"

// WorkerRules turns detected check specs into Claude Code allow rules, one exact command line each ("Bash(go test ./...)").
// It is the whole of what a worker may run when the project switched "let workers run this project's checks" on
// (docs/worker-commands.md, tier 1): the commands Drydock itself runs after the turn, nothing appended, no prefixes.
//
// Only argv shapes that Detect produces are accepted, so a spec a project or a caller built differently is dropped rather than
// turned into a rule: go build|test|vet ./..., npm run <script> --silent, task <target>. Anything else (go run, npx, a shell)
// never becomes a rule.
func WorkerRules(specs []Spec) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range specs {
		if !workerSafe(s.Argv) {
			continue
		}
		r := "Bash(" + strings.Join(s.Argv, " ") + ")"
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func workerSafe(a []string) bool {
	for _, x := range a {
		if x == "" || strings.ContainsAny(x, "*()\x00\r\n\t;&|<>`$\"'\\") {
			return false
		}
	}
	switch {
	case len(a) == 3 && a[0] == "go" && (a[1] == "build" || a[1] == "test" || a[1] == "vet") && a[2] == "./...":
		return true
	case len(a) == 4 && a[0] == "npm" && a[1] == "run" && a[3] == "--silent" && scriptName(a[2]):
		return true
	case len(a) == 2 && a[0] == "task" && scriptName(a[1]):
		return true
	}
	return false
}

// scriptName accepts the standard check names only; Detect never produces another.
func scriptName(s string) bool {
	switch s {
	case "build", "test", "lint", "typecheck":
		return true
	}
	return false
}

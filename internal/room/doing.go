package room

import (
	"encoding/json"
	"path"
	"strings"
)

// DoingCommandLen is how much of a shell command a doing line keeps.
const DoingCommandLen = 60

// DoingLine is what an agent's tool call is doing, in a few words with its argument (decision P97): "Editing limiter.go",
// "Running go test ./...", "Reading parse.go", "Searching for parseExp". It is read off the tool's name, as the agent CLIs name
// their tools (Claude's Edit and Bash, Gemini's read_file and run_shell_command, OpenCode's edit, an ACP agent's title or kind),
// and from its arguments only the base name of a file, the first DoingCommandLen characters of a command and a search pattern
// are kept. A call it cannot read is "Using <tool>". The caller redacts the line (the Tracker does, with TrackerOptions.Redact).
func DoingLine(name string, args json.RawMessage) string {
	var a map[string]any
	_ = json.Unmarshal(args, &a)
	file := fileArg(a)
	n := strings.ToLower(name)
	if i := strings.LastIndex(n, "__"); i >= 0 { // an MCP tool: mcp__server__tool
		n = n[i+2:]
	}
	// an ACP agent may name a call by its title ("Read parse.go"): without an argument to show, the title says it best
	title := strings.Contains(strings.TrimSpace(name), " ")
	with := func(verb, arg, alone string) string {
		switch {
		case arg != "":
			return verb + " " + arg
		case title:
			return clipLine(name, DoingCommandLen+20)
		}
		return alone
	}
	switch {
	case strings.Contains(n, "todo") || strings.Contains(n, "plan"):
		return "Updating the plan"
	case n == "websearch" || n == "web_search" || n == "google_web_search":
		return with("Searching the web for", clipLine(stringArg(a, "query", "q"), 40), "Searching the web")
	case n == "webfetch" || n == "web_fetch" || n == "fetch":
		return with("Reading", hostOf(stringArg(a, "url")), "Reading a web page")
	case strings.Contains(n, "edit") || strings.Contains(n, "replace") || strings.Contains(n, "patch"):
		return with("Editing", file, "Editing files")
	case strings.Contains(n, "write") || strings.Contains(n, "create_file"):
		return with("Writing", file, "Writing a file")
	case n == "ls" || strings.Contains(n, "list"):
		return with("Listing", file, "Listing files")
	case strings.Contains(n, "grep") || strings.Contains(n, "search") || n == "glob" || strings.Contains(n, "find"):
		return with("Searching for", clipLine(stringArg(a, "pattern", "query", "regex", "glob"), 40), "Searching the code")
	case strings.Contains(n, "read") || n == "view" || n == "cat" || n == "open":
		return with("Reading", file, "Reading files")
	case strings.Contains(n, "bash") || strings.Contains(n, "shell") || strings.Contains(n, "exec") || strings.Contains(n, "command") ||
		strings.Contains(n, "terminal") || n == "run":
		return with("Running", clipLine(commandArg(a), DoingCommandLen), "Running a command")
	case n == "task" || n == "agent":
		return with("Delegating:", clipLine(stringArg(a, "description"), 40), "Delegating to a helper")
	}
	if title {
		return clipLine(name, DoingCommandLen+20)
	}
	if strings.TrimSpace(name) == "" {
		return "Using a tool"
	}
	return "Using " + clipLine(name, 40)
}

// stringArg is the first of the keys that holds a non-empty string.
func stringArg(a map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := a[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// fileArg is the base name of the file or folder a call names (a Windows or a POSIX path), or of an ACP call's first location.
func fileArg(a map[string]any) string {
	p := stringArg(a, "file_path", "filePath", "path", "notebook_path", "absolute_path", "target_file", "file", "filename", "dir_path", "directory")
	if p == "" {
		if locs, ok := a["locations"].([]any); ok && len(locs) > 0 {
			if l, ok := locs[0].(map[string]any); ok {
				p, _ = l["path"].(string)
			}
		}
	}
	p = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/")
	if p == "" {
		return ""
	}
	return clipLine(path.Base(p), 60)
}

// commandArg is the command a shell call runs: a string, or an argv whose shell wrapper (bash -lc "...") is left out.
func commandArg(a map[string]any) string {
	if s := stringArg(a, "command", "cmd", "script"); s != "" {
		return s
	}
	for _, k := range []string{"command", "cmd", "argv"} {
		list, ok := a[k].([]any)
		if !ok || len(list) == 0 {
			continue
		}
		var argv []string
		for _, x := range list {
			if s, ok := x.(string); ok {
				argv = append(argv, s)
			}
		}
		if len(argv) >= 3 && (argv[1] == "-c" || argv[1] == "-lc") {
			return argv[len(argv)-1]
		}
		return strings.Join(argv, " ")
	}
	return ""
}

func hostOf(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "@"); i >= 0 { // never the user and password of a URL
		u = u[i+1:]
	}
	return clipLine(u, 60)
}

// clipLine is s on one line, at most n bytes (cut on a character boundary, with "...").
func clipLine(s string, n int) string { return clip(s, n) }

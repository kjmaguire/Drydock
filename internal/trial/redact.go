package trial

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A run's directory is committed, and the repository is public, so what it keeps of a session has to be safe to publish. The
// Claude Code CLI's first event (system/init) lists the signed-in account's plugins, with the folder each was synced to (which
// names the organization), its connectors and commands, and the working folder of the machine; none of that helps someone
// reading the run, and all of it says which account made it (audit 2026-10-16, L55). RedactTranscript cuts that event down
// and RedactPaths makes the machine's paths generic, in a transcript and in the other recorded files alike.

// initKeep is what the init event keeps: how the session was set up, not who set it up.
var initKeep = []string{"type", "subtype", "model", "permissionMode", "claude_code_version", "output_style", "apiKeySource"}

// RedactTranscript returns the stream-json transcript with its init event reduced to initKeep and the built-in tools, a note
// of what was left out, and the machine's paths made generic everywhere. Running it again changes nothing.
func RedactTranscript(raw []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(raw))
	for len(raw) > 0 {
		line := raw
		rest := []byte(nil)
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			line, rest = raw[:i], raw[i+1:]
		}
		out.Write(RedactPaths(redactInit(line)))
		if rest != nil || bytes.HasSuffix(raw, []byte("\n")) {
			out.WriteByte('\n')
		}
		raw = rest
	}
	return out.Bytes()
}

// redactInit reduces one line if it is the init event, and returns every other line as it was.
func redactInit(line []byte) []byte {
	if !bytes.Contains(line, []byte(`"init"`)) {
		return line
	}
	var ev map[string]json.RawMessage
	if json.Unmarshal(line, &ev) != nil || string(ev["type"]) != `"system"` || string(ev["subtype"]) != `"init"` {
		return line
	}
	if _, done := ev["redacted"]; done {
		return line // already reduced: the count of what was left out is not to be counted again
	}
	kept := map[string]any{}
	for _, k := range initKeep {
		if v, ok := ev[k]; ok {
			kept[k] = v
		}
	}
	var tools []string
	if json.Unmarshal(ev["tools"], &tools) == nil {
		builtin := []string{}
		for _, t := range tools {
			if !strings.Contains(t, "__") && !strings.Contains(t, ":") { // mcp__server__tool and plugin tools name a connector
				builtin = append(builtin, t)
			}
		}
		kept["tools"] = builtin
		kept["toolsLeftOut"] = len(tools) - len(builtin)
	}
	kept["redacted"] = "the account's plugins, connectors, commands, skills and agents, the ids of the session and the machine's paths are left out of a committed transcript's first event, and the machine's paths are made generic in all of it (docs/evals.md)"
	b, err := json.Marshal(kept)
	if err != nil {
		return line
	}
	return b
}

var pathScrubs = []struct {
	re   *regexp.Regexp
	with string
}{
	// the CLI's own state folder: its plugins (the folder names the organization), projects and sessions
	{regexp.MustCompile(`(?:/[^/"\s\\]+)*/\.claude/[^"\s\\]*`), `<home>/.claude/<redacted>`},
	// the development sandbox's per-session scratch folder: /tmp/claude-0/<project>/<session id>/scratchpad
	{regexp.MustCompile(`/tmp/claude-\d+/[^/"\s\\]+/[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}/scratchpad`), `<scratchpad>`},
	{regexp.MustCompile(`/tmp/claude-\d+/[^/"\s\\]+/[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}`), `<session-folder>`},
}

// RedactPaths makes the paths of the machine's Claude state and session folders generic. It leaves everything else as it was,
// so the file stays what it was (valid JSON stays valid: the replacements hold no quote or backslash).
func RedactPaths(raw []byte) []byte {
	for _, s := range pathScrubs {
		raw = s.re.ReplaceAll(raw, []byte(s.with))
	}
	return raw
}

// redactorFor returns what makes the named file of a run directory safe to commit, or nil when the file needs nothing: the
// transcript is the CLI's own record of a session, events and evidence name folders of the machine.
func redactorFor(name string) func([]byte) []byte {
	switch name {
	case "transcript.jsonl":
		return RedactTranscript
	case "events.json", "evidence.json":
		return RedactPaths
	}
	return nil
}

// RedactResults applies the same to every run directory under dir, in place, and returns how many files changed. It is for runs
// recorded before Record.Write did it; a file that is already redacted is left as it is.
func RedactResults(dir string) (changed int, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		redact := redactorFor(d.Name())
		if redact == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := redact(b)
		if bytes.Equal(out, b) {
			return nil
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return err
		}
		changed++
		return nil
	})
	return changed, err
}

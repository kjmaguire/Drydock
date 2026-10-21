package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The Logs tab of the bottom dock (docs/ui.md 2): what a task's checks printed and what its agents streamed, read by byte
// range so a long log is never loaded whole. A log is named by an id Drydock gave it (an evidence row or a node execution);
// the file path always comes from the database, never from the caller.

const logReadMax = 256 << 10

func init() {
	RegisterExtension(Extension{Name: "logs", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"logs.list": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
			}) (any, error) {
				return c.TaskLogs(ctx, p.TaskID)
			}),
			"logs.read": bind(func(ctx context.Context, p struct {
				TaskID string `json:"taskId"`
				ID     string `json:"id"`
				From   int64  `json:"from"`
				Limit  int    `json:"limit"`
				Tail   bool   `json:"tail"`
			}) (any, error) {
				return c.ReadLog(ctx, p.TaskID, p.ID, p.From, p.Limit, p.Tail)
			}),
		}, nil, nil
	}})
}

// LogSource is one log of a task.
type LogSource struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // check | agent
	Title   string `json:"title"`
	Size    int64  `json:"size"`
	Passed  *bool  `json:"passed,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Created int64  `json:"created"`
	Running bool   `json:"running,omitempty"`
}

// LogText is a range of a log as text. Next is the byte offset to continue from; Start is where this text began (after a
// tail read it is past the beginning of the file).
type LogText struct {
	Text  string `json:"text"`
	Start int64  `json:"start"`
	Next  int64  `json:"next"`
	Size  int64  `json:"size"`
}

// TaskLogs lists a task's check logs and agent streams, oldest first.
func (c *Core) TaskLogs(ctx context.Context, taskID string) ([]LogSource, error) {
	ev, err := c.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := []LogSource{}
	for _, e := range ev {
		if path, ok := evidenceLogPath(e.Kind, e.Ref); ok {
			if size, _, ok := runfiles.Stat(path); ok {
				out = append(out, LogSource{ID: "ev:" + strconv.FormatInt(e.ID, 10), Kind: "check", Title: e.Kind, Size: size, Passed: e.Passed, Attempt: e.Attempt, Created: e.Created})
			}
		}
	}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT n.run_id, n.node_id, n.step, n.attempt, n.status, n.partial_ref, n.started_at
		FROM node_executions n JOIN workflow_runs r ON r.id = n.run_id
		WHERE r.task_id = ? AND n.partial_ref <> '' ORDER BY n.started_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run, node, status, ref string
		var step, attempt int
		var started int64
		if err := rows.Scan(&run, &node, &step, &attempt, &status, &ref, &started); err != nil {
			return nil, err
		}
		size, _, ok := runfiles.Stat(ref)
		if !ok || (size == 0 && status != "started") {
			continue // a node that streamed nothing (prepare, guard, finalize) has no log to show
		}
		out = append(out, LogSource{ID: fmt.Sprintf("nx:%s|%s|%d|%d", run, node, step, attempt), Kind: "agent", Title: node + " (step " + strconv.Itoa(step) + ")",
			Size: size, Attempt: attempt, Created: started, Running: status == "started"})
	}
	return out, rows.Err()
}

// evidenceLogPath returns the log file of an evidence row: a check's absolute log path. A diff, a screenshot or a reference
// that is not a file path (a commit, a URL) is not a log.
func evidenceLogPath(kind, ref string) (string, bool) {
	switch kind {
	case "diff", "screenshot", "commit", "pr", "ci", "design":
		return "", false
	}
	return ref, ref != "" && filepath.IsAbs(ref)
}

// logPath resolves a log id of a task to its file. The id must belong to the task.
func (c *Core) logPath(ctx context.Context, taskID, id string) (path string, agent bool, err error) {
	switch {
	case strings.HasPrefix(id, "ev:"):
		n, err := strconv.ParseInt(strings.TrimPrefix(id, "ev:"), 10, 64)
		if err != nil {
			return "", false, errors.New("logs: unknown log")
		}
		ev, err := c.Tasks.Evidence(ctx, taskID)
		if err != nil {
			return "", false, err
		}
		for _, e := range ev {
			if e.ID == n {
				if p, ok := evidenceLogPath(e.Kind, e.Ref); ok {
					return p, false, nil
				}
			}
		}
	case strings.HasPrefix(id, "nx:"):
		f := strings.Split(strings.TrimPrefix(id, "nx:"), "|")
		if len(f) == 4 {
			var ref string
			err := c.DB.Reader().QueryRowContext(ctx, `SELECT n.partial_ref FROM node_executions n JOIN workflow_runs r ON r.id = n.run_id
				WHERE r.task_id = ? AND n.run_id = ? AND n.node_id = ? AND n.step = ? AND n.attempt = ?`, taskID, f[0], f[1], f[2], f[3]).Scan(&ref)
			if err == nil && ref != "" {
				return ref, true, nil
			}
		}
	}
	return "", false, errors.New("logs: unknown log")
}

// ReadLog returns a range of a log as text. An agent stream (JSON lines of chunks) is shown as readable lines; it is read in
// whole lines, so Next always lands on a line boundary. Tail starts near the end instead of at from.
func (c *Core) ReadLog(ctx context.Context, taskID, id string, from int64, limit int, tail bool) (LogText, error) {
	path, agent, err := c.logPath(ctx, taskID, id)
	if err != nil {
		return LogText{}, err
	}
	f, err := runfiles.Open(path) // a run older than 30 days has its logs compacted (retention.go)
	if err != nil {
		return LogText{}, err
	}
	defer f.Close()
	size := f.Size()
	if limit <= 0 || limit > logReadMax {
		limit = logReadMax
	}
	if tail {
		from = max(0, size-int64(limit))
	}
	from = min(max(from, 0), size)
	buf := make([]byte, limit)
	n, err := f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return LogText{}, err
	}
	buf = buf[:n]
	start := from
	if tail && from > 0 {
		// begin on a line boundary rather than mid-line
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf, start = buf[i+1:], from+int64(i+1)
		}
	}
	if !agent {
		return LogText{Text: strings.ToValidUTF8(string(buf), "�"), Start: start, Next: start + int64(len(buf)), Size: size}, nil
	}
	// whole lines only: the last partial line waits for the next read (or for the writer to finish it)
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[:i+1]
	} else if start+int64(len(buf)) < size || len(buf) == limit {
		buf = buf[:0]
	}
	return LogText{Text: renderChunks(buf), Start: start, Next: start + int64(len(buf)), Size: size}, nil
}

// renderChunks turns stream.jsonl lines into what a person reads: the agent's text as it streamed, tool calls and results
// on their own lines, usage and errors labelled. A line that is not a chunk is shown as it is.
func renderChunks(b []byte) string {
	var out strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	midText := false
	line := func(s string) {
		if midText {
			out.WriteString("\n")
			midText = false
		}
		out.WriteString(s + "\n")
	}
	for sc.Scan() {
		var ch workflow.Chunk
		if err := json.Unmarshal(sc.Bytes(), &ch); err != nil || ch.Kind == "" {
			line(strings.ToValidUTF8(sc.Text(), "�"))
			continue
		}
		switch ch.Kind {
		case workflow.ChunkText:
			var d workflow.TextDelta
			_ = ch.Decode(&d)
			out.WriteString(d.Text)
			midText = !strings.HasSuffix(d.Text, "\n")
		case workflow.ChunkThinking:
			var d workflow.TextDelta
			_ = ch.Decode(&d)
			line("[thinking] " + clipLine(d.Text, 2000))
		case workflow.ChunkMessageEnd:
			if midText {
				out.WriteString("\n")
				midText = false
			}
		case workflow.ChunkToolCall:
			var tc workflow.ToolCall
			_ = ch.Decode(&tc)
			line("[tool] " + tc.Name + " " + clipLine(string(tc.Args), 500))
		case workflow.ChunkToolResult:
			var tr workflow.ToolResult
			_ = ch.Decode(&tr)
			label := "[result] "
			if tr.IsError {
				label = "[tool error] "
			}
			line(label + tr.Name + ": " + clipLine(tr.Output, 2000))
		case workflow.ChunkUsage:
			var u workflow.UsageChunk
			_ = ch.Decode(&u)
			line(fmt.Sprintf("[usage] %s in %d, out %d, cached %d (%s)", u.Model, u.InputTokens, u.OutputTokens, u.CachedTokens, u.Source))
		case workflow.ChunkError:
			var e workflow.ErrorChunk
			_ = ch.Decode(&e)
			line("[error] " + e.Message)
		case workflow.ChunkDone:
			var d workflow.DoneChunk
			_ = ch.Decode(&d)
			line("[turn ended] " + d.StopReason)
		case workflow.ChunkLog:
			var d workflow.TextDelta
			_ = ch.Decode(&d)
			line(d.Text)
		default:
			line("[" + ch.Kind + "] " + clipLine(string(ch.Data), 500))
		}
	}
	if midText {
		out.WriteString("\n")
	}
	return out.String()
}

func clipLine(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

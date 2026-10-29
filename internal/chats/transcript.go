package chats

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Reading Claude Code's own session transcripts for the import (decision P109). Claude Code keeps one JSON Lines file per
// session, <config>/projects/<the folder, encoded>/<session id>.jsonl; each line is one event of the session (a user or
// assistant message, a summary, a system note, ...). Only those files are read: the projects folder is listed two levels
// deep, nothing outside it is opened (its credentials, settings and plugins never: decision D1), an entry that is a link or
// anything but a plain folder or file is passed over, and a file or line over its cap is not read. The format is Claude
// Code's own and undocumented: a line Drydock cannot read is skipped and counted, never an error.

// The caps are variables so tests can lower them.
var (
	maxTranscriptBytes int64 = 64 << 20 // a session file larger than this is not read
	maxTranscriptLine        = 8 << 20  // a line longer than this (a pasted picture, a huge tool result) is skipped
	maxTranscriptFiles       = 5000     // session files one listing reads
	maxImportMessages        = 5000     // messages kept from one session (the newest)
)

// sessionIDPattern is what a session file's name must be to be read: Claude Code names it by the session's UUID. The name
// becomes the chat's --resume argument, so it may not start with a dash or hold anything but letters, digits, - and _.
var sessionIDPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,127}$`)

// transcriptFS is how the importer reaches the files: listing a folder and opening a file, nothing else. Tests record what
// it touched through it.
var transcriptFS = struct {
	readDir func(string) ([]os.DirEntry, error)
	lstat   func(string) (os.FileInfo, error)
	open    func(string) (*os.File, error)
}{os.ReadDir, os.Lstat, os.Open}

// sessionFile is one session transcript found in a projects folder.
type sessionFile struct {
	path      string
	sessionID string
	size      int64
	modified  int64 // unix milliseconds
}

// findSessions lists the session files of a Claude Code projects folder: <projects>/<folder>/<session id>.jsonl, the
// newest first, at most limit of them. skipped counts the files passed over (too large, a link).
func findSessions(projects string, limit int) (files []sessionFile, skipped int, err error) {
	dirs, err := transcriptFS.readDir(projects)
	if err != nil {
		return nil, 0, err
	}
	for _, d := range dirs {
		if d.Type() != fs.ModeDir { // a link, a junction or a file: never followed out of the folder
			continue
		}
		entries, err := transcriptFS.readDir(filepath.Join(projects, d.Name()))
		if err != nil {
			skipped++
			continue
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if !ok || !sessionIDPattern.MatchString(id) {
				continue
			}
			if !e.Type().IsRegular() {
				skipped++
				continue
			}
			info, err := e.Info()
			if err != nil || info.Size() > maxTranscriptBytes {
				skipped++
				continue
			}
			files = append(files, sessionFile{path: filepath.Join(projects, d.Name(), e.Name()), sessionID: id, size: info.Size(), modified: info.ModTime().UnixMilli()})
		}
	}
	sortSessionFiles(files)
	if len(files) > limit {
		skipped += len(files) - limit
		files = files[:limit]
	}
	return files, skipped, nil
}

func sortSessionFiles(files []sessionFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified != files[j].modified {
			return files[i].modified > files[j].modified
		}
		return files[i].sessionID < files[j].sessionID
	})
}

// openTranscript opens a session file that is still the plain file it was listed as: not a link (checked before and after
// it is opened, so a file swapped for a link meanwhile is not read through it) and not over the size cap.
func openTranscript(path string) (*os.File, error) {
	before, err := transcriptFS.lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("not a plain file")
	}
	f, err := transcriptFS.open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !os.SameFile(before, st) || !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("the file changed while it was opened")
	}
	if st.Size() > maxTranscriptBytes {
		f.Close()
		return nil, fmt.Errorf("the file is larger than %d MB", maxTranscriptBytes>>20)
	}
	return f, nil
}

// transcriptMsg is one message of a transcript as a chat stores it.
type transcriptMsg struct {
	role   string
	body   string
	tool   string // the tool of a tool call
	callID string // the CLI's id of a tool call, which its result names
	status string // how a tool call ended (ToolOK, ToolFailed, "" unknown)
	at     int64  // unix milliseconds
	msgID  string // the API message id of an assistant text, so its blocks join into one message
}

// transcript is what one session file holds, as far as an import needs it.
type transcript struct {
	sessionID string
	cwd       string // the folder the session ran in (the first line that names one)
	title     string // a title the session was given, else a summary of it, else "" (the first message is used then)
	msgs      []transcriptMsg
	first     int64 // the first and the last time a line names (unix milliseconds; 0 when none does)
	last      int64
	skipped   int // lines that were not JSON Drydock could read, or too long
	dropped   int // the oldest messages left out (more than maxImportMessages)
}

// conversation counts the transcript's user and assistant messages.
func (t transcript) conversation() int {
	n := 0
	for _, m := range t.msgs {
		if m.role == RoleUser || m.role == RoleAssistant {
			n++
		}
	}
	return n
}

// firstUser is the first message the person typed.
func (t transcript) firstUser() string {
	for _, m := range t.msgs {
		if m.role == RoleUser {
			return m.body
		}
	}
	return ""
}

type transcriptLine struct {
	Type        string `json:"type"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	UUID        string `json:"uuid"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	IsAPIError  bool   `json:"isApiErrorMessage"`
	Summary     string `json:"summary"`
	LeafUUID    string `json:"leafUuid"`
	CustomTitle string `json:"customTitle"`
	Message     *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type transcriptBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
}

// parseTranscript reads a session file. Messages the person typed and the model's text answers become user and assistant
// messages; a tool call becomes a one-line tool message ("Used tool Bash") whose result marks it ok or failed; the model's
// reasoning, tool output, pictures, system lines, sub-agent (sidechain) lines and the CLI's own notes (slash commands and
// their output) are left out.
func parseTranscript(r io.Reader, sessionID string) (transcript, error) {
	t := transcript{sessionID: sessionID}
	uuids := map[string]bool{}
	var summaries []struct{ leaf, text string }
	calls := map[string]int{} // tool call id -> index in t.msgs
	var at int64
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, tooLong, err := readTranscriptLine(br, maxTranscriptLine)
		if tooLong {
			t.skipped++
		} else if len(bytes.TrimSpace(line)) > 0 {
			var l transcriptLine
			if json.Unmarshal(line, &l) != nil {
				t.skipped++
			} else {
				if ts, ok := parseStamp(l.Timestamp); ok {
					at = ts
					if t.first == 0 {
						t.first = ts
					}
					t.last = ts
				}
				if l.UUID != "" {
					uuids[l.UUID] = true
				}
				if t.cwd == "" && l.Cwd != "" && !l.IsSidechain {
					t.cwd = l.Cwd
				}
				switch {
				case l.Type == "summary" && strings.TrimSpace(l.Summary) != "":
					summaries = append(summaries, struct{ leaf, text string }{l.LeafUUID, l.Summary})
				case l.Type == "custom-title" && strings.TrimSpace(l.CustomTitle) != "":
					t.title = l.CustomTitle // the person's own name for the session wins; the last one given counts
				case (l.Type == "user" || l.Type == "assistant") && l.Message != nil && !l.IsSidechain && !l.IsMeta:
					t.add(l, at, calls)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return t, err
		}
	}
	if t.title == "" {
		for _, s := range summaries { // a summary of this session's own conversation (others may be carried over)
			if uuids[s.leaf] {
				t.title = s.text
			}
		}
	}
	if t.title == "" && len(summaries) > 0 {
		t.title = summaries[0].text
	}
	if len(t.msgs) > maxImportMessages {
		t.dropped = len(t.msgs) - maxImportMessages
		t.msgs = t.msgs[t.dropped:]
	}
	return t, nil
}

// add appends what one user or assistant line says.
func (t *transcript) add(l transcriptLine, at int64, calls map[string]int) {
	content := bytes.TrimSpace(l.Message.Content)
	var blocks []transcriptBlock
	switch {
	case len(content) > 0 && content[0] == '"':
		var s string
		if json.Unmarshal(content, &s) != nil {
			t.skipped++
			return
		}
		blocks = []transcriptBlock{{Type: "text", Text: s}}
	case len(content) > 0 && content[0] == '[':
		if json.Unmarshal(content, &blocks) != nil {
			t.skipped++
			return
		}
	default:
		return
	}
	for _, b := range blocks {
		switch {
		case b.Type == "text" && l.Type == "user":
			text := strings.TrimSpace(b.Text)
			switch {
			case text == "", strings.HasPrefix(text, "<command-"), strings.HasPrefix(text, "<local-command-"):
				// a slash command the person ran and its output: Claude Code's own, not the conversation
			case strings.HasPrefix(text, "[Request interrupted by user"):
				t.msgs = append(t.msgs, transcriptMsg{role: RoleSystem, body: "Stopped by you.", at: at})
			default:
				t.msgs = append(t.msgs, transcriptMsg{role: RoleUser, body: text, at: at})
			}
		case b.Type == "text" && l.Type == "assistant":
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			role := RoleAssistant
			if l.IsAPIError {
				role = RoleError
			}
			if n := len(t.msgs); n > 0 && role == RoleAssistant && l.Message.ID != "" && t.msgs[n-1].role == RoleAssistant && t.msgs[n-1].msgID == l.Message.ID {
				t.msgs[n-1].body += "\n\n" + text // Claude Code writes each block of a message as a line of its own
				continue
			}
			t.msgs = append(t.msgs, transcriptMsg{role: role, body: text, at: at, msgID: l.Message.ID})
		case b.Type == "tool_use" && l.Type == "assistant":
			name := strings.TrimSpace(b.Name)
			if name == "" {
				name = "a tool"
			}
			if b.ID != "" {
				calls[b.ID] = len(t.msgs)
			}
			t.msgs = append(t.msgs, transcriptMsg{role: RoleTool, body: "Used tool " + name, tool: name, callID: b.ID, at: at})
		case b.Type == "tool_result":
			if i, ok := calls[b.ToolUseID]; ok {
				t.msgs[i].status = ToolOK
				if b.IsError {
					t.msgs[i].status = ToolFailed
				}
			}
		}
	}
}

// readTranscriptLine reads one line of at most max bytes. A longer line is read to its end and dropped (tooLong).
func readTranscriptLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, tooLong, err
	}
}

// parseStamp reads a line's ISO 8601 timestamp as unix milliseconds.
func parseStamp(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 || t.Year() > 3000 {
		return 0, false
	}
	return t.UnixMilli(), true
}

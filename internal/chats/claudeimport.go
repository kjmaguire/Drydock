package chats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/redact"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Importing Claude Code's own chats (decision P109). The person starts it on the Chats screen: Drydock lists the sessions it
// finds in Claude Code's transcripts (on Windows and in each WSL distro where Claude Code is installed), the person picks
// which to import, and each becomes a chat with the session's messages, its original times and its folder. Nothing is written
// to Claude Code's files. The chat keeps the session id, so its next turn continues that session (--resume, as every chat
// continues its own); a session that cannot be resumed falls back to a new one given the conversation, as for any chat.
// Importing a session again does nothing: the session id is unique among imported chats (migration 00134).

// ImportSourceClaudeCode is Chat.ImportedFrom of a chat imported from a Claude Code session.
const ImportSourceClaudeCode = "claude-code"

// maxImportRefs is how many sessions one chat.import takes.
const maxImportRefs = 500

// ClaudeSource is one place where Claude Code keeps its session transcripts.
type ClaudeSource struct {
	// WSLDistro is the WSL distro whose Claude Code wrote them, "" for the one on Windows. An imported chat runs there.
	WSLDistro string
	// Projects is Claude Code's projects folder (<config>/projects) as this process opens it.
	Projects string
	// Folder is the folder a transcript's cwd is, as a chat of this source stores it (the Windows form of a distro's folder);
	// ok is false for a cwd that names no folder there.
	Folder func(cwd string) (string, bool)
	// Err says why the source cannot be read (the distro could not be reached); the rest is ignored then.
	Err string
}

// ImportCandidate is one Claude Code session chat.importList found.
type ImportCandidate struct {
	SessionID string `json:"sessionId"`
	WSLDistro string `json:"wslDistro"` // "" = Claude Code on Windows
	Title     string `json:"title"`
	// Cwd is the folder the session ran in, as the chat will run in it ("" when the transcript names none: see Problem).
	Cwd string `json:"cwd"`
	// ProjectID is the project the chat would be in: the one whose folder the session ran in, else "".
	ProjectID string `json:"projectId"`
	Messages  int    `json:"messages"` // user and assistant messages
	FirstAt   int64  `json:"firstAt"`  // unix milliseconds (0 = unknown)
	LastAt    int64  `json:"lastAt"`
	Size      int64  `json:"size"` // the transcript's bytes
	// ChatID is the chat the session was imported as: imported already, so importing it again does nothing.
	ChatID string `json:"chatId,omitempty"`
	// Problem says why the session cannot be imported ("" = it can).
	Problem string `json:"problem,omitempty"`
}

// ImportSourceInfo is what chat.importList read from one source.
type ImportSourceInfo struct {
	WSLDistro string `json:"wslDistro"`
	Folder    string `json:"folder"`   // the projects folder
	Sessions  int    `json:"sessions"` // sessions listed from it
	Skipped   int    `json:"skipped"`  // files passed over (too large, a link, nothing of a conversation in it)
	Error     string `json:"error,omitempty"`
}

// ImportList is chat.importList.
type ImportList struct {
	Sources  []ImportSourceInfo `json:"sources"`
	Sessions []ImportCandidate  `json:"sessions"` // the most recently active first
}

// ImportRef names one session to import.
type ImportRef struct {
	SessionID string `json:"sessionId"`
	WSLDistro string `json:"wslDistro"`
}

// ImportReq is chat.import.
type ImportReq struct {
	Sessions []ImportRef `json:"sessions"`
}

// ImportedChat is one session imported.
type ImportedChat struct {
	SessionID string `json:"sessionId"`
	Chat      Chat   `json:"chat"`
	Messages  int    `json:"messages"` // messages stored
}

// ImportSkip is one session not imported, and why.
type ImportSkip struct {
	SessionID string `json:"sessionId"`
	WSLDistro string `json:"wslDistro"`
	Reason    string `json:"reason"`
	ChatID    string `json:"chatId,omitempty"` // the chat it was imported as before
}

// ImportResult is what chat.import did.
type ImportResult struct {
	Imported []ImportedChat `json:"imported"`
	Skipped  []ImportSkip   `json:"skipped"`
}

// found is a session file of a source.
type found struct {
	src  ClaudeSource
	file sessionFile
}

// claudeFiles lists the session files of every source, and what each source said.
func (s *Service) claudeFiles(ctx context.Context) ([]found, []ImportSourceInfo) {
	if s.d.ClaudeSources == nil {
		return nil, []ImportSourceInfo{}
	}
	var out []found
	infos := []ImportSourceInfo{}
	for _, src := range s.d.ClaudeSources(ctx) {
		info := ImportSourceInfo{WSLDistro: src.WSLDistro, Folder: src.Projects, Error: src.Err}
		if src.Err == "" {
			files, skipped, err := findSessions(src.Projects, maxTranscriptFiles)
			if err != nil && !errors.Is(err, fs.ErrNotExist) { // no projects folder: Claude Code kept no session there
				info.Error = "Claude Code's sessions could not be read: " + err.Error()
			}
			info.Skipped = skipped
			for _, f := range files {
				out = append(out, found{src: src, file: f})
			}
		}
		infos = append(infos, info)
	}
	return out, infos
}

// readSession reads a session file.
func readSession(f sessionFile) (transcript, error) {
	fh, err := openTranscript(f.path)
	if err != nil {
		return transcript{}, err
	}
	defer fh.Close()
	return parseTranscript(fh, f.sessionID)
}

// importTitle is the title an imported chat gets: the session's own name or summary, else the first line of the first
// message (AutoTitle), one line, at most 200 characters. It is redacted before it is shortened, so a secret cut in two is not
// missed by the scanner.
func (s *Service) importTitle(t transcript) string {
	red := func(text string) string {
		return redact.Lines(cutUTF8(strings.ToValidUTF8(text, "�"), summaryScanBytes), s.d.Redact)
	}
	if title := oneLine(red(t.title)); title != "" {
		return clip(title, 200)
	}
	return AutoTitle(red(t.firstUser()))
}

// ownSession reports whether a session is one Drydock started (a chat's session, recorded on the chat or on any of its turns;
// an agent's run): those are Drydock's already and are not listed. A session imported as a chat is not "own" (importedAs).
func (s *Service) ownSession(ctx context.Context, sessionID, cwd string) bool {
	var n int
	if s.d.DB.Reader().QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM chats WHERE session_id=?) + (SELECT COUNT(*) FROM chat_turns WHERE session_id=?)`,
		sessionID, sessionID).Scan(&n) == nil && n > 0 {
		return true
	}
	return s.d.DrydockSession != nil && s.d.DrydockSession(ctx, sessionID, cwd)
}

// importedAs is the chat a session was imported as ("" = none).
func importedAs(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sessionID string) string {
	var id string
	_ = q.QueryRowContext(ctx, `SELECT id FROM chats WHERE import_session=?`, sessionID).Scan(&id)
	return id
}

// ImportClaudeCodeList lists the Claude Code sessions that can be imported, and those imported already (with their chat),
// the most recently active first. Sessions Drydock started itself are left out. It only reads.
func (s *Service) ImportClaudeCodeList(ctx context.Context) (ImportList, error) {
	files, infos := s.claudeFiles(ctx)
	out := ImportList{Sources: infos, Sessions: []ImportCandidate{}}
	seen := map[string]bool{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return ImportList{}, err
		}
		idx := sourceIndex(out.Sources, f.src)
		if seen[f.file.sessionID] { // the same session in two places (a copied folder): listed once
			out.Sources[idx].Skipped++
			continue
		}
		t, err := readSession(f.file)
		if err != nil || t.conversation() == 0 {
			out.Sources[idx].Skipped++
			continue
		}
		chatID := importedAs(ctx, s.d.DB.Reader(), f.file.sessionID)
		if chatID == "" && s.ownSession(ctx, f.file.sessionID, t.cwd) {
			continue
		}
		seen[f.file.sessionID] = true
		c := ImportCandidate{SessionID: f.file.sessionID, WSLDistro: f.src.WSLDistro, Title: s.importTitle(t), Messages: t.conversation(),
			FirstAt: t.first, LastAt: t.last, Size: f.file.size, ChatID: chatID}
		if c.LastAt == 0 {
			c.LastAt = f.file.modified
		}
		if dir, ok := f.src.Folder(t.cwd); ok && t.cwd != "" {
			c.Cwd = dir
			if s.d.ProjectFor != nil {
				c.ProjectID = s.d.ProjectFor(ctx, f.src.WSLDistro, dir)
			}
		} else {
			c.Problem = "The session does not say which folder it ran in, so it cannot be continued."
		}
		out.Sources[idx].Sessions++
		out.Sessions = append(out.Sessions, c)
	}
	sort.SliceStable(out.Sessions, func(i, j int) bool { return out.Sessions[i].LastAt > out.Sessions[j].LastAt })
	return out, nil
}

func sourceIndex(infos []ImportSourceInfo, src ClaudeSource) int {
	for i, in := range infos {
		if in.WSLDistro == src.WSLDistro && in.Folder == src.Projects {
			return i
		}
	}
	return 0
}

// ImportClaudeCode imports the sessions named: each becomes a chat (Claude Code, on Windows or in the session's WSL distro)
// in the session's folder, in the project whose folder that is (else in none), with the session's messages at their original
// times, redacted, and the session id, so the chat's next turn continues it. A session imported before, one Drydock started
// itself, or one no longer found is skipped with the reason. Claude Code's files are only read.
func (s *Service) ImportClaudeCode(ctx context.Context, r ImportReq) (ImportResult, error) {
	if s.d.Licensed != nil {
		if err := s.d.Licensed(ctx); err != nil {
			return ImportResult{}, err
		}
	}
	if len(r.Sessions) == 0 {
		return ImportResult{}, errors.New("choose the sessions to import")
	}
	if len(r.Sessions) > maxImportRefs {
		return ImportResult{}, fmt.Errorf("at most %d sessions can be imported at once", maxImportRefs)
	}
	caps, ok := s.caps("claude")
	if !ok {
		return ImportResult{}, errors.New("Claude Code is not available in this Drydock")
	}
	mode, _ := permissionMode(caps, "")
	files, _ := s.claudeFiles(ctx)
	byKey := map[string]found{}
	for _, f := range files {
		k := strings.ToLower(f.src.WSLDistro) + "\x00" + f.file.sessionID
		if _, dup := byKey[k]; !dup {
			byKey[k] = f
		}
	}
	res := ImportResult{Imported: []ImportedChat{}, Skipped: []ImportSkip{}}
	done := map[string]bool{}
	for _, ref := range r.Sessions {
		ref.SessionID, ref.WSLDistro = strings.TrimSpace(ref.SessionID), strings.TrimSpace(ref.WSLDistro)
		skip := func(reason, chatID string) {
			res.Skipped = append(res.Skipped, ImportSkip{SessionID: ref.SessionID, WSLDistro: ref.WSLDistro, Reason: reason, ChatID: chatID})
		}
		if done[ref.SessionID] {
			continue
		}
		done[ref.SessionID] = true
		f, ok := byKey[strings.ToLower(ref.WSLDistro)+"\x00"+ref.SessionID]
		if !ok || !sessionIDPattern.MatchString(ref.SessionID) {
			skip("The session was not found in Claude Code's sessions any more.", "")
			continue
		}
		t, err := readSession(f.file)
		if err != nil {
			skip("The session could not be read: "+err.Error(), "")
			continue
		}
		if t.conversation() == 0 {
			skip("The session holds no messages.", "")
			continue
		}
		if id := importedAs(ctx, s.d.DB.Reader(), ref.SessionID); id != "" {
			skip("Imported already.", id)
			continue
		}
		if s.ownSession(ctx, ref.SessionID, t.cwd) {
			skip("Drydock started this session itself.", "")
			continue
		}
		dir, ok := f.src.Folder(t.cwd)
		if !ok || t.cwd == "" {
			skip("The session does not say which folder it ran in, so it cannot be continued.", "")
			continue
		}
		project := ""
		if s.d.ProjectFor != nil {
			project = s.d.ProjectFor(ctx, f.src.WSLDistro, dir)
		}
		c, n, existing, err := s.importOne(ctx, t, f.src.WSLDistro, dir, project, mode)
		switch {
		case existing != "":
			skip("Imported already.", existing)
		case err != nil:
			skip("The session could not be imported: "+err.Error(), "")
		default:
			res.Imported = append(res.Imported, ImportedChat{SessionID: ref.SessionID, Chat: c, Messages: n})
			s.publish(ctx, "chat.created", c.ProjectID, map[string]any{"chatId": c.ID, "chat": c})
		}
	}
	return res, nil
}

// importOne stores one session as a chat in one transaction: the chat, its messages and a note that says where it came from.
// existing is the chat the session was imported as before (nothing is written then).
func (s *Service) importOne(ctx context.Context, t transcript, distro, dir, project, mode string) (c Chat, stored int, existing string, err error) {
	now := storage.Now()
	c = Chat{ID: ids.New(strings.TrimSuffix(AgentPrefix, "_")), ProjectID: project, Title: s.importTitle(t), Adapter: "claude", WSLDistro: distro,
		PermissionMode: mode, Cwd: dir, SessionID: t.sessionID, AllowedTools: []string{}, ReadHistory: true, Skills: []SkillRef{}, MCPServers: []MCPGrant{},
		Status: StatusIdle, Created: t.first, Updated: t.last, Version: 1, ImportedFrom: ImportSourceClaudeCode, ImportedSession: t.sessionID}
	if c.Created == 0 {
		c.Created = now
	}
	if c.Updated < c.Created {
		c.Updated = c.Created
	}
	c.SkipsApprovals = SkipsApprovals(c.PermissionMode)
	notes := []string{}
	if t.dropped > 0 {
		notes = append(notes, fmt.Sprintf("The %d earlier messages of the Claude Code session were not imported; the session itself still has them.", t.dropped))
	}
	err = s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		stored, existing = 0, ""
		if existing = importedAs(ctx, tx, t.sessionID); existing != "" {
			return nil
		}
		if err := insertChatTx(ctx, tx, c); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET import_source=?, import_session=? WHERE id=?`, c.ImportedFrom, c.ImportedSession, c.ID); err != nil {
			return err
		}
		seq := 0
		add := func(role, body, tool, status string, at int64) error {
			seq++
			body = strings.ToValidUTF8(body, "�")
			if len(body) > s.o.MaxBodyBytes {
				body = cutUTF8(body, s.o.MaxBodyBytes) + "\n[cut by Drydock: the message was longer than it keeps]"
			}
			if at == 0 {
				at = c.Created
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(id,chat_id,seq,role,body,tool_name,tool_input,turn_id,created_at,tool_status) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				ids.New("cmsg"), c.ID, seq, role, redact.Lines(body, s.d.Redact), s.d.Redact(tool), "", "", at, status)
			return err
		}
		for _, n := range notes {
			if err := add(RoleSystem, n, "", "", c.Created); err != nil {
				return err
			}
		}
		for _, m := range t.msgs {
			if err := add(m.role, m.body, m.tool, m.status, m.at); err != nil {
				return err
			}
		}
		stored = len(t.msgs)
		return add(RoleSystem, "Imported from Claude Code (session "+t.sessionID+"). The next message continues that session in "+c.Cwd+".", "", "", now)
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") { // imported meanwhile by another call
			if id := importedAs(ctx, s.d.DB.Reader(), t.sessionID); id != "" {
				return Chat{}, 0, id, nil
			}
		}
		return Chat{}, 0, "", err
	}
	if existing != "" {
		return Chat{}, 0, existing, nil
	}
	got, err := s.Get(ctx, c.ID)
	if err != nil {
		return Chat{}, 0, "", err
	}
	return got, stored, "", nil
}

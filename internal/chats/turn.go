package chats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

var (
	errStopped  = errors.New("stopped by you")
	errShutdown = errors.New("the app stopped while this turn was running")
)

// ResumeFailedNote is the system message stored when a chat's CLI session could not be resumed and a fresh session was given
// the transcript instead.
const ResumeFailedNote = "Continued in a new session (the previous one could not be resumed)"

func (s *Service) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id] != nil
}

// SendReq is chat.send.
type SendReq struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Attach []string `json:"attach"` // ids of other chats whose transcripts this turn is given
	// Files are files and pictures sent with the message (P90): at most MaxFiles, each at most MaxFileBytes.
	Files []FileIn `json:"files"`

	replaceFrom int64        // Resend: the person's message this turn replaces, with everything after it
	replaceID   string       // and that message's id, checked again once the turn holds the chat
	keep        []Attachment // and its files, which the new message keeps
}

// ResendReq is chat.resend: the person's message at Seq is replaced by Text (the same text when empty: regenerate), the
// messages after it are removed, and a new turn runs from there (P89).
type ResendReq struct {
	ID   string `json:"id"`
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// ErrNotYourMessage is returned for a resend of a message that is not the person's own.
var ErrNotYourMessage = errors.New("only your own message can be edited and sent again")

// Resend edits the person's message at r.Seq (or keeps its text, to regenerate the answer) and runs the turn again from there.
// The messages from that one on are removed, and the CLI session is dropped, since a session cannot be rewound to an earlier
// message: the new turn starts a fresh session that is given the conversation before it as quoted history (the fallback a
// fork's first turn takes). Everything happens once the turn holds the chat, so nothing is removed if it cannot start.
func (s *Service) Resend(ctx context.Context, r ResendReq) (SendResult, error) {
	if r.Seq < 1 {
		return SendResult{}, errors.New("name the message to send again")
	}
	var id, role, body string
	err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT id,role,body FROM chat_messages WHERE chat_id=? AND seq=?`, r.ID, r.Seq).Scan(&id, &role, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return SendResult{}, errors.New("that message is not in this chat")
	}
	if err != nil {
		return SendResult{}, err
	}
	if role != RoleUser {
		return SendResult{}, ErrNotYourMessage
	}
	text := r.Text
	if strings.TrimSpace(text) == "" {
		text = body
	}
	var atts string
	_ = s.d.DB.Reader().QueryRowContext(ctx, `SELECT attachments FROM chat_messages WHERE id=?`, id).Scan(&atts)
	return s.Send(ctx, SendReq{ID: r.ID, Text: text, replaceFrom: r.Seq, replaceID: id, keep: attachmentsOf(atts)})
}

// SendResult is what chat.send returns.
type SendResult struct {
	TurnID string `json:"turnId"`
	// Version is the chat's version once the turn was claimed and published as running (chat.updated): a snapshot of the chat
	// at this version or later already shows the turn, running or over.
	Version int64 `json:"version"`
}

// Send stores the human's message and starts one agent turn in the background. It publishes the chat as running
// (chat.updated, then chat.turn_started) and returns the turn id; the turn's output arrives as chat.delta, chat.tool,
// chat.message and finally chat.turn and chat.updated events. A second send while a turn runs is refused.
func (s *Service) Send(ctx context.Context, r SendReq) (SendResult, error) {
	if s.d.Licensed != nil {
		if err := s.d.Licensed(ctx); err != nil {
			return SendResult{}, err
		}
	}
	text := strings.TrimSpace(strings.ToValidUTF8(r.Text, "�"))
	if text == "" {
		return SendResult{}, errors.New("the message is empty")
	}
	if len(text) > s.o.MaxTextBytes {
		return SendResult{}, fmt.Errorf("the message is %d bytes; the limit is %d", len(text), s.o.MaxTextBytes)
	}
	c, err := s.Get(ctx, r.ID)
	if err != nil {
		return SendResult{}, err
	}
	if c.Archived {
		return SendResult{}, errors.New("this chat is archived: unarchive it to continue")
	}
	caps, ok := s.caps(c.Adapter)
	if !ok {
		return SendResult{}, fmt.Errorf("the adapter %q of this chat is not available", c.Adapter)
	}
	attached, err := s.loadAttachments(ctx, c, r.Attach)
	if err != nil {
		return SendResult{}, err
	}
	files, err := s.checkFiles(r.Files, caps)
	if err != nil {
		return SendResult{}, err
	}
	for _, k := range r.keep { // a resend keeps the replaced message's pictures: the chat's agent must still take them
		if k.Kind == KindImage && !caps.ImageInput {
			return SendResult{}, fmt.Errorf("%s is a picture, and this chat's agent does not take pictures in Drydock", k.Name)
		}
	}
	if c.WSLDistro != "" {
		// the distro may be gone, or its CLI signed out, since the chat was created: say so before anything is claimed
		if s.d.CheckWSL == nil || s.d.WSLFolder == nil {
			return SendResult{}, fmt.Errorf("this chat runs in the WSL distro %s, and WSL is not available here", c.WSLDistro)
		}
		if _, err := s.d.CheckWSL(ctx, c.ProjectID, c.Adapter, c.WSLDistro); err != nil {
			return SendResult{}, err
		}
	}
	cwd := c.Cwd
	switch {
	case c.ProjectID != "":
		if cwd, err = s.d.ProjectDir(ctx, c.ProjectID); err != nil {
			return SendResult{}, err
		}
	case c.WSLDistro != "":
		if cwd, err = s.d.WSLFolder(ctx, c.WSLDistro, c.ID, c.Cwd); err != nil {
			return SendResult{}, fmt.Errorf("this chat's folder cannot be used any more: %w", err)
		}
	default:
		if cwd, err = ValidateFolder(c.Cwd); err != nil {
			return SendResult{}, fmt.Errorf("this chat's folder cannot be used any more: %w", err)
		}
	}

	// one turn at a time: the in-memory reservation, then the row (which also covers a second process on the same data)
	turnID := ids.New("cturn")
	tctx, cancel := context.WithCancelCause(s.bg)
	s.mu.Lock()
	if s.running[c.ID] != nil {
		s.mu.Unlock()
		cancel(nil)
		return SendResult{}, ErrBusy
	}
	s.running[c.ID] = &turnState{turnID: turnID, cancel: cancel}
	s.mu.Unlock()
	release := func() {
		s.unreserve(c.ID, turnID)
		cancel(nil)
	}
	attachedIDs := []string{}
	for _, a := range attached {
		attachedIDs = append(attachedIDs, a.chat.ID)
	}
	aj, _ := json.Marshal(attachedIDs)
	var claimed bool
	var dropped []string // files of the messages a resend removes
	err = s.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		dropped = nil
		res, err := tx.ExecContext(ctx, `UPDATE chats SET status=?, last_error='', cwd=?, updated_at=?, version=version+1 WHERE id=? AND status<>?`, StatusRunning, cwd, storage.Now(), c.ID, StatusRunning)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if claimed = n == 1; !claimed {
			return nil
		}
		if r.replaceFrom > 0 {
			// the message is still the person's one this resend names; it and everything after it go, and so does the session
			var role string
			if err := tx.QueryRowContext(ctx, `SELECT role FROM chat_messages WHERE id=? AND chat_id=? AND seq=?`, r.replaceID, c.ID, r.replaceFrom).Scan(&role); err != nil || role != RoleUser {
				return ErrNotYourMessage
			}
			keep := map[string]bool{}
			for _, k := range r.keep {
				keep[k.ID] = true
			}
			rows, err := tx.QueryContext(ctx, `SELECT a.id FROM chat_attachments a JOIN chat_messages m ON m.id=a.message_id WHERE m.chat_id=? AND m.seq>=?`, c.ID, r.replaceFrom)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil && !keep[id] {
					dropped = append(dropped, id)
				}
			}
			rows.Close()
			for _, id := range dropped {
				if _, err := tx.ExecContext(ctx, `DELETE FROM chat_attachments WHERE id=?`, id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM chat_messages WHERE chat_id=? AND seq>=?`, c.ID, r.replaceFrom); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET session_id='' WHERE id=?`, c.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_turns(id,chat_id,status,attached,started_at) VALUES(?,?,?,?,?)`, turnID, c.ID, TurnRunning, string(aj), storage.Now())
		return err
	})
	if err != nil || !claimed {
		release()
		if err == nil {
			err = ErrBusy
		}
		return SendResult{}, err
	}
	c.Cwd, c.Status = cwd, StatusRunning
	if r.replaceFrom > 0 {
		c.SessionID = ""
		s.removeAttachmentFiles(c.ID, dropped)
		s.publish(ctx, "chat.rewound", c.ProjectID, map[string]any{"chatId": c.ID, "fromSeq": r.replaceFrom})
	}
	if err := s.storeFiles(c.ID, files); err != nil {
		s.endTurnRow(c, turnID, TurnError, err.Error())
		release()
		return SendResult{}, fmt.Errorf("the attached files could not be kept: %w", err)
	}
	user, err := s.insertMessage(ctx, c, turnID, RoleUser, text, "", nil, msgExtra{files: files, keep: r.keep})
	if err != nil {
		s.dropFiles(c.ID, files)
		s.endTurnRow(c, turnID, TurnError, err.Error())
		release()
		return SendResult{}, err
	}
	res := SendResult{TurnID: turnID}
	// the running chat as a versioned snapshot, so a view orders it with every other snapshot of the chat
	if nc, err := s.Get(ctx, c.ID); err == nil {
		res.Version = nc.Version
		s.publish(ctx, "chat.updated", c.ProjectID, map[string]any{"chatId": c.ID, "chat": nc})
	}
	s.publish(ctx, "chat.turn_started", c.ProjectID, map[string]any{"chatId": c.ID, "turnId": turnID, "attach": attachedIDs})
	s.wg.Add(1)
	safe.Go("chats.Service.Send", func() {
		defer s.wg.Done()
		defer release()
		s.run(tctx, c, caps, turnID, user, attachmentBlock(attached, s.o.AttachChars))
	})
	return res, nil
}

// unreserve ends turnID's hold on its chat, and only its own: by the time a turn's deferred release runs, the next turn may
// already hold the chat.
func (s *Service) unreserve(chatID, turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.running[chatID]; t != nil && t.turnID == turnID {
		delete(s.running, chatID)
	}
}

// Stop cancels the running turn of a chat. It reports whether a turn was running.
func (s *Service) Stop(ctx context.Context, id string) (bool, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return false, err
	}
	s.mu.Lock()
	t := s.running[id]
	s.mu.Unlock()
	if t == nil {
		return false, nil
	}
	t.cancel(errStopped)
	return true, nil
}

// loadAttachments reads the chats the human attached, oldest messages first (renderTranscript keeps the newest).
func (s *Service) loadAttachments(ctx context.Context, c Chat, list []string) ([]attachedChat, error) {
	seen := map[string]bool{}
	var out []attachedChat
	for _, id := range list {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if id == c.ID {
			return nil, errors.New("a chat cannot be attached to itself: its own history is already part of the conversation")
		}
		if len(out) == s.o.MaxAttach {
			return nil, fmt.Errorf("at most %d chats can be attached to one message", s.o.MaxAttach)
		}
		ac, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		ms, err := s.recent(ctx, id, 1<<62, 400)
		if err != nil {
			return nil, err
		}
		out = append(out, attachedChat{chat: ac, messages: ms})
	}
	return out, nil
}

// recent returns up to limit messages of a chat before seq, oldest first.
func (s *Service) recent(ctx context.Context, chatID string, beforeSeq int64, limit int) ([]Message, error) {
	rows, err := s.d.DB.Reader().QueryContext(ctx, `SELECT `+msgCols+` FROM chat_messages
		WHERE chat_id=? AND seq<? ORDER BY seq DESC LIMIT ?`, chatID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// hasConversation reports whether messages hold anything a transcript would carry.
func hasConversation(ms []Message) bool {
	for _, m := range ms {
		if m.Role == RoleUser || m.Role == RoleAssistant {
			return true
		}
	}
	return false
}

// run executes the turn: continue the CLI session when there is one, fall back to a fresh session given the transcript when it
// cannot be resumed, store what the agent did, record usage and the session id, and publish chat.turn.
func (s *Service) run(ctx context.Context, c Chat, caps agents.Capabilities, turnID string, user Message, attachBlock string) {
	tctx, cancel := context.WithTimeoutCause(ctx, s.o.TurnTimeout, fmt.Errorf("the turn took longer than %s and was stopped", s.o.TurnTimeout))
	defer cancel()
	fileBlock, images, ferr := s.turnFiles(context.Background(), c.ID, user.Attachments)
	if ferr != nil {
		t := &turnRun{s: s, c: c, turnID: turnID, seen: map[string]bool{}}
		t.finish(tctx, attemptOut{err: ferr}, false, false)
		return
	}
	base := attachBlock + fileBlock + user.Body
	earlier, err := s.recent(context.Background(), c.ID, user.Seq, 400)
	if err != nil {
		s.logf("chats: read history: %v", err)
	}
	resume := c.SessionID != "" && caps.SessionResume
	if resume {
		// the CLI was stopped before it saved the answer the person saw: tell it what was shown, or it contradicts it
		base = stoppedNote(earlier) + base
	}
	prompt, transcript := base, false
	if !resume && hasConversation(earlier) {
		// a fork, an adapter that cannot resume, or a session already known to be gone: the transcript carries the context
		prompt, transcript = fallbackPrompt(earlier, s.o.TranscriptChars, base), true
	}
	resumeID := ""
	if resume {
		resumeID = c.SessionID
	}
	t := &turnRun{s: s, c: c, turnID: turnID, seen: map[string]bool{}, images: images}
	out := t.attempt(tctx, prompt, resumeID)
	resumed := resume
	if resume && out.resumeFailed() && ctx.Err() == nil {
		s.logf("chats: %s: session %s could not be resumed (%s); continuing in a new session", c.ID, resumeID, out.reason())
		if _, err := s.insertMessage(context.Background(), c, turnID, RoleSystem, ResumeFailedNote, "", nil); err != nil {
			s.logf("chats: note: %v", err)
		}
		resumed, transcript = false, true
		t.lostSession = true
		out = t.attempt(tctx, fallbackPrompt(earlier, s.o.TranscriptChars, base), "")
	}
	t.finish(tctx, out, resumed, transcript)
}

// turnRun is the state of one turn across its attempts.
type turnRun struct {
	s           *Service
	c           Chat
	turnID      string
	seen        map[string]bool // tool call ids already stored
	lostSession bool            // the stored session is gone (resume failed)
	assistant   bool            // an assistant message was stored in this turn
	images      []agents.Image  // pictures the person sent with the message (P90), given beside the prompt
	// thinking is the reasoning reported since the last stored tool call or answer: it is kept on the next one (P89)
	thinking strings.Builder
}

// takeThinking returns the reasoning waiting for a row and clears it.
func (t *turnRun) takeThinking() string {
	s := t.thinking.String()
	t.thinking.Reset()
	return s
}

// attemptOut is what one agent process did.
type attemptOut struct {
	runID          string
	sessionID      string
	sessionAtStart bool // the adapter knew the session id as soon as it started (ACP after session/new or session/load)
	sawSession     bool // the CLI announced its session (claude system/init)
	sawOutput      bool // text, a tool call or a denial arrived
	started        bool // the process started
	stop           string
	err            error
	errs           []workflow.ErrorChunk // the non-fatal error chunks, in order: coded warnings and the agent's own errors
	in, out        int64
	cost           float64
	costKnown      bool
	usageSeen      bool
	totals         json.RawMessage // the adapter's running totals of the session after the last result (UsageChunk.Totals)
	partial        string          // streamed text that no complete message has covered yet
}

// errors returns the messages of the agent's own error chunks: the uncoded ones. A coded warning (a setting the agent does not
// offer, adapter drift) is not a failure of anything.
func (o attemptOut) errors() []string {
	var msgs []string
	for _, e := range o.errs {
		if e.Code == "" {
			msgs = append(msgs, e.Message)
		}
	}
	return msgs
}

// resumeFailed reports whether the attempt failed the way a CLI fails when it cannot resume: before any session or output.
func (o attemptOut) resumeFailed() bool {
	if o.sessionAtStart || o.sawSession || o.sawOutput {
		return false
	}
	return o.err != nil || len(o.errors()) > 0 || (o.stop != "" && o.stop != "end_turn")
}

func (o attemptOut) reason() string {
	switch {
	case o.err != nil:
		return o.err.Error()
	case len(o.errors()) > 0:
		return strings.Join(o.errors(), "; ")
	}
	return o.stop
}

// outcome decides how the turn ended: its status, its error and whether that error is one of the agent's own error chunks
// (fromMsgs), which is then stored once, as the turn's error. cause is the turn context's cause; answered whether an assistant
// message was stored. Whether the agent's turn failed is workflow.TurnFailed, the rule the Activity reads too, so the chat and
// the Activity never tell one turn two ways (AG-UI review 2026-10-25); a cancelled or interrupted turn is read before it.
func (o attemptOut) outcome(cause error, answered bool) (status, errMsg string, fromMsgs bool) {
	status = TurnDone
	switch {
	case errors.Is(cause, errStopped):
		status = TurnStopped
	case errors.Is(cause, errShutdown):
		status, errMsg = TurnError, errShutdown.Error()
	case cause != nil:
		status, errMsg = TurnError, cause.Error()
	case o.err != nil:
		status, errMsg = TurnError, o.err.Error()
	case o.stop == "cancelled" || o.stop == "interrupted":
		status = TurnStopped
	default:
		if failed, reason := workflow.TurnFailed(o.stop, answered, o.errs); failed {
			// the reason is the agent's last error when it gave one, else what the stop reason says
			status, errMsg, fromMsgs = TurnError, reason, len(o.errors()) > 0
		}
	}
	return status, errMsg, fromMsgs
}

// attempt starts the CLI (resuming resumeID when set), sends the prompt and stores what comes back.
func (t *turnRun) attempt(ctx context.Context, prompt, resumeID string) attemptOut {
	s, c := t.s, t.c
	var o attemptOut
	spec := agents.RunSpec{AgentID: c.ID, Role: "chat", Cwd: c.Cwd, Model: c.Model, Effort: c.Effort, PermissionMode: c.PermissionMode,
		ResumeID: resumeID, AllowedTools: append([]string(nil), c.AllowedTools...), AskPermission: t.asker()}
	if c.WSLDistro != "" {
		// the adapter's own WSL start (decision P41), as for an agent: paths converted, WSLENV, the MCP shim, the marker sweep
		spec.RunMode, spec.WSLDistro = agents.RunModeWSL, c.WSLDistro
	}
	if resumeID != "" {
		spec.UsageBaseline = s.sessionUsage(ctx, c.ID, resumeID)
	}
	if s.d.Instructions != nil {
		// the chat's skills (decision P60): an index the CLI reads with every request, their text through the skills tool
		spec.SystemAppend = s.d.Instructions(ctx, c)
	}
	m, err := s.d.Runtime.Spawn(ctx, rt.Config{AgentID: c.ID, ProjectID: c.ProjectID, Adapter: c.Adapter, Spec: spec, Provider: c.Adapter, Tier: "premium"})
	if err != nil {
		o.err = err
		return o
	}
	defer func() { _ = m.Stop(context.Background()) }()
	o.started, o.runID = true, m.RunID
	o.sessionAtStart = m.Sess.ID() != ""
	s.setTurnRun(t.turnID, m.RunID)
	var pending strings.Builder
	var lastDelta time.Time
	calls := map[string]toolStart{} // tool calls of this attempt waiting for their result, by the CLI's call id
	var deltaBuf, thinkBuf strings.Builder
	var lastThink time.Time
	// stream publishes buffered text as event. Unless all is set it stops at the last whitespace and keeps the rest, so a token
	// that arrives in pieces is redacted whole (the scanner matches whole secrets, which contain no whitespace).
	stream := func(buf *strings.Builder, last *time.Time, event string, all bool) {
		text := buf.String()
		if !all {
			i := strings.LastIndexAny(text, " \t\r\n")
			if i < 0 {
				if len(text) < 4096 {
					return
				}
				i = len(text) - 1
			}
			text = text[:i+1]
		}
		if text == "" {
			return
		}
		rest := buf.String()[len(text):]
		buf.Reset()
		buf.WriteString(rest)
		*last = time.Now()
		s.publish(context.Background(), event, c.ProjectID, map[string]any{"chatId": c.ID, "turnId": t.turnID, "text": s.d.Redact(text)})
	}
	flush := func(all bool) { stream(&deltaBuf, &lastDelta, "chat.delta", all) }
	// the reasoning streams as chat.thinking (P89) and is kept on the next tool call or answer
	flushThink := func(all bool) { stream(&thinkBuf, &lastThink, "chat.thinking", all) }
	flushDelta := func() { flush(true) }
	res, err := m.RunTurnImages(ctx, prompt, t.images, nil, func(ch workflow.Chunk) {
		if ch.Kind != workflow.ChunkText {
			flushDelta()
		}
		if ch.Kind != workflow.ChunkThinking {
			flushThink(true)
		}
		switch ch.Kind {
		case workflow.ChunkThinking:
			var d workflow.TextDelta
			if ch.Decode(&d) == nil && d.Text != "" {
				t.thinking.WriteString(d.Text)
				thinkBuf.WriteString(d.Text)
				if time.Since(lastThink) >= s.o.DeltaEvery {
					flushThink(false)
				}
			}
		case workflow.ChunkText:
			var d workflow.TextDelta
			if ch.Decode(&d) == nil && d.Text != "" {
				o.sawOutput = true
				pending.WriteString(d.Text)
				deltaBuf.WriteString(d.Text)
				if time.Since(lastDelta) >= s.o.DeltaEvery {
					flush(false)
				}
			}
		case workflow.ChunkMessageEnd:
			var d workflow.TextDelta
			if ch.Decode(&d) == nil && strings.TrimSpace(d.Text) != "" {
				o.sawOutput = true
				pending.Reset()
				t.store(RoleAssistant, strings.TrimSpace(d.Text), "", nil)
				t.assistant = true
			}
		case workflow.ChunkToolCall:
			var tc workflow.ToolCall
			if ch.Decode(&tc) != nil || tc.Name == "" {
				return
			}
			o.sawOutput = true
			if tc.ID != "" {
				if t.seen[tc.ID] {
					return
				}
				t.seen[tc.ID] = true
			}
			summary := s.d.Redact(ToolSummary(tc.Name, tc.Args))
			msg, err := s.insertMessage(context.Background(), c, t.turnID, RoleTool, summary, tc.Name, tc.Args, msgExtra{callID: tc.ID, thinking: t.takeThinking()})
			if err != nil {
				s.logf("chats: store tool message: %v", err)
				return
			}
			if tc.ID != "" {
				calls[tc.ID] = toolStart{msgID: msg.ID, at: time.Now()}
			}
			s.publish(context.Background(), "chat.tool", c.ProjectID, map[string]any{"chatId": c.ID, "turnId": t.turnID, "name": msg.ToolName, "summary": summary, "messageId": msg.ID})
		case workflow.ChunkToolResult:
			// how the call ended, on the call's own row: the UI shows it as done or failed with what it returned
			var tr workflow.ToolResult
			if ch.Decode(&tr) != nil || tr.ID == "" {
				return
			}
			st, ok := calls[tr.ID]
			if !ok {
				return
			}
			delete(calls, tr.ID)
			status := ToolOK
			if tr.IsError {
				status = ToolFailed
			}
			s.setToolResult(c, t.turnID, st.msgID, status, tr.Output, time.Since(st.at).Milliseconds())
		case workflow.ChunkPermissionDenied:
			var d workflow.PermissionDenial
			if ch.Decode(&d) == nil && d.ToolName != "" {
				o.sawOutput = true
				t.store(RoleDenied, "Not allowed in this chat: "+ToolSummary(d.ToolName, d.Input), d.ToolName, d.Input)
			}
		case workflow.ChunkPermission:
			// an ACP agent asked; the turn's asker (permission.go) stores the request and waits for the person's answer
			o.sawOutput = true
		case workflow.ChunkSession:
			var sc workflow.SessionChunk
			if ch.Decode(&sc) == nil && sc.SessionID != "" {
				o.sawSession, o.sessionID = true, sc.SessionID
			}
		case workflow.ChunkUsage:
			var u workflow.UsageChunk
			if ch.Decode(&u) == nil {
				o.usageSeen = true
				if len(u.Totals) > 0 {
					o.totals = u.Totals
				}
				o.in += u.InputTokens
				o.out += u.OutputTokens
				if u.CostUSD > 0 {
					o.cost += u.CostUSD
					o.costKnown = true
				}
			}
		case workflow.ChunkError:
			var e workflow.ErrorChunk
			if ch.Decode(&e) == nil && !e.Fatal && strings.TrimSpace(e.Message) != "" {
				o.errs = append(o.errs, e)
			}
		}
	})
	flushDelta()
	flushThink(true)
	o.stop, o.err, o.partial = res.StopReason, err, pending.String()
	if id := m.Sess.ID(); id != "" {
		o.sessionID = id
	}
	if res.SessionID != "" {
		o.sessionID = res.SessionID
	}
	return o
}

// sessionUsage is the stored running usage totals of the chat's CLI session, when that session is the one being resumed.
func (s *Service) sessionUsage(ctx context.Context, chatID, sessionID string) json.RawMessage {
	var sid, usage string
	if err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT session_id, cli_usage FROM chats WHERE id=?`, chatID).Scan(&sid, &usage); err != nil || sid != sessionID || usage == "" {
		return nil
	}
	return json.RawMessage(usage)
}

// toolStart is a tool call stored in this turn, waiting for its result.
type toolStart struct {
	msgID string
	at    time.Time
}

// setToolResult records how a tool call ended on its message (redacted and bounded as its input is) and publishes
// chat.tool_result; storage failures are logged and do not end the turn.
func (s *Service) setToolResult(c Chat, turnID, msgID, status, output string, ms int64) {
	out := s.d.Redact(strings.ToValidUTF8(output, "\uFFFD"))
	if len(out) > s.o.MaxToolResult {
		out = cutUTF8(out, s.o.MaxToolResult) + "\n[cut by Drydock: the result was longer than it keeps]"
	}
	if ms < 0 {
		ms = 0
	}
	err := s.d.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE chat_messages SET tool_status=?, tool_result=?, tool_ms=? WHERE id=? AND chat_id=?`, status, out, ms, msgID, c.ID)
		return err
	})
	if err != nil {
		s.logf("chats: store tool result: %v", err)
		return
	}
	s.publish(context.Background(), "chat.tool_result", c.ProjectID, map[string]any{"chatId": c.ID, "turnId": turnID, "messageId": msgID, "status": status, "result": out, "ms": ms})
}

// store saves a message of this turn; storage failures are logged and do not end the turn.
func (t *turnRun) store(role, body, tool string, input json.RawMessage) (Message, bool) {
	var extra []msgExtra
	if role == RoleAssistant { // the answer keeps the reasoning that came before it
		extra = append(extra, msgExtra{thinking: t.takeThinking()})
	}
	m, err := t.s.insertMessage(context.Background(), t.c, t.turnID, role, body, tool, input, extra...)
	if err != nil {
		t.s.logf("chats: store %s message: %v", role, err)
		return Message{}, false
	}
	return m, true
}

func (s *Service) setTurnRun(turnID, runID string) {
	_ = s.d.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE chat_turns SET agent_run_id=? WHERE id=?`, runID, turnID)
		return err
	})
}

func (s *Service) endTurnRow(c Chat, turnID, status, errMsg string) {
	_ = s.d.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `UPDATE chat_turns SET status=?, error=?, ended_at=? WHERE id=?`, status, errMsg, storage.Now(), turnID); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `UPDATE chats SET status=?, last_error=?, version=version+1 WHERE id=?`, StatusError, errMsg, c.ID)
		return err
	})
}

// finish records the end of the turn.
func (t *turnRun) finish(ctx context.Context, o attemptOut, resumed, transcript bool) {
	s, c := t.s, t.c
	bg := context.Background()
	status, errMsg, fromMsgs := o.outcome(context.Cause(ctx), t.assistant) // the cause is nil while the context is live
	if p := strings.TrimSpace(o.partial); p != "" && status != TurnDone {
		t.store(RoleAssistant, p, "", nil)
		t.assistant = true
	}
	for _, e := range o.errs {
		if fromMsgs && e.Code == "" && strings.TrimSpace(e.Message) == errMsg {
			continue // the turn's own error, stored once below
		}
		// a warning of a turn that otherwise worked (a setting the agent does not offer, ...), or an error before the last one
		t.store(RoleError, clip(e.Message, 2000), "", nil)
	}
	switch status {
	case TurnStopped:
		t.store(RoleSystem, StoppedNote, "", nil)
	case TurnError:
		errMsg = clip(s.d.Redact(strings.TrimSpace(errMsg)), 2000)
		t.store(RoleError, errMsg, "", nil)
	}

	// the CLI session to continue next time: the one this turn ran in; none when the stored one is gone and no new one started
	session := c.SessionID
	switch {
	case o.sessionID != "":
		session = o.sessionID
	case t.lostSession:
		session = ""
	}
	var turnCost any
	unknown := 0
	if o.costKnown {
		turnCost = o.cost
	} else if o.usageSeen || o.sawOutput {
		unknown = 1
	}
	chatStatus := StatusIdle
	if status == TurnError {
		chatStatus = StatusError
	}
	title := ""
	if c.Title == "" && t.assistant {
		title = s.firstUserTitle(bg, c.ID)
	}
	now := storage.Now()
	err := s.d.DB.WriteTx(bg, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(bg, `UPDATE chat_turns SET status=?, session_id=?, resumed=?, fallback=?, tokens_in=?, tokens_out=?, cost_usd=?, error=?, ended_at=? WHERE id=?`,
			status, session, b2i(resumed), b2i(transcript), o.in, o.out, turnCost, errMsg, now, t.turnID); err != nil {
			return err
		}
		q := `UPDATE chats SET status=?, last_error=?, session_id=?, tokens_in=tokens_in+?, tokens_out=tokens_out+?, cost_unknown_turns=cost_unknown_turns+?, updated_at=?, version=version+1`
		args := []any{chatStatus, errMsg, session, o.in, o.out, unknown, now}
		// the session's running usage totals, for the turn that resumes it (Service.sessionUsage)
		switch {
		case session == "":
			q += `, cli_usage=''`
		case len(o.totals) > 0:
			q += `, cli_usage=?`
			args = append(args, string(o.totals))
		case session != c.SessionID:
			q += `, cli_usage=''` // a new session that reported no totals
		}
		if o.costKnown {
			q += `, cost_usd=COALESCE(cost_usd,0)+?`
			args = append(args, o.cost)
		}
		if title != "" {
			q += `, title=CASE WHEN title='' THEN ? ELSE title END`
			args = append(args, title)
		}
		_, err := tx.ExecContext(bg, q+` WHERE id=?`, append(args, c.ID)...)
		return err
	})
	if err != nil {
		s.logf("chats: record turn end: %v", err)
	}
	// the chat takes its next message from the moment chat.turn says this one ended (the UI and the API act on that event)
	s.unreserve(c.ID, t.turnID)
	usage := map[string]any{"inputTokens": o.in, "outputTokens": o.out, "costUsd": turnCost}
	ev := map[string]any{"chatId": c.ID, "turnId": t.turnID, "status": status, "usage": usage, "sessionId": session, "resumed": resumed, "fallback": transcript}
	if errMsg != "" {
		ev["error"] = errMsg
	}
	s.publish(bg, "chat.turn", c.ProjectID, ev)
	if nc, err := s.Get(bg, c.ID); err == nil {
		s.publish(bg, "chat.updated", c.ProjectID, map[string]any{"chatId": c.ID, "chat": nc})
	}
}

// firstUserTitle derives the automatic title from the chat's first user message (already redacted when it was stored).
func (s *Service) firstUserTitle(ctx context.Context, chatID string) string {
	var body string
	if err := s.d.DB.Reader().QueryRowContext(ctx, `SELECT body FROM chat_messages WHERE chat_id=? AND role='user' ORDER BY seq LIMIT 1`, chatID).Scan(&body); err != nil {
		return ""
	}
	return AutoTitle(body)
}

// StoppedNote is the system note a turn the person stopped ends with.
const StoppedNote = "Stopped by you."

// stoppedNote returns, when the previous turn was stopped by the person, a note quoting the answer they saw up to the stop, to
// put in front of the next message of a resumed session ("" otherwise). A stopped CLI is killed before it saves the partial
// answer to its session (observed with claude 2.1.296), so without the note the next answer can deny what the person read.
func stoppedNote(earlier []Message) string {
	n := len(earlier)
	if n == 0 || earlier[n-1].Role != RoleSystem || earlier[n-1].Body != StoppedNote {
		return ""
	}
	var parts []string
	for i := n - 2; i >= 0 && earlier[i].Role != RoleUser; i-- {
		if earlier[i].Role == RoleAssistant && strings.TrimSpace(earlier[i].Body) != "" {
			parts = append([]string{strings.TrimSpace(earlier[i].Body)}, parts...)
		}
	}
	shown := strings.Join(parts, "\n\n")
	if shown == "" {
		return "(Note from Drydock: I stopped your previous answer before you wrote anything.)\n\n"
	}
	if len(shown) > 4000 {
		shown = "..." + shown[len(shown)-4000:]
	}
	return "(Note from Drydock: I stopped your previous answer before it finished. This is what I saw of it; your session may not contain it:\n\n" +
		quoteLines(shown) + ")\n\n"
}

func quoteLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

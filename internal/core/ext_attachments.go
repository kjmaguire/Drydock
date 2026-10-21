package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Attachments in the room composer (brief 21: "a composer (mentions, slash commands, markers, attachments)"). A human attaches
// files to a message; each file is kept on disk under the data folder and listed in the message's payload. An agent sees a
// short text file's content (after the secret scan) as untrusted data in its room context, and only the name, size and type
// of anything else. Only the human posts attachments: there is no agent path to this method.

const (
	attachMaxFiles = 4
	attachMaxSize  = 10 << 20
	// attachTextMax is the most text of one file an agent is shown.
	attachTextMax = 32 << 10
)

func init() {
	RegisterExtension(Extension{Name: "attachments", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"room.attach": bind(func(ctx context.Context, p struct {
				RoomID     string       `json:"roomId"`
				Body       string       `json:"body"`
				ThreadRoot string       `json:"threadRoot"`
				Files      []attachFile `json:"files"`
			}) (any, error) {
				return c.AttachToRoom(ctx, p.RoomID, p.Body, p.ThreadRoot, p.Files)
			}),
			"room.attachment": bind(func(ctx context.Context, p idParam) (any, error) { return c.ReadAttachment(ctx, p.ID) }),
		}, nil, nil
	}})
}

type attachFile struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64
}

// AttachmentData is an attached file for the human to save.
type AttachmentData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	Data []byte `json:"data"`
}

// safeName keeps the last path element of a file name, without control characters, at most 120 bytes.
func safeName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimSpace(strings.Trim(n, "."))
	if n == "" {
		n = "file"
	}
	for len(n) > 120 {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	return n
}

// AttachToRoom stores the files and posts one human message that carries them.
func (c *Core) AttachToRoom(ctx context.Context, roomID, body, threadRoot string, files []attachFile) (room.Posted, error) {
	if len(files) == 0 {
		return room.Posted{}, errors.New("attach: no files")
	}
	if len(files) > attachMaxFiles {
		return room.Posted{}, fmt.Errorf("attach: at most %d files in one message", attachMaxFiles)
	}
	r, err := c.Room.GetRoom(ctx, roomID)
	if err != nil {
		return room.Posted{}, err
	}
	if r.Archived {
		return room.Posted{}, errors.New("attach: the room is archived")
	}
	type stored struct {
		att  room.Attachment
		hash string
		path string
	}
	var done []stored
	cleanup := func() {
		for _, s := range done {
			_ = os.RemoveAll(filepath.Dir(s.path))
		}
	}
	for _, f := range files {
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			cleanup()
			return room.Posted{}, fmt.Errorf("attach: %s is not valid base64", safeName(f.Name))
		}
		if len(data) > attachMaxSize {
			cleanup()
			return room.Posted{}, fmt.Errorf("attach: %s is over %d MB", safeName(f.Name), attachMaxSize>>20)
		}
		name := safeName(f.Name)
		id := ids.New("att")
		dir := filepath.Join(c.cfg.DataDir, "attachments", roomID, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			cleanup()
			return room.Posted{}, err
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			cleanup()
			return room.Posted{}, err
		}
		sum := sha256.Sum256(data)
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if mt == "" {
			mt = http.DetectContentType(data)
		}
		a := room.Attachment{ID: id, Name: name, Mime: mt, Size: int64(len(data))}
		if len(data) <= attachTextMax && utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
			a.Text = c.Scanner.Redact(string(data))
		}
		done = append(done, stored{att: a, hash: hex.EncodeToString(sum[:]), path: path})
	}
	atts := make([]room.Attachment, len(done))
	names := make([]string, len(done))
	for i, s := range done {
		atts[i], names[i] = s.att, s.att.Name
	}
	if strings.TrimSpace(body) == "" {
		body = "Attached " + strings.Join(names, ", ")
	}
	posted, err := c.Room.Post(ctx, room.PostParams{RoomID: roomID, Type: room.TypeChat, Author: room.Human(c.cfg.User), Body: body, ThreadRootID: threadRoot,
		Payload: map[string]any{"attachments": atts}})
	if err != nil {
		cleanup()
		return room.Posted{}, err
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, s := range done {
			if _, err := tx.ExecContext(ctx, `INSERT INTO room_attachments(id,room_id,message_id,name,mime,size,sha256,path,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				s.att.ID, roomID, posted.Message.ID, s.att.Name, s.att.Mime, s.att.Size, s.hash, s.path, storage.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	return posted, err
}

// ReadAttachment returns an attached file for the human to save. The path comes from the database row, and the file must
// still match the hash recorded when it was attached.
func (c *Core) ReadAttachment(ctx context.Context, id string) (AttachmentData, error) {
	var out AttachmentData
	var path, hash string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT id,name,mime,size,path,sha256 FROM room_attachments WHERE id=?`, id).Scan(&out.ID, &out.Name, &out.Mime, &out.Size, &path, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.New("attachment not found")
	}
	if err != nil {
		return out, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("the attached file is gone: %w", err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != hash {
		return out, errors.New("the attached file was changed on disk after it was attached")
	}
	out.Data = b
	return out, nil
}

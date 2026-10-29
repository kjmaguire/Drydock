package integrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/ids"
)

// Attachments of external items (decision P114). Nothing about them is read by sync: a person asks to list an item's attachments
// (the vendor's metadata is stored, cleaned like any outside string) and then to download one. A download is capped
// (Options.MaxAttachmentBytes), fetched through the connection's API client or, for a file the vendor keeps on another host,
// through internal/netguard without the credential, and stored under <app data>/integrations/<connection>/attachments, never in
// a repository. A text file is stored only as its sanitized, secret-redacted copy and that copy is the only text of it anyone
// is given (AttachmentText, for the person and for an agent's context); any other file is kept as it came and is never shown as
// text. Linear's attachments are mostly links to other sites and Notion's "external" files are links: those are listed, never
// fetched.

// RawAttachment is one attachment as an adapter read it.
type RawAttachment struct {
	ExternalID   string
	Name         string
	Mime         string
	Size         int64
	URL          string
	Downloadable bool
}

// attachmentSource is an adapter that can list and download an item's attachments.
type attachmentSource interface {
	Attachments(ctx context.Context, a *API, cfg map[string]string, it Item) ([]RawAttachment, error)
	FetchAttachment(ctx context.Context, a *API, cfg map[string]string, it Item, att RawAttachment, max int64) ([]byte, string, error)
}

// Attachment is a stored attachment.
type Attachment struct {
	ID            string `json:"id"`
	ItemID        string `json:"itemId"`
	IntegrationID string `json:"integrationId"`
	ExternalID    string `json:"externalId"`
	Name          string `json:"name"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	URL           string `json:"url,omitempty"`
	Downloadable  bool   `json:"downloadable"`
	LocalPath     string `json:"-"`
	SHA256        string `json:"sha256,omitempty"`
	TextKept      bool   `json:"textKept"`
	Redactions    int    `json:"redactions"`
	Risk          int    `json:"risk"`
	Listed        int64  `json:"listedAt"`
	Downloaded    int64  `json:"downloadedAt,omitempty"`
	DownloadedBy  string `json:"downloadedBy,omitempty"`
	Untrusted     bool   `json:"untrusted"` // always true
}

// EventAttachmentDownloaded records a person's download (ids, size and hash; never the content).
const EventAttachmentDownloaded = "integration.attachment.downloaded"

const attCols = `id,item_id,integration_id,external_id,name,mime,size,url,downloadable,local_path,sha256,text_kept,redactions,risk,listed_at,downloaded_at,downloaded_by`

func scanAttachment(r rowScanner) (Attachment, error) {
	var a Attachment
	var dl, tk int
	err := r.Scan(&a.ID, &a.ItemID, &a.IntegrationID, &a.ExternalID, &a.Name, &a.Mime, &a.Size, &a.URL, &dl, &a.LocalPath, &a.SHA256, &tk, &a.Redactions, &a.Risk, &a.Listed, &a.Downloaded, &a.DownloadedBy)
	a.Downloadable, a.TextKept, a.Untrusted = dl != 0, tk != 0, true
	return a, err
}

// ListAttachments returns an item's stored attachments.
func (s *Store) ListAttachments(ctx context.Context, itemID string) ([]Attachment, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+attCols+` FROM integration_attachments WHERE item_id=? ORDER BY name, id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAttachment returns one stored attachment.
func (s *Store) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	a, err := scanAttachment(s.DB.Reader().QueryRowContext(ctx, `SELECT `+attCols+` FROM integration_attachments WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: attachment %s", ErrNotFound, id)
	}
	return a, err
}

func (s *Store) upsertAttachment(ctx context.Context, a Attachment) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO integration_attachments(id,item_id,integration_id,external_id,name,mime,size,url,downloadable,listed_at) VALUES(?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(item_id,external_id) DO UPDATE SET name=excluded.name, mime=excluded.mime, size=excluded.size, url=excluded.url, downloadable=excluded.downloadable, listed_at=excluded.listed_at`,
			a.ID, a.ItemID, a.IntegrationID, a.ExternalID, a.Name, a.Mime, a.Size, a.URL, b2i(a.Downloadable), a.Listed)
		return err
	})
}

func (s *Store) markDownloaded(ctx context.Context, a Attachment) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE integration_attachments SET local_path=?, sha256=?, text_kept=?, redactions=?, risk=?, downloaded_at=?, downloaded_by=?, mime=CASE WHEN mime='' THEN ? ELSE mime END, size=? WHERE id=?`,
			a.LocalPath, a.SHA256, b2i(a.TextKept), a.Redactions, a.Risk, a.Downloaded, a.DownloadedBy, a.Mime, a.Size, a.ID)
		return err
	})
}

func (s *Service) attachmentSourceFor(ctx context.Context, itemID string) (Item, Connection, Assignment, attachmentSource, error) {
	it, err := s.st.GetItem(ctx, itemID)
	if err != nil {
		return it, Connection{}, Assignment{}, nil, err
	}
	conn, err := s.st.GetConnection(ctx, it.IntegrationID)
	if err != nil {
		return it, conn, Assignment{}, nil, err
	}
	ad, _ := AdapterFor(conn.Kind)
	src, ok := ad.(attachmentSource)
	if !ok {
		return it, conn, Assignment{}, nil, errf(ErrOpUnsupported, "Drydock does not read %s attachments", vendor(conn.Kind))
	}
	var asg Assignment
	if it.AssignmentID != "" {
		if asg, err = s.st.GetAssignment(ctx, it.AssignmentID); err != nil {
			return it, conn, asg, nil, err
		}
	}
	return it, conn, asg, src, nil
}

// Attachments returns what is stored about an item's attachments (no vendor call).
func (s *Service) Attachments(ctx context.Context, itemID string) ([]Attachment, error) {
	if _, err := s.st.GetItem(ctx, itemID); err != nil {
		return nil, err
	}
	return s.st.ListAttachments(ctx, itemID)
}

// RefreshAttachments reads an item's attachments from the vendor and stores their metadata. Only a person asks for it.
func (s *Service) RefreshAttachments(ctx context.Context, by, itemID string) ([]Attachment, error) {
	if err := requireHuman(by); err != nil {
		return nil, err
	}
	it, conn, asg, src, err := s.attachmentSourceFor(ctx, itemID)
	if err != nil {
		return nil, err
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return nil, s.scrubFailure(fail)
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	raws, err := src.Attachments(cctx, api, asg.Config, it)
	cancel()
	if err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = Classify(conn.Kind, err, "")
		}
		return nil, s.scrubFailure(f)
	}
	for i, r := range raws {
		if i >= 100 {
			break
		}
		a := Attachment{ID: ids.New("att"), ItemID: it.ID, IntegrationID: conn.ID, ExternalID: clampText(r.ExternalID, 200), Name: s.san.Line(ctx, r.Name, 160).Text,
			Mime: cleanMime(r.Mime), Size: r.Size, URL: validHTTPURL(r.URL), Downloadable: r.Downloadable, Listed: s.nowMS()}
		if a.Size < 0 {
			a.Size = 0
		}
		if a.ExternalID == "" {
			continue
		}
		if err := s.st.upsertAttachment(ctx, a); err != nil {
			return nil, err
		}
	}
	return s.st.ListAttachments(ctx, it.ID)
}

// cleanMime keeps a media type only when it parses (it is outside text too).
func cleanMime(v string) string {
	t, _, err := mime.ParseMediaType(strings.TrimSpace(v))
	if err != nil || len(t) > 100 {
		return ""
	}
	return t
}

// textual reports whether a file is text Drydock keeps as a redacted copy: a text media type (or a common text extension when
// the vendor gave none) whose bytes are UTF-8.
func textual(mt, name string, b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	switch {
	case strings.HasPrefix(mt, "text/"), mt == "application/json", mt == "application/xml", mt == "application/x-yaml", mt == "application/yaml", mt == "application/x-ndjson":
		return true
	case mt == "" || mt == "application/octet-stream":
		switch strings.ToLower(filepath.Ext(name)) {
		case ".txt", ".log", ".json", ".md", ".csv", ".yaml", ".yml", ".xml", ".ndjson", ".ini", ".cfg":
			return true
		}
	}
	return false
}

// DownloadAttachment downloads one attachment into the connection's attachment folder. Only a person asks for it. The size is
// capped; a text file is kept only as its redacted copy.
func (s *Service) DownloadAttachment(ctx context.Context, by, attachmentID string) (Attachment, error) {
	if err := requireHuman(by); err != nil {
		return Attachment{}, err
	}
	att, err := s.st.GetAttachment(ctx, attachmentID)
	if err != nil {
		return att, err
	}
	if !att.Downloadable {
		return att, errf(ErrOpUnsupported, "this attachment is a link to another site; open it there")
	}
	if att.Size > s.o.MaxAttachmentBytes {
		return att, fmt.Errorf("%w (%d MB at most; this one is %d MB)", ErrTooLarge, s.o.MaxAttachmentBytes>>20, (att.Size+(1<<20)-1)>>20)
	}
	if s.o.BodyDir == "" {
		return att, errors.New("integrations: no folder is configured for attachments")
	}
	it, conn, asg, src, err := s.attachmentSourceFor(ctx, att.ItemID)
	if err != nil {
		return att, err
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return att, s.scrubFailure(fail)
	}
	cctx, cancel := context.WithTimeout(ctx, 3*s.o.CallTimeout)
	b, ctype, err := src.FetchAttachment(cctx, api, asg.Config, it, RawAttachment{ExternalID: att.ExternalID, Name: att.Name, URL: att.URL, Downloadable: att.Downloadable}, s.o.MaxAttachmentBytes)
	cancel()
	if err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = Classify(conn.Kind, err, "")
		}
		return att, s.scrubFailure(f)
	}
	mt := att.Mime
	if mt == "" {
		mt = cleanMime(ctype)
	}
	out := att
	out.Mime, out.Size, out.Downloaded, out.DownloadedBy = mt, int64(len(b)), s.nowMS(), by
	dir := filepath.Join(s.o.BodyDir, conn.ID, "attachments", it.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return att, fmt.Errorf("integrations: could not create the attachment folder: %w", err)
	}
	data := b
	name := att.ID + ".bin"
	if textual(mt, att.Name, b) {
		// the scanner and the sanitizer see the text before anything is kept: only the redacted copy is written
		cl := s.san.Text(ctx, string(b), MaxBodyBytes)
		data, name = []byte(cl.Text), att.ID+".txt"
		out.TextKept, out.Redactions, out.Risk = true, cl.Redactions, cl.Risk
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return att, fmt.Errorf("integrations: could not store the attachment: %w", err)
	}
	sum := sha256.Sum256(data)
	out.SHA256 = hex.EncodeToString(sum[:])
	rel, _ := filepath.Rel(s.o.BodyDir, p)
	out.LocalPath = filepath.ToSlash(rel)
	if err := s.st.markDownloaded(ctx, out); err != nil {
		return att, err
	}
	s.emit(ctx, EventAttachmentDownloaded, it.ProjectID, it.TaskID, map[string]any{"integrationId": conn.ID, "itemId": it.ID, "attachmentId": att.ID, "bytes": len(data), "sha256": out.SHA256,
		"text": out.TextKept, "redactions": out.Redactions, "by": by})
	return s.st.GetAttachment(ctx, att.ID)
}

// AttachmentText is the text of a downloaded text attachment: the redacted, sanitized copy, still data, never instructions.
type AttachmentText struct {
	ID         string `json:"id"`
	ItemID     string `json:"itemId"`
	Name       string `json:"name"`
	Text       string `json:"text"`
	Redactions int    `json:"redactions"`
	Risk       int    `json:"risk"`
	Untrusted  bool   `json:"untrusted"`
}

// AttachmentText returns the stored text of a downloaded text attachment. A binary file has none.
func (s *Service) AttachmentText(ctx context.Context, attachmentID string) (AttachmentText, error) {
	a, err := s.st.GetAttachment(ctx, attachmentID)
	if err != nil {
		return AttachmentText{}, err
	}
	if a.Downloaded == 0 || !a.TextKept || a.LocalPath == "" {
		return AttachmentText{}, errf(ErrBadRequest, "this attachment has no text Drydock keeps (download a text file first)")
	}
	clean := filepath.Clean(filepath.FromSlash(a.LocalPath))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return AttachmentText{}, errf(ErrBadRequest, "the stored attachment path is not valid")
	}
	b, err := os.ReadFile(filepath.Join(s.o.BodyDir, clean))
	if err != nil {
		return AttachmentText{}, fmt.Errorf("integrations: the downloaded copy is gone: %w", err)
	}
	return AttachmentText{ID: a.ID, ItemID: a.ItemID, Name: a.Name, Text: string(b), Redactions: a.Redactions, Risk: a.Risk, Untrusted: true}, nil
}

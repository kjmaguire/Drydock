package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Notices a person acknowledges once (decision D1). The first: Drydock drives the person's own, signed-in Claude Code CLI (and
// the other agent CLIs), under the terms of the person's own agreement with the provider, and never reads, copies or sends
// their credentials. The window shows it until the person says they understand; the acknowledgement is kept with the version
// of the text it answered, so a changed text is shown again, and it is in the audit log.

// noticeClaudeTerms is the id of the D1 notice; noticeClaudeTermsVersion changes when its words change in substance.
const (
	noticeClaudeTerms        = "claude-terms"
	noticeClaudeTermsVersion = 1
)

// NoticeState is one notice and whether the person has acknowledged the current version of it.
type NoticeState struct {
	ID           string `json:"id"`
	Version      int    `json:"version"`
	Acknowledged bool   `json:"acknowledged"`
	At           int64  `json:"at,omitempty"`
}

type noticeAck struct {
	Version int    `json:"version"`
	At      int64  `json:"at"`
	By      string `json:"by"`
}

func init() {
	RegisterExtension(Extension{Name: "notices", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"notice.status": bind(func(ctx context.Context, _ struct{}) (any, error) {
				st, err := c.noticeState(ctx, noticeClaudeTerms, noticeClaudeTermsVersion)
				return map[string]any{"notices": []NoticeState{st}}, err
			}),
			"notice.acknowledge": bind(func(ctx context.Context, p struct {
				ID      string `json:"id"`
				Version int    `json:"version"`
			}) (any, error) {
				return c.AcknowledgeNotice(ctx, p.ID, p.Version, c.Human())
			}),
		}, nil, nil
	}})
}

func noticeKey(id string) string { return "notice." + id }

func (c *Core) noticeState(ctx context.Context, id string, version int) (NoticeState, error) {
	st := NoticeState{ID: id, Version: version}
	var raw string
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, noticeKey(id)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	var a noticeAck
	if json.Unmarshal([]byte(raw), &a) == nil && a.Version >= version {
		st.Acknowledged, st.At = true, a.At
	}
	return st, nil
}

// AcknowledgeNotice records that a person read the current version of a notice. Only a person may, and only the version the
// window showed: an acknowledgement of an older text does not count for a newer one.
func (c *Core) AcknowledgeNotice(ctx context.Context, id string, version int, by string) (NoticeState, error) {
	if err := humanOnly(by, "acknowledge a notice"); err != nil {
		return NoticeState{}, err
	}
	if id != noticeClaudeTerms {
		return NoticeState{}, fmt.Errorf("there is no notice %q", id)
	}
	if version != noticeClaudeTermsVersion {
		return NoticeState{}, fmt.Errorf("the notice was changed since it was shown (version %d, now %d): read it again", version, noticeClaudeTermsVersion)
	}
	now := time.Now().UnixMilli()
	b, _ := json.Marshal(noticeAck{Version: version, At: now, By: by})
	if err := c.setSetting(ctx, noticeKey(id), string(b)); err != nil {
		return NoticeState{}, err
	}
	if err := auditAppend(ctx, c, "notice.acknowledge", id, map[string]any{"version": version, "by": by}); err != nil {
		c.logf("audit notice.acknowledge: %v", err)
	}
	c.publish(ctx, "notice.acknowledged", "", map[string]any{"id": id, "version": version})
	return NoticeState{ID: id, Version: version, Acknowledged: true, At: now}, nil
}

package core

import (
	"context"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/room"
)

// Markdown minutes and ADR-candidate exports of a room (room-protocol section 8; the room package renders and stores them as
// immutable snapshots). The human asks for one from the room; each is kept with its hash so it can be verified later against
// the messages it was made from. Agents cannot request exports.

func init() {
	RegisterExtension(Extension{Name: "exports", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"room.export": bind(func(ctx context.Context, p struct {
				RoomID  string `json:"roomId"`
				Kind    string `json:"kind"` // minutes | adr_candidates | pr_description
				FromSeq int64  `json:"fromSeq"`
				ToSeq   int64  `json:"toSeq"`
				Title   string `json:"title"`
				Summary string `json:"summary"`
			}) (any, error) {
				r := room.ExportRequest{Scope: "human:" + c.cfg.User, ReceiptID: fmt.Sprintf("ui-%d", time.Now().UnixNano()), FromSeq: p.FromSeq, ToSeq: p.ToSeq,
					Title: p.Title, Summary: p.Summary, By: room.Human(c.cfg.User)}
				var ex room.Export
				var err error
				switch room.ExportKind(p.Kind) {
				case room.ExportMinutes:
					ex, _, err = c.Room.ExportMinutes(ctx, p.RoomID, r)
				case room.ExportADR:
					ex, _, err = c.Room.ExportADRCandidates(ctx, p.RoomID, r)
				case room.ExportPR:
					ex, _, err = c.Room.ExportPRDescription(ctx, p.RoomID, r)
				default:
					return nil, fmt.Errorf("unknown export %q: minutes, adr_candidates or pr_description", p.Kind)
				}
				return ex, err
			}),
			"room.exports": bind(func(ctx context.Context, p struct {
				RoomID string `json:"roomId"`
			}) (any, error) {
				l, err := c.Room.ListExports(ctx, p.RoomID)
				if l == nil {
					l = []room.Export{}
				}
				return l, err
			}),
			"room.exportGet": bind(func(ctx context.Context, p idParam) (any, error) { return c.Room.GetExport(ctx, p.ID) }),
			"room.exportVerify": bind(func(ctx context.Context, p idParam) (any, error) {
				err := c.Room.VerifyExport(ctx, p.ID)
				return map[string]any{"ok": err == nil, "error": errString(err)}, nil
			}),
			"room.adrCandidates": bind(func(ctx context.Context, p struct {
				RoomID string `json:"roomId"`
			}) (any, error) {
				l, err := c.Room.ADRCandidates(ctx, p.RoomID, 0, 0)
				if l == nil {
					l = []room.ADRCandidate{}
				}
				return l, err
			}),
		}, nil, nil
	}})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

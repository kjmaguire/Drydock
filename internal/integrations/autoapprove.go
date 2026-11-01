package integrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Auto-approve classes (decision P114, docs/integrations.md section 4). Every outbound write is still a proposal in the outbox
// bound to an approval of its exact call; a class only lets one narrow, templated kind of write of one connection in one project
// be approved without asking each time. The classes are a closed list in code, each covering named operations of named vendors;
// a destructive or signal-suppressing operation (an Aikido ignore or snooze, a Sentry ignore: OpInfo.NeverAuto) is in none of
// them. A class is off until a person turns it on, and only a person can; an agent's or a tool's proposal never names a class:
// only Drydock's own templated writes (the pull request link-back, status sync) do. The approval is decided in the name of the
// person who turned the class on, and the outbox row records the class.

// Auto-approve classes.
const (
	// ClassLinkback is the "Drydock opened a pull request" comment on the item a task came from (LinkBack): fixed text plus the
	// pull request's address.
	ClassLinkback = "linkback_comment"
	// ClassStatusSync is the status write status sync proposes from the person's own status mapping (Linear set_state, Notion
	// set_status, Sentry resolve or unresolve).
	ClassStatusSync = "status_sync"
)

// AutoClassInfo describes one class for the screen.
type AutoClassInfo struct {
	Class       string   `json:"class"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Kinds       []string `json:"kinds"`
}

// AutoClasses is the closed list.
var AutoClasses = []AutoClassInfo{
	{Class: ClassLinkback, Label: "Pull request link-back comment", Kinds: []string{KindLinear, KindNotion},
		Description: "The comment \"Drydock opened a pull request for this item: <link>\" on the item a task came from. The text is Drydock's fixed template and the pull request's address; nothing else can be sent under it."},
	{Class: ClassStatusSync, Label: "Status sync", Kinds: []string{KindLinear, KindNotion, KindSentry},
		Description: "Moving a linked item to the status your status mapping names when its task changes status (Linear workflow state, Notion status property, Sentry resolve or reopen). Never an ignore, a delete or a bulk change."},
}

// classOps are the operations each class may send, per vendor.
var classOps = map[string]map[string][]string{
	ClassLinkback:   {KindLinear: {"comment"}, KindNotion: {"comment"}},
	ClassStatusSync: {KindLinear: {"set_state"}, KindNotion: {"set_status"}, KindSentry: {"resolve", "unresolve"}},
}

// classAllows reports whether a class may send op to a vendor. An operation marked NeverAuto never qualifies, whatever the
// table says.
func classAllows(class, kind, op string) bool {
	if ad, ok := AdapterFor(kind); ok {
		for _, o := range ad.Ops() {
			if o.Op == op && o.NeverAuto {
				return false
			}
		}
	}
	return containsStr(classOps[class][kind], op)
}

// AutoApprove is one class's setting for a connection and project.
type AutoApprove struct {
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
	Class         string `json:"class"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Enabled       bool   `json:"enabled"`
	CreatedBy     string `json:"createdBy,omitempty"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
	Updated       int64  `json:"updated,omitempty"`
	Version       int64  `json:"version"` // 0: never set
}

func (s *Store) getAutoApprove(ctx context.Context, integrationID, projectID, class string) (AutoApprove, error) {
	a := AutoApprove{IntegrationID: integrationID, ProjectID: projectID, Class: class}
	var en int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT enabled,created_by,updated_by,updated_at,version FROM integration_auto_approve WHERE integration_id=? AND project_id=? AND class=?`,
		integrationID, projectID, class).Scan(&en, &a.CreatedBy, &a.UpdatedBy, &a.Updated, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	a.Enabled = en != 0
	return a, err
}

// AutoApprovals lists every class that applies to the connection's vendor, with its setting for the project.
func (s *Service) AutoApprovals(ctx context.Context, integrationID, projectID string) ([]AutoApprove, error) {
	conn, err := s.st.GetConnection(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	out := []AutoApprove{}
	for _, c := range AutoClasses {
		if !containsStr(c.Kinds, conn.Kind) {
			continue
		}
		a, err := s.st.getAutoApprove(ctx, integrationID, projectID, c.Class)
		if err != nil {
			return nil, err
		}
		a.Label, a.Description = c.Label, c.Description
		out = append(out, a)
	}
	return out, nil
}

// AutoApproveReq turns a class on or off for a connection in a project (compare-and-swap on Version; 0 for a first setting).
type AutoApproveReq struct {
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
	Class         string `json:"class"`
	Enabled       bool   `json:"enabled"`
	Version       int64  `json:"version"`
}

// SetAutoApprove changes one class's setting. Only a human can.
func (s *Service) SetAutoApprove(ctx context.Context, by string, r AutoApproveReq) (AutoApprove, error) {
	if err := requireHuman(by); err != nil {
		return AutoApprove{}, err
	}
	conn, err := s.st.GetConnection(ctx, r.IntegrationID)
	if err != nil {
		return AutoApprove{}, err
	}
	var info *AutoClassInfo
	for i := range AutoClasses {
		if AutoClasses[i].Class == r.Class {
			info = &AutoClasses[i]
		}
	}
	if info == nil || !containsStr(info.Kinds, conn.Kind) {
		return AutoApprove{}, errf(ErrBadRequest, "%q is not an auto-approve class of %s", r.Class, vendor(conn.Kind))
	}
	if _, err := s.st.AssignmentFor(ctx, conn.ID, r.ProjectID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return AutoApprove{}, fmt.Errorf("%w: assign %s to the project first", ErrNotAssigned, vendor(conn.Kind))
		}
		return AutoApprove{}, err
	}
	now := s.nowMS()
	var n int64
	err = s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if r.Version == 0 {
			res, err = tx.ExecContext(ctx, `INSERT INTO integration_auto_approve(integration_id,project_id,class,enabled,created_by,updated_by,created_at,updated_at,version)
				VALUES(?,?,?,?,?,?,?,?,1) ON CONFLICT DO NOTHING`, conn.ID, r.ProjectID, r.Class, b2i(r.Enabled), by, by, now, now)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE integration_auto_approve SET enabled=?, updated_by=?, updated_at=?, version=version+1 WHERE integration_id=? AND project_id=? AND class=? AND version=?`,
				b2i(r.Enabled), by, now, conn.ID, r.ProjectID, r.Class, r.Version)
		}
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return AutoApprove{}, err
	}
	if n == 0 {
		return AutoApprove{}, ErrConflict
	}
	s.emit(ctx, EventAutoApproveChanged, r.ProjectID, "", map[string]any{"integrationId": conn.ID, "class": r.Class, "enabled": r.Enabled, "by": by})
	a, err := s.st.getAutoApprove(ctx, conn.ID, r.ProjectID, r.Class)
	a.Label, a.Description = info.Label, info.Description
	return a, err
}

// EventAutoApproveChanged records a person turning a class on or off.
const EventAutoApproveChanged = "integration.auto_approve.changed"

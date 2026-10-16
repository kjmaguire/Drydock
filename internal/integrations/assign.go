package integrations

import (
	"context"
	"errors"
	"strings"
)

// AssignReq assigns a connection to a project, or changes an assignment (decision P42). Config is the project's remote scope;
// the keys are the kind's Settings (Catalog).
type AssignReq struct {
	IntegrationID string            `json:"integrationId"`
	ProjectID     string            `json:"projectId"`
	Config        map[string]string `json:"config"`
	Enabled       *bool             `json:"enabled"`
	// Version, when set, makes a change a compare-and-swap.
	Version int64 `json:"version"`
}

// Assign creates the assignment of a connection to a project, or updates it when the project is already assigned. Only a
// human can do it. The first assignment of a connection made before P42 adopts that connection's stored scope, items and
// cursor. A GitHub assignment without a repository gets the project's own (its origin remote).
func (s *Service) Assign(ctx context.Context, by string, r AssignReq) (AssignmentView, error) {
	if err := requireHuman(by); err != nil {
		return AssignmentView{}, err
	}
	conn, err := s.st.GetConnection(ctx, r.IntegrationID)
	if err != nil {
		return AssignmentView{}, err
	}
	if strings.TrimSpace(r.ProjectID) == "" {
		return AssignmentView{}, errf(ErrBadRequest, "choose the Drydock project to assign %s to", vendor(conn.Kind))
	}
	if s.o.ProjectExists != nil {
		if err := s.o.ProjectExists(ctx, r.ProjectID); err != nil {
			return AssignmentView{}, err
		}
	}
	cfg, err := validateConfig(conn.Kind, r.Config)
	if err != nil {
		return AssignmentView{}, err
	}
	existing, gerr := s.st.AssignmentFor(ctx, conn.ID, r.ProjectID)
	if gerr != nil && !errors.Is(gerr, ErrNotFound) {
		return AssignmentView{}, gerr
	}
	var out Assignment
	if gerr == nil {
		if r.Version != 0 && r.Version != existing.Version {
			return AssignmentView{}, ErrConflict
		}
		upd := existing
		upd.Config = cfg
		if r.Enabled != nil {
			upd.Enabled = *r.Enabled
		}
		if err := s.fillDefaults(ctx, conn, &upd); err != nil {
			return AssignmentView{}, err
		}
		if err := checkRequired(conn.Kind, upd.Config); err != nil {
			return AssignmentView{}, err
		}
		if out, err = s.st.UpdateAssignment(ctx, upd, existing.Version, !sameConfig(existing.Config, upd.Config)); err != nil {
			return AssignmentView{}, err
		}
	} else {
		asgs, err := s.st.ListAssignments(ctx, conn.ID)
		if err != nil {
			return AssignmentView{}, err
		}
		// only the first assignment ever adopts the pre-P42 scope, items and cursor: a connection that was assigned and then
		// unassigned has no assignment either, but its old scope was adopted (and maybe deliberately changed) back then
		adopt := len(asgs) == 0 && !conn.LegacyAdopted
		if adopt && len(cfg) == 0 && len(conn.Config) > 0 {
			if cfg, err = validateConfig(conn.Kind, conn.Config); err != nil {
				return AssignmentView{}, err
			}
		}
		a := Assignment{IntegrationID: conn.ID, ProjectID: r.ProjectID, Config: cfg, Enabled: true, CreatedBy: by}
		if r.Enabled != nil {
			a.Enabled = *r.Enabled
		}
		if err := s.fillDefaults(ctx, conn, &a); err != nil {
			return AssignmentView{}, err
		}
		if err := checkRequired(conn.Kind, a.Config); err != nil {
			return AssignmentView{}, err
		}
		if out, err = s.st.InsertAssignment(ctx, a, adopt); err != nil {
			return AssignmentView{}, err
		}
	}
	s.emit(ctx, EventAssigned, out.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": out.ID, "kind": conn.Kind, "by": by, "version": out.Version})
	return s.assignmentView(ctx, conn, out)
}

func (s *Service) fillDefaults(ctx context.Context, conn Connection, a *Assignment) error {
	if conn.Kind == KindGitHub && a.Config["repo"] == "" {
		if s.o.ProjectRepo == nil {
			return errf(ErrBadRequest, "name the GitHub repository (owner/name)")
		}
		repo, err := s.o.ProjectRepo(ctx, a.ProjectID)
		if err != nil || repo == "" {
			return errf(ErrBadRequest, "the project has no GitHub repository Drydock can see (its origin remote); name one (owner/name)")
		}
		if a.Config == nil {
			a.Config = map[string]string{}
		}
		a.Config["repo"] = repo
	}
	return nil
}

func sameConfig(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Unassign removes a connection from a project: its cursor, the rules of that project for this connection and the items that
// never became a task go; items that became tasks stay with their tasks. The connection stays connected.
func (s *Service) Unassign(ctx context.Context, by, assignmentID string) error {
	if err := requireHuman(by); err != nil {
		return err
	}
	a, err := s.st.GetAssignment(ctx, assignmentID)
	if err != nil {
		return err
	}
	if err := s.st.DeleteAssignment(ctx, a); err != nil {
		return err
	}
	s.emit(ctx, EventUnassigned, a.ProjectID, "", map[string]any{"integrationId": a.IntegrationID, "assignmentId": a.ID, "by": by})
	return nil
}

// Assignments lists the assignments of a connection ("" = all).
func (s *Service) Assignments(ctx context.Context, integrationID string) ([]Assignment, error) {
	return s.st.ListAssignments(ctx, integrationID)
}

func (s *Service) assignmentView(ctx context.Context, c Connection, a Assignment) (AssignmentView, error) {
	cur, err := s.st.GetCursor(ctx, c.ID, a.ID)
	if err != nil {
		return AssignmentView{}, err
	}
	n, err := s.st.CountAssignmentItems(ctx, a.ID)
	if err != nil {
		return AssignmentView{}, err
	}
	return AssignmentView{Assignment: a, Sync: s.syncStatus(c, cur, n)}, nil
}

// Choices lists the values of an assignment setting from the vendor's API (Linear teams, Sentry organizations and projects,
// Notion data sources, Aikido repositories, GitHub repositories).
func (s *Service) Choices(ctx context.Context, integrationID, key string, cfg map[string]string) ([]Choice, error) {
	conn, err := s.st.GetConnection(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	ad, ok := AdapterFor(conn.Kind)
	if !ok {
		return nil, errf(ErrBadRequest, "unknown integration %q", conn.Kind)
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return nil, fail
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	out, err := ad.Choices(cctx, api, key, cfg)
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			return nil, &Failure{Code: f.Code, Message: s.scrub(f.Message), Cause: f.Cause}
		}
		return nil, err
	}
	// labels come from the vendor: one line each, size-limited
	for i := range out {
		out[i].Label = s.san.Line(ctx, out[i].Label, 120).Text
		out[i].Value = clampText(out[i].Value, 200)
	}
	return out, nil
}

// Kinds describes the integrations for the screen.
func (s *Service) Kinds() []KindInfo {
	out := Catalog(s.OAuthAvailable)
	for i := range out {
		for j := range out[i].Auth {
			if out[i].Auth[j].ID != AuthOAuth {
				continue
			}
			if info, ok := s.OAuthAppInfo(out[i].Kind); ok {
				out[i].Auth[j].OAuthApp = &info
			}
		}
	}
	return out
}

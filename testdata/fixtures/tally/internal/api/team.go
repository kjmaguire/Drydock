package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"example.com/tally/internal/auth"
	"example.com/tally/internal/mail"
	"example.com/tally/internal/store"
)

func (s *Server) getMe(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	u, _ := s.store.User(p.UserID)
	o, _ := s.store.Org(p.OrgID)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "org": o, "role": p.Role, "api_key_id": p.KeyID})
}

func (s *Server) getOrg(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	o, ok := s.store.Org(p.OrgID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such organization")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) updateOrg(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !auth.CanManageTeam(p.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "only owners and admins change the organization")
		return
	}
	var in struct {
		Name      *string        `json:"name"`
		TimeZone  *string        `json:"time_zone"`
		TaxRateBP *int           `json:"tax_rate_bp"`
		Plan      *store.OrgPlan `json:"plan"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := s.store.UpdateOrg(p.OrgID, func(o *store.Org) error {
		if in.Name != nil {
			o.Name = *in.Name
		}
		if in.TimeZone != nil {
			o.TimeZone = *in.TimeZone
		}
		if in.TaxRateBP != nil {
			o.TaxRateBP = *in.TaxRateBP
		}
		if in.Plan != nil {
			o.Plan = *in.Plan
		}
		return nil
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// memberView is a member with the user's details.
type memberView struct {
	UserID   string     `json:"user_id"`
	Email    string     `json:"email"`
	Name     string     `json:"name"`
	Role     store.Role `json:"role"`
	JoinedAt string     `json:"joined_at"`
}

func (s *Server) memberView(m store.Member) memberView {
	u, _ := s.store.User(m.UserID)
	return memberView{UserID: m.UserID, Email: u.Email, Name: u.Name, Role: m.Role, JoinedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	items := []memberView{}
	for _, m := range s.store.Members(p.OrgID) {
		items = append(items, s.memberView(m))
	}
	writeJSON(w, http.StatusOK, list{Items: items})
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !auth.CanManageTeam(p.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "only owners and admins manage members")
		return
	}
	var in struct {
		Role store.Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := s.store.SetMemberRole(p.OrgID, r.PathValue("user_id"), in.Role)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.memberView(m))
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !auth.CanManageTeam(p.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "only owners and admins manage members")
		return
	}
	if err := s.store.RemoveMember(p.OrgID, r.PathValue("user_id")); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	writeJSON(w, http.StatusOK, list{Items: s.store.Invites(p.OrgID)})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !auth.CanManageTeam(p.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "only owners and admins invite members")
		return
	}
	var in struct {
		Email string     `json:"email"`
		Role  store.Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	inv, err := s.store.CreateInvite(p.OrgID, in.Email, in.Role, p.UserID, s.clock.Now())
	if err != nil {
		s.storeError(w, err)
		return
	}
	if err := s.sendInvite(r.Context(), inv); err != nil {
		s.log.Error("send invite", "invite", inv.ID, "err", err)
	}
	writeJSON(w, http.StatusCreated, inv)
}

// sendInvite emails the invite's link to the invited address.
func (s *Server) sendInvite(ctx context.Context, inv store.Invite) error {
	o, _ := s.store.Org(inv.OrgID)
	link := s.baseURL + "/invites/accept?token=" + url.QueryEscape(inv.Token)
	return s.mail.Send(ctx, mail.Message{
		To:      inv.Email,
		Subject: fmt.Sprintf("You are invited to join %s on Tally", o.Name),
		Body:    fmt.Sprintf("You have been invited to join %s on Tally as %s.\n\nAccept the invitation: %s\n", o.Name, inv.Role, link),
	})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !auth.CanManageTeam(p.Role) {
		writeError(w, http.StatusForbidden, "forbidden", "only owners and admins manage invites")
		return
	}
	if _, err := s.store.RevokeInvite(p.OrgID, r.PathValue("id"), s.clock.Now()); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// acceptInvite needs no API key: the invite's token is the credential. It answers the new member and their first API key.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, m, k, err := s.store.AcceptInvite(in.Token, in.Name, s.clock.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invite_not_found", "no such invite")
			return
		}
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": u, "member": s.memberView(m), "api_key": k})
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	writeJSON(w, http.StatusOK, list{Items: s.store.APIKeys(p.OrgID)})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	k, err := s.store.CreateAPIKey(p.OrgID, p.UserID, in.Name, s.clock.Now())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if _, err := s.store.RevokeAPIKey(p.OrgID, r.PathValue("id"), s.clock.Now()); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

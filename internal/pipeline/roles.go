package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// TeamMember is the part of an agent the template role resolution needs.
type TeamMember struct {
	ID   string
	Name string
	Role string
}

// Template roles beyond the task's own worker and reviewer.
const (
	RoleWork      = "work"
	RoleReview    = "review"
	RolePM        = "pm"
	RoleTests     = "tests"
	RoleArchitect = "architect"
	RoleSecurity  = "security"
)

// roleSynonyms maps the normalized text of an agent's role to the template role it can fill. Roles in Drydock are free
// text on the agent (brief section 1: PM, Architect, UX Designer, Frontend, Backend, Database, DevOps, QA, Security,
// Reviewer), so matching is by normalized name, never by provider.
var roleSynonyms = map[string][]string{
	RolePM:        {"pm", "product manager", "product owner", "product"},
	RoleTests:     {"qa", "tester", "test engineer", "quality", "quality assurance", "sdet", "test"},
	RoleArchitect: {"architect", "software architect", "architecture"},
	RoleSecurity:  {"security", "security engineer", "appsec", "secops"},
}

func normRole(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// RoleMatches reports whether an agent role text fills a template role.
func RoleMatches(agentRole, templateRole string) bool {
	n := normRole(agentRole)
	for _, syn := range roleSynonyms[templateRole] {
		if n == syn {
			return true
		}
	}
	return false
}

// RoleNeed describes how one role of a template is filled.
type RoleNeed struct {
	Role     string `json:"role"`
	Required bool   `json:"required"`
	Purpose  string `json:"purpose"`
}

// ResolvedRoles is the outcome of ResolveRoles.
type ResolvedRoles struct {
	Roles map[string]string `json:"roles"` // template role -> agent id (pm, tests, architect, security)
	Notes []string          `json:"notes,omitempty"`
}

// ResolveRoles picks the agent for each extra role of a template. overrides (from the human) always win; otherwise the
// first team member whose role matches; the tests role falls back to the task's worker (said in Notes) because a team
// without a QA agent can still do test-first work; the architect is optional; the PM and the security reviewer are
// required where the template needs them, and must not be the agent that implements the task (independence).
// "Templates set defaults, not locks": every choice can be overridden per run.
func ResolveRoles(needs []RoleNeed, worker, reviewer string, team []TeamMember, overrides map[string]string) (ResolvedRoles, error) {
	out := ResolvedRoles{Roles: map[string]string{}}
	byID := map[string]TeamMember{}
	for _, m := range team {
		byID[m.ID] = m
	}
	for role, id := range overrides {
		if role == RoleWork || role == RoleReview {
			continue // those belong to the task (task.assign)
		}
		if _, ok := byID[id]; !ok {
			return out, fmt.Errorf("the %s override %q is not on this project's team", role, id)
		}
	}
	var missing []string
	for _, need := range needs {
		role := need.Role
		if id := overrides[role]; id != "" {
			out.Roles[role] = id
			continue
		}
		var found string
		for _, m := range team {
			if !RoleMatches(m.Role, role) || (m.ID == worker && role != RoleTests) {
				continue // the agent that implements the task does not also write its spec, design or security review
			}
			found = m.ID
			break
		}
		switch {
		case found != "":
			out.Roles[role] = found
		case role == RoleTests && worker != "":
			out.Roles[role] = worker
			out.Notes = append(out.Notes, "no QA agent on the team: the worker writes the tests too (add a QA agent, or pass an override, for independent tests)")
		case need.Required:
			missing = append(missing, role)
		default:
			out.Notes = append(out.Notes, fmt.Sprintf("no %s agent on the team: that step is skipped", role))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return out, fmt.Errorf("this template needs an agent with the role %s on the team (or an explicit override); add one in the team settings", strings.Join(missing, ", "))
	}
	if sec := out.Roles[RoleSecurity]; sec != "" && sec == worker {
		return out, fmt.Errorf("the security reviewer must be a different agent from the one that does the work")
	}
	if reviewer != "" && worker != "" && reviewer == worker {
		return out, fmt.Errorf("the reviewer must be a different agent from the worker")
	}
	return out, nil
}

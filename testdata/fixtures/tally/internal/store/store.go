// Package store keeps Tally's data in memory. It is safe for concurrent use.
//
// Every object belongs to one organization. Methods that take an orgID only ever see that organization's objects; the few
// methods that find an object by ID alone (CustomerByID, InvoiceByID, EventByID) are for background jobs and the admin
// tool, which act on behalf of the system rather than a member.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors. Callers check them with errors.Is; most are wrapped with a detail.
var (
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid")
	ErrConflict      = errors.New("conflict")
	ErrEmailTaken    = fmt.Errorf("%w: email address already in use", ErrConflict)
	ErrAlreadyMember = fmt.Errorf("%w: already a member", ErrConflict)
	// ErrCustomerOrgMismatch is returned when an object names a customer of another organization.
	ErrCustomerOrgMismatch = errors.New("customer belongs to another organization")
)

// Store holds everything.
type Store struct {
	mu  sync.Mutex
	seq int

	orgs         map[string]Org
	users        map[string]User
	usersByEmail map[string]string // lower-case email -> user ID
	members      map[memberKey]Member
	apiKeys      map[string]APIKey
	keysByToken  map[string]string // token -> key ID
	invites      map[string]Invite

	customers        map[string]Customer
	customersByEmail map[string]string // lower-case email -> customer ID

	plans     map[string]Plan
	subs      map[string]Subscription
	usage     []UsageRecord
	invoices  map[string]Invoice
	payments  map[string]Payment
	endpoints map[string]WebhookEndpoint
	events    map[string]Event
	delivs    map[string]Delivery
}

type memberKey struct{ org, user string }

// New returns an empty store.
func New() *Store {
	return &Store{
		orgs: map[string]Org{}, users: map[string]User{}, usersByEmail: map[string]string{}, members: map[memberKey]Member{},
		apiKeys: map[string]APIKey{}, keysByToken: map[string]string{}, invites: map[string]Invite{},
		customers: map[string]Customer{}, customersByEmail: map[string]string{},
		plans: map[string]Plan{}, subs: map[string]Subscription{}, invoices: map[string]Invoice{}, payments: map[string]Payment{},
		endpoints: map[string]WebhookEndpoint{}, events: map[string]Event{}, delivs: map[string]Delivery{},
	}
}

// newID returns the next ID with the prefix, for example "cus_000042". The caller holds the lock.
func (s *Store) newID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%06d", prefix, s.seq)
}

// NewToken returns prefix followed by 32 random hex characters.
func NewToken(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return prefix + hex.EncodeToString(b)
}

func normEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(email string) bool {
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1 && !strings.ContainsAny(email, " \t\r\n,;<>")
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// ---- organizations

// CreateOrg adds an organization.
func (s *Store) CreateOrg(name string, plan OrgPlan, timeZone string, taxRateBP int, now time.Time) (Org, error) {
	o := Org{Name: strings.TrimSpace(name), Plan: plan, TimeZone: timeZone, TaxRateBP: taxRateBP, CreatedAt: now.UTC()}
	if err := checkOrg(o); err != nil {
		return Org{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o.ID = s.newID("org")
	s.orgs[o.ID] = o
	return o, nil
}

func checkOrg(o Org) error {
	if o.Name == "" {
		return invalid("an organization needs a name")
	}
	if !o.Plan.Valid() {
		return invalid("unknown plan %q", o.Plan)
	}
	if _, err := time.LoadLocation(o.TimeZone); err != nil || o.TimeZone == "" {
		return invalid("unknown time zone %q", o.TimeZone)
	}
	if o.TaxRateBP < 0 || o.TaxRateBP > 10000 {
		return invalid("the tax rate is 0 to 10000 basis points")
	}
	return nil
}

// Org returns an organization.
func (s *Store) Org(id string) (Org, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[id]
	return o, ok
}

// UpdateOrg changes an organization with f, which may refuse with an error.
func (s *Store) UpdateOrg(id string, f func(*Org) error) (Org, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[id]
	if !ok {
		return Org{}, ErrNotFound
	}
	if err := f(&o); err != nil {
		return Org{}, err
	}
	o.ID = id
	if err := checkOrg(o); err != nil {
		return Org{}, err
	}
	s.orgs[id] = o
	return o, nil
}

// ---- users and members

// CreateUser adds a user. Email addresses are unique, ignoring case.
func (s *Store) CreateUser(email, name string, now time.Time) (User, error) {
	email, name = strings.TrimSpace(email), strings.TrimSpace(name)
	if !validEmail(email) {
		return User{}, invalid("a user needs a valid email address")
	}
	if name == "" {
		return User{}, invalid("a user needs a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByEmail[normEmail(email)]; ok {
		return User{}, ErrEmailTaken
	}
	u := User{ID: s.newID("usr"), Email: email, Name: name, CreatedAt: now.UTC()}
	s.users[u.ID] = u
	s.usersByEmail[normEmail(email)] = u.ID
	return u, nil
}

// User returns a user.
func (s *Store) User(id string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	return u, ok
}

// UserByEmail finds a user by email address, ignoring case.
func (s *Store) UserByEmail(email string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.usersByEmail[normEmail(email)]
	if !ok {
		return User{}, false
	}
	return s.users[id], true
}

// AddMember makes a user a member of an organization.
func (s *Store) AddMember(orgID, userID string, role Role, now time.Time) (Member, error) {
	if !role.Valid() {
		return Member{}, invalid("unknown role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addMember(orgID, userID, role, now)
}

func (s *Store) addMember(orgID, userID string, role Role, now time.Time) (Member, error) {
	if _, ok := s.orgs[orgID]; !ok {
		return Member{}, ErrNotFound
	}
	if _, ok := s.users[userID]; !ok {
		return Member{}, ErrNotFound
	}
	k := memberKey{orgID, userID}
	if _, ok := s.members[k]; ok {
		return Member{}, ErrAlreadyMember
	}
	m := Member{OrgID: orgID, UserID: userID, Role: role, CreatedAt: now.UTC()}
	s.members[k] = m
	return m, nil
}

// Member returns a user's membership of an organization.
func (s *Store) Member(orgID, userID string) (Member, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.members[memberKey{orgID, userID}]
	return m, ok
}

// Members returns an organization's members, oldest first.
func (s *Store) Members(orgID string) []Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Member{}
	for _, m := range s.members {
		if m.OrgID == orgID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].UserID < out[j].UserID
	})
	return out
}

// SetMemberRole changes a member's role.
func (s *Store) SetMemberRole(orgID, userID string, role Role) (Member, error) {
	if !role.Valid() {
		return Member{}, invalid("unknown role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{orgID, userID}
	m, ok := s.members[k]
	if !ok {
		return Member{}, ErrNotFound
	}
	m.Role = role
	s.members[k] = m
	return m, nil
}

// RemoveMember removes a user from an organization. Their API keys for it stop working, as they are no longer a member.
func (s *Store) RemoveMember(orgID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{orgID, userID}
	if _, ok := s.members[k]; !ok {
		return ErrNotFound
	}
	delete(s.members, k)
	return nil
}

// ---- API keys

// CreateAPIKey gives a member a new API key.
func (s *Store) CreateAPIKey(orgID, userID, name string, now time.Time) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createAPIKey(orgID, userID, name, now)
}

func (s *Store) createAPIKey(orgID, userID, name string, now time.Time) (APIKey, error) {
	if _, ok := s.members[memberKey{orgID, userID}]; !ok {
		return APIKey{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "API key"
	}
	k := APIKey{ID: s.newID("key"), OrgID: orgID, UserID: userID, Name: name, Token: NewToken("tly_"), CreatedAt: now.UTC()}
	s.apiKeys[k.ID] = k
	s.keysByToken[k.Token] = k.ID
	return k, nil
}

// APIKeyByToken finds the key a request presented. Revoked keys are returned too; the caller checks RevokedAt.
func (s *Store) APIKeyByToken(token string) (APIKey, bool) {
	if token == "" {
		return APIKey{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.keysByToken[token]
	if !ok {
		return APIKey{}, false
	}
	return s.apiKeys[id], true
}

// APIKey returns one of an organization's keys.
func (s *Store) APIKey(orgID, id string) (APIKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.apiKeys[id]
	if !ok || k.OrgID != orgID {
		return APIKey{}, false
	}
	return k, true
}

// APIKeys returns an organization's keys, oldest first.
func (s *Store) APIKeys(orgID string) []APIKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []APIKey{}
	for _, k := range s.apiKeys {
		if k.OrgID == orgID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RevokeAPIKey stops a key from working.
func (s *Store) RevokeAPIKey(orgID, id string, now time.Time) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.apiKeys[id]
	if !ok || k.OrgID != orgID {
		return APIKey{}, ErrNotFound
	}
	if k.RevokedAt == nil {
		t := now.UTC()
		k.RevokedAt = &t
		s.apiKeys[id] = k
	}
	return k, nil
}

// TouchAPIKey records that a key was used.
func (s *Store) TouchAPIKey(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.apiKeys[id]; ok {
		t := now.UTC()
		k.LastUsedAt = &t
		s.apiKeys[id] = k
	}
}

// ---- invites

// CreateInvite invites an email address to join an organization with a role.
func (s *Store) CreateInvite(orgID, email string, role Role, invitedBy string, now time.Time) (Invite, error) {
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return Invite{}, invalid("an invite needs a valid email address")
	}
	if !role.Valid() || role == RoleOwner {
		return Invite{}, invalid("an invite's role is admin, billing, member or viewer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[orgID]; !ok {
		return Invite{}, ErrNotFound
	}
	if uid, ok := s.usersByEmail[normEmail(email)]; ok {
		if _, member := s.members[memberKey{orgID, uid}]; member {
			return Invite{}, ErrAlreadyMember
		}
	}
	inv := Invite{ID: s.newID("inv"), OrgID: orgID, Email: email, Role: role, Token: NewToken("invite_"), InvitedBy: invitedBy, CreatedAt: now.UTC()}
	s.invites[inv.ID] = inv
	return inv, nil
}

// Invite returns one of an organization's invites.
func (s *Store) Invite(orgID, id string) (Invite, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[id]
	if !ok || inv.OrgID != orgID {
		return Invite{}, false
	}
	return inv, true
}

// Invites returns an organization's invites that were neither accepted nor revoked, oldest first.
func (s *Store) Invites(orgID string) []Invite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Invite{}
	for _, inv := range s.invites {
		if inv.OrgID == orgID && inv.AcceptedAt == nil && inv.RevokedAt == nil {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RevokeInvite withdraws an invite.
func (s *Store) RevokeInvite(orgID, id string, now time.Time) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[id]
	if !ok || inv.OrgID != orgID {
		return Invite{}, ErrNotFound
	}
	if inv.RevokedAt == nil {
		t := now.UTC()
		inv.RevokedAt = &t
		s.invites[id] = inv
	}
	return inv, nil
}

// AcceptInvite accepts the invite with this token: it finds or creates the user for the invited address (named name),
// makes them a member with the invite's role and gives them a first API key.
func (s *Store) AcceptInvite(token, name string, now time.Time) (User, Member, APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var inv Invite
	found := false
	for _, i := range s.invites {
		if i.Token == token && token != "" {
			inv, found = i, true
			break
		}
	}
	if !found || inv.RevokedAt != nil {
		return User{}, Member{}, APIKey{}, ErrNotFound
	}
	var u User
	if id, ok := s.usersByEmail[normEmail(inv.Email)]; ok {
		u = s.users[id]
	} else {
		name = strings.TrimSpace(name)
		if name == "" {
			return User{}, Member{}, APIKey{}, invalid("a new user needs a name")
		}
		u = User{ID: s.newID("usr"), Email: inv.Email, Name: name, CreatedAt: now.UTC()}
		s.users[u.ID] = u
		s.usersByEmail[normEmail(u.Email)] = u.ID
	}
	m, err := s.addMember(inv.OrgID, u.ID, inv.Role, now)
	if err != nil {
		return User{}, Member{}, APIKey{}, err
	}
	k, err := s.createAPIKey(inv.OrgID, u.ID, "Default", now)
	if err != nil {
		return User{}, Member{}, APIKey{}, err
	}
	t := now.UTC()
	inv.AcceptedAt = &t
	s.invites[inv.ID] = inv
	return u, m, k, nil
}

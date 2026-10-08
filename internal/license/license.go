// Package license verifies Drydock's offline Ed25519 license tokens and derives the license state (docs/packaging.md
// section 10). It has no network access. The scheme is Drydock's own and has not been externally reviewed; it protects
// against casual sharing and gives honest expiry behaviour, not against a determined user patching the binary.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const domain = "drydock-license-v1\n"

// State is where a license stands.
type State string

const (
	StateNone    State = "none"
	StateTrial   State = "trial"
	StateActive  State = "active"
	StateGrace   State = "grace"
	StateExpired State = "expired" // read-only: nothing is ever deleted
)

// Payload is the signed content.
type Payload struct {
	V         int      `json:"v"`
	KID       string   `json:"kid"`
	JTI       string   `json:"jti"`
	Kind      string   `json:"kind"` // trial | paid
	Sub       string   `json:"sub"`
	Name      string   `json:"name,omitempty"`
	Plan      string   `json:"plan,omitempty"`
	Seats     int      `json:"seats,omitempty"`
	Features  []string `json:"features,omitempty"`
	Channels  []string `json:"channels,omitempty"`
	IAT       int64    `json:"iat"`
	NBF       int64    `json:"nbf"`
	EXP       int64    `json:"exp"`
	GraceDays int      `json:"grace_days,omitempty"`
	MID       string   `json:"mid,omitempty"`
	MinApp    string   `json:"min_app,omitempty"`
	MaxApp    string   `json:"max_app,omitempty"`
}

// Errors.
var (
	ErrFormat    = errors.New("license: malformed token")
	ErrSignature = errors.New("license: signature does not verify with any known key")
	ErrNotYet    = errors.New("license: token is not valid yet (nbf is in the future)")
)

// Keys maps key ids to public keys. The app embeds this list; rotation adds a new kid.
type Keys map[string]ed25519.PublicKey

// Parse verifies the token's signature and returns its payload. It does not judge validity in time: see Evaluate.
func Parse(token string, keys Keys) (Payload, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Payload{}, ErrFormat
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Payload{}, fmt.Errorf("%w: payload: %v", ErrFormat, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Payload{}, fmt.Errorf("%w: signature", ErrFormat)
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return Payload{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if p.V != 1 {
		return Payload{}, fmt.Errorf("%w: unsupported version %d", ErrFormat, p.V)
	}
	pub, ok := keys[p.KID]
	if !ok || !ed25519.Verify(pub, append([]byte(domain), body...), sig) {
		return Payload{}, ErrSignature
	}
	return p, nil
}

// Sign creates a token (used by the vendor tool and by tests; the private key never ships).
func Sign(priv ed25519.PrivateKey, p Payload) string {
	p.V = 1
	body, _ := json.Marshal(p)
	sig := ed25519.Sign(priv, append([]byte(domain), body...))
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Status is the evaluated license.
type Status struct {
	State     State    `json:"state"`
	Payload   *Payload `json:"payload,omitempty"`
	ExpiresAt int64    `json:"expiresAt,omitempty"`
	DaysLeft  int      `json:"daysLeft"`
	Message   string   `json:"message"`
}

// ReadOnly reports whether the app must refuse to start agents, tasks and merges.
func (s Status) ReadOnly() bool { return s.State == StateExpired || s.State == StateNone }

// Evaluate derives the state at time now. maxSeen is the highest timestamp the app has ever observed: a system clock set
// back beyond a day does not extend validity (tamper-evident, not tamper-proof). A token with nbf in the future is an
// error, not a state.
func Evaluate(p *Payload, now, maxSeen time.Time) (Status, error) {
	if p == nil {
		return Status{State: StateNone, Message: "No license. Drydock is read-only until a license or trial is added."}, nil
	}
	eff := now
	if maxSeen.After(now.Add(24 * time.Hour)) {
		eff = maxSeen
	}
	if p.NBF > eff.Unix() {
		return Status{}, ErrNotYet
	}
	st := Status{Payload: p, ExpiresAt: p.EXP}
	switch {
	case eff.Unix() <= p.EXP:
		st.State = StateActive
		if p.Kind == "trial" {
			st.State = StateTrial
		}
		st.DaysLeft = int(time.Unix(p.EXP, 0).Sub(eff).Hours() / 24)
		st.Message = fmt.Sprintf("%s license valid for about %d more day(s).", p.Kind, st.DaysLeft)
	case eff.Unix() <= p.EXP+int64(p.GraceDays)*86400:
		st.State = StateGrace
		st.DaysLeft = int(time.Unix(p.EXP, 0).AddDate(0, 0, p.GraceDays).Sub(eff).Hours() / 24)
		st.Message = fmt.Sprintf("License expired; full functionality for %d more grace day(s). Renew to keep working.", st.DaysLeft)
	default:
		st.State = StateExpired
		st.Message = "License expired. Drydock is read-only: you can read, export and back up everything; nothing has been deleted."
	}
	return st, nil
}

// Allows reports whether an action is permitted in this state. Expired and unlicensed installs can read, export, back up,
// restore, import a license and uninstall, and nothing else.
func (s Status) Allows(action string) bool {
	switch action {
	case "read", "export", "backup", "restore", "support_bundle", "import_license":
		return true
	}
	return !s.ReadOnly()
}

// ChannelAllowed reports whether the license entitles the user to an update channel (empty list = stable only).
func (p Payload) ChannelAllowed(ch string) bool {
	if len(p.Channels) == 0 {
		return ch == "stable"
	}
	for _, c := range p.Channels {
		if c == ch {
			return true
		}
	}
	return false
}

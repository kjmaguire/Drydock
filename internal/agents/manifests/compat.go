package manifests

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Class says how a version was verified, weakest first (docs/connections.md section 8). The UI never says "verified" for
// something that was only inspected.
type Class string

const (
	ClassDocsOnly      Class = "docs_only"      // read about, never run
	ClassInspected     Class = "inspected"      // run on Linux without a login: flags, version, ACP initialize
	ClassVerifiedLinux Class = "verified_linux" // a real turn on Linux with the user's own login: conformance suite subset
	ClassVerifiedShort Class = "verified_short" // Windows "Test connection" passed
	ClassVerifiedFull  Class = "verified_full"  // Windows full conformance suite passed
)

var classRank = map[Class]int{ClassDocsOnly: 0, ClassInspected: 1, ClassVerifiedLinux: 2, ClassVerifiedShort: 3, ClassVerifiedFull: 4}

// AtLeast reports c >= o.
func (c Class) AtLeast(o Class) bool { return classRank[c] >= classRank[o] }

// Compat is compat.yaml: what was actually run, with the evidence class.
type Compat struct {
	Schema   int                      `yaml:"schema"`
	Node     NodeCompat               `yaml:"node"`
	Adapters map[string]CompatAdapter `yaml:"adapters"`
	Blocked  []BlockedVersion         `yaml:"blocked"`
}

// NodeCompat is the Node prerequisite (decision D9).
type NodeCompat struct {
	Min string `yaml:"min"`
}

// CompatAdapter lists the verified versions of one adapter.
type CompatAdapter struct {
	Min         string        `yaml:"min"`
	Recommended string        `yaml:"recommended"`
	Entries     []CompatEntry `yaml:"entries"`
}

// CompatEntry is one version that was run.
type CompatEntry struct {
	Version            string `yaml:"version"`
	Class              Class  `yaml:"class"`
	Date               string `yaml:"date"`
	Platform           string `yaml:"platform"`
	Method             string `yaml:"method"`
	Transport          string `yaml:"transport"`
	Bridge             string `yaml:"bridge"`
	WindowsConformance string `yaml:"windows_conformance"`
	Note               string `yaml:"note"`
}

// BlockedVersion is a version known to be bad.
type BlockedVersion struct {
	Adapter string `yaml:"adapter"`
	Version string `yaml:"version"`
	Reason  string `yaml:"reason"`
}

// LoadCompat parses and validates compat.yaml content.
func LoadCompat(data []byte) (*Compat, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Compat
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("compat.yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// EmbeddedCompat loads the compat.yaml shipped with Drydock.
func EmbeddedCompat() (*Compat, error) {
	b, err := fs.ReadFile(embedded, "compat.yaml")
	if err != nil {
		return nil, err
	}
	return LoadCompat(b)
}

// Validate checks structure: known classes, parseable versions, no duplicates, and the rule that no entry may claim a
// Windows class on a non-Windows platform.
func (c *Compat) Validate() error {
	var p []string
	if c.Schema != 1 {
		p = append(p, fmt.Sprintf("schema must be 1, got %d", c.Schema))
	}
	if _, ok := ParseVersion(c.Node.Min); !ok {
		p = append(p, fmt.Sprintf("node.min %q is not a version", c.Node.Min))
	}
	for id, a := range c.Adapters {
		vmin, ok := ParseVersion(a.Min)
		if !ok {
			p = append(p, fmt.Sprintf("%s.min %q is not a version", id, a.Min))
		}
		if a.Recommended != "" {
			if r, ok2 := ParseVersion(a.Recommended); !ok2 || (ok && r.Less(vmin)) {
				p = append(p, fmt.Sprintf("%s.recommended %q is not a version at or above min", id, a.Recommended))
			}
		}
		seen := map[string]bool{}
		for i, e := range a.Entries {
			w := fmt.Sprintf("%s.entries[%d]", id, i)
			if _, ok := ParseVersion(e.Version); !ok {
				p = append(p, w+": version "+e.Version+" is not a version")
			}
			if _, known := classRank[e.Class]; !known {
				p = append(p, w+": unknown class "+string(e.Class))
			}
			if e.Date == "" || e.Platform == "" || e.Method == "" {
				p = append(p, w+": date, platform and method are required (an entry says what was actually run)")
			}
			key := e.Version + "/" + e.Transport + "/" + e.Platform
			if seen[key] {
				p = append(p, w+": duplicate entry for "+key)
			}
			seen[key] = true
			if (e.Class == ClassVerifiedShort || e.Class == ClassVerifiedFull) && !strings.EqualFold(e.Platform, "windows") {
				p = append(p, w+": "+string(e.Class)+" means a Windows run; platform is "+e.Platform)
			}
		}
	}
	for i, b := range c.Blocked {
		if _, ok := ParseVersion(b.Version); !ok || b.Adapter == "" || b.Reason == "" {
			p = append(p, fmt.Sprintf("blocked[%d]: adapter, version and reason are required", i))
		}
	}
	if len(p) > 0 {
		sort.Strings(p)
		return fmt.Errorf("compat.yaml is invalid: %s", strings.Join(p, "; "))
	}
	return nil
}

// Standing is how a detected version relates to what has been verified.
type Standing int

const (
	// StandingBelowMin is older than the adapter's minimum.
	StandingBelowMin Standing = iota
	// StandingVerified is a version with an entry of class inspected or better.
	StandingVerified
	// StandingBetween is at or above min, below the newest verified version, with no entry of its own: allowed.
	StandingBetween
	// StandingUntested is above the newest verified version (or the adapter has no entries): allowed but degraded
	// until a Test connection passes on it.
	StandingUntested
	// StandingBlocked is listed in `blocked`.
	StandingBlocked
)

// Classify places version relative to the verified set for adapter.
func (c *Compat) Classify(adapter, version string) (Standing, *CompatEntry) {
	v, ok := ParseVersion(version)
	if !ok {
		return StandingUntested, nil
	}
	for _, b := range c.Blocked {
		if b.Adapter == adapter {
			if bv, ok := ParseVersion(b.Version); ok && bv.Compare(v) == 0 {
				return StandingBlocked, nil
			}
		}
	}
	a, known := c.Adapters[adapter]
	if !known {
		return StandingUntested, nil
	}
	if m, ok := ParseVersion(a.Min); ok && v.Less(m) {
		return StandingBelowMin, nil
	}
	var newest Version
	have := false
	for i := range a.Entries {
		e := &a.Entries[i]
		if !e.Class.AtLeast(ClassInspected) {
			continue
		}
		ev, ok := ParseVersion(e.Version)
		if !ok {
			continue
		}
		if ev.Compare(v) == 0 {
			return StandingVerified, e
		}
		if !have || newest.Less(ev) {
			newest, have = ev, true
		}
	}
	if !have || newest.Less(v) {
		return StandingUntested, nil
	}
	return StandingBetween, nil
}

// Best returns the strongest entry for the exact version, if any.
func (c *Compat) Best(adapter, version string) (CompatEntry, bool) {
	v, ok := ParseVersion(version)
	if !ok {
		return CompatEntry{}, false
	}
	var best CompatEntry
	found := false
	for _, e := range c.Adapters[adapter].Entries {
		if ev, ok := ParseVersion(e.Version); ok && ev.Compare(v) == 0 && (!found || e.Class.AtLeast(best.Class)) {
			best, found = e, true
		}
	}
	return best, found
}

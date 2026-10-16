package manifests_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

func TestEmbeddedCompatLoadsAndAgreesWithManifests(t *testing.T) {
	c, err := manifests.EmbeddedCompat()
	if err != nil {
		t.Fatal(err)
	}
	if c.Node.Min != "22" {
		t.Errorf("node %q (decision D9)", c.Node.Min)
	}
	s := embeddedSet(t)
	for _, m := range s.All() {
		if m.Meta.Custom {
			continue
		}
		a, ok := c.Adapters[m.Meta.ID]
		if !ok {
			t.Errorf("compat.yaml has no entry for %s", m.Meta.ID)
			continue
		}
		if a.Min != m.Versions.Min || a.Recommended != m.Versions.Recommended {
			t.Errorf("%s: compat min/recommended %s/%s differ from the manifest %s/%s", m.Meta.ID, a.Min, a.Recommended, m.Versions.Min, m.Versions.Recommended)
		}
	}
	for id := range c.Adapters {
		if _, ok := s.Get(id); !ok {
			t.Errorf("compat.yaml names unknown adapter %s", id)
		}
	}
	// no entry may claim more than a Linux run proved (decision P2)
	for id, a := range c.Adapters {
		for _, e := range a.Entries {
			if e.Class.AtLeast(manifests.ClassVerifiedShort) {
				t.Errorf("%s %s claims %s without a Windows run", id, e.Version, e.Class)
			}
			if e.Platform != "linux" {
				t.Errorf("%s %s: platform %q", id, e.Version, e.Platform)
			}
		}
	}
}

func TestClassifyVersions(t *testing.T) {
	c, err := manifests.LoadCompat([]byte(`
schema: 1
node: {min: "22"}
adapters:
  x:
    min: "1.0.0"
    entries:
      - {version: "1.2.0", class: inspected, date: 2026-01-01, platform: linux, method: m}
      - {version: "1.4.0", class: verified_linux, date: 2026-01-02, platform: linux, method: m}
      - {version: "1.5.0", class: docs_only, date: 2026-01-03, platform: linux, method: m}
  y: {min: "2.0.0", entries: []}
blocked: [{adapter: x, version: "1.3.0", reason: "crashes on start"}]
`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		adapter, version string
		want             manifests.Standing
	}{
		{"x", "0.9.9", manifests.StandingBelowMin},
		{"x", "1.2.0", manifests.StandingVerified},
		{"x", "1.4.0", manifests.StandingVerified},
		{"x", "1.2.5", manifests.StandingBetween},
		{"x", "1.3.0", manifests.StandingBlocked},
		{"x", "1.4.1", manifests.StandingUntested},
		{"x", "1.5.0", manifests.StandingUntested}, // docs_only is not verification
		{"y", "2.0.0", manifests.StandingUntested}, // no entries: nothing has been run
		{"zzz", "1.0.0", manifests.StandingUntested},
		{"x", "banana", manifests.StandingUntested},
	}
	for _, tc := range cases {
		if got, _ := c.Classify(tc.adapter, tc.version); got != tc.want {
			t.Errorf("%s %s: got %v want %v", tc.adapter, tc.version, got, tc.want)
		}
	}
	if e, ok := c.Best("x", "1.4.0"); !ok || e.Class != manifests.ClassVerifiedLinux {
		t.Errorf("best %+v %v", e, ok)
	}
	if !manifests.ClassVerifiedFull.AtLeast(manifests.ClassInspected) || manifests.ClassDocsOnly.AtLeast(manifests.ClassInspected) {
		t.Error("class order")
	}
}

func TestCompatValidationRejectsDishonestEntries(t *testing.T) {
	head := "schema: 1\nnode: {min: '22'}\nadapters:\n  x:\n    min: '1.0.0'\n    entries:\n"
	cases := map[string]struct{ entry, want string }{
		"windows class on linux": {"      - {version: '1.0.0', class: verified_short, date: 2026-01-01, platform: linux, method: m}", "means a Windows run"},
		"unknown class":          {"      - {version: '1.0.0', class: gold, date: 2026-01-01, platform: linux, method: m}", "unknown class"},
		"no method":              {"      - {version: '1.0.0', class: inspected, date: 2026-01-01, platform: linux}", "required"},
		"no date":                {"      - {version: '1.0.0', class: inspected, platform: linux, method: m}", "required"},
		"bad version":            {"      - {version: 'abc', class: inspected, date: 2026-01-01, platform: linux, method: m}", "not a version"},
		"duplicate":              {"      - {version: '1.0.0', class: inspected, date: 2026-01-01, platform: linux, method: m}\n      - {version: '1.0.0', class: inspected, date: 2026-01-02, platform: linux, method: n}", "duplicate"},
		"unknown field":          {"      - {version: '1.0.0', class: inspected, date: 2026-01-01, platform: linux, method: m, verified: yes}", "verified"},
	}
	for name, tc := range cases {
		if _, err := manifests.LoadCompat([]byte(head + tc.entry + "\n")); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v want %q", name, err, tc.want)
		}
	}
	if _, err := manifests.LoadCompat([]byte("schema: 2\nnode: {min: '22'}\nadapters: {}\n")); err == nil {
		t.Error("schema 2")
	}
	if _, err := manifests.LoadCompat([]byte("schema: 1\nnode: {min: ''}\nadapters: {}\n")); err == nil {
		t.Error("node min")
	}
}

package core_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
)

// The startup probe's switch is reported by route.settings.get with who set it (effort review F8): off by default and set by
// nobody, then what route.probe.startup stored, with an event the page re-reads on.
func TestRouteSettingsReportTheStartupProbeAndWhoSetIt(t *testing.T) {
	a := open(t, "")
	var rs core.RouteSettings
	a.call("route.settings.get", nil, &rs)
	if rs.ProbeStartup || rs.ProbeStartupBy != "" {
		t.Fatalf("default %+v / %q", rs.ProbeStartup, rs.ProbeStartupBy)
	}
	var out struct {
		Enabled bool   `json:"enabled"`
		By      string `json:"by"`
	}
	a.call("route.probe.startup", map[string]bool{"enabled": true}, &out)
	if !out.Enabled || out.By == "" {
		t.Fatalf("set %+v", out)
	}
	a.call("route.settings.get", nil, &rs)
	if !rs.ProbeStartup || rs.ProbeStartupBy != out.By {
		t.Fatalf("after turning on: %v by %q", rs.ProbeStartup, rs.ProbeStartupBy)
	}
	a.call("route.probe.startup", map[string]bool{"enabled": false}, &out)
	a.call("route.settings.get", nil, &rs)
	if rs.ProbeStartup || rs.ProbeStartupBy != out.By {
		t.Fatalf("after turning off: %v by %q", rs.ProbeStartup, rs.ProbeStartupBy)
	}
	if evs := a.eventsOf("routing.settings_changed"); len(evs) != 2 {
		t.Fatalf("events %v", evs)
	}
}

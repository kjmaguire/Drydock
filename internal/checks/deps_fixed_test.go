package checks

import (
	"strings"
	"testing"
)

// osv-scanner's records name the versions that fix each advisory (affected[].ranges[].events[].fixed): the finding keeps the
// lowest version above the installed one that fixes them all, and the advisories with no such version. A "version" that does
// not look like one (it comes from an outside database) is dropped.
func TestParseOSVNamesTheVersionThatFixesTheAdvisories(t *testing.T) {
	js := `{"results":[{"source":{"path":"go.mod"},"packages":[{"package":{"name":"golang.org/x/net","version":"0.10.0","ecosystem":"Go"},
	  "vulnerabilities":[
	    {"id":"GO-1","affected":[{"package":{"name":"golang.org/x/net","ecosystem":"Go"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"0.9.0"},{"fixed":"0.17.0"},{"fixed":"0.12.0"}]}]},
	                             {"package":{"name":"other","ecosystem":"Go"},"ranges":[{"events":[{"fixed":"9.9.9"}]}]}]},
	    {"id":"GO-2","affected":[{"package":{"name":"golang.org/x/net","ecosystem":"Go"},"ranges":[{"events":[{"fixed":"0.11.0"},{"fixed":"$(rm -rf)"}]}]}]},
	    {"id":"GO-3","affected":[{"package":{"name":"golang.org/x/net","ecosystem":"Go"},"ranges":[{"events":[{"introduced":"0"}]}]}]}
	  ]}]}]}`
	fs, err := ParseOSV([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Fixed != "0.12.0" || strings.Join(fs[0].Unfixed, ",") != "GO-3" {
		t.Fatalf("%+v", fs)
	}
	for _, c := range []struct {
		a, b string
		want int
	}{{"1.2.10", "1.2.9", 1}, {"v1.2.0", "1.2.0", 0}, {"1.2.0-rc1", "1.2.0", -1}, {"1.2", "1.2.1", -1}, {"2.0.0", "10.0.0", -1}, {"1.0.0-beta.2", "1.0.0-beta.10", -1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

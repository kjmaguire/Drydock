package core

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/sandbox"
)

// The forward list says which ports have their window open (effort review X314), asked of the viewer at every list: a cached list
// still says it as it is now. The engine-backed test of the whole path needs a container engine; this one does not.
func TestTheForwardListSaysWhichWindowsAreOpen(t *testing.T) {
	open := map[string]bool{"http://127.0.0.1:49153/": true}
	c := &Core{cfg: Config{SandboxAppShown: func(url string) bool { return open[url] }}} // TEST DOUBLE: the viewer's windows
	v := SandboxForwards{Forwards: []sandbox.Forward{{Port: 3000, HostPort: 49153}, {Port: 4000, HostPort: 49154}, {Port: 5000}}}
	if got := c.withViewing(v).Viewing; len(got) != 1 || got[0] != 3000 {
		t.Fatalf("viewing %v", got)
	}
	open["http://127.0.0.1:49154/"] = true
	delete(open, "http://127.0.0.1:49153/")
	if got := c.withViewing(v).Viewing; len(got) != 1 || got[0] != 4000 {
		t.Fatalf("viewing %v", got)
	}
	// a window without a viewer lists none, as an empty list
	if got := (&Core{}).withViewing(v).Viewing; got == nil || len(got) != 0 {
		t.Fatalf("no viewer: %v", got)
	}
	if sandboxAppURL(sandbox.Forward{HostPort: 49153}) != "http://127.0.0.1:49153/" {
		t.Fatal(sandboxAppURL(sandbox.Forward{HostPort: 49153}))
	}
}

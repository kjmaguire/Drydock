package sandbox

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/sandbox/sandboxtest"
)

// The Docker tests build their images offline from this machine's programs (sandboxtest); the product pulls the images a
// project names.

type testImages struct {
	Base, Web string // tags
	Egress    string // path of the Linux drydock-egress binary
}

var (
	webOnce sync.Once
	webBin  string
)

// dockerOrSkip returns the engine, or skips the test when no engine runs here (set DRYDOCK_REQUIRE_DOCKER=1 to fail instead).
func dockerOrSkip(t *testing.T) *Engine {
	t.Helper()
	return &Engine{Bin: sandboxtest.Engine(t)}
}

// buildTestImages builds the base and web images and the egress binary once per test run.
func buildTestImages(t *testing.T, e *Engine) testImages {
	t.Helper()
	webOnce.Do(func() {
		dir, err := os.MkdirTemp("", "drydock-sandbox-web-*")
		if err != nil {
			t.Fatal(err)
		}
		webBin = filepath.Join(dir, "web")
		sandboxtest.GoBuild(t, webBin, "./testdata/webserver")
	})
	return testImages{
		Base:   sandboxtest.Base(t, e.Bin),
		Web:    sandboxtest.Custom(t, e.Bin, "drydock-sandbox-test/web:2", map[string]string{"/web": webBin}, `CMD ["/web"]`),
		Egress: sandboxtest.EgressBinary(t),
	}
}

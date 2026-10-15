package localllm_test

import (
	"os"
	"testing"

	"github.com/kjmaguire/drydock/internal/localllm/llamatest"
)

// The test binary is also the LABELLED STAND-IN of llama-server.
func TestMain(m *testing.M) {
	if llamatest.IsStandIn(os.Args) {
		os.Exit(llamatest.Main(os.Args))
	}
	os.Exit(m.Run())
}

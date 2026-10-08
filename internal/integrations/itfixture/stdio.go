package itfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AikidoStdioFlag is the first argument that turns a test binary into the Aikido fixture server (see RunAikidoStdio).
const AikidoStdioFlag = "-itfixture-aikido"

// RunAikidoStdio runs the Aikido fixture as a real stdio MCP server, the way the user-installed @aikidosec/mcp package runs.
// A test binary calls it from TestMain when os.Args[1] == AikidoStdioFlag. The remaining arguments are
// -findings=<json file> (re-read on every list call, so a test can change what Aikido "finds") and
// -expect=<sha256 hex of the API key>: the server accepts a call only when the environment variable AIKIDO_API_KEY hashes to it,
// which proves the key arrived through the environment and never through argv.
func RunAikidoStdio(args []string) int {
	var findingsPath, expect string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-findings="):
			findingsPath = strings.TrimPrefix(a, "-findings=")
		case strings.HasPrefix(a, "-expect="):
			expect = strings.TrimPrefix(a, "-expect=")
		}
	}
	f := NewAikido()
	f.Load = func() []AikidoFinding {
		b, err := os.ReadFile(findingsPath)
		if err != nil {
			return nil
		}
		var v struct {
			Findings []AikidoFinding `json:"findings"`
		}
		_ = json.Unmarshal(b, &v)
		return v.Findings
	}
	f.KeyCheck = func() error {
		key := os.Getenv("AIKIDO_API_KEY")
		if key == "" {
			return errors.New("AIKIDO_API_KEY is not set")
		}
		sum := sha256.Sum256([]byte(key))
		if expect != "" && hex.EncodeToString(sum[:]) != expect {
			return errors.New("invalid api key")
		}
		return nil
	}
	if err := f.Server().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "aikido fixture:", err)
		return 1
	}
	return 0
}

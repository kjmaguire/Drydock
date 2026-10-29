package main

import (
	"context"
	"strings"
	"testing"
)

// B0's CLI and provider flags (decision P121) are checked before any run starts and before any CLI is asked for its version.
func TestRunChecksB0sCLIAndProviderFirst(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-arms", "B0", "-b0-cli", "no-such-cli"}, `-b0-cli no-such-cli`},
		{[]string{"-arms", "B0", "-b0-cli", "gemini"}, "-b0-cli gemini is not installed here"},
		{[]string{"-arms", "B0", "-b0-provider-url", "https://api.cheap.example"}, "go with -b0-provider"},
		{[]string{"-arms", "B0", "-b0-provider", "cheapco", "-b0-provider-url", "https://api.cheap.example", "-b0-provider-key-env", "B0_FLAGS_NO_SUCH_KEY"}, "is empty"},
		{[]string{"-arms", "B0", "-b0-provider", "cheapco", "-b0-provider-url", "http://api.cheap.example", "-b0-provider-key-env", "PATH"}, "must be https"},
	} {
		args := append([]string{"-tasks", "saasapi-invoice-rules", "-claude", "no-such-claude"}, c.args...)
		if err := run(context.Background(), args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

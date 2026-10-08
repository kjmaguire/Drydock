package proc

import (
	"sort"
	"strings"
	"testing"
)

func TestScrubEnvKeepsOnlyAllowlistedNamesAndPrefixes(t *testing.T) {
	parent := []string{
		"PATH=/bin", "Path=C:\\Windows", "HOME=/home/u", "GITHUB_TOKEN=ghp_secret", "AWS_SECRET_ACCESS_KEY=aws_secret", "CLOUDFLARE_API_TOKEN=cf",
		"CLAUDE_CODE_OAUTH_TOKEN=tok", "claude_lower=1", "ANTHROPIC_API_KEY=sk-ant", "HTTPS_PROXY=http://p", "MY_COMPANY_VAR=x", "NOEQUALS", "DRYDOCK_X=1",
	}
	got := ScrubEnv(parent, append(append([]string(nil), BaseEnvNames...), "my_company_var"), []string{"CLAUDE_", "ANTHROPIC_"})
	sort.Strings(got)
	want := []string{"ANTHROPIC_API_KEY=sk-ant", "CLAUDE_CODE_OAUTH_TOKEN=tok", "HOME=/home/u", "HTTPS_PROXY=http://p", "MY_COMPANY_VAR=x", "PATH=/bin", "Path=C:\\Windows", "claude_lower=1"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for _, kv := range got {
		for _, bad := range []string{"GITHUB_TOKEN", "AWS_", "CLOUDFLARE", "DRYDOCK_"} {
			if strings.HasPrefix(kv, bad) {
				t.Errorf("%s leaked", kv)
			}
		}
	}
}

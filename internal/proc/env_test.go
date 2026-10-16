package proc

import (
	"os"
	"sort"
	"strconv"
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

func TestToolEnvKeepsToolchainsAndDropsCredentials(t *testing.T) {
	parent := []string{"PATH=/bin", "GOPATH=/go", "GOFLAGS=-mod=mod", "JAVA_HOME=/jdk", "NUMBER_OF_PROCESSORS=8", "GH_TOKEN=ghp_x", "GITHUB_TOKEN=ghp_y",
		"NPM_TOKEN=npm_z", "npm_config_//registry.npmjs.org/:_authToken=npm_w", "AWS_SECRET_ACCESS_KEY=a", "ANTHROPIC_API_KEY=sk-ant", "CLAUDE_CODE_OAUTH_TOKEN=c",
		"CLOUDFLARE_API_TOKEN=cf", "DATABASE_URL=postgres://u:p@h/db", "DRYDOCK_DEV_TOKEN=d"}
	got := ToolEnv(parent, []string{"database_url"})
	sort.Strings(got)
	want := []string{"DATABASE_URL=postgres://u:p@h/db", "GOFLAGS=-mod=mod", "GOPATH=/go", "JAVA_HOME=/jdk", "NUMBER_OF_PROCESSORS=8", "PATH=/bin"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for _, n := range ToolEnvNames {
		for _, bad := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "AUTH"} {
			if strings.Contains(n, bad) {
				t.Errorf("%s in ToolEnvNames looks like a credential", n)
			}
		}
	}
}

func TestGitConfigEnvNamesThreeSettingsAndAnEmptyHooksDirectory(t *testing.T) {
	env := GitConfigEnv()
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["GIT_CONFIG_COUNT"] != "3" {
		t.Fatalf("count: %v", env)
	}
	want := map[string]string{"core.hooksPath": NoHooksDir(), "core.fsmonitor": "false", "diff.ignoreSubmodules": "all"}
	for i := 0; i < 3; i++ {
		k := got["GIT_CONFIG_KEY_"+strconv.Itoa(i)]
		if v, ok := want[k]; !ok || got["GIT_CONFIG_VALUE_"+strconv.Itoa(i)] != v {
			t.Fatalf("setting %d: %q = %q in %v", i, k, got["GIT_CONFIG_VALUE_"+strconv.Itoa(i)], env)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Fatalf("settings missing: %v", want)
	}
	if ents, err := os.ReadDir(NoHooksDir()); err != nil || len(ents) != 0 {
		t.Fatalf("the hooks directory must exist and be empty: %v %v", ents, err)
	}
	// the scrubbed environments keep no GIT_CONFIG_* of their own, so these entries are the only ones
	scrubbed := ToolEnv([]string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.sshCommand", "GIT_CONFIG_VALUE_0=evil", "PATH=/bin"}, nil)
	if len(scrubbed) != 1 || scrubbed[0] != "PATH=/bin" {
		t.Fatalf("ToolEnv kept %v", scrubbed)
	}
}

func TestWithSafeExeSearchAlwaysEndsWithTheSettingOn(t *testing.T) {
	got := WithSafeExeSearch([]string{"PATH=/bin", "nodefaultcurrentdirectoryinexepath=0", "A=b"})
	if strings.Join(got, "|") != "PATH=/bin|A=b|NoDefaultCurrentDirectoryInExePath=1" {
		t.Fatalf("got %v", got)
	}
	t.Setenv("DRYDOCK_X", "1")
	inherited := WithSafeExeSearch(nil)
	if len(inherited) == 0 || inherited[len(inherited)-1] != "NoDefaultCurrentDirectoryInExePath=1" {
		t.Fatalf("nil must mean this process's environment, with the setting: %v", inherited)
	}
	seen := false
	for _, kv := range inherited {
		seen = seen || kv == "DRYDOCK_X=1"
	}
	if !seen {
		t.Fatal("the inherited environment is missing")
	}
	// a scrubbed base environment keeps the setting when the app has it
	if got := ScrubEnv([]string{"PATH=/bin", "NoDefaultCurrentDirectoryInExePath=1", "TOKEN=x"}, BaseEnvNames, nil); strings.Join(got, "|") != "PATH=/bin|NoDefaultCurrentDirectoryInExePath=1" {
		t.Fatalf("scrubbed %v", got)
	}
}

func TestHardenProcessSetsTheSettingForInheritors(t *testing.T) {
	t.Setenv(NoCwdSearchVar, "")
	HardenProcess()
	if os.Getenv(NoCwdSearchVar) != "1" {
		t.Fatalf("got %q", os.Getenv(NoCwdSearchVar))
	}
}

// A proxy address reaches a scrubbed agent or command without its user name and password, so a program an agent steers cannot
// read them from its environment; the address stays, and a variable the person passes on by name is left as it is (audit
// 2026-10-16, L8).
func TestProxyCredentialsAreTakenOutOfTheAddressUnlessThePersonPassesItOn(t *testing.T) {
	parent := []string{"PATH=/bin", "HTTPS_PROXY=http://alice:s3cret@proxy.corp:8080", "http_proxy=https://bob:p%40ss@proxy.corp:3128/pac?x=1",
		"ALL_PROXY=socks5://u:p@sock:1080", "NO_PROXY=localhost,.corp", "HTTP_PROXY=alice:s3cret@proxy.corp:8080", "FTP_PROXY=http://a:b@ftp"}
	got := ToolEnv(parent, nil)
	sort.Strings(got)
	want := []string{"ALL_PROXY=socks5://sock:1080", "HTTPS_PROXY=http://proxy.corp:8080", "HTTP_PROXY=proxy.corp:8080", "NO_PROXY=localhost,.corp", "PATH=/bin",
		"http_proxy=https://proxy.corp:3128/pac?x=1"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for _, kv := range got {
		if strings.Contains(kv, "s3cret") || strings.Contains(kv, "p%40ss") || strings.Contains(kv, "alice") || strings.Contains(kv, "u:p@") {
			t.Errorf("%s still holds a credential", kv)
		}
	}
	// the person passes HTTPS_PROXY on by name: it is handed over as it is, the others are still cut
	got = ToolEnv(parent, []string{"https_proxy"})
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "HTTPS_PROXY=http://alice:s3cret@proxy.corp:8080") || strings.Contains(joined, "ALL_PROXY=socks5://u:p@") {
		t.Fatalf("a variable passed on by name must stay as it is, the others cut: %v", got)
	}
	// an address with an @ in its path or query keeps it; one with no credentials is untouched
	for in, want := range map[string]string{"http://proxy:8080/a@b": "http://proxy:8080/a@b", "http://proxy:8080": "http://proxy:8080", "proxy:8080": "proxy:8080",
		"http://u:p@host:1/a@b?c=d@e": "http://host:1/a@b?c=d@e", "http://a@b@host": "http://host", "": ""} {
		if got := stripUserinfo(in); got != want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

// A scrub that keeps nothing is an empty environment, not a nil one: a nil Spec.Env starts the child with the app's whole
// environment (audit 2026-10-16, L31).
func TestAScrubThatKeepsNothingIsNotNil(t *testing.T) {
	got := ScrubEnv([]string{"GITHUB_TOKEN=x", "AWS_SECRET_ACCESS_KEY=y"}, []string{"PATH"}, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
	if got := ToolEnv(nil, nil); got == nil {
		t.Fatal("ToolEnv of nothing is nil")
	}
}

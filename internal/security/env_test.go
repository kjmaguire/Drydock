package security

import (
	"reflect"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if n, v, ok := splitEnv(kv); ok {
			m[n] = v
		}
	}
	return m
}

func TestScrubEnvDefaults(t *testing.T) {
	in := []string{
		"PATH=/usr/bin", "HOME=/home/k", "USERPROFILE=C:\\Users\\k", "LANG=en_US.UTF-8", "SystemRoot=C:\\Windows", "TEMP=C:\\Temp",
		"GH_TOKEN=x", "GITHUB_TOKEN=x", "github_token=lower", "CLOUDFLARE_API_TOKEN=x", "CLOUDFLARE_ACCOUNT_ID=x", "CF_API_TOKEN=x",
		"ANTHROPIC_API_KEY=x", "OPENAI_API_KEY=x", "OPENROUTER_API_KEY=x", "GEMINI_API_KEY=x", "CLAUDE_CODE_OAUTH_TOKEN=x",
		"AWS_ACCESS_KEY_ID=x", "AWS_SECRET_ACCESS_KEY=x", "AZURE_CLIENT_ID=x", "GOOGLE_APPLICATION_CREDENTIALS=/k.json",
		"DRYDOCK_DB=/x", "DRYDOCK_MASTER=x", "NPM_TOKEN=x", "MY_SERVICE_PASSWORD=x", "DB_PASSWD=x", "SOME_SECRET_THING=x", "STRIPE_KEY=x",
		"SSH_PRIVATE_THING=x", "MY_CREDENTIALS=x", "NODE_ENV=test", "EDITOR=vim", "=C:=C:\\work", "MALFORMED",
	}
	got := envMap(ScrubEnv(in, EnvPolicy{}))
	for _, keep := range []string{"PATH", "HOME", "USERPROFILE", "LANG", "SystemRoot", "TEMP", "NODE_ENV", "EDITOR", "=C:"} {
		if _, ok := got[keep]; !ok {
			t.Errorf("%s was removed", keep)
		}
	}
	for _, drop := range []string{"GH_TOKEN", "GITHUB_TOKEN", "github_token", "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID", "CF_API_TOKEN",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GEMINI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY", "AZURE_CLIENT_ID", "GOOGLE_APPLICATION_CREDENTIALS", "DRYDOCK_DB", "DRYDOCK_MASTER", "NPM_TOKEN",
		"MY_SERVICE_PASSWORD", "DB_PASSWD", "SOME_SECRET_THING", "STRIPE_KEY", "SSH_PRIVATE_THING", "MY_CREDENTIALS", "MALFORMED"} {
		if _, ok := got[drop]; ok {
			t.Errorf("%s survived the scrub", drop)
		}
	}
}

func TestScrubEnvAllowAndNever(t *testing.T) {
	in := []string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-a", "ANTHROPIC_BASE_URL=https://x", "OPENAI_API_KEY=sk-o", "GH_TOKEN=gh", "GITHUB_TOKEN=gh",
		"CLOUDFLARE_API_TOKEN=cf", "DRYDOCK_SECRET=d", "MY_TOKEN=t"}
	got := envMap(ScrubEnv(in, EnvPolicy{Allow: []string{"anthropic_api_key", "ANTHROPIC_BASE_URL", "MY_TOKEN"}}))
	if got["ANTHROPIC_API_KEY"] != "sk-a" || got["ANTHROPIC_BASE_URL"] == "" || got["MY_TOKEN"] != "t" {
		t.Errorf("allowed variables missing: %v", got)
	}
	for _, n := range []string{"OPENAI_API_KEY"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s was not allowed but survived", n)
		}
	}
	// the strip list cannot be overridden by an adapter
	got = envMap(ScrubEnv(in, EnvPolicy{Allow: []string{"GH_TOKEN", "GITHUB_TOKEN", "CLOUDFLARE_*", "DRYDOCK_*"}, Set: map[string]string{"CLOUDFLARE_API_TOKEN": "x", "DRYDOCK_SECRET": "x"}}))
	for _, n := range []string{"GH_TOKEN", "GITHUB_TOKEN", "CLOUDFLARE_API_TOKEN", "DRYDOCK_SECRET"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s must never reach an agent", n)
		}
	}
}

func TestScrubEnvSetAndOrder(t *testing.T) {
	in := []string{"B=1", "PATH=/bin", "A=2", "path=dup", "ANTHROPIC_API_KEY=old"}
	out := ScrubEnv(in, EnvPolicy{Set: map[string]string{"ANTHROPIC_API_KEY": "from-vault", "Z_EXTRA": "z"}, Deny: []string{"A"}})
	want := []string{"B=1", "PATH=/bin", "ANTHROPIC_API_KEY=from-vault", "Z_EXTRA=z"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
	if got := ScrubEnv(in, EnvPolicy{Set: map[string]string{"BAD=NAME": "x", "": "y"}}); len(got) != 3 { // B, PATH, A (path dup dropped)
		t.Fatalf("invalid Set names accepted: %v", got)
	}
}

func TestScrubEnvOnlyBase(t *testing.T) {
	in := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "LC_CTYPE=C", "RANDOM_VAR=1", "EDITOR=vim", "ANTHROPIC_API_KEY=k"}
	got := envMap(ScrubEnv(in, EnvPolicy{OnlyBase: true, Allow: []string{"ANTHROPIC_API_KEY"}}))
	for _, n := range []string{"PATH", "HOME", "LC_ALL", "LC_CTYPE", "ANTHROPIC_API_KEY"} {
		if _, ok := got[n]; !ok {
			t.Errorf("%s missing", n)
		}
	}
	for _, n := range []string{"RANDOM_VAR", "EDITOR"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s kept in base-only mode", n)
		}
	}
}

func TestScrubEnvNeverLeaksValues(t *testing.T) {
	in := []string{"X_API_KEY=" + fakeGitHubToken(), "PATH=/bin"}
	out := strings.Join(ScrubEnv(in, EnvPolicy{}), "\n")
	if strings.Contains(out, fakeGitHubToken()) {
		t.Fatal("secret value in scrubbed environment")
	}
	if names := EnvNames(ScrubEnv(in, EnvPolicy{})); !reflect.DeepEqual(names, []string{"PATH"}) {
		t.Fatalf("names %v", names)
	}
}

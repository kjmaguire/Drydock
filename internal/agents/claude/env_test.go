package claude

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
)

func agentsRunSpec() agents.RunSpec { return agents.RunSpec{AgentID: "a1", Cwd: "/x"} }

func TestChildEnvIsScrubbedAndTakesNamesToPassFromTheHuman(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h", "GITHUB_TOKEN=ghp_x", "AWS_PROFILE=work", "CLAUDE_CODE_OAUTH_TOKEN=t", "ANTHROPIC_BASE_URL=u"}
	has := func(env []string, name string) bool {
		for _, kv := range env {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	}
	open := (&Adapter{}).childEnv(parent)
	if len(open) != len(parent) {
		t.Fatalf("without ScrubEnv the environment is inherited whole: %v", open)
	}
	a := &Adapter{ScrubEnv: true}
	env := a.childEnv(parent)
	for _, keep := range []string{"PATH", "HOME", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if !has(env, keep) {
			t.Errorf("%s was removed: claude needs it to run and sign in", keep)
		}
	}
	for _, drop := range []string{"GITHUB_TOKEN", "AWS_PROFILE"} {
		if has(env, drop) {
			t.Errorf("%s reached the agent", drop)
		}
	}
	// a Bedrock login the allowlist cannot know about: the human names the variable and it is read at every start
	pass := []string{}
	a.PassEnv = func() []string { return pass }
	if has(a.childEnv(parent), "AWS_PROFILE") {
		t.Fatal("passed before the human asked")
	}
	pass = []string{"aws_profile"}
	env = a.childEnv(parent)
	if !has(env, "AWS_PROFILE") || has(env, "GITHUB_TOKEN") {
		t.Fatalf("pass-through: %v", env)
	}
}

func TestUserSettingsOnlyAddsTheSettingSourcesFlag(t *testing.T) {
	has := func(args []string) bool {
		for i, a := range args {
			if a == "--setting-sources" && i+1 < len(args) && args[i+1] == "user" {
				return true
			}
		}
		return false
	}
	if has((&Adapter{}).Args(agentsRunSpec())) {
		t.Fatal("the flag must be opt-in on the adapter")
	}
	if !has((&Adapter{UserSettingsOnly: true}).Args(agentsRunSpec())) {
		t.Fatal("a repository's own hooks would run in an unattended agent")
	}
}

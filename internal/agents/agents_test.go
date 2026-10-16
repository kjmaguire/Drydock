package agents_test

import (
	"context"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
)

// nativeOnly is a LABELLED TEST DOUBLE: an adapter that lists only the native run mode and never starts anything.
type nativeOnly struct{ modes []string }

func (n nativeOnly) ID() string                                       { return "x" }
func (n nativeOnly) Detect(context.Context) (agents.Detection, error) { return agents.Detection{}, nil }
func (n nativeOnly) Capabilities() agents.Capabilities                { return agents.Capabilities{RunModes: n.modes} }
func (n nativeOnly) Start(context.Context, proc.Manager, agents.RunSpec) (agents.Session, error) {
	return nil, nil
}

func TestCheckRunModeRefusesAModeTheAdapterDoesNotList(t *testing.T) {
	if err := agents.CheckRunMode(nativeOnly{[]string{"native"}}, agents.RunSpec{RunMode: agents.RunModeWSL}); err == nil {
		t.Fatal("an ACP adapter has no WSL run mode: the start must be refused")
	}
	for _, m := range []string{"", agents.RunModeNative} {
		if err := agents.CheckRunMode(nativeOnly{nil}, agents.RunSpec{RunMode: m}); err != nil {
			t.Fatalf("%q: %v", m, err)
		}
	}
	if err := agents.CheckRunMode(nativeOnly{[]string{"native", "wsl"}}, agents.RunSpec{RunMode: agents.RunModeWSL}); err != nil {
		t.Fatal(err)
	}
}

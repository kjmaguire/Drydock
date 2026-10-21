package agui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheUIsGeneratedTypesAreCurrent(t *testing.T) {
	want, err := TypeScript()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(TSPath)))
	if err != nil {
		t.Fatalf("%v: run go run ./cmd/drydock-tsgen", err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Fatalf("%s is out of date: run go run ./cmd/drydock-tsgen from the repository root", TSPath)
	}
	for _, s := range []string{"export type AguiEvent =", "type: 'TOOL_CALL_RESULT';", "events: AguiEvent[];", "content: PlanActivity;", "export interface PlanEntry"} {
		if !strings.Contains(want, s) {
			t.Errorf("the generated types lack %q", s)
		}
	}
}

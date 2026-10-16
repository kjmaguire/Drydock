package manifests_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

const baseACP = `
meta: {schema: 1, id: demo, display_name: Demo, kind: agent_cli, license: MIT, tier: cheap}
binaries: {native: [demo.exe]}
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
versions: {min: "1.0.0", recommended: "1.2.0"}
transports:
  primary: acp
  acp: {argv: [demo, acp]}
`

func TestValidBaseManifestParses(t *testing.T) {
	m, err := manifests.Parse([]byte(baseACP))
	if err != nil {
		t.Fatal(err)
	}
	if m.Meta.ID != "demo" || m.Transports.ACP.Argv[0] != "demo" || m.Auth.Probe != nil {
		t.Fatalf("%+v", m)
	}
}

func TestInvalidManifestsAreRejectedWithReasons(t *testing.T) {
	r := strings.NewReplacer
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown top-level key", baseACP + "\nsurprise: 1\n", "surprise"},
		{"unknown nested key", strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], colour: red}", 1), "colour"},
		{"unknown transport value", strings.Replace(baseACP, "primary: acp", "primary: websocket", 1), `transports.primary must be "acp" or "headless"`},
		{"unknown transport key", baseACP + "  websocket: {argv: [x]}\n", "websocket"},
		{"primary block missing", strings.Replace(baseACP, "primary: acp", "primary: headless", 1), "transports.headless is missing"},
		{"no transports", r("primary: acp\n  acp: {argv: [demo, acp]}", "primary: acp").Replace(baseACP), "at least one of acp and headless"},
		{"schema", strings.Replace(baseACP, "schema: 1", "schema: 2", 1), "meta.schema must be 1"},
		{"missing id", strings.Replace(baseACP, "id: demo, ", "", 1), "meta.id"},
		{"bad id", strings.Replace(baseACP, "id: demo", "id: Demo_X", 1), "meta.id"},
		{"missing display name", strings.Replace(baseACP, "display_name: Demo, ", "", 1), "display_name is required"},
		{"bad kind", strings.Replace(baseACP, "kind: agent_cli", "kind: model_provider", 1), "meta.kind"},
		{"bad tier", strings.Replace(baseACP, "tier: cheap", "tier: luxury", 1), "tier"},
		{"no binaries", strings.Replace(baseACP, "binaries: {native: [demo.exe]}", "binaries: {}", 1), "binaries.native"},
		{"no version argv", strings.Replace(baseACP, "argv: [--version], ", "", 1), "detect.version.argv"},
		{"bad regex", strings.Replace(baseACP, `'(\d+\.\d+\.\d+)'`, `'(['`, 1), "detect.version.regex"},
		{"regex without capture", strings.Replace(baseACP, `'(\d+\.\d+\.\d+)'`, `'\d+'`, 1), "capture group"},
		{"missing min version", strings.Replace(baseACP, `min: "1.0.0", `, "", 1), "versions.min"},
		{"recommended below min", strings.Replace(baseACP, `recommended: "1.2.0"`, `recommended: "0.9.0"`, 1), "below versions.min"},
		{"empty acp argv", strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: []}", 1), "transports.acp.argv"},
		{"unpinned npx", strings.Replace(baseACP, "[demo, acp]", "[npx, -y, '@agentclientprotocol/x-acp']", 1), "pinned to an exact version"},
		{"floating npx", strings.Replace(baseACP, "[demo, acp]", "[npx, -y, '@agentclientprotocol/x-acp@latest']", 1), "pinned"},
		{"range npx", strings.Replace(baseACP, "[demo, acp]", "[npx, -y, '@agentclientprotocol/x-acp@^2.1.1']", 1), "pinned"},
		{"deprecated bridge", strings.Replace(baseACP, "[demo, acp]", "[npx, -y, '@zed-industries/codex-acp@0.16.0']", 1), "deprecated"},
		{"npx without package", strings.Replace(baseACP, "[demo, acp]", "[npx, -y]", 1), "npx without a package"},
		{"unknown tool requirement", strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], requires: [{tool: ruby, min: '3'}]}", 1), "unknown tool"},
		{"bad requirement version", strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], requires: [{tool: node, min: 'new'}]}", 1), "not a version"},
		{"unknown placeholder", strings.Replace(baseACP, "[demo, acp]", "[demo, acp, '{{secret}}']", 1), "unknown placeholder"},
		{"unknown placeholder in env", strings.Replace(baseACP, "{argv: [demo, acp]}", "{argv: [demo, acp], env: {A: '{{nope}}'}}", 1), "unknown placeholder"},
		{"bad probe parse", baseACP + "auth: {probe: {argv: [s], parse: regex, ready_when: 'exit == 0'}}\n", "auth.probe.parse"},
		{"probe without ready_when", baseACP + "auth: {probe: {argv: [s], parse: json}}\n", "ready_when is required"},
		{"probe bad condition", baseACP + "auth: {probe: {argv: [s], parse: exit_code, ready_when: 'exit is zero'}}\n", "condition"},
		{"probe scalar other than none", baseACP + "auth: {probe: maybe}\n", "auth.probe"},
		{"forbidden flag present", strings.Replace(baseACP, "[demo, acp]", "[demo, acp, --bare]", 1) + "auth: {forbidden_flags: [--bare]}\n", "forbidden flag"},
		{"unknown capability", baseACP + "capabilities_declared: {telepathy: true}\n", "unknown capability"},
		{"bad capability value", baseACP + "capabilities_declared: {streaming: maybe}\n", "is not true, false, unknown or estimate"},
		{"gate without id", baseACP + "gate: {required: true}\n", "gate.id"},
		{"terms without notice", baseACP + "terms: {warning: hi}\n", "terms.notice"},
		{"bad install shell", baseACP + "install: [{shell: zsh, command: x}]\n", "install[0].shell"},
		{"install without command", baseACP + "install: [{shell: cmd}]\n", "install[0].command"},
		{"two documents", baseACP + "---\n" + baseACP, "more than one YAML document"},
		{"repo_config outside the folder", baseACP + "repo_config: ['../.gemini/settings.json']\n", "repo_config[0]"},
		{"repo_config absolute", baseACP + "repo_config: ['/etc/gemini/settings.json']\n", "repo_config[0]"},
		{"repo_config with a backslash", baseACP + "repo_config: ['.gemini\\settings.json']\n", "repo_config[0]"},
		{"repo_config empty", baseACP + "repo_config: ['']\n", "repo_config[0]"},
		{"repo_config the folder itself", baseACP + "repo_config: ['./']\n", "repo_config[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manifests.Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestValidationReportsEveryProblemAtOnce(t *testing.T) {
	bad := `
meta: {schema: 3, id: BAD, display_name: "", kind: nope}
binaries: {}
detect: {version: {argv: [], regex: '['}}
transports: {primary: carrier-pigeon}
`
	_, err := manifests.Parse([]byte(bad))
	var ve *manifests.ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) < 6 {
		t.Fatalf("want many problems in one error, got %v", err)
	}
}

const customBase = `
meta: {schema: 1, id: mycli, display_name: Mine, kind: agent_cli, custom: true}
binaries: {native: [my-cli.exe]}
detect: {version: {argv: [--version], regex: '(\d+\.\d+\.\d+)'}}
gate: {id: custom-runs-arbitrary-code-v1, required: true}
transports:
  primary: headless
  headless:
    argv: [run, --json]
    dialect: {format: jsonl, type_field: type, map: {assistant_text: {type: message, text: $.text}}}
`

func TestCustomManifestRules(t *testing.T) {
	if _, err := manifests.Parse([]byte(customBase)); err != nil {
		t.Fatalf("valid custom manifest rejected: %v", err)
	}
	cases := map[string]struct{ yaml, want string }{
		"names a Go parser":     {strings.Replace(customBase, "    argv: [run, --json]\n", "    argv: [run, --json]\n    parser: claude_sdkmessage\n", 1), "cannot name a Go parser"},
		"no dialect":            {strings.Replace(customBase, "    dialect: {format: jsonl, type_field: type, map: {assistant_text: {type: message, text: $.text}}}\n", "", 1), "dialect"},
		"no gate":               {strings.Replace(customBase, "gate: {id: custom-runs-arbitrary-code-v1, required: true}\n", "", 1), "must carry a required gate"},
		"gate not required":     {strings.Replace(customBase, "required: true", "required: false", 1), "must carry a required gate"},
		"acp transport":         {customBase + "  acp: {argv: [x]}\n", "cannot define an acp transport"},
		"bad dialect format":    {strings.Replace(customBase, "format: jsonl", "format: xml", 1), "dialect.format"},
		"unknown dialect event": {strings.Replace(customBase, "assistant_text:", "telepathy:", 1), "unknown event"},
	}
	for name, tc := range cases {
		if _, err := manifests.Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v want mention of %q", name, err, tc.want)
		}
	}
}

func TestLoadRejectsMismatchedAndDuplicateIDs(t *testing.T) {
	fsys := fstest.MapFS{"m/other.yaml": {Data: []byte(baseACP)}}
	if _, err := manifests.Load(fsys, "m"); err == nil || !strings.Contains(err.Error(), "must match the file name") {
		t.Fatalf("err %v", err)
	}
	fsys = fstest.MapFS{"m/demo.yaml": {Data: []byte(baseACP)}, "m/notes.txt": {Data: []byte("ignored")}}
	s, err := manifests.Load(fsys, "m")
	if err != nil || len(s.IDs()) != 1 {
		t.Fatalf("%v %v", s, err)
	}
	fsys = fstest.MapFS{"m/demo.yaml": {Data: []byte(baseACP)}, "m/broken.yaml": {Data: []byte("meta: [")}}
	if _, err := manifests.Load(fsys, "m"); err == nil {
		t.Fatal("a broken file must fail the load, not be skipped silently")
	}
	if _, err := manifests.Load(fsys, "nope"); err == nil {
		t.Fatal("missing dir")
	}
}

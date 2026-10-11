package sandbox

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfigFillsDefaultsAndRefusesWhatCannotWork(t *testing.T) {
	c, err := ParseConfig([]byte(`{"image":"node:24-bookworm","setup":["npm ci"],"env":{"DATABASE_URL":"postgres://app@db/app"},
		"services":{"db":{"image":"postgres:17","env":{"POSTGRES_PASSWORD":"dev"}}},"allowHosts":["API.Stripe.com","*.example.org"],
		"volumes":["/workspace/node_modules"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Network != NetworkAllowlist || c.CPUs != 2 || c.Memory != "4g" || c.Pids != 2048 {
		t.Fatalf("defaults %+v", c)
	}
	hosts := strings.Join(c.Hosts(), " ")
	for _, want := range []string{"api.stripe.com", "*.example.org", "registry.npmjs.org", "proxy.golang.org"} {
		if !strings.Contains(hosts, want) {
			t.Errorf("allowlist lacks %s: %s", want, hosts)
		}
	}
	c2 := c
	c2.NoDefaultHosts = true
	if got := c2.Hosts(); len(got) != 2 {
		t.Fatalf("without the defaults only the project's hosts remain: %v", got)
	}
	if h1, h2 := c.Hash(), c.Hash(); h1 != h2 || h1 == c2.Hash() {
		t.Fatal("the hash must be stable and change with the definition")
	}

	for name, js := range map[string]string{
		"no image":             `{}`,
		"image with space":     `{"image":"node 24"}`,
		"flag-like image":      `{"image":"--privileged"}`,
		"unknown field":        `{"image":"node","privileged":true}`,
		"proxy override":       `{"image":"node","env":{"HTTPS_PROXY":"http://evil"}}`,
		"drydock env":          `{"image":"node","env":{"DRYDOCK_CMD":"x"}}`,
		"bad env name":         `{"image":"node","env":{"A-B":"x"}}`,
		"service named egress": `{"image":"node","services":{"egress":{"image":"x"}}}`,
		"bad service image":    `{"image":"node","services":{"db":{"image":""}}}`,
		"bad network":          `{"image":"node","network":"host"}`,
		"host with scheme":     `{"image":"node","allowHosts":["https://x.com"]}`,
		"host with port":       `{"image":"node","allowHosts":["x.com:8443"]}`,
		"relative volume":      `{"image":"node","volumes":["node_modules"]}`,
		"workspace volume":     `{"image":"node","volumes":["/workspace"]}`,
		"dotted volume":        `{"image":"node","volumes":["/workspace/../etc"]}`,
		"bad memory":           `{"image":"node","memory":"lots"}`,
		"too many cpus":        `{"image":"node","cpus":500}`,
		"bad user":             `{"image":"node","user":"root; rm -rf /"}`,
		"empty setup":          `{"image":"node","setup":["  "]}`,
	} {
		if _, err := ParseConfig([]byte(js)); err == nil {
			t.Errorf("%s: accepted %s", name, js)
		}
	}
}

func TestStarterSuggestsADefinitionFromTheProjectFiles(t *testing.T) {
	has := func(files ...string) func(string) bool {
		return func(n string) bool {
			for _, f := range files {
				if f == n {
					return true
				}
			}
			return false
		}
	}
	if c := Starter(has("package.json", "package-lock.json")); c.Image != "node:24-bookworm" || c.Setup[0] != "npm ci" || c.Volumes[0] != "/workspace/node_modules" {
		t.Fatalf("node project: %+v", c)
	}
	if c := Starter(has("go.mod")); !strings.HasPrefix(c.Image, "golang:") {
		t.Fatalf("go project: %+v", c)
	}
	if c := Starter(has()); c.Image == "" {
		t.Fatal("an empty project still gets an image")
	}
	for _, c := range []Config{Starter(has("package.json", "pnpm-lock.yaml")), Starter(has("go.mod")), Starter(has("pyproject.toml"))} {
		if err := c.Validate(); err != nil {
			t.Fatalf("a starter must be valid: %v", err)
		}
	}
}

func TestContainerDirStaysInsideTheWorkingFolder(t *testing.T) {
	for in, want := range map[string]string{"": "/workspace", ".": "/workspace", "web": "/workspace/web", "web/./api/": "/workspace/web/api",
		`web\api`: "/workspace/web/api", "/workspace/web": "/workspace/web", "/workspace": "/workspace"} {
		got, err := containerDir(in)
		if err != nil || got != want {
			t.Errorf("containerDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../b", "/etc", "/workspace/../etc"} {
		if got, err := containerDir(bad); err == nil {
			t.Errorf("containerDir(%q) = %q, want a refusal", bad, got)
		}
	}
}

func TestCaptureKeepsTheHeadAndTheTailOfLongOutput(t *testing.T) {
	c := newCapture()
	_, _ = c.Write([]byte("short"))
	if out, n, cut := c.text(); out != "short" || n != 5 || cut {
		t.Fatalf("%q %d %v", out, n, cut)
	}
	c = newCapture()
	big := strings.Repeat("a", headBytes) + strings.Repeat("m", 100000) + strings.Repeat("z", tailBytes)
	for i := 0; i < len(big); i += 777 {
		end := min(i+777, len(big))
		_, _ = c.Write([]byte(big[i:end]))
	}
	out, n, cut := c.text()
	if !cut || n != int64(len(big)) || !strings.HasPrefix(out, strings.Repeat("a", headBytes)) || !strings.HasSuffix(out, strings.Repeat("z", tailBytes)) || strings.Contains(out, "mmmm") {
		t.Fatalf("cut=%v n=%d len(out)=%d", cut, n, len(out))
	}
	res := ExecResult{ExitCode: 0, Output: out, Bytes: n, Truncated: true, Duration: 1500 * time.Millisecond}
	if f := res.Format("x"); !strings.Contains(f, "Exit code 0") || !strings.Contains(f, "output shortened") {
		t.Fatalf("format: %.200s", f)
	}
	if f := (ExecResult{TimedOut: true, Duration: time.Minute}).Format("x"); !strings.Contains(f, "Timed out") {
		t.Fatal(f)
	}
}

func TestBaseNameIsAValidContainerName(t *testing.T) {
	for _, id := range []string{"task_ABC123", "../../etc", strings.Repeat("x", 200), "a b/c"} {
		n := BaseName(id)
		if !strings.HasPrefix(n, "drydock-") || len(n) > 48 || unsafeName.MatchString(n) {
			t.Errorf("BaseName(%q) = %q", id, n)
		}
	}
}

func TestCommandArgsQuoteEveryArgumentAndStayInTheWorktree(t *testing.T) {
	b := &Box{Spec: Spec{Worktree: "/data/wt/task_1"}, Workspace: "drydock-task_1-ws", hasTimeout: true}
	m := &Manager{}
	args, err := m.CommandArgs(b, []string{"npm", "run", "test", "--", "--grep", "it's $HOME; rm -rf /"}, "/data/wt/task_1/web", []string{"CI=1"}, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, "\x00")
	for _, want := range []string{"-w\x00/workspace/web", "DRYDOCK_CMD=npm run test -- --grep 'it'\"'\"'s $HOME; rm -rf /'", "-e\x00CI=1", "drydock-task_1-ws\x00sh\x00-c\x00exec timeout -k 5 90 sh -c \"$DRYDOCK_CMD\""} {
		if !strings.Contains(got, want) {
			t.Errorf("args lack %q:\n%q", want, args)
		}
	}
	if _, err := m.CommandArgs(b, []string{"ls"}, "/data/wt/other", nil, 0); err == nil {
		t.Fatal("a folder outside the worktree is refused")
	}
	if _, err := m.CommandArgs(b, nil, "", nil, 0); err == nil {
		t.Fatal("an empty command is refused")
	}
	if _, err := m.CommandArgs(b, []string{"ls"}, "", []string{"NO VALUE"}, 0); err == nil {
		t.Fatal("a malformed environment entry is refused")
	}
}

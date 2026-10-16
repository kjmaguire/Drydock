package winverify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Facts describe the machine a run happened on. They hold versions and names only: never an environment variable's
// value, a token or a sign-in detail.
type Facts struct {
	OS        string `json:"os"`                  // "Windows 11 Pro", "Ubuntu 24.04.1 LTS"
	OSVersion string `json:"osVersion,omitempty"` // "24H2"
	Build     string `json:"build,omitempty"`     // "26100.2033" (Windows)
	Kernel    string `json:"kernel,omitempty"`    // the kernel release (Linux)
	Arch      string `json:"arch"`
	CPU       string `json:"cpu"`
	Cores     int    `json:"logicalCpus"`
	RAMBytes  uint64 `json:"ramBytes"`
	// Windows only: whether this run is elevated (admin), and two settings some tests depend on.
	Elevated      *bool `json:"elevated,omitempty"`
	DeveloperMode *bool `json:"developerMode,omitempty"` // symbolic links without admin
	LongPaths     *bool `json:"longPathsEnabled,omitempty"`
	// GitAutoCRLF is git's core.autocrlf here ("not set" when unset). The package tests run with false, as CI does; the
	// core smoke runs with this machine's own setting.
	GitAutoCRLF string `json:"gitAutoCrlf,omitempty"`
	Tools       []Tool `json:"tools"`
}

// Tool is one program looked up on PATH.
type Tool struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"` // with the home folder shown as ~
	Version string `json:"version,omitempty"`
	Note    string `json:"note,omitempty"`
}

// toolProbes are the programs reported, with the arguments that print a version.
var toolProbes = []struct {
	name string
	args []string
}{
	{"git", []string{"--version"}},
	{"gh", []string{"--version"}},
	{"node", []string{"--version"}},
	{"npm", []string{"--version"}},
	{"claude", []string{"--version"}},
	{"docker", []string{"--version"}},
	{"go", []string{"version"}},
}

// CollectFacts gathers the facts. Lookups that fail leave a field empty; nothing here fails the run.
func CollectFacts(ctx context.Context) *Facts {
	f := &Facts{Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
	osFacts(f)
	red := newRedactor()
	for _, p := range toolProbes {
		t := Tool{Name: p.name}
		if path, err := exec.LookPath(p.name); err == nil {
			t.Found, t.Path = true, red.text(path)
			if runtime.GOOS == "windows" {
				switch strings.ToLower(filepath.Ext(path)) {
				case ".cmd", ".bat":
					t.Note = "a batch-file launcher (runs through cmd.exe)"
				}
			}
			t.Version = red.text(toolVersion(ctx, path, p.args))
			if p.name == "git" {
				f.GitAutoCRLF = gitAutoCRLF(ctx, path)
			}
		}
		f.Tools = append(f.Tools, t)
	}
	return f
}

func gitAutoCRLF(ctx context.Context, git string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, git, "config", "--get", "core.autocrlf").Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return v
	}
	return "not set"
}

func toolVersion(ctx context.Context, path string, args []string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	v := firstLine(out.String())
	if err != nil && v == "" {
		return "(did not print a version: " + err.Error() + ")"
	}
	if len(v) > 120 {
		v = v[:120]
	}
	return v
}

// OSLine is one line about the system: name, version and build.
func (f *Facts) OSLine() string {
	s := f.OS
	if f.OSVersion != "" {
		s += " " + f.OSVersion
	}
	if f.Build != "" {
		s += " (build " + f.Build + ")"
	}
	if f.Kernel != "" {
		s += " (kernel " + f.Kernel + ")"
	}
	return s + ", " + f.Arch
}

// HardwareLine is one line about the CPU and memory.
func (f *Facts) HardwareLine() string {
	ram := "unknown memory"
	if f.RAMBytes > 0 {
		ram = fmt.Sprintf("%.1f GB RAM", float64(f.RAMBytes)/(1<<30))
	}
	cpu := f.CPU
	if cpu == "" {
		cpu = "unknown CPU"
	}
	return fmt.Sprintf("%s, %d logical CPUs, %s", cpu, f.Cores, ram)
}

// Lines are the facts as label/value pairs for the Markdown report.
func (f *Facts) Lines() [][2]string {
	yes := func(b *bool) string {
		if *b {
			return "yes"
		}
		return "no"
	}
	lines := [][2]string{{"System", f.OSLine()}, {"Hardware", f.HardwareLine()}}
	if f.Elevated != nil {
		lines = append(lines, [2]string{"Running as administrator", yes(f.Elevated)})
	}
	if f.DeveloperMode != nil {
		lines = append(lines, [2]string{"Developer Mode (symbolic links without admin)", yes(f.DeveloperMode)})
	}
	if f.LongPaths != nil {
		lines = append(lines, [2]string{"Long paths enabled", yes(f.LongPaths)})
	}
	if f.GitAutoCRLF != "" {
		lines = append(lines, [2]string{"git core.autocrlf", f.GitAutoCRLF + " (package tests run with false, as CI does; the core smoke uses this setting)"})
	}
	for _, t := range f.Tools {
		v := "not on PATH"
		if t.Found {
			v = t.Version + " (" + t.Path + ")"
			if t.Note != "" {
				v += ", " + t.Note
			}
		}
		lines = append(lines, [2]string{t.Name, v})
	}
	return lines
}

// Has reports whether a tool was found on PATH.
func (f *Facts) Has(name string) bool {
	for _, t := range f.Tools {
		if t.Name == name {
			return t.Found
		}
	}
	return false
}

func factsStep() Step {
	return Step{
		ID:     "facts",
		Title:  "Machine facts",
		Proves: "Records the machine this ran on (system version and build, CPU, memory) and which tools are installed, so every other result can be read in context.",
		Run: func(ctx context.Context, sc *StepContext) Outcome {
			f := CollectFacts(ctx)
			if sc.Env != nil {
				sc.Env.Facts = f
			}
			var b strings.Builder
			for _, kv := range f.Lines() {
				fmt.Fprintf(&b, "%s: %s\n", kv[0], kv[1])
			}
			sc.Logf("%s", b.String())
			return Outcome{Status: Pass, Excerpt: strings.TrimSpace(b.String()), Facts: f}
		},
	}
}

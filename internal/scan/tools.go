package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"runtime/debug"

	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Install hints, shown when a tool is missing. Drydock installs nothing itself.
const (
	osvInstall      = "Install osv-scanner: press Install below, or download osv-scanner_windows_amd64.exe from https://github.com/google/osv-scanner/releases, name it osv-scanner.exe and put it on your PATH (or `go install github.com/google/osv-scanner/v2/cmd/osv-scanner@latest`)."
	opengrepInstall = "Install Opengrep: press Install below, or download opengrep_windows_x86.zip (opengrep_windows_x86.exe before 2.0) from https://github.com/opengrep/opengrep/releases, keep opengrep.exe with its DLLs and put that folder on your PATH (or run the repository's install.ps1)."
)

// gitleaksVersion is the version of the gitleaks rules and engine compiled into Drydock, read from the build's module list.
func gitleaksVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/zricethezav/gitleaks/v8" {
				if d.Replace != nil && d.Replace.Version != "" {
					return d.Replace.Version
				}
				return d.Version
			}
		}
	}
	return "built in"
}

// Paths says where the external tools are, when they are not the ones on PATH (tests and the e2e harness).
type Paths struct {
	OSV      Tool
	Opengrep Tool
	LookPath func(string) (string, error)
}

// Detect reports what this machine has: the tools, their versions, and whether the rules folder is usable. Each version
// probe gets ten seconds.
func Detect(ctx context.Context, pm proc.Manager, env []string, p Paths, rulesDir string) Tools {
	t := Tools{Gitleaks: ToolInfo{Tool: "gitleaks", BuiltIn: true, Found: true, Version: gitleaksVersion()}}
	var wg sync.WaitGroup
	wg.Add(2)
	safe.Go("scan.Detect", func() {
		defer wg.Done()
		t.OSV = detectTool(ctx, pm, env, p.OSV, "osv-scanner", []string{"--version"}, osvInstall, p.LookPath)
	})
	safe.Go("scan.Detect#2", func() {
		defer wg.Done()
		t.Opengrep = detectTool(ctx, pm, env, p.Opengrep, "opengrep", []string{"--version"}, opengrepInstall, p.LookPath)
	})
	wg.Wait()
	t.RulesDir = rulesDir
	t.RulesOK, t.RulesNote = checkRules(rulesDir)
	t.Ready = t.OSV.Found && t.Opengrep.Found && t.RulesOK
	return t
}

func detectTool(ctx context.Context, pm proc.Manager, env []string, tool Tool, name string, versionArgs []string, install string, look func(string) (string, error)) ToolInfo {
	info := ToolInfo{Tool: name, Install: install}
	if tool.Chosen {
		info.Chosen = tool.Path
	}
	path, source, err := tool.resolve(name, look)
	if err != nil {
		return info
	}
	info.Found, info.Path, info.Source = true, path, source
	info.Version = toolVersion(ctx, pm, env, path, append(append([]string(nil), tool.Args...), versionArgs...), name)
	return info
}

// toolVersion runs the tool's --version and keeps the first line (the "osv-scanner version: " prefix dropped), capped.
func toolVersion(ctx context.Context, pm proc.Manager, env []string, path string, args []string, label string) string {
	r := runTool(ctx, pm, path, args, "", env, "scan:"+label+":version", 10*time.Second)
	out := strings.TrimSpace(string(r.Stdout))
	if out == "" {
		out = strings.TrimSpace(string(r.Stderr))
	}
	line := firstLine(out)
	line = strings.TrimPrefix(line, "osv-scanner version: ")
	return cleanLine(line, 60)
}

// checkRules says whether the rules folder can be used by the Code engine.
func checkRules(dir string) (bool, string) {
	if strings.TrimSpace(dir) == "" {
		return false, "No rules folder chosen: the Code engine is skipped until one is."
	}
	if !filepath.IsAbs(dir) {
		// Opengrep runs in the repository and Drydock elsewhere: a relative path would name two different folders
		return false, "The rules folder must be a full path (for example C:\\tools\\opengrep-rules)."
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false, "The rules folder does not exist or is not a folder."
	}
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".yaml" || ext == ".yml" {
			found = true
			return errors.New("found")
		}
		return nil
	})
	if !found {
		return false, "The rules folder has no .yaml or .yml rule files."
	}
	return true, ""
}

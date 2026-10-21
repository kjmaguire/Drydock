package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/agents/pins"
	"github.com/kjmaguire/drydock/internal/connections"
	"github.com/kjmaguire/drydock/internal/contract"
	"github.com/kjmaguire/drydock/internal/proc"
)

// detectCLIs finds each CLI the selected contracts concern. A CLI with a manifest goes through Drydock's own Connections
// detection (the probes the app runs: binary, version, sign-in); an npx bridge is looked up on PATH and asked for --version.
// DRYDOCK_ACP_<NAME>, when set, is what the ACP checks run instead, so it is what is reported.
func detectCLIs(ctx context.Context, names []string) []contract.CLIInfo {
	svc, svcErr := connections.NewService(connections.Config{})
	check := func(info *contract.CLIInfo, adapter string) {
		if svcErr != nil {
			info.Status, info.Detail = "unknown", svcErr.Error()
			return
		}
		c, err := svc.Check(ctx, adapter)
		if err != nil {
			info.Status, info.Detail = "unknown", err.Error()
			return
		}
		info.Version, info.Path, info.Status = c.Version, c.Path, string(c.Status)
		if c.Path != "" {
			info.Source = "path"
		}
		switch {
		case len(c.Problems) > 0:
			info.Detail = strings.Join(c.Problems, "; ")
		case c.AuthDetail != "":
			info.Detail = "signed in (" + c.AuthDetail + ")"
		}
	}
	var out []contract.CLIInfo
	for _, n := range names {
		info := contract.CLIInfo{Name: n}
		tg, isACP := contract.ACPTargetByName(n)
		switch {
		case isACP && os.Getenv(tg.OverrideVar()) != "":
			argv := strings.Fields(os.Getenv(tg.OverrideVar()))
			info.Path, info.Source = strings.Join(argv, " "), "override"
			info.Version, _ = versionOf(ctx, argv...)
		case isACP && tg.Bridge:
			if p, err := exec.LookPath(tg.Bin); err == nil {
				info.Path, info.Source, info.Status = p, "path", "found"
				if v, err := versionOf(ctx, p); err == nil {
					info.Version = v
				} else {
					info.Detail = err.Error()
				}
			} else {
				info.Status, info.Detail = "not installed", tg.Bin+" is not on PATH"
			}
		case isACP:
			check(&info, tg.Adapter)
		default:
			check(&info, n)
		}
		out = append(out, info)
	}
	return out
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// versionOf runs `<argv> --version` and returns the first x.y.z it prints.
func versionOf(ctx context.Context, argv ...string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "--version")...)
	cmd.Env = contract.CleanEnv()
	out, err := cmd.CombinedOutput()
	if v := versionRe.FindString(string(out)); v != "" {
		return v, nil
	}
	if err == nil {
		err = fmt.Errorf("no version in %q", strings.TrimSpace(string(out)))
	}
	return "", fmt.Errorf("%s --version: %w", filepath.Base(argv[0]), err)
}

// pinsFor returns the npm pins the run concerns: every pin without --cli, else those whose program or manifest id was named.
func pinsFor(names, filter []string) ([]pins.Pin, error) {
	all, err := pins.Embedded()
	if err != nil {
		return nil, err
	}
	if len(filter) == 0 {
		return all, nil
	}
	var out []pins.Pin
	for _, p := range all {
		for _, f := range filter {
			if f == p.Bin || f == p.Adapter {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}

// installedVersions maps each pin's program to the version installed here: from detection when the run detected it, else from
// its --version on PATH ("" when it is not installed).
func installedVersions(ctx context.Context, ps []pins.Pin, clis []contract.CLIInfo) map[string]string {
	out := map[string]string{}
	for _, c := range clis {
		if c.Version != "" {
			out[c.Name] = c.Version
		}
	}
	for _, p := range ps {
		if _, done := out[p.Bin]; done {
			continue
		}
		if path, err := exec.LookPath(p.Bin); err == nil {
			if v, err := versionOf(ctx, path); err == nil {
				out[p.Bin] = v
			}
		}
	}
	return out
}

// checkUpdates asks npm for the newest version of each pin, in the order given.
func checkUpdates(ctx context.Context, ps []pins.Pin, installed map[string]string) []contract.Update {
	var out []contract.Update
	for _, p := range ps {
		u := contract.Update{CLI: p.Bin, Package: p.Package, Kind: p.Kind, Pinned: p.Version, Installed: installed[p.Bin]}
		latest, err := pins.Latest(ctx, p.Package)
		if err != nil {
			u.Error = err.Error()
		} else {
			u.Latest = latest
			u.Change, u.Newer = describeChange(u.Pinned, u.Installed, u.Latest)
		}
		out = append(out, u)
	}
	return out
}

// describeChange says in words how the pinned, installed and newest versions relate. newer is true when npm has a version
// newer than what is installed (or than the pin, when nothing is installed): the version --latest would test.
func describeChange(pinned, installed, latest string) (change string, newer bool) {
	lv, okL := manifests.ParseVersion(latest)
	pv, okP := manifests.ParseVersion(pinned)
	iv, okI := manifests.ParseVersion(installed)
	okI = okI && installed != ""
	if !okL || !okP {
		return "cannot compare " + pinned + " with " + latest, false
	}
	var parts []string
	switch {
	case !okI && pv.Less(lv):
		parts, newer = append(parts, fmt.Sprintf("not installed here; %s is newer than the pin", latest)), true
	case !okI:
		parts = append(parts, "not installed here; the pin is the newest release")
	case iv.Less(lv):
		parts, newer = append(parts, fmt.Sprintf("%s is newer than the installed %s", latest, installed)), true
	default:
		parts = append(parts, "the installed version is the newest release")
	}
	if okI && pv.Less(iv) {
		parts = append(parts, fmt.Sprintf("the pin %s is behind the installed %s (move it once the suite passes on %s)", pinned, installed, installed))
	}
	if okI && iv.Less(pv) {
		parts = append(parts, fmt.Sprintf("the installed %s is older than the pin %s", installed, pinned))
	}
	if newer {
		parts = append(parts, "run the suite with --latest before adopting it")
	}
	return strings.Join(parts, "; "), newer
}

// installLatest installs the newest published version of each pin into an npm prefix and returns the folder to put first on
// PATH. A package that fails to install is reported and left out.
func installLatest(ctx context.Context, w io.Writer, prefix string, ps []pins.Pin) (string, error) {
	installed := 0
	for _, p := range ps {
		spec := p.Package + "@latest"
		fmt.Fprintf(w, "--latest: npm install -g --prefix %s %s\n", prefix, spec)
		cmd := exec.CommandContext(ctx, "npm", npmInstallArgs(prefix, p.Package, spec)...)
		cmd.Env = npmEnv(os.Environ())
		var errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = io.Discard, &errb
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(w, "--latest: %s could not be installed: %v: %s\n", spec, err, lastLines(errb.String(), 3))
			continue
		}
		installed++
	}
	if installed == 0 {
		return "", errors.New("--latest: nothing could be installed")
	}
	return npmBin(prefix, runtime.GOOS), nil
}

// npmEnv is the environment npm runs the install scripts of a package in: what a build tool needs to find its toolchain and the
// network (proc.ToolEnv), and npm's own settings, and nothing else. The sign-in secrets of the suite, the job's token and every
// other credential in the caller's environment stay out of code that is downloaded (audit 2026-10-16, M19).
func npmEnv(parent []string) []string {
	names := append(append([]string(nil), proc.BaseEnvNames...), proc.ToolEnvNames...)
	return proc.WithoutProxyCredentials(proc.ScrubEnv(parent, names, []string{"NPM_CONFIG_"}), nil)
}

// npmInstallArgs is `npm install -g` of one package (into prefix, unless it is ""), with that package's install script allowed:
// Claude Code's npm package links its native binary in one, and npm 12 runs no install script unless the command allows it
// (npm 11 accepts the flag too).
func npmInstallArgs(prefix, pkg, spec string) []string {
	args := []string{"install", "-g"}
	if prefix != "" {
		args = append(args, "--prefix", prefix)
	}
	return append(args, "--no-audit", "--no-fund", "--allow-scripts="+pkg, spec)
}

// npmBin is where `npm install -g --prefix` puts the commands: <prefix>\ on Windows, <prefix>/bin elsewhere.
func npmBin(prefix, goos string) string {
	if goos == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

func printUpdates(w io.Writer, us []contract.Update) {
	fmt.Fprintln(w, "Version watch (npm registry):")
	for _, u := range us {
		installed := u.Installed
		if installed == "" {
			installed = "-"
		}
		latest, change := u.Latest, u.Change
		if u.Error != "" {
			latest, change = "?", "lookup failed: "+u.Error
		}
		fmt.Fprintf(w, "  %-17s %-39s pinned %-8s installed %-8s latest %-8s %s\n", u.CLI, u.Package, u.Pinned, installed, latest, change)
	}
}

func printCLIs(w io.Writer, cs []contract.CLIInfo) {
	fmt.Fprintln(w, "CLIs:")
	for _, c := range cs {
		v := c.Version
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(w, "  %-17s %-9s %s %s %s\n", c.Name, v, c.Status, c.Path, c.Detail)
	}
}

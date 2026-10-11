package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/proc"
)

// DepsOptions configures the dependency check.
type DepsOptions struct {
	// Binary is the osv-scanner executable (default "osv-scanner", looked up on PATH).
	Binary string
	// Offline passes --offline: osv-scanner then uses only a local vulnerability database and sends no package metadata to
	// osv.dev. It needs databases downloaded beforehand (osv-scanner --download-offline-databases); without them osv-scanner
	// fails and the check reports the failure as Errored, never as a pass.
	Offline bool
	Timeout time.Duration
	// LookPath overrides exec.LookPath (tests).
	LookPath func(file string) (string, error)
}

// osvReport is the part of osv-scanner's JSON output (--format json) the check reads. The shape follows the osv-scanner
// documentation (results[].packages[].vulnerabilities[]); this repository has not captured output from a real run
// because osv-scanner is not installed in the development sandbox. Unknown fields are ignored.
type osvReport struct {
	Results []struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Vulnerabilities []struct {
				ID string `json:"id"`
			} `json:"vulnerabilities"`
		} `json:"packages"`
	} `json:"results"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// DepsFinding is one vulnerable package. Only identifiers are kept: advisory summaries come from an external database and
// are untrusted text that must not reach an agent's prompt.
type DepsFinding struct {
	Ecosystem string   `json:"ecosystem"`
	Package   string   `json:"package"`
	Version   string   `json:"version"`
	IDs       []string `json:"ids"`
	Source    string   `json:"source"`
}

// ParseOSV parses osv-scanner JSON into findings.
func ParseOSV(data []byte) ([]DepsFinding, error) {
	var rep osvReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("osv-scanner output is not the expected JSON: %w", err)
	}
	var out []DepsFinding
	for _, r := range rep.Results {
		for _, p := range r.Packages {
			if len(p.Vulnerabilities) == 0 {
				continue
			}
			f := DepsFinding{Ecosystem: clean(p.Package.Ecosystem, 40), Package: clean(p.Package.Name, 120), Version: clean(p.Package.Version, 60), Source: clean(r.Source.Path, 200)}
			seen := map[string]bool{}
			for _, v := range p.Vulnerabilities {
				if safeID.MatchString(v.ID) && !seen[v.ID] {
					seen[v.ID] = true
					f.IDs = append(f.IDs, v.ID)
				}
			}
			sort.Strings(f.IDs)
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func clean(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// DepsSpec is the `deps` check: osv-scanner as an external tool. If it is not installed the result is Skipped with the
// reason "osv-scanner not installed": never a pass. Exit codes follow osv-scanner: 0 no vulnerabilities, 1 vulnerabilities
// found (parsed from the JSON), 128 no supported lockfile or manifest (skipped: nothing was checked); anything else is an
// Errored result with the tool's own message.
func DepsSpec(dir string, o DepsOptions) Spec {
	bin := o.Binary
	if bin == "" {
		bin = "osv-scanner"
	}
	return Spec{Name: "deps", Argv: []string{bin, "scan", "source", "--format", "json", "-r", "."}, Dir: dir, Timeout: o.Timeout, InProcess: func(ctx context.Context, pm proc.Manager, s Spec) (Result, error) {
		return runDeps(ctx, pm, s, bin, o)
	}}
}

func runDeps(ctx context.Context, pm proc.Manager, s Spec, bin string, o DepsOptions) (Result, error) {
	var res Result
	look := o.LookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look(bin)
	if err != nil {
		res.Skipped, res.SkipReason = true, "osv-scanner not installed"
		res.Output = "skipped: osv-scanner not installed. The dependency check did not run, so no dependency was checked. Install osv-scanner (https://github.com/google/osv-scanner) or turn this check off for the project."
		return res, nil
	}
	args := []string{"scan", "source", "--format", "json", "-r"}
	if o.Offline {
		args = append(args, "--offline")
	}
	args = append(args, ".")
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := pm.Start(cctx, proc.Spec{Path: path, Args: args, Dir: s.Dir, Env: append(commandEnv(), "NO_COLOR=1"), Label: "check:deps"})
	if err != nil {
		res.Errored = true
		res.Output = "osv-scanner could not be started: " + err.Error()
		return res, nil
	}
	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&stdout, io.LimitReader(p.Stdout(), 32<<20)) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&stderr, io.LimitReader(p.Stderr(), 1<<20)) }()
	_ = p.Stdin().Close()
	var ei proc.ExitInfo
	select {
	case ei = <-p.Wait():
	case <-cctx.Done():
		_ = p.Kill(true)
		ei = <-p.Wait()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	res.ExitCode = ei.Code
	if cctx.Err() != nil {
		res.Errored = true
		res.Output = fmt.Sprintf("osv-scanner did not finish within %s", timeout)
		return res, nil
	}
	switch ei.Code {
	case 0:
		res.Passed = true
		res.Output = "osv-scanner reported no known vulnerabilities in the supported lockfiles and manifests"
	case 1:
		fs, perr := ParseOSV(stdout.Bytes())
		if perr != nil || len(fs) == 0 {
			res.Errored = true
			res.Output = "osv-scanner exited 1 (vulnerabilities found) but its JSON could not be read: " + errText(perr)
			return res, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d vulnerable package(s). Upgrade or replace them; advisory ids:\n", len(fs))
		for _, f := range fs {
			fmt.Fprintf(&sb, "%s %s@%s: %s\n", f.Ecosystem, f.Package, f.Version, strings.Join(f.IDs, ", "))
			res.FailingTests = append(res.FailingTests, fmt.Sprintf("%s@%s", f.Package, f.Version))
		}
		res.Output = sb.String()
	case 128:
		res.Skipped, res.SkipReason = true, "osv-scanner found no supported lockfile or manifest"
		res.Output = "skipped: osv-scanner found no supported lockfile or manifest, so no dependency was checked"
	default:
		res.Errored = true
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 1500 {
			msg = msg[:1500] + "..."
		}
		hint := ""
		if o.Offline {
			hint = " (offline mode needs a local database: run osv-scanner with --download-offline-databases once)"
		}
		res.Output = fmt.Sprintf("osv-scanner failed with exit code %d%s: %s", ei.Code, hint, msg)
	}
	return res, nil
}

func errText(err error) string {
	if err == nil {
		return "no vulnerabilities parsed"
	}
	return err.Error()
}

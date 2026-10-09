package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var latestGo string // set by goToolchain, used for the Go versions CI pins

var goDirectiveR = regexp.MustCompile(`(?m)^go\s+(\S+)`)

// goToolchain: the version go.mod requires, the one installed here, and the newest stable release (from the Go module proxy,
// which publishes every toolchain as golang.org/toolchain).
func goToolchain(ctx context.Context) []Item {
	var items []Item
	mod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	used := ""
	if m := goDirectiveR.FindSubmatch(mod); m != nil {
		used = string(m[1])
	}
	latest, latestSeries := "", ""
	out, err := run(ctx, root, nil, "go", "list", "-m", "-versions", "golang.org/toolchain")
	if err != nil {
		fail("go toolchain versions: " + err.Error())
	} else {
		var all []string
		for _, f := range strings.Fields(out) {
			// the proxy lists toolchains as v0.0.1-go1.27.2.linux-amd64
			if i := strings.Index(f, "-go"); i >= 0 && strings.HasSuffix(f, ".linux-amd64") {
				all = append(all, strings.TrimSuffix(f[i+1:], ".linux-amd64"))
			}
		}
		sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
		for i := len(all) - 1; i >= 0; i-- {
			if _, _, _, pre, ok := parseSemver(all[i]); ok && !pre {
				latest = strings.TrimPrefix(all[i], "go")
				break
			}
		}
		// the newest patch of the series Drydock is on
		um, un, _, _, _ := parseSemver(used)
		for i := len(all) - 1; i >= 0; i-- {
			m, n, _, pre, ok := parseSemver(all[i])
			if ok && !pre && m == um && n == un {
				latestSeries = strings.TrimPrefix(all[i], "go")
				break
			}
		}
	}
	latestGo = latest
	it := Item{ID: "go", Group: "Language and toolchain", Name: "Go", Kind: "runtime", Used: used, Latest: latest, Where: "go.mod (go directive)"}
	if latestSeries != "" && latestSeries != used {
		it.Note = "Newest patch of the " + used[:strings.LastIndex(used, ".")] + " series: " + latestSeries + "."
	}
	items = append(items, it)
	if v, err := run(ctx, root, nil, "go", "env", "GOVERSION"); err == nil {
		items = append(items, Item{ID: "go-installed", Group: "Language and toolchain", Name: "Go (installed in this sandbox)", Kind: "runtime", Used: strings.TrimPrefix(strings.TrimSpace(v), "go"), Latest: latest, Where: "this machine"})
	}
	return items
}

func less(a, b string) bool {
	am, an, ap, _, _ := parseSemver(a)
	bm, bn, bp, _, _ := parseSemver(b)
	if am != bm {
		return am < bm
	}
	if an != bn {
		return an < bn
	}
	return ap < bp
}

type listedModule struct {
	Path       string
	Version    string
	Time       string
	Indirect   bool
	Main       bool
	Deprecated string
	Update     *struct {
		Version string
		Time    string
	}
	Replace *struct{ Path, Version string }
}

// goModules lists every module in the build list with the newest version the module proxy offers.
func goModules(ctx context.Context) []Item {
	out, err := run(ctx, root, nil, "go", "list", "-m", "-u", "-json", "all")
	if err != nil {
		fail("go list -m -u: " + err.Error())
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var items []Item
	for dec.More() {
		var m listedModule
		if err := dec.Decode(&m); err != nil {
			fail("go list output: " + err.Error())
			break
		}
		if m.Main {
			continue
		}
		it := Item{ID: "go:" + m.Path, Name: m.Path, Used: strings.TrimPrefix(m.Version, "v"), Where: "go.mod", Deprecated: m.Deprecated, UsedDate: day(m.Time)}
		if m.Indirect {
			it.Kind, it.Group = "indirect", "Go libraries pulled in by other libraries"
		} else {
			it.Kind, it.Group = "direct", "Go libraries Drydock imports"
		}
		if m.Update != nil {
			it.Latest, it.LatestDate = strings.TrimPrefix(m.Update.Version, "v"), day(m.Update.Time)
		} else {
			it.Latest = it.Used
		}
		if strings.Contains(it.Used, "-0.") || regexp.MustCompile(`-\d{14}-[0-9a-f]{12}$`).MatchString(it.Used) {
			// a pseudo-version: a commit, not a release
			it.Note = strings.TrimSpace(it.Note + " Pinned to a commit, not a release.")
			if m.Update == nil {
				it.Status = "current"
			} else {
				it.Status = "unknown"
				if it.Latest != "" {
					it.Status = "patch"
				}
			}
		}
		items = append(items, it)
	}
	return items
}

// markGoUsage counts, for every direct Go library, the product packages that import it, on both the Linux and the Windows
// build (Wails and the Windows APIs are only compiled on Windows). internal/tools/deps.go only pins versions, so it does not count.
func markGoUsage(ctx context.Context, items []Item) {
	imports := map[string]map[string]bool{} // package -> imports
	for _, goos := range []string{"linux", "windows"} {
		out, err := run(ctx, root, []string{"GOOS=" + goos}, "go", "list", "-e", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
		if err != nil {
			fail("go list imports (" + goos + "): " + err.Error())
			return
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || strings.HasSuffix(f[0], "/internal/tools") {
				continue
			}
			if imports[f[0]] == nil {
				imports[f[0]] = map[string]bool{}
			}
			for _, imp := range f[1:] {
				imports[f[0]][imp] = true
			}
		}
	}
	// every package compiled into the product or its tests, transitively
	built := map[string]bool{}
	for _, goos := range []string{"linux", "windows"} {
		out, err := run(ctx, root, []string{"GOOS=" + goos}, "go", "list", "-e", "-deps", "-test", "-f", "{{.ImportPath}}", "./...")
		if err != nil {
			fail("go list -deps (" + goos + "): " + err.Error())
			break
		}
		for _, l := range strings.Fields(out) {
			built[l] = true
		}
	}
	for i := range items {
		it := &items[i]
		if strings.HasPrefix(it.ID, "go:") && len(built) > 0 {
			mod := strings.TrimPrefix(it.ID, "go:")
			in := false
			for p := range built {
				if p == mod || strings.HasPrefix(p, mod+"/") {
					in = true
					break
				}
			}
			it.InBuild = &in
		}
		if it.Kind != "direct" || !strings.HasPrefix(it.ID, "go:") {
			continue
		}
		mod := strings.TrimPrefix(it.ID, "go:")
		n := 0
		for _, imps := range imports {
			for imp := range imps {
				if imp == mod || strings.HasPrefix(imp, mod+"/") {
					n++
					break
				}
			}
		}
		it.UsedBy = &n
	}
}

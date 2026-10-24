// Command drydock-winident checks that a built drydock.exe carries Drydock's Windows identity (docs/windows-identity.md): the
// icon group Wails and Explorer use with every size, the version resource with the build's version, and the application
// manifest. CI's app job runs it on the exe it built:
//
//	go run ./cmd/drydock-winident -exe drydock.exe [-version 1.2.3]
//
// Without -version the version is the one build/windows/winres.json carries (the default of a build given none). It exits 1
// and lists every problem when anything is missing or wrong.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/kjmaguire/drydock/internal/winident"
)

func main() {
	exe := flag.String("exe", "drydock.exe", "the executable to check")
	want := flag.String("version", "", "the version the build was given (default: the definition's)")
	root := flag.String("root", ".", "the repository root (where build/windows/winres.json is)")
	flag.Parse()
	if err := run(*exe, *want, *root); err != nil {
		fmt.Fprintln(os.Stderr, "drydock-winident:", err)
		os.Exit(1)
	}
}

func run(exe, want, root string) error {
	if want == "" {
		d, err := winident.LoadDefinition(root)
		if err != nil {
			return err
		}
		want = d.DefaultVersion()
	}
	f, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := winident.CheckEXE(f, want)
	if err != nil {
		return fmt.Errorf("%s: %w", exe, err)
	}
	fmt.Printf("%s\n  first icon group: %s\n", exe, r.IconGroup)
	for _, e := range r.Icon {
		kind := "BMP"
		if e.PNG {
			kind = "PNG"
		}
		fmt.Printf("    %3d px, %d-bit %s\n", e.Size, e.BitCount, kind)
	}
	fmt.Printf("  version: %d.%d.%d.%d\n", r.Fixed[0], r.Fixed[1], r.Fixed[2], r.Fixed[3])
	keys := make([]string, 0, len(r.Strings))
	for k := range r.Strings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %s: %s\n", k, r.Strings[k])
	}
	fmt.Printf("  manifest: %v\n", r.Manifest)
	if len(r.Problems) > 0 {
		for _, p := range r.Problems {
			fmt.Println("  PROBLEM:", p)
		}
		return fmt.Errorf("%s does not carry Drydock's Windows identity (%d problems)", exe, len(r.Problems))
	}
	fmt.Println("  ok: icon, version", want, "and manifest are all there")
	return nil
}

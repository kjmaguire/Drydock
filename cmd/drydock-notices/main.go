// Command drydock-notices rewrites the generated parts of THIRD_PARTY_NOTICES.md (the Go modules compiled into Drydock, the ones
// under the MPL-2.0, and the UI packages) from docs/software-versions.json, ui/package-lock.json and the Go module cache. Run it
// after the Software Versions page has been refreshed (docs/software-versions.md, "The rule"):
//
//	go run ./cmd/drydock-notices          # rewrites THIRD_PARTY_NOTICES.md
//	go run ./cmd/drydock-notices -check   # changes nothing; exits 1 if the file is out of date
//
// It reads license files; it runs nothing it reads. Needs the modules in the cache (go mod download).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/notices"
)

func main() {
	check := flag.Bool("check", false, "change nothing; exit 1 if THIRD_PARTY_NOTICES.md is out of date")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "drydock-notices:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	read := func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) }
	var src notices.Source
	for rel, dst := range map[string]*[]byte{"docs/software-versions.json": &src.VersionsJSON, "ui/package-lock.json": &src.LockJSON, "ui/package.json": &src.PackageJSON} {
		if *dst, err = read(rel); err != nil {
			return err
		}
	}
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return fmt.Errorf("go env GOMODCACHE: %w", err)
	}
	src.ModCache = strings.TrimSpace(string(out))
	raw, err := read("THIRD_PARTY_NOTICES.md")
	if err != nil {
		return err
	}
	// a checkout on Windows may have turned the line ends into CRLF (core.autocrlf): the file is judged, and written, as git stores it
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	got, err := notices.Generate(doc, src)
	if err != nil {
		return err
	}
	if got == doc {
		fmt.Println("THIRD_PARTY_NOTICES.md is up to date")
		return nil
	}
	if check {
		return fmt.Errorf("THIRD_PARTY_NOTICES.md is out of date: run go run ./cmd/drydock-notices")
	}
	if err := os.WriteFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"), []byte(got), 0o644); err != nil {
		return err
	}
	fmt.Println("THIRD_PARTY_NOTICES.md rewritten")
	return nil
}

func repoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "cmd", "drydock-notices")); err == nil {
				return d, nil
			}
		}
		p := filepath.Dir(d)
		if p == d {
			return "", fmt.Errorf("run drydock-notices inside the Drydock repository")
		}
		d = p
	}
}

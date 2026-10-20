// Package bundle reads the tools a Drydock download carries beside drydock.exe (decision P77, on the owner's words of
// 2026-10-20: "I want the app to already have the dependencies installed for OSV-Scanner and OpenGrep", "I want [llama.cpp]
// already installed with Google's TurboQuant built in"). The download's tools folder holds each tool's release file, exactly as
// its GitHub release published it, and bundle.json, which names each file with its release, the address it came from and the
// SHA-256 GitHub recorded for it. At start each installer takes its tool from here instead of the network: Stage copies the
// file into the installer's download cache only when its SHA-256 matches, and the installer's ordinary install (which checks the
// digest again) does the rest. Nothing in the folder is run from there.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/runners"
)

// FileName is the manifest's name inside the tools folder.
const FileName = "bundle.json"

// Manifest is bundle.json: what the download carries.
type Manifest struct {
	// Version of the manifest's format (1).
	Version int `json:"version"`
	// Built is when the bundle was made (RFC 3339), for the page.
	Built string `json:"built,omitempty"`
	// Scan are the Security page's external tools (osv-scanner, Opengrep).
	Scan []ScanTool `json:"scan"`
	// Llama is the llama.cpp build for "Models on this PC", when the download carries one.
	Llama *LlamaBuild `json:"llama,omitempty"`
}

// ScanTool is one bundled scan tool: its release and the release file.
type ScanTool struct {
	Tool  string        `json:"tool"`
	Tag   string        `json:"tag"`
	Asset runners.Asset `json:"asset"`
}

// LlamaBuild is the bundled llama.cpp build: its source (official or the TurboQuant fork), release, backend and archive.
type LlamaBuild struct {
	Source  string        `json:"source"`
	Tag     string        `json:"tag"`
	Backend string        `json:"backend"`
	Archive runners.Asset `json:"archive"`
}

// Read reads dir/bundle.json. A folder without one is not an error: it returns nil (a download without tools, or a
// development build).
func Read(dir string) (*Manifest, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("%s: format %d is not one this version reads", FileName, m.Version)
	}
	return &m, nil
}

// Stage copies the bundled release file a into cacheDir, under its own name, when it is in dir, its name is a plain file
// name, it came from prefix (the tool's own release downloads), and its SHA-256 is the one recorded. The installer's download
// then finds it in its cache and fetches nothing. It returns where the file now is.
func Stage(dir string, a runners.Asset, prefix, cacheDir string) (string, error) {
	if a.Name == "" || a.Name == "." || a.Name == ".." || strings.ContainsAny(a.Name, `/\:`) {
		return "", fmt.Errorf("bad file name %q in %s", a.Name, FileName)
	}
	if !strings.HasPrefix(a.URL, prefix) {
		return "", fmt.Errorf("%s did not come from %s", a.Name, prefix)
	}
	if len(a.SHA256) != 64 {
		return "", fmt.Errorf("%s has no SHA-256 in %s", a.Name, FileName)
	}
	src := filepath.Join(dir, a.Name)
	sum, err := fileSHA256(src)
	if err != nil {
		return "", err
	}
	if sum != strings.ToLower(a.SHA256) {
		return "", fmt.Errorf("%s does not match the SHA-256 in %s", a.Name, FileName)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(cacheDir, a.Name)
	if got, err := fileSHA256(dst); err == nil && got == sum {
		return dst, nil
	}
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".bundle-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Dir is the tools folder of the running executable (the folder beside drydock.exe), or "" when the executable cannot be found.
func Dir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return filepath.Join(filepath.Dir(exe), "tools")
}

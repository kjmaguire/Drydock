// Package supportbundle builds the local support .zip (docs/packaging.md section 9). A bundle is assembled from an explicit
// allow-list of named inputs: nothing is added by globbing a directory. Every text file passes through the same pipeline:
// the user profile path, machine name and user name become tokens, the secret scanner redacts what it finds, and a file
// whose scan fails is refused. The bundle is never uploaded; the human reviews the file list and the scrub report first.
package supportbundle

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/scrub"
)

// Notice is shown with every bundle.
const Notice = "Scrubbing reduces but does not eliminate leakage of proprietary code. Review the file list and send the file yourself; Drydock never uploads it."

// Connection is one Connection with its detected CLI version and compatibility verdict.
type Connection struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	State   string `json:"state,omitempty"`
	Version string `json:"cliVersion,omitempty"`
	Verdict string `json:"compatibility,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// EventRef is an event reduced to what a person debugging needs: type, sequence, time and ids. There is no payload field,
// so no code path can put one in.
type EventRef struct {
	Seq       uint64 `json:"seq"`
	Type      string `json:"type"`
	TS        int64  `json:"ts"`
	ProjectID string `json:"projectId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
}

// WatchdogSignature is a normalized circuit-breaker trip: the signal and stage with a count, no task content.
type WatchdogSignature struct {
	Signal string `json:"signal"`
	Stage  int    `json:"stage"`
	Count  int    `json:"count"`
	Last   int64  `json:"lastTs"`
}

// RunEvent is one event WITH its payload, used only when the human opts in to include a run.
type RunEvent struct {
	Seq  uint64 `json:"seq"`
	Type string `json:"type"`
	TS   int64  `json:"ts"`
	Data string `json:"data"`
}

// RunFile is one raw output file of an included run.
type RunFile struct {
	Name    string
	Content string
}

// RunInclusion is the opt-in addition of one run.
type RunInclusion struct {
	RunID  string
	Events []RunEvent
	Files  []RunFile
}

// Inputs is everything a bundle can contain. The builder maps each field to a fixed file name.
type Inputs struct {
	AppVersion    string
	Build         string
	OS            string
	OSVersion     string
	WebView2      string
	InstallScope  string
	SchemaVersion int64
	Tools         map[string]string // detected Node, Git and gh versions
	Connections   []Connection
	Conformance   map[string]string // last conformance results per adapter
	Settings      map[string]string // raw settings; secrets are removed by the builder
	Logs          []LogFile
	Events        []EventRef
	Watchdog      []WatchdogSignature
	DiskPath      string
	DiskFree      uint64
	DiskTotal     uint64
	EnvNames      []string
	Run           *RunInclusion
}

// LogFile is one application log file's text.
type LogFile struct {
	Name string // base name, for example drydock.log.1
	Text string
}

// Options configures scrubbing.
type Options struct {
	Scanner  scrub.Scanner
	Identity *scrub.Identity
	Now      func() time.Time
}

// File is one bundle file.
type File struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Data []byte `json:"-"`
}

// FileReport is the scrub result for one file.
type FileReport struct {
	Name     string         `json:"name"`
	Secrets  int            `json:"secretsRedacted"`
	ByRule   map[string]int `json:"byRule,omitempty"`
	Identity map[string]int `json:"identityTokens,omitempty"`
}

// Refusal names a file that was left out and why.
type Refusal struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Report is the scrub report shown before the bundle is saved and stored inside it.
type Report struct {
	Files        []FileReport   `json:"files"`
	TotalSecrets int            `json:"totalSecretsRedacted"`
	ByRule       map[string]int `json:"byRule,omitempty"`
	Refused      []Refusal      `json:"refused,omitempty"`
	RunIncluded  string         `json:"runIncluded,omitempty"`
	Notice       string         `json:"notice"`
}

// Bundle is a built, scrubbed bundle.
type Bundle struct {
	Files  []File `json:"files"`
	Report Report `json:"report"`
}

// Manifest is bundle/manifest.json.
type Manifest struct {
	Created     int64       `json:"created"`
	AppVersion  string      `json:"appVersion"`
	RunIncluded string      `json:"runIncluded,omitempty"`
	Files       []FileEntry `json:"files"`
}

// FileEntry lists one bundle file.
type FileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type builder struct {
	ctx   context.Context
	opt   Options
	files []File
	rep   Report
}

// Build assembles the bundle in memory and scrubs it. Nothing is written.
func Build(ctx context.Context, in Inputs, opt Options) (Bundle, error) {
	if opt.Scanner == nil {
		return Bundle{}, errors.New("supportbundle: a secret scanner is required")
	}
	b := &builder{ctx: ctx, opt: opt}
	b.rep.ByRule = map[string]int{}
	b.rep.Notice = Notice

	b.json("summary.json", map[string]any{
		"app":   map[string]any{"version": in.AppVersion, "build": in.Build, "installScope": in.InstallScope, "databaseSchemaVersion": in.SchemaVersion},
		"os":    map[string]any{"name": in.OS, "version": in.OSVersion, "webview2": in.WebView2},
		"tools": in.Tools,
		"disk":  map[string]any{"path": in.DiskPath, "freeBytes": in.DiskFree, "totalBytes": in.DiskTotal},
	})
	b.json("connections.json", map[string]any{"connections": nonNilConns(in.Connections), "conformance": in.Conformance})
	b.json("settings.json", cleanSettings(in.Settings))
	for _, l := range in.Logs {
		name := filepath.Base(l.Name)
		if !strings.HasPrefix(name, "drydock.log") {
			b.rep.Refused = append(b.rep.Refused, Refusal{Name: "logs/" + name, Reason: "not an application log file"})
			continue
		}
		b.text("logs/"+name, l.Text)
	}
	b.json("events.json", map[string]any{"note": "type, sequence, time and ids only; no payloads", "events": nonNilEvents(in.Events)})
	b.json("watchdog.json", map[string]any{"signatures": nonNilWatchdog(in.Watchdog)})
	names := append([]string(nil), in.EnvNames...)
	sort.Strings(names)
	b.json("environment.json", map[string]any{"note": "variable names only; values are never read into the bundle", "names": names})
	if in.Run != nil {
		if err := b.run(in.Run); err != nil {
			return Bundle{}, err
		}
		b.rep.RunIncluded = in.Run.RunID
	}
	if b.ctx.Err() != nil {
		return Bundle{}, b.ctx.Err()
	}

	// the report and the manifest describe everything above (they contain counts and names, never content)
	sort.Slice(b.rep.Files, func(i, j int) bool { return b.rep.Files[i].Name < b.rep.Files[j].Name })
	if len(b.rep.ByRule) == 0 {
		b.rep.ByRule = nil
	}
	rb, _ := json.MarshalIndent(b.rep, "", "  ")
	b.files = append(b.files, File{Name: "scrub-report.json", Size: int64(len(rb)), Data: rb})
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	m := Manifest{Created: now().UTC().UnixMilli(), AppVersion: in.AppVersion, RunIncluded: b.rep.RunIncluded}
	for _, f := range b.files {
		sum := sha256.Sum256(f.Data)
		m.Files = append(m.Files, FileEntry{Name: f.Name, Size: f.Size, SHA256: hex.EncodeToString(sum[:])})
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	b.files = append(b.files, File{Name: "manifest.json", Size: int64(len(mb)), Data: mb})
	sort.SliceStable(b.files, func(i, j int) bool { return b.files[i].Name < b.files[j].Name })
	return Bundle{Files: b.files, Report: b.rep}, nil
}

func nonNilConns(c []Connection) []Connection {
	if c == nil {
		return []Connection{}
	}
	return c
}
func nonNilEvents(e []EventRef) []EventRef {
	if e == nil {
		return []EventRef{}
	}
	return e
}
func nonNilWatchdog(w []WatchdogSignature) []WatchdogSignature {
	if w == nil {
		return []WatchdogSignature{}
	}
	return w
}

// cleanSettings removes secrets from settings: a value under a secret-looking name is replaced outright (the text scanner
// then handles values that carry secrets under harmless names).
func cleanSettings(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if scrub.SensitiveName(k) {
			out[k] = "[REDACTED:secret-name]"
			continue
		}
		out[k] = v
	}
	return out
}

func (b *builder) json(name string, v any) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b.rep.Refused = append(b.rep.Refused, Refusal{Name: name, Reason: "could not be encoded: " + err.Error()})
		return
	}
	b.text(name, string(raw))
}

// text runs one file through the pipeline and adds it, or refuses it.
func (b *builder) text(name, s string) {
	fr := FileReport{Name: name}
	s, ident := b.opt.Identity.ApplyCount(s)
	if len(ident) > 0 {
		fr.Identity = ident
	}
	out, fs, err := scrub.Text(b.ctx, b.opt.Scanner, s)
	if err != nil {
		b.rep.Refused = append(b.rep.Refused, Refusal{Name: name, Reason: "the secret scan could not complete (" + err.Error() + "), so the file is not included"})
		return
	}
	if len(fs) > 0 {
		fr.Secrets = len(fs)
		fr.ByRule = scrub.Rules(fs)
		b.rep.TotalSecrets += len(fs)
		for r, n := range fr.ByRule {
			b.rep.ByRule[r] += n
		}
	}
	// the redacted text must scan clean: a mask that exposed an adjacent secret would otherwise slip through
	if len(fs) > 0 {
		if _, left, err := scrub.Text(b.ctx, b.opt.Scanner, out); err != nil || len(left) > 0 {
			b.rep.Refused = append(b.rep.Refused, Refusal{Name: name, Reason: "the file still failed the secret scan after redaction, so it is not included"})
			b.rep.TotalSecrets -= fr.Secrets
			for r, n := range fr.ByRule {
				b.rep.ByRule[r] -= n
			}
			return
		}
	}
	b.files = append(b.files, File{Name: name, Size: int64(len(out)), Data: []byte(out)})
	b.rep.Files = append(b.rep.Files, fr)
}

func (b *builder) run(r *RunInclusion) error {
	if r.RunID == "" || strings.ContainsAny(r.RunID, `/\`) {
		return errors.New("supportbundle: invalid run id")
	}
	base := "run/" + r.RunID + "/"
	raw, _ := json.MarshalIndent(r.Events, "", "  ")
	b.text(base+"events-with-payloads.json", string(raw))
	for _, f := range r.Files {
		n := filepath.ToSlash(filepath.Clean(f.Name))
		if n == "." || strings.HasPrefix(n, "../") || strings.HasPrefix(n, "/") || strings.Contains(n, ":") {
			b.rep.Refused = append(b.rep.Refused, Refusal{Name: base + f.Name, Reason: "unsafe file name"})
			continue
		}
		b.text(base+"files/"+n, f.Content)
	}
	return nil
}

// Write saves the bundle as a zip at path (written to a temporary name and renamed).
func Write(b Bundle, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, bf := range b.Files {
		w, err := zw.Create(bf.Name)
		if err == nil {
			_, err = w.Write(bf.Data)
		}
		if err != nil {
			zw.Close()
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write %s: %w", bf.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

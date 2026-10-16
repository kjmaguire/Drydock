// Package notices writes the third-party part of THIRD_PARTY_NOTICES.md: every Go module compiled into Drydock and every npm
// package the UI ships with, with the license it is under, so the file says what the build holds instead of what somebody once
// typed (audit 2026-10-16, L57: it was seeded by hand on 2026-10-08, named old versions and left out the MPL-2.0 modules).
//
// The lists come from files that are already measured and committed: docs/software-versions.json (cmd/drydock-audit says which Go
// modules are in the build and at which version) and ui/package-lock.json (which names every package's license). A Go module's
// license is read from its license files in the module cache; the text is read, never run. cmd/drydock-notices does the work.
package notices

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Row is one line of a table.
type Row struct {
	Name, Version, License, Copyright, Role string
	Direct                                  bool
}

// MPL reports whether the module's own license is the Mozilla Public License 2.0 (a file-level copyleft: its source must be
// available to whoever gets the program).
func (r Row) MPL() bool { return strings.HasPrefix(r.License, "MPL-2.0") }

// licenseRules find a license in the text of a license file. They run on the text lower-cased with white space collapsed, and each
// reports where its evidence first appears: the license a file starts with is the one it grants, the rest are code it includes.
var licenseRules = []struct {
	id string
	re *regexp.Regexp
}{
	{"MPL-2.0", regexp.MustCompile(`mozilla public license,? (?:version |v\.? ?)2\.0`)},
	{"Apache-2.0", regexp.MustCompile(`apache license,? version 2\.0`)},
	{"MIT", regexp.MustCompile(`permission is hereby granted, free of charge,`)},
	{"BSD", regexp.MustCompile(`redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met`)},
	{"ISC", regexp.MustCompile(`permission to use, copy, modify,? (?:and/or|and) distribute this software for any purpose with or without fee`)},
	{"Unlicense", regexp.MustCompile(`this is free and unencumbered software released into the public domain`)},
	{"CC0-1.0", regexp.MustCompile(`creative commons legal code cc0 1\.0 universal`)},
}

var (
	spaceRe     = regexp.MustCompile(`\s+`)
	gnuHeaderRe = regexp.MustCompile(`^(?:\W|\d)*gnu (affero |lesser |library )?general public license`)
	bsdNameRe   = regexp.MustCompile(`neither the name|the names of|endorse or promote`)
)

// Detect returns the licenses the text of one license file grants, the one it starts with first. A copyleft license is found
// only when the file begins with it: the MPL's own text names the GPL as a secondary license, and that is not the module's.
func Detect(text string) []string {
	s := strings.TrimSpace(spaceRe.ReplaceAllString(strings.ToLower(text), " "))
	if m := gnuHeaderRe.FindStringSubmatch(s); m != nil {
		kind := strings.ToUpper(strings.TrimSpace(m[1]))
		switch kind {
		case "AFFERO":
			return []string{"AGPL"}
		case "LESSER", "LIBRARY":
			return []string{"LGPL"}
		}
		return []string{"GPL"}
	}
	type hit struct {
		pos int
		id  string
	}
	var hits []hit
	for _, r := range licenseRules {
		for _, loc := range r.re.FindAllStringIndex(s, -1) {
			id := r.id
			if id == "BSD" {
				id = "BSD-2-Clause"
				if bsdNameRe.MatchString(s[loc[1]:min(len(s), loc[1]+1500)]) {
					id = "BSD-3-Clause"
				}
			}
			hits = append(hits, hit{loc[0], id})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	var out []string
	for _, h := range hits {
		if !contains(out, h.id) {
			out = append(out, h.id)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

var copyrightRe = regexp.MustCompile(`(?m)^[ \t*#/]*((?:Copyright|COPYRIGHT)[ \t]+(?:\([cC]\)|©|\d{4})[^\n]*)$`)

// Copyright is the first copyright line of a license file ("" when it has none; the template line of the Apache License is not
// one).
func Copyright(text string) string {
	for _, m := range copyrightRe.FindAllStringSubmatch(text, -1) {
		line := strings.TrimSpace(m[1])
		if strings.ContainsAny(line, "[]{}<>") || strings.Contains(strings.ToLower(line), "yyyy") {
			continue
		}
		if len(line) > 140 {
			line = line[:140] + "..."
		}
		return line
	}
	return ""
}

// licenseFileRe names the files of a module that hold its license: LICENSE, LICENSE.md, LICENSE.txt, LICENCE, COPYING, UNLICENSE
// and their LICENSE-MIT style variants. A folder (REUSE's LICENSES/) is not one.
var licenseFileRe = regexp.MustCompile(`(?i)^(?:licen[sc]e|copying|unlicense)(?:[-_.].*)?$`)

// ReadLicenses reads the license files of the module in dir. The main file (LICENSE, LICENSE.md, LICENSE.txt, COPYING) comes
// first, so what the module starts with is the license it grants.
func ReadLicenses(dir string) (texts []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && licenseFileRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	rank := func(n string) int {
		switch strings.ToUpper(n) {
		case "LICENSE", "LICENSE.MD", "LICENSE.TXT", "LICENCE", "LICENCE.MD", "LICENCE.TXT", "COPYING", "COPYING.MD", "COPYING.TXT", "UNLICENSE":
			return 0
		}
		return 1
	}
	sort.SliceStable(names, func(i, j int) bool {
		if ri, rj := rank(names[i]), rank(names[j]); ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		texts = append(texts, string(b))
	}
	return texts, nil
}

// Describe is the license column of a module from the texts of its license files: the license it starts with, and, when other
// licenses appear in its files, that they do ("Apache-2.0, with code under BSD-3-Clause"). "" when the files say nothing. The
// copyright line is given only for a module under one license: when there are several, a copyright line may belong to any of
// them, and the files themselves are the place to read it.
func Describe(texts []string) (license, copyright string) {
	var ids []string
	for _, t := range texts {
		for _, id := range Detect(t) {
			if !contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	switch len(ids) {
	case 0:
		return "", ""
	case 1:
		for _, t := range texts {
			if copyright = Copyright(t); copyright != "" {
				break
			}
		}
		return ids[0], copyright
	}
	return ids[0] + ", with code under " + strings.Join(ids[1:], ", "), ""
}

// overrides are the modules whose license the files alone do not state well: what the license column should say, with the reason.
// Each is checked by hand against the module's own LICENSE; the test of this package names every module that has one.
var overrides = map[string]string{
	// two licenses for the module itself, not code it happens to include
	"github.com/modelcontextprotocol/go-sdk": "MIT and Apache-2.0 (the project is moving its code from MIT to Apache-2.0; its LICENSE holds both)",
	"gopkg.in/yaml.v3":                       "MIT and Apache-2.0 (the library is MIT; its libyaml port is under the MIT-style license in LICENSE, and NOTICE names the Apache-2.0 files)",
	"go.yaml.in/yaml/v3":                     "MIT and Apache-2.0 (the library is MIT; its libyaml port is under the MIT-style license in LICENSE, and NOTICE names the Apache-2.0 files)",
	"github.com/sorairolake/lzip-go":         "Apache-2.0 OR MIT (the author's choice: LICENSE-APACHE and LICENSE-MIT)",
	// SQLite itself is in the public domain; the module bundles code under other licenses
	"modernc.org/sqlite": "BSD-3-Clause (SQLite itself is in the public domain; code the module bundles is under the licenses listed in its LICENSE-3RD-PARTY.md)",
	// a module whose source is not in the module cache here (only its go.mod is needed); the license is its repository's
	"google.golang.org/genproto": "Apache-2.0 (the repository's LICENSE; this module's source is not needed to build)",
}

// Source is what the rows are made from.
type Source struct {
	// VersionsJSON is docs/software-versions.json, LockJSON is ui/package-lock.json, PackageJSON is ui/package.json.
	VersionsJSON, LockJSON, PackageJSON []byte
	// ModCache is the Go module cache (go env GOMODCACHE).
	ModCache string
}

// escapePath is the module cache's spelling of a module path: each upper-case letter is "!" and the lower-case letter.
func escapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GoRows lists the Go modules compiled into Drydock (docs/software-versions.json says which: inBuild), with the license and the
// first copyright line of each, read from the module cache. A module with no license file in the cache and no override is an
// error: the file would claim a license nobody read.
func GoRows(src Source) ([]Row, error) {
	var rep struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Used    string `json:"used"`
			Role    string `json:"role"`
			InBuild *bool  `json:"inBuild"`
		} `json:"items"`
	}
	if err := json.Unmarshal(src.VersionsJSON, &rep); err != nil {
		return nil, fmt.Errorf("docs/software-versions.json: %w", err)
	}
	var rows []Row
	var problems []string
	for _, it := range rep.Items {
		if !strings.HasPrefix(it.ID, "go:") || it.InBuild == nil || !*it.InBuild {
			continue
		}
		row := Row{Name: it.Name, Version: "v" + it.Used, Direct: it.Kind == "direct", Role: firstSentence(it.Role)}
		dir := filepath.Join(src.ModCache, escapePath(it.Name)+"@v"+it.Used)
		if texts, err := ReadLicenses(dir); err == nil {
			row.License, row.Copyright = Describe(texts)
		}
		if o, ok := overrides[it.Name]; ok {
			row.License = o
		}
		if row.License == "" {
			problems = append(problems, fmt.Sprintf("%s v%s: no license found in %s", it.Name, it.Used, dir))
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	if len(problems) > 0 {
		return rows, fmt.Errorf("%s\n(run go mod download first; a module whose source is not needed gets an entry in overrides)", strings.Join(problems, "\n"))
	}
	return rows, nil
}

// NPMRows lists the npm packages the UI's runtime dependencies install: every package of ui/package-lock.json that is not only a
// development tool and not an optional platform build (the esbuild, oxc and lightningcss binaries of other systems), with the
// license the lock file records for it. The lock file does not say which of them end up in the bundle, so build tools the
// dependencies bring along are listed too.
func NPMRows(src Source) ([]Row, error) {
	var lock struct {
		Packages map[string]struct {
			Version     string `json:"version"`
			License     any    `json:"license"`
			Dev         bool   `json:"dev"`
			DevOptional bool   `json:"devOptional"`
			Optional    bool   `json:"optional"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(src.LockJSON, &lock); err != nil {
		return nil, fmt.Errorf("ui/package-lock.json: %w", err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(src.PackageJSON, &pkg); err != nil {
		return nil, fmt.Errorf("ui/package.json: %w", err)
	}
	var rep struct {
		Items []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"items"`
	}
	_ = json.Unmarshal(src.VersionsJSON, &rep)
	roles := map[string]string{}
	for _, it := range rep.Items {
		roles[it.ID] = it.Role
	}
	var rows []Row
	var problems []string
	for key, p := range lock.Packages {
		i := strings.LastIndex(key, "node_modules/")
		if key == "" || i < 0 || p.Dev || p.DevOptional || p.Optional {
			continue
		}
		name := key[i+len("node_modules/"):]
		_, direct := pkg.Dependencies[name]
		row := Row{Name: name, Version: p.Version, Direct: direct}
		switch l := p.License.(type) {
		case string:
			row.License = l
		case map[string]any:
			row.License, _ = l["type"].(string)
		}
		if direct {
			row.Role = firstSentence(roles["npm:"+name])
		}
		if row.License == "" {
			problems = append(problems, name+"@"+p.Version+": the lock file records no license")
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Version < rows[j].Version
	})
	if len(problems) > 0 {
		return rows, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return rows, nil
}

// firstSentence is the first sentence of a role, short enough for a table cell.
func firstSentence(s string) string {
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.ReplaceAll(s, "|", "/")
}

func cell(s string) string {
	if s == "" {
		return "-"
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " ")
}

// GoTable is the Go modules as a Markdown table: those Drydock imports first, then those they pull in.
func GoTable(rows []Row) string {
	var b strings.Builder
	for _, direct := range []bool{true, false} {
		if direct {
			b.WriteString("Imported by Drydock itself:\n\n")
		} else {
			b.WriteString("\nPulled in by those, and compiled in:\n\n")
		}
		b.WriteString("| Module | Version | License | Copyright | Used for |\n|---|---|---|---|---|\n")
		for _, r := range rows {
			if r.Direct == direct {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", cell(r.Name), cell(r.Version), cell(r.License), cell(r.Copyright), cell(r.Role))
			}
		}
	}
	return b.String()
}

// NPMTable is the UI packages as a Markdown table, the ones Drydock lists itself first.
func NPMTable(rows []Row) string {
	var b strings.Builder
	for _, direct := range []bool{true, false} {
		if direct {
			b.WriteString("Listed in `ui/package.json` as what the UI needs to run:\n\n")
		} else {
			b.WriteString("\nPulled in by those:\n\n")
		}
		b.WriteString("| Package | Version | License | Used for |\n|---|---|---|---|\n")
		for _, r := range rows {
			if r.Direct == direct {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(r.Name), cell(r.Version), cell(r.License), cell(r.Role))
			}
		}
	}
	return b.String()
}

// MPLSection names the Go modules and UI packages under the MPL-2.0 and says how to get their source, which the license asks of
// whoever hands out a program that contains them. A package offered under the MPL-2.0 or another license is named as such.
func MPLSection(goRows, npmRows []Row) string {
	var mpl, npm, dual []Row
	for _, r := range goRows {
		if r.MPL() {
			mpl = append(mpl, r)
		}
	}
	for _, r := range npmRows {
		switch {
		case r.License == "MPL-2.0":
			npm = append(npm, r)
		case strings.Contains(r.License, "MPL-2.0"):
			dual = append(dual, r)
		}
	}
	if len(mpl)+len(npm)+len(dual) == 0 {
		return "Nothing Drydock contains is under the Mozilla Public License.\n"
	}
	var b strings.Builder
	b.WriteString("The Mozilla Public License 2.0 (https://www.mozilla.org/MPL/2.0/) is a license on the files of a module, not on the program that\n")
	b.WriteString("contains it. Drydock includes the following unmodified. The source of each, at the version Drydock contains, is available from the\n")
	b.WriteString("place named.\n\n")
	if len(mpl) > 0 {
		b.WriteString("Go modules (the module proxy serves each at `https://proxy.golang.org/<module>/@v/<version>.zip`):\n\n")
		for _, r := range mpl {
			fmt.Fprintf(&b, "- `%s` %s: https://%s\n", r.Name, r.Version, repoURL(r.Name))
		}
	}
	if len(npm) > 0 {
		b.WriteString("\nUI packages (the npm registry serves each at `https://registry.npmjs.org/<package>`):\n\n")
		for _, r := range npm {
			fmt.Fprintf(&b, "- `%s` %s\n", r.Name, r.Version)
		}
	}
	if len(dual) > 0 {
		b.WriteString("\nOffered under the MPL-2.0 or another license, and used under the other one:\n\n")
		for _, r := range dual {
			fmt.Fprintf(&b, "- `%s` %s: %s\n", r.Name, r.Version, r.License)
		}
	}
	return b.String()
}

var majorSuffixRe = regexp.MustCompile(`^v\d+$`)

// repoURL is the repository that serves a module path: on GitHub the first three elements, and without a major version suffix
// (github.com/hashicorp/golang-lru/v2 is in github.com/hashicorp/golang-lru).
func repoURL(path string) string {
	parts := strings.Split(path, "/")
	if parts[0] == "github.com" && len(parts) > 3 {
		parts = parts[:3]
	}
	if n := len(parts); n > 1 && majorSuffixRe.MatchString(parts[n-1]) {
		parts = parts[:n-1]
	}
	return strings.Join(parts, "/")
}

// Splice replaces what is between the two marker lines with body and returns the document. The markers stay. An error if either
// is missing or they are out of order.
func Splice(doc, begin, end, body string) (string, error) {
	i := strings.Index(doc, begin)
	j := strings.Index(doc, end)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("the markers %q and %q are not both in the file, in that order", begin, end)
	}
	return doc[:i+len(begin)] + "\n\n" + strings.TrimRight(body, "\n") + "\n\n" + doc[j:], nil
}

// Markers of the generated parts of THIRD_PARTY_NOTICES.md.
const (
	GoBegin  = "<!-- BEGIN GENERATED: Go modules (go run ./cmd/drydock-notices) -->"
	GoEnd    = "<!-- END GENERATED: Go modules -->"
	MPLBegin = "<!-- BEGIN GENERATED: MPL-2.0 modules (go run ./cmd/drydock-notices) -->"
	MPLEnd   = "<!-- END GENERATED: MPL-2.0 modules -->"
	NPMBegin = "<!-- BEGIN GENERATED: UI packages (go run ./cmd/drydock-notices) -->"
	NPMEnd   = "<!-- END GENERATED: UI packages -->"
)

// Generate returns doc with its three generated parts rewritten from src.
func Generate(doc string, src Source) (string, error) {
	goRows, gerr := GoRows(src)
	npmRows, nerr := NPMRows(src)
	if err := errorsJoin(gerr, nerr); err != nil {
		return "", err
	}
	var err error
	if doc, err = Splice(doc, GoBegin, GoEnd, GoTable(goRows)); err != nil {
		return "", err
	}
	if doc, err = Splice(doc, MPLBegin, MPLEnd, MPLSection(goRows, npmRows)); err != nil {
		return "", err
	}
	return Splice(doc, NPMBegin, NPMEnd, NPMTable(npmRows))
}

func errorsJoin(errs ...error) error {
	var msgs []string
	for _, e := range errs {
		if e != nil {
			msgs = append(msgs, e.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

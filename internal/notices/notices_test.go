package notices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	mitText = `MIT License

Copyright (c) 2023 Ayman Bagabas

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software.
`
	bsd3Text = `Copyright (c) 2009 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.
`
	bsd2Text = `Copyright (c) 2015, Someone
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following
conditions are met:

1. Redistributions of source code must retain the above copyright notice.
2. Redistributions in binary form must reproduce the above copyright notice.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS".
`
	apacheText = `
                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,

   APPENDIX: How to apply the Apache License to your work.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
`
	mplText = `Mozilla Public License Version 2.0
==================================

1.12. "Secondary License"
    means either the GNU General Public License, Version 2.0, the GNU Lesser
    General Public License, Version 2.1, the GNU Affero General Public
    License, Version 3.0, or any later versions of those licenses.
`
	iscText = `ISC License

Copyright (c) 2025 Coder

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.
`
	gplText = `                    GNU GENERAL PUBLIC LICENSE
                       Version 3, 29 June 2007
`
)

func TestDetectReadsTheLicenseAFileGrants(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		want []string
	}{
		"MIT":                       {mitText, []string{"MIT"}},
		"BSD 3-clause":              {bsd3Text, []string{"BSD-3-Clause"}},
		"BSD 2-clause":              {bsd2Text, []string{"BSD-2-Clause"}},
		"Apache":                    {apacheText, []string{"Apache-2.0"}},
		"ISC":                       {iscText, []string{"ISC"}},
		"MPL names the GPL":         {mplText, []string{"MPL-2.0"}},
		"GPL at the top":            {gplText, []string{"GPL"}},
		"LGPL at the top":           {"GNU LESSER GENERAL PUBLIC LICENSE\nVersion 3", []string{"LGPL"}},
		"AGPL at the top":           {"\n\nGNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3", []string{"AGPL"}},
		"Unlicense":                 {"This is free and unencumbered software released into the public domain.", []string{"Unlicense"}},
		"nothing":                   {"see the website", nil},
		"empty":                     {"", nil},
		"the first one is first":    {apacheText + "\n" + bsd3Text, []string{"Apache-2.0", "BSD-3-Clause"}},
		"in the other order":        {bsd3Text + "\n" + apacheText, []string{"BSD-3-Clause", "Apache-2.0"}},
		"lines wrapped another way": {strings.ReplaceAll(mitText, "Permission is hereby granted, free of charge,", "Permission is hereby\n   granted,  free of\ncharge,"), []string{"MIT"}},
	} {
		if got := Detect(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	// a GPL file that merely mentions the MIT license further down is still the GPL
	if got := Detect(gplText + "\n" + mitText); !slices.Equal(got, []string{"GPL"}) {
		t.Errorf("GPL then MIT text: %v", got)
	}
}

func TestCopyrightIsTheFirstRealLine(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"MIT":               {mitText, "Copyright (c) 2023 Ayman Bagabas"},
		"BSD":               {bsd3Text, "Copyright (c) 2009 The Go Authors. All rights reserved."},
		"Apache's template": {apacheText, ""},
		"plain year":        {"Apache License\n\nCopyright 2025 Coder Technologies, Inc.\n", "Copyright 2025 Coder Technologies, Inc."},
		"comment marker":    {" * Copyright 2019 Someone\n", "Copyright 2019 Someone"},
		"upper case":        {"COPYRIGHT (C) 2001 Somebody", "COPYRIGHT (C) 2001 Somebody"},
		"a word in prose":   {"copyright license to reproduce, prepare\nCopyright holders may sue", ""},
		"none":              {"Permission is hereby granted", ""},
	} {
		if got := Copyright(tc.text); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	long := "Copyright (c) 2020 " + strings.Repeat("A", 300)
	if got := Copyright(long); len(got) > 150 || !strings.HasSuffix(got, "...") {
		t.Errorf("a long line is cut: %q", got)
	}
}

func TestDescribeSaysWhenSeveralLicensesAreInTheFiles(t *testing.T) {
	if l, c := Describe([]string{mitText}); l != "MIT" || c != "Copyright (c) 2023 Ayman Bagabas" {
		t.Errorf("one license: %q %q", l, c)
	}
	l, c := Describe([]string{apacheText + "\n" + bsd3Text})
	if l != "Apache-2.0, with code under BSD-3-Clause" || c != "" {
		t.Errorf("two: %q, copyright %q (it may belong to either)", l, c)
	}
	if l, c := Describe([]string{apacheText, mitText}); l != "Apache-2.0, with code under MIT" || c != "" {
		t.Errorf("two files: %q %q", l, c)
	}
	if l, _ := Describe([]string{"nothing to see"}); l != "" {
		t.Errorf("no license: %q", l)
	}
	if l, _ := Describe(nil); l != "" {
		t.Errorf("no files: %q", l)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadLicensesTakesTheLicenseFilesAndTheMainOneFirst(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "LICENSE-3RD-PARTY.md"), "third")
	write(t, filepath.Join(dir, "LICENSE"), "main")
	write(t, filepath.Join(dir, "LICENSE-MIT"), "mit")
	write(t, filepath.Join(dir, "README.md"), "readme")
	write(t, filepath.Join(dir, "LICENSES", "MIT.txt"), "a folder is not a license file")
	write(t, filepath.Join(dir, "notice.txt"), "not a license file name")
	got, err := ReadLicenses(dir)
	if err != nil || strings.Join(got, ",") != "main,third,mit" && strings.Join(got, ",") != "main,mit,third" {
		t.Fatalf("%v, %v", got, err)
	}
	if got[0] != "main" {
		t.Errorf("the main file must come first: %v", got)
	}
	if _, err := ReadLicenses(filepath.Join(dir, "missing")); err == nil {
		t.Error("a folder that is not there is an error")
	}
}

func versionsJSON(t *testing.T, items ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoRowsReadTheModuleCacheAndNameWhatItCannotRead(t *testing.T) {
	cache := t.TempDir()
	write(t, filepath.Join(cache, "github.com", "!burnt!sushi", "toml@v1.2.3", "LICENSE"), mitText)
	write(t, filepath.Join(cache, "github.com", "hashicorp", "golang-lru", "v2@v2.0.7", "LICENSE"), mplText)
	write(t, filepath.Join(cache, "example.com", "bare@v0.1.0", "README.md"), "no license file at all")
	yes, no := true, false
	src := Source{ModCache: cache, VersionsJSON: versionsJSON(t,
		map[string]any{"id": "go:github.com/BurntSushi/toml", "name": "github.com/BurntSushi/toml", "kind": "direct", "used": "1.2.3", "role": "Reads TOML. Second sentence | with a bar.", "inBuild": &yes},
		map[string]any{"id": "go:github.com/hashicorp/golang-lru/v2", "name": "github.com/hashicorp/golang-lru/v2", "kind": "indirect", "used": "2.0.7", "inBuild": &yes},
		map[string]any{"id": "go:example.com/graph-only", "name": "example.com/graph-only", "kind": "indirect", "used": "9.9.9", "inBuild": &no},
		map[string]any{"id": "go:example.com/unmeasured", "name": "example.com/unmeasured", "kind": "indirect", "used": "9.9.9"},
		map[string]any{"id": "npm:marked", "name": "marked", "kind": "direct", "used": "1", "inBuild": &yes},
	)}
	rows, err := GoRows(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "github.com/BurntSushi/toml" || rows[1].Name != "github.com/hashicorp/golang-lru/v2" {
		t.Fatalf("only the modules in the build, in order: %+v", rows)
	}
	if r := rows[0]; r.Version != "v1.2.3" || r.License != "MIT" || r.Copyright != "Copyright (c) 2023 Ayman Bagabas" || !r.Direct || r.Role != "Reads TOML." {
		t.Errorf("toml: %+v", r)
	}
	if r := rows[1]; !r.MPL() || r.Direct {
		t.Errorf("golang-lru: %+v", r)
	}

	// a module in the build whose license cannot be read is an error that names it, never a row with a guess
	src.VersionsJSON = versionsJSON(t, map[string]any{"id": "go:example.com/bare", "name": "example.com/bare", "kind": "direct", "used": "0.1.0", "inBuild": &yes},
		map[string]any{"id": "go:example.com/absent", "name": "example.com/absent", "kind": "direct", "used": "0.1.0", "inBuild": &yes})
	if _, err := GoRows(src); err == nil || !strings.Contains(err.Error(), "example.com/bare") || !strings.Contains(err.Error(), "example.com/absent") {
		t.Errorf("modules with no license: %v", err)
	}
	// an override says what the license column reads when the module's source is not in the cache
	src.VersionsJSON = versionsJSON(t, map[string]any{"id": "go:google.golang.org/genproto", "name": "google.golang.org/genproto", "kind": "indirect", "used": "0.0.0-2025", "inBuild": &yes})
	rows, err = GoRows(src)
	if err != nil || len(rows) != 1 || !strings.HasPrefix(rows[0].License, "Apache-2.0") {
		t.Errorf("override: %+v %v", rows, err)
	}
}

func TestNPMRowsAreTheRuntimePackagesAndNothingOnlyForDevelopment(t *testing.T) {
	lock := `{"packages":{
	  "": {"name":"ui"},
	  "node_modules/marked": {"version":"18.1.1","license":"MIT"},
	  "node_modules/remix/node_modules/inner": {"version":"2.0.0","license":{"type":"ISC"}},
	  "node_modules/@remix-run/ui": {"version":"0.12.1","license":"MIT"},
	  "node_modules/vitest": {"version":"4.0.0","license":"MIT","dev":true},
	  "node_modules/both": {"version":"1.0.0","license":"MIT","devOptional":true},
	  "node_modules/esbuild-linux": {"version":"1.0.0","license":"MIT","optional":true},
	  "node_modules/lightningcss": {"version":"1.33.0","license":"MPL-2.0"},
	  "node_modules/dompurify": {"version":"3.4.16","license":"(MPL-2.0 OR Apache-2.0)"}
	}}`
	src := Source{LockJSON: []byte(lock), PackageJSON: []byte(`{"dependencies":{"marked":"18.1.1","@remix-run/ui":"0.12.1"}}`),
		VersionsJSON: versionsJSON(t, map[string]any{"id": "npm:marked", "role": "Splits Markdown. Other things."})}
	rows, err := NPMRows(src)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if want := []string{"@remix-run/ui", "dompurify", "inner", "lightningcss", "marked"}; !slices.Equal(names, want) {
		t.Fatalf("%v, want %v", names, want)
	}
	for _, r := range rows {
		if r.Name == "inner" && r.License != "ISC" {
			t.Errorf("a license recorded as an object: %+v", r)
		}
		if r.Name == "marked" && (!r.Direct || r.Role != "Splits Markdown.") {
			t.Errorf("a package Drydock lists: %+v", r)
		}
		if r.Name == "lightningcss" && r.Direct {
			t.Errorf("a package pulled in is not direct: %+v", r)
		}
	}
	section := MPLSection(nil, rows)
	if !strings.Contains(section, "- `lightningcss` 1.33.0") || !strings.Contains(section, "- `dompurify` 3.4.16: (MPL-2.0 OR Apache-2.0)") || strings.Contains(section, "`marked`") {
		t.Errorf("the MPL section:\n%s", section)
	}
	src.LockJSON = []byte(`{"packages":{"node_modules/nolicense":{"version":"1.0.0"}}}`)
	if _, err := NPMRows(src); err == nil || !strings.Contains(err.Error(), "nolicense") {
		t.Errorf("a package with no license: %v", err)
	}
}

func TestTablesAreEscapedSortedAndSplicedBetweenMarkers(t *testing.T) {
	rows := []Row{
		{Name: "b/mod", Version: "v1", License: "MIT", Role: "uses | a bar", Direct: true},
		{Name: "a/mod", Version: "v2", License: "Apache-2.0, with code under BSD-3-Clause", Copyright: "Copyright 2025 A"},
	}
	table := GoTable(rows)
	if strings.Count(table, "| a/mod ") != 1 || strings.Index(table, "b/mod") > strings.Index(table, "a/mod") {
		t.Errorf("direct modules come before the ones they pull in:\n%s", table)
	}
	if strings.Contains(table, "| uses | a bar") || !strings.Contains(table, "uses / a bar") || !strings.Contains(table, "| - |") {
		t.Errorf("escaping and empty cells:\n%s", table)
	}
	doc := "head\n" + GoBegin + "\nold\n" + GoEnd + "\ntail\n"
	got, err := Splice(doc, GoBegin, GoEnd, "new body\n")
	if err != nil || got != "head\n"+GoBegin+"\n\nnew body\n\n"+GoEnd+"\ntail\n" {
		t.Fatalf("%q, %v", got, err)
	}
	again, _ := Splice(got, GoBegin, GoEnd, "new body\n")
	if again != got {
		t.Errorf("splicing the same body again must change nothing:\n%q\n%q", got, again)
	}
	if _, err := Splice("no markers", GoBegin, GoEnd, "x"); err == nil {
		t.Error("missing markers")
	}
	if _, err := Splice(GoEnd+"\n"+GoBegin, GoBegin, GoEnd, "x"); err == nil {
		t.Error("markers in the wrong order")
	}
}

func TestRepoURLDropsTheMajorVersionSuffixAndSubpackages(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/hashicorp/golang-lru/v2":          "github.com/hashicorp/golang-lru",
		"github.com/hashicorp/go-version":             "github.com/hashicorp/go-version",
		"github.com/aws/aws-sdk-go-v2/service/ec2/v3": "github.com/aws/aws-sdk-go-v2",
		"gopkg.in/yaml.v3":                            "gopkg.in/yaml.v3",
		"mvdan.cc/sh/v3":                              "mvdan.cc/sh",
	} {
		if got := repoURL(in); got != want {
			t.Errorf("repoURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEveryOverrideIsForAModuleInTheBuild(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "docs", "software-versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Items []struct {
			Name    string `json:"name"`
			InBuild *bool  `json:"inBuild"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	inBuild := map[string]bool{}
	for _, it := range rep.Items {
		if it.InBuild != nil && *it.InBuild {
			inBuild[it.Name] = true
		}
	}
	for mod := range overrides {
		if !inBuild[mod] {
			t.Errorf("the override for %s is for a module that is not in the build: take it out", mod)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// THIRD_PARTY_NOTICES.md says what the build holds: every Go module the measurement (docs/software-versions.json) puts in the build
// is in the generated table at its version with a license, the MPL section is the one the table gives, and the UI table is
// exactly what ui/package-lock.json gives. A change to the modules that leaves the file behind fails here, with the command that
// fixes it (audit 2026-10-16, L57).
func TestTheCommittedNoticesNameEveryModuleInTheBuildAtItsVersion(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	doc := string(read("THIRD_PARTY_NOTICES.md"))
	between := func(begin, end string) string {
		i, j := strings.Index(doc, begin), strings.Index(doc, end)
		if i < 0 || j < i {
			t.Fatalf("THIRD_PARTY_NOTICES.md lacks the markers %q ... %q", begin, end)
		}
		return doc[i+len(begin) : j]
	}
	const fix = "\nrun: go run ./cmd/drydock-notices (after go run ./cmd/drydock-audit, docs/software-versions.md \"The rule\")"

	// the Go table: the same modules at the same versions as the measurement says are compiled in
	var rep struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Used    string `json:"used"`
			InBuild *bool  `json:"inBuild"`
		} `json:"items"`
	}
	if err := json.Unmarshal(read("docs/software-versions.json"), &rep); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, it := range rep.Items {
		if strings.HasPrefix(it.ID, "go:") && it.InBuild != nil && *it.InBuild {
			want[it.Name+" v"+it.Used] = true
		}
	}
	var goRows []Row
	have := map[string]bool{}
	for _, line := range strings.Split(between(GoBegin, GoEnd), "\n") {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), " | ")
		if !strings.HasPrefix(line, "| ") || len(cells) != 5 || strings.TrimSpace(cells[0]) == "Module" {
			continue
		}
		name, version, license := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		have[name+" "+version] = true
		goRows = append(goRows, Row{Name: name, Version: version, License: license})
		if license == "" || license == "-" {
			t.Errorf("%s %s has no license in the table", name, version)
		}
	}
	for k := range want {
		if !have[k] {
			t.Errorf("%s is compiled in and is not in the Go table at that version%s", k, fix)
		}
	}
	for k := range have {
		if !want[k] {
			t.Errorf("%s is in the Go table and the measurement does not put it in the build at that version%s", k, fix)
		}
	}
	if len(want) < 100 {
		t.Errorf("only %d modules are measured as in the build: is docs/software-versions.json current?", len(want))
	}

	// the UI table and the MPL section are made from the lock file and the table
	npmRows, err := NPMRows(Source{VersionsJSON: read("docs/software-versions.json"), LockJSON: read("ui/package-lock.json"), PackageJSON: read("ui/package.json")})
	if err != nil {
		t.Fatal(err)
	}
	wantNPM := "\n\n" + strings.TrimRight(NPMTable(npmRows), "\n") + "\n\n"
	if got := between(NPMBegin, NPMEnd); got != wantNPM {
		t.Errorf("the UI table is not what ui/package-lock.json gives%s", fix)
	}
	wantMPL := "\n\n" + strings.TrimRight(MPLSection(goRows, npmRows), "\n") + "\n\n"
	if got := between(MPLBegin, MPLEnd); got != wantMPL {
		t.Errorf("the MPL-2.0 section is not the one the tables give%s\n--- have:\n%s--- want:\n%s", fix, got, wantMPL)
	}
	if !strings.Contains(between(MPLBegin, MPLEnd), "hashicorp/go-version") {
		t.Error("the MPL-2.0 modules are missing from the MPL section")
	}

	// what the file says about Drydock's own license is what LICENSE says
	if !strings.Contains(string(read("LICENSE")), "BSD 3-Clause License") {
		t.Skip("LICENSE is not the BSD 3-Clause License any more: the opening of THIRD_PARTY_NOTICES.md must change with it")
	}
	if strings.Contains(strings.ToLower(doc), "proprietary commercial software") || !strings.Contains(doc, "published under the BSD 3-Clause License") {
		t.Error("THIRD_PARTY_NOTICES.md must state the license LICENSE grants")
	}
}

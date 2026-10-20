# Third-party notices

Drydock's own source code is published under the BSD 3-Clause License (`LICENSE`). The app also checks signed license tokens for
its commercial packaging (`internal/license`, `docs/packaging.md` section 10); that is a feature of the product and does not
change the license the source was published under. Which terms later releases are sold on is the owner's decision, and it is
still open (`docs/decisions.md`, open items).

This file lists the software Drydock contains or uses, with the license each is under. The tables of Go modules and UI packages
are generated (`go run ./cmd/drydock-notices`) from the module list the Software Versions page is built from, the UI lock file and
the Go module cache, and a test fails when they disagree with those files; the rest is written by hand. **Legal review is required
before commercial release** (the MCP Go SDK's MIT to Apache-2.0 relicensing; Anthropic, Google and OpenAI terms for orchestrating
their CLIs).

## Go modules compiled into Drydock

Every module that `go list -deps` finds in the Linux or the Windows build, tests included, at the version `go.mod` selects. The
copyright line is the first one in the module's license file.

<!-- BEGIN GENERATED: Go modules (go run ./cmd/drydock-notices) -->

Imported by Drydock itself:

| Module | Version | License | Copyright | Used for |
|---|---|---|---|---|
| cel.dev/cel-go | v0.32.0 | Apache-2.0, with code under BSD-3-Clause | - | Evaluates the rules a person writes (model routing, integration rules) in Google's CEL expression language. |
| github.com/anthropics/anthropic-sdk-go | v1.83.0 | MIT | Copyright 2023 Anthropic, PBC. | Drydock's own requests to the Anthropic API, through the llm layer that adds caching, quota and retries. |
| github.com/aymanbagabas/go-pty | v0.2.3 | MIT | Copyright (c) 2023 Ayman Bagabas | The human's terminals in the bottom dock: ConPTY on Windows, a Unix pseudo-terminal elsewhere (internal/terminal). |
| github.com/charmbracelet/x/ansi | v0.11.9 | MIT | Copyright (c) 2023 Charmbracelet, Inc. | Meant for reading terminal escape codes in agent and terminal output. |
| github.com/coder/acp-go-sdk | v0.13.5 | Apache-2.0 | Copyright 2025 Coder Technologies, Inc. | Agent Client Protocol: how Drydock talks to Codex, Gemini, OpenCode and Goose. |
| github.com/google/jsonschema-go | v0.4.3 | MIT | Copyright (c) 2025 JSON Schema Go Project Authors | Describes the workspace tools' inputs to MCP clients as JSON Schema. |
| github.com/modelcontextprotocol/go-sdk | v1.8.0 | MIT and Apache-2.0 (the project is moving its code from MIT to Apache-2.0; its LICENSE holds both) | - | The official MCP SDK: the gateway agents connect to, the clients that reach the servers you add, and the stdio shim. |
| github.com/openai/openai-go/v3 | v3.82.0 | Apache-2.0 | Copyright 2026 OpenAI | Talks to OpenAI-compatible providers (OpenRouter and similar) for Drydock's own requests. |
| github.com/pressly/goose/v3 | v3.29.0 | MIT | - | Runs the database migrations. |
| github.com/tree-sitter/go-tree-sitter | v0.25.0 | MIT | - | The repo map's parser (decision P62): tree-sitter's official Go binding, which compiles tree-sitter's C library into Drydock through CGO, so the Windows build n... |
| github.com/tree-sitter/tree-sitter-c-sharp | v0.23.5 | MIT | Copyright (c) 2014-2023 Max Brunsfeld, Damien Guard, Amaan Qureshi, and contributors. | C# grammar of the repo map, with its queries/tags.scm. |
| github.com/tree-sitter/tree-sitter-go | v0.25.0 | MIT | Copyright (c) 2014 Max Brunsfeld | Go grammar of the repo map, with its own queries/tags.scm (copied into internal/repomap/queries). |
| github.com/tree-sitter/tree-sitter-java | v0.23.5 | MIT | Copyright (c) 2017 Ayman Nadeem | Java grammar of the repo map, with its queries/tags.scm. |
| github.com/tree-sitter/tree-sitter-javascript | v0.25.0 | MIT | Copyright (c) 2014 Max Brunsfeld | JavaScript (and JSX) grammar of the repo map, with its queries/tags.scm. |
| github.com/tree-sitter/tree-sitter-python | v0.25.0 | MIT | Copyright (c) 2016 Max Brunsfeld | Python grammar of the repo map, with its queries/tags.scm. |
| github.com/tree-sitter/tree-sitter-rust | v0.24.2 | MIT | Copyright (c) 2017 Maxim Sokolov | Rust grammar of the repo map, with its queries/tags.scm. |
| github.com/tree-sitter/tree-sitter-typescript | v0.23.2 | MIT | Copyright (c) 2017 Max Brunsfeld | TypeScript and TSX grammars of the repo map, with their queries/tags.scm (combined with JavaScript's, as the grammar does) and Drydock's own extra queries. |
| github.com/wailsapp/wails/v3 | v3.0.0-beta.34 | MIT | Copyright (c) 2018-Present Lea Anthony | The Windows desktop shell: the window and the WebView2 that shows the UI. |
| github.com/zalando/go-keyring | v0.2.8 | MIT | Copyright (c) 2016 Zalando SE | Stores secrets in the Windows Credential Manager. |
| github.com/zricethezav/gitleaks/v8 | v8.30.1 | MIT | Copyright (c) 2019 Zachary Rice | Scans diffs and outputs for secrets before they are kept or sent. |
| golang.org/x/oauth2 | v0.37.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | OAuth 2 token handling: the MCP SDK's sign-in (stored tokens, refreshing them), and the integrations' own sign-ins (Linear: authorization code with PKCE; Sentry... |
| golang.org/x/sys | v0.49.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | Windows Job Objects (killing an agent's whole process tree) and other Windows calls. |
| gopkg.in/yaml.v3 | v3.0.1 | MIT and Apache-2.0 (the library is MIT; its libyaml port is under the MIT-style license in LICENSE, and NOTICE names the Apache-2.0 files) | - | Reads YAML: agent manifests, the MCP catalog, expert packs, skills. |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause (SQLite itself is in the public domain; code the module bundles is under the licenses listed in its LICENSE-3RD-PARTY.md) | - | The database. |
| mvdan.cc/sh/v3 | v3.14.1 | BSD-3-Clause | Copyright (c) 2016, Daniel Martí. All rights reserved. | Parses shell commands so permissions can be checked before an agent runs them (POSIX syntax only). |

Pulled in by those, and compiled in:

| Module | Version | License | Copyright | Used for |
|---|---|---|---|---|
| cel.dev/expr | v0.25.3 | Apache-2.0 | - | - |
| dario.cat/mergo | v1.0.2 | BSD-3-Clause | Copyright (c) 2013 Dario Castañé. All rights reserved. | - |
| github.com/BobuSumisu/aho-corasick | v1.0.3 | MIT | Copyright (c) 2019 Øyvind Ingvaldsen | - |
| github.com/Masterminds/goutils | v1.1.1 | Apache-2.0 | - | - |
| github.com/Masterminds/semver/v3 | v3.5.0 | MIT | Copyright (C) 2014-2019, Matt Butcher and Matt Farina | - |
| github.com/Masterminds/sprig/v3 | v3.3.0 | MIT | Copyright (C) 2013-2020 Masterminds | - |
| github.com/STARRY-S/zip | v0.2.3 | BSD-3-Clause | Copyright (c) 2023, Starry | - |
| github.com/adrg/xdg | v0.5.3 | MIT | - | - |
| github.com/andybalholm/brotli | v1.2.6 | MIT | Copyright (c) 2009, 2010, 2013-2016 by the Brotli Authors. | - |
| github.com/antlr4-go/antlr/v4 | v4.13.1 | BSD-3-Clause | Copyright (c) 2012-2023 The ANTLR Project. All rights reserved. | - |
| github.com/aymanbagabas/go-osc52/v2 | v2.0.1 | MIT | Copyright (c) 2022 Ayman Bagabas | - |
| github.com/bahlo/generic-list-go | v0.2.0 | BSD-3-Clause | Copyright (c) 2009 The Go Authors. All rights reserved. | - |
| github.com/bodgit/plumbing | v1.3.0 | BSD-3-Clause | Copyright (c) 2019, Matt Dainty | - |
| github.com/bodgit/sevenzip | v1.6.5 | BSD-3-Clause | Copyright (c) 2020, Matt Dainty | - |
| github.com/bodgit/windows | v1.0.1 | BSD-3-Clause | Copyright (c) 2020, Matt Dainty | - |
| github.com/buger/jsonparser | v1.6.1 | MIT | Copyright (c) 2016 Leonid Bugaev | - |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT | Copyright (c) 2016 Caleb Spare | - |
| github.com/charmbracelet/colorprofile | v0.4.3 | MIT | Copyright (c) 2020-2024 Charmbracelet, Inc | - |
| github.com/charmbracelet/lipgloss | v1.1.1-0.20250404203927-76690c660834 | MIT | Copyright (c) 2021-2023 Charmbracelet, Inc | - |
| github.com/charmbracelet/x/cellbuf | v0.0.15 | MIT | Copyright (c) 2023 Charmbracelet, Inc. | - |
| github.com/charmbracelet/x/term | v0.2.2 | MIT | Copyright (c) 2023 Charmbracelet, Inc. | - |
| github.com/clipperhouse/displaywidth | v0.11.0 | MIT | Copyright (c) 2025 Matt Sherman | - |
| github.com/clipperhouse/uax29/v2 | v2.7.0 | MIT | Copyright (c) 2020 Matt Sherman | - |
| github.com/coder/websocket | v1.8.15 | ISC | Copyright (c) 2025 Coder | - |
| github.com/creack/pty | v1.1.24 | MIT | Copyright (c) 2011 Keith Rarick | - |
| github.com/danieljoos/wincred | v1.2.3 | MIT | Copyright (c) 2014 Daniel Joos | - |
| github.com/dsnet/compress | v0.0.2-0.20230904184137-39efe44ab707 | BSD-3-Clause | Copyright © 2015, Joe Tsai and The Go Authors. All rights reserved. | - |
| github.com/dustin/go-humanize | v1.1.0 | MIT | - | - |
| github.com/fatih/semgroup | v1.3.0 | BSD-3-Clause | Copyright (c) 2022, Fatih Arslan | - |
| github.com/fsnotify/fsnotify | v1.10.1 | BSD-3-Clause | Copyright © 2012 The Go Authors. All rights reserved. | - |
| github.com/gitleaks/go-gitdiff | v0.9.1 | MIT | Copyright (c) 2019 Billy Keyes | - |
| github.com/go-logr/logr | v1.4.4 | Apache-2.0 | - | - |
| github.com/go-logr/stdr | v1.2.2 | Apache-2.0 | - | - |
| github.com/go-ole/go-ole | v1.3.0 | MIT | - | - |
| github.com/go-viper/mapstructure/v2 | v2.5.0 | MIT | Copyright (c) 2013 Mitchell Hashimoto | - |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | Copyright (c) 2009,2014 Google Inc. All rights reserved. | - |
| github.com/h2non/filetype | v1.1.3 | MIT | Copyright (c) Tomas Aparicio | - |
| github.com/hashicorp/go-version | v1.9.0 | MPL-2.0 | - | - |
| github.com/hashicorp/golang-lru/v2 | v2.0.7 | MPL-2.0 | Copyright (c) 2014 HashiCorp, Inc. | - |
| github.com/huandu/xstrings | v1.6.3 | MIT | Copyright (c) 2015 Huan Du | - |
| github.com/invopop/jsonschema | v0.14.0 | MIT | Copyright (C) 2014 Alec Thomas | - |
| github.com/klauspost/compress | v1.20.3 | BSD-3-Clause, with code under Apache-2.0, MIT | - | - |
| github.com/klauspost/pgzip | v1.2.7 | MIT | Copyright (c) 2014 Klaus Post | - |
| github.com/lucasb-eyer/go-colorful | v1.4.1 | MIT | Copyright (c) 2013 Lucas Beyer | - |
| github.com/mattn/go-colorable | v0.1.16 | MIT | Copyright (c) 2016 Yasuhiro Matsumoto | - |
| github.com/mattn/go-isatty | v0.0.24 | MIT | - | - |
| github.com/mattn/go-pointer | v0.0.1 | MIT | Copyright (c) 2019 Yasuhiro Matsumoto | Used by go-tree-sitter to hand Go values to C callbacks (the repo map). |
| github.com/mattn/go-runewidth | v0.0.31 | MIT | Copyright (c) 2016 Yasuhiro Matsumoto | - |
| github.com/mfridman/interpolate | v0.0.2 | MIT | Copyright (c) 2014-2017 Buildkite Pty Ltd | - |
| github.com/mholt/archives | v0.1.5 | MIT | Copyright (c) 2016 Matthew Holt | - |
| github.com/mikelolasagasti/xz | v1.0.1 | ISC | - | - |
| github.com/minio/minlz | v1.2.3 | Apache-2.0 | - | - |
| github.com/mitchellh/copystructure | v1.2.0 | MIT | Copyright (c) 2014 Mitchell Hashimoto | - |
| github.com/mitchellh/reflectwalk | v1.0.2 | MIT | Copyright (c) 2013 Mitchell Hashimoto | - |
| github.com/muesli/termenv | v0.16.0 | MIT | Copyright (c) 2019 Christian Muehlhaeuser | - |
| github.com/ncruces/go-strftime | v1.1.0 | MIT | Copyright (c) 2022 Nuno Cruces | - |
| github.com/nwaples/rardecode/v2 | v2.4.5 | BSD-2-Clause | Copyright (c) 2015, Nicholas Waples | - |
| github.com/pb33f/go-yaml | v0.1.1 | Apache-2.0 | Copyright 2025 - The go-yaml Project Contributors | - |
| github.com/pb33f/ordered-map/v2 | v2.3.2 | Apache-2.0 | - | - |
| github.com/pelletier/go-toml/v2 | v2.4.3 | MIT | Copyright (c) 2021 - 2023 Thomas Pelletier | - |
| github.com/pierrec/lz4/v4 | v4.1.33 | BSD-3-Clause | Copyright (c) 2015, Pierre Curto | - |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | Copyright (c) 2012 The Go Authors. All rights reserved. | - |
| github.com/rivo/uniseg | v0.4.7 | MIT | Copyright (c) 2019 Oliver Kuederle | - |
| github.com/rs/zerolog | v1.35.1 | MIT | Copyright (c) 2017 Olivier Poitrey | - |
| github.com/sagikazarmark/locafero | v0.12.0 | MIT | - | - |
| github.com/segmentio/asm | v1.2.1 | MIT | Copyright 2023 Segment | - |
| github.com/segmentio/encoding | v0.5.4 | MIT | Copyright (c) 2019 Segment.io, Inc. | - |
| github.com/sethvargo/go-retry | v0.5.0 | Apache-2.0 | - | - |
| github.com/shopspring/decimal | v1.6.0 | MIT | Copyright (c) 2015 Spring, Inc. | - |
| github.com/sorairolake/lzip-go | v0.3.8 | Apache-2.0 OR MIT (the author's choice: LICENSE-APACHE and LICENSE-MIT) | - | - |
| github.com/spf13/afero | v1.15.0 | Apache-2.0 | - | - |
| github.com/spf13/cast | v1.10.0 | MIT | Copyright (c) 2014 Steve Francia | - |
| github.com/spf13/pflag | v1.0.10 | BSD-3-Clause | Copyright (c) 2012 Alex Ogier. All rights reserved. | - |
| github.com/spf13/viper | v1.21.0 | MIT | Copyright (c) 2014 Steve Francia | - |
| github.com/standard-webhooks/standard-webhooks/libraries | v0.0.1 | MIT | Copyright (c) 2023 Svix (https://www.svix.com) | - |
| github.com/stangelandcl/ppmd | v0.1.1 | MIT | Copyright (c) 2022 Clayton Stangeland | - |
| github.com/subosito/gotenv | v1.6.0 | MIT | Copyright (c) 2013 Alif Rachmawadi | - |
| github.com/tidwall/gjson | v1.20.0 | MIT | Copyright (c) 2016 Josh Baker | - |
| github.com/tidwall/match | v1.2.0 | MIT | Copyright (c) 2016 Josh Baker | - |
| github.com/tidwall/pretty | v1.2.2 | MIT | Copyright (c) 2017 Josh Baker | - |
| github.com/tidwall/sjson | v1.2.5 | MIT | Copyright (c) 2016 Josh Baker | - |
| github.com/u-root/u-root | v0.16.0 | BSD-3-Clause | Copyright (c) 2012-2019, u-root Authors | - |
| github.com/ulikunitz/xz | v0.5.17 | BSD-3-Clause | Copyright (c) 2014-2022  Ulrich Kunitz | - |
| github.com/xo/terminfo | v1.2.0 | MIT | Copyright (c) 2016 Anmol Sethi | - |
| github.com/yosida95/uritemplate/v3 | v3.0.2 | BSD-3-Clause | - | - |
| go.opentelemetry.io/auto/sdk | v1.2.1 | Apache-2.0 | - | - |
| go.opentelemetry.io/otel | v1.47.0 | Apache-2.0, with code under BSD-3-Clause | - | - |
| go.opentelemetry.io/otel/log | v1.47.0 | Apache-2.0, with code under BSD-3-Clause | - | - |
| go.opentelemetry.io/otel/metric | v1.47.0 | Apache-2.0, with code under BSD-3-Clause | - | - |
| go.opentelemetry.io/otel/trace | v1.47.0 | Apache-2.0, with code under BSD-3-Clause | - | - |
| go.uber.org/multierr | v1.11.0 | MIT | Copyright (c) 2017-2021 Uber Technologies, Inc. | - |
| go.yaml.in/yaml/v3 | v3.0.5 | MIT and Apache-2.0 (the library is MIT; its libyaml port is under the MIT-style license in LICENSE, and NOTICE names the Apache-2.0 files) | - | - |
| go4.org | v0.0.0-20261019143151-5b882263cdaf | Apache-2.0 | - | - |
| golang.org/x/crypto | v0.58.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| golang.org/x/exp | v0.0.0-20261014143641-f0251eb4aa3e | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| golang.org/x/mod | v0.42.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| golang.org/x/sync | v0.24.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| golang.org/x/text | v0.43.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| golang.org/x/time | v0.16.0 | BSD-3-Clause | Copyright 2009 The Go Authors. | - |
| google.golang.org/genproto | v0.0.0-20251202230838-ff82c1b0f217 | Apache-2.0 (the repository's LICENSE; this module's source is not needed to build) | - | Generated stubs of Google's public API protocol buffers; indirect (its googleapis/api and googleapis/rpc modules are what the gRPC libraries use). |
| google.golang.org/genproto/googleapis/api | v0.0.0-20261019180522-ce908d29299b | Apache-2.0 | - | - |
| google.golang.org/genproto/googleapis/rpc | v0.0.0-20261019180522-ce908d29299b | Apache-2.0 | - | - |
| google.golang.org/protobuf | v1.36.12 | BSD-3-Clause | Copyright (c) 2018 The Go Authors. All rights reserved. | - |
| modernc.org/libc | v1.79.0 | BSD-3-Clause, with code under MIT | - | The C runtime that modernc.org/sqlite (pure Go SQLite) is translated against; indirect, but compiled into the app. |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause | Copyright (c) 2014 The mathutil Authors. All rights reserved. | - |
| modernc.org/memory | v1.12.1 | BSD-3-Clause | Copyright (c) 2017 The Memory Authors. All rights reserved. | - |

<!-- END GENERATED: Go modules -->

### Modules under the Mozilla Public License 2.0

<!-- BEGIN GENERATED: MPL-2.0 modules (go run ./cmd/drydock-notices) -->

The Mozilla Public License 2.0 (https://www.mozilla.org/MPL/2.0/) is a license on the files of a module, not on the program that
contains it. Drydock includes the following unmodified. The source of each, at the version Drydock contains, is available from the
place named.

Go modules (the module proxy serves each at `https://proxy.golang.org/<module>/@v/<version>.zip`):

- `github.com/hashicorp/go-version` v1.9.0: https://github.com/hashicorp/go-version
- `github.com/hashicorp/golang-lru/v2` v2.0.7: https://github.com/hashicorp/golang-lru

UI packages (the npm registry serves each at `https://registry.npmjs.org/<package>`):

- `lightningcss` 1.33.0

Offered under the MPL-2.0 or another license, and used under the other one:

- `dompurify` 3.4.16: (MPL-2.0 OR Apache-2.0)

<!-- END GENERATED: MPL-2.0 modules -->

### Tree-sitter and its grammars (the repo map, decision P62)

The repo map compiles tree-sitter's C library and eight grammars into `drydock.exe`, and its tag queries
(`internal/repomap/queries/*-tags.scm`) are the grammars' own `queries/tags.scm`, copied unchanged with a header naming
their origin. Every one is under the MIT License. Copyright notices, from each project's `LICENSE`:

- go-tree-sitter (tree-sitter's Go binding, which includes the tree-sitter C library): Copyright (c) 2024 Amaan Qureshi
- tree-sitter-go: Copyright (c) 2014 Max Brunsfeld
- tree-sitter-typescript: Copyright (c) 2017 Max Brunsfeld
- tree-sitter-javascript: Copyright (c) 2014 Max Brunsfeld
- tree-sitter-python: Copyright (c) 2016 Max Brunsfeld
- tree-sitter-c-sharp: Copyright (c) 2014-2023 Max Brunsfeld, Damien Guard, Amaan Qureshi, and contributors
- tree-sitter-java: Copyright (c) 2017 Ayman Nadeem
- tree-sitter-rust: Copyright (c) 2017 Maxim Sokolov
- go-pointer (github.com/mattn/go-pointer): Copyright (c) 2019 Yasuhiro Matsumoto

The MIT License, as each of these projects states it: Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction,
including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions: The
above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

The extra queries in `internal/repomap/queries/*-extra.scm` are Drydock's own.

## Tools and rules the Windows download carries (decision P77)

The `tools` folder beside `drydock.exe` holds these release files exactly as each project published them on GitHub, pinned in
`packaging/bundle.json` and checked against the SHA-256 digest GitHub recorded (`tools/bundle.json` lists each file, its address and
its digest). Drydock installs them into its own folder at the first start; it does not change them.

- osv-scanner v2.7.0 (https://github.com/google/osv-scanner): Apache-2.0.
- Opengrep v1.31.0 (https://github.com/opengrep/opengrep): LGPL-2.1. The program is shipped unmodified, as its own executable, not
  linked into Drydock. Its source for that release is at https://github.com/opengrep/opengrep/tree/v1.31.0; on request Drydock's
  maintainers provide the same source.
- llama.cpp, the TurboQuant fork's release `b10298-2.0.0`, its Windows Vulkan build
  (https://github.com/AtomicBot-ai/atomic-llama-cpp-turboquant): MIT, as llama.cpp and ggml (copyright the ggml
  authors, as its LICENSE file states). Its archive carries the licences of what it contains; Drydock unpacks it as published.

The built-in Opengrep rules in `internal/scan/rules` are compiled into Drydock and written to its data folder with each set's
`LICENSE`:

- Drydock's own rules (`internal/scan/rules/drydock`): under Drydock's licence.
- dgryski/semgrep-go (https://github.com/dgryski/semgrep-go), at commit db0227c03f4b3c4e71d900188d51db4c81d66932: MIT,
  Copyright (c) 2020 Damian Gryski.
- 0xdea/semgrep-rules, the C and C++ rules (https://github.com/0xdea/semgrep-rules), at commit
  e0121ac19d9fa259a44f3a24d3e42c95719b17c7: MIT, Copyright (c) 2022 raptor.

## Tools Drydock invokes but does not bundle

Installed and licensed by the user: Claude Code (proprietary, Anthropic), Codex CLI (Apache-2.0), Gemini CLI (Apache-2.0), OpenCode (MIT), Goose (Apache-2.0), Cline CLI (Apache-2.0), Antigravity CLI (proprietary, Google), `git` (GPL-2.0), GitHub CLI (MIT), Playwright (Apache-2.0), Wrangler (MIT OR Apache-2.0), Node.js (MIT).

Fetched when used, not bundled: the ACP bridges `@agentclientprotocol/claude-agent-acp` and `@agentclientprotocol/codex-acp`
(Apache-2.0), started with `npx -y` at the versions the agent manifests pin; the official llama.cpp build (MIT, ggml-org/llama.cpp)
when a person installs it, with NVIDIA's CUDA runtime archive beside a CUDA build (NVIDIA CUDA Toolkit EULA); the GitHub
Actions runner (MIT) for the CI runners; models from the Hugging Face Hub under each model's own licence; and container images a
person's sandbox or runner pulls (Debian, Ubuntu, Node, Go and Python official images, each under its own licences). The Sentry MCP server (FSL-1.1-Apache-2.0), the Aikido MCP server (AGPL or commercial) and Notion's local MCP server are **not** bundled (decision D14).

## Dev tooling (not shipped)

go-task (MIT), go-licenses/v2 (Apache-2.0).

## UI packages

The packages `ui/package-lock.json` installs with the UI's runtime dependencies (not the development tools, and not the optional
builds for other systems), with the license the lock file records. The lock file does not say which of them end up in the bundle the
app ships, so build tools that the dependencies bring along are listed too.

<!-- BEGIN GENERATED: UI packages (go run ./cmd/drydock-notices) -->

Listed in `ui/package.json` as what the UI needs to run:

| Package | Version | License | Used for |
|---|---|---|---|
| @fontsource-variable/inter | 5.3.0 | OFL-1.1 | Bundles the Inter variable font so the desktop app looks the same offline (OFL-1.1). |
| @remix-run/ui | 0.12.1 | MIT | Headless, accessible widgets for the UI (tabs, menus, switches), wrapped in ui/app/ui. |
| @xterm/addon-fit | 0.11.0 | MIT | Sizes a terminal to its panel. |
| @xterm/xterm | 6.0.0 | MIT | Draws the human's terminals in the bottom dock (lazy-loaded). |
| lucide | 1.60.0 | ISC | The UI icons (ISC), drawn as inline SVG from icon nodes; only the icons used are bundled. |
| marked | 14.0.0 | MIT | Splits Markdown (agent messages, task text) into tokens; Drydock builds the elements from the tokens itself. |
| marked | 18.1.1 | MIT | Splits Markdown (agent messages, task text) into tokens; Drydock builds the elements from the tokens itself. |
| monaco-editor | 0.57.0 | MIT | The read-only diff editor for a task's changes (lazy-loaded; only the diff editor, find, folding, copy and the language definitions are imported). |
| remix | 3.0.0 | MIT | The UI framework: Remix 3 components (JSX, not React), the SPA router (remix/routes, remix/router, remix/spa), the test runner and assertions (remix test, remix... |

Pulled in by those:

| Package | Version | License | Used for |
|---|---|---|---|
| @jridgewell/resolve-uri | 3.1.2 | MIT | - |
| @jridgewell/sourcemap-codec | 1.6.0 | MIT | - |
| @jridgewell/trace-mapping | 0.3.31 | MIT | - |
| @oxc-project/runtime | 0.121.0 | MIT | - |
| @oxc-project/types | 0.121.0 | MIT | - |
| @remix-run/assert | 1.0.0 | MIT | - |
| @remix-run/assets | 1.0.0 | MIT | - |
| @remix-run/async-context-middleware | 1.0.0 | MIT | - |
| @remix-run/auth | 1.0.0 | MIT | - |
| @remix-run/auth-middleware | 1.0.0 | MIT | - |
| @remix-run/cli | 1.0.0 | MIT | - |
| @remix-run/component | 1.0.0 | MIT | - |
| @remix-run/component-hmr | 1.0.0 | MIT | - |
| @remix-run/compression-middleware | 1.0.0 | MIT | - |
| @remix-run/cookie | 1.0.0 | MIT | - |
| @remix-run/cop-middleware | 1.0.0 | MIT | - |
| @remix-run/cors-middleware | 1.0.0 | MIT | - |
| @remix-run/csrf-middleware | 1.0.0 | MIT | - |
| @remix-run/data-schema | 1.0.0 | MIT | - |
| @remix-run/data-table | 1.0.0 | MIT | - |
| @remix-run/data-table-mysql | 1.0.0 | MIT | - |
| @remix-run/data-table-postgres | 1.0.0 | MIT | - |
| @remix-run/data-table-sqlite | 1.0.0 | MIT | - |
| @remix-run/fetch-proxy | 1.0.0 | MIT | - |
| @remix-run/fetch-router | 1.0.0 | MIT | - |
| @remix-run/file-storage | 1.0.0 | MIT | - |
| @remix-run/file-storage-s3 | 1.0.0 | MIT | - |
| @remix-run/form-data-middleware | 1.0.0 | MIT | - |
| @remix-run/form-data-parser | 1.0.0 | MIT | - |
| @remix-run/fs | 1.0.0 | MIT | - |
| @remix-run/headers | 1.0.0 | MIT | - |
| @remix-run/html-template | 1.0.0 | MIT | - |
| @remix-run/lazy-file | 5.0.8 | MIT | - |
| @remix-run/logger-middleware | 1.0.0 | MIT | - |
| @remix-run/method-override-middleware | 1.0.0 | MIT | - |
| @remix-run/mime | 1.0.0 | MIT | - |
| @remix-run/multipart-parser | 1.0.0 | MIT | - |
| @remix-run/multiple-import-maps-polyfill | 1.0.0 | MIT | - |
| @remix-run/node-fetch-server | 1.0.0 | MIT | - |
| @remix-run/node-hmr | 1.0.0 | MIT | - |
| @remix-run/node-tsx | 1.0.0 | MIT | - |
| @remix-run/render-middleware | 1.0.0 | MIT | - |
| @remix-run/response | 1.0.0 | MIT | - |
| @remix-run/route-pattern | 1.0.0 | MIT | - |
| @remix-run/session | 1.0.0 | MIT | - |
| @remix-run/session-middleware | 1.0.0 | MIT | - |
| @remix-run/session-storage-memcache | 1.0.0 | MIT | - |
| @remix-run/session-storage-redis | 1.0.0 | MIT | - |
| @remix-run/spa | 1.0.0 | MIT | - |
| @remix-run/static-middleware | 1.0.0 | MIT | - |
| @remix-run/tar-parser | 1.0.0 | MIT | - |
| @remix-run/terminal | 1.0.0 | MIT | - |
| @remix-run/test | 1.0.0 | MIT | - |
| @standard-schema/spec | 1.1.0 | MIT | - |
| @types/dom-navigation | 1.0.7 | MIT | - |
| @types/istanbul-lib-coverage | 2.0.6 | MIT | - |
| aws4fetch | 1.0.20 | MIT | - |
| chokidar | 5.0.0 | MIT | - |
| convert-source-map | 2.0.0 | MIT | - |
| detect-libc | 2.1.2 | Apache-2.0 | - |
| dompurify | 3.4.16 | (MPL-2.0 OR Apache-2.0) | - |
| es-module-lexer | 2.3.2 | MIT | - |
| esbuild | 0.28.2 | MIT | - |
| get-tsconfig | 4.14.3 | MIT | - |
| has-flag | 4.0.0 | MIT | - |
| html-escaper | 2.0.2 | MIT | - |
| istanbul-lib-coverage | 3.2.2 | BSD-3-Clause | - |
| istanbul-lib-report | 3.0.1 | BSD-3-Clause | - |
| istanbul-reports | 3.2.0 | BSD-3-Clause | - |
| jsonc-parser | 3.3.1 | MIT | - |
| lightningcss | 1.33.0 | MPL-2.0 | - |
| magic-string | 0.30.21 | MIT | - |
| make-dir | 4.0.0 | MIT | - |
| oxc-minify | 0.121.0 | MIT | - |
| oxc-parser | 0.121.0 | MIT | - |
| oxc-resolver | 11.24.2 | MIT | - |
| oxc-transform | 0.121.0 | MIT | - |
| picomatch | 4.0.7 | MIT | - |
| readdirp | 5.1.1 | MIT | - |
| resolve-pkg-maps | 1.0.0 | MIT | - |
| semver | 7.8.5 | ISC | - |
| source-map-js | 1.2.2 | BSD-3-Clause | - |
| supports-color | 7.2.0 | MIT | - |
| v8-to-istanbul | 9.3.0 | ISC | - |

<!-- END GENERATED: UI packages -->

## Icons copied into the UI

- **Simple Icons** 16.35.0 (CC0-1.0, https://simpleicons.org): most brand marks in `ui/app/ui/brand-logo.tsx`.
- **LobeHub Icons**, `@lobehub/icons-static-svg` 1.95.1 (MIT, Copyright (c) LobeHub, https://github.com/lobehub/lobe-icons):
  the Codex, Goose, Antigravity and OpenAI marks in `ui/app/ui/brand-logo.tsx`, copied as paths. Permission is hereby granted, free
  of charge, to any person obtaining a copy of this software and associated documentation files, to deal in the Software
  without restriction, subject to including the copyright notice and this permission notice; THE SOFTWARE IS PROVIDED "AS
  IS", WITHOUT WARRANTY OF ANY KIND (the MIT License, in full at https://opensource.org/license/mit).

- **Aikido**: the two paths of the logo in `docs/banner.svg` of https://github.com/AikidoSec/safe-chain, scaled into the
  24-unit box. That repository is offered under the AGPL-3.0 or a commercial licence (its `LICENSE`, read on 2026-10-20).
- **Microsoft**: the four squares, drawn in `ui/app/ui/brand-logo.tsx` (Simple Icons carries no Microsoft mark).

The marks are trademarks of their owners, shown only to say which service or CLI a card connects to.

## Skill descriptions quoted in the catalog

`internal/skills/registry.yaml` quotes the description from the front matter of each skill the prebuilt agents name, read at a
pinned commit; nothing else of the skills ships (a skill's files are fetched only when a person adds it):

- anthropics/skills at dbd4588f9e1033efb41dad4bef2f7947c8993d44: Apache-2.0 (each skill's `LICENSE.txt`).
- vercel-labs/agent-skills at 063bee94c3f4df8453406c830b0a7df0f2860278: MIT.
- cloudflare/skills at 86505e44f87d801aa1864a3c4109e9a8ae5c409f: Apache-2.0.
- MicrosoftDocs/mcp at f8ffde185dfd232dbf5d187c22ce299eadc3d583: content CC BY 4.0, code MIT.

## Reused architecture and algorithms (reimplemented, not copied)

- **Aider** (Apache-2.0): the repo-map technique (tree-sitter tags, personalized PageRank over a file/symbol graph, identifier weights, token-budget binary search), reimplemented in Go in `internal/repomap` (decision P62); no Aider source is included. Attribution retained here as required practice.
- **Microsoft Agent Framework for Go** (MIT): design reference for supersteps, request ports and checkpoint lineage. Not imported.
- **Agent Room** (MIT): room concepts and the idea of recovering tool calls that models emit as plain text. Reimplemented generally, not copied.
- **Pregel** (Google, paper): the superstep model.

## Documentation content

Cloudflare documentation is CC BY 4.0 (attribution required when excerpts are redistributed); Microsoft Learn content is CC BY 4.0 with MIT code samples.

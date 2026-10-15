# Third-party notices

Drydock is proprietary commercial software. It depends on and, in places, re-implements ideas from the open-source projects below. Versions are those verified on 2026-10-08 (see `docs/phase0.md` and `docs/phase0/go-platform.md`). This file is seeded by hand from verified license texts; the release build regenerates the Go section with `go-licenses/v2` and a human reviews it. **Legal review is required before commercial release** (mixed MIT/Apache-2.0 relicensing in the MCP Go SDK; Anthropic, Google and OpenAI terms for orchestrating their CLIs).

## Go dependencies in the product

| Project | Version | License | Use |
|---|---|---|---|
| github.com/wailsapp/wails/v3 | v3.0.0-beta.28 | MIT | desktop shell (not yet imported) |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause (SQLite: public domain) | storage |
| github.com/pressly/goose/v3 ("the migrations tool") | v3.28.0 | MIT | schema migrations |
| github.com/zalando/go-keyring (+ danieljoos/wincred) | v0.2.8 | MIT | Windows Credential Manager |
| github.com/coder/acp-go-sdk | v0.13.5 | Apache-2.0 | ACP client |
| github.com/anthropics/anthropic-sdk-go | v1.79.1 | MIT | app-owned LLM calls |
| github.com/openai/openai-go/v3 | v3.74.0 | Apache-2.0 | app-owned LLM calls, OpenAI-compatible providers |
| github.com/modelcontextprotocol/go-sdk | v1.8.0 | MIT and Apache-2.0 (relicensing in progress) | MCP gateway and workspace server |
| cel.dev/cel-go | v0.32.0 | Apache-2.0 | routing/policy rules |
| mvdan.cc/sh/v3 | v3.14.1 | BSD-3-Clause | command analysis |
| github.com/zricethezav/gitleaks/v8 | v8.30.1 | MIT | secret scanning |
| github.com/aymanbagabas/go-pty | v0.2.3 | MIT | ConPTY terminal |
| github.com/charmbracelet/x/ansi | v0.11.8 | MIT | ANSI handling |
| gopkg.in/yaml.v3 | v3.0.1 | MIT and Apache-2.0 | manifests, Taskfile detection |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | Windows Job Objects |
| github.com/tree-sitter/go-tree-sitter and grammars | v0.25.0 / per grammar | MIT | repo map (gated, decision D5; not yet imported) |
| go.opentelemetry.io/otel | v1.47.0 | Apache-2.0 | telemetry (not yet imported) |

## Tools Drydock invokes but does not bundle

Installed and licensed by the user: Claude Code (proprietary, Anthropic), Codex CLI (Apache-2.0), Gemini CLI (Apache-2.0), OpenCode (MIT), Goose (Apache-2.0), Antigravity CLI (proprietary, Google), `git` (GPL-2.0), GitHub CLI (MIT), osv-scanner (Apache-2.0), Playwright (Apache-2.0), Wrangler (MIT OR Apache-2.0), Node.js (MIT). The Sentry MCP server (FSL-1.1-Apache-2.0), the Aikido MCP server (AGPL or commercial) and Notion's local MCP server are **not** bundled (decision D14).

## Dev tooling (not shipped)

go-task (MIT), go-licenses/v2 (Apache-2.0).

## Icons copied into the UI

- **Simple Icons** 16.35.0 (CC0-1.0, https://simpleicons.org): most brand marks in `ui/app/ui/brand-logo.tsx`.
- **LobeHub Icons**, `@lobehub/icons-static-svg` 1.95.1 (MIT, Copyright (c) LobeHub, https://github.com/lobehub/lobe-icons):
  the Codex, Goose and Antigravity marks in `ui/app/ui/brand-logo.tsx`, copied as paths. Permission is hereby granted, free
  of charge, to any person obtaining a copy of this software and associated documentation files, to deal in the Software
  without restriction, subject to including the copyright notice and this permission notice; THE SOFTWARE IS PROVIDED "AS
  IS", WITHOUT WARRANTY OF ANY KIND (the MIT License, in full at https://opensource.org/license/mit).

The marks are trademarks of their owners, shown only to say which service or CLI a card connects to.

## Reused architecture and algorithms (reimplemented, not copied)

- **Aider** (Apache-2.0): the repo-map technique (tree-sitter tags, personalized PageRank over a file/symbol graph, token-budget binary search). Reimplemented in Go; no Aider source is included. Attribution retained here as required practice.
- **Microsoft Agent Framework for Go** (MIT): design reference for supersteps, request ports and checkpoint lineage. Not imported.
- **Agent Room** (MIT): room concepts and the idea of recovering tool calls that models emit as plain text. Reimplemented generally, not copied.
- **Pregel** (Google, paper): the superstep model.

## Documentation content

Cloudflare documentation is CC BY 4.0 (attribution required when excerpts are redistributed); Microsoft Learn content is CC BY 4.0 with MIT code samples.

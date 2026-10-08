# Drydock: Expert Packs, Skills and Requirements

Scope: this document specifies attachable domain knowledge (brief sections 9, 11 "Requirements", 15). It covers Expert Packs presented as one concept in two kinds, indexed (crawled, versioned, cited, offline) and live (MCP docs servers reached through the gateway); the `pack.yaml` manifest with examples for Cloudflare and Microsoft docs; ingest and refresh with content hashes; FTS5 BM25 retrieval with heading-aware chunking; the two modes CONSULT (stateless one-shot with answer, citations, confidence, source age) and ATTACH (short instructions plus a `search_pack` tool); citation and freshness rules; how CONSULT is routed between `trainable` and `private` providers; injection handling; Skills (glob-scoped rules, `registry.yaml`, `SKILL.md` frontmatter, delivery rules per CLI capability, since only Claude Code has native path-scoped rules); and Requirements (uploaded or synced documents cited like a pack). Decisions in [`decisions.md`](decisions.md) win over the brief.

Evidence tags: `[tool §n]` = [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md), `[cli §n]` = [`phase0/agent-clis.md`](phase0/agent-clis.md), `[go §n]` = [`phase0/go-platform.md`](phase0/go-platform.md), `[prov §X]` = [`phase0/model-providers.md`](phase0/model-providers.md), `Dn` = decisions.md. Unmarked choices are **design**; unconfirmed facts are **unverified**. Related: [`mcp.md`](mcp.md) (gateway, `search_pack` and `skills` tool schemas), [`connections.md`](connections.md) (instruction-file generator), [`integrations.md`](integrations.md) (synced requirements).

## 1. Concepts

| Term | Meaning |
|---|---|
| Pack | A named body of knowledge with a manifest `packs/<id>/pack.yaml`. `kind: indexed` (local FTS5 copy), `kind: live` (queried through an MCP docs server), `kind: hybrid` (indexed first, live as fallback or supplement). The UI shows one list; the kind is a badge. |
| Page, chunk | A fetched document and its heading-delimited pieces; both carry content hashes. Chunks are the unit of retrieval and citation. |
| CONSULT | Stateless one-shot question to a pack: returns `{found, answer, citations, confidence, source_age}`. No conversation, no repo context. |
| ATTACH | The pack is part of an agent `(Provider, Role, Packs[])`: its short instructions enter the context package and the agent gets `search_pack` limited to attached packs (plus the live server's tools through the gateway allowlist). |
| Visibility | `public` (documentation anyone can fetch) or `private` (user-supplied; content derived from it is `private` data class and never reaches a `trainable` provider, brief section 6). |

## 2. `pack.yaml`

```yaml
schema: 1
id: <slug>
title: string
kind: indexed | live | hybrid
visibility: public | private
license: {content: SPDX, code: SPDX, attribution: "text rendered in citations/exports", attribution_required: bool}
sources:                                   # indexed/hybrid only
  - {type: llms_txt | sitemap | git_docs | local_dir | url_list, url|path: ..., follow: ..., page_format: markdown_suffix | html | mdx}
crawl: {respect_robots: true, require_content_signal: ai-input, allowed_hosts: [..], deny_paths: [..], rps: 2, concurrency: 2, max_page_bytes: 2097152, redirects: same_host}
chunking: {split_on: [h1, h2, h3], target_tokens: 350, max_tokens: 700, code_block_atomic_tokens: 1200}
refresh: {mode: scheduled | manual, interval: 7d, digest: {enabled: true, tier: cheap, data_class: trainable_ok}}
freshness: {warn_after: 14d, stale_after: 30d}
live: {mcp: <catalog id>, tool: <tool>, arg: query, use: primary | fallback | supplement}   # live/hybrid
attach: {instructions: "<=400 tokens", default_k: 5, roles: [..]}
consult: {max_chunks: 8, route: trainable_ok | private_only}
```

Cloudflare (hybrid), using the verified structure of the docs site [tool §1a, §1b]: the root `llms.txt` is only an index to per-product `.../<product>/llms.txt`; any page has a Markdown form at `<page>/index.md`; per-product `llms-full.txt` exists; content is CC BY 4.0 and code MIT; `robots.txt` carries `Content-Signal: ai-train=yes, search=yes, ai-input=yes`.

```yaml
schema: 1
id: cloudflare
title: Cloudflare Developer Docs
kind: hybrid
visibility: public
license: {content: CC-BY-4.0, code: MIT, attribution_required: true, attribution: "Cloudflare Developer Documentation (developers.cloudflare.com), CC BY 4.0"}
sources:
  - {type: llms_txt, url: https://developers.cloudflare.com/llms.txt, follow: per_product_llms_txt, page_format: markdown_suffix}   # fetch <page>/index.md
crawl: {respect_robots: true, require_content_signal: ai-input, allowed_hosts: [developers.cloudflare.com],
        deny_paths: [/client-ip-geolocation, /constellation, /cdn-cgi/, /email-security/], rps: 2, concurrency: 2}   # Disallow list from robots.txt
chunking: {split_on: [h1, h2, h3], target_tokens: 350, max_tokens: 700}
refresh: {mode: scheduled, interval: 7d, digest: {enabled: true, tier: cheap, data_class: trainable_ok}}
freshness: {warn_after: 14d, stale_after: 30d}
live: {mcp: cfdocs, tool: search_cloudflare_documentation, arg: query, use: supplement}
attach: {instructions: "Cloudflare: prefer Workers over Pages for new projects; use `wrangler preview`/`versions upload`, never `deploy`, from agent processes. Search the pack before asserting API details.", default_k: 5, roles: [Backend, DevOps]}
consult: {max_chunks: 8, route: trainable_ok}
```

Facts to hold when using it: the deployed site matching the repo HEAD is **unverified**; the `docs-mcp-rag` chunk size is unknown; Cloudflare says to start new projects on Workers and is moving tooling toward a `cf` CLI [tool §1c]. The `git_docs` source type (`cloudflare/cloudflare-docs`, branch `production`, `src/content/docs`) is an offline alternative that needs MDX handling; not specified here.

Microsoft docs (live): no crawl or sitemap facts were gathered, and Learn's commercial-use terms were not found, so v1 makes this a live pack only (content CC BY 4.0, code MIT; service terms unverified [tool §6]).

```yaml
schema: 1
id: msdocs
title: Microsoft Learn
kind: live
visibility: public
license: {content: CC-BY-4.0, code: MIT, attribution_required: true, attribution: "Microsoft Learn, CC BY 4.0"}
live: {mcp: mslearn, tool: microsoft_docs_search, arg: query, use: primary}   # also microsoft_docs_fetch{url}, microsoft_code_sample_search{query, language?}
attach: {instructions: "Microsoft/Azure questions: search Learn first; fetch the page for details before quoting.", default_k: 5, roles: [Backend, DevOps, Security]}
consult: {max_chunks: 8, route: trainable_ok}
offline: false          # no local copy; results are cached by the gateway for 24 h, not stored as pages
```

## 3. Storage

SQLite via `modernc.org/sqlite`; FTS5 with `bm25()`, `snippet()` and external-content tables with triggers were run on Linux (tokenizer `'porter unicode61'`) [go §2, §4]. Windows/NTFS re-run is pending (P2).

```sql
CREATE TABLE packs(id TEXT PRIMARY KEY, kind TEXT, visibility TEXT, version TEXT, manifest_hash TEXT, status TEXT, last_refresh_at INTEGER);
CREATE TABLE pack_pages(id INTEGER PRIMARY KEY, pack_id TEXT, url TEXT, title TEXT, content_hash TEXT, etag TEXT, last_modified TEXT,
  fetched_at INTEGER, first_version TEXT, last_version TEXT, status TEXT, risk INTEGER DEFAULT 0, raw_ref TEXT, UNIQUE(pack_id,url));
CREATE TABLE pack_chunks(id INTEGER PRIMARY KEY, pack_id TEXT, page_id INTEGER, ordinal INTEGER, heading_path TEXT, body TEXT,
  token_est INTEGER, chunk_hash TEXT, version_added TEXT, version_removed TEXT, risk INTEGER DEFAULT 0);
CREATE VIRTUAL TABLE pack_chunks_fts USING fts5(heading_path, body, content='pack_chunks', content_rowid='id', tokenize='porter unicode61');
-- AFTER INSERT/DELETE/UPDATE triggers keep the index in sync (pattern proven with the migrations tool [go §4])
```

Pack `version` is `YYYYMMDD.n`; chunks are never overwritten, they gain `version_removed`, so a citation to `cloudflare@20261008.1` resolves while that version is retained (keep the last 2 versions, or any version referenced by a stored citation). Requirements use the same code with separate tables (section 10).

## 4. Ingest and refresh

1. **Fetch** with Go `net/http` (polite rate, `If-None-Match`/`If-Modified-Since` from stored `etag`/`last_modified`), same-host redirects only, content-type and size caps, `robots.txt` and the content-signal check from the manifest. Pages the signal forbids are skipped and counted.
2. **Normalize** to Markdown (already Markdown for `index.md` and `llms.txt` sources), drop navigation and scripts, strip zero-width and control characters, record `content_hash = sha256(normalized)`.
3. **Diff** by `content_hash` per page: unchanged pages are only touched (`fetched_at`); new, changed and removed pages are queued. Chunk hashes (`sha256(heading_path + body)`) let unchanged chunks keep their rows.
4. **Chunk** heading-aware: split at H1 to H3, prefix each chunk's `heading_path` (`Workers > Wrangler > Commands > preview`), target 350 and cap 700 tokens (**design**, chars/4 estimate), keep fenced code blocks whole up to 1200 tokens and split longer ones by lines, never split inside a table row. Every chunk repeats its page title in `heading_path` so BM25 column weighting can use it.
5. **Scan** each chunk for injection (section 8) and secrets (gitleaks); set `risk`.
6. **Commit** a new pack version in one write transaction (single writer connection, `BEGIN IMMEDIATE` [decisions.md section 2]); emit `pack.page_changed` per changed page and `pack.refreshed` once.
7. **Digest** (optional): a cheap-model summary of changed pages (titles, diffs) for the UI. It sends public documentation text only, so a `trainable` route is allowed when `visibility: public`; a `private` pack uses a `private` route only (brief section 15, section 6). Skipped without a model; refresh never depends on it.

Refresh is scheduled (default 7 days for Cloudflare) or manual, runs with the app open (no background service), is resumable, and records failures as `pack.refresh_failed` without touching the previous version. Offline: indexed packs stay searchable; live packs report `unavailable`.

## 5. Retrieval

`search_pack(query, pack_ids?, k)` and CONSULT both call one function. Query construction avoids FTS5 syntax injection: split on non-word characters, quote every token (`"tok"`), keep identifiers containing `.` or `_` (such as `wrangler.toml`) as phrase queries, append `*` to the final token, AND the terms first, and if fewer than `k` hits come back retry with OR. Ranking is `bm25(pack_chunks_fts, 4.0, 1.0)` (heading path weighted above body; lower is better), restricted to `version_removed IS NULL` and the requested packs, deduplicated to at most 2 chunks per page, with `snippet()` highlights. The result is `found: false` only when no chunk matches any query term; there is no embedding or semantic index in v1 (brief section 15). Hybrid packs query the local index first; when it returns fewer than `k` or `live.use` is `supplement`, the gateway calls the live tool and merges results marked `origin: live`.

## 6. CONSULT

```go
type ConsultRequest struct{ PackIDs []string; Question string; CallerRunID string }
type Consult struct {
    Found bool; Answer string; Citations []Citation; Confidence string // high|medium|low
    SourceAgeDays int; Route string; Model string; Usage Usage
}
type Citation struct{ Pack, Version, URL, HeadingPath string; ChunkID int64; FetchedAt time.Time; ContentHash, Origin string }
```

Flow: (1) retrieve up to `max_chunks` per section 5; zero hits returns `Found=false` and the text "nothing found in <packs>" with no model call. (2) Route (below). (3) Prompt = fixed instruction ("answer only from the numbered sources; cite as [n]; if they do not contain the answer say so") + the question + untrusted-marked chunks; no tools, no repo content. (4) The model returns JSON `{answer, used:[n...]}`; Drydock rejects any `used` index outside the supplied set, requires at least one citation for `Found=true`, and renders citations from stored chunk metadata, never from model-written URLs. (5) `exact-match llm_cache` is allowed (no side effects), keyed by model plus prompt hash, which includes chunk ids and hashes, so a refresh invalidates it. `Confidence` is a **heuristic from code, not model self-report**: `high` when the top chunk matches all query terms and the source is within `warn_after`, `low` when only partial matches or stale or live-only results, otherwise `medium`. It is not calibrated; calibration against the eval harness is future work.

**Routing between trainable and private (brief section 6; D4, D13).** A `trainable` provider (for example an OpenRouter `:free` route) may be used only if all hold: every pack is `visibility: public` and `consult.route` allows it; the caller-supplied `Question` is at most 600 characters, has no fenced code, passes gitleaks, and the prompt contains nothing but the fixed instruction, the question and public chunks (no Context Engine sources, no diffs, room history or memory). Otherwise the request must use a `private` route (`provider:{data_collection:"deny", zdr:true}` on OpenRouter, direct Anthropic/OpenAI, or a self-hosted host), and fails closed if none is configured. If neither route is available or the quota ledger is exhausted (free models: 20 requests per minute, 50 or 1000 per day [prov §A]), CONSULT degrades to **retrieval-only**: the ranked chunks with citations and no synthesized answer. The router picks provider and model; this package states only the data-class requirement.

## 7. ATTACH, citations, freshness

ATTACH adds `attach.instructions` (about 400 tokens maximum, **design**) to the context package with a manifest entry (source, size, reason, priority; brief section 9), exposes `search_pack` restricted to the agent's attached packs, and for live or hybrid packs adds the live server's tools to the run allowlist ([`mcp.md`](mcp.md) section 3). Instructions say what the pack is for and to cite; they never contain crawled text.

Citation rules (enforced in code, brief section 15: "Answers must cite sources or say nothing was found"): `search_pack` and CONSULT return citations as structured data (`pack`, `version`, `url`, `heading_path`, `chunk_id`, `fetched_at`, `content_hash`, `origin`); agent output that quotes docs is encouraged to carry the same ids; the Room renders citation chips; exports (minutes, PR drafts) append each pack's `attribution` line once when `attribution_required` (CC BY 4.0 for Cloudflare and Microsoft content [tool §1b, §6]). Freshness: `source_age = now - fetched_at` for indexed results; live results show `origin: live` and age unknown upstream. At `warn_after` the chip shows an amber age; at `stale_after` results still return but with `refresh_suggested`, and the digest/refresh job is offered. Events: `pack.consulted`, `pack.stale`, `pack.refreshed`, `pack.page_changed`, `pack.refresh_failed`, `pack.attached`.

## 8. Injection handling

All pack, MCP and requirement content is untrusted data (brief section 16). Controls: (a) ingest scan flags chunks with instruction-like patterns (for example "ignore previous instructions", tool-call imitation, hidden HTML comments, long base64 or zero-width runs); `risk` is a score from pattern count, and chunks above a threshold are quarantined from retrieval until a human releases them. (b) Delivery wraps every chunk in the same `<untrusted ...>` markers as gateway results. (c) CONSULT uses a tool-less model call, so a successful injection can at worst change the answer text, which is shown with citations. (d) Retrieved text can never alter permissions, approvals or routing. (e) Crawlers never follow cross-host redirects and never execute content. These are heuristics; they reduce but do not remove risk, and the scan has no measured false-positive rate yet.

## 9. Skills

Skills are glob-scoped Markdown rules in `.aet/skills/` (brief section 9), resolved from repository scans and curated registries (never arbitrary GitHub), pinned to commits, scanned for secrets and injection, and delivered only when globs or roles match. Format is the Agent Skills specification (a directory with `SKILL.md`: YAML frontmatter plus Markdown; `agentskills/agentskills`, code Apache-2.0, docs CC BY 4.0) [tool §7]: required `name` (at most 64 chars, lowercase letters, digits, hyphens, no leading/trailing/double hyphen, equal to the directory name) and `description` (at most 1024 chars); optional `license`, `compatibility` (at most 500), `metadata` (string map), `allowed-tools` (space-separated, experimental); optional `scripts/`, `references/`, `assets/`. Guidance: body under 5000 tokens and 500 lines; about 100 tokens of metadata load at startup (progressive disclosure). Drydock extends only through `metadata` keys, so every file stays spec-valid:

```markdown
---
name: d1-migrations
description: Rules for editing D1 migration files and wrangler.jsonc bindings. Use when touching migrations or database bindings.
license: Apache-2.0
metadata:
  drydock.globs: "migrations/**/*.sql,wrangler.jsonc"
  drydock.roles: "Database,Backend"
  drydock.source: "registry:cloudflare-skills@<commit>"
---
Never edit an applied migration; add a new numbered file. ...
```

Drydock ignores `allowed-tools` (a skill can never widen permissions) and does not deliver `scripts/` unless the user approves each script (skills may carry executable content). A skill matches when any glob matches a path in scope (task file hints, repo-map focus, files touched) and, if `drydock.roles` is set, the agent's role matches.

`registry.yaml` is shipped curated and user-extendable only by an explicit action (with a warning); `pinned_commit` is filled at import. Seeded from [tool §7]; last-commit dates are **unverified** (github.com was unreachable in Phase 0), so none is asserted:

```yaml
schema: 1
registries:
  - {id: cloudflare-skills, repo: cloudflare/skills, root: skills/, license: Apache-2.0, trust: vendor_official, also: "docs site publishes /.well-known/agent-skills/* (index.json, SKILL.md, tarballs)", notes: "wrangler skill defers to cf CLI when cloudflare.config.ts exists"}
  - {id: stripe-ai, repo: stripe/ai, root: skills/, license: MIT, trust: vendor_official, notes: "synced from docs.stripe.com/.well-known/skills"}
  - {id: react-router, repo: remix-run/react-router, root: .agents/skills/react-router, license: MIT, trust: vendor_official, notes: "repo AGENTS.md files are contributor guidance, not app rules: exclude"}
  - {id: microsoft-skills, repo: microsoft/skills, root: .github/skills/, license: MIT, trust: vendor_official, import: selective_only, notes: "175 skills; repo warns loading all causes context rot"}
  - {id: msdocs-agent-skills, repo: MicrosoftDocs/Agent-Skills, license: CC-BY-4.0, trust: vendor_official}
  - {id: msdocs-mcp-skills, repo: MicrosoftDocs/mcp, license: "content CC BY 4.0, code MIT", trust: vendor_official, notes: "3 skills: microsoft-docs, microsoft-code-reference, microsoft-skill-creator"}
  - {id: vercel-agent-skills, repo: vercel-labs/agent-skills, license: MIT, trust: vendor_official}
  - {id: openai-skills, repo: openai/skills, license_scope: per_skill (LICENSE.txt), trust: vendor_official}
  - {id: anthropic-skills, repo: anthropics/skills, license_scope: per_skill, import: per_skill_review, notes: "no root LICENSE; some skills are source-available"}
  - {id: msgraph-community, repo: merill/msgraph, license: MIT, trust: community, notes: "no official Microsoft Graph skills repo found"}
```

Import pipeline: resolve to a commit (pin), fetch only the listed `root`, validate against the spec rules above (the spec's `skills-ref` validator is a separate tool; Drydock reimplements the checks in Go), check the license (per-skill where `license_scope: per_skill`; refuse unlicensed), gitleaks and injection scan, size caps, then store under `.aet/skills/<name>/` with `content_hash` and `skills` rows. Events: `skill.imported`, `skill.resolved`, `skill.delivered{method, fidelity}`, `skill.blocked{reason}`.

**Delivery per CLI capability.** Only Claude Code has native path-scoped rules; everything else is approximated by Drydock [cli §11, tool §7].

| CLI | Native scoping | Drydock delivery | Fidelity |
|---|---|---|---|
| Claude Code | `.claude/rules/**/*.md` with `paths:` frontmatter (key name inferred from the changelog, docs page unread); skills `.claude/skills/<n>/SKILL.md` with optional `paths:`; nested files load when Read/Write/Edit touches a file | Generate rules and skills natively | exact |
| Gemini CLI | none; `GEMINI.md` loads just-in-time for directories a tool touches | Directory-anchored globs (such as `src/api/**`) become a nested `GEMINI.md` in that directory; others via the `skills` tool | exact for directory-anchored globs, otherwise on-demand |
| Goose | none; nested `AGENTS.md`/`.goosehints` load when files in those directories are accessed (git repos) | Same nested-file technique | as Gemini |
| Codex | none; `AGENTS.md` from project root to cwd, deeper wins, 32768-byte limit; skills in `.agents/skills` or `.codex/skills` (docs unreadable) | Run-start selection: matched skills copied into the worktree's `.agents/skills/`; one line in `AGENTS.md` tells the agent to call `skills.for_paths` before editing matching files | run-start approximation plus on-demand |
| OpenCode | `instructions` globs in `opencode.json` select files to load, not per edited file; skills in `.opencode/skills`, `.claude/skills`, `.agents/skills` | Run-start selection via `instructions` plus `skills.for_paths` | run-start approximation plus on-demand |
| Antigravity | `rules.json` manifest and `.agents/` directories (details unverified) | `.agents/skills/` plus `skills.for_paths` | on-demand, low confidence |

On-demand delivery depends on the model calling the tool; it cannot be guaranteed, so `skill.delivered` records the method and whether the agent called `skills.for_paths`, and evals report coverage. The `skills` tool is in the Workspace server ([`mcp.md`](mcp.md) section 8). Generated nested instruction files are app-owned and carry the generator marker described in [`connections.md`](connections.md) section 14.

## 10. Requirements (brief section 11, "Requirements")

Requirements are uploaded or synced documents chunked into FTS5 and cited like a pack. Same ingest, chunking and retrieval code; separate tables `requirements`, `requirement_chunks`, `requirement_chunks_fts`; `visibility` is forced to `private` (never `trainable`). Sources: upload (Markdown or plain text in v1; PDF and DOCX conversion is unspecified), or sync from Notion/Linear/GitHub through `external_items` ([`integrations.md`](integrations.md)), each sync producing a new document version when `content_hash` changes and emitting `requirement.changed`. Citations use `REQ:<doc>@<version>#<heading_path>`. Tools: `search_requirements` (Workspace server); the spec gate (brief section 22) writes Given/When/Then criteria that link back via `requirement_links(requirement_id, task_id)`; tasks whose linked requirement version is superseded are flagged. Requirement text is untrusted data like any other external content, and requirements are never sent to `trainable` providers or CONSULT free routes. Events: `requirement.added`, `requirement.changed`, `requirement.linked`.

## 11. Open questions / unverified

- No pack has been crawled; Cloudflare's live `llms.txt` structure, Learn's crawlability and commercial-use terms, and docs-server result sizes are unverified (docs hosts were unreachable) [tool §1b, §6]. Whether storing Learn content locally is permitted is unknown, hence live-only.
- Chunk size targets, token estimates (chars/4), the BM25 column weights, the injection-pattern list and threshold, the 600-character CONSULT limit, and the confidence heuristic are design defaults with no measurements yet; all need the eval harness (brief section 23).
- The Claude `paths:` frontmatter key for rules is inferred from changelog wording, not a docs page [tool §7]. Whether Codex reads `.agents/skills` and `.codex/skills` is inferred from binary strings. Antigravity `rules.json` semantics are unknown. Nested-file scoping for Gemini and Goose rests on docs summaries and is untested.
- Skills registry last-commit dates, per-skill licenses, and the accuracy of the "175 skills" count are unverified. No official Microsoft Graph skills repo exists in the evidence.
- PDF/DOCX requirement ingestion is not specified; FTS5 behavior on NTFS with Defender is pending a Windows run (P2). Contradiction noted: brief section 15 lets the changed-pages digest use a cheap model, while section 6 restricts `trainable` providers to public-docs CONSULT; this document allows the digest on a `trainable` route only for `public` packs, an interpretation for the owner to confirm.

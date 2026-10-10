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

**As built.** The instructions go into the generated `AGENTS.md` ("Expert packs" section). Drydock starts Claude Code with `--setting-sources user` (decision P16), and then Claude Code does not read the project's `CLAUDE.md`, its `@AGENTS.md` import or `.claude/rules` on its own. The Claude adapter therefore reads those files from the working folder itself (`internal/agents/claude/project_memory.go`: `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md` with their `@` imports inside the folder, and `.claude/rules/*.md`; at most 64 KB a file and 256 KB in all) and appends them to the system prompt, passed in a file with `--append-system-prompt-file`. Settings files are never read.

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

Drydock ignores `allowed-tools` (a skill can never widen permissions; a list form is accepted too) and does not deliver `scripts/` unless the user approves each script (skills may carry executable content). A skill matches when any glob matches a path in scope (task file hints, repo-map focus, files touched) and, if `drydock.roles` is set, the agent's role matches.

**Keys outside the format (2026-10-09).** Agent products add their own top-level front matter keys (Claude Code's `context: fork` in the Microsoft Learn skills, `paths`, `argument-hint`...). The parser ignores and records them (at most 32) instead of refusing the file; the known fields keep their strict checks. Ignored keys are never acted on and never written into a worktree (Drydock rebuilds every delivered file from the known fields and the body). A registry skill is identified by its directory: when its front matter `name` differs (vercel-labs ships `vercel-react-best-practices` in `react-best-practices/`), the skill is stored under the directory name and the difference recorded; a repository skill must still match its directory. Both findings are kept in `skills.parse_notes` (migration `00102`) and shown as `notes`.

**Who gets a skill (2026-10-10, decision P35; replaces the per-agent assignment rule of 2026-10-09).** Every skill has an audience (`skills.audience`, migration `00109`). `team`, the default: every agent whose role its `drydock.roles` name (every agent when it names none), agents added later included, plus each agent it is given to (`agent_skills`, migration `00100`). `agents`: only the agents it is given to, whatever its roles say. Giving a skill to one more agent (`skill.assign {projectId, name, agentId, on}`, human only) never takes it from anyone; taking a chosen-agents skill from its last agent leaves it with nobody. `skill.audience {projectId, name, audience, agents}` sets both at once (the Skills page's "Change who gets"); an agent removed from the team is left out of the list. Agents are given skills when they are made (`agent.create` `skills`) or edited (`agent.update` `addSkills` and `removeSkills`); the names are checked before anything changes. Prebuilt agents and agent templates install their skills the same way (decision P27): a skill new to the project goes to the new agent only, one the whole team already gets for the newcomer's role stays shared, and any other skill already in the project is given to the newcomer as well. `skill.list` and `skill.get` return `audience` and `agents`. A run with no known agent receives what its role gets from team skills. Removing a repository skill drops its assignments. Globs still apply, and only accepted skills are ever delivered: giving a skill never accepts it.

Before P35 the first assignment narrowed a skill to its assigned agents (giving a QA skill to a backend agent took it from QA) and taking the last assignment away gave it back to its roles. Migration `00109` starts every skill that had an assignment as `agents`, so nobody gained or lost a skill on upgrade.

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

**As built for Claude Code.** The table's "exact" no longer holds. Claude Code runs with user settings only (decision P16), so it does not load `.claude/rules` itself; the adapter appends every `.claude/rules/*.md` file to the system prompt of every turn (see section 7, As built). A rule's `paths:` front matter is passed along as text and is not applied: each accepted skill planned for the agent is in full in every turn's prompt, whatever files the turn touches.

**Skill delivery `relevant` (decision P37, 2026-10-10).** A project chooses how Claude gets its skills (`DevSettings.SkillDelivery`, development settings "Skills for Claude agents"). `all` is the behaviour above. `relevant`, the default, makes a skill a `.claude/rules` file only when its globs match the task's file hints, or, for a task that names no files, files of the repository (fidelity `run_start`); every other skill, including every skill without globs, is one line of a "Skills" section in `AGENTS.md` (name, description up to 300 characters, its globs, its file) and its text is written to `.claude/drydock-skills/<name>.md` (method `claude_on_demand`, fidelity `on_demand`). That directory is not `.claude/rules`, so nothing loads it; the agent reads a file with the Read tool when its work needs it, the way Claude Code's own skills work. The files are generated, kept out of the diff and removed when no longer planned like the rules. Each turn's context manifest lists both kinds of file with their sizes and the total Claude gets in every turn against a 32 KB budget. The default was set from the trial in [`evals.md`](evals.md) section 14: with the prebuilt agents' twelve skills, `all` cost 1.9 times as much as no skills for no better acceptance, and `relevant` cost the same as none; agents opened a codebase-specific skill that was only listed in 2 of 6 runs, so skills whose globs match the repository load.

## 10. Requirements (brief section 11, "Requirements")

Requirements are uploaded or synced documents chunked into FTS5 and cited like a pack. Same ingest, chunking and retrieval code; separate tables `requirements`, `requirement_chunks`, `requirement_chunks_fts`; `visibility` is forced to `private` (never `trainable`). Sources: upload (Markdown or plain text in v1; PDF and DOCX conversion is unspecified), or sync from Notion/Linear/GitHub through `external_items` ([`integrations.md`](integrations.md); as built, nothing calls the sync entry point yet: a synced Notion page appears on the Integrations page as a requirements candidate and goes no further, so requirements come from uploads only), each sync producing a new document version when `content_hash` changes and emitting `requirement.changed`. Citations use `REQ:<doc>@<version>#<heading_path>`. Tools: `search_requirements` (Workspace server); the spec gate (brief section 22) writes Given/When/Then criteria that link back via `requirement_links(requirement_id, task_id)`; tasks whose linked requirement version is superseded are flagged. Requirement text is untrusted data like any other external content, and requirements are never sent to `trainable` providers or CONSULT free routes. Events: `requirement.added`, `requirement.changed`, `requirement.linked`.

## 11. Open questions / unverified

- No pack has been crawled; Cloudflare's live `llms.txt` structure, Learn's crawlability and commercial-use terms, and docs-server result sizes are unverified (docs hosts were unreachable) [tool §1b, §6]. Whether storing Learn content locally is permitted is unknown, hence live-only.
- Chunk size targets, token estimates (chars/4), the BM25 column weights, the injection-pattern list and threshold, the 600-character CONSULT limit, and the confidence heuristic are design defaults with no measurements yet; all need the eval harness (brief section 23).
- The Claude `paths:` frontmatter key for rules is inferred from changelog wording, not a docs page [tool §7]. Whether Codex reads `.agents/skills` and `.codex/skills` is inferred from binary strings. Antigravity `rules.json` semantics are unknown. Nested-file scoping for Gemini and Goose rests on docs summaries and is untested.
- Skills registry last-commit dates, per-skill licenses, and the accuracy of the "175 skills" count are unverified. No official Microsoft Graph skills repo exists in the evidence.
- PDF/DOCX requirement ingestion is not specified; FTS5 behavior on NTFS with Defender is pending a Windows run (P2). Contradiction noted: brief section 15 lets the changed-pages digest use a cheap model, while section 6 restricts `trainable` providers to public-docs CONSULT; this document allows the digest on a `trainable` route only for `public` packs, an interpretation for the owner to confirm.

## As built (Slice 3, 2026-10-08, Linux sandbox)

Code: `internal/packs` (packs, chunker, fetcher, scanner, CONSULT/ATTACH, workspace provider), `internal/requirements` (requirements, links, traceability, milestones, `search_requirements` provider), `internal/skills` (skills, registries, delivery planner, `skills` provider), hooks in `internal/context` (`hooks.go`: `Sections` and `Files`), `internal/memory/manage.go`, and the extension `internal/core/ext_packs.go` (methods `pack.*`, `requirement.*`, `milestone.*`, `skill.*`, `memory.*`). Migrations `00050_packs.sql`, `00051_requirements.sql`, `00052_skills.sql`. Tests use real SQLite with FTS5, real files, a real Git repository, the real gitleaks scanner and the real `internal/mcp` gateway with a go-sdk MCP client. Where a test needs an HTTP peer it uses a **labeled protocol fixture** (a local server speaking robots.txt, `llms.txt`, sitemaps, ETags, redirects, the Cloudflare docs MCP result shape, the GitHub API and codeload shapes). The real docs sites, the real MCP docs servers and GitHub were not contacted.

**What works and is tested.**
- Manifest: strict `pack.yaml` (unknown keys, a second document, http, credentials in URLs, hosts outside `crawl.allowed_hosts`, `respect_robots: false`, cross-host redirects, a private pack with a trainable route, over-long or injection-shaped `attach.instructions`, unknown roles are all rejected). The two example manifests of section 2 ship as templates (`pack.templates`) and parse; installing one fetches nothing.
- Ingest: sources `local_dir` (no symlinks followed), `url_list`, `llms_txt` (including `per_product_llms_txt`; `llms-full.txt` dumps are skipped), `sitemap` (path-prefix filter); page formats Markdown, HTML (a small built-in converter, not a general parser) and MDX (imports and bare JSX lines stripped). Conditional GET with stored ETag/Last-Modified; per-page and per-chunk SHA-256; heading-aware chunking (code blocks atomic up to 1200 tokens and re-fenced when split, tables split only between rows with the header repeated); secrets redacted with gitleaks before chunking; hidden characters stripped and counted; injection score per chunk with quarantine at 3 (human release; a release survives refreshes of an unchanged chunk).
- Fetcher: https only, host allowlist, deny paths, robots.txt (named group beats `*`, longest match, wildcards, 5xx means disallow-all) and an explicit `Content-Signal` grant when the manifest requires one, rate limit, size cap, text-only content types, total timeout, same-host redirects (3 max). **SSRF**: the address is checked at connect time (loopback, RFC 1918, link-local including 169.254.169.254, CGNAT, multicast, reserved, IPv4-mapped and 6to4 forms), which also covers DNS rebinding. No proxy is used (see below).
- Versions `YYYYMMDD.n`; chunks are retired, not overwritten; the last two versions plus pinned ones are retained; a citation `pack@version` resolves while retained (`pack.citation`). Refresh is one write transaction; failure keeps the previous version, sets `status: failed` and emits `pack.refresh_failed`; more than half the pages failing, or nothing loadable, aborts. Changed-pages digest is built from line diffs without a model; the optional `Summarizer` runs only for a public pack whose digest class is `trainable_ok`. Scheduler (`Due`, `RunDue`, `Start`) runs while the app is open, retries a failed pack after one hour. Offline: an indexed pack answers from SQLite with no network (tested with the site closed).
- Retrieval: FTS5 `bm25(pack_chunks_fts, 4.0, 1.0)`, AND pass then OR pass, quoted terms, identifiers as phrases, stop words dropped, at most two chunks per page, quarantined and retired chunks excluded; FTS5 syntax in a query is inert (tested).
- CONSULT: retrieval-only answer assembled from the top chunks with `[n]` markers, citations from stored metadata (pack, version, URL, heading path, chunk id, fetched-at, chunk hash, page hash, origin), heuristic confidence, source age, refresh/warn/unavailable lists, attribution lines. `nothing found in <packs>` with no model call when nothing matches. The optional `Answerer` receives the fixed instruction, the question and untrusted-wrapped sources and the `AllowTrainable` flag computed by `TrainableEligible` (all packs public and `trainable_ok`, question at most 600 characters, no code fence, passes gitleaks); its output is rejected (CONSULT degrades to retrieval-only with `degraded` set) when it cites nothing, cites a source that was not supplied, returns no text or uses a trainable route that was not allowed.
- ATTACH: per project, optionally per agent or role (`pack_attachments`); instructions go into the generated `AGENTS.md` as an "Expert packs" section through the new context hook (crawled text never does); `Store.AllowFor(...).Apply(&policy)` yields the `search_pack` and live-tool entries for a run policy. `search_pack` (`packs.NewWorkspaceProvider`) reads only packs attached to the calling run's project/agent/role and says so when none is.
- Live packs: queried through the existing gateway, not around it. Each search opens a short-lived service run whose policy allows exactly one tool of one catalog server, calls it as an MCP client on the run's loopback endpoint (so allowlist, egress rules, cache, scan and untrusted marking apply: a query with a code fence is refused, a repeated query is a cache hit), and closes the run. A refresh invalidates the gateway cache entries of the pack's server. Results with an injection score at or above the threshold are withheld and counted. Hybrid merging reserves slots for a supplementing server.
- Requirements: Markdown, text or pasted PDF text (hard-wrap and page-number cleanup, numbered and ALL-CAPS headings); binary PDF/DOCX refused with a message; secrets redacted before storage; versions per content hash; chunks of older versions stay so `REQ:<doc>@<version>#<heading path>` keeps resolving; `Sync` upserts by `(project, source_ref)` for integrations (callers `integration:<id>`, `system` or a human; there is no RPC for it); links of Given/When/Then criteria to tasks (`LinkCriteria`, defaults to the task's own `Acceptance`), stale detection when the version moved on, `Trace` (requirement -> tasks -> latest test evidence, `Verified` only when no link is stale and every linked task's latest test evidence passed), `Coverage`. Milestones with task membership, progress by task state, overdue, close (needs `force` with unfinished tasks), reopen, delete.
- Skills: `SKILL.md` parsing per the spec rules (name equals the directory, lengths, front-matter keys outside the format ignored and recorded since 2026-10-09 (section 9), `allowed-tools` recorded and ignored, `scripts/` recorded and never delivered); globs with `**`, braces, anchoring, a pattern without `/` matching the file name at any depth (a design choice, gitignore style); repository scan of `.aet/skills/` in the project's main checkout (symlinks skipped, at most 200 skills); every repository skill is `offered` until a human accepts it (decision P11; the core sets `ManualAcceptRepoSkills`); with the setting `skills.manual_accept_repo = 0` a skill that is committed and unmodified at HEAD and passes the scan is accepted automatically and pinned to the commit that last touched it, and anything else stays `offered`. The registry allowlist is the table `skill_registries`, seeded from `registry.yaml` (the ten curated entries of section 9); a human adds (with an explicit acknowledgement, always `community` trust), disables or removes entries; curated entries can only be disabled. Import resolves a ref to a full commit, fetches the registry tarball through the allowlisted fetcher (GitHub hosts only), validates, license-checks (`per_skill` registries need a license in the front matter or a `LICENSE` file; unlicensed is blocked), scans (a secret blocks and the text is not stored; an injection score or any hidden character blocks), and stores the skill as `offered` or `blocked`. Only `accepted` skills are ever planned into a worktree or served by the tool; a changed skill drops back to `offered`; a new commit with identical text keeps the decision and updates the pin.
- Delivery (`Service.Plan`): Claude gets `.claude/rules/<name>.md` with `paths:` front matter (no `paths:` key when a skill has no globs), which the Claude adapter appends to the system prompt in full on every turn because Claude Code runs with user settings only (section 9, As built for Claude Code); Gemini and Goose get nested `GEMINI.md`/`AGENTS.md` for skills whose globs are all directory-anchored, the rest on demand; Codex, OpenCode, Antigravity and custom CLIs get a run-start copy to `.agents/skills/<name>/SKILL.md` for skills matching the task's file hints (and global skills) plus a sentence in `AGENTS.md` telling the agent to call `skills` with `for_paths`. Unknown adapter means Claude. The context engine writes the files, never overwrites a file of the repository's own (no generator marker), refuses unsafe paths and symlinks on the way, removes stale generated rules, reports them in `WriteResult.Extras` so the pipeline's `LocalIgnore` keeps them out of the diff (tested in the real task loop). Every written file and every tool delivery is a `skill_deliveries` row plus `skill.delivered{method, fidelity}`; `skill.coverage` reports files versus tool deliveries.
- Memory UI methods: list, get, search (any status, any project), propose (kind validated, secrets redacted, never accepted by the author), accept and reject (human principal only, also enforced in `Knowledge.DecideMemory`), delete; events `memory.*`. `search_memory` provider (`Knowledge.Workspace().Memory`) serves accepted entries of the run's project, or of every project for `scope: workspace`.
- Human-only mutation is enforced in the services with `security.RequireHuman`, not only in the RPC layer; the RPC methods act as `Core.Human()`. Audit rows (hash chain) are written for pack install/remove/attach/detach/release, requirement add/update/archive/release, skill import/accept/reject/release/registry edits and memory accept/reject/delete.

**Differences from the text above.**
- Section 3 schema was extended: `packs` keeps the manifest text, `version_seq`, `last_success_at`, `last_error`; `pack_versions` (history and digest), `pack_attachments`, `pack_pins`; chunks have integer `seq_added/seq_removed` (the `YYYYMMDD.n` strings do not sort once `n >= 10`), `flags`, `quarantined`, `released_by`. Requirements have `requirement_versions` and per-version chunks (`current` flag) because old citations must resolve.
- Manifest extensions: `sources[].urls` (for `url_list`), `crawl.max_pages` (default 500, cap 5000), `crawl.timeout`, `crawl.refresh_budget`. `crawl.respect_robots: false` is an error, `crawl.allowed_hosts` is required for URL sources, `require_content_signal` needs an explicit `yes` in robots.txt (absent means not granted), `git_docs` is rejected at install ("not built"). `attach.roles` are the roles `pack.attachDefaults` attaches to; they are not enforced elsewhere.
- Confidence is exactly: `low` for live-only results, a top hit that matched only some terms, or a source older than `stale_after`; `high` when the top hit matched every term and is within `warn_after`; otherwise `medium`.
- Injection scoring (weights, threshold 3) is its own table in `packs/risk.go` rather than the gateway's labels, because the gateway's patterns would flag ordinary documentation ("you should run the curl command"); skills treat any hidden character as blocking.
- The `search_pack` result type `mcp.PackHit` has no origin, confidence or content-hash fields, so the hash is carried in `chunk_id` as `<id>#<first 12 hex of the chunk hash>` (`live#<hash>` for live results) and the text of one hit is cut at 2000 characters. The structured `Consult` result has everything.
- The exact-match `llm_cache` for CONSULT is not implemented in `packs`: the `Answerer` that wraps the LLM client owns caching (its key must include chunk ids and hashes, as specified).
- Live results from servers other than Cloudflare docs are parsed generically (JSON `results` arrays with url/title/text, otherwise one hit whose URL is `mcp://<server>/<tool>`); Microsoft Learn's real result shape is unverified.
- Wired (2026-10-08): the Core now owns a shared MCP gateway (`internal/core/ext_mcp.go`) built with `Knowledge().Workspace()`, and `Knowledge().UseGateway(g)` points live packs at it, so the private on-demand gateway of this extension is no longer started. Every agent turn of a verified adapter (Claude today) gets a run on that gateway whose policy allows `search_pack` only when `Packs.AllowFor(project, agent, role)` reports an attached pack; only the `search_pack` part of the allowance is applied (`Apply`'s live-docs-server entries are not: agents have no direct docs-server access, `search_pack` queries live packs itself). A real Claude turn called `search_pack` through the generated config and quoted pack, version and URL (internal/core `TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig`); see [`mcp.md`](mcp.md) "As built". The default per-role policy is the minimal one described there, not a per-role table. Roles are matched exactly as attached (`pack.attach` role), and a review turn runs as role `reviewer`, so a pack attached to `Backend` is not searchable from that agent's review turns.
- Sections 10 and 8: requirements are chunked by the pack chunker; quarantine applies to them too (a requirement that quotes an attack string is held back until released).

**UI (built since).** The sidebar's Knowledge section has Expert packs (`/packs`: install, refresh, attach and detach, search, CONSULT, quarantined passages to release, remove), Requirements, Skills (repository scan, registries, import, accept, reject, release, deliveries and coverage) and Memory; Milestones is in the Project section (`ui/app/screens`).

**Not built.** PDF/DOCX parsing (only pasted text); `git_docs` sources; an embedding or semantic index (none planned in v1); the LLM-backed `Answerer` and `Summarizer` (interfaces only, no provider route is wired); OpenCode `instructions` globs in `opencode.json`; per-skill script approval (scripts are never delivered); reading `references/` and `assets/` of a skill (only `SKILL.md` is stored); an HTTP proxy for the crawler (corporate proxies are unsupported because the connect-time address check cannot see through a proxy; `FetchPolicy.UseSystemProxy` exists and is off); Notion/Linear/GitHub requirement sync itself (the `Sync` entry point exists for the integrations worker); per-role default allowlists beyond the minimal one in [`mcp.md`](mcp.md) (read-only tools for every role, `search_pack` when a pack is attached); `search_pack` for any CLI other than Claude (their MCP configs are unverified, so they get no MCP).

**Unverified.** Everything on Windows (NTFS with Defender for FTS5, symlink refusal tests skip where symlinks are unavailable, path handling); the live structure of `developers.cloudflare.com` (`llms.txt`, per-product lists, `<page>/index.md`, robots Content-Signal), Learn's crawlability and terms, the real result shapes of both docs MCP servers; GitHub's API and codeload behaviour (fixture only; unauthenticated rate limits apply); the Claude `paths:` front-matter key, Codex/OpenCode/Antigravity reading `.agents/skills`, and Gemini/Goose loading nested instruction files just in time; chunk sizes, BM25 weights, stop words, the injection pattern list, weights and threshold (no measured false-positive rate), the 600-character limit and the confidence heuristic (no eval run); `skill.delivered` coverage numbers (nothing has run with a real agent that calls `skills`). Race detector: passed for packs, requirements, skills, context and memory; the core suite passes except that `TestRealClaudePlansATinyGoal` (needs a logged-in Claude) and a timing-sensitive plan test of another worker were not mine to judge (the latter passes when run alone).

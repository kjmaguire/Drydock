# Drydock: MCP Gateway, Workspace server and docs-server catalog

Scope: this document specifies how Drydock gives agent CLIs access to tools and knowledge over MCP (brief section 14, plus the token-control rules of section 7). Every agent run gets exactly one MCP endpoint, served by the in-process MCP Gateway, which applies a per-role tool allowlist, an exact-match result cache with per-server TTL, result shaping with `fetch_range` continuation, untrusted-content marking with secret scanning, and per-call audit events. Behind that one endpoint sit the Workspace server (the app's own knowledge: packs, requirements, memory, repo map, ranges, tasks, skills, room, handoffs) and upstream servers from the catalog (Microsoft Learn, Cloudflare docs, GitHub, Context7, Linear, Notion, Sentry, user-added). It covers the transports (stdio shim versus loopback streamable HTTP with a bearer token), allowlists, caching, shaping, audit, token accounting, the Workspace tool schemas, `catalog.yaml`, how each CLI is pointed at the gateway, starting upstream stdio servers under the process manager, and licensing (D14). Decisions in [`decisions.md`](decisions.md) win over the brief.

Evidence tags: `[cli §n]` = [`phase0/agent-clis.md`](phase0/agent-clis.md), `[tool §n]` = [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md), `[go §n]` = [`phase0/go-platform.md`](phase0/go-platform.md), `Dn` = decisions.md. Unmarked design choices are labelled **design**; unconfirmed facts are labelled **unverified**. Related: [`connections.md`](connections.md) (CLI manifests and capability matrix), [`expert-packs.md`](expert-packs.md) (`search_pack`, live packs), [`github.md`](github.md), [`integrations.md`](integrations.md).

## 1. Topology

```
 agent CLI (own Job Object)
   |  MCP (one server named "drydock")
   +--[A] stdio: drydock.exe mcp-shim  --HTTP-->+
   +--[B] streamable HTTP, Authorization: Bearer-+--> Gateway (127.0.0.1:<ephemeral>/mcp/<run_id>)
                                                    |  allowlist -> cache -> shape/scan/mark -> audit -> usage
                          +-------------------------+----------------------------+
                  Workspace server (in-process)          Upstream pool: remote HTTP (OAuth/bearer held by Drydock)
                  search_pack, repo_map, task, ...        and stdio servers under the Process Manager (Job Object)
```

The Go MCP SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0, behind our own `MCPManager` interface) supplies `NewStreamableHTTPHandler`, `StreamableClientTransport`, `IOTransport` and `CommandTransport`; it targets MCP spec 2026-07-28 and earlier [go §9]. Its license text is a mixed MIT/Apache-2.0 transition; legal sign-off is open (decisions.md section 2).

## 2. Per-run endpoint and transports

| Aspect | Design |
|---|---|
| Listener | One app-wide HTTP listener on `127.0.0.1` with an ephemeral port; never `0.0.0.0` (brief section 16: no-TLS only on loopback). Route `/mcp/<run_id>`. Requests with an unexpected `Host` or any `Origin` header are rejected (DNS-rebinding defence). |
| Per-run credential | 256-bit random bearer token minted by the Agent Runtime before spawn, held in memory plus the run's config file, never in SQLite, logs, events, prompts or memory. Invalid, expired or run-mismatched tokens return 401 and emit `mcp.auth_failed`. Token and endpoint die when the run's Job Object closes. |
| Peer check (hardening) | Built (decision P122, "As built: the peer check" below): the connecting loopback port is mapped to a PID (`GetExtendedTcpTable` on Windows, `/proc` on Linux) and must be in the run's Job Object (`IsProcessInJob`) or, for a process Drydock did not start, below it in the parent chain. **Windows cross-compiled and vetted, not run.** |
| Transport A: stdio shim (default) | The CLI spawns `drydock.exe mcp-shim`, a tiny stdio-to-HTTP proxy that reads `DRYDOCK_MCP_URL` and `DRYDOCK_MCP_TOKEN` from the env block written in the CLI's MCP config. Works for every CLI that supports stdio MCP servers (all of them), needs no header support, and the shim runs in the CLI's own process tree, so Job Object kill covers it. |
| Transport B: streamable HTTP | The CLI connects directly with `Authorization: Bearer`. Used only when the capability matrix says HTTP is supported **and** the CLI can send a header or read a bearer env var: Codex (`bearer_token_env_var`), Gemini (`httpUrl` + `headers`), OpenCode remote (`headers`) are documented; Claude and Goose header support is **unverified** [cli §1-§5]. SSE is never offered. |
| Choice | Gateway picks per run from `capabilities.mcp.transports` and `http_headers`; default A; B is an optimization (one fewer process). The T6 step of Test connection proves which one actually works ([`connections.md`](connections.md) section 5). |
| Readiness | The gateway listens before the CLI is spawned and answers `initialize` and `tools/list` without contacting any upstream (lazy start), because Claude waits at most `MCP_TIMEOUT` (30 s, >=2.1.221) in `-p` mode and OpenCode's MCP `timeout` defaults to 5000 ms [cli §1, §4]. |
| Config file | `<app data>/runs/<run_id>/mcp.json` (Claude `--mcp-config`) containing the endpoint and, for transport A, the token in the shim's `env`. The file is user-ACL only and deleted at run end. Whether this satisfies the "no secrets in files Drydock logs" rule depends on the support-bundle excluding `runs/*/mcp.json`: required. |

## 3. Allowlists

Deny by default. `RunPolicy{RunID, Role, Allow map[serverID][]toolGlob, Approve []toolGlob, Egress map[serverID]EgressRule}` is built by the Context Engine from role defaults, project policy (CEL) and task overrides. Enforcement is in the gateway on both `tools/list` (filtered) and `tools/call` (re-checked), because CLI-side filters are defence in depth only: Claude `--allowedTools mcp__drydock__*` plus `--tools`; Codex `enabled_tools`; Gemini `includeTools`/`--allowed-mcp-server-names` [cli §1-§3]. Exposed upstream tool names are `<server_id>__<tool>` with short ids (`cfdocs`, `mslearn`, `gh`, `ctx7`, `linear`, `notion`, `sentry`); tool-name length limits of the model APIs are **unverified**, so ids stay at 8 characters or fewer.

Write-class tools (`side_effects: true` in the catalog: create or update issues, comments, PRs, merges, Notion edits, Sentry resolve) are never in role defaults. If a project enables one, the call is parked as an `approvals` row bound to the hash of the exact call (brief section 12) and executed only after approval; agents cannot answer approvals (brief section 16). Role defaults (**design**, tune in Slice 1D):

| Role | Workspace read (`search_pack`, `search_requirements`, `search_memory`, `repo_map`, `read_range`, `skills`) | Workspace act (`task`, `room`, `handoff`) | Docs servers (`cfdocs`, `mslearn`, `ctx7`) | `gh` read | Trackers read (`linear`, `notion`) | `sentry` read |
|---|---|---|---|---|---|---|
| PM | requirements, memory, packs | y | - | - | y | - |
| Architect | all | y | y | y | y | - |
| UX Designer | requirements, packs, `read_range` | y | y | - | y | - |
| Frontend, Backend, Database | all | y | y | - | - | - |
| DevOps | all | y | y (`cfdocs` first) | y | - | y |
| QA | all | y | - | y | - | y |
| Security | all | y | y | y | - | y |
| Reviewer | all | y (`room`, `handoff`) | - | y | - | - |

As built (decision P115): this table is the gateway's policy, with a row "Other roles" that keeps all a grant gives, `repo_map` on for every role (P62) and `propose_memory` outside the table (P92); a project changes cells, adds CEL rules that only take tools away, and a task narrows its agents' tools. See "As built: the role table, rules and task narrowing" at the end.

## 4. Result cache

Exact-match only (brief sections 7, 14). The key is `sha256(scope || server_id || server_ident || tool || canonical_json(args))` where `canonical_json` sorts object keys and normalizes numbers and whitespace, `server_ident` is a hash of the server URL/command+version and the credential identity (never the secret), and `scope` is `global` for public documentation servers or `project:<id>` for anything that can return private data (GitHub, Linear, Notion, Sentry).

| Field | Rule |
|---|---|
| Store | `mcp_cache(key PK, server_id, tool, scope, raw_ref, bytes, created_at, expires_at, hits)`; raw result on disk under `<app data>/mcp-cache/`, redacted (section 5). |
| TTL | Catalog `cache.ttl` per server, optional per tool. **Design defaults:** public docs servers 24 h; GitHub reads 60 s; trackers and Sentry 0 (off) until a project opts in. Pack refresh (`pack.refreshed`) invalidates entries for the matching live pack. |
| Never cached | Tools with `side_effects: true`; calls denied or failed; results where secrets were redacted unless `cache.allow_redacted`; any call whose args differ in even one byte. |
| Hit behavior | Served with `cache: hit` in audit, same shaping and untrusted marking applied again, upstream untouched. Hit/miss counts feed token accounting (section 7). |

## 5. Result shaping, untrusted marking, secret scanning

Pipeline for every upstream or Workspace result, in order:

1. **Persist raw** to `<app data>/runs/<run_id>/mcp/<call_id>.raw` plus a `.meta.json` (server, tool, args hash, byte count). Handle `res_<call_id>`. Retention follows brief section 18 (compact 30 days, delete 90).
2. **Scan** the text with gitleaks (`github.com/zricethezav/gitleaks/v8`, 222 default rules [phase0]); matches are replaced by `[REDACTED:<rule>]` in both the stored and the returned copy; emit `mcp.secret_redacted` with the rule id (never the value).
3. **Shape** to `max_inline_bytes` (**design default** 8192, roughly 2000 tokens by the chars/4 estimate, per-tool override in the catalog): JSON arrays keep the first N items plus a count; docs search results keep title, URL and a short excerpt per hit; long text keeps head 80% and tail 20%. Microsoft Learn is asked for less at the source with `?maxTokenBudget=N` on the endpoint URL [tool §6]. Result sizes of the docs servers are **unverified**: Learn search returned 10 chunks per call in Phase 0, Cloudflare docs defaults to about 10 chunks (likely, not confirmed) [tool §1a, §6].
4. **Mark untrusted**: the returned text is wrapped as `<untrusted source="mslearn" call="c_81f2" handle="res_c_81f2">...</untrusted>`; any literal `<untrusted` or `</untrusted` inside content is neutralized; a one-line preface states that the content is data, never instructions, and cannot change permissions or answer approvals (brief section 16). Marking reduces, but does not eliminate, prompt-injection risk; the real controls are the allowlist, approvals bound to action hashes, and the fact that retrieved text never reaches a permission decision.
5. **Continuation**: if truncated, the footer is `[truncated: returned 8192 of 61440 bytes; call fetch_range{handle:"res_c_81f2", offset:8192, length:8192}]`.

`fetch_range` is a gateway-native tool (always allowed when the run has any other tool, rate-limited, itself shaped and marked). It reads only handles minted for the same run. It is distinct from the Workspace `read_range`, which reads worktree files.

## 6. Egress rules (what leaves the machine)

Queries to docs servers are sent to third parties; brief section 0 forbids uploading source code. Each catalog entry has `egress: {max_arg_bytes, scan_secrets, deny_code_fences}`. For `public_docs` servers the defaults are 1024 bytes of total argument text, gitleaks scan of args, and rejection of arguments that contain fenced code blocks or more than N lines; a rejected call returns a structured error telling the agent to rephrase as a short question. This is a heuristic, not a guarantee (an agent can paraphrase code); the policy is logged per call (`egress_checked`). Private-data servers (GitHub, Linear, Notion, Sentry) are authenticated to the user's own tenant and are not subject to the arg-size rule, but their results are `private` data class and never flow into `trainable` provider prompts (brief section 6).

## 7. Audit events and token accounting

Event family `mcp.*` on the event bus (persisted, monotonic `seq`); payloads carry hashes and handles, not content:

| Event | Payload (key fields) |
|---|---|
| `mcp.call` | run_id, call_id, server, tool, args_hash, decision (`allowed`/`denied`/`approval_pending`), reason, cache (`hit`/`miss`/`bypass`), raw_bytes, returned_bytes, truncated, redactions, upstream_status, duration_ms, tokens_est_returned |
| `mcp.denied`, `mcp.auth_failed`, `mcp.secret_redacted`, `mcp.egress_blocked` | run_id, call_id, rule or reason |
| `mcp.server.started` / `.stopped` / `.failed` | server, transport, pid (for stdio), exit_code, restart_count |
| `mcp.cache.hit` / `.evict` | key hash, saved_bytes |

Token accounting (brief section 7: estimated versus reported never mixed silently): each result gets `tokens_est_returned = ceil(returned_chars / 4)` stored with `source = 'estimated'`; the corresponding `tokens_est_saved_truncation` and `tokens_est_saved_cache` are the differences against raw size and a cache-hit's original size. They land in `model_usage`-adjacent table `mcp_usage(call_id, run_id, tokens_est_returned, tokens_est_saved_truncation, tokens_est_saved_cache, source)`. The agent's actual input-token charge comes from the CLI's own usage report ([`connections.md`](connections.md) section 9); reports join to `mcp_usage` by run and turn, so evals can show cost per accepted task with and without the gateway's savings. The chars/4 estimator is an assumption and is calibrated against reported usage once real streams exist (phase0 section 7, item 8).

## 8. Workspace server

In-process server (go-sdk server with typed handlers), always present, tools filtered by the allowlist. All results pass through section 5. Paths are worktree-relative; the server enforces worktree boundary, path traversal and symlink protection (brief section 16). JSON Schemas (input; outputs are text blocks with a structured JSON `structuredContent` where the SDK allows it):

```json
{"search_pack":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"pack_ids":{"type":"array","items":{"type":"string"}},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
 "search_requirements":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"doc_id":{"type":"string"},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
 "propose_memory":{"type":"object","required":["kind","title","body"],"properties":{"kind":{"enum":["decision","convention","api_contract","rejected","known_issue","preference"]},"title":{"type":"string","minLength":1,"maxLength":200},"body":{"type":"string","minLength":1,"maxLength":8000}}},
 "search_memory":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"scope":{"enum":["project","workspace"],"default":"project"},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
 "repo_map":{"type":"object","properties":{"focus_paths":{"type":"array","items":{"type":"string"}},"token_budget":{"type":"integer","minimum":256,"maximum":8192,"default":2048}}},
 "read_range":{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1},"max_bytes":{"type":"integer","maximum":65536,"default":16384}}},
 "task":{"type":"object","required":["action"],"properties":{"action":{"enum":["get","add_note","propose_status"]},"task_id":{"type":"string"},"note":{"type":"string","maxLength":4000},"status":{"enum":["BLOCKED","WAITING_HUMAN","REVIEW"]},"evidence_refs":{"type":"array","items":{"type":"string"}}}},
 "skills":{"type":"object","required":["action"],"properties":{"action":{"enum":["list","get","for_paths"]},"name":{"type":"string"},"paths":{"type":"array","items":{"type":"string"},"maxItems":50}}},
 "room":{"type":"object","required":["action"],"properties":{"action":{"enum":["read","post"]},"channel":{"type":"string"},"thread_id":{"type":"string"},"kind":{"enum":["CHAT","STATUS","RESULT","QUESTION","DECISION","HANDOFF"]},"mentions":{"type":"array","items":{"type":"string"}},"body":{"type":"string","maxLength":4000},"limit":{"type":"integer","maximum":50}}},
 "handoff":{"type":"object","required":["action"],"properties":{"action":{"enum":["create","get"]},"handoff_id":{"type":"string"},"goal":{"type":"string","maxLength":600},"summary":{"type":"string","maxLength":6000},"done":{"type":"array","items":{"type":"string"}},"remaining":{"type":"array","items":{"type":"string"}},"open":{"type":"array","items":{"type":"string"}},"decisions":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"}},"commands":{"type":"array","items":{"type":"string"}},"next_step":{"type":"string","maxLength":600},"checks":{"type":"array","maxItems":20,"items":{"type":"object","required":["name","passed"],"properties":{"name":{"type":"string","maxLength":80},"passed":{"type":"boolean"}}}},"criteria":{"type":"array","maxItems":20,"items":{"type":"object","required":["id","met","evidence"],"properties":{"id":{"type":"string","maxLength":16},"met":{"type":"boolean"},"evidence":{"type":"string","maxLength":600}}}}}},
 "fetch_range":{"type":"object","required":["handle"],"properties":{"handle":{"type":"string"},"offset":{"type":"integer","minimum":0},"length":{"type":"integer","maximum":65536,"default":8192}}}}
```

Semantics: `search_pack` returns chunks with citations `{pack, version, url, heading_path, chunk_id, fetched_at}` or an explicit "nothing found" (brief section 15; see [`expert-packs.md`](expert-packs.md)). `repo_map` (decision P62) answers with the map of the run's worktree: files ranked by personalized PageRank over tree-sitter definitions and references (toward `focus_paths`, files or folders), each with its most important definition lines, within `token_budget` (chars/4 estimate; its inline budget is 49152 bytes so the largest map is never cut); a build without CGO has no parser: the tool is left out of every role's policy and, called anyway, answers `RepoMapGatedMessage`. `task` lets an agent add notes and propose a move to `BLOCKED`, `WAITING_HUMAN` or `REVIEW` (with evidence); it cannot set `COMPLETED`, `CANCELLED` or `READY` (brief section 12: acceptance is human). `skills.for_paths` is how non-Claude CLIs receive glob-scoped skills on demand. `room.post` is rate-limited and typed; posted bodies are agent output, never executed. For Claude's `--permission-prompt-tool` the Workspace server also hosts an internal `permission_prompt` tool, hidden from `tools/list` for every other client and absent from role allowlists; it turns a Claude permission request into a workflow request bound to an action hash (D1, brief sections 4, 12).

## 9. `catalog.yaml`

Shipped in the binary, signed with the release; users add custom servers through the same schema (stored in `<app data>/mcp/user-servers.yaml`) and pass the same allowlist, cache, shaping and egress pipeline. Schema:

```yaml
schema: 1
servers:
  - id: <=8-char slug
    display: string
    transport: http | stdio            # remote streamable HTTP, or a local command
    url: https://...                   # http
    command: {argv: [...], pin: "pkg@x.y.z"}   # stdio; user-installed unless bundled: true
    auth: {kind: none | bearer | oauth2_pkce | oauth_dcr, header: Authorization, keyring: <ref>}
    trust: public_docs | third_party_docs | private_data
    side_effects_tools: [tool, ...]    # always approval-gated
    cache: {ttl: 24h, scope: global | project}
    shape: {max_inline_bytes: 8192, url_params: {maxTokenBudget: 2000}}
    egress: {max_arg_bytes: 1024, scan_secrets: true, deny_code_fences: true}
    license: {server: SPDX, content: SPDX, attribution: "text shown in citations", bundled: false}
    health: {probe: tools/list, timeout: 5s}
    terms_status: verified | unverified
```

| id | Entry (facts from [tool §5, §6]) |
|---|---|
| `mslearn` | `https://learn.microsoft.com/api/mcp`, streamable HTTP, auth none. Tools `microsoft_docs_search{query}`, `microsoft_docs_fetch{url}`, `microsoft_code_sample_search{query, language?}`. `?maxTokenBudget=N` truncates search responses. Content CC BY 4.0, code MIT; Learn Terms of Use apply; commercial-use terms **unverified**. `trust: public_docs`, TTL 24 h. |
| `cfdocs` | `https://docs.mcp.cloudflare.com/mcp`, stateless streamable HTTP (`/sse` is only an alias; legacy SSE GET returns 410), auth none. One tool, `search_cloudflare_documentation{query}` (returns `{results:[{similarity,url,title,text}]}`; `migrate_pages_to_workers_guide` is no longer listed for this server, re-checked 2026-10-12). Server repo Apache-2.0; docs content CC BY 4.0 (attribution required), code MIT. Best-effort service, no SLA found; cache. |
| `cfapi` | (P43) `https://mcp.cloudflare.com/mcp`, Cloudflare's API server ("Code Mode", repo `cloudflare/mcp`, Apache-2.0): OAuth through Cloudflare (recommended) or a Cloudflare API token as `Authorization: Bearer`. Tools `docs`, `search` (JavaScript against the OpenAPI spec), `execute` (JavaScript that calls the API: `side_effects_tools`) and `whoami` (the identity tool). Whether its OAuth server offers dynamic client registration is **unverified**. Cloudflare's per-product servers (Workers Bindings, Observability, Builds, Radar, ...) are marked deprecated in favour of it and are not offered. |
| `gh` | Remote `https://api.githubcopilot.com/mcp/`, streamable HTTP; OAuth, or `Authorization: Bearer <PAT>`. MIT. About 90 tools in toolsets; the catalog enables a read subset: `pull_request_read{method: get\|get_diff\|get_status\|get_files\|get_commits\|get_review_comments\|get_reviews\|get_comments\|get_check_runs, owner, repo, pullNumber, page?, perPage?, after?}`, `actions_get/list`, `get_job_logs`, `list_issues`, `search_*`. Write tools (`pull_request_review_write` incl. `resolve_thread`, `create_pull_request`, `merge_pull_request`) are `side_effects_tools`. It covers review threads natively, which `gh pr view --json` does not (D14; [`github.md`](github.md)). Its OAuth needs a GitHub App or OAuth App registered by the host application (no dynamic client registration), so the catalog connects it with a personal access token by default and, since P122, also through an OAuth app the person registers with GitHub (`auth.oauth_app`, "OAuth sign-in" below); `/readonly` variants of every URL and an `X-MCP-Readonly` header exist. `get_me` is the identity tool. `trust: private_data`. |
| `ctx7` | `https://mcp.context7.com/mcp` (streamable HTTP) or `npx @upstash/context7-mcp` (stdio, user-installed; 4.3.0 on 2026-10-12). `Authorization: Bearer <API key>`; a free key from context7.com/dashboard is recommended for rate limits. Server MIT; backend, parsing and crawling are private and not in the repo. Tools `resolve-library-id{query, libraryName}`, `query-docs{libraryId, query}`. Community-submitted docs carry an accuracy/security disclaimer, so `trust: third_party_docs` (extra marking, never auto-attached to packs). |
| `linear` | `https://mcp.linear.app/mcp`, streamable HTTP, OAuth 2.1 with dynamic client registration; an OAuth token or API key is also accepted as `Authorization: Bearer`; a read-only endpoint `/mcp/readonly` exposes only read tools. Hosted, not open source; re-checked 2026-10-12 from search-engine extracts of linear.app/docs/mcp (the page itself was not reachable from the sandbox). Default enabled endpoint for agents: the read-only one. |
| `notion` | `https://mcp.notion.com/mcp` (preferred) and `/sse` (fallback); OAuth 2.0 with PKCE, interactive only; Client ID Metadata Documents are supported next to dynamic client registration (search-engine extracts of developers.notion.com). The local `makenotion/notion-mcp-server` says it is no longer maintained or supported (D14). `notion-search`, `notion-fetch` and `notion-get-self` (identity) are declared read-only from a connected client's tool list, not from Notion's docs. Terms **unverified**. |
| `sentry` | `https://mcp.sentry.dev/mcp`: OAuth with dynamic client registration (the server's own code registers clients at `/oauth/register`), or a pasted token as `Authorization: Sentry-Bearer <token>` (Bearer is reserved for OAuth access tokens; the gateway sends an OAuth token as Bearer whatever the entry's scheme). FSL-1.1 with an Apache-2.0 future license. |

Upstream OAuth (Linear, Notion, Sentry, GitHub): the user completes a browser flow once from the Connection; tokens go to the credential store (values over 2560 bytes encrypted into SQLite, key in Credential Manager [go §3]). The gateway attaches credentials on upstream requests, so agents never see them. The go-sdk has `auth`/`oauthex` packages whose client OAuth is marked experimental [go §9]; the Notion access-token lifetime of 1 h is snippet-only.

## 10. Pointing each CLI at the single gateway entry

The registered server name is `drydock` in every CLI. Facts from [cli §1-§8]:

| CLI | Mechanism (per-run, no user-level config touched) | Notes |
|---|---|---|
| Claude Code | `--mcp-config <run>/mcp.json --strict-mcp-config`, plus `--disallowedTools mcp__plugin_*` because `--strict-mcp-config` does not keep installed plugins' MCP servers out (contract CLAUDE-MCP-01: they still start, their tools are refused); tool filter `--allowedTools mcp__drydock__*`; approvals via `--permission-prompt-tool mcp__drydock__permission_prompt` | stdio, http (`streamable-http` alias), sse (deprecated), ws. `-p` waits up to `MCP_TIMEOUT` 30 s. |
| Codex | ACP `session/new.mcpServers` (stdio, http; `sse:false`); or `-c mcp_servers.drydock.url=... -c mcp_servers.drydock.bearer_token_env_var=DRYDOCK_MCP_TOKEN -c mcp_servers.drydock.enabled_tools=[...]` | stdio + streamable HTTP only. |
| Gemini CLI | ACP `session/new.mcpServers`; or `settings.json` `mcpServers.drydock` with `httpUrl`+`headers` (or `command`/`args`/`env`) plus `--allowed-mcp-server-names drydock`, `includeTools` | `url` is SSE, `httpUrl` is streamable HTTP. |
| OpenCode | ACP `session/new.mcpServers`; or `OPENCODE_CONFIG_CONTENT` inline config `mcp.drydock` as `type: local` (`command[]`, `environment`) or `type: remote` (`url`, headers) | `timeout` default 5000 ms; tools are prefixed by server name. |
| Goose | ACP `session/new.mcpServers` (stdio, http; `sse:false`), merged into extensions; headless `--with-extension "<cmd>"` or `--with-streamable-http-extension URL` | Header support on the HTTP flag is **unverified**; use the shim. |
| Antigravity | `mcp_config.json`; per-session scoping and ACP behavior **unverified**; plugin MCP servers are namespaced `<plugin>_<server>` | Whether a per-run config path is honored is unverified; until proven the Connection records `mcp: unknown` and the gateway is not offered. Checked again for P117: the documented headless mode has no flag, variable or per-session file for an MCP configuration, and writing into the person's own `mcp_config.json` is not done, so it stays not offered. |
| Custom | Manifest `mcp: {via: arg, format: mcpServers, args: [..., "{{mcp_config}}"]}` (a flag naming the run's config file) or `{via: file, format: mcpServers, file: <relative .json path>}` (the file in the working folder); without the section the CLI gets no MCP | As built (P117): the mcpServers JSON with the single `drydock` entry, off until the person allows the gateway for that CLI (`mcp.try.set`, as P20); the file route never overwrites a file that is there, excludes the path from git and removes it at run end ([`connections.md`](connections.md), "As built: MCP, interrupt and acknowledgement"). |

**As built (decisions P20).** For Codex, Gemini, OpenCode and Goose the gateway is handed over only through ACP `session/new.mcpServers`, as the single `drydock` stdio-shim entry read from the per-run JSON file (the `-c` / settings.json / environment routes in the table are generated by `ConfigFor` but the core does not apply them). Because no real run has proven that route for those CLIs, it is **off until the human allows it** per CLI (`mcp.try.set`, the "MCP gateway" panel on the Connections page (on the Library page until 2026-10-14), setting `mcp.try.<adapter>`); a CLI with no known mechanism (Antigravity, a custom CLI whose manifest has no `mcp` section) cannot be allowed. A custom CLI with an `mcp` section (decision P117) is allowed the same way and gets the same JSON file, named by its flag or put in its working folder. `agent.mcp` events and the context manifest say `unverified: true` for such sessions. Tested end to end with the scripted ACP agent (`internal/agents/acp/fakeacp`, a separate process on the real ACP SDK), which acts as a real MCP client against what `session/new` gave it: it started the shim, listed the gateway's tools and called one (`TestHumanCanAllowTheGatewayForAnUnverifiedACPCLIAndItWorksEndToEnd`). That proves Drydock's side only; no vendor CLI has been run.

ACP `McpServer` descriptors for `session/new` follow the ACP schema (stdio command/args/env; HTTP url and, per the schema, headers); header support in `coder/acp-go-sdk` v0.13.5 types is **unverified** here and is checked when the adapter is built.

## 11. Upstream stdio servers under the process manager

`CommandTransport` in the Go SDK closes stdin, waits `TerminateDuration` (5 s), then `Process.Signal(SIGTERM)` and `Kill()`. On Windows only `Kill` works, and it terminates the direct child only, so `npx.cmd`, `uvx` and similar wrappers leave grandchildren [go §9]. Therefore Drydock never uses `CommandTransport` for upstream servers:

```go
func (m *Manager) startStdio(ctx context.Context, s ServerSpec) (*Upstream, error) {
    p, err := m.proc.Start(ctx, proc.Spec{Argv: s.Argv, Env: s.Env, Cwd: s.Cwd,
        Job: proc.JobOwn /* own Job Object, KILL_ON_JOB_CLOSE, suspended-then-resume (D7) */})
    if err != nil { return nil, err }
    t := &mcp.IOTransport{Reader: p.Stdout(), Writer: p.Stdin()} // go-sdk transport over our pipes
    c, err := m.client.Connect(ctx, t) // initialize with a 10 s budget
    ...
    return &Upstream{Proc: p, Client: c}, nil
}
```

Servers start lazily on the first allowed call, are shared per (server id, credential identity) across runs, multiplexed by the gateway with per-run allowlists, and stopped after an idle timeout (**design default** 10 min) or at app exit (the app-level job kills any survivor, D7). A crash emits `mcp.server.failed`; calls return a structured error (`upstream_unavailable`) with a circuit breaker (3 failures, 60 s open); the run continues (brief section 17, "MCP failure"). stdout/stderr go to the run log, redacted. The `.cmd` argument-escaping behavior is an open Windows item [go].

## 12. Licensing notes (D14, brief section 26)

| Item | License | Rule |
|---|---|---|
| Sentry MCP server | FSL-1.1-Apache-2.0 (source-available, not OSI; competing-use restriction) | Do not bundle; hosted `mcp.sentry.dev` only (the local-program template was removed, P45). |
| Aikido MCP (`@aikidosec/mcp`) | AGPL, or commercial | Do not bundle or embed; not in the catalog (P45). A person who wants it adds it as their own stdio server after legal review. |
| Notion local MCP server | MIT text, unmaintained | Unsupported; hosted remote MCP only. |
| GitHub MCP server | MIT | Remote hosted only; the local `github-mcp-server` template was removed from the catalog (P45). |
| Cloudflare API MCP server | Apache-2.0 | Hosted only. |
| Context7 MCP server | MIT; backend private | Hosted or user `npx`. |
| Cloudflare docs MCP server | Apache-2.0 | Hosted; content CC BY 4.0 needs attribution when we redistribute excerpts (catalog `license.attribution` is rendered in citations, minutes exports and PR drafts). |
| Microsoft Learn | content CC BY 4.0, code MIT; service terms not read | Attribution as above; commercial-resale terms **unverified**. |
| go-sdk (MCP) | mixed MIT/Apache-2.0 | Legal sign-off before release; `THIRD_PARTY_NOTICES.md` entry. |

## 13. Open questions / unverified

- No docs MCP server was called in Phase 0; result sizes, rate limits and commercial terms for Learn, Cloudflare docs and Context7 are unverified [tool §6]. Linear, Notion, Sentry and Aikido MCP and API facts are mostly snippet-level.
- Whether Goose, Antigravity and each ACP agent accept an HTTP MCP server with an `Authorization` header is unverified; the stdio shim is the default for that reason. Claude Code 2.1.294 accepted both the shim and the header route in a live run on Linux ("As built"). Whether a secret in the run's `mcp.json` is acceptable, and whether the shim inherits env, is a design call to confirm with a live run.
- The `GetExtendedTcpTable`/`IsProcessInJob` peer check (built in P122, run on Linux only), loopback-only listener behavior with Windows Defender Firewall, and `Host`/`Origin` validation in the go-sdk handler are unproven on Windows (NEEDS-WINDOWS). `.cmd` shim argument escaping and `IOTransport` over Process Manager pipes are not yet exercised.
- Model-API tool-name length limits, the chars/4 token estimate, default TTLs, `max_inline_bytes`, and the egress heuristics are design defaults awaiting measurement (brief section 23 evals).
- Role-default allowlists are a starting point; the brief does not specify them. Since P115 a project can change every cell, and the defaults are the table of section 3 as written.
- Contradiction noted: brief section 14 lists Linear/Notion/Sentry servers in the shipped catalog; D14 forbids bundling Sentry's and Notion's local servers and Aikido's. Resolved here as hosted entries only (the user-install templates were removed on 2026-10-13, P45).

## As built (2026-10-08, Linux sandbox)

`internal/mcp` implements the gateway (one loopback endpoint `/mcp/<run_id>` per run with a 256-bit bearer token, Host and Origin checks, rate-limited `mcp.auth_failed`: a bucket per run id of the shape Drydock makes, one shared bucket for every other path and a ceiling on all together, so a flood of made-up ids holds no memory and cannot fill the event log; headers are limited to 64 KiB and an idle connection is closed after five minutes, audit 2026-10-16 L24), the per-call pipeline (allowlist, approval gate, egress check, cache, scan, shape with `fetch_range`, untrusted wrapping, audit), the stdio shim (refuses non-loopback URLs), the nine-tool workspace server with symlink-safe confinement, and the catalog (`catalog.yaml`; the Sentry and Aikido stdio templates it first carried, D14, were removed in P45). Migration `00040_mcp.sql` adds `mcp_cache` and `mcp_usage`. `go vet` (also `GOOS=windows`) and the tests pass; `-race` passed on the worker's run.

Differences from the text above: events are `mcp.cache_hit` and `mcp.cache_evict`; truncation is head-only so `fetch_range` offsets are exact; a critical upstream failure can pause a run (`OnCritical`), an extension; `repo_map` is listed in every build; with CGO it answers the map (internal/core `ext_repomap.go`), without it the gated error (P62); unknown tools of a `private_data` server are treated as write-class and approval-gated; the catalog schema gained `data_class`, `read_only_tools`, `allow_tools`, `tools`, `critical`, `recommended_roles`, `default_enabled`, `template`, `auth.scheme`, `auth.optional`, `command.secret_env`, and (P47) `skill_registries`: the skill registries the same vendor publishes, shown as "Skills from this vendor" on the merged Integrations page (the core's gallery test checks every id names a registry of `internal/skills/registry.yaml`).

Built since (decision P122): OAuth for catalog servers whose provider has no dynamic client registration (through an OAuth app the person registers, "OAuth sign-in" below) and the peer check on Windows and Linux ("As built: the peer check" below). Unverified: every live server (the Learn live test skips because the sandbox proxy returns Forbidden), whether any CLI other than Claude accepts the generated configs or the shim, the Sentry `secret_env` variable name, and all Windows behaviour.

### Wired into agent runs (2026-10-08, Linux sandbox)

Before this, no run used the gateway. Now:

- **One shared gateway** per Core: `internal/core/ext_mcp.go` (extension `mcp`, started after `knowledge` through the new `Extension.After`) creates `mcp.New` with `Options.Workspace` = `Knowledge().Workspace()` (search_pack, search_requirements, search_memory, skills) plus `task`, `room` and `handoff` (below), listens on `127.0.0.1:<ephemeral>`, hands itself to `Knowledge().UseGateway`, and is closed by `Core.Close` (`Core.MCPGateway()`). Config files that a crash left in `runs/*/mcp.json` are deleted at start. The integrations extension still builds its own listener-less client gateway for app-side calls; it was left alone.
- **One seam for every agent turn**: `Runtime.MCP` (`internal/agents/runtime`), called by `Runtime.Spawn` before the process starts. Every session goes through `Spawn` (work, review, template role turns, the planner, room replies), so the hook is one place instead of six call sites; with it unset (any test that does not wire it) nothing changes. The hook gets the agent run id (`arun_...`, so `mcp.*` events and `agent_runs` correlate), may set `RunSpec.MCPConfigPath` and `AllowedTools`, and returns a `Release` that `Spawn` runs exactly once when the session ends: `Stop`, process exit or crash, or a failed start. A hook error does not stop the turn: the session runs without MCP and `agent.mcp` records why. The pipeline's read side is `Deps.MCPStatus`, used by `saveManifest` to list `mcp:drydock` in the context manifest, included with the tool names or excluded with the reason (or "not wired in this build" when unset).
- **Per run**: `StartRun` with `RunInfo{RunID, ProjectID, TaskID, AgentID, Role, Worktree}` (`Worktree` is the turn's cwd: the task worktree, or the project root for the planner and room replies), `DataClass` private, and the role policy below. The config is `mcp.ConfigFor(adapter)` written with `mcp.WriteConfig` (mode 0600) to `<data>/runs/<arun>/mcp.json` (the path in the table of section 2, which `EndRun` deletes; the raw results of the run live next to it in `runs/<arun>/mcp/`). It holds exactly one entry, `drydock`, the stdio shim (`<this exe> mcp-shim` with `DRYDOCK_MCP_URL` and `DRYDOCK_MCP_TOKEN` in its env). Claude additionally gets `--allowedTools mcp__drydock__*` appended. `main.go` and `cmd/drydock-dev` run the shim when started as `<exe> mcp-shim` (`mcp.MaybeRunShim`); any other binary that calls `core.Open` and starts real CLIs must do the same, or the CLI cannot start the shim (the `core` test binary does it in `TestMain`).
- **Which adapters**: only a verified mechanism is used (`mcp.ConfigFor(...).Verified`), which today is Claude over the shim. Codex, Gemini, OpenCode, Goose, Antigravity and custom adapters get no config (since P20 the person may allow the unproven ones; since P117 that includes a custom CLI with an `mcp` section); the manifest and `agent.mcp` say "MCP was not attached" with the reason ("the generated MCP configuration is unverified", or Antigravity's `ErrMCPUnknown`). A run whose provider route is `trainable` (`EffectiveDataClass`) gets none either (security.md section 9). `mcp.status` lists each adapter's answer.
- **Role policy** (decision P115: the table of section 3, see "As built: the role table, rules and task narrowing" below; until P115 every role got the same read tools). `propose_memory` (decision P92: a proposal for the project's memory that reaches no agent until a person accepts it on the Memory page; at most 5 per session, redacted and bounded as a person's proposal is) and the gateway's `fetch_range` reach every role; `search_pack` only when `Packs.AllowFor(project, agent, role)` says a pack is attached; `repo_map` is on for every role by default in a build with the tree-sitter parser (CGO), and is left out of the policy of a build without it (decision P62). The state-changing tools `task`, `room`, `handoff` are off unless a human turns on the per-project setting `mcp.act_tools` (`mcp.actTools.set`, audited as `mcp.act_tools`; since P115 the switch is on the project's Integrations page); then a reviewer gets `room` and `handoff`, every other role all three, as far as the role's act column allows.
- **Providers behind the stateful tools** (`internal/core/mcp_providers.go`): `task` reads the run's own task (title, description, acceptance, status, the last 20 evidence lines, review feedback; another project's task is "not found"); `add_note` posts a STATUS message of the agent in the task's room; `propose_status` does **not** move the task (the pipeline and the human own the state machine): it posts a blocking QUESTION for BLOCKED or WAITING_HUMAN (so it reaches Needs you) or a STATUS for REVIEW, with the evidence refs, and publishes `task.status_proposed`. `room` reads and posts in a channel of the project by name or in the task room, only as a member (agents never join on their own) and never in DMs or group chats; posts are the agent's own, untrusted, scanned, and rate limited by the gateway. Since decision P130 its action `request_join` (a channel and a reason) asks a person to let the agent into a channel of its project it is not in: an Inbox item the person answers, the agent joining as an observer only if let in ([`room-protocol.md`](room-protocol.md) s10). `handoff` stores a JSON artifact (capped at 8 KiB) in the existing `handoffs` table (migration 00002; no migration was added) and reads back a handoff of the same project (through its task) or one the same agent wrote; the watchdog's and router's own handoff shapes that may be written there later are shown as the summary. Since decision P119 it also takes `goal`, `remaining`, `decisions`, `commands`, `next_step` and `checks` (`{name, passed}`), and a handoff it stored is checked before a fresh session of the task is seeded with it (files that exist, check claims that match the recorded results, no secret, within bounds; `handoffs.verification`, migration 00155; [`token-control.md`](token-control.md) "As built: package ceiling, file ranges and verified handoffs"). Since decision P125 it also takes `criteria` (`{id, met, evidence}`: a criterion named by its id, AC1, or its number); a criterion the task does not have is refused, a met one without evidence too, and each met one is recorded as the agent's claim (`acceptance_marks`, `claim_met`), which never makes the criterion met; and the outgoing session of a task is asked, in a bounded turn of its own, to write a handoff with this tool before a fresh session replaces it ([`token-control.md`](token-control.md) "As built: role prompts under the ceiling, the handoff turn, criteria").
- **Secrets**: the bearer token exists in memory and in the run's config file only. The tests search every column of every table, every file under the data directory, and the application log for the token after a full pipeline run and find none; `agent.mcp` and the manifest carry tool names and reasons only; the config path is not a secret.

Findings from running the real Claude Code 2.1.294 against the generated config (these were bugs; the first two are fixed in `internal/mcp`):

1. Claude negotiates the 2026-07-28 protocol. A `tools/list` result whose `cacheScope` is empty is rejected (`Invalid result for tools/list ... cacheScope`), after which the agent sees **no** tools. The gateway and the shim replace the SDK's list handler, so the SDK never filled the field. Both now send `ttlMs: 0, cacheScope: "private"`.
2. For the same protocol every `tools/call` result needs `resultType: "complete"`; without it Claude reports `missing required resultType` and the agent sees an error instead of the answer. The SDK sets it only for results of its own tool handlers, and the field is unexported, so `completeResult` (`protocol.go`) round-trips the result through JSON for requests on that protocol and leaves older ones unchanged.
3. In Claude's `plan` mode (reviewers, room replies, the planner) an MCP tool is refused unless it carries `readOnlyHint`. The six read-only Workspace tools now do; `task`, `room`, `handoff` do not.
4. Both generated routes work with the real CLI: the stdio shim (default; the only one `Verified`) and direct streamable HTTP with `Authorization: Bearer` (one observation; not made the default). `TestRealClaudeTransports` (internal/mcp) runs both and checks the gateway's `mcp.call` event, not the model's words; `TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig` (internal/core) runs a Spawn through the Core's wiring in `acceptEdits` and `plan` mode with `haiku` and quotes a pack citation (about $0.004 and $0.09 per turn; plan mode was much more expensive because Claude Code answers a plan-mode `--model haiku` session with Sonnet, documented since 2.0.17 and measured with 2.1.296; read-only sessions therefore no longer use plan mode, see `agents.RunSpec.ReadOnly`).

Not verified: anything on Windows (the shim as `drydock.exe` is a GUI-subsystem Wails binary started with pipes by the CLI; `drydock.exe mcp-shim` has never run), any CLI other than Claude, the MCP_TIMEOUT behaviour on a cold start, catalog servers through an agent run against the real hosted servers (a catalog entry reaches agents as a library server the human connected, added to a project and granted, see "User-added servers" below, or, since P115, as the docs server of a live pack attached for the agent; both are tested with test doubles only), and `repo_map` called by a real agent CLI (the tests call it through a real MCP client against the shared gateway).

Permission prompt tool (2026-10-11, decision P40): `permission_prompt` is offered only to a run whose policy names it exactly (a glob such as `*` never does), which the Core does for a Claude worker turn outside a sandboxed project; Claude is started with `--permission-prompt-tool mcp__drydock__permission_prompt`. The tool takes `{"tool_name", "input", "tool_use_id"}`, hands the request to `Options.Permissions` (the Core's analyzer and hashed approval, `docs/security.md` s5 "Claude permission prompts") and answers with one text block holding `{"behavior":"allow","updatedInput":<the exact input>}` or `{"behavior":"deny","message":...}`. Its answer skips the result pipeline (no untrusted wrapper, no shaping), because the CLI parses it. A malformed request, a run without a permission service, and a run that ends while the request waits (`EndRun` ends the wait) are refused. `mcp.call` records the decision with the requested tool in `reason` and the args hash, never the input. Unverified: the shapes against a live Claude Code turn (contract CLAUDE-PERMPROMPT-01, paid).

Slice 5 addition: `internal/mcp/appcall.go` adds `Gateway.AppUpstream(ctx, spec)` with `Tools` and `Call`, the gateway's client side for calls made by the app itself. Integrations used it for sync and approved writes until 2026-10-12, when they moved to each vendor's own API (decision P42, [`integrations.md`](integrations.md)); the MCP library's Test, tool list and connector identity (P43) use it now. It uses the same pooled upstream as agent runs but has no run and no agent allowlist: the caller owns its tool list and gates writes. Each call publishes `mcp.app_call` (server, tool, args hash, status, bytes, duration; never arguments or results). A gateway without a listener can serve these calls; an idle reaper must then be ticked by the owner when `Start` is not used.

## User-added servers (as built, decisions P15)

The human connects a server **once** in the workspace **MCP library** (`internal/core/ext_mcpservers.go`, migration 00091, UI page "MCP library"; credentials are entered there and only there), puts it into a project (on that project's Integrations page, `/project/integrations`, since 2026-10-14; before, by dragging it onto the project or onto an agent in the Library) and grants it to the agents that may use it. A library server is a `ProjectConfig.Custom` entry in the terms of section 9, stored in the database instead of a YAML file. Nothing is reachable by default at either step: a project reaches only the servers it was put into (`project_mcp_servers`: enabled flag per project), an agent only the ones granted to it inside its own project (`agent_mcp_access`: tool globs per agent). A grant that names no tools (the agent checkboxes) gets the server's tools as they are known at that moment, by name, listing them first if they were never listed; before decision P37 it was `*`, every tool the server would ever offer, so a tool a server added later reached agents without anyone seeing it. When the tools are listed, each tool's name, description and input schema are scored with the patterns used for outside text (`integrations.Score`); a tool at or over the threshold is flagged (tool poisoning), left out of the grants Drydock makes, and shown as "held back" in the Library. A human can still name it in a grant. Grants stored as `*` before P37 keep working, but every run now checks what the server offers against what the person reviewed (`internal/mcp/toolreview.go`, audit 2026-10-16, M2): listing the tools keeps a fingerprint of each tool's name, description and input schema (`ToolInfo.Digest`), and a run keeps from the model a tool whose text no longer matches it, a tool marked as instructions and a tool that was not in the reviewed list, unless the grant names that tool (a person's decision); a changed tool stays withheld until the tools are listed again. Each such tool is reported once per run as `mcp.tool_withheld` with the reason. The grant that dropping a server onto an agent makes (a template, a prebuilt agent, the crew of a new project) names the tools too; it stored `*` until then. A library server listed before fingerprints were kept has none, so only its marks apply until its tools are listed again, and a catalog server that only declares its tools (so a run can list them without starting it) is not reviewed this way. `core.planMCP` adds, for the agent that is starting, every server that is enabled in its project and granted to it: the specs go into `RunConfig.Servers` and the granted tool globs into `RunPolicy.Allow[server]`, so the agent sees `<id>__<tool>` through the same single `drydock` entry as before (Claude's `--allowedTools mcp__drydock__*` covers them). Reusable agent setups (including which library servers to grant) are agent templates (decisions P18).

- **Trust and classification:** always `private_data` (cache scope project, no caching, withheld on a trainable route). The human declares read-only tools (`ServerSpec.ReadOnlyTools`); the server's `readOnlyHint` is displayed and ignored. Every other tool goes to the gateway `Approver`, implemented by the core: a `security.Approvals` row of kind `mcp_call` (Needs you, and the MCP servers page), the call stays pending until `human:` answers, an expired, rejected or cancelled approval refuses it, an approved one is consumed against the exact action hash.
- **Send guard:** `Egress{MaxArgBytes 4096, ScanSecrets, DenyCodeFences}` by default (section 6), switchable per server.
- **Plain environment values (L6):** the `env` of a server is stored as text and shown on the Library page, so a value the secret scanner would change is refused with a pointer to the secret variables (the message does not repeat the value). The sign-in tokens of OAuth servers are taught to the scanner when they are issued and again at every start (L25).
- **Credentials:** bearer tokens and secret environment values are in the vault under `mcp:<id>:token` and `mcp:<id>:env:<NAME>` (one copy per library server, whatever projects use it); the gateway's `CredentialSource` resolves only `mcp:` references; the scanner is taught each value (and again at start-up) so a result cannot echo it. A stdio server starts with the allowlisted environment of section 11 plus the plain `Env` the human typed.
- **Find tools** (`mcpserver.discover`) connects through `Gateway.AppUpstream(...).ToolList`, stores the tool list as the declared `Tools` (lazy start, section 2 "Readiness") and shows the servers' read-only claims as suggestions.
- **Never exported or imported** with a project (a program or URL chosen on one machine must not arrive from an archive); a changed command or URL clears the discovered tools.
- **Tested:** a real stdio MCP server (`internal/mcp/mcptestserver`, a fixture re-executed by the test binary) through the real gateway endpoint with the go-sdk client: tools listed for the granted agent and absent for another, read-only call runs, a secret reaches the server's environment and appears in no table, event or file, the send guard blocks a code fence, an approved write runs and a rejected one does not (a notes file shows exactly which ran). The real Claude Code CLI 2.1.295 (haiku) called `tst__echo` of such a server through the generated entry and received the agent's purpose (`TestRealClaudeUsesAUserAddedServerAndReceivesThePurpose`; whether the model obeys it is logged, not asserted).
- **OAuth:** see "OAuth sign-in" below.
- **Not verified:** Windows (a `.cmd` wrapper such as `npx.cmd` starting a stdio server is the open item of section 11); any CLI other than Claude; OAuth servers (bearer tokens only); an approval left pending while the agent's CLI gives up on the call (the CLI's own tool timeout decides, and the approval then expires after 30 minutes).

- **From the catalog (2026-10-09):** `mcplib.catalog {}` lists the built-in catalog servers a human can add (http, auth none, `Authorization: Bearer`, or OAuth; Sentry's `Sentry-Bearer` scheme and the stdio templates are not offered) with `id, display, url, description, auth (none|bearer|oauth), authOptional, readOnlyTools, recommendedRoles, trust, inLibrary, libraryId`. `mcplib.addFromCatalog {catalogId}` stores an ordinary library server (still `private_data`, guard on, the catalog's `read_only_tools` declared by the catalog on the human's behalf, its tool declarations until the first discovery) and returns `{server, created, next}`; a library server that already points at the same URL is returned unchanged (idempotent), and a different server holding the catalog id makes the new one `<id>2`. `next` says what is still owed (a token, a sign-in). Prebuilt agents (decision P27) use it to connect `cfdocs`, `mslearn` and `ctx7`. Catalog entries gained a `description` field (extension).

## OAuth sign-in (as built, decisions P22)

A library server of kind "web address" can be set to **Sign in with the server's account (OAuth)** instead of a pasted token. The flow is the official SDK's `auth.AuthorizationCodeHandler` (OAuth 2.1 authorization code with PKCE, discovery of the protected-resource and authorization-server metadata, dynamic client registration); `internal/core/ext_mcpoauth.go` wires it:

1. The human presses **Sign in** on the server's card (`mcplib.oauth.begin`). The core listens on a random port on 127.0.0.1 for exactly one answer, connects to the server, and hands the page the server's own sign-in address. The page links it (https, or an address on this computer for a local server; anything else is not shown). Drydock does not open a browser itself.
2. The browser returns to `http://127.0.0.1:<port>/callback`. Only a GET with that exact host is accepted, the first answer wins, the SDK checks the `state` and the issuer, and the listener closes when the flow ends (a human-denied sign-in, a wrong `state`, a cancel, or 10 minutes).
3. The tokens, the registered client and the token address are kept as one entry in the credential vault (`mcp:<id>:oauth`); the database keeps only that the server uses OAuth, when the human signed in and the granted scope. The scanner is taught the token values.
4. The gateway asks for the access token **on every upstream request** (`authTransport.dynamic`), so a token that expires while a connection is open is refreshed with the stored refresh token and the new tokens are written back. A refresh the server refuses (access revoked) marks the server signed out and says to sign in again.
5. **Sign out** deletes the entry. Changing the server's address, turning OAuth off or disconnecting the server deletes it too.

Network guard (`internal/netguard`, audit 2026-10-16 M9): the sign-in addresses come from the server's own answers, so every request of the flow (metadata, registration, token, refresh, every hop of a redirect) goes through a transport that resolves the name itself, refuses every address that is not public (loopback, private, link-local including cloud metadata, carrier-grade NAT, multicast, reserved, and IPv6 addresses that embed one: mapped, NAT64, 6to4) and anything that is not https, and then dials the address it checked, not the name again, so a name that changes its answer between the check and the connection (DNS rebinding) changes nothing; the dialer's own hook checks the address really being connected to as well. The server the person configured is allowed whatever it resolves to, and a server on this computer may also name other loopback addresses and ports; a server on the local network does not open the rest of the network. The callback of the flow answers only a request that carries the state of its own authorization address, so something else on this computer reaching the port first cannot use up the flow's one answer. With a proxy from the environment the name is resolved and checked before the proxy is asked. Upstream http servers of the gateway: a response is cut off at 64 MiB and a server has 15 minutes to start answering.

**Tested** against a fake server that speaks the protocol (`internal/mcp/oauthfake`, a test double also run by the browser harness: a real browser follows the sign-in link back to the core's loopback address in `e2e/round9.spec.ts`) (discovery, registration, authorization with PKCE verification, token, refresh, revocation; the real SDK handler and our wiring on the Drydock side): sign-in, the tools learned with the token, no token or client secret in any table, event or log, refresh with the refresh token, revocation shown as signed out, a denied sign-in, a forged `state`, cancel, the callback closing after the flow, sign-out, the network guard. **Not verified:** any real provider (Linear, Notion, Sentry, GitHub); a real provider without dynamic client registration (since P122 a client the person registered can be entered, tested against the fake provider started without a registration endpoint; GitHub itself unverified); Windows (the browser hand-over inside the Wails window: an https link with `target=_blank` is assumed to open the default browser); what happens when a provider rotates refresh tokens on every refresh is handled (the new tokens are written back) but was only tested with a fixed one.

### A client the person registers (as built, decision P122)

A provider without dynamic client registration (GitHub's hosted server: "each host app must configure a GitHub App or OAuth App")
cannot be signed in to with the flow above alone. The person registers an OAuth app with the provider (redirect address
`http://127.0.0.1/callback`; GitHub documents that a loopback redirect may use any port, which the flow's random port needs) and
enters its client id and, for a confidential client, its secret:

- **`mcplib.oauth.client {id, clientId, clientSecret}`** on a library server set to OAuth: both go to the same vault entry family
  as the tokens (`mcp:<id>:oauth-client`), the secret is taught to the scanner (and again at every start), the database keeps
  only that a client is set (`mcp_library.oauth_client`, migration 00160) and `mcp.server_oauth_client` says set or removed,
  never which. An empty id removes it. Setting, changing or removing the client signs the server out (its tokens belong to the
  client that obtained them); turning OAuth off, moving the server to another origin or disconnecting it forgets the client.
- **The sign-in** passes it to the SDK handler as `PreregisteredClient` (the SDK tries a client id metadata document, then the
  registered client, then dynamic registration). Without one, a provider that offers no registration now fails with what to do
  ("register an OAuth app with it ... and enter its client id"), and the server's sign-in panel then offers "Use your own OAuth
  app".
- **The catalog**: an entry's `auth.oauth_app {register_url, help}` says its server signs in through such an app. The shipped
  `gh` entry has one (`https://github.com/settings/applications/new`) and keeps the token as its default; its card shows "Sign in
  with your own app" (client id and secret, sent once) and `mcplib.connect {catalogId, clientId, clientSecret}` connects it as
  an OAuth library server with that client and starts the sign-in. A card says `oauth` and `oauthClient` once connected so.
- **Tested** against the fake provider started without a registration endpoint and with a pre-registered client
  (`oauthfake.NewWithoutRegistration`, `Preregister`): the failure without a client, the sign-in with it and no registration,
  the tools learned, no client secret or token in any table, event, file or log, the scanner knowing the secret, removal and
  OAuth off forgetting it, and the gallery's connect through the app (`internal/core/mcp_oauth_client_test.go`); UI unit tests.
  **Not verified:** GitHub's hosted server itself (that its protected-resource metadata leads to github.com's authorization server,
  with PKCE, and accepts a loopback redirect on any port).

## As built: the peer check (decision P122)

Hardening on top of each run's bearer token (section 2): a request for a run is answered only when the process that opened the
TCP connection belongs to the run's agent. There was none on any platform before; it is built for both.

- **Which process** (`internal/peer.Owner`): on Windows the TCP table with owning process ids, `GetExtendedTcpTable` with
  `TCP_TABLE_OWNER_PID_CONNECTIONS` for IPv4 and IPv6, called through `iphlpapi.dll` (`golang.org/x/sys/windows` has no wrapper),
  the buffer grown while Windows says it is too small; the row whose local end is the client's address and whose remote end is
  the gateway's gives the pid. On Linux `/proc/net/tcp{,6}` gives the socket's inode and `/proc/<pid>/fd` the process holding it.
  Elsewhere the lookup is unsupported. The two Windows table layouts (`tcpmib.h`) and the Linux files are parsed by code built on
  every platform, so they are unit-tested on Linux; the Windows call itself is behind a build tag.
- **Whose process** (`proc.InTree`): for a process Drydock started on Windows, `IsProcessInJob` with that process's kill-on-close
  Job Object (through `kernel32.dll`), so a child that outlived its parent still counts and nothing outside the job does; for any
  other process, and on Linux, the chain of parents (`CreateToolhelp32Snapshot` on Windows, `/proc/<pid>/stat`), at most 64 steps.
- **The gateway** (`internal/mcp/peercheck.go`): a run started with `RunConfig.PeerCheck` answers a connection only when its
  process is in the tree of a process bound to the run (`Gateway.BindPeer`); the answer is kept per TCP connection (looked up
  once on a keep-alive connection). A request that comes before the first bind waits for it (10 seconds at most). Refused:
  another process (403, `mcp.peer_refused` with the reason and the pid, never the token, rate-limited like `mcp.auth_failed`),
  a connection whose process cannot be found, a run never bound. A platform without a lookup is not checked and the run says so
  once (`mcp.peer_unchecked`). The token is checked first, so a wrong one is still a 401.
- **The binding** (`runtime.MCPAttachment.BindProcess`): the runtime hands the adapter a process manager that binds every
  process it starts for the session, the moment it exists and before the adapter talks to it (an ACP agent may connect during
  `session/new`; a headless CLI starts a process per turn). The core turns the check on for every agent session except one in a
  WSL distro (it connects from inside the distro, where neither the table nor the job can follow it). The stdio shim the CLI
  starts is its child, so it is answered.
- **Tests**: `internal/peer` (both Windows tables, the Linux files, this process found for a real connection),
  `internal/proc` (the parent walk, a child in its parent's tree and not the reverse), `internal/mcp/peercheck_test.go` (with the
  real lookups: a child process of the test bound to the run is answered and the test process, holding the token, refused;
  per connection, never bound, unsupported), the runtime's binding test, `internal/core/mcp_peercheck_test.go` (a spawned
  session refuses the test process), and every end-to-end test of a fake agent, an ACP agent and a custom CLI calling tools
  through the shim with the check on. A test whose own process plays the agent binds itself (`playAgent`).
- **Not verified:** Windows. `GetExtendedTcpTable` and `IsProcessInJob` were cross-compiled and vetted (`GOOS=windows go vet`),
  never run; whether the shim, started by a CLI that is itself in the job, is in the job on Windows is what the design assumes.


## Connector gallery (as built, decision P43, 2026-10-12)

The MCP page shows the catalog as a **connector gallery**, the way other apps present connectors: one card per catalog entry with its logo, name, publisher, a line on what it gives agents, the sign-in kind, the license and a link to the vendor page it was checked against (`internal/core/ext_mcpgallery.go`, UI in [`ui.md`](ui.md)). Catalog entries gained `publisher`, `official`, `brand`, `group`, `summary`, `docs_url`, `token_help`, `identity` (a read-only tool that reports the signed-in account; it must be a read-only tool of the entry) and `integration`.

- **`mcplib.gallery {}`** lists every entry with its state, derived from the library server connected from it (marked with `catalog_id`, or an http server at its URL): `not_connected`; `needs_sign_in` (OAuth, not signed in); `needs_token` (a required token or a template's secret is missing); `error` with the reason (the last failed connection); `connected` with the account and the server's identity. A connected card carries the tools the server listed; before that, the catalog's declaration (`toolsListed: false`). Listing contacts no server.
- **`mcplib.connect {catalogId, token?}`** needs no project. It adds the entry to the library as an ordinary library server (P15 applies unchanged: private data, guard on, every tool not declared read-only waits for an approval; adding is idempotent), keeps the token or the template's secret in the credential vault (`mcp:<id>:token`, `mcp:<id>:env:<NAME>`), and then: for an OAuth entry starts the browser sign-in (P22) and returns its status with the sign-in address; otherwise reaches the server once (`mcplib.discover`). A template (stdio) is connected as a library program running the catalog's command with its secret variable (the shipped catalog has had no template since P45; the path is kept and tested with a test-double catalog). Refused: a required token or secret that is missing, a token for an OAuth entry.
- **Discovery records identity.** `mcplib.discover` (also run after a sign-in) stores, besides the tools, what the server said about itself in its answer to `initialize` (`serverInfo` name, title, version, description, an https website, and `instructions`, trimmed to 4,000 bytes and scanned for known secrets) and, for a catalog entry with an identity tool that the server lists, the account it reports (the first of the entry's `fields` in a JSON answer, else the first line of text, control characters removed, at most 120 characters, scanned). A failure is kept as `last_error` (scrubbed) and shown on the card; a success clears it. A changed address forgets the identity; a changed credential forgets the account. Migration `00112_mcp_gallery.sql` (`catalog_id`, `server_info`, `account`, `last_error`, `last_error_at`); none of them holds a secret. The identity and instructions are the server's claims, shown as data; Drydock does not pass a server's instructions to agents.
- **Custom servers** are shown on the page by that identity ("Identifies itself as ..."), and can be imported from a `claude_desktop_config.json`, a Cursor `mcp.json` (`mcpServers`) or a VS Code `mcp.json` (`servers`): `command`/`args`/`env` become a stdio server whose every environment value goes to the vault, `url` (or `serverUrl`, `httpUrl`) with an `Authorization: Bearer` header becomes an http server with a bearer token; other headers are not sent (said per server), and entries with `${...}` placeholders or the old SSE transport are not imported (said why). The parsing is in the UI (`ui/app/screens/mcp/import.ts`); every server is saved through `mcplib.save`, so the core validates it as if typed.
- **OAuth tokens are bearer tokens.** The gateway sends an OAuth access token as `Authorization: Bearer` whatever scheme the entry declares for a pasted token (hosted Sentry's `Sentry-Bearer` stays for a pasted Sentry token).
- **Prebuilt agents** (`agentcatalog.list`) now return, per MCP server, its display name, publisher, brand, summary and auth kind, and per skill, the description from its SKILL.md at the pin, its license, the registry's repository and a link to the skill's directory at the pinned commit. The descriptions are recorded in `internal/skills/registry.yaml` (`skills:`), read from each repository at the pinned commit; the agent catalog refuses a skill that is not recorded at its pin.

**Verified for each vendor (2026-10-12).** The sandbox's proxy could not resolve linear.app, developers.notion.com, docs.sentry.io, learn.microsoft.com, developers.cloudflare.com, context7.com or help.aikido.dev; GitHub (pages and raw files) and the npm registry were reachable, and search-engine extracts were used for the blocked pages, as marked.

| Vendor | Server | Transport and URL | Auth | License, official | Source read |
|---|---|---|---|---|---|
| GitHub | hosted | streamable HTTP, `https://api.githubcopilot.com/mcp/` (`/readonly` variants) | OAuth with an app the host registers (no DCR), or a PAT as Bearer | MIT, official | github.com/github/github-mcp-server README and docs/remote-server.md |
| GitHub | local | stdio, `github-mcp-server stdio` (v2.0.2, 8 Oct) | PAT in `GITHUB_PERSONAL_ACCESS_TOKEN`, or browser sign-in | MIT, official | same README, releases page |
| Linear | hosted | streamable HTTP, `https://mcp.linear.app/mcp`, `/mcp/readonly` | OAuth 2.1 with DCR; OAuth token or API key as Bearer | proprietary hosted, official | search extracts of linear.app/docs/mcp (blocked) |
| Notion | hosted | streamable HTTP, `https://mcp.notion.com/mcp` (`/sse` fallback) | OAuth 2.0 with PKCE, interactive; DCR and CIMD | proprietary hosted, official; local server unmaintained (MIT) | makenotion/notion-mcp-server README; search extracts of developers.notion.com/guides/mcp (blocked) |
| Sentry | hosted | streamable HTTP, `https://mcp.sentry.dev/mcp` | OAuth with DCR (`/oauth/register`); pasted token as `Sentry-Bearer` | FSL-1.1 (Apache-2.0 future), official | getsentry/sentry-mcp README, LICENSE.md, packages/mcp-cloudflare/src/server/index.ts; search extract of mcp.sentry.dev |
| Sentry | local | stdio, `npx @sentry/mcp-server@0.42.0` | `SENTRY_ACCESS_TOKEN` or `--access-token` | FSL-1.1-ALv2 (npm), official | README; npm registry |
| Aikido | local only | stdio, `npx @aikidosec/mcp@1.0.28` (2026-10-08) | browser sign-in, or `AIKIDO_API_KEY` | AGPL (npm `license`), official (`@aikidosec` scope); no hosted server of Aikido's own (a third party, MintMCP, hosts one) | npm registry metadata and README; search results |
| Microsoft | Learn | streamable HTTP, `https://learn.microsoft.com/api/mcp` | none | repo CC-BY-4.0 and MIT; Learn Terms of Use; official | github.com/MicrosoftDocs/mcp README (learn.microsoft.com blocked) |
| Cloudflare | Documentation | streamable HTTP, `https://docs.mcp.cloudflare.com/mcp` | none | Apache-2.0, official | cloudflare/mcp-server-cloudflare README and apps/docs-ai-search/README |
| Cloudflare | API | streamable HTTP, `https://mcp.cloudflare.com/mcp` | OAuth via Cloudflare, or an API token as Bearer; DCR **unverified** | Apache-2.0, official | github.com/cloudflare/mcp README |
| Upstash | Context7 | streamable HTTP, `https://mcp.context7.com/mcp` | optional API key as Bearer | MIT (server; backend private), official | github.com/upstash/context7 README; npm 4.3.0 |

**Tested.** `internal/mcp` (the catalog facts, the new fields' validation, identity and account from a real SDK server, an OAuth token sent as Bearer next to a `Sentry-Bearer` entry), `internal/core` (`TestMCPGalleryShowsEveryCatalogServerAsACard`; `TestMCPGalleryConnectsByOAuthTokenAndProgram` against test doubles: an HTTP server with an identity, a token server whose identity tool names the account, the fake OAuth server with a browser played by the test, this test binary as a stdio template with its secret, an unreachable server shown as Error, Add to project, grants, Disconnect, no token or secret in any table, event or file; `TestCustomServerIsIdentifiedByItsServerInfo`; `TestPrebuiltAgentListSaysWhatEachAgentComesWith`), `internal/skills` and `internal/agentcatalog` (every prebuilt skill recorded at its pin), the UI in Chromium, and `ui/e2e/round16.spec.ts` (the harness adds a "Hosted tracker (test double)" card for its fake OAuth server through the test-only `core.Config.MCPCatalog`). **Not verified:** any real vendor server (none was contacted; a real sign-in, a real identity tool's answer shape, and Cloudflare's dynamic client registration are unknown), Windows (the browser hand-over from WebView2, `npx.cmd` for the templates), and the Notion tool names, which come from a connected client's list rather than Notion's docs.

## As built: the role table, rules and task narrowing (decision P115, 2026-10-29)

The table of section 3 is the gateway's policy for every agent run (`internal/mcp/rolepolicy.go`, applied by `core.planMCP`).

- **Rows and columns.** A row per role of section 3 (pm, architect, ux designer, frontend, backend, database, devops, qa,
  security, reviewer; a role is matched as `pipeline.NormalizePlanRole` reads it, a review turn runs as reviewer) and a row
  "Other roles" for every other role (the team lead, a technical writer, a role a person typed), which keeps everything a
  grant gives. Columns: the Workspace read tools one by one (`read_range`, `repo_map`, `skills`, `search_memory`,
  `search_requirements`, `search_pack`), act (`task`, `room`, `handoff`; a reviewer `room` and `handoff`), and the kinds of
  upstream server: docs servers (catalog entries with `public_docs` or `third_party_docs` trust: `cfdocs`, `mslearn`,
  `ctx7`), GitHub (`gh`), trackers (`linear`, `notion`), Sentry, and other (every other server: `cfapi`, a server a person
  described). A library server's column is the one of the catalog entry it was connected from (`mcp_library.catalog_id`).
- **Defaults** are section 3 as written, with the decisions that came after it: `propose_memory` is not in the table (every
  role may propose, P92) and `repo_map` is on for every role (P62, also for PM and UX, whose cell in section 3 names
  neither). A grant is still needed for a server: the table only says which kinds of servers a role may reach.
- **A project changes cells** on its Integrations page ("Agent tools by role": a checkbox per cell, a changed cell shaded,
  a row put back in one go; `mcp.rolePolicy.get`, `mcp.rolePolicy.set` with `allowed: null` for the default; migration
  `00148_mcp_role_policy.sql`, `mcp_role_policy`). The page also holds the act switch (`mcp.act_tools`), which had no page.
- **CEL rules** (`mcp_policy_rules`, `mcp.policyRule.add/remove`; the CEL module was already a dependency): boolean
  expressions over `role`, `column`, `server` ("workspace" or the server's id), `tool`, `adapter` and `task` (the run works on
  a task). A rule that is true takes the tool away; nothing gives one. At most 32 per project, 2 KiB each, a cost limit as
  the security policy's, checked when saved; a stored set that no longer compiles gives the run no MCP, with the reason.
- **A task narrows** its agents' tools to exposed names or globs (`read_range`, `ctx7__*`; migration
  `00149_task_mcp_tools.sql`, `task_mcp_tools`; `task.mcpTools.get/set`, the task page's "Agent tools"). It is an
  intersection: a name the role, the grant or the rules do not give stays out.
- **Enforcement.** Rules and narrowing are `RunPolicy.Refuse`, asked by the gateway for every tool on `tools/list` and again
  on `tools/call` (a refused call is `mcp.denied`); the permission prompt and the sandbox's command tools are exempt, because
  without them a turn could not ask or run its commands. The exposed names written into the CLI's config and the context
  manifest are filtered the same way.
- **Live packs' docs servers** (expert-packs.md): the catalog server of a live or hybrid pack attached for the agent is added
  to its run with exactly the pack's tool (`<id>__<tool>`), under the role's docs column, through the catalog entry's own
  trust, cache (a public docs server's 24 h, global), send guard and untrusted marking; a library server of the same id keeps
  its settings and gets the pack's tool added. `search_pack` still queries live packs itself through a service run.
- **Not exported** with a project: the table, the rules and a task's narrowing are permission grants, set again after import
  (`projectio`); deleted with the project.
- **Tested** with a real MCP client against the shared gateway (`TestRoleTableDecidesEveryRunsToolsAndLivePackServersReachAgents`:
  in-process test doubles for a docs server and a server under the id `gh`; the PM, backend, devops and reviewer rows, a live
  pack's server cached and marked, the send guard, overrides, a rule, a task's narrowing, the audit), in `internal/mcp`
  (defaults cell by cell, every catalog entry has a column, rules), and in the browser (`e2e/agent-tools.spec.ts`).
  **Not verified:** a real CLI calling a live pack's server, the real hosted docs servers.

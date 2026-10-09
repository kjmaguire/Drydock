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
| Peer check (hardening) | On Windows, map the connecting loopback port to a PID (`GetExtendedTcpTable`) and require membership in the run's Job Object (`IsProcessInJob`). **Design, unproven on Windows.** |
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
 "search_memory":{"type":"object","required":["query"],"properties":{"query":{"type":"string","maxLength":512},"scope":{"enum":["project","workspace"],"default":"project"},"k":{"type":"integer","minimum":1,"maximum":10,"default":5}}},
 "repo_map":{"type":"object","properties":{"focus_paths":{"type":"array","items":{"type":"string"}},"token_budget":{"type":"integer","minimum":256,"maximum":8192,"default":2048}}},
 "read_range":{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1},"max_bytes":{"type":"integer","maximum":65536,"default":16384}}},
 "task":{"type":"object","required":["action"],"properties":{"action":{"enum":["get","add_note","propose_status"]},"task_id":{"type":"string"},"note":{"type":"string","maxLength":4000},"status":{"enum":["BLOCKED","WAITING_HUMAN","REVIEW"]},"evidence_refs":{"type":"array","items":{"type":"string"}}}},
 "skills":{"type":"object","required":["action"],"properties":{"action":{"enum":["list","get","for_paths"]},"name":{"type":"string"},"paths":{"type":"array","items":{"type":"string"},"maxItems":50}}},
 "room":{"type":"object","required":["action"],"properties":{"action":{"enum":["read","post"]},"channel":{"type":"string"},"thread_id":{"type":"string"},"kind":{"enum":["CHAT","STATUS","RESULT","QUESTION","DECISION","HANDOFF"]},"mentions":{"type":"array","items":{"type":"string"}},"body":{"type":"string","maxLength":4000},"limit":{"type":"integer","maximum":50}}},
 "handoff":{"type":"object","required":["action"],"properties":{"action":{"enum":["create","get"]},"handoff_id":{"type":"string"},"summary":{"type":"string","maxLength":6000},"done":{"type":"array","items":{"type":"string"}},"open":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"}}}},
 "fetch_range":{"type":"object","required":["handle"],"properties":{"handle":{"type":"string"},"offset":{"type":"integer","minimum":0},"length":{"type":"integer","maximum":65536,"default":8192}}}}
```

Semantics: `search_pack` returns chunks with citations `{pack, version, url, heading_path, chunk_id, fetched_at}` or an explicit "nothing found" (brief section 15; see [`expert-packs.md`](expert-packs.md)). `repo_map` is registered only when the tree-sitter build tag is on and the Windows CI job is green (D5); otherwise the tool is absent rather than faked. `task` lets an agent add notes and propose a move to `BLOCKED`, `WAITING_HUMAN` or `REVIEW` (with evidence); it cannot set `COMPLETED`, `CANCELLED` or `READY` (brief section 12: acceptance is human). `skills.for_paths` is how non-Claude CLIs receive glob-scoped skills on demand. `room.post` is rate-limited and typed; posted bodies are agent output, never executed. For Claude's `--permission-prompt-tool` the Workspace server also hosts an internal `permission_prompt` tool, hidden from `tools/list` for every other client and absent from role allowlists; it turns a Claude permission request into a workflow request bound to an action hash (D1, brief sections 4, 12).

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
| `cfdocs` | `https://docs.mcp.cloudflare.com/mcp`, stateless streamable HTTP (`/sse` is only an alias; legacy SSE GET returns 410), auth none. Tools `search_cloudflare_documentation{query}` (returns `{results:[{similarity,url,title,text}]}`) and `migrate_pages_to_workers_guide{}`. Server repo Apache-2.0; docs content CC BY 4.0 (attribution required), code MIT. Best-effort service, no SLA found; cache. Cloudflare's recommended "Code Mode" server `https://mcp.cloudflare.com/mcp` has **unverified** auth and is not in v1. |
| `gh` | Remote `https://api.githubcopilot.com/mcp/`, streamable HTTP; OAuth, or `Authorization: Bearer <PAT>`. MIT. About 90 tools in toolsets; the catalog enables a read subset: `pull_request_read{method: get\|get_diff\|get_status\|get_files\|get_commits\|get_review_comments\|get_reviews\|get_comments\|get_check_runs, owner, repo, pullNumber, page?, perPage?, after?}`, `actions_get/list`, `get_job_logs`, `list_issues`, `search_*`. Write tools (`pull_request_review_write` incl. `resolve_thread`, `create_pull_request`, `merge_pull_request`) are `side_effects_tools`. It covers review threads natively, which `gh pr view --json` does not (D14; [`github.md`](github.md)). A local Go server `github.com/github/github-mcp-server/v2` (stdio, MIT) is an option, not bundled in v1. `trust: private_data`. |
| `ctx7` | `https://mcp.context7.com/mcp` (streamable HTTP) or `npx @upstash/context7-mcp` (stdio, user-installed). `Authorization: Bearer <API key>`; a free key from context7.com/dashboard is recommended for rate limits. Server MIT; backend, parsing and crawling are private and not in the repo. Tools `resolve-library-id{query, libraryName}`, `query-docs{libraryId, query}`. Community-submitted docs carry an accuracy/security disclaimer, so `trust: third_party_docs` (extra marking, never auto-attached to packs). |
| `linear` | `https://mcp.linear.app/mcp`, streamable HTTP, OAuth 2.1 with dynamic client registration; an OAuth token or API key is also accepted as `Authorization: Bearer`; a read-only endpoint `/mcp/readonly` exists. Hosted, not open source; snippet-level evidence, **unverified** [tool §5]. Default enabled endpoint for agents: the read-only one. |
| `notion` | `https://mcp.notion.com/mcp` (preferred) and `/sse` (fallback); OAuth 2.0 with PKCE, interactive only. The local `makenotion/notion-mcp-server` is retired and unsupported (D14). Terms **unverified**. |
| `sentry` | `https://mcp.sentry.dev/mcp`, OAuth, or `Authorization: Sentry-Bearer ${SENTRY_ACCESS_TOKEN}`. The stdio package `npx @sentry/mcp-server@latest --access-token=...` (scopes `org:read project:read project:write team:read team:write event:write`) is **FSL-1.1-Apache-2.0 and is not bundled** (D14); a user-installed template entry exists with `bundled: false`. |
| (template) Aikido | `@aikidosec/mcp@1.0.27`, local stdio, AGPL or commercial: never bundled (D14), shown only as a user-install template; auth `AIKIDO_API_KEY`. See [`integrations.md`](integrations.md). |

Upstream OAuth (Linear, Notion, Sentry, GitHub): the user completes a browser flow once from the Connection; tokens go to the credential store (values over 2560 bytes encrypted into SQLite, key in Credential Manager [go §3]). The gateway attaches credentials on upstream requests, so agents never see them. The go-sdk has `auth`/`oauthex` packages whose client OAuth is marked experimental [go §9]; the Notion access-token lifetime of 1 h is snippet-only.

## 10. Pointing each CLI at the single gateway entry

The registered server name is `drydock` in every CLI. Facts from [cli §1-§8]:

| CLI | Mechanism (per-run, no user-level config touched) | Notes |
|---|---|---|
| Claude Code | `--mcp-config <run>/mcp.json --strict-mcp-config`; tool filter `--allowedTools mcp__drydock__*`; approvals via `--permission-prompt-tool mcp__drydock__permission_prompt` | stdio, http (`streamable-http` alias), sse (deprecated), ws. `-p` waits up to `MCP_TIMEOUT` 30 s. |
| Codex | ACP `session/new.mcpServers` (stdio, http; `sse:false`); or `-c mcp_servers.drydock.url=... -c mcp_servers.drydock.bearer_token_env_var=DRYDOCK_MCP_TOKEN -c mcp_servers.drydock.enabled_tools=[...]` | stdio + streamable HTTP only. |
| Gemini CLI | ACP `session/new.mcpServers`; or `settings.json` `mcpServers.drydock` with `httpUrl`+`headers` (or `command`/`args`/`env`) plus `--allowed-mcp-server-names drydock`, `includeTools` | `url` is SSE, `httpUrl` is streamable HTTP. |
| OpenCode | ACP `session/new.mcpServers`; or `OPENCODE_CONFIG_CONTENT` inline config `mcp.drydock` as `type: local` (`command[]`, `environment`) or `type: remote` (`url`, headers) | `timeout` default 5000 ms; tools are prefixed by server name. |
| Goose | ACP `session/new.mcpServers` (stdio, http; `sse:false`), merged into extensions; headless `--with-extension "<cmd>"` or `--with-streamable-http-extension URL` | Header support on the HTTP flag is **unverified**; use the shim. |
| Antigravity | `mcp_config.json`; per-session scoping and ACP behavior **unverified**; plugin MCP servers are namespaced `<plugin>_<server>` | Whether a per-run config path is honored is unverified; until proven the Connection records `mcp: unknown` and the gateway is not offered. |
| Custom | Manifest `mcp.inject: config_file \| acp_session_new \| env`; if none, the CLI gets no MCP and the Connection records `mcp: absent` | |

**As built (decisions P20).** For Codex, Gemini, OpenCode and Goose the gateway is handed over only through ACP `session/new.mcpServers`, as the single `drydock` stdio-shim entry read from the per-run JSON file (the `-c` / settings.json / environment routes in the table are generated by `ConfigFor` but the core does not apply them). Because no real run has proven that route for those CLIs, it is **off until the human allows it** per CLI (`mcp.try.set`, the "Agent CLIs" panel on the Library page, setting `mcp.try.<adapter>`); a CLI with no known mechanism (Antigravity) cannot be allowed. `agent.mcp` events and the context manifest say `unverified: true` for such sessions. Tested end to end with the scripted ACP agent (`internal/agents/acp/fakeacp`, a separate process on the real ACP SDK), which acts as a real MCP client against what `session/new` gave it: it started the shim, listed the gateway's tools and called one (`TestHumanCanAllowTheGatewayForAnUnverifiedACPCLIAndItWorksEndToEnd`). That proves Drydock's side only; no vendor CLI has been run.

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
| Sentry MCP server | FSL-1.1-Apache-2.0 (source-available, not OSI; competing-use restriction) | Do not bundle; hosted `mcp.sentry.dev` or a user-installed copy only. |
| Aikido MCP (`@aikidosec/mcp`) | AGPL, or commercial | Do not bundle or embed; user-installed separate process only (legal review). |
| Notion local MCP server | MIT text, unmaintained | Unsupported; hosted remote MCP only. |
| GitHub MCP server | MIT | Remote hosted by default; local not bundled in v1. |
| Context7 MCP server | MIT; backend private | Hosted or user `npx`. |
| Cloudflare docs MCP server | Apache-2.0 | Hosted; content CC BY 4.0 needs attribution when we redistribute excerpts (catalog `license.attribution` is rendered in citations, minutes exports and PR drafts). |
| Microsoft Learn | content CC BY 4.0, code MIT; service terms not read | Attribution as above; commercial-resale terms **unverified**. |
| go-sdk (MCP) | mixed MIT/Apache-2.0 | Legal sign-off before release; `THIRD_PARTY_NOTICES.md` entry. |

## 13. Open questions / unverified

- No docs MCP server was called in Phase 0; result sizes, rate limits and commercial terms for Learn, Cloudflare docs and Context7 are unverified [tool §6]. Linear, Notion, Sentry and Aikido MCP and API facts are mostly snippet-level.
- Whether Goose, Antigravity and each ACP agent accept an HTTP MCP server with an `Authorization` header is unverified; the stdio shim is the default for that reason. Claude Code 2.1.294 accepted both the shim and the header route in a live run on Linux ("As built"). Whether a secret in the run's `mcp.json` is acceptable, and whether the shim inherits env, is a design call to confirm with a live run.
- `GetExtendedTcpTable`/`IsProcessInJob` peer checks, loopback-only listener behavior with Windows Defender Firewall, and `Host`/`Origin` validation in the go-sdk handler are design only (NEEDS-WINDOWS). `.cmd` shim argument escaping and `IOTransport` over Process Manager pipes are not yet exercised.
- Model-API tool-name length limits, the chars/4 token estimate, default TTLs, `max_inline_bytes`, and the egress heuristics are design defaults awaiting measurement (brief section 23 evals).
- Role-default allowlists are a starting point; the brief does not specify them.
- Contradiction noted: brief section 14 lists Linear/Notion/Sentry servers in the shipped catalog; D14 forbids bundling Sentry's and Notion's local servers and Aikido's. Resolved here as hosted entries only plus user-install templates.

## As built (2026-10-08, Linux sandbox)

`internal/mcp` implements the gateway (one loopback endpoint `/mcp/<run_id>` per run with a 256-bit bearer token, Host and Origin checks, rate-limited `mcp.auth_failed`), the per-call pipeline (allowlist, approval gate, egress check, cache, scan, shape with `fetch_range`, untrusted wrapping, audit), the stdio shim (refuses non-loopback URLs), the nine-tool workspace server with symlink-safe confinement, and the catalog (`catalog.yaml`; Sentry and Aikido are non-bundled stdio templates, D14). Migration `00040_mcp.sql` adds `mcp_cache` and `mcp_usage`. `go vet` (also `GOOS=windows`) and the tests pass; `-race` passed on the worker's run.

Differences from the text above: events are `mcp.cache_hit` and `mcp.cache_evict`; truncation is head-only so `fetch_range` offsets are exact; a critical upstream failure can pause a run (`OnCritical`), an extension; `repo_map` is listed but returns the D5 gated error instead of being absent; unknown tools of a `private_data` server are treated as write-class and approval-gated; the catalog schema gained `data_class`, `read_only_tools`, `allow_tools`, `tools`, `critical`, `recommended_roles`, `default_enabled`, `template`, `auth.scheme`, `auth.optional`, `command.secret_env`.

Not built: the `permission_prompt` tool, OAuth for catalog entries (the flow below covers servers connected in the library), the Windows peer check (`GetExtendedTcpTable`). Unverified: every live server (the Learn live test skips because the sandbox proxy returns Forbidden), whether any CLI other than Claude accepts the generated configs or the shim, the Sentry `secret_env` variable name, and all Windows behaviour.

### Wired into agent runs (2026-10-08, Linux sandbox)

Before this, no run used the gateway. Now:

- **One shared gateway** per Core: `internal/core/ext_mcp.go` (extension `mcp`, started after `knowledge` through the new `Extension.After`) creates `mcp.New` with `Options.Workspace` = `Knowledge().Workspace()` (search_pack, search_requirements, search_memory, skills) plus `task`, `room` and `handoff` (below), listens on `127.0.0.1:<ephemeral>`, hands itself to `Knowledge().UseGateway`, and is closed by `Core.Close` (`Core.MCPGateway()`). Config files that a crash left in `runs/*/mcp.json` are deleted at start. The integrations extension still builds its own listener-less client gateway for app-side calls; it was left alone.
- **One seam for every agent turn**: `Runtime.MCP` (`internal/agents/runtime`), called by `Runtime.Spawn` before the process starts. Every session goes through `Spawn` (work, review, template role turns, the planner, room replies), so the hook is one place instead of six call sites; with it unset (any test that does not wire it) nothing changes. The hook gets the agent run id (`arun_...`, so `mcp.*` events and `agent_runs` correlate), may set `RunSpec.MCPConfigPath` and `AllowedTools`, and returns a `Release` that `Spawn` runs exactly once when the session ends: `Stop`, process exit or crash, or a failed start. A hook error does not stop the turn: the session runs without MCP and `agent.mcp` records why. The pipeline's read side is `Deps.MCPStatus`, used by `saveManifest` to list `mcp:drydock` in the context manifest, included with the tool names or excluded with the reason (or "not wired in this build" when unset).
- **Per run**: `StartRun` with `RunInfo{RunID, ProjectID, TaskID, AgentID, Role, Worktree}` (`Worktree` is the turn's cwd: the task worktree, or the project root for the planner and room replies), `DataClass` private, and the role policy below. The config is `mcp.ConfigFor(adapter)` written with `mcp.WriteConfig` (mode 0600) to `<data>/runs/<arun>/mcp.json` (the path in the table of section 2, which `EndRun` deletes; the raw results of the run live next to it in `runs/<arun>/mcp/`). It holds exactly one entry, `drydock`, the stdio shim (`<this exe> mcp-shim` with `DRYDOCK_MCP_URL` and `DRYDOCK_MCP_TOKEN` in its env). Claude additionally gets `--allowedTools mcp__drydock__*` appended. `main.go` and `cmd/drydock-dev` run the shim when started as `<exe> mcp-shim` (`mcp.MaybeRunShim`); any other binary that calls `core.Open` and starts real CLIs must do the same, or the CLI cannot start the shim (the `core` test binary does it in `TestMain`).
- **Which adapters**: only a verified mechanism is used (`mcp.ConfigFor(...).Verified`), which today is Claude over the shim. Codex, Gemini, OpenCode, Goose, Antigravity and custom adapters get no config; the manifest and `agent.mcp` say "MCP was not attached" with the reason ("the generated MCP configuration is unverified", or Antigravity's `ErrMCPUnknown`). A run whose provider route is `trainable` (`EffectiveDataClass`) gets none either (security.md section 9). `mcp.status` lists each adapter's answer.
- **Role policy** (implemented: a minimal honest table, not the full table of section 3). Every role gets the read-only tools `read_range`, `search_memory`, `search_requirements`, `skills` (and the gateway's `fetch_range`); `search_pack` only when `Packs.AllowFor(project, agent, role)` says a pack is attached (`Apply`'s live-docs-server part is deliberately not applied: `search_pack` queries live packs itself through a service run); `repo_map` is withheld while gated (D5). The state-changing tools `task`, `room`, `handoff` are off unless a human turns on the per-project setting `mcp.act_tools` (`mcp.actTools.set`, audited as `mcp.act_tools`); then a reviewer gets `room` and `handoff`, every other role all three. Not built: the per-role differences of the table in section 3 (PM without `read_range`, UX without `skills`, ...), docs-server and tracker entries per role, CEL project policy and task overrides.
- **Providers behind the stateful tools** (`internal/core/mcp_providers.go`): `task` reads the run's own task (title, description, acceptance, status, the last 20 evidence lines, review feedback; another project's task is "not found"); `add_note` posts a STATUS message of the agent in the task's room; `propose_status` does **not** move the task (the pipeline and the human own the state machine): it posts a blocking QUESTION for BLOCKED or WAITING_HUMAN (so it reaches Needs you) or a STATUS for REVIEW, with the evidence refs, and publishes `task.status_proposed`. `room` reads and posts in a channel of the project by name or in the task room, only as a member (agents never join on their own) and never in DMs or group chats; posts are the agent's own, untrusted, scanned, and rate limited by the gateway. `handoff` stores a JSON artifact (capped at 8 KiB) in the existing `handoffs` table (migration 00002; no migration was added) and reads back a handoff of the same project (through its task) or one the same agent wrote; the watchdog's and router's own handoff shapes that may be written there later are shown as the summary.
- **Secrets**: the bearer token exists in memory and in the run's config file only. The tests search every column of every table, every file under the data directory, and the application log for the token after a full pipeline run and find none; `agent.mcp` and the manifest carry tool names and reasons only; the config path is not a secret.

Findings from running the real Claude Code 2.1.294 against the generated config (these were bugs; the first two are fixed in `internal/mcp`):

1. Claude negotiates the 2026-07-28 protocol. A `tools/list` result whose `cacheScope` is empty is rejected (`Invalid result for tools/list ... cacheScope`), after which the agent sees **no** tools. The gateway and the shim replace the SDK's list handler, so the SDK never filled the field. Both now send `ttlMs: 0, cacheScope: "private"`.
2. For the same protocol every `tools/call` result needs `resultType: "complete"`; without it Claude reports `missing required resultType` and the agent sees an error instead of the answer. The SDK sets it only for results of its own tool handlers, and the field is unexported, so `completeResult` (`protocol.go`) round-trips the result through JSON for requests on that protocol and leaves older ones unchanged.
3. In Claude's `plan` mode (reviewers, room replies, the planner) an MCP tool is refused unless it carries `readOnlyHint`. The six read-only Workspace tools now do; `task`, `room`, `handoff` do not.
4. Both generated routes work with the real CLI: the stdio shim (default; the only one `Verified`) and direct streamable HTTP with `Authorization: Bearer` (one observation; not made the default). `TestRealClaudeTransports` (internal/mcp) runs both and checks the gateway's `mcp.call` event, not the model's words; `TestRealClaudeCallsTheSearchPackToolThroughTheGeneratedConfig` (internal/core) runs a Spawn through the Core's wiring in `acceptEdits` and `plan` mode with `haiku` and quotes a pack citation (about $0.004 and $0.09 per turn; plan mode was much more expensive because Claude Code answers a plan-mode `--model haiku` session with Sonnet, documented since 2.0.17 and measured with 2.1.296; read-only sessions therefore no longer use plan mode, see `agents.RunSpec.ReadOnly`).

Not verified: anything on Windows (the shim as `drydock.exe` is a GUI-subsystem Wails binary started with pipes by the CLI; `drydock.exe mcp-shim` has never run), any CLI other than Claude, the MCP_TIMEOUT behaviour on a cold start, catalog servers through an agent run (the shipped catalog entries are still not enabled for any run, so `gh__*`, `mslearn__*` etc. are not reachable from agents; only servers the human puts in the library, adds to a project and grants to the agent are, see "User-added servers" below), and `repo_map`.

Slice 5 addition: `internal/mcp/appcall.go` adds `Gateway.AppUpstream(ctx, spec)` with `Tools` and `Call`, the gateway's client side for calls made by the app itself (integration sync and approved outbound writes, [`integrations.md`](integrations.md)). It uses the same pooled upstream as agent runs but has no run and no agent allowlist: the caller owns its tool list and gates writes. Each call publishes `mcp.app_call` (server, tool, args hash, status, bytes, duration; never arguments or results). The integrations extension runs a gateway without a listener for this; an idle reaper must be ticked by the owner when `Start` is not used.

## User-added servers (as built, decisions P15)

The human connects a server **once** in the workspace **MCP library** (`internal/core/ext_mcpservers.go`, migration 00091, UI page "MCP library"; credentials are entered there and only there), puts it into a project (drag it onto the project, or onto an agent, which also adds it to that agent's project) and grants it to the agents that may use it. A library server is a `ProjectConfig.Custom` entry in the terms of section 9, stored in the database instead of a YAML file. Nothing is reachable by default at either step: a project reaches only the servers it was put into (`project_mcp_servers`: enabled flag per project), an agent only the ones granted to it inside its own project (`agent_mcp_access`: tool globs per agent). `core.planMCP` adds, for the agent that is starting, every server that is enabled in its project and granted to it: the specs go into `RunConfig.Servers` and the granted tool globs into `RunPolicy.Allow[server]`, so the agent sees `<id>__<tool>` through the same single `drydock` entry as before (Claude's `--allowedTools mcp__drydock__*` covers them). Reusable agent setups (including which library servers to grant) are agent templates (decisions P18).

- **Trust and classification:** always `private_data` (cache scope project, no caching, withheld on a trainable route). The human declares read-only tools (`ServerSpec.ReadOnlyTools`); the server's `readOnlyHint` is displayed and ignored. Every other tool goes to the gateway `Approver`, implemented by the core: a `security.Approvals` row of kind `mcp_call` (Needs you, and the MCP servers page), the call stays pending until `human:` answers, an expired, rejected or cancelled approval refuses it, an approved one is consumed against the exact action hash.
- **Send guard:** `Egress{MaxArgBytes 4096, ScanSecrets, DenyCodeFences}` by default (section 6), switchable per server.
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

Network guard: the sign-in addresses come from the server's own answers, so every request of the flow (metadata, registration, token, refresh) goes through a transport that refuses addresses on this computer or its network (loopback, private, link-local including cloud metadata, carrier-grade NAT) and anything that is not https, unless the MCP server itself is on this computer. DNS is resolved before the request, so a name that resolves into the network is refused too; a name that changes its answer between that check and the connection (DNS rebinding) is not caught.

**Tested** against a fake server that speaks the protocol (`internal/mcp/oauthfake`, a test double also run by the browser harness: a real browser follows the sign-in link back to the core's loopback address in `e2e/round9.spec.ts`) (discovery, registration, authorization with PKCE verification, token, refresh, revocation; the real SDK handler and our wiring on the Drydock side): sign-in, the tools learned with the token, no token or client secret in any table, event or log, refresh with the refresh token, revocation shown as signed out, a denied sign-in, a forged `state`, cancel, the callback closing after the flow, sign-out, the network guard. **Not verified:** any real provider (Linear, Notion, Sentry, GitHub); a provider without dynamic client registration (a pre-registered client id and secret cannot be entered yet); Windows (the browser hand-over inside the Wails window: an https link with `target=_blank` is assumed to open the default browser); what happens when a provider rotates refresh tokens on every refresh is handled (the new tokens are written back) but was only tested with a fixed one.


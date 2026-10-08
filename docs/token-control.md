# Drydock: token and cost control design

Scope: this document maps each of the 17 token and cost rules in brief §7 to the component that enforces it and to the test that proves it, and specifies the watchdog (signals, windows, normalization, stage 1 and 2, thresholds per workflow template, and the fallback when an agent CLI gives no tool-call visibility), the measurement and pricing-table design, the context manifest, and the two caches (`llm_cache` and `mcp_cache`). Primary success metric is cost per accepted task; the second is premium-subscription usage per accepted task (brief preamble). "brief §N" refers to `docs/brief.md`; D1-D14 to `docs/decisions.md`; evidence is cited to `docs/phase0.md` and the appendices (`MP` = model-providers, `AC` = agent-clis, `GP` = go-platform, `TI` = tooling-integrations). No component here is built yet, every test named is a test to be written with its slice, and no savings are claimed: any improvement needs measured numbers in `docs/evals.md` (brief §0, §23). A hard limit applies throughout: Drydock controls what it assembles, sends and runs; it cannot control what an agent CLI reads from the worktree or sends to its own provider by itself, and it observes those only through tool-call events (when the CLI emits them) and usage reports.

## 1. Rule matrix (brief §7, 17 rules)

"Test" names are proposed Go test identifiers or integration scenarios. Integration tests use real CLIs, real Git and fixture repos and are reported as skipped with a reason when a CLI is absent or unauthenticated (P3). A labeled stub is used only to test a failure boundary (brief §0).

| # | Rule (brief §7) | Enforcing component | Mechanism | Proving test |
|---|---|---|---|---|
| 1 | No broadcast context | `internal/context` (Assemble) + `internal/room` delivery | Room messages enter a package only if addressed to the agent (mention, thread membership, assignment) or selected by retrieval; there is no "all room messages" source type | `TestAssembleExcludesUnaddressedRoomMessages`: three agents, one message to A, B and C manifests contain zero entries for it (manifest assertion, no model needed) |
| 2 | Orchestrated turn-taking | `internal/room` turn scheduler + Agent Runtime | `StartTurn` requires a `TurnGrant` from the scheduler (modes `auto`, `sequential`, `moderator`, room-protocol s6); no grant, no run | `TestRuntimeRefusesTurnWithoutGrant`; `TestSequentialModeOneSpeaker` |
| 3 | Bounded debate (about 3 rounds) | Room scheduler round counter + workflow group-collaboration node (brief §4) | Per-thread round counter, default 3; at the bound the scheduler posts `SYSTEM`, produces a `DECISION` candidate or a human `QUESTION`; no further agent turns on that topic | `TestDebateStopsAtBound` (scheduler unit test with a labeled participant stub for the always-disagree boundary); integration with real cheap agents in a group node |
| 4 | Team-size routing | `internal/router` (`TeamSize` in `RoutingDecision`) | Default 1; greater than 1 only when a rule sets it (risk, Quality and above); never from agent request | `TestTeamSizeDefaultsToOne`, level tables (model-routing s14) |
| 5 | Handoff artifacts, not transcripts | Agent Runtime handoff service + `internal/context` | Session changes (escalation, 70% occupancy, new task) seed the fresh session with a handoff artifact (s6) and never with the prior transcript | `TestFreshSessionSeededOnlyWithHandoff`: manifest of the new session has one `handoff` entry and zero `transcript` entries; artifact size under cap |
| 6 | Session policy | Agent Runtime | Fresh session per independent task; same-session continuation only when cheaper or better; at 70% occupancy when idle request a verified handoff and start fresh within one node execution (brief §5) | `TestOccupancyThresholdTriggersHandoff` (occupancy fed from recorded real usage fixtures); integration with an ACP agent that reports `usage_update` |
| 7 | Cache-friendly prompts (stable prefix first) | `internal/context` PromptBuilder, `internal/llm` | Fixed section order, volatile data last (s4); cache breakpoints on provider APIs that support them | `TestPrefixByteStable` (two builds differing only in the tail share an identical prefix); integration: second identical-prefix Anthropic call above the minimum length reports `cache_read_input_tokens > 0` (needs a key; skipped otherwise) |
| 8 | Truncate tool output; raw kept on disk; fetch by range | MCP Gateway shaping (brief §14), adapters' tool-result handling, run output store (`<app data>/runs/<run_id>/<node>/<attempt>/`) | Results over the per-tool cap are cut with a marker `[truncated N of M bytes; fetch_range(ref, offset, len)]`; raw saved first | `TestShapingTruncatesAndKeepsRaw`: 1 MiB output, shaped size at cap, raw SHA-256 equals original, `fetch_range` returns exact bytes. Limit: output a CLI feeds to its own model from its native tools cannot be truncated by us |
| 9 | Retrieve, don't preload | `internal/context` + Workspace MCP server (`search_pack`, `search_requirements`, `search_memory`, `repo_map`, `read_range`, `task`, `skills`, `room`, `handoff`) | Initial package carries task, instructions, a token-budgeted repo map and pointers; content comes through tools on demand | `TestInitialPackageUnderCeiling`; manifest shows `pointer` entries for excluded bodies |
| 10 | Deterministic tools before LLMs | Workflow engine ordering + `check` nodes (brief §4, §13a) | Review nodes depend on a passing `check` edge; a reviewer is never started on a failing `build/test/lint/typecheck` | Engine test `TestReviewNotStartedBeforeChecks`; Iterate Until Verified integration |
| 11 | Ranges and diffs over whole files | Context Engine + `read_range` + diff provider | Files above a line cap enter as ranges; changes enter as diffs against the safe point (`refs/aet/<task>/<attempt>`, brief §13) | `TestLargeFileIncludedAsRange`, `TestChangedFileIncludedAsDiff` |
| 12 | Per-run ceilings with human escalation | Budget middleware + request port (brief §4) | Ceilings on tokens, cost, wall time, turns per run; crossing creates an `approval`-type request, run to `WAITING_HUMAN`; resume only by human response with a new ceiling | `TestCeilingCreatesRequestAndWaits`; engine acceptance "approval resume across app kill" |
| 13 | Exact-match response cache | `internal/llm` `llm_cache`, `internal/mcp` `mcp_cache` (s8) | SHA-256 of canonical request; never for side-effecting calls | `TestCacheHitSkipsProvider` (provider call counter on a recorded-fixture HTTP server, labeled boundary test); `TestSideEffectingNeverCached` |
| 14 | Budgets at project, workflow, task, agent (warn 80%, pause 100%) | `internal/budget` | Scoped counters; reservation before the call, settlement after; 80% emits `budget.warn`, 100% pauses the scope (`budget.paused`) and creates a request | Boundary tests at 79.9 / 80 / 99.9 / 100 percent per scope; pause survives restart |
| 15 | No LLM where rules suffice | Package boundaries; router, watchdog, marker parser, minutes export, inbox derivation, check summarizer | Deterministic packages may not import `internal/llm` (enforced by an import-boundary test over `go list -deps`); the only sanctioned LLM uses are listed in s9 | `TestDeterministicPackagesDoNotImportLLM`; runtime assertion that `model_usage.purpose` in the deterministic set is empty in the eval corpus |
| 16 | Minimal iteration context | Context Engine iteration mode + Iterate Until Verified template (brief §22) | Iteration N gets task, the previous `REVISE` feedback only, current diff, failing check signature; not earlier outputs | `TestIterationPackageContainsOnlyFeedback`; eval compares tokens per iteration |
| 17 | Watchdog | `internal/watchdog` (pure Go, no LLM) | s5 | Engine acceptance suite from brief §4 (repeat-read nudge then pause, error echo, per-turn cap, false-positive control in Iterate Until Verified) plus s5 unit corpus |

## 2. Context package and manifest

Per run, the Context Engine builds a controlled package (brief §9): task, role instructions, project instructions, pack instructions, relevant room messages, relevant files, git diff, previous results and handoffs, workflow state, memory hits, MCP results. Each package has a manifest stored in `context_manifests` (header) and `context_manifest_entries`:

| Field | Meaning |
|---|---|
| Header: `run_id, node_id, attempt, created_at, target_provider, target_model, data_class_ceiling, token_budget, package_sha256, prefix_sha256, prefix_tokens, volatile_tokens, builder_version` | `prefix_sha256` is what rule 7's test compares |
| `entry_id, ordinal` | Position in the rendered package |
| `source_type` | `task`, `role_instructions`, `project_instructions`, `pack_instructions`, `room_message`, `room_digest`, `file_range`, `file_whole`, `diff`, `previous_result`, `handoff`, `workflow_state`, `memory_hit`, `mcp_result`, `repo_map`, `skill`, `pointer` |
| `source_ref` | Message id, path plus `[start,end]` and content hash, commit, memory id, MCP call id, skill id plus pinned commit |
| `included` / `exclusion_reason` | `over_budget`, `not_addressed`, `redundant`, `stale`, `secret_blocked`, `data_class`, `untrusted_suppressed`, `duplicate_of:<entry>` |
| `bytes, est_tokens, token_source` | `estimated` (our counter) or `reported` (after the call, back-filled) |
| `priority, reason` | Why it was selected: rule that pulled it in and rank |
| `sensitivity` | `private` or `public_docs` (security.md s9) |
| `trust` | `trusted` (app-authored) or `untrusted` with provenance (security.md s10) |
| `scan` | `clean`, `redacted:<n>`, `blocked` (gitleaks) and `suspicious` flag |
| `cache_zone` | `stable_prefix` or `volatile_tail` |
| `compressed_from` | Digest ids when old room history was compressed into rolling digests (brief §9) |
| `raw_ref` | Disk reference for truncated bodies (rule 8) |

A run is "fully explained by manifest, usage, checkpoints and events" (brief §24, Slice 1D). Limit stated in the UI: the manifest covers what Drydock sent; files an agent CLI reads itself appear only as tool calls when the adapter reports them (`ToolCallVisibility`).

Repo map: token-budgeted, personalized PageRank, cached by content hash (Aider technique, GP s13); behind a build tag until the Windows CI job is green (D5), so Slice 1 manifests contain no `repo_map` entries and the matrix rows that mention it degrade to `read_range` and `search_*`.

## 3. Handoff artifact

Written by the outgoing agent on request (verified by the engine: required fields present, files and refs exist) or synthesized deterministically by Drydock when the agent cannot answer (watchdog stage 2, crash). Fields: `task_id, goal, acceptance_criteria_status, worktree_ref` (safe-point ref and head), `done, remaining, decisions, files_touched` (paths and hashes), `failing_check_signature, commands_of_note, open_questions, evidence_refs` (run output refs, not content), `next_step`. Size cap 1,500 tokens (**design assumption**), enforced at write time. Stored in `handoffs`; the next session receives the artifact plus the task, never the prior transcript (rule 5). Escalation to a premium model always carries one (brief §6).

## 4. Prompt assembly and cache layout (rule 7)

Order, outermost first: tool definitions, system/role/project/pack instructions, generated instruction file content, repo map, pinned standing context, then the volatile tail: handoff, task, thread excerpt, diff, check output, untrusted blocks. This follows Anthropic's prefix order `tools -> system -> messages` (MP D). Facts the builder honors, all from the appendices:

- Anthropic: `cache_control` on a block or one top-level automatic setting; default 5 minute TTL refreshed on use, optional 1 hour at extra cost (5m write 1.25x, 1h write 2x, read 0.1x for the quoted example model); exact-match, 100% identical segments, isolated per workspace; minimum cacheable length is 512 to 4,096 tokens depending on model and shorter prompts are silently uncached (detect `cache_creation_input_tokens` and `cache_read_input_tokens` both 0); changing tools, `tool_choice`, thinking, effort, images, or speed invalidates (MP D).
- OpenAI: automatic prefix caching, minimum 1,024 tokens, `prompt_cache_key` to improve routing, retention `in_memory` or `24h` (spec text, MP D); the cross-model statement is secondary evidence.
- Gemini: implicit caching on 2.5+ (read multiplier 0.25), explicit caches are model-bound; free and paid retention differ (MP C, G).
- OpenRouter: sticky routing per account, model and conversation, 10 minute expiry, wants a stable `session_id`; with `provider.order` set it is unused (MP G). Never use OpenRouter's response cache header for private data (MP A.8).
- Switching model mid-session recomputes the whole prompt (Claude Code docs: "Each model has its own cache", MP G, AC s1); tool order and tool-definition text are therefore frozen per session, and the router receives `SwitchCostTokens` (model-routing s8).

The builder records `cache_read` and `cache_write` per call; if a prefix above the model's minimum shows zero reads on the second identical-prefix call, it emits a `context.cache_miss` warning event so prefix drift is visible. Local hosts: enable usage detail flags (`--enable-prompt-tokens-details` on vLLM, `--enable-cache-report` on SGLang, MP E) or cached tokens will not be reported.

## 5. Watchdog (`internal/watchdog`, pure Go, no LLM; rule 17)

Inputs: normalized `ToolEvent{CallID, Tool, ArgsCanonical, ResultNorm, IsError, Start, End}` emitted by adapters (brief §7), text chunks, check results at `check` nodes/back edges, the worktree diff hash, and per-turn counters. Adapters must emit normalized `tool_call`/`tool_result`; a CLI that cannot sets `ToolCallVisibility=false` and gets reduced detection. Visibility by CLI (AC s12): Claude true; Gemini true; OpenCode true over ACP (completed-only in headless `run`); Codex partial in `exec --json` (shell, MCP, file paths, web search; ACP full); Goose true over ACP; Antigravity headless thin; custom CLIs unknown until the conformance suite records them.

**Canonicalization of arguments.** JSON with sorted keys; paths made worktree-relative and case-folded on Windows; read ranges kept (a different range is a different call); volatile ids removed.

**Normalization of results and texts (before hashing).** Strip timestamps (ISO 8601 and `HH:MM:SS[.mmm]`), durations, temp paths (`%TEMP%`-style Windows paths and `/tmp`), hex addresses (`0x...`), ANSI escape sequences (`charmbracelet/x/ansi` `Strip`, verified in GP s10), trailing whitespace; proposed additions: CRLF to LF, GUIDs. Hash at most the first 4 KB of the normalized text (SHA-256).

| Signal | Definition | Window | Default trip |
|---|---|---|---|
| Tool spin | same `(tool, canonical args, normalized result)` | rolling window of 8 events in the turn | 3 occurrences (brief §7); 2 after a soft-stagnation warning |
| Error echo | same failure signature and unchanged diff hash at a `check` node or back edge | consecutive check results in the loop | brief names the signal but not the count; proposed trip on the 2nd consecutive identical result with no diff change (**design assumption**) |
| Hard caps | tool calls, wall time, consecutive tool errors | per turn | 60 calls, 20 minutes, 5 consecutive errors (brief §7; the brief's wording attaches "per turn" to the list, treated here as per turn) |
| Soft stagnation | bigram Jaccard of consecutive agent texts of 200+ characters | last 3 comparisons | at least 0.85 three times: warn, lower tool-spin threshold to 2 (does not cancel) |

Failure signature for error echo: for tests, sorted failing test ids plus first normalized error line of each; for build/lint/typecheck, normalized first N diagnostics (N=5, **design assumption**); for generic commands, exit code plus first 4 KB normalized.

**Stage 1 (first trip on a task).** Cancel the turn (ACP `session/cancel`, Claude control-protocol `interrupt` or SIGINT; Windows signal delivery is unverified, AC s1) and send a nudge containing the evidence: signal name, the repeated call or failure signature, the count, what to change. The nudge is the first message of the next turn in the same session; a `watchdog_trips` row and `watchdog.trip` event are written.

**Stage 2 (second trip on the same task, across attempts and sessions).** Write a handoff artifact (agent-written if responsive, else synthesized), set the task `WAITING_HUMAN`, the agent `NEEDS_ATTENTION`, emit `watchdog.escalated` and a notification (Wails notifications service exists, GP s1), post an `ERROR`/`SYSTEM` message (room-protocol s12), and keep the task for the dead-letter set (brief §12). The router treats one stage-2 trip as an escalation trigger after human resume (model-routing s8). The watchdog never freezes (suspends) a process; stage 2 leaves the process idle or lets the Runtime stop it gracefully, and Job Object kill-on-close still cleans up (D7).

**Thresholds per template.** The brief fixes only the global defaults above and requires a false-positive control in Iterate Until Verified. The per-template table below is **proposed** and must be tuned against the measured false-trip rate (brief §23):

| Template | Tool spin | Error echo | Caps (calls / min / errors) | Notes |
|---|---|---|---|---|
| Full Stack SaaS | 3/8 | 2nd identical | 60 / 20 / 5 | Long builds happen in `check` nodes, which do not count against agent turn time |
| Feature | 3/8 | 2nd identical | 60 / 20 / 5 | Spec gate turn (PM role) uses a lower call cap: 25 |
| Bug | 3/8 | 2nd identical | 45 / 15 / 5 | Repeated reproduction is expected: reproduction commands with changing output are not spin |
| Security | 4/8 | 2nd identical | 60 / 20 / 5 | Read-heavy review; identical re-reads still count, different ranges do not |
| Iterate Until Verified | 3/8 | trips only when diff hash is unchanged between iterations | 60 / 20 / 5 per iteration; loop limit 5 iterations (brief §22) | Repeating a failing check with a changed diff is progress and must not trip (the false-positive control test) |

**Visibility fallback (`ToolCallVisibility=false`).** Cannot run: tool spin, tool-call count cap, consecutive-tool-error cap. Still run: wall-time cap (20 minutes), soft stagnation on text, error echo at Drydock-run `check` nodes (uses diff hash and check output, not the CLI's tool events), and two proposed substitutes: a no-progress timer (no worktree diff change and no new output for 10 minutes while `WORKING`) and an output-volume cap per turn. The reduced detection is recorded on the agent (`watchdog_mode=reduced`) and shown in the Agent Inspector next to watchdog status.

Events and storage: `watchdog.warn`, `watchdog.trip`, `watchdog.escalated`; `watchdog_trips(id, task_id, run_id, stage, signal, evidence_json, evidence_hash, diff_hash, created_at)`; evidence holds hashes and short normalized excerpts, not raw tool output, and passes the secret scan.

## 6. Session policy and context occupancy (rule 6)

Occupancy source per CLI: ACP `usage_update{used,size}` from the Claude, Codex (no cost), OpenCode and Goose bridges; none from Gemini (token counts only in non-standard `_meta.quota`); Claude headless `result.modelUsage.contextWindow` (AC s7.2, s1). The ACP SDK marks `usage_update` and prompt `usage` UNSTABLE (GP s5), so the adapter validates shapes and falls back to Drydock's own count of what it sent plus tool results (labeled `estimated`). At 70% when the agent is idle, the Runtime asks for a verified handoff, ends the session and starts a fresh one seeded with it inside the same node execution (brief §5). Resume capability is read from `agentCapabilities` at `initialize` (Gemini and Goose advertise `loadSession` only, D10), so same-session continuation is never assumed.

## 7. Budgets and ceilings (rules 12 and 14)

Scopes: project, workflow, task, agent, each with `{limit_usd, limit_tokens, spent_estimated, spent_reported, pct_used, state}`. A reservation (estimate from the manifest and price table) is taken before a call and settled with the observed usage after; unsettled estimates count so concurrent agents cannot jointly overrun. 80% emits `budget.warn` and the router downshifts (model-routing s7); 100% pauses the scope, creates a request and sets the run `WAITING_HUMAN`. Per-run ceilings (tokens, cost, wall time, turns) are separate: crossing one asks the human before continuing. Subscription CLIs have no dollar budget; the second metric is tracked in provider units (s9) and the CLI's own rate-limit signals feed the quota ledger (model-routing s6).

## 8. Caches (rule 13)

**`llm_cache`** (SQLite, `internal/llm`). Key: SHA-256 of canonical JSON of `{schema_v, provider_id, base_url_hash, model, effort, max_tokens, sampling (temperature, top_p, seed), system, messages, tools (in order), tool_choice, response_format, provider_routing (OpenRouter data_collection/zdr object), data_class, purpose}`; excluded: API key, request ids, timestamps. Value: response content, tool calls, finish reason, usage at fill time, `created_at`, `ttl`, `hit_count`, `cost_usd_at_fill`. Rules: exact match only; never for calls flagged side-effecting (default: a call is cacheable only if its `purpose` is on an allowlist such as `classify`, `summarize`, `consult`, `probe`); hits write a `model_usage` row with `cache_hit=true`, zero tokens and zero cost, and an estimated saving labeled `estimated`; content is secret-scanned before store; entries carry the data class and are purged with the project; default TTL is purpose-specific (CONSULT answers are stamped with source age because documentation changes, brief §15); private completions are never sent to or cached by a provider-side response cache (MP A.8).

**`mcp_cache`** (SQLite, `internal/mcp` Gateway). Key: SHA-256 of `{server_id, server_config_hash, server_version, tool, canonical_args, auth_principal, project_id}`. Value: raw result reference, shaped result, `fetched_at`, `ttl`. Rules: only tools marked read-only (for example Cloudflare's docs search carries `readOnlyHint`, TI s1a; unknown tools default to not cacheable); per-server TTL from `catalog.yaml` with conservative defaults for volatile sources (GitHub and issue trackers short, documentation servers long); invalidated on server config change; results stay marked untrusted and are rescanned on read; shaping (truncate, `fetch_range`) is applied after the cache so the same raw result serves several shapes. Result sizes of the docs servers are unverified (TI s6), so the per-tool byte cap is configurable and set from measurements, for example Learn's `maxTokenBudget` parameter (TI s6).

## 9. Measurement and pricing

Every model call and every agent run writes `model_usage`; a run's usage is the sum of its turns. Both metrics are computed only from rows that exist; nothing is back-filled.

```go
type Usage struct {
    InputTotal    int // all input tokens including cached (OTel view, D12)
    InputUncached int
    CacheRead     int
    CacheWrite    int // if the source splits TTLs: CacheWrite5m, CacheWrite1h
    Output        int
    Reasoning     int
    ContextSize   int // occupancy at end of turn, when known
    TokensSource  Source // reported | estimated
    CostUSD       *float64
    CostSource    Source // reported | computed_from_table | unknown
}
```

`model_usage` columns: `id, ts, run_id, node_id, attempt, task_id, decision_id, agent_id, role, workflow, template, tier, provider_id, model, purpose, usage fields above, price_row_id, cache_hit, latency_ms, manifest_id, sources_json, exclusions_json`. "Sources and exclusions" come from the manifest so a number can be traced to what was sent and what was left out (brief §7).

Normalization happens in one place (D12), with per-source fixtures captured from real streams (not yet captured; Phase 0 had no logins, phase0 s1):

| Source | Fields | Normalization notes |
|---|---|---|
| Anthropic API / Claude CLI | `input_tokens` (excludes cached), `cache_read_input_tokens`, `cache_creation_input_tokens` (TTL split `Ephemeral5m/1h`), `output_tokens`; CLI `modelUsage{...,costUSD,contextWindow}`, `total_cost_usd` (client-side estimate) | `InputTotal = input + cache_read + cache_creation`; CLI cost is `reported` but flagged `client_estimate`; subscription users pay no per-token dollars |
| OpenAI-compatible / OpenRouter | `prompt_tokens`, `completion_tokens`, `prompt_tokens_details.{cached_tokens, cache_write_tokens}`, `completion_tokens_details.reasoning_tokens`, `cost`, `cost_details`, `is_byok` | Read the final stream chunk ourselves: `ChatCompletionAccumulator` drops `CacheWriteTokens` (GP s6); OpenRouter `cost` is `reported` |
| Codex `exec --json` | `turn.completed.usage{input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens}` | No dollars; whether `input_tokens` includes cached is unverified, the fixture decides |
| Gemini CLI | `result.stats{total_tokens, input_tokens, output_tokens, cached, input, models{...}}` | No dollars; over ACP only `_meta.quota` without cache split |
| OpenCode | `step_finish.part{cost, tokens{input, output, reasoning, cache{read,write}}}`; ACP `usage_update` plus prompt `usage` | Cost reported by the CLI |
| Goose | `complete{total_tokens, input_tokens, output_tokens, cache_read_input_tokens, cache_write_input_tokens, cost_usd}`; ACP `usage_update` | |
| Antigravity | step usage in `step_update` (shape unverified); `agy -p "/usage"` for quota | |

**Estimated versus reported are never mixed silently.** Each row carries `TokensSource` and `CostSource`; every aggregate API returns a breakdown by source, the UI badges mixed totals, and reports in `docs/evals.md` print reported and estimated columns separately. Estimates (pre-call reservation, tokenizer counts) are replaced by reported values when they arrive but both are kept (`est_tokens`, `reported_tokens`).

**Pricing table `model_prices`:** `provider_id, model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_5m_per_mtok, cache_write_1h_per_mtok, reasoning_policy (billed_as_output|included), currency, source (catalog|builtin|user), effective_from, updated_at, user_edited, note`. Editable in Settings with a visible last-updated date (brief §7); prices from the OpenRouter catalog update rows with `source=catalog` and never overwrite a `user_edited` row (the user's value wins, a drift warning shows). Defaults for cache pricing when only the input price is known come from documented multipliers: Anthropic read 0.1x, 5 minute write 1.25x, 1 hour write 2x (model-dependent footnotes exist); Google read 0.25x; OpenAI cache write 1.25x for newer models per OpenRouter's doc (MP D, G). Built-in rows are labeled with the evidence date; none is a recommendation. Cost is `computed_from_table` only when no reported cost exists.

**Metrics.** Cost per accepted task = sum of cost of all runs for the task, including failed attempts, rejected reviews, watchdog escalations and cache fills, divided by tasks that reached `COMPLETED` with human acceptance, grouped by role, workflow and tier, with reported and estimated shown apart. Premium-subscription usage per accepted task = vendor-specific units: tokens and turns on subscription CLIs plus rate-limit utilization deltas where the CLI exposes them (Claude `rate_limit_event.utilization`, Codex `usedPercent`, Antigravity `/usage`); Gemini CLI, OpenCode and Goose expose none, so for them only token counts exist and the metric is labeled approximate.

Sanctioned LLM uses (rule 15): CONSULT answers, rolling room digests, changed-pages digests for packs, optional minutes summary, ambiguity tiebreak in the router, handoff drafting by the outgoing agent. Everything else (routing by rule, watchdog, markers, inbox, exports skeleton, check summarization, diff/stat) is code.

## 10. Telemetry boundary

OpenTelemetry export is optional and off by default; attribute names are our own constants (D12); prompts, code, tool output and secrets are never exported unless explicitly enabled (brief §10). Token attributes use the normalized `InputTotal` (cached included) with cache read and write kept separate; `cache_creation` versus `cache_write` naming drift in upstream conventions is avoided by owning the keys (GP s8).

## Open questions / unverified

- No real streams exist for any CLI (no logins in Phase 0): cached-token semantics per CLI, rate-limit event shapes, `usage_update` stability, mid-tool interrupt behavior and Windows signal delivery are unverified; normalization fixtures must be captured before the normalizer is trusted.
- Context occupancy for Gemini is only an estimate; the 70% handoff for Gemini and any custom CLI depends on Drydock's own counter.
- Native-tool output inside a CLI cannot be truncated by Drydock (rule 8) and files a CLI reads itself are invisible to the manifest (rule 9, 11); only Gateway tools, Drydock-run checks and the package we assemble are enforceable. Rules 8, 9 and 11 are therefore enforced for Drydock-mediated paths only.
- Watchdog numbers other than those in brief §7 (error-echo count, per-template caps, no-progress timer, signature depth, handoff cap, TTLs, tool-argument canonicalization details) are design assumptions to be tuned from the false-trip rate and `docs/evals.md`.
- Brief §7 attaches "per turn" ambiguously to the hard-cap list; interpreted as per turn.
- Result sizes of MCP docs servers (Cloudflare, Learn, Context7, GitHub) are unmeasured, so Gateway caps are provisional.
- OpenRouter usage fields through `openai-go` against a real response (cache writes, cost), and Gemini `cached_tokens` in the compatibility endpoint, are unverified.
- Prefix-cache behavior on Windows hosts and local servers (vLLM/SGLang/Ollama flags) is unmeasured; the GPU benchmark has not been run.
- The repo map is gated by D5; until the Windows CI job is green, rules 9 and 11 rely on `read_range` and search tools alone.

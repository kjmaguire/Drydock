# Drydock: token and cost control design

Scope: this document maps each of the 17 token and cost rules in brief §7 to the component that enforces it and to the test that proves it, and specifies the watchdog (signals, windows, normalization, stage 1 and 2, thresholds per workflow template, and the fallback when an agent CLI gives no tool-call visibility), the measurement and pricing-table design, the context manifest, and the two caches (`llm_cache` and `mcp_cache`). Primary success metric is cost per accepted task; the second is premium-subscription usage per accepted task (brief preamble). "brief §N" refers to `docs/brief.md`; D1-D14 to `docs/decisions.md`; evidence is cited to `docs/phase0.md` and the appendices (`MP` = model-providers, `AC` = agent-clis, `GP` = go-platform, `TI` = tooling-integrations). Sections 1 to 10 are the design: the test names in them are the original proposals, and much of it is now built (budgets, the watchdog, checks, telemetry, manifests, routing of agent turns); the "As built" section at the end says what exists and where the code differs, and anything it does not list should be treated as not built. No savings are claimed: any improvement needs measured numbers in `docs/evals.md` (brief §0, §23). A hard limit applies throughout: Drydock controls what it assembles, sends and runs; it cannot control what an agent CLI reads from the worktree or sends to its own provider by itself, and it observes those only through tool-call events (when the CLI emits them) and usage reports.

## 1. Rule matrix (brief §7, 17 rules)

The last column names the Go tests that prove each rule as built, with their package; every name in it exists (checked 2026-10-11, decision P39). Where part of a rule is not built, the cell says so and names no test for that part. Before P39 the column held proposed names, none of which existed (audit F8). Integration tests use real CLIs, real Git and fixture repos and are reported as skipped with a reason when a CLI is absent or unauthenticated (P3). A labeled stub is used only to test a failure boundary (brief §0).

| # | Rule (brief §7) | Enforcing component | Mechanism | Proving test |
|---|---|---|---|---|
| 1 | No broadcast context | `internal/context` (Assemble) + `internal/room` delivery | Room messages enter a package only if addressed to the agent (mention, thread membership, assignment) or selected by retrieval; there is no "all room messages" source type | `TestSelectContextRules` (`internal/room`: chatter, proposed decisions, another task's decisions and a handoff to someone else are left out as `not_addressed`), `TestContextForReadsOnlyThisRoomAndThread` |
| 2 | Orchestrated turn-taking | `internal/room` turn scheduler + Agent Runtime | `StartTurn` requires a `TurnGrant` from the scheduler (modes `auto`, `sequential`, `moderator`, room-protocol s6); no grant, no run | Turn plans: `TestNextSpeakersAuto`, `TestNextSpeakersSequential`, `TestNextSpeakersModerator` (`internal/room`), `TestModeratorModeLetsTheModeratorNameTheSpeakers` (`internal/agents/responder`). Not built: a `TurnGrant` the runtime checks before a turn (no such type exists; the runtime starts any turn it is asked to) |
| 3 | Bounded debate (about 3 rounds) | Room scheduler round counter + workflow group-collaboration node (brief §4) | Per-thread round counter, default 3; at the bound the scheduler posts `SYSTEM`, produces a `DECISION` candidate or a human `QUESTION`; no further agent turns on that topic | `TestDebateStopsAtBoundWithAlwaysDisagreeStub`, `TestSequentialRoomStopsAfterThreePasses` (`internal/room`), `TestAgentsPingPongingByMentionStopAtTheBound`, `TestSequentialModeStopsAtTheRoundBoundAndAsksTheHuman` (`internal/agents/responder`). Not built: a group-collaboration workflow node |
| 4 | Team-size routing | `internal/router` (`TeamSize` in `RoutingDecision`) | Default 1; greater than 1 only when a rule sets it (risk, Quality and above); never from agent request | `TestEffortAndTeamSize` (`internal/router`: 1 by default, 2 only at Maximum Quality with risk, a rule's value otherwise) |
| 5 | Handoff artifacts, not transcripts | Agent Runtime handoff service + `internal/context` | Session changes (escalation, 70% occupancy, new task) seed the fresh session with a handoff artifact (s6) and never with the prior transcript | `TestFreshSessionSeededOnlyWithHandoff`, `TestEscalationSeedsTheNewModelsSessionWithAHandoff` (`internal/pipeline`), `TestTheRouterChoosesTheModelOfAgentTurnsAndEscalates` (`internal/core`). As built (P39): a work turn that does not continue the previous session of its task starts a fresh one seeded with a handoff artifact Drydock writes (s3, s6), never the transcript; the watchdog's stage 2 writes one too |
| 6 | Session policy | Agent Runtime | Fresh session per independent task; same-session continuation only when cheaper or better; at 70% occupancy when idle request a verified handoff and start fresh within one node execution (brief §5) | `TestOccupancyThresholdTriggersHandoff`, `TestBelowTheThresholdTheSameSessionContinues`, `TestAResumeThatFailsStartsAFreshSessionWithAHandoff` (`internal/pipeline`), `TestATurnWhoseSessionNeverStartedIsMarked` (`internal/agents/runtime`). As built (P39): decided between two turns, never inside one (s6) |
| 7 | Cache-friendly prompts (stable prefix first) | `internal/context` PromptBuilder, `internal/llm` | Fixed section order, volatile data last (s4); cache breakpoints on provider APIs that support them | `TestPrefixByteStable` (two requests that differ only in the last message are the same bytes apart from it, and a request is the same bytes every time), `TestRequestCarriesCacheControlOnTheStablePrefix` (`internal/llm/anthropic`); integration `TestLiveAnthropicPromptCache` (needs a key; skipped otherwise) |
| 8 | Truncate tool output; raw kept on disk; fetch by range | MCP Gateway shaping (brief §14), adapters' tool-result handling, run output store (`<app data>/runs/<run_id>/<node>/<attempt>/`) | Results over the per-tool cap are cut with a marker `[truncated N of M bytes; fetch_range(ref, offset, len)]`; raw saved first | `TestTruncationAndFetchRangeRoundTrip`, `TestFetchRangeLimitsAndIsolation` (`internal/mcp`), `TestTruncateKeepsHeadAndTailAndPointsToTheLog` (`internal/checks`). Limit: output a CLI feeds to its own model from its native tools cannot be truncated by us |
| 9 | Retrieve, don't preload | `internal/context` + Workspace MCP server (`search_pack`, `search_requirements`, `search_memory`, `repo_map`, `read_range`, `task`, `skills`, `room`, `handoff`) | Initial package carries task, instructions, a token-budgeted repo map and pointers; content comes through tools on demand | `TestAcceptedMemoryIsCappedNewestFirst` (`internal/context`); `TestRepoMapPageAndAgentsSection` (`internal/core`: the repo map section of AGENTS.md, 1024 tokens, only from 25 source files, recorded in the manifest). Not built: a ceiling on the whole initial package; the manifest lists what was left out with the reason |
| 10 | Deterministic tools before LLMs | Workflow engine ordering + `check` nodes (brief §4, §13a) | Review nodes depend on a passing `check` edge; a reviewer is never started on a failing `build/test/lint/typecheck` | `TestReviewNotStartedBeforeChecks`, `TestIterateUntilVerifiedHappyPathEndsInHumanMerge` (`internal/pipeline`) |
| 11 | Ranges and diffs over whole files | Context Engine + `read_range` + diff provider | Files above a line cap enter as ranges; changes enter as diffs against the safe point (`refs/aet/<task>/<attempt>`, brief §13) | `TestReviewPromptSaysWhenTheDiffIsTruncated` (`internal/pipeline`: the reviewer gets the diff), `TestReadRangeConfinement` (`internal/mcp`). Not built: files entering the package as ranges (agents read files themselves) |
| 12 | Per-run ceilings with human escalation | Budget middleware + request port (brief §4) | Ceilings on tokens, cost, wall time, turns per run; crossing creates an `approval`-type request, run to `WAITING_HUMAN`; resume only by human response with a new ceiling | `TestExceededBudgetPausesBeforeTheNextTurnAndOnlyAHumanResumes`, `TestPerRunCeilingTemplateAppliesToEachRunSeparately` (`internal/budget`), `TestApprovalResumeAcrossKill` (`internal/workflow`) |
| 13 | Exact-match response cache | `internal/llm` `llm_cache`, `internal/mcp` `mcp_cache` (s8) | SHA-256 of canonical request; never for side-effecting calls | `TestCacheHitSkipsProviderAndRecordsASavedUsageRow`, `TestSideEffectingAndUnlistedCallsAreNeverCached` (`internal/llm`), `TestCacheHitMissAndTTLExpiry` (`internal/mcp`) |
| 14 | Budgets at project, workflow, task, agent (warn 80%, pause 100%) | `internal/budget` | Scoped counters; reservation before the call, settlement after; 80% emits `budget.warn`, 100% pauses the scope (`budget.paused`) and creates a request | `TestThresholdBoundaries`, `TestWarningAt80PercentFiresOnceAndTheRunContinues`, `TestExceededBudgetPausesBeforeTheNextTurnAndOnlyAHumanResumes` (`internal/budget`) |
| 15 | No LLM where rules suffice | Package boundaries; router, watchdog, marker parser, minutes export, inbox derivation, check summarizer | Deterministic packages may not import `internal/llm` (enforced by an import-boundary test over `go list -deps`); the only sanctioned LLM uses are listed in s9 | `TestRouterDoesNotImportLLM` (`internal/router`). Not built: the same check over the other deterministic packages, and the eval-corpus assertion |
| 16 | Minimal iteration context | Context Engine iteration mode + Iterate Until Verified template (brief §22) | Iteration N gets task, the previous `REVISE` feedback only, current diff, failing check signature; not earlier outputs | `TestPipelineLoopsOnFailingChecksWithMinimalContext` (`internal/pipeline`), `TestWorkPromptKeepsTheTaskOnEveryPassAndRendersGuidanceAsText` (`internal/pipeline`). The prompt carries only this; below 70 % occupancy the session itself continues (rule 6, P39) |
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

Repo map (decision P62, `internal/repomap`): token-budgeted, personalized PageRank, cached by content hash (Aider technique, GP s13). AGENTS.md carries a small one (`RepoMapAgentsBudget`, 1024 tokens) in a repository of 25 or more source files, after the other generated sections (section "Repository map"); the turn's manifest lists it as `repo_map` with its size, or as left out with the reason (a small repository, no parser in a build without CGO, mapping slower than 20 s). A bigger or focused map comes through the `repo_map` tool on demand. A build without CGO has no repo map: the matrix rows that mention it then degrade to `read_range` and `search_*`.

## 3. Handoff artifact

Written by the outgoing agent on request (verified by the engine: required fields present, files and refs exist) or synthesized deterministically by Drydock when the agent cannot answer (watchdog stage 2, crash). Fields: `task_id, goal, acceptance_criteria_status, worktree_ref` (safe-point ref and head), `done, remaining, decisions, files_touched` (paths and hashes), `failing_check_signature, commands_of_note, open_questions, evidence_refs` (run output refs, not content), `next_step`. Size cap 1,500 tokens (**design assumption**), enforced at write time. Stored in `handoffs`; the next session receives the artifact plus the task, never the prior transcript (rule 5). Escalation to a premium model always carries one (brief §6). As built (P39): Drydock synthesizes the artifact itself (no request to the outgoing agent, so no extra paid turn): `kind`, `trigger`, `reason`, the task (title, description, acceptance criteria, status, iteration, branch, files), `done` (the previous session's final summary, at most 1,500 characters), `diffStat` and `filesTouched` (the worktree against its base), `failingChecks` (name, exit code, failing tests), `remaining` (review feedback and guidance in the inbox), `openQuestions` (question lines of the previous summary), the sessions and models on both sides and the occupancy. It is redacted by the secret scanner and capped at 8 KB (`pipeline.SaveHandoff`, which the watchdog's stage-2 artifact also uses). Not built: `decisions`, `commands_of_note`, `next_step`, file hashes, and verification of an agent-written artifact.

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

**Normalization of results and texts (before hashing).** Strip timestamps (ISO 8601 and `HH:MM:SS[.mmm]`), durations, temp paths (`%TEMP%`-style Windows paths and `/tmp`), hex addresses (`0x...`), ANSI escape sequences (`charmbracelet/x/ansi` `Strip`, verified in GP s10), trailing whitespace; proposed additions: CRLF to LF, GUIDs. Hash at most the first 4 KB of the normalized text (SHA-256). As built (decision P38): at most 4 KB, but a longer text keeps its first and last 2 KB and its length, because `go test ./...` prints its stable `ok` lines first and the failure that changes as the agent works last; the first 4 KB alone read that progress as a spin (`TestALongOutputThatChangesAtTheEndIsNotASpin`).

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

Occupancy source per CLI: ACP `usage_update{used,size}` from the Claude, Codex (no cost), OpenCode and Goose bridges; none from Gemini (token counts only in non-standard `_meta.quota`); Claude headless `result.modelUsage.contextWindow` (AC s7.2, s1). The ACP SDK marks `usage_update` and prompt `usage` UNSTABLE (GP s5), so the adapter validates shapes and falls back to Drydock's own count of what it sent plus tool results (labeled `estimated`). At 70% when the agent is idle, the Runtime asks for a verified handoff, ends the session and starts a fresh one seeded with it inside the same node execution (brief §5). **As built** (decision P39): see "As built: session policy and handoffs" below; the decision is taken when the next work turn of the task starts, from the occupancy the previous turn reported when it ended, and the handoff is written by Drydock, not asked of the agent. Resume capability is read from `agentCapabilities` at `initialize` (Gemini and Goose advertise `loadSession` only, D10), so same-session continuation is never assumed.

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

**Pricing table `model_prices`:** `provider_id, model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_5m_per_mtok, cache_write_1h_per_mtok, reasoning_policy (billed_as_output|included), currency, source (catalog|builtin|user), effective_from, updated_at, user_edited, note`. Editable in Settings with a visible last-updated date (brief §7); prices from the OpenRouter catalog update rows with `source=catalog` and never overwrite a `user_edited` row (the user's value wins, a drift warning shows). As built (checked 2026-10-11): the table is `model_catalog`, a price the person edited carries `price_source='user'` and a catalog refresh keeps it (`internal/providers/catalog.go`); no drift warning is shown when the catalog's price differs. Defaults for cache pricing when only the input price is known come from documented multipliers: Anthropic read 0.1x, 5 minute write 1.25x, 1 hour write 2x (model-dependent footnotes exist); Google read 0.25x; OpenAI cache write 1.25x for newer models per OpenRouter's doc (MP D, G). Built-in rows are labeled with the evidence date; none is a recommendation. Cost is `computed_from_table` only when no reported cost exists.

**Metrics.** Cost per accepted task = sum of cost of all runs for the task, including failed attempts, rejected reviews, watchdog escalations and cache fills, divided by tasks that reached `COMPLETED` with human acceptance, grouped by role, workflow and tier, with reported and estimated shown apart. Premium-subscription usage per accepted task = vendor-specific units: tokens and turns on subscription CLIs plus rate-limit utilization deltas where the CLI exposes them (Claude `rate_limit_event.utilization`, Codex `usedPercent`, Antigravity `/usage`); Gemini CLI, OpenCode and Goose expose none, so for them only token counts exist and the metric is labeled approximate.

Sanctioned LLM uses (rule 15): CONSULT answers, rolling room digests, changed-pages digests for packs, optional minutes summary, ambiguity tiebreak in the router, handoff drafting by the outgoing agent. Everything else (routing by rule, watchdog, markers, inbox, exports skeleton, check summarization, diff/stat) is code.

## 10. Telemetry boundary

OpenTelemetry export is optional and off by default; attribute names are our own constants (D12); prompts, code, tool output and secrets are never exported unless explicitly enabled (brief §10). Token attributes use the normalized `InputTotal` (cached included) with cache read and write kept separate; `cache_creation` versus `cache_write` naming drift in upstream conventions is avoided by owning the keys (GP s8).

## Open questions / unverified

- No real streams exist for any CLI (no logins in Phase 0): cached-token semantics per CLI, rate-limit event shapes, `usage_update` stability, mid-tool interrupt behavior and Windows signal delivery are unverified; normalization fixtures must be captured before the normalizer is trusted.
- Context occupancy for Gemini is only an estimate; the 70% handoff for Gemini and any custom CLI depends on Drydock's own counter, which is not built: as built, a turn whose CLI reported no occupancy is never continued (P39).
- Native-tool output inside a CLI cannot be truncated by Drydock (rule 8) and files a CLI reads itself are invisible to the manifest (rule 9, 11); only Gateway tools, Drydock-run checks and the package we assemble are enforceable. Rules 8, 9 and 11 are therefore enforced for Drydock-mediated paths only.
- Watchdog numbers other than those in brief §7 (error-echo count, per-template caps, no-progress timer, signature depth, handoff cap, TTLs, tool-argument canonicalization details) are design assumptions to be tuned from the false-trip rate and `docs/evals.md`.
- Brief §7 attaches "per turn" ambiguously to the hard-cap list; interpreted as per turn.
- Result sizes of MCP docs servers (Cloudflare, Learn, Context7, GitHub) are unmeasured, so Gateway caps are provisional.
- OpenRouter usage fields through `openai-go` against a real response (cache writes, cost), and Gemini `cached_tokens` in the compatibility endpoint, are unverified.
- Prefix-cache behavior on Windows hosts and local servers (vLLM/SGLang/Ollama flags) is unmeasured; the GPU benchmark has not been run.
- Whether the repo map in AGENTS.md lowers what a task costs is unmeasured (no eval run compares turns with and without it); it costs about 1,000 tokens in every turn of a repository of 25 or more source files.


## As built: budgets, ceilings, checks and telemetry (2026-10-08, Slice 2, Linux sandbox)

Built in `internal/budget` (migration `00060_budget.sql`), `internal/telemetry` (`00063_telemetry.sql`), `internal/checks` (`secrets`, `deps`; `00062_checks_probe.sql`), wired by `internal/core/ext_economics.go`. Methods: `budget.list/set/delete/status/resume`, `checks.settings.get/set`, `telemetry.status/enable/preview/export`. Differences from the text above:

- **Scopes (rule 14, s7).** Built: `project`, `task`, `run`, `agent`. A `workflow` scope is not built (a run ceiling on a `*` template covers "per run"). A budget limits USD, tokens (input + output as recorded) and/or agent turns (`agent_runs` rows). `scope_id = '*'` makes a ceiling that applies to every task, run or agent (each judged alone), optionally restricted to one project: this is the per-run ceiling of rule 12. **Wall-time ceilings are not built** (the watchdog's per-turn 20 minute cap exists; a per-run wall-clock ceiling that excludes time waiting for the human has no definition yet).
- **Accounting is a sum, not a counter.** There is no reservation/settlement and no `spent_*` column: every evaluation sums `model_usage` (and `agent_runs`), so a budget can never disagree with the rows. The consequence the text above wanted to avoid exists: concurrent agents can jointly overrun a limit by the turns already running, because the check happens at turn boundaries only.
- **Cost never mixes silently.** A row's cost is `reported`, `estimated` or `unknown` (one classification, `budget.CostKindExpr`, shared with the evals). Unknown rows are counted and shown beside the dollar figure and are never priced at zero; a dollar limit only sees the known part, `costComplete=false` says so, and a token or turn limit is the exact guard for CLIs that report no cost.
- **Events.** `budget.warning` (once, at `warnPct`, default 80%), `budget.exceeded` (once, when a finished turn takes a budget to 100%), `budget.paused` (each time a turn is held), `budget.resumed`, `budget.changed`. The text above says `budget.warn`/`budget.paused`; the brief-level names were chosen as `budget.warning`/`budget.exceeded`. Each warning, crossing and pause also posts a SYSTEM message in the task channel. The room has no budget item type, so the **Needs-you item is the engine's own approval request** (kind `approval`, title `Budget limit reached: ...`); warnings are messages and events only.
- **Pause.** `Enforcer.Middleware` wraps every `agent_task` node: before the turn, any applicable budget at 100% returns an approval request with `Retry: true` instead of running the turn, so the run is `WAITING_HUMAN` and **the owed turn runs after the answer** (a new `RequestSpec.Retry` in `internal/workflow` restores the parked node's inbox; the engine acceptance tests still pass and one was added). The pause takes effect at the next turn boundary: a turn already running when the limit is crossed completes. Only a `human:` identity resumes: `budget.resume` optionally raises the limit of each exceeded budget (compare-and-swap, clears the fired alerts so thresholds re-arm) and answers the request; approving without raising the limit lets exactly one more turn through (the approval is consumed by that turn); rejecting fails the run with the reason. The gate reads the human's answer from the `approvals` row and refuses one whose `decided_by` is not a `human:` identity. A budget that cannot be evaluated fails closed (the turn does not start).
- **Agent scope** needs the node to say which agent it runs: `Meta["agent"]` of `work` or `review` (the pipeline graphs set it); a custom graph without it is not limited by agent budgets.
- **Router downshift at 80% (s7).** The router's `budget-downshift` rule and the hard-limit block read `RouteRequest.Budget`. `route.preview`, `core.EconomicsRouter` and the routing of agent turns (`internal/core/turnrouting.go`) fill it from the budget store. Agent work and review turns are routed only for an agent whose human listed the models the router may choose between (Team page), so the live downshift applies to those agents only; every other agent runs its configured model. A turn a human allowed with "one more turn" over a hard limit is routed as usual (`budget.ApprovedOverLimit`) instead of being blocked by the router.
- **Other turns.** Room and DM replies check the budgets before a reply starts (project, the agent, and the task's in a task room; `EconomicsBudgetBlock`): an exceeded budget means no reply, and the refusal is posted in the room. The team lead's turn is checked the same way first. A budget that cannot be read refuses the turn (fail closed). The human's own chats (`chats.md`) are not budget-gated.
- **Watchdog stage 2 (s5).** As built it does not set the task `WAITING_HUMAN` or the agent `NEEDS_ATTENTION`: the run waits on a question, the task keeps its state and the agent's presence shows Needs Attention (`state-machines.md` s5). Thresholds per node come from `watchdog.ForNode`, not from the per-template table above (`workflow-engine.md` s9).
- **Telemetry (s10, D12).** A local SQLite sink, **off by default**, human-enabled, no OTLP exporter and no dependency. Attribute names are `internal/telemetry` constants (`drydock.*`, and `gen_ai.*` names for model, provider, token type and token counts, with input including cached tokens and cache read/write kept apart). An attribute is stored only if its key is on a closed allowlist and its value is identifier-shaped (no whitespace, at most 96 characters, nothing the secret scanner changes), so free text cannot be recorded by construction; dropped keys are counted by name, never their values. `telemetry.preview` shows what an export would contain, `telemetry.export` writes a generated-name JSON file under `<data dir>/telemetry/` and marks those rows exported. Spans are one per node execution (identity and outcome only); metrics are token usage, cost (only when a cost was reported or estimated) and node duration. LLM Wrapper calls are not instrumented. Middleware order is Watchdog, Telemetry, Budget (the core registers the watchdog first), not Telemetry first as in `workflow-engine.md` s7.
- **Checks (rule 10 and brief s13a).** `secrets` scans only the lines the task **added** (diff against the base, including uncommitted and untracked files) with the gitleaks-backed scanner; a finding fails the check and the output names rule, file and line only. `deps` runs `osv-scanner` as an external tool if it is on PATH (`--offline` per project); not installed gives `skipped: osv-scanner not installed`, recorded as evidence with no verdict plus a `task.check_inconclusive` event (posted in the room with status `skipped`), never a pass and never sent back to an agent. Per-project settings: secrets on, deps off by default. The JSON shape osv-scanner prints was taken from its documentation, not captured from a real run (it is not installed here).

Not built: pre-call reservation and settlement, a `workflow` budget scope, wall-time ceilings, a budget item type in the room inbox, OTLP export, telemetry for Wrapper calls.

## As built: Claude Code sessions and unattended spend (2026-10-10, decision P36)

On the owner's request ("make sure when we add claude code either by API and or CLI that it's not burning tokens"). Adding
Claude Code spends nothing: detection runs `claude --version` and `claude auth status`, and API providers are checked with
GET listings only. A subscription login and an API key run the same CLI with the same settings; only who pays differs
(`connections.md`, Claude-specific rules). What follows is what every session carries, and what could spend without a person
asking.

**Measured** with Claude Code 2.1.296 on haiku, in a small repository, with the ten plugins this machine's user has installed
(contract CLAUDE-TOKENS-01 repeats the main figures):

| Setup | First request of a session (tokens) | New tokens a session in a second worktree writes to the cache |
|---|---|---|
| Before (the argv of 2026-10-09) | 27,463 | 12,200 |
| With `QuietEnv` and `--disable-slash-commands` | 14,621 | 2,786 |
| Also with `UnattendedDenied` (as built) | 9,846 | 2,673 |

Every later request of a session reads this prefix again from the cache (a worker turn with 30 tool calls makes about 30
requests), so the 17,600 fewer tokens are saved on each request, not once per session.

What every Claude Code session Drydock starts now gets (`internal/agents/claude/adapter.go`; workers, reviewers, the lead,
room replies, chats and the connection test alike):

- **`QuietEnv`**, replacing any value inherited from the person's environment: no auto memory (a memory folder read into every
  session and written behind Drydock's own memory), no workflows (multi-agent runs started from inside a turn), no advisor
  (a second, larger model consulted on the side), no cron (prompts scheduled to run later; durable ones are saved into the
  repository's `.claude/scheduled_tasks.json`), no background tasks and no automatic backgrounding (a long command is waited
  for; moved to the background its result was lost and the turn wasted, as the sandbox had already measured), no idle
  check-in turns (`CLAUDE_CODE_GOAL_CHECKIN_MINUTES=0`), no prompt suggestions or away summaries. Each name and value was read
  from the CLI's code.
- **`--disable-slash-commands`**: the skill list of the person's Claude Code (bundled skills, their own, 47 from their
  plugins, those synced from claude.ai: 9,200 tokens here) is no longer in every request. Drydock's own accepted skills reach
  Claude as rules in the instructions (`internal/skills`), which this does not touch; automatic compaction is a separate
  switch in the CLI and keeps working.
- **`UnattendedDenied`** in the deny list: the tools that schedule or wake turns nobody asked for, reach past the session (the
  person's other Claude sessions, notifications, files sent to them), search or change their Claude Code setup (plugins,
  skills, connectors, the MCP registry), or leave the folder Drydock gave the agent (worktrees). A worker is offered 13 tools:
  Bash, Edit, Write, Read, NotebookEdit, Task, TaskStop, ToolSearch, WebFetch, WebSearch and the three MCP resource tools,
  plus the gateway's. CLAUDE-QUIET-01 (free) checks all three on every CLI version and warns about any tool nobody reviewed.

The whole Claude contract suite passed with these on 2.1.296: 20 passed, 0 failed, 3 skipped (the disabled ACP bridge, a
Windows-only check), $0.065 reported, including resume, interrupt, the MCP gateway, restricted (sandbox) mode and the long
MCP call.

**Measured and left as they are.** `--exclude-dynamic-system-prompt-sections` made no difference across worktrees (2,786 new
tokens either way). `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS` saves 147 tokens and removes the CLI's git safety rules. Reordering
Drydock's appended instructions cannot raise cache reads: the CLI caches the per-session part (environment, instructions,
prompt) as a whole. The prompt-cache lifetime stays the CLI's default. Changed since by decision P37: skills reach Claude by skill delivery
`relevant` (the default; the repository's own skills, except one whose `paths` match none of the task's files, and the
imported ones whose globs match the task's files are loaded, the others are listed and read on demand; measured in [`evals.md`](evals.md) section 14: putting the prebuilt agents' twelve skills in
every turn cost 1.9 times as much, `relevant` cost the same as no skills), and accepted memory goes into AGENTS.md newest
first up to 8 KB, each entry cut to 600 characters, the rest through `search_memory`; each turn's manifest says when what
Claude gets in every turn is over 32 KB. Not changed, with their cost visible: the person's own and their plugins' hooks still run (P16), so a SessionStart hook that adds
context adds tokens; installed plugins' MCP servers still start, with their tools denied (CLAUDE-MCP-01).

**Spend nobody asked for, closed.**

- **The team lead** (`internal/pipeline/lead_service.go`): a supervision turn that fails is stored (action `failed`) and counts
  toward the bound of 3 per task and run; before, a failing lead was asked again at every silence of a stalled run, each turn
  spending. What the monitor asked is read from the stored interventions, so a restart asks neither about the same silence
  nor about the same unassigned task again.
- **Restarts** (`internal/workflow/engine.go`): an agent turn cut off by a stop is re-run once on its own; cut off again, the
  run asks the person (approve runs it once more, reject fails the run). Before, a turn interrupted at every start was paid
  for at every start.
- **Test connection**: "Also send one tiny prompt" is off until the person ticks it; it is the only step that spends.
- **Drydock's own tests**: every Go test that prompts the real claude now runs only with `DRYDOCK_REAL_CLAUDE=1`
  (`contract.RealClaudeSkip`; `cmd/drydock-contract` sets it). Before, `go test ./...` sent real prompts whenever claude was
  installed and signed in, on the owner's machine as on this one.
- **Billing**: a headless run uses `ANTHROPIC_API_KEY` over a subscription login, and Drydock passes `ANTHROPIC_*` to agents;
  the Connection's warning now says that every agent turn is then billed per token to that key's account.

Spend that stays by design: a room reply to each human message in a task room (the assignee answers), the lead's monitor
(only with a lead on the team, bounded as above), automatic scheduling (off by default), stand-ups when a person asks.

## As built: session policy and handoffs (2026-10-11, decision P39)

Built in `internal/pipeline/session.go` for the worker's turns of every task workflow (`work` node of the task pipeline, Iterate Until Verified and its pull request variant, and the implementation turns of the test-first templates). Review, spec, design, planner and room turns still start a fresh session each time.

**When the next work turn of a run starts**, Drydock reads the previous work turn's output (its session id, agent, CLI, model and the last usage report) and decides (`pipeline.DecideSession`), in this order:

| Situation | Decision | Handoff reason |
|---|---|---|
| First work turn of the run | fresh session with the task | |
| A retry of an attempt the app cut off | fresh session, handoff | `interrupted` |
| A human let the work go on after a watchdog pause | fresh session, handoff | `watchdog` |
| Another agent or CLI takes the task over | fresh session, handoff | `agent_change` |
| The router asked for a handoff and the model changed (an escalation) | fresh session, handoff | `escalation` |
| The model changed for another reason | fresh session, handoff | `model_change` |
| The CLI cannot continue a stored session (`Capabilities.SessionResume` false, or `--no-session-persistence`) | fresh session with the task and the latest feedback | |
| The previous turn reported no context occupancy | fresh session with the task and the latest feedback | |
| The CLI compacted the session's context during the previous turn (`UsageChunk.Compactions`; OpenCode reports it over ACP) | fresh session, handoff | `context_pressure` |
| Occupancy at or above 70 % of the model's window (`ContextUsed` / `ContextSize`, Claude: `modelUsage.contextWindow`) | fresh session, handoff | `context_pressure` |
| Otherwise | **the same session continues** (`--resume <session>`, with the session's running usage totals as the baseline so each turn records only its own usage) | |

A turn is never cut: the decision uses what the previous turn reported when it ended. A continued session that the CLI can no longer resume (it ends before its session starts, `rt.ErrNotStarted` or `TurnResult.Started` false) is replaced in the same node execution by a fresh session with a handoff (`resume_failed`).

**What the fresh session receives**: the handoff (s3) as quoted data before the turn's usual prompt (the task, its criteria, the latest failure, feedback or guidance), with what the previous session said it did, the diff stat and touched files, the failing checks and its open questions. Never the transcript.

**Where the decision is recorded**: the work node's output (`session`: action, trigger, reason, occupancy, threshold, previous session, models, handoff id), the turn's context manifest (the `previous session transcript` entry says continued or not and why; a `handoff:<id>` entry when one was given), an `agent.session` event per work turn, and for a handoff a `handoffs` row with `handoff.created`.

**Differences from the design above**: the handoff is synthesized by Drydock rather than requested from the agent and verified (no extra paid turn; the agent's own `handoff` tool can still write one); the switch happens at the start of the next turn rather than "when idle" inside the same turn; continuation is per run (a new run of a task starts a fresh session); a CLI that reports no occupancy (Gemini over ACP) is never continued, since Drydock's own estimate is not built. Below the threshold the session is continued even after corrections; the best-practices advice to restart a session after two corrections (`claude-best-practices.md` s2) is not applied.

## As built: OpenCode's compaction, sub-agents and resumed cost (2026-10-15, decision P53 a)

Read from OpenCode's own ACP code (v2, `packages/cli/src/acp`); Drydock pins OpenCode 1.18.35, and everything below is harmless
on 1.x (it ignores what it does not know and sends none of it).

- **Who owns compaction.** Drydock does: between work turns a session at or above 70 % occupancy is handed to a fresh session
  with Drydock's own handoff (P39). OpenCode's automatic compaction is stated explicitly in the inline configuration Drydock
  passes it (`OPENCODE_CONFIG_CONTENT`, from `transports.acp.env` in `internal/agents/manifests/cli/opencode.yaml`, and the
  same section in `mcp.OpenCodeCompaction()` for the MCP inline config): `{"compaction":{"auto":true}}`. It stays on because it
  is the only guard against a single turn that overflows the window (Drydock never cuts a turn); OpenCode compacts only when
  the window is nearly full (usable context minus a reserve), far above 70 %, so the two do not compete, and switching it off
  would fail such a turn instead. The inline configuration is merged over the global and project files, so a user's own
  `compaction.auto: false` does not apply to Drydock's sessions (OpenCode's managed configuration still wins). Only
  `auto` is written: it is the one key both 1.x and 2.x accept (2.x has `keep.tokens` and `buffer`; 1.x's `reserved` and
  `preserve_recent_tokens` are legacy aliases in 2.x and `prune` is unsupported there).
- **A compaction is seen.** OpenCode 2 reports a compaction to a client that did not advertise ACP's own (unstable)
  `clientCapabilities.session.compaction` as a `session_info_update` with `_meta["opencode/compaction"]`
  `{status: started|completed|failed, messageId, reason}`. The ACP adapter counts each completed one of the session itself
  (not a sub-agent's) into the turn's usage chunk (`UsageChunk.Compactions`), and the session policy treats it as context
  pressure: the next work turn is a fresh session with a handoff, whatever occupancy the CLI reported after compacting, since
  the session now holds OpenCode's summary instead of the transcript (`internal/agents/acp/ext.go`, `DecideSession`). Drydock
  does not advertise the standard capability: the pinned acp-go-sdk 0.13.5 cannot send it (no `session` field in its
  ClientCapabilities) and decodes a `compaction_update` as a `session_info_update` without its status. Contract
  OPENCODE-COMPACT-01.
- **Sub-agents.** OpenCode's task tool runs a child session. The OpenCode manifest opts in with
  `clientCapabilities._meta["opencode/child-session-updates"]: true` (`transports.acp.client_meta`; no other manifest declares
  anything), and OpenCode then sends the child's updates on `opencode/session/child_update` instead of the session's own
  stream, where a child's text would have joined the parent's message. The adapter maps the child's tool calls (ids
  `<child>:<id>`, titles `<child title>: <tool>`) into the turn's tool events, which the watchdog's loop detection and the
  activity feed read, and drops its text, thoughts, plans and usage. OpenCode sends the method without the `_` ACP reserves
  for extensions and the SDK drops such a notification, so the adapter renames it on the way in (only for an adapter that
  opted in). Other unknown notifications and session updates are ignored as before. Contract OPENCODE-CHILD-01.
- **Resumed cost.** `usage_update.cost` is the stored session's cumulative cost, so the first turn of a process that loads or
  resumes a session reported every earlier turn's cost as its own. The adapter now reports the session's cumulative cost in
  `UsageChunk.Totals` and starts a resumed session from `RunSpec.UsageBaseline` (a chat keeps it in `chats.cli_usage`, a work
  turn passes the previous turn's), as the Claude parser does since CLAUDE-USAGE-03. Contract OPENCODE-USAGE-01.

Proving tests: `TestAResumedSessionRecordsOnlyItsOwnCost`, `TestACompactionIsCountedInTheTurnsUsage`,
`TestChildSessionToolCallsBecomeTheTurnsToolEvents`, `TestWithoutTheOptInChildUpdatesStayOnTheSessionStream`
(`internal/agents/acp`), `TestAnACPChatCountsEachTurnsCostOnce` (`internal/core`), `TestOccupancyThresholdTriggersHandoff`
(`internal/pipeline`), `TestOpenCodeInlineConfigStatesCompaction` (`internal/mcp`),
`TestOnlyTheOpenCodeManifestOptsInToAgentExtensions` (`internal/agents/manifests`).

## As built: library versions in the instructions (2026-10-15, decision P54)

An agent that looks up an SDK's documentation gets the latest version's, and writes code for it even when the repository pins
an older major (Stripe 18 against a lockfile on 15). Drydock now says which version the repository uses.

- **What is read** (`internal/libver`): every `package.json` (exact versions from the nearest `package-lock.json`,
  `pnpm-lock.yaml` or `yarn.lock`, including a workspace's own nested copy), `go.mod` (direct requirements, not `// indirect`),
  `.csproj`/`.fsproj`/`.vbproj` (`PackageReference`, with `Directory.Packages.props` for central versions), `requirements.txt`
  and `pyproject.toml` (pins and ranges, exact from `uv.lock` or `poetry.lock`). Up to three folder levels down; never
  `node_modules`, `vendor`, build output, tool state, `testdata` or fixtures; no symbolic link is followed; at most 24 manifests
  and 20,000 entries. Files only: no package manager runs and nothing is contacted.
- **What is reported**: only libraries on a curated list (`internal/libver/curated.go`): vendor SDKs (payments, issue trackers,
  Microsoft, the clouds, observability, AI, auth, messaging) and the frameworks whose API changes between majors, at most 40
  in all. Every other library's version stays in the files the section names. A name or version that does not look like one
  (spaces, newlines, backticks, shell text) is dropped, so a manifest cannot put text of its own into the instructions.
- **Where it goes**: a "Library versions" section of AGENTS.md (so every CLI gets it, and Claude Code in its cached system
  prompt), one line per manifest, for example
  ``- `package.json` (exact versions from `package-lock.json`): @linear/sdk 58.1.0, stripe 15.4.0``, with the instruction to
  write code for these versions and to ask Context7, Microsoft Learn or the vendor's site for them, not the latest. A
  repository that uses none of the curated libraries gets no section. Typical size: a few hundred bytes; it changes only when
  the manifests do, and holds no worktree path, so worktrees of one base share the prompt cache. The instruction writer's
  manifest records it (`libver:<files>`).
- **Recommendations**: the lead's recommendation turn (P52) is told "Libraries the repository uses: ..." and that a vendor SDK
  in use is a good reason to recommend that vendor's documentation server or skills.

Proving tests: `internal/libver` (lockfiles of npm, pnpm and yarn, workspaces, go.mod, central .NET versions, Python pins and
uv.lock, the bounds, the curated list, and `TestAManifestCannotWriteIntoTheInstructions`), and in `internal/core`
`TestAWorkersInstructionsNameTheSDKVersionsTheRepositoryPins` and `TestTheRecommendationPromptNamesTheRepositorysSDKs` (both
fail on the code before P54) and `TestARepositoryWithoutManifestsGetsNoLibrarySection`. Not verified: a real agent following
the section (no paid turn was run), Bun's `bun.lock`, Gradle, Maven, Cargo and Ruby (not read yet).

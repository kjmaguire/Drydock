# Drydock: model routing design

Scope: this document specifies the Model Router (`internal/router`), the provider registry and model catalog (`internal/providers`), and the quota ledger (`internal/budget` plus provider adapters) for Drydock v1. It covers router inputs and outputs (`RoutingDecision`), levels, provider kinds/tiers/`data_class`, what each provider actually exposes for quota per the Phase 0 appendices, the cheap-first ladder with default CEL rules and the variables they can use, escalation/de-escalation/fallback, model-agnostic OpenRouter catalog selection with the startup tool-call probe (D4), override priority, the Codex Responses-only validation (D3), the "Why this model?" text, and outcome recording. Section numbers "brief §N" refer to `docs/brief.md`; D1-D14 to `docs/decisions.md`; evidence is cited to `docs/phase0/model-providers.md` ("MP"), `docs/phase0/agent-clis.md` ("AC"), `docs/phase0/go-platform.md` ("GP") and `docs/phase0.md`. Where decisions and brief differ, decisions win. Nothing here is built: until Slice 2 assignment is a static mapping behind the `ModelRouter` interface (brief §6), and no savings are claimed until measured (brief §0).

## 1. Position and contract

The router is an independent Go package. It never calls providers, never opens the network, and is deterministic and rule-based; an LLM is consulted only for genuine ambiguity (s7.6). Workflow nodes never call model providers either: an `agent_task` node asks the Agent Runtime to run a task, the Runtime asks the router for a decision, then drives the CLI (brief §3). The app's own LLM calls (`LLMClient`, brief §8) also ask the router for an approved provider/model and fallbacks.

```go
type ModelRouter interface {
    Decide(ctx context.Context, req RouteRequest) (RoutingDecision, error)
    Next(ctx context.Context, prev RoutingDecision, cause Cause) (RoutingDecision, error) // escalate | deescalate | fallback
    Explain(d RoutingDecision) string // "Why this model?" (s11)
}

type RouteRequest struct {
    RunKind     RunKind   // agent_task | review | consult | summarize | classify | probe
    TaskID, RunID, NodeID string; Attempt int
    Role        Role      // PM, Architect, UX, Frontend, Backend, Database, DevOps, QA, Security, Reviewer
    TaskType    string    // iteration, test_fix, boilerplate, tested_refactor, exploration, docs, first_pass_review, architecture, security_review, final_review, implement, ...
    Complexity  float64   // 0..1 heuristic (s7.5)
    Risk        bool
    DiffLines   int
    Failures    FailureCounts // cheap_iterations, premium_revise, watchdog_stage1, watchdog_stage2, same_signature_repeats
    Level       Level     // project/task level (s3)
    Explicit    *Pin      // user instruction: provider/model/effort/agent/tier pin
    Policies    []Rule    // stored CEL rules in scope (workspace, project)
    Budget      BudgetState   // remaining and pct_used per scope (project/workflow/task/agent)
    ContextCeiling DataClass  // highest sensitivity in the context package: private | public_docs
    Session     *SessionState // current provider/model, context tokens, cache age (switch-cost estimate)
    Worker      *WorkerRef    // for reviews: worker model, to prefer a different model (brief §12)
}

type RoutingDecision struct {
    ID           string
    Agent        AgentRef      // adapter + instance
    Provider     ProviderID
    Model        string
    Effort       Effort        // low|medium|high|max (abstract; adapter maps, s9)
    EffortApplied bool         // false when the adapter has no effort control (recorded, not hidden)
    TeamSize     int
    Reason       string        // one line; Explain() renders the full text
    Confidence   float64       // 0..1, rule agreement; not a quality prediction
    EstTokens    TokenEstimate // input, output, cached; from manifest size + history (estimated, labeled)
    EstCostUSD   *float64      // nil when price unknown; never silently zero
    PoliciesApplied []string   // rule ids, invariants first
    EscalatedFrom, DeescalatedFrom *PriorRef
    Alternatives []Alternative // top rejected candidates with the rule or filter that rejected them
    Reservation  ReservationID // quota slot held (s6)
    DecidedBy    string        // "rules" | "explicit" | "llm"
    SwitchCostTokens int       // uncached re-read cost if this changes model mid-session (s8)
    Created      time.Time
}
```

Brief §6 lists the decision record fields (agent, provider, model, effort, team size, reason, confidence, estimated cost and tokens, policies applied, escalated/deescalated from, alternatives); the additional fields above exist for explanation, quota reservation and outcome joins.

## 2. Pipeline inside `Decide`

1. **Hard filters (invariants, code, not overridable):** data class (`ContextCeiling=private` removes `trainable` providers; tier `free` is allowed only for `RunKind=consult`); adapter capability fit (s9); provider health and enabled; TLS rule (brief §16); `max_concurrent_agents` for hosts (brief §6 item 5).
2. **Explicit pin** (user instruction) narrows the set; a pin that violates an invariant returns `BLOCKED` with the reason, never a silent substitute.
3. **Stored CEL policies** (s7) run in priority order; effects are `require_*`, `deny`, `prefer_*`, `escalate_to`, `set_effort`, `set_team_size`.
4. **Budget hard limits**: budget at 100% pauses via the budget middleware; the router additionally downshifts at 80% (s7).
5. **Heuristic ladder** (s7) ranks the remaining candidates.
6. **Quota reservation** on the top candidate; on `ErrQuotaExhausted` try the next (fallback s8).
7. Emit `routing.decided` (plus `routing.escalated`, `routing.deescalated`, `routing.fallback` when applicable); persist the decision in `routing_decisions` (brief §18).

Slice 1: `StaticRouter` returns the agent's configured provider/model and records a decision with `DecidedBy="static"`; outcome recording (s12) is on from day one.

## 3. Levels

Levels are project defaults and per-task overrides (brief §6). Team templates set defaults, not model locks (brief §1). **All numbers below are proposed starting values, unmeasured; they are tuned through the eval harness (brief §23).**

| Level | Intent | Worker tier order | Review policy | Effort default | Escalate after |
|---|---|---|---|---|---|
| Cheapest | Minimum spend | cheap, then free for CONSULT only | Cheap reviewer; premium only when a premium-only class applies | low | 2 cheap iterations without passing checks |
| Fast | Latency | cheap, fast models first | Cheap reviewer, premium on risk | low-medium | 3 |
| Balanced (default) | Brief §6 ladder | cheap for cheap-first classes, premium for premium-only classes | cheap SHIP, then one premium review (brief §6.3) | medium | 3 cheap iterations, 2 premium REVISE, or watchdog stage 2 |
| Quality | Premium implement and review; cheap for exploration, docs, test-fix | premium | premium review always | high | 2 |
| Maximum Quality | Best available | premium at top effort, team size up to 2 on risk | premium reviewer on a different model | max | 1 |
| Custom | User-defined | per stored CEL rules and pins | per rules | per rules | per rules |

## 4. Provider registry

Registered in Settings (brief §6): `name`, `base_url`, `key_ref` (credential store reference, never the key), `kind` in `aggregator | direct | host`, `data_class` in `private | trainable` (D13: `trainable` is the brief term; MP used `public`), `tier` in `premium | cheap | free`. Additional fields (**design**): `wire_apis` (`chat`, `responses`, `anthropic_messages`) with `responses_validated_at` (D3), `attestations` (for example `billing_enabled`, `lan_attested`), `max_concurrent`, `request_overrides` (the OpenRouter `provider` object), `tls` (`system | pinned_fingerprint | none_loopback | none_private`), `enabled`, `health`.

Tier note: MP proposed `premium | cheap | local`; the brief's tier is `premium | cheap | free`. A self-hosted host has no marginal token price; v1 registers it as `cheap` (user can mark `free` for zero cost accounting) and distinguishes it by `kind=host`.

Presets and defaults (evidence in MP):

| Route | kind | data_class | tier | Evidence and conditions |
|---|---|---|---|---|
| OpenRouter paid, request carries `provider:{data_collection:"deny", zdr:true}` | aggregator | private | cheap or premium by model | MP A.8: per-request filter only tightens policy, fails closed when no endpoint qualifies (404/503); account toggles are not readable via API |
| OpenRouter `:free`, `openrouter/free` | aggregator | trainable | free | MP A.10, A.6; `openrouter/free` picks a random provider so it cannot be data-class aware; 20 rpm; 50 rpd below 10 credits purchased, 1000 rpd at or above (nuance: ceiling applies from 9 credits) |
| OpenRouter paid without the filter | aggregator | trainable | cheap | default `data_collection` is `allow` |
| Anthropic API (commercial key) | direct | private | premium | MP D: Commercial Terms "may not train"; Covered Models require 30-day retention and exclude ZDR |
| OpenAI API | direct | private | premium | MP D: secondary evidence, re-verify before shipping |
| Google AI Studio, unbilled project | direct | trainable | free/cheap | MP C: unpaid terms allow product improvement and human review |
| Google AI Studio, billing-enabled project, user-attested | direct | private | cheap/premium | only with grounding, Interactions `store`, File API and explicit caching disabled; billing cannot be detected from a key |
| vLLM / SGLang / Ollama, loopback or verified TLS + auth | host | private | cheap (or free) | MP E; bare LAN Ollama has no auth (needs attestation) |
| Subscription CLI routes (Claude Code, Codex, Antigravity, Gemini CLI) | agent-native | private (unverified, see security.md Q2) | premium | not registry providers; modeled as agent-native routes whose quota comes from CLI signals (s6) |

Validation on registration ("Test connection", brief §5): API-key providers are validated with `GET /v1/models` then stored (brief §5). Gemini's OpenAI-compat endpoint returns HTTP 400 for an invalid key, not 401 (MP C), and `GET .../openai/models` with a key is unverified. Hosts: `GET /health`, `GET /v1/models`; Ollama `GET /api/version`. Local-host context pitfall: Ollama defaults to 4k context below 24 GiB VRAM and the OpenAI API cannot change it (MP E); the Connection shows `ollama ps` context and warns below 64k. `OLLAMA_NUM_PARALLEL` defaults to 1, so `max_concurrent` defaults to 1 for Ollama; vLLM/SGLang values are user-set until the GPU benchmark (MP E procedure) is run.

## 5. Model catalog and OpenRouter selection (D4)

No model is hard-coded as "the cheap model". The catalog table `model_catalog` holds per provider: `model_id`, `context`, `max_output`, `prices` (prompt, completion, cache read, cache write; per Mtok), `supported_parameters`, `is_free`, `quantization`, `fetched_at`, `source` (`listing | endpoints | user`), `probe_status`, `probe_at`.

Refresh: at setup and in the background (default every 6 hours, **design assumption**) from `GET /api/v1/models/user`, the effective catalog filtered by the user's provider preferences, privacy settings and guardrails (MP A.8). Per-model endpoint detail comes from `/models/{author}/{slug}/endpoints` (pricing, `supported_parameters`, `quantization`, `uptime_last_30m`); `GET /api/v1/endpoints/zdr` lists ZDR endpoints and pre-filters `private` candidates. Prices float with provider routing, so the router reads them at decision time and never caches them in code (MP A.12).

Selection of the cheap `private` model:
1. Candidates: paid (`pricing.prompt != "0"`), `supported_parameters` contains `tools`, context at least the project minimum (default 64k, **design assumption**; matches the Ollama guidance in MP E), at least one endpoint that satisfies `data_collection:deny` and `zdr:true`.
2. Rank by blended price per Mtok using an input:output weight (default 3:1, **design assumption**, editable), counting cache-read price when the adapter benefits from caching.
3. **Startup tool-call probe** on the top N (default 5) via `LLMClient`, `RunKind=probe`, content class `synthetic` (no user data): a fixed 2-tool schema and a deterministic instruction; run K=3 times (**design assumption**); pass requires structured `tool_calls` present, arguments valid JSON matching the schema, correct tool chosen, and no leaked tool-call text in `content` (the failure shape Agent Room's recovery handles, GP item 12). The probe uses the same `provider` object as production private traffic; a "No endpoints found matching your data policy" style error marks the model `unavailable_private`. Probe results go to `probe_results(model, ts, pass, latency_ms, failure_mode, endpoint)`; passes are valid for 7 days or until the catalog entry's endpoint set changes (**design assumption**).
4. The cheapest passing model becomes `cheap_default` for the provider; the second becomes `cheap_fallback`. The probe proves protocol-level tool calling, not agentic competence; a second step, the Connection "Test connection" short conformance run, drives the actual agent (OpenCode by default) on a scratch repo task before the model is marked ready for real work (brief §5).
5. Cohere North Mini Code is a candidate only when a paid private route or a self-hosted endpoint exists (D4); on OpenRouter it is only `cohere/north-mini-code:free`, `trainable` (MP A.11). Self-hosted notes: sampling must be sent by the app (`temperature=1.0`, `top_p=0.95`; `generation_config.json` has none), interleaved `reasoning` must be replayed (vLLM field `reasoning`, SGLang `reasoning_content`), and vLLM needs `--tool-call-parser cohere_command4` (MP B, E).
6. Free routes: catalog entries with `pricing.prompt == "0"` are `trainable`/`free` and appear only for CONSULT.

If no model passes, `cheap_default` is unset, cheap-first rules degrade to the next available `private` tier, and the Providers page says so plainly (brief §0: unimplemented or unavailable functionality is shown honestly).

## 6. Quota ledger

Table `provider_quotas` (brief §6) stores per provider and window: `{unit: requests|tokens|credits, window, limit, remaining, resets_at, source: header|poll|config|429-body, updated_at}`; `config` limits are soft (decrement locally), `header`/`poll` are authoritative (MP F recommendation). A reservation is taken before routing is finalized and settled after the call.

```go
type QuotaLedger interface {
    Reserve(ctx context.Context, p ProviderID, model string, est TokenEstimate) (Reservation, error) // ErrQuotaExhausted{RetryAt, Scope}
    Commit(ctx context.Context, r Reservation, obs Observed) error // usage + headers after the call
    Release(ctx context.Context, r Reservation)                    // call never happened
    Observe(ctx context.Context, p ProviderID, s Signal)           // poll result, 429 body, CLI rate-limit event
    Snapshot(ctx context.Context, p ProviderID) QuotaState
}
```

| Provider | Readable automatically | Configured or learned | Retry semantics |
|---|---|---|---|
| OpenRouter | Poll `GET /api/v1/key` (`limit`, `limit_remaining`, `limit_reset`, `usage`, `usage_daily/weekly/monthly`, `is_free_tier`, `free_model_daily_requests{used,limit,remaining}`); `usage.cost` and token fields on every response (always on, no opt-in); `GET /api/v1/credits` | Per-minute free usage (20 rpm, not reported by `/key`: local sliding window); upstream provider limits | `X-RateLimit-*` and `Retry-After` **only on error responses** (429, 503, 402 `openrouter_in_flight_budget`); branch on `error.metadata.limit_source`; mid-stream failures arrive as an SSE event with `finish_reason:"error"` |
| Anthropic | `anthropic-ratelimit-{requests,tokens,input-tokens,output-tokens}-{limit,remaining,reset}` on responses (reset RFC 3339; `tokens-remaining` rounded to nearest thousand) | Spend caps: 429 without `retry-after`, detect `error.details.error_code == "enforced_spend_limit_reached"` and stop retrying | `retry-after` seconds ("earlier retries will fail"); 529 `overloaded_error` |
| OpenAI | `x-ratelimit-limit/remaining/reset-{requests,tokens}` (optional; missing or odd on some models) | `insufficient_quota` is not retryable | `Retry-After` is a minimum; add jitter |
| Google Gemini | Nothing proactive found | Per-model RPM/RPD/TPM configured by the user or learned from the 429 body (`QuotaFailure.quotaId`, `RetryInfo.retryDelay`); per project, daily reset midnight Pacific | Body `retryDelay`, not a header; classify by `quotaId`, stop retrying per-day quotas even if `retryDelay` is short |
| vLLM / SGLang / Ollama | Concurrency and queue only (vLLM `/metrics`, `/load`; SGLang `/metrics`, `/server_info`; Ollama `/api/ps`, 503 when queue of 512 is full) | `max_concurrent` from benchmark | none |
| Claude Code subscription | Stream `rate_limit_event.rate_limit_info{status: allowed\|allowed_warning\|rejected, resetsAt, utilization}` (CLI >= 2.1.181); ACP bridge forwards as `_meta["_claude/rateLimit"]` (AC s1, s10) | No JSON usage command | `rejected` marks unavailable until `resetsAt`; `allowed_warning` downshifts premium use |
| Codex subscription | `codex app-server` `account/rateLimits/read` and `account/rateLimits/updated` (primary/secondary windows with `usedPercent`, `windowDurationMins`, `resetsAt`); a side-channel client in Go; over ACP only `/status` text | | |
| Antigravity | `agy -p "/usage"` prints plan quota and reset times (CHANGELOG, SNIPPET-ONLY on format) | | |
| Gemini CLI, OpenCode, Goose | None programmatic (Gemini internal `retrieveUserQuota` only, via interactive `/stats`); local accounting only | Docs list static daily limits for Gemini CLI auth methods (consumer sign-in reportedly retired 2026-06-18) | |

The `Retry-After` for Anthropic/OpenAI SDK calls is honored by the SDKs (default 2 retries, `retry-after-ms`/`Retry-After`, GP s6); `option.WithResponseInto` captures headers for `Observe`. OpenRouter usage fields read through `openai-go` against a real response are unverified (GP s6 concern). Any approach that proxies CLI traffic to read quota carries the terms risk in D1/D2, so only what each CLI prints or emits itself is read (phase0 s5).

Reservation lifecycle: `Reserve` (counts one request and estimated tokens against the tightest window), call, `Commit` with real usage, or `Release` on cancel; reservations expire after a TTL so a crash cannot leak slots; on app restart expired reservations are released from the event log.

## 7. Cheap-first ladder and CEL rules

Brief §6 ladder, restated as rules: (1) cheap-first classes (iteration, test-fix loops, boilerplate, tested refactors, exploration, docs, first-pass review) go to a `cheap`, `private`, tool-calling model; (2) premium-only: architecture, security review, ambiguous or high-risk tasks, final `SHIP` review above a diff size; (3) cheap worker iterates until checks pass and a cheap reviewer returns `SHIP`, then one premium review; (4) unavailable or quota exhausted: next provider in tier, then next tier up (`routing.fallback`), or `BLOCKED` if policy pins a tier; (5) hosts never exceed `max_concurrent_agents`.

Rules are CEL (`cel.dev/cel-go` v0.32.0; the old `github.com/google/cel-go` path fails, GP s10). A rule is `{id, scope, priority, when, then}`; `when` must type-check to `bool` against the environment below, `then` is a declarative action map (CEL does not compute actions, which keeps rules auditable). Rules are compiled once at save time with the typed environment, rejected on type errors, previewed against stored decision fixtures, and versioned (`routing_rules.version` for compare-and-swap). Evaluation is cost-limited (**unverified**: cel-go cost limits to be confirmed; only compile/eval was run in Phase 0).

Variables:

| Namespace | Variables |
|---|---|
| `task` | `type` (string), `role` (string), `complexity` (double 0..1), `ambiguity` (double 0..1), `risk` (bool), `diff_lines` (int), `files_touched` (int), `attempt` (int), `cheap_iterations` (int), `premium_revise_count` (int), `watchdog_stage1` (int), `watchdog_stage2` (int), `same_signature_repeats` (int), `checks_passing` (bool), `pinned_tier` (string, may be empty), `worker_model` (string) |
| `run` | `kind` (string), `workflow` (string), `template` (string), `iteration` (int), `max_iterations` (int) |
| `ctx` | `ceiling` (`private` or `public_docs`), `tokens` (int), `has_untrusted` (bool) |
| `budget` | `scope` (string), `pct_used` (double), `remaining_usd` (double), `hard_limit` (bool) |
| `level` | string: `cheapest`, `fast`, `balanced`, `quality`, `maximum_quality`, `custom` |
| `candidate` | `provider` (string), `model` (string), `tier`, `data_class`, `kind` (strings), `supports_tools` (bool), `responses_validated` (bool), `est_cost_usd` (double), `quota_remaining_pct` (double, -1 if unknown), `healthy` (bool), `concurrent` (int), `max_concurrent` (int), `probe_passed` (bool), `context` (int), `agent_native` (bool) |
| `time` | `now` (timestamp), `quota_resets_in_minutes` (int) |

The `ambiguity` and `complexity` signals are heuristics from rule-computed features (estimated files touched, diff lines, module count, task text features, dependency fan-out); the formula is **not defined by the brief or evidence**, starts simple and is calibrated against eval data. Premium-only `diff_lines` threshold: the brief says "above a diff size" without a number; default 400 lines is a placeholder.

Default rules (shipped as editable copies; invariants such as `ctx.ceiling == 'private' && candidate.data_class == 'trainable'` are code and not in this list):

```yaml
- id: cheap-first-classes
  when: task.type in ['iteration','test_fix','boilerplate','tested_refactor','exploration','docs','first_pass_review'] && !task.risk && level != 'maximum_quality'
  then: { prefer_tier: cheap, require_capability: [tool_calls] }
- id: premium-only
  when: task.type in ['architecture','security_review'] || task.risk || task.ambiguity >= 0.7 || (task.type == 'final_review' && task.diff_lines >= 400)
  then: { require_tier: premium }
- id: no-free-for-agent-work
  when: candidate.tier == 'free' && run.kind != 'consult'
  then: { deny: true }
- id: escalate-premium-revise
  when: task.premium_revise_count >= 2
  then: { escalate_to: premium, handoff: true }
- id: escalate-cheap-stuck
  when: task.cheap_iterations >= 3 && !task.checks_passing
  then: { escalate_to: premium, handoff: true }
- id: escalate-watchdog
  when: task.watchdog_stage2 >= 1
  then: { escalate_to: premium, handoff: true }
- id: reviewer-differs
  when: run.kind == 'review' && candidate.model == task.worker_model
  then: { penalty: 1.0 }          # prefer a different model; penalty not deny (brief §12: "when possible")
- id: budget-downshift
  when: budget.pct_used >= 0.8 && level != 'maximum_quality' && !task.risk
  then: { prefer_tier: cheap }
- id: quota-avoid-low
  when: candidate.quota_remaining_pct >= 0 && candidate.quota_remaining_pct < 0.10
  then: { penalty: 0.5 }
- id: security-high-effort
  when: task.role == 'Security' && candidate.tier == 'premium'
  then: { set_effort: high }
```

Illustrative user policies: pin all Database-role work to a chosen model (`task.role == 'Database'`), forbid a provider after hours (`time.now.getHours() >= 22 && candidate.provider == 'openrouter'`), cap premium spend (`budget.pct_used > 0.5 && candidate.tier == 'premium' && !task.risk` with `then: {deny: true}`), always require a premium reviewer on `Backend` diffs over 150 lines.

LLM tiebreak (s2 step 3, brief §6 "LLM only for genuine ambiguity"): only when two or more non-invariant rules of equal priority conflict, or `task.ambiguity` falls in a configured uncertain band; one `LLMClient` call to the cheapest `private` model with a closed set of options, cached in `llm_cache`, recorded with `DecidedBy="llm"`. Disabled at the Cheapest level.

## 8. Escalation, de-escalation, fallback

| Event | Trigger | Action | Event |
|---|---|---|---|
| Escalation | two premium `REVISE`, one watchdog stage 2, or three cheap iterations without passing checks (brief §6.3) | Handoff artifact written, fresh session on the premium decision, counters recorded | `routing.escalated` |
| De-escalation | Next task or next node after the premium resolution; or budget downshift; never mid-session | New decision starts from the cheap ladder again; the premium model is not kept "sticky" | `routing.deescalated` |
| Fallback | provider unavailable, quota exhausted, 5xx/overloaded, model unavailable | Next provider in the same tier, then next tier up; `BLOCKED` if policy pins the tier or a data-class invariant leaves no candidate | `routing.fallback` |

Cache economics decide where switches happen (MP G): each model has its own prompt cache, so a mid-session model switch costs one full uncached prompt; Anthropic caches are per model and workspace (5 minute default TTL, optional 1 hour), Gemini explicit caches are model-bound, OpenRouter sticky routing is per account, per model, per conversation (10 minute expiry; stable `session_id` should be sent), local KV caches cannot cross models. The decision therefore carries `SwitchCostTokens` and `Next` only switches at natural breaks: escalation and de-escalation start a fresh session seeded with the handoff artifact (brief §5), and an automatic downgrade inside a live session is refused unless the estimated saving exceeds the switch cost. Effort changes also invalidate Anthropic caches on most models (AC s1).

Fallback specifics: Anthropic Covered Models return `400 invalid_request_error` when retention is not enabled; the error maps to "enable retention or pick another model" and removes the model from the candidate set for this org (MP D). Per-day Gemini quota 429s mark the provider unavailable until reset instead of retrying. OpenRouter 402 `openrouter_in_flight_budget` carries `Retry-After`; other 402s mean exhausted credits and are not retried (MP A.3, A.5: negative balance gives 402 even for free models). Retry policy is middleware (brief §4); the router only supplies the next candidate.

## 9. Capability filtering and agent fit

Decisions are taken against the adapter capability matrix (brief §5), never provider names outside adapters. Verified fit (phase0 s3.3, AC s12):

| Agent | Non-native (OpenAI-compatible) providers | Rule |
|---|---|---|
| OpenCode | Yes (`@ai-sdk/openai-compatible` + `baseURL`; `@ai-sdk/openai` for `/v1/responses`) | Default cheap-tier agent (D3); driven over `opencode acp` (D11) |
| Goose | Yes (OpenAI host/base path, OpenRouter, Ollama, custom JSON providers) | Third; ACP `goose acp` (D10) |
| Codex | Only `wire_api = "responses"` | Subscription or API key by default (premium); joins cheap tier only on a Responses-validated provider (below) |
| Claude Code | No (Anthropic format only) | Premium only; driven headless per D1 |
| Gemini CLI | No (Gemini-protocol base URL only) | Premium/cheap via Gemini auth; API-key or Vertex (D2) |
| Antigravity | Unverified | Behind consent gate (D2) |

**Codex Responses-only validation (D3).** The provider-config writer for Codex refuses to write a `[model_providers.<id>]` unless `wire_api = "responses"` (the only value the schema accepts, AC s2) and the provider's `responses_validated_at` is set. Validation is the Connection test: a real `POST /v1/responses` with a minimal tool-call request against the actual endpoint, checking a streamed response and a structured tool call; failure leaves the provider ineligible for Codex (still eligible for OpenCode/Goose). Whether OpenRouter's Responses endpoint is good enough for Codex is unverified (phase0 s3.3). Codex on a validated cheap provider is tagged `agent_native=false` in candidates; Codex on its own login is `agent_native=true`, tier premium.

Effort mapping per adapter: Claude `--effort low|medium|high|xhigh|max`; Codex `-c model_reasoning_effort=...`; OpenCode `--variant` (provider-specific); Antigravity `--effort`; Gemini CLI has no effort flag (thinking via `modelConfigs`); Goose has no unified effort flag. Where unsupported `EffortApplied=false` is recorded.

## 10. Override priority

1. **Invariants** (not an override tier): data-class refusal, approvals, hard-deny, consent gates, tier pins producing `BLOCKED`.
2. **Explicit user instruction** (an @mention naming a model, a per-task pin, a slash command).
3. **Stored CEL policies**.
4. **Budget hard limits**. Brief §6 orders explicit instruction above budget hard limits; clarification: an explicit pin can choose any allowed model but cannot bypass the 100% pause (brief §7), which resumes only by human action.
5. **Router heuristics** (the ladder).

Each applied layer is listed in `PoliciesApplied`, and a lower layer that was overridden appears in `Alternatives` with the reason.

## 11. "Why this model?"

Shown in the Agent Inspector from `Explain(d)`, deterministic and generated from the record, no LLM:

```
Why this model? gpt-example via OpenRouter (cheap, private) for task #42 "Fix failing parser tests", attempt 2.
- Class: test_fix, not risky, diff 38 lines -> cheap-first-classes (priority 50)
- Data: context is private; only private providers considered (invariant). 3 trainable candidates excluded.
- Capability: OpenCode over ACP; tool calling probe passed 2026-10-08 (3/3).
- Quota: 71% of daily budget remaining; no rate limit warning.
- Budget: project at 34%, no downshift.
- Alternatives rejected: claude-premium (premium-only rule not matched), local-vllm (max_concurrent reached 2/2).
- Estimated: 14k input / 2k output tokens, about $0.004 (estimated from catalog price updated 2026-10-08; not reported).
- Next: escalates to premium after 1 more failed iteration (3 cheap iterations) with a handoff artifact.
```

Model names above are illustrative placeholders, not recommendations. Estimated and reported figures are labeled separately (brief §7).

## 12. Outcome recording

Recorded from day one (brief §6) in `routing_outcomes(decision_id, task_id, accepted bool, accepted_at, iterations, ship_count, revise_count, premium_revise_count, escalated bool, checks_passed bool, watchdog_trips, tokens_in, tokens_out, tokens_cached, cost_usd, cost_source, premium_subscription_units, wall_ms, failure_class)`. Joins `model_usage` (token-control.md s9) by `decision_id`. "Accepted" means the task reached `COMPLETED` through human acceptance (brief §12). Aggregations feed `docs/evals.md`: cost per accepted task, premium-subscription usage per accepted task (units: tokens plus CLI-reported rate-limit utilization deltas, labeled approximate), cheap-only acceptance share, premium `REVISE` rate on cheap-worker tasks, routing quality, escalation rate. The v1 router does not learn from outcomes; no learning or savings claim is made without numbers (brief §0). Dead letters keep failed or watchdog-twice tasks for evals (brief §12).

## 13. Events

`routing.decided`, `routing.escalated`, `routing.deescalated`, `routing.fallback`, `routing.blocked`, `provider.registered`, `provider.health_changed`, `provider.catalog_refreshed`, `provider.probe_completed`, `provider.quota_updated`, `budget.warn` (80%), `budget.paused` (100%). Payloads carry ids and counts, not prompts.

## 14. Tests

Unit: rule compilation and type errors; priority and override order; data-class invariant property test (no `trainable` candidate ever returned for `private` ceiling); ladder tables per level; escalation counters; fallback chain and `BLOCKED`; quota reservation/expiry; catalog selection with recorded fixture listings and a probe fake labeled as a failure-boundary mock (brief §0). Integration (skipped with reason when unavailable, P3): `GET /api/v1/key` and `/models/user` against a real key; probe against a real endpoint; Codex Responses validation against a real endpoint; Gemini 429 body classification from captured samples. Eval: cost per accepted task per level.

## Open questions / unverified

- No OpenRouter key was available: live prices, whether any paid private cheap coding model currently passes the probe, and the data-policy tag of the free North Mini Code endpoint are unverified (decisions s4 item 5). MP A.12 prices are aggregator snapshots, not recommendations.
- Whether agent-CLI traffic to OpenRouter carries `data_collection:"deny", zdr:true` (see security.md Q1): OpenCode/Goose/Codex passthrough of provider routing fields is unverified; a loopback injecting proxy is the fallback design and would also expose usage/headers to the ledger, but adds terms and complexity questions.
- Brief tier `free` versus MP tier `local`, and `trainable` versus `public`: resolved here per D13; self-hosted registered as `cheap`/`free` with `kind=host`.
- Whether a Claude, Codex or Antigravity subscription route is `private` is unestablished (security.md Q2); Gemini unbilled keys are `trainable`.
- Complexity and ambiguity heuristics, level thresholds, premium diff threshold (400), probe parameters (N=5, K=3, TTL 7 days), refresh period (6 hours), blended price weights (3:1) and minimum context (64k) are design assumptions to tune with evals.
- cel-go cost limiting, rule hot-reload behavior, and CEL on Windows are unverified (only compile/eval ran on Linux).
- Codex `exec` interrupt behavior, Gemini/Goose/OpenCode headless interrupt behavior, and real usage streams are unverified (phase0 s7 item 8); the cache-switch cost estimate assumes the per-model cache statements in MP G, where OpenAI's statement is secondary evidence only.
- GPU throughput for `max_concurrent` on vLLM/SGLang/Ollama is not measured (MP E procedure); until then Ollama defaults to 1.
- Brief §22 says Iterate Until Verified runs five iterations by default while brief §6 escalates after three cheap iterations without passing checks; here escalation is evaluated per iteration so the two compose (escalate at iteration 3, continue to 5 on premium), which should be confirmed by the owner.

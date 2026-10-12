# Drydock: Integrations (Linear, Notion, Sentry, Aikido, GitHub)

Scope: this document specifies how Drydock connects to external work-tracking, documentation and security systems (brief section 11, "Integrations"; Slice 5). Each integration is a Connection ([`connections.md`](connections.md)) with two independent channels: app sync (polling with cursors into `external_items`, run by the app, read-only) and agent access (official MCP servers through the gateway, [`mcp.md`](mcp.md)). Nothing becomes a task without a human action or a stored rule; outbound writes are approval-gated by default; inbound content is untrusted data. It covers the `Integration` interface, the `external_items` and related schemas, task-creation rules, outbound gating, per-integration auth, cursors, rate limits and MCP access, errors, and licensing (D14: Sentry's MCP server is FSL, Aikido's is AGPL or commercial, Notion's local server is retired; none is bundled; Aikido auth is per-customer client credentials). GitHub is specified in [`github.md`](github.md) and reuses the same tables. Decisions in [`decisions.md`](decisions.md) win over the brief. The "As built" sections at the end record what exists; the newest (decision P42: workspace connections assigned to projects, sync through the vendors' own APIs) wins where they differ from the design above.

Evidence tags: `[tool §5]` = [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md) section 5 (integrations), `[tool §6]` = its MCP catalog, `[go §3]` = [`phase0/go-platform.md`](phase0/go-platform.md) section 3 (keyring), `Dn` = decisions.md. Most Linear, Notion and Aikido API facts are search-snippet level because their documentation hosts were unreachable in Phase 0; they are tagged **snippet** and must be re-verified from a networked machine. **Design** marks our choices; **unverified** marks unconfirmed facts.

## 1. Interface and channels

```go
type Integration interface {
    ID() string
    Detect(ctx context.Context) (ConnectionState, error)                 // auth probe, scopes, workspace identity
    Sync(ctx context.Context, sc Scope, cur Cursor) (Page, error)       // read-only, idempotent, resumable
    Plan(ctx context.Context, op OutboundOp) (Preview, error)           // renders exactly what would be sent
    Send(ctx context.Context, op OutboundOp, key IdempotencyKey) (Receipt, error) // only after an approved outbox row
    MCP() *MCPBinding                                                    // catalog id, auth wiring, default read-only tool set
}
```

| Channel | Who uses it | Credentials | Writes |
|---|---|---|---|
| App sync (REST/GraphQL polling) | The app (project Integrations panel, Context Engine, rules) | Stored in the credential store (go-keyring; values over 2560 bytes encrypted into SQLite with the key in Credential Manager [go §3]); never in prompts, events, logs | None. Outbound ops go through the outbox (section 4). |
| Agent access (MCP via gateway) | Agents, per role allowlist | Held by the gateway; the agent never sees them | Write tools are `side_effects_tools`, off by default, approval-gated even when enabled ([`mcp.md`](mcp.md) section 3). |

Each integration appears on the Connections page with the shared status enum (`not_installed` is replaced by "not configured" for hosted services), a Test connection that performs one authenticated read, and a project mapping (which Linear team, Notion database or page tree, Sentry project, Aikido repository corresponds to which Drydock project).

## 2. Schemas

```sql
CREATE TABLE integrations(id TEXT PRIMARY KEY, kind TEXT NOT NULL, project_id TEXT, status TEXT, auth_kind TEXT, credential_ref TEXT,
  base_url TEXT, config_json TEXT, poll_interval_s INTEGER, enabled INTEGER DEFAULT 1, version INTEGER NOT NULL DEFAULT 1);
CREATE TABLE integration_mappings(integration_id TEXT, scope TEXT, remote_id TEXT, remote_name TEXT, PRIMARY KEY(integration_id, scope, remote_id));
CREATE TABLE integration_cursors(integration_id TEXT, scope TEXT, cursor_json TEXT, watermark TEXT, last_success_at INTEGER, last_error TEXT,
  PRIMARY KEY(integration_id, scope));
CREATE TABLE external_items(
  id TEXT PRIMARY KEY, integration_id TEXT NOT NULL, project_id TEXT, external_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('issue','page','pr','error_event','finding')),
  url TEXT, title TEXT, body_ref TEXT, excerpt TEXT, state TEXT, labels_json TEXT, assignee TEXT, priority TEXT, parent_external_id TEXT,
  external_created_at INTEGER, external_updated_at INTEGER, fetched_at INTEGER, content_hash TEXT NOT NULL,
  risk INTEGER DEFAULT 0, untrusted INTEGER NOT NULL DEFAULT 1 CHECK(untrusted = 1),
  task_id TEXT, imported_by TEXT,            -- 'human:<user>' or 'rule:<id>'; NULL until a task exists
  gone INTEGER DEFAULT 0, version INTEGER NOT NULL DEFAULT 1, UNIQUE(integration_id, external_id));
CREATE TABLE integration_outbox(id TEXT PRIMARY KEY, integration_id TEXT, op TEXT, target_external_id TEXT, payload_ref TEXT, payload_hash TEXT NOT NULL,
  approval_id TEXT, status TEXT CHECK(status IN ('planned','awaiting_approval','approved','sent','failed','rejected','expired')),
  idempotency_key TEXT UNIQUE NOT NULL, created_by TEXT, created_at INTEGER, sent_at INTEGER, receipt_ref TEXT, error TEXT);
CREATE TABLE integration_rules(id TEXT PRIMARY KEY, project_id TEXT, integration_id TEXT, name TEXT, when_cel TEXT NOT NULL, action TEXT NOT NULL,
  params_json TEXT, max_per_day INTEGER NOT NULL DEFAULT 5, dry_run INTEGER NOT NULL DEFAULT 1, enabled INTEGER DEFAULT 0, created_by TEXT NOT NULL);
```

The body is stored raw on disk (`body_ref`, under `<app data>/integrations/<id>/`), with a redacted excerpt in SQLite. `untrusted` is constrained to 1 so no code path can clear it. `content_hash` makes upserts idempotent and drives change events. Tasks, rooms and workflow runs carry `version` for compare-and-swap (brief section 18); `external_items.version` does the same for concurrent sync and human actions.

## 3. Rules for creating tasks

1. An external item becomes a Drydock task only by (a) a human action ("Create task" on the item) or (b) a stored rule created by a human. The creation records `imported_by` and emits `integration.task_created` with the rule id.
2. A rule is `when_cel` over item fields (kind, state, labels, priority, assignee, project mapping) plus a fixed `action` (v1: `create_task`, `link_task`, `notify`) and fixed `params` (target status, role, workflow template) chosen by the human. Item text can match a rule but can never supply parameters, roles, permissions, model choices or commands.
3. New rules start `enabled = 0`, `dry_run = 1`: they list what would be created. Enabling is a human action. `max_per_day` (default 5) bounds rule-created tasks; excess items wait in the Needs-you inbox.
4. Created tasks start in BACKLOG, never READY or later; the human moves them on. The task description holds the item title and body inside an "External source (untrusted)" block with the item URL.
5. Items with `risk` above threshold (the same injection heuristics as packs, [`expert-packs.md`](expert-packs.md) section 8) are never rule-eligible and are shown with a warning.
6. Deduplicate on `(integration_id, external_id)`; a re-opened item attaches to its existing task instead of creating another.

## 4. Outbound writes: approval gating

Every outbound write is a row in `integration_outbox`, never a direct call. Flow: `Plan` renders the exact payload and a diff-style preview; the row gets `payload_hash = sha256(canonical payload)`; an `approvals` row of kind `approval` is created and bound to that hash (brief section 12: `REQUESTED -> APPROVED -> CONSUMED`, bound to the exact action); only an APPROVED, unconsumed approval lets `Send` run, with `idempotency_key = (run_or_task_id, op, target, payload_hash)` so a retry or workflow resume sends at most once. Changing the payload changes the hash and voids the approval. Agents cannot answer approvals (brief section 16).

| Integration | Typical ops | Default | Never automatic |
|---|---|---|---|
| Linear | comment, change state, create issue, add label | approval | delete, bulk change |
| Notion | append block, comment, update property, create page | approval | delete or archive |
| Sentry | comment, resolve/ignore issue | approval | resolve or ignore in bulk |
| Aikido | ignore, unignore, snooze, unsnooze finding | approval | any ignore/snooze (it suppresses a security signal) |
| GitHub | reply, resolve thread, review, create repo | approval ([`github.md`](github.md)) | release creation, force push |

A project may opt a narrow, templated, low-risk class (for example "post a status comment on the linked Linear issue when a PR opens") into auto-approve with a standing, human-authored policy; the exact class and template are stored, audited (`integration.outbound.sent` with `auto: true`), and revocable. This is a **design** option for owner review; the brief only requires approval by default.

## 5. Per-integration design

| | Linear | Notion | Sentry | Aikido |
|---|---|---|---|---|
| API | GraphQL `https://api.linear.app/graphql` (**snippet**) | REST `api.notion.com` (**snippet**) | REST `https://sentry.io/api/0/` (read from docs repo) | REST `https://app.aikido.dev/api/public/v1`; regional hosts app.us / app.au / app.me (**snippet**) |
| App-sync auth | Personal API key, header `Authorization: <key>` (v1 default). OAuth 2.0 authorization code (24 h access tokens, rotating refresh, 30 min refresh grace) is later; `client_credentials` must be enabled on the OAuth app (30-day token). A commercial build needs its own registered OAuth app (steps unverified). | Internal integration token (`ntn_` prefix seen in the Notion MCP server README); the user creates the integration and shares pages with it. Public OAuth 2.0 integration is later (Marketplace listing needs Notion security review). | Bearer token from an internal integration or a personal token; OAuth2 authorization code for third-party apps (`/oauth/authorize/`, `/oauth/token/`; access tokens expire after 30 days, refresh supported). Self-hosted base URL configurable. | OAuth 2.0 **client credentials only**: `POST /api/oauth/token`, Basic auth with client id and secret, created by a workspace admin. Each customer creates their own credentials; no authorization-code flow documented; partner program unverified. |
| Pagination and cursor | Relay cursors (`first/after`, `pageInfo.endCursor`, default page 50) and `orderBy: updatedAt` (**snippet**); an `updatedAt` filter is unconfirmed. Cursor: `{watermark: updatedAt, after}`. Sync walks newest-first and stops at the stored watermark, then moves the watermark to `max(updatedAt) - 2 min` (overlap for clock skew). | `start_cursor` / `next_cursor` / `has_more` (**snippet**). No delta feed found: search/query newest-first by `last_edited_time` and stop at the watermark. A 10,000-result cap on queries is claimed by one source (**unverified**). Scope is limited to the pages and databases the user shared. | `Link` header with `cursor=` and `results="true"\|"false"`; cursors are returned for both directions "intended for polling", so the stored cursor is the last `next`. Issues endpoint path not verified here. | No cursor evidence. Per-repository export `GET /issues/export` with `filter_code_repo_id` or `filter_code_repo_name`, JSON or CSV; diff against stored `content_hash` per finding. |
| Rate limits | API key 2,500 requests/h per user, OAuth app 5,000/h per user, unauthenticated 600/h per IP, plus a complexity limit with unverified numbers (**snippet**) | About 3 requests/s average per integration with bursts; 429 with `Retry-After` (**snippet**) | Fixed window per caller and endpoint plus a concurrency limit; headers `X-Sentry-Rate-Limit-Limit/-Remaining/-Reset/-ConcurrentLimit/-ConcurrentRemaining`; numeric limits undocumented; "polling is likely to quickly trigger rate limiting" | 20 calls/min per workspace (50 on request), 429 with `Retry-After`; page about 12 months stale (**snippet**) |
| Poll interval (design) | 120 s | 300 s | 300 s, backoff to the rate-limit reset | 3600 s or manual |
| Agent access (MCP) | Hosted `https://mcp.linear.app/mcp`, OAuth 2.1 with dynamic client registration; token or API key as Bearer; `/mcp/readonly` for agents by default | Hosted `https://mcp.notion.com/mcp` (and `/sse` fallback), OAuth 2.0 with PKCE, interactive only. Local server retired: unsupported (D14) | Hosted `https://mcp.sentry.dev/mcp`, OAuth, or `Authorization: Sentry-Bearer <token>`. Stdio package `@sentry/mcp-server` is FSL: not bundled (D14) | No hosted MCP found. `@aikidosec/mcp@1.0.27` is a local stdio server, AGPL or commercial: user-installed template only, never bundled (D14); auth `AIKIDO_API_KEY` or browser sign-in |
| Commercial/resale terms | unverified | unverified (Notion may "modify or discontinue access" per MSA excerpt) | unverified | unverified |

Notes per integration:

- **Linear**: webhooks exist (`Linear-Signature` HMAC-SHA256 header, retries on 5xx), but a desktop app has no public endpoint and the brief forbids a cloud component, so v1 is polling only. The SDK repo `linear/linear` is MIT [tool §5].
- **Notion**: sync relies on the user explicitly sharing content with the integration, which is also the permission boundary. The MCP route needs a one-time interactive OAuth that the app cannot automate; headless access is REST with the integration token only. Page bodies arrive as blocks; they are flattened to Markdown for `external_items.excerpt` and for requirement import.
- **Sentry**: events carry user-supplied data (stack traces, breadcrumbs, request data, possibly PII and secrets). Before any text reaches a prompt it passes the gitleaks scan and a PII scrub; the data class is `private`, so it never goes to a `trainable` provider. Sentry's docs recommend webhooks over polling; unavailable here, so poll sparingly and honor the headers.
- **Aikido**: findings describe vulnerabilities in the user's code; they stay local and `private`. Token lifetime and payload field names are unverified. Credentials are the customer's own client id and secret (secret in the credential store); the OAuth token is cached until expiry and re-fetched on 401.
- **GitHub**: `kind` is `issue` or `pr`; sync and polling are in [`github.md`](github.md) sections 6 and 8.

## 6. Sync engine

A scheduler runs `Sync` per `(integration, scope)` at the integration's interval with jitter, on app focus, and on manual refresh; one in-flight sync per scope; work is resumable because the cursor is committed after each page. Per page: upsert by `(integration_id, external_id)` when `content_hash` differs, emit `integration.item.upserted`, write bodies to disk, run the injection and secret scans, and commit cursor and rows in one transaction (single writer connection). A full sweep (weekly, or after a cursor error) lists everything in scope and marks items missing upstream `gone = 1` (deletion is only detectable by a sweep). Backoff: honor `Retry-After` and Sentry rate-limit headers; otherwise exponential with jitter; `integration.rate_limited` records the wait. An invalid or expired cursor restarts from the watermark; 401 triggers token refresh (OAuth) or `integration.auth_expired` and a Connection state of `unauthenticated`, with the sync paused, not failed. Schema drift (unknown or missing fields) is counted like adapter drift and raises `integration.schema_drift` with the Connection `degraded`.

Context use: the Context Engine may include an external item only through a manifest entry marked untrusted (source, size, reason; brief section 9), by explicit task link or human attachment, never by broadcast. Requirement sync (a Notion page or Linear issue becoming a requirements document) is described in [`expert-packs.md`](expert-packs.md) section 10.

## 7. Errors and recovery (brief section 17)

| Condition | Behavior |
|---|---|
| Not configured, token revoked or expired | Connection `unauthenticated`; sync paused; outbox rows stay `awaiting_approval` or `approved` and are re-planned after re-auth (payload hash unchanged, so approvals remain valid until they expire). |
| Rate limited or throttled | Section 6; polling slows; user-initiated actions are queued, not dropped. |
| Network failure or vendor outage | Backoff; `integration.sync.failed` after three attempts; the app works offline on stored items. |
| Outbound send fails after approval | Outbox `failed` with the error; approval stays CONSUMED only if the API accepted the call; the retry uses the same idempotency key, and for vendors without idempotency support the receipt is checked first (look up the comment or issue by marker) to avoid duplicates. |
| Item deleted or archived upstream | `gone = 1`; linked tasks keep the stored copy and show a banner. |
| Mapping removed (team, database, project) | Items stay; sync for that scope stops; tasks are untouched. |
| Prompt-injection content in an item | `risk` flag, excluded from rules, wrapped as untrusted; cannot change permissions or answer approvals (brief section 16). |
| License or terms change for a vendor API | Integration feature flag off per integration; no code path assumes availability. |

## 8. Licensing and terms (D14, brief section 26)

| Item | Status | Rule |
|---|---|---|
| Sentry MCP server | FSL-1.1-Apache-2.0, source-available, competing-use restriction | Do not bundle; hosted service or user-installed. |
| Aikido MCP `@aikidosec/mcp` | `package.json` AGPL; LICENSE: commercial and AGPL | Do not bundle or embed; user-installed separate process only, after legal review. |
| Notion local MCP server | MIT, unmaintained | Unsupported; hosted remote MCP only. |
| Linear SDK, Notion SDK | MIT | May be used as libraries if chosen (list in `THIRD_PARTY_NOTICES.md`); v1 uses plain HTTP clients. |
| Third-party sync and commercial resale terms (Linear, Notion, Sentry, Aikido) | **unverified** for all four | Legal review before release; each integration ships behind a feature flag. |

## 9. Events

`integration.sync.started`, `.completed`, `.failed`; `integration.item.upserted`; `integration.task_created` (with `rule_id` or `human`); `integration.outbound.requested`, `.approved`, `.rejected`, `.sent`, `.failed`; `integration.rate_limited`; `integration.auth_expired`; `integration.schema_drift`. Payloads carry ids and hashes, not content; telemetry export never includes item text unless explicitly enabled (brief section 10).

## 10. Open questions / unverified

- Phase 0 could not reach linear.app, developers.notion.com, docs.sentry.io or apidocs.aikido.dev; every Linear, Notion and Aikido API fact above is search-snippet level, and no integration was called with credentials. Linear's `updatedAt` filter, Notion's pagination cap and delta options, Sentry's issues endpoint path, and Aikido's payload fields and token lifetime must be checked on a networked machine (phase0 section 7, item 12).
- Commercial resale and third-party sync terms for Linear, Notion, Sentry and Aikido are unverified; whether Aikido or Linear offer partner OAuth flows (so customers need not create their own credentials) is unknown. Aikido remains per-customer client credentials (D14).
- Webhooks would reduce polling (Sentry recommends them) but need a public endpoint; the brief has no cloud component, so a relay is out of scope. Owner decision needed if lower latency matters.
- Whether auto-approval classes (section 4) are acceptable is an owner call; the default is approval for every write. Interpretation to confirm: brief section 11 says outbound writes are "approval-gated by default", which this document treats as a per-project policy switch with an audited exception list.
- Contradiction noted: brief section 14 lists Linear/Notion/Sentry servers in the shipped catalog; D14 forbids bundling Sentry's and Notion's local servers and Aikido's. This document and `mcp.md` ship only hosted entries plus user-install templates.
- Rate-limit budgets, poll intervals, `max_per_day`, the overlap window and risk thresholds are design defaults awaiting measurement.

## As built (2026-10-08, Linux sandbox, Slice 5)

What exists: `internal/integrations` (Connections for Linear, Notion, Sentry and Aikido; polling sync with cursors; untrusted-content sanitizer; importers; stored rules; approval-gated outbound outbox; Test connection), `internal/integrations/itfixture` (protocol fixtures, tests only), `internal/mcp/appcall.go` (the gateway's client side for calls made by the app itself), migration `00080_integrations.sql`, and the core extension `internal/core/ext_integrations.go`, which registers `integration.list`, `.connect`, `.disconnect`, `.test`, `.sync`, `.items`, `.item`, `.rules.list`, `.rules.set`, `.import`, `.notion.page`, `.outbound.propose`, `.outbound.decide`, `.outbound.execute`, `.outbound.list` and `.linkback`. A background loop polls due connections every 15 s. Every method acts as `core.Human()`; the service refuses any other principal for connect, disconnect, rules, import and decisions.

**No live vendor was contacted.** The sandbox cannot reach Linear, Notion, Sentry or Aikido, so every tool name and result shape marked below as unverified exists only in the protocol fixtures under `internal/integrations/itfixture`, which were written from this document, not from a vendor. The tests prove Drydock's behaviour (cursors, dedupe, untrusted handling, approval gating, secrets), not the vendors' APIs. The first run against real accounts will probably need adapter changes; Test connection names the missing tools, and a result the parsers do not understand degrades the Connection with `integration.schema_drift` instead of guessing.

### Differences from the text above

- **Sync goes through MCP tools, not REST or GraphQL** (the Slice 5 brief asked for the gateway client). Consequences: there is no HTTP `ETag`/`If-Modified-Since` and no `Link`, `Retry-After` or `X-Sentry-Rate-Limit-*` headers. `external_items.etag` holds the vendor's own version marker (Linear `updatedAt`, Notion `last_edited_time`, Sentry `lastSeen`) and acts as the If-Modified analogue: a Notion page whose marker is unchanged is not fetched again. Rate limits are recognized from tool-error text (`429`, "rate limit", "Retry-After: N") by a heuristic; a vendor that words it differently is treated as an ordinary failure and gets exponential backoff instead.
- **Token entry only, no OAuth** (as first built; superseded on 2026-10-09: Linear, Notion and Sentry can also connect with an OAuth sign-in to the vendor's hosted MCP server, see "As built: OAuth sign-in" below). Docs record Linear OAuth as later and Sentry/Aikido as token or client-credential based; Notion's hosted MCP server requires an interactive OAuth with PKCE that the app cannot automate, and whether it accepts a pasted integration token is **unverified**. Aikido uses the user-installed stdio server's `AIKIDO_API_KEY` (a personal access token); the client-credentials exchange for the REST API (D14) is not built. The token goes to the Vault (`providers.SecretStore`) under `integrations/<id>/token`; the connection row holds only that reference. The gateway's `Credentials` adapter resolves both that reference and the catalog's `mcp/<server>` references to the same token, so one pasted token serves app sync and, once a gateway is wired for agent runs, agent access.
- **Tool names.** Aikido's come from the published package's tool list (`aikido_issues_list`, `aikido_ignore_issue`, `aikido_unignore_issue`, `aikido_snooze_issue`, `aikido_unsnooze_issue`); their arguments and result fields are unverified. Notion's (`notion-search`, `notion-fetch`, `notion-create-pages`, `notion-create-comment`) were observed in the tool list of the session that built this; shapes unverified. Linear (`list_issues`, `create_comment`, `update_issue`) and Sentry (`list_issues`, `get_issue_details`, `update_issue`) are unverified in name and shape; each role lists candidates (for example `search_issues` for Sentry) and Test connection reports which one the server offers.
- **Schema.** `integrations`, `integration_cursors`, `external_items`, `integration_rules`, `integration_outbox` exist with the columns in `00080_integrations.sql`, which differ from section 2: `integration_mappings` is not built (the remote scope is `config_json`: Linear `team`, Notion `query`, Sentry `organization`/`project`/`query`, Aikido `workspace`/`repo`/`argv`); `external_items` gained `issue_ref`, `meta_json` (normalized facts rules match: level, events, severity...), `etag`, `risk_flags`, `redactions`, `truncated`, `imported_at`; `integration_rule_matches` is new (dry-run listings, rate-limited waits, risky skips); the outbox stores the exact tool and arguments. Bodies are written to `<app data>/integrations/<connection>/` as the sanitized, secret-redacted copy only; the raw text is never stored.
- **Rules.** `when_cel` is CEL over one variable, `item`, holding normalized facts (`item.source`, `kind`, `state`, `title`, `labels`, `priority`, `level`, `events`, `users`, `severity`, `project`, `team`, `repo`, `type`, `risk`, `ref`); the body is not exposed. Params are limited to `priority` and `template` and are fixed by the human; item text cannot supply any parameter. Actions are `create_task` and `notify` (`link_task` is not built; `notify` only records the match and emits an event, it does not create a Needs-you item). A new rule is always created disabled and in dry-run; a disabled rule that is still in dry-run evaluates and lists what it would create (`would_create`), and tasks are created only when `enabled && !dry_run`. Updates and deletes are compare-and-swap on `version`. `max_per_day` is a rolling 24 hours counted from `external_items.imported_at`; items over the limit stay eligible and are tried again at the next evaluation. Items with `risk >= 3` are never created by a rule. A Notion rule cannot use `create_task` (a page is a requirements source).
- **Outbound.** `Propose` builds the exact call (tool, arguments, target) through the adapter, refuses text containing a secret, strips control and invisible characters, stores the row, and requests a `security.KindIntegrationWrite` approval bound to the hash of `{integration, kind, op, target, tool, args}`. `Execute` rebuilds the descriptor from the stored columns, so an altered payload, target, tool or operation, or an approval borrowed from another action, fails `Approvals.Consume` and nothing is sent. `Consume` is one-shot, so a send that fails after approval needs a new proposal and approval (the doc's "retry with the same key" is not built); an action found `executing` after a crash is marked failed and never re-sent. An identical, already-sent action (same task, operation, target, payload) cannot be proposed again. The auto-approve class of section 4 is a design option and is **not built**; the `neverAuto` flag is recorded on the operations that must never qualify (Aikido ignore/snooze, Sentry ignore). The check for an existing comment before a retry is not built. Supported operations: Linear `comment`, `set_state`; Notion `create_page`, `comment`; Sentry `resolve`, `ignore` (the Sentry MCP server offers no comment tool); Aikido `ignore`, `unignore`, `snooze`, `unsnooze`. `integration.linkback` proposes the "Drydock opened a pull request" comment on the Linear or Notion item a task came from; its text is the template plus the PR URL.
- **Events.** As in section 9, plus `integration.connected`, `.disconnected`, `.test.completed`, `.rule.changed`, `.rule.would_create`, `.rule.rate_limited`, `.rule.skipped_risky`, `.item.risk_flagged`, `.item.secret_redacted`, `.notion_page_ready` (ids, the `notion:<page id>` source ref and a content hash; the text is read with `integration.notion.page`), and `mcp.app_call` from the gateway. `integration.sync.failed` is emitted on every failed attempt with the consecutive count, not after the third.
- **Backoff.** First failure waits 30 s, doubling to 1 h, with the jitter factor injected (pinned to 1 in tests); a rate limit waits at least its `Retry-After`; an auth failure pauses the connection (`unauthenticated`) until a new token is pasted; a failure inside a shutdown is not recorded. A sweep that hits `MaxPages` (20 pages of 50) or the Notion body budget (25 per run) stores its cursor and resumes on the next tick, including after a restart.
- **Sweeps.** Aikido (no cursor evidence) lists everything on each sync and marks missing findings `gone`; the other kinds mark `gone` only on a sweep requested with `full` (the weekly automatic sweep of section 6 is **not** scheduled). A listing that proves unsorted (Notion) is walked to the end instead of stopping at the watermark.
- **Gateway wiring.** The core extension creates its own `mcp.Gateway` without a listener, only as a client (`Gateway.AppUpstream`): same pooled upstreams, credential handling, circuit breaker and stdio-under-process-manager as agent runs. When the base app later wires a gateway for agent runs it will be a second instance with its own pool; per-role allowlists, the catalog entries and `Credentials` are unchanged by this slice.
- **Task fields.** Imported tasks are BACKLOG tasks (the task service only creates BACKLOG), `IssueRef` is the Linear identifier (for example `ENG-123`), the Sentry short id, or `AIK-<id>`; priority is mapped to Drydock's integer (assumed: higher is more urgent; Linear urgent=4 ... low=1, Sentry fatal=4, error=3, warning=2, info=1, Aikido critical=4 ... low=1). The task title is `<ref>: <title>` cut to 100 characters, or a neutral placeholder when the title contains instruction-like text. The description is Drydock's own header (origin, importer, enum-constrained facts only) plus the external text inside a block quote in which every line, blank ones included, is prefixed `> `; labels, status and assignee are inside the quote. A Sentry stack trace (first 60 lines, 3000 characters, secret-scanned) is fetched only for the issue that is becoming a task and quoted separately.

### Untrusted content

Every inbound string is cleaned before it is stored: invalid UTF-8, control characters and invisible characters (zero-width, bidi, tags, BOM) removed; secrets replaced by `[REDACTED:<rule>]` by the app's gitleaks scanner (plus the exact stored tokens, registered with the scanner); literal `<untrusted` tags neutralized; length limited (body 64 K characters, excerpt 400, quote 4000, title 160). A scan failure withholds the text. A risk score is computed from instruction-like patterns (ignore-instructions, role takeover, approval claims, pipe-to-shell, tool-call and role-tag imitation, hidden comments, long base64, hidden characters, exfiltration phrases); a score of 3 or more excludes the item from rules and shows a warning. This is a heuristic, not a guarantee. Residual risk: a one-line task title or the quoted description can still carry persuasive text to an agent; the controls that do not depend on the model are that no approval, permission, rule, model choice or outbound write can be created from item text, which the tests assert.

### Not built, unverified

- No live vendor API or MCP server was called. Everything vendor-specific above is unverified. Hosted OAuth, the Notion integration-token question, Linear's `/mcp/readonly` versus read-write endpoint behaviour, rate-limit wording and the Windows `npx` resolution of the Aikido template are unverified; Windows behaviour (Credential Manager through the Vault, stdio under Job Objects) was not run, only cross-compiled and vetted.
- UI (built since): the Integrations screen (`/integrations`, `ui/app/screens/integrations`) connects a service with a token or an OAuth sign-in (section below), and per connection offers Test connection, Sync now, Disconnect, the items with their risk score and Import as a task, the outbound writes (propose, approve or decline the exact text, execute) and the rule editor (rules start disabled and in dry-run). An outbound approval also appears in Needs you, which links to the Integrations page to read the text and decide.
- Not built: webhooks, REST/GraphQL clients, `integration_mappings`, `link_task` rules, auto-approve classes, comment-marker lookup before retry, the weekly automatic sweep, attachments, Notion databases as typed rows (a database row arrives as a page if the search tool returns it), two-way status sync.

### Tests

`go test ./internal/integrations/... ` runs 35 tests against protocol fixtures: Linear, Notion and Sentry over real loopback HTTP through the gateway (the fixtures check the `Authorization` header, so the token reaching the server is asserted), Aikido as a real stdio subprocess whose key can only arrive through `AIKIDO_API_KEY`. They cover cursors and dedupe, resume after an interrupted sweep and after restart, ETag-style skipping, unsorted listings, full sweeps, rules (dry run, enable, rate limit across a 24 h window, risky items, CAS, human-only, CEL validation), imports for each kind, untrusted-content handling (an injection payload never appears outside the quote in the prompt text and changes no approval, outbox, rule or Linear state), outbound gating (unapproved, rejected, expired, tampered payload, tampered target/tool/op, borrowed approval, secrets in text, interrupted execution), failure messages and backoff, Test connection, and a scan of the SQLite file, WAL, body files, events, logs, views, errors and outbox for the token. `go test ./internal/core -run Integrations` drives the same flow through the UI methods and a restart.


## As built: OAuth sign-in (2026-10-09)

Linear, Notion and Sentry connect with a pasted token or with an OAuth sign-in to the vendor's hosted MCP server, the same
server the sync reads through. `integration.oauthBegin {kind, baseUrl?}` keeps one MCP library server per vendor (`linauth`,
`notauth`, `senauth`: the catalog URL or the self-hosted base URL, OAuth on) and starts the library's OAuth flow (authorization
code with PKCE, dynamic client registration, loopback redirect; docs/mcp.md). Once signed in, `integration.connect` with
`oauthRef: "oauth/mcplib/<id>"` stores that reference instead of a token (`auth_kind` oauth); the gateway resolves it to the
sign-in's current access token at call time, refreshing it with the stored refresh token. Signing out in the Library leaves the
connection without a credential (the connection test says to sign in again). Aikido has no hosted server and stays token-only.
Tested against the OAuth protocol fixture (`internal/mcp/oauthfake`); no live vendor sign-in has been run, so whether each
vendor's hosted server accepts this flow as-is is unverified.

Room exports (room-protocol s8) are exposed too: `room.export {kind: minutes | adr_candidates | pr_description}`, `room.exports`,
`room.exportGet`, `room.exportVerify`, `room.adrCandidates`; the room header has an Export menu with a Markdown download.

## As built: outside text and live checks (2026-10-10, decision P37)

- **The core refuses a high-risk item.** `Import` returns `ErrNotImportable` for an item whose risk score is at or over the
  threshold, whoever calls it; before, only the screen disabled the button. Rules already skipped such items.
- **GitHub issues get the same cleaning** (`Sanitizer.OutsideTask`, see [`github.md`](github.md) section 8) and the same refusal.
- **Imported work waits for a human.** A task with an issue reference (from a connection, a rule or a GitHub issue) is never
  moved from BACKLOG to READY by the scheduler (`tasks.PromoteReady`), even with automatic scheduling on, and the team lead
  is not asked about it while it is in BACKLOG. Rule 4 above ("the human moves them on") was not true with automatic
  scheduling on before this.
- **Live checks.** `TestLiveLinear`, `TestLiveNotion`, `TestLiveSentry` and `TestLiveAikido` (`internal/integrations/live_test.go`)
  run against the real services when `DRYDOCK_LIVE_<VENDOR>_TOKEN` is set: Test connection, one sync, every item parsed with
  an id, a title and a reference, no schema drift, and the token nowhere in the database, body files or events. They read
  only: nothing is imported and nothing is written back. Scope variables: `DRYDOCK_LIVE_LINEAR_TEAM`, `DRYDOCK_LIVE_SENTRY_ORG`,
  `DRYDOCK_LIVE_SENTRY_PROJECT`, `DRYDOCK_LIVE_AIKIDO_WORKSPACE`, `DRYDOCK_LIVE_NOTION_QUERY`, and
  `DRYDOCK_LIVE_<VENDOR>_BASE_URL` for a self-hosted server. `DRYDOCK_LIVE_REPORT=<file>` writes what was found (tool
  names, counts, missing tools; no item text). The check itself is tested against the Linear fixture. **Not run yet:** this
  environment has no vendor credentials, so every vendor fact above is still unverified until the owner runs them:

  ```sh
  DRYDOCK_LIVE_LINEAR_TOKEN=lin_api_... DRYDOCK_LIVE_REPORT=live.json go test ./internal/integrations -run TestLive -v
  ```


## As built: workspace connections and the vendors' own APIs (2026-10-12, decision P42)

This section supersedes the earlier "As built" sections where they differ: app sync no longer goes through the vendors' MCP
servers, and a connection no longer carries a project.

### Connections and assignments

- **A connection is the workspace's link to one vendor**: the credential (in the OS credential store under
  `integrations/<id>/token`, never in a row, event, log or error), the API host, and what the vendor's API says the credential
  reaches (`account_json`: account, workspace, workspace URL, scopes, how it was made; `checked_at`). It is named after the
  vendor and cannot be renamed. There is one connection per vendor; `integration.connect` with an `id` reconnects (a new
  credential for the same connection, its assignments and items untouched).
- **An assignment** (`integration_assignments`) gives a connection to one project with that project's scope:
  Linear `team`, `project`; Notion `data_source`, `query`; Sentry `organization` (required), `project`, `query`; Aikido `repo`;
  GitHub `repo` (default: the project's origin remote), `labels`. Each assignment has its own cursor (`integration_cursors.scope`
  is the assignment id), its own items (`external_items` is unique per connection, assignment and external id, so one team
  assigned to two projects gives each project its own copy) and the rules of its project. Changing an assignment's scope starts
  its cursor again and drops the old scope's items that never became tasks. Unassigning removes the project's cursor, rules and
  untasked items; items with tasks stay, marked gone. Disconnecting removes the credential and every assignment.
- **Migration 00114** (tested from the old shape in `migrate_test.go`): every connection that had a project gets the assignment
  `asg_<connection id>` with the same config, its cursor moves to that scope, its items get the assignment; credential
  references, tasks and rules are untouched; Aikido's `argv` and every MCP endpoint override are dropped (a self-hosted Sentry
  URL is kept); every connection is `untested` again. A connection without a project keeps its items and config; its first
  assignment adopts them. Outbound proposals that named an MCP tool and were not sent are cancelled with their approvals (the
  approval was bound to the MCP call); sent and failed ones stay as history. The down migration restores one project per
  connection (the first assignment's).
- **Methods**: `integration.kinds` (the catalog the screen is built from: fixed name, auth methods and fields, assignment
  settings, API, version, rate limit, documentation), `integration.list`, `.get`, `.connect`, `.disconnect`, `.test`,
  `.assign`, `.unassign`, `.assignments`, `.choices` (the values of a setting, read from the vendor: Linear teams and projects,
  Notion data sources, Sentry organizations and projects, Aikido repositories, GitHub repositories), `.oauthStart`,
  `.oauthStatus`, `.sync` (a connection, or one `assignmentId`), `.items` (by connection, assignment or project), and the item,
  rule, import and outbound methods as before. `integration.oauthBegin` (the MCP library sign-in) is kept for connections made
  with it.

### The vendors' APIs

No vendor publishes an official Go SDK, so each adapter is a plain HTTPS client written from the vendor's documentation and SDK
sources. One `apiClient` per connection sends the Authorization header, `Accept: application/json` and a Drydock user agent,
reads at most 16 MB, classifies HTTP status codes in plain language (a vendor's error code is shown only when it is letters and
underscores, never free text that could echo a credential), never follows a redirect to another host, and accepts only https
API hosts (plain http only on a loopback address, for fixtures). Before each request a token bucket spends the documented
budget evenly; after each response the vendor's rate-limit headers can hold further requests until their reset; a wait longer
than 30 s fails as rate limited and the sync's backoff takes over (at least the vendor's `Retry-After`).

| | Linear | Notion | Sentry | Aikido | GitHub |
|---|---|---|---|---|---|
| API and version | GraphQL `https://api.linear.app/graphql` (unversioned; schema of `@linear/sdk` 97.1.0) | REST `https://api.notion.com/v1`, `Notion-Version: 2026-03-11` | Web API v0, `https://sentry.io/api/0/` or a self-hosted host | Public REST API v1, `<region>/api/public/v1`; regions `app.aikido.dev`, `app.us.`, `app.me.`, `app.au.` | REST API through `gh api` |
| Official SDK | TypeScript only (`@linear/sdk`, MIT) | JavaScript only (`@notionhq/client` 5.27.0, MIT) | none for the API (`sentry-go` sends events) | none | none needed: `gh` |
| Auth | personal API key as `Authorization: <key>`; OAuth 2 authorization code with PKCE (no client secret), rotating refresh token, `Bearer` | internal integration secret as `Bearer` (public OAuth needs a client secret: not offered) | user, organization or internal-integration auth token as `Bearer`; OAuth device flow (RFC 8628) | OAuth 2 client credentials only: `POST <host>/api/oauth/token`, Basic client id and secret; the access token is kept in memory and bought again on a 401 | the user's `gh` sign-in |
| Identity (Test) | `viewer { name displayName }`, `organization { name urlKey }` | `GET /v1/users/me` (bot name, `workspace_name`) | `GET /api/0/` (`user`, `auth.scopes`), `GET /api/0/organizations/` | `GET /workspace`; scopes from the token answer | `GET /user` |
| Sync | `issues(first, after, filter: {team.key, project.id, updatedAt > watermark}, orderBy: updatedAt)`; Relay `pageInfo` | `POST /v1/data_sources/{id}/query` (sort and filter on `last_edited_time`) or `POST /v1/search` (pages, newest first); `GET /v1/pages/{id}/markdown` for the body | `GET /organizations/{org}/issues/?project=&query=&sort=date&limit=` (at most 100), `Link` header cursors (`rel="next"; results="true"`) | `GET /issues/export?format=json&filter_status=open[&filter_code_repo_id]`: everything at once, a full sweep each time | `GET /repos/{o}/{r}/issues?state=open&sort=updated&direction=desc&since=&labels=&per_page=&page=`, pull requests skipped |
| Writes (approval-gated) | `commentCreate`; `issueUpdate(stateId)` after reading the team's states | `POST /v1/comments`; `POST /v1/pages` (Markdown content under a parent page) | `PUT /organizations/{org}/issues/{id}/` status resolved or ignored | `PUT /issues/{id}/ignore`, `/unignore`, `/snooze` (`snooze_until`), `/unsnooze` | none here |
| Rate limit in the code | 2,500 requests/h per user with an API key, 5,000/h with OAuth (burst 50); `X-RateLimit-Requests-*` and `X-RateLimit-Complexity-*` remaining 0 holds until the reset (epoch ms); a `ratelimited` GraphQL error is a rate limit | 3 requests/s average (burst 3); 429 `rate_limited` with `Retry-After` | no published numbers: `X-Sentry-Rate-Limit-Remaining` 0 holds until `X-Sentry-Rate-Limit-Reset` (epoch s); 429 `Retry-After` | 20 requests/min per workspace (burst 5); 429 `Retry-After` | 5,000 requests/h for a signed-in user; `gh` errors map to a rate-limit backoff |
| Item reference | `ENG-123` | none (a requirements source) | the short id | `AIK-<id>` | `#<number>` (so the pull request says "Closes #n") |

OAuth sign-in (`oauthflow.go`, `golang.org/x/oauth2`): `integration.oauthStart` returns the page to open (PKCE: a loopback
redirect `http://127.0.0.1:<port>/callback` checks the state and exchanges the code with the verifier; device: the code to type
and the verification page), `integration.oauthStatus` says when it finished (never the token), and `integration.connect` with
`oauthSession` stores the token as JSON in the credential store. Tokens are refreshed when they expire and stored again. A kind
signs in only when a build sets its public client id (`-ldflags "-X .../core.LinearOAuthClientID=..."`,
`SentryOAuthClientID`, or `DRYDOCK_LINEAR_OAUTH_CLIENT_ID` / `DRYDOCK_SENTRY_OAUTH_CLIENT_ID`); no OAuth app is registered yet,
so in the shipped build both are off and the screen says so. The redirect URI a registered Linear app must allow is the
loopback callback.

Credentials for agents: `integrations.Credentials` (the MCP gateway's credential source for `mcp/<kind>`) still resolves a pasted
token or an OAuth access token; Aikido's REST client credentials are never handed to its MCP server. Wiring a gateway for agent
runs is the MCP screen's work.

### GitHub

The GitHub card shows the `gh` sign-in (the same check as the project wizard: `github.status`) and connects with nothing typed
(`auth_kind` `gh`, no credential reference). Test reads the login. An assignment names the repository whose issues the project
imports (default: the project's origin remote; any repository `gh` can see can be listed with `integration.choices`). Issues
sync into `external_items` like any other kind (cleaned, scored, quoted; a high-risk issue is never imported) and an import
creates a BACKLOG task with the reference `#<number>`. The Pull Requests screen's direct issue import (`github.issues`,
`github.importIssue`) is unchanged.

### Failures: the connection and its assignments

One connection serves several projects, so a failure is recorded where it belongs:

- **The credential is refused** (HTTP 401, Linear "authentication error", no stored credential, a refresh the vendor did not
  accept): the connection becomes `unauthenticated` and the scheduler leaves it alone until it is reconnected; this stops
  every project, because every project uses that credential.
- **The credential works but may not read one project's scope** (HTTP 403, Linear "forbidden", GitHub's 403 that is not a
  rate limit, Notion's `restricted_resource`): only that assignment fails. Its sync state is `forbidden` with a message about
  the missing permission or sharing, it backs off and is retried like any failure, and the connection and its other projects
  keep syncing. A 403 in Test's one-item read marks the connection `degraded`, not `unauthenticated`.
- **The connection's status** (`integrations.status`, shown on the card) only says what is wrong with the connection itself:
  `unauthenticated` (the credential), `unreachable` (network, server errors, time-outs) or `degraded` (the API answered in a
  shape Drydock does not know). Every other failure (forbidden, not found, a rate limit, anything else) is recorded on that
  assignment's cursor only, and the Integrations page shows it on that project's row (`sync.state`, `sync.lastError`); the
  card's sync summary is the worst of its assignments.
- **A success clears only what it proves.** A complete sync of one assignment proves the credential is accepted and the vendor
  answers, so it turns `unauthenticated`, `unreachable` or `untested` back to `ok`. It turns `degraded` back to `ok` only when
  no other assignment still records schema drift. It never clears another assignment's failure: that stays on its row until
  that assignment syncs.

### Documentation read for this change, and what could not be read

The sandbox's proxy refused `linear.app`, `developers.notion.com`, `docs.sentry.io`, `apidocs.aikido.dev`, `apis.io` and the
vendors' API hosts except `sentry.io` (which answered `GET /api/0/` with `{"version":"0","auth":null,"user":null}` and the
`X-Sentry-Rate-Limit-*` headers). Read in full instead:

- Linear: `linear/linear` (the official SDK repository): `packages/sdk/src/schema.graphql` (Query.issues and its arguments,
  IssueFilter, TeamFilter, Organization, User, Team.states, CommentCreateInput, IssueUpdateInput) and `src/error.ts` (the
  error types). Linear's rate-limit and OAuth pages were read only as search-engine snippets (2,500/h API key, 5,000/h OAuth,
  complexity headers; PKCE with the client id only, rotating refresh tokens).
- Notion: `makenotion/notion-sdk-js` 5.27.0 (the official SDK): `src/Client.ts` (default `Notion-Version` 2025-09-03, retry
  on 429 with `Retry-After`), `src/api-endpoints/*` (search, data sources query, pages and `pages/{id}/markdown`, comments,
  users/me, the bot object). The 2026-03-11 upgrade guide and the request-limit page were read as snippets.
- Sentry: `getsentry/sentry-docs` `docs/api/` (auth.mdx with OAuth and the device flow, pagination.mdx, ratelimits.mdx,
  permissions.mdx, requests.mdx) and `getsentry/sentry-api-schema` `openapi-derefed.json` (the issues list, update and latest
  event endpoints and their fields).
- Aikido: no page could be read. Paths and the client-credentials exchange come from search snippets of `apidocs.aikido.dev`
  (export issues, list code repositories, ignore and snooze), the regional hosts from the official `@aikidosec/mcp` 1.0.28
  package, and the export's field names from a community Go client (`hazcod/aikido-sdk-go`). The 20 requests/min limit is the
  Phase 0 snippet.
- GitHub: the existing `internal/git/host` and GitHub's REST documentation as already recorded in `github.md`.

### Tests

Protocol fixtures (`internal/integrations/itfixture`, labelled as such) follow the documented shapes: a Linear GraphQL server, a
Notion REST server that refuses a request without `Notion-Version`, a Sentry server with `Link` cursors and rate-limit headers,
an Aikido server with the client-credentials token endpoint, a GitHub REST server, and an OAuth server for PKCE, refresh and the
device flow. `go test ./internal/integrations/...` covers connecting without a project, assigning to two projects, unassigning,
the migration from the old shape (and back), each adapter's identity, choices, sync, writes and failures, the documented rate
limits and headers, both sign-ins, and the credential never reaching the database, files, events, logs or errors.
`go test ./internal/core -run 'Integration|Workspace'` drives the same through the UI methods, GitHub included. The UI has browser
tests for the screen and `ui/e2e/integrations.spec.ts` connects Linear (the fixture) without a project, checks its fixed name and
logo, assigns it, syncs and unassigns it.

### Not verified

No vendor was called with a real credential. Until the owner runs the live checks, every shape above is only as right as the
documentation and SDK sources it was written from; Aikido's least of all. The live checks are read-only and print nothing
secret:

```sh
DRYDOCK_LIVE_LINEAR_TOKEN=lin_api_... DRYDOCK_LIVE_LINEAR_TEAM=ENG go test ./internal/integrations -run TestLiveLinear -v
DRYDOCK_LIVE_NOTION_TOKEN=ntn_... [DRYDOCK_LIVE_NOTION_DATA_SOURCE=...] go test ./internal/integrations -run TestLiveNotion -v
DRYDOCK_LIVE_SENTRY_TOKEN=... DRYDOCK_LIVE_SENTRY_ORG=acme [DRYDOCK_LIVE_SENTRY_PROJECT=web] go test ./internal/integrations -run TestLiveSentry -v
DRYDOCK_LIVE_AIKIDO_CLIENT_ID=... DRYDOCK_LIVE_AIKIDO_CLIENT_SECRET=... [DRYDOCK_LIVE_AIKIDO_REGION=us] go test ./internal/integrations -run TestLiveAikido -v
DRYDOCK_LIVE_GITHUB_REPO=owner/name go test ./internal/integrations -run TestLiveGitHub -v   # uses your signed-in gh
```

Also unverified: the OAuth sign-ins against Linear's and Sentry's real servers (no client id is registered), whether Linear
accepts a loopback redirect with any port, whether a pre-P42 MCP sign-in's token works against the vendor's API (Test says if
not), Notion's 2026-03-11 behaviour beyond the SDK's types, and every Aikido path and field.

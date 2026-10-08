# Drydock: Integrations (Linear, Notion, Sentry, Aikido, GitHub)

Scope: this document specifies how Drydock connects to external work-tracking, documentation and security systems (brief section 11, "Integrations"; Slice 5). Each integration is a Connection ([`connections.md`](connections.md)) with two independent channels: app sync (polling with cursors into `external_items`, run by the app, read-only) and agent access (official MCP servers through the gateway, [`mcp.md`](mcp.md)). Nothing becomes a task without a human action or a stored rule; outbound writes are approval-gated by default; inbound content is untrusted data. It covers the `Integration` interface, the `external_items` and related schemas, task-creation rules, outbound gating, per-integration auth, cursors, rate limits and MCP access, errors, and licensing (D14: Sentry's MCP server is FSL, Aikido's is AGPL or commercial, Notion's local server is retired; none is bundled; Aikido auth is per-customer client credentials). GitHub is specified in [`github.md`](github.md) and reuses the same tables. Decisions in [`decisions.md`](decisions.md) win over the brief.

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

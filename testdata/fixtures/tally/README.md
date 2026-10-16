# Tally

Tally is a billing platform for SaaS companies. Each organization (a tenant) keeps its customers, sells them plans, bills
them through subscriptions and invoices, takes payments through the payment gateway, and receives webhooks about what
happened. Its own staff work in the organization as members with roles. Go standard library only.

## Layout

| Package | What it does |
|---|---|
| `internal/store` | Everything Tally knows, in memory, safe for concurrent use. Every object belongs to one organization; methods that take an `orgID` only see that organization's objects. |
| `internal/money` | Currencies and amounts. Amounts are `int64` counts of the currency's minor unit (cents for USD), never floats. |
| `internal/billing` | Invoice arithmetic (`Compute`) and billing periods (`AddInterval`). No state. |
| `internal/payments` | Charges invoices through the payment `Gateway`; `Fake` is the gateway of tests and the demo server. |
| `internal/subscriptions` | Starts, changes, cancels and renews subscriptions (`RenewDue` runs every 30 seconds in the server). |
| `internal/webhooks` | Checks endpoint URLs, records events (`Publish`) and sends deliveries (`RunDue`, every 30 seconds). |
| `internal/mail` | Email goes through a `Sender`; `Outbox` keeps messages (tests, demo server). |
| `internal/jobs` | The background job runner. |
| `internal/reports` | The dashboard's reports. |
| `internal/snapshot` | Saves the store to a JSON file and loads it back, migrating files written by older versions. |
| `internal/auth` | Who a request is and what they may do. |
| `internal/api` | The JSON HTTP API, version 1. |
| `internal/apitest` | Builds a whole service with fakes (clock, gateway, outbox, webhook receivers, DNS) and two organizations, for tests. |
| `cmd/tally` | The server: `go run ./cmd/tally -addr :8080 -data tally.json` (prints a demo API key on first start). |

## API conventions

- Every request under `/v1/` sends an API key: `Authorization: Bearer tly_...`. A key acts as the member who created it, in
  that member's organization, with the member's current role (`owner`, `admin`, `billing`, `member`, `viewer`).
- Bodies are JSON. Errors are `{"error": "<code>", "message": "<for people>"}` with the matching status. Another
  organization's object is answered exactly like one that does not exist: 404 `not_found`.
- Lists are `{"items": [...], "next_cursor": "..."}`, oldest first; `?limit=` (1 to 100, default 20) and `?cursor=` page
  through them. `next_cursor` is absent on the last page.
- Times are RFC 3339 in UTC. Amounts are integers in the currency's minor unit; fields ending in `_formatted` are for people.
- Every response carries an `X-Request-Id` (the caller's, when it sent a sensible one).

## Working on Tally

- Run the tests with `go test ./...`. Use `apitest.New(t)` for anything that goes through the API; move time with
  `e.Clock`, never with sleeps.
- The admin tool and the integration test suite (both kept in other repositories) use the exported identifiers of these
  packages and the HTTP API. Keep existing exported names, signatures and API fields working; adding new ones is fine.

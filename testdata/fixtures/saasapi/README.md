# saasapi

The billing API of a small multi-tenant SaaS product. Standard library only.

- `internal/store`: accounts, API keys, customers and invoices, kept in memory.
- `internal/billing`: invoice totals. All amounts are integer cents.
- `internal/api`: the JSON HTTP API. Every request under `/api/` needs an `X-API-Key` header. A key belongs to one account,
  and the API only ever shows that account's data: another account's customer or invoice is "not found".
- `cmd/server`: runs the API on `:8080` (or `$PORT`) with demo data.

Other services and the dashboard depend on the exported functions of `internal/store`, `internal/billing` and `internal/api`:
keep their signatures.

Run the tests with `go test ./...`.

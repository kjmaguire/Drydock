| Variant | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Prompt tokens per run, median (range) | Wall time per run, median (range) | Skill files read per run, median (range) |
|---|---|---|---|---|---|---|---|---|
| 2026-10-10-skills-none | 46 | 45 | $4.3668 | $0.0970 | $0.0583 ($0.0207 to $0.4096) | 1345194 (391839 to 4151216) | 4m0s (1m38s to 18m34s) | 0.0 (0.0 to 0.0) |
| 2026-10-10-skills-all | 46 | 43 | $8.2401 | $0.1916 | $0.1221 ($0.0407 to $0.5641) | 2359046 (889117 to 7658851) | 3m51s (1m47s to 13m46s) | 0.0 (0.0 to 0.0) |
| 2026-10-10-skills-relevant | 46 | 45 | $4.3790 | $0.0973 | $0.0586 ($0.0240 to $0.3890) | 1556865 (347963 to 5072330) | 3m55s (1m21s to 7m31s) | 0.0 (0.0 to 0.0) |

Task by task (accepted of counted runs):

| Task | 2026-10-10-skills-none | 2026-10-10-skills-all | 2026-10-10-skills-relevant |
|---|---|---|---|
| tally-api-key-hashing | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-audit-log | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-coupons | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-csv-import | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-currency-minor-units | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-graceful-shutdown | 1 of 2 | 0 of 2 | 2 of 2 |
| tally-idempotent-payments | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-invite-tokens | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-invoice-list-performance | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-month-end-renewals | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-proration | 2 of 2 | 2 of 2 | 1 of 2 |
| tally-renewal-dedupe | 2 of 2 | 1 of 2 | 2 of 2 |
| tally-role-permissions | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-seat-limits | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-snapshot-v3 | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-soft-delete-customers | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-tenant-isolation | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-tiered-pricing | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-timezone-reports | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-webhook-retries | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-webhook-signatures | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-webhook-ssrf | 2 of 2 | 2 of 2 | 2 of 2 |
| tally-zero-line-invoices | 2 of 2 | 2 of 2 | 2 of 2 |

2026-10-10-skills-all against 2026-10-10-skills-none: accepted more often on 0 task(s), less often on 2, as often on 21; exact two-sided sign test p = 0.500.
2026-10-10-skills-relevant against 2026-10-10-skills-none: accepted more often on 1 task(s), less often on 1, as often on 21; exact two-sided sign test p = 1.000.

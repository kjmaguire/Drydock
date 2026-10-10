Trial 2026-10-10-tally-haiku; Drydock cba4f9eaa7b8; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 22 | 22 | $3.2452 | $0.1475 | $0.1165 ($0.0331 to $0.4310) | 4m16s (1m34s to 9m8s) |
| B1 | 23 | 23 | $2.6744 | $0.1163 | $0.0664 ($0.0222 to $0.3337) | 3m9s (59s to 6m19s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-api-key-hashing | D | 1 | 1 | $0.0513 (n=1) | 2m58s (n=1) |
| tally-api-key-hashing | B1 | 1 | 1 | $0.0661 (n=1) | 3m13s (n=1) |
| tally-audit-log | D | 1 | 1 | $0.3626 (n=1) | 6m4s (n=1) |
| tally-audit-log | B1 | 1 | 1 | $0.3337 (n=1) | 4m54s (n=1) |
| tally-coupons | D | 1 | 1 | $0.4310 (n=1) | 7m59s (n=1) |
| tally-coupons | B1 | 1 | 1 | $0.3137 (n=1) | 4m31s (n=1) |
| tally-csv-import | D | 1 | 1 | $0.1311 (n=1) | 6m6s (n=1) |
| tally-csv-import | B1 | 1 | 1 | $0.0358 (n=1) | 2m9s (n=1) |
| tally-currency-minor-units | D | 1 | 1 | $0.0520 (n=1) | 2m38s (n=1) |
| tally-currency-minor-units | B1 | 1 | 1 | $0.0264 (n=1) | 1m19s (n=1) |
| tally-graceful-shutdown | D | 1 | 1 | $0.0367 (n=1) | 2m54s (n=1) |
| tally-graceful-shutdown | B1 | 1 | 1 | $0.0287 (n=1) | 2m45s (n=1) |
| tally-idempotent-payments | D | 1 | 1 | $0.2587 (n=1) | 5m28s (n=1) |
| tally-idempotent-payments | B1 | 1 | 1 | $0.1281 (n=1) | 3m27s (n=1) |
| tally-invite-tokens | D | 1 | 1 | $0.0494 (n=1) | 3m10s (n=1) |
| tally-invite-tokens | B1 | 1 | 1 | $0.0646 (n=1) | 2m55s (n=1) |
| tally-invoice-list-performance | D | 1 | 1 | $0.2094 (n=1) | 5m22s (n=1) |
| tally-invoice-list-performance | B1 | 1 | 1 | $0.1763 (n=1) | 4m35s (n=1) |
| tally-month-end-renewals | D | 1 | 1 | $0.0549 (n=1) | 3m37s (n=1) |
| tally-month-end-renewals | B1 | 1 | 1 | $0.0318 (n=1) | 2m3s (n=1) |
| tally-proration | D | 1 | 1 | $0.2975 (n=1) | 9m8s (n=1) |
| tally-proration | B1 | 1 | 1 | $0.3018 (n=1) | 4m39s (n=1) |
| tally-renewal-dedupe | D | 1 | 1 | $0.1863 (n=1) | 5m9s (n=1) |
| tally-renewal-dedupe | B1 | 1 | 1 | $0.1100 (n=1) | 6m18s (n=1) |
| tally-role-permissions | D | 1 | 1 | $0.1609 (n=1) | 6m15s (n=1) |
| tally-role-permissions | B1 | 1 | 1 | $0.1393 (n=1) | 3m49s (n=1) |
| tally-seat-limits | B1 | 1 | 1 | $0.0678 (n=1) | 6m19s (n=1) |
| tally-snapshot-v3 | D | 1 | 1 | $0.1019 (n=1) | 4m24s (n=1) |
| tally-snapshot-v3 | B1 | 1 | 1 | $0.0664 (n=1) | 2m51s (n=1) |
| tally-soft-delete-customers | D | 1 | 1 | $0.1704 (n=1) | 4m9s (n=1) |
| tally-soft-delete-customers | B1 | 1 | 1 | $0.2624 (n=1) | 3m52s (n=1) |
| tally-tenant-isolation | D | 1 | 1 | $0.3015 (n=1) | 5m15s (n=1) |
| tally-tenant-isolation | B1 | 1 | 1 | $0.2571 (n=1) | 4m5s (n=1) |
| tally-tiered-pricing | D | 1 | 1 | $0.1568 (n=1) | 6m39s (n=1) |
| tally-tiered-pricing | B1 | 1 | 1 | $0.0555 (n=1) | 2m40s (n=1) |
| tally-timezone-reports | D | 1 | 1 | $0.0460 (n=1) | 2m44s (n=1) |
| tally-timezone-reports | B1 | 1 | 1 | $0.0263 (n=1) | 1m35s (n=1) |
| tally-webhook-retries | D | 1 | 1 | $0.0648 (n=1) | 3m51s (n=1) |
| tally-webhook-retries | B1 | 1 | 1 | $0.0971 (n=1) | 3m9s (n=1) |
| tally-webhook-signatures | D | 1 | 1 | $0.0555 (n=1) | 3m21s (n=1) |
| tally-webhook-signatures | B1 | 1 | 1 | $0.0381 (n=1) | 2m18s (n=1) |
| tally-webhook-ssrf | D | 1 | 1 | $0.0333 (n=1) | 2m20s (n=1) |
| tally-webhook-ssrf | B1 | 1 | 1 | $0.0222 (n=1) | 1m13s (n=1) |
| tally-zero-line-invoices | D | 1 | 1 | $0.0331 (n=1) | 1m34s (n=1) |
| tally-zero-line-invoices | B1 | 1 | 1 | $0.0251 (n=1) | 59s (n=1) |

D against B1, task by task (counted runs):

| Task | Class | D accepted | B1 accepted | D cost per run, median | B1 cost per run, median | D wall, median | B1 wall, median |
|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | security review | 1 of 1 | 1 of 1 | $0.0513 | $0.0661 | 2m58s | 3m13s |
| tally-audit-log | iteration | 1 of 1 | 1 of 1 | $0.3626 | $0.3337 | 6m4s | 4m54s |
| tally-coupons | iteration | 1 of 1 | 1 of 1 | $0.4310 | $0.3137 | 7m59s | 4m31s |
| tally-csv-import | iteration | 1 of 1 | 1 of 1 | $0.1311 | $0.0358 | 6m6s | 2m9s |
| tally-currency-minor-units | test-fix loop | 1 of 1 | 1 of 1 | $0.0520 | $0.0264 | 2m38s | 1m19s |
| tally-graceful-shutdown | high-risk | 1 of 1 | 1 of 1 | $0.0367 | $0.0287 | 2m54s | 2m45s |
| tally-idempotent-payments | high-risk | 1 of 1 | 1 of 1 | $0.2587 | $0.1281 | 5m28s | 3m27s |
| tally-invite-tokens | security review | 1 of 1 | 1 of 1 | $0.0494 | $0.0646 | 3m10s | 2m55s |
| tally-invoice-list-performance | tested refactor | 1 of 1 | 1 of 1 | $0.2094 | $0.1763 | 5m22s | 4m35s |
| tally-month-end-renewals | test-fix loop | 1 of 1 | 1 of 1 | $0.0549 | $0.0318 | 3m37s | 2m3s |
| tally-proration | iteration | 1 of 1 | 1 of 1 | $0.2975 | $0.3018 | 9m8s | 4m39s |
| tally-renewal-dedupe | test-fix loop | 1 of 1 | 1 of 1 | $0.1863 | $0.1100 | 5m9s | 6m18s |
| tally-role-permissions | security review | 1 of 1 | 1 of 1 | $0.1609 | $0.1393 | 6m15s | 3m49s |
| tally-snapshot-v3 | iteration | 1 of 1 | 1 of 1 | $0.1019 | $0.0664 | 4m24s | 2m51s |
| tally-soft-delete-customers | iteration | 1 of 1 | 1 of 1 | $0.1704 | $0.2624 | 4m9s | 3m52s |
| tally-tenant-isolation | security review | 1 of 1 | 1 of 1 | $0.3015 | $0.2571 | 5m15s | 4m5s |
| tally-tiered-pricing | iteration | 1 of 1 | 1 of 1 | $0.1568 | $0.0555 | 6m39s | 2m40s |
| tally-timezone-reports | iteration | 1 of 1 | 1 of 1 | $0.0460 | $0.0263 | 2m44s | 1m35s |
| tally-webhook-retries | iteration | 1 of 1 | 1 of 1 | $0.0648 | $0.0971 | 3m51s | 3m9s |
| tally-webhook-signatures | security review | 1 of 1 | 1 of 1 | $0.0555 | $0.0381 | 3m21s | 2m18s |
| tally-webhook-ssrf | security review | 1 of 1 | 1 of 1 | $0.0333 | $0.0222 | 2m20s | 1m13s |
| tally-zero-line-invoices | test-fix loop | 1 of 1 | 1 of 1 | $0.0331 | $0.0251 | 1m34s | 59s |

| Class | D accepted | B1 accepted |
|---|---|---|
| high-risk | 2 of 2 | 2 of 2 |
| iteration | 9 of 9 | 9 of 9 |
| security review | 6 of 6 | 6 of 6 |
| test-fix loop | 4 of 4 | 4 of 4 |
| tested refactor | 1 of 1 | 1 of 1 |

D was accepted more often than B1 on 0 task(s), less often on 0 and as often on 22 (22 tasks run by both, at least 1 counted run(s) per arm and task).
This is a pilot: decision P34 asks for at least 20 tasks with 3 counted runs of each arm before any claim, so no test is computed.

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | B1 | 1 | accepted | $0.0661 | 3m13s | 1 | 44 / 29714 / 1596071 / 103010 |  |  |
| tally-api-key-hashing | D | 1 | accepted | $0.0513 | 2m58s | 2 | 46 / 33582 / 1322204 / 106242 | SHIP |  |
| tally-audit-log | B1 | 1 | accepted | $0.3337 | 4m54s | 1 | 50 / 65188 / 2482524 / 147539 |  |  |
| tally-audit-log | D | 1 | accepted | $0.3626 | 6m4s | 2 | 76 / 81896 / 3461075 / 195771 | SHIP |  |
| tally-coupons | B1 | 1 | accepted | $0.3137 | 4m31s | 1 | 56 / 65150 / 2916623 / 140881 |  |  |
| tally-coupons | D | 1 | accepted | $0.4310 | 7m59s | 4 | 82 / 104729 / 3694787 / 272098 | REVISE, SHIP |  |
| tally-csv-import | B1 | 1 | accepted | $0.0358 | 2m9s | 1 | 24 / 26929 / 757386 / 73563 |  |  |
| tally-csv-import | D | 1 | accepted | $0.1311 | 6m6s | 4 | 78 / 73633 / 2413043 / 186621 | REVISE, SHIP |  |
| tally-currency-minor-units | B1 | 1 | accepted | $0.0264 | 1m19s | 1 | 26 / 14768 / 608373 / 64643 |  |  |
| tally-currency-minor-units | D | 1 | accepted | $0.0520 | 2m38s | 2 | 58 / 26181 / 1224187 / 150252 | SHIP |  |
| tally-graceful-shutdown | B1 | 1 | accepted | $0.0287 | 2m45s | 1 | 18 / 30546 / 385001 / 48118 |  |  |
| tally-graceful-shutdown | D | 1 | accepted | $0.0367 | 2m54s | 2 | 22 / 35982 / 415769 / 72987 | SHIP |  |
| tally-idempotent-payments | B1 | 1 | accepted | $0.1281 | 3m27s | 1 | 36 / 44334 / 1440240 / 100470 |  |  |
| tally-idempotent-payments | D | 1 | accepted | $0.2587 | 5m28s | 2 | 62 / 65823 / 2761639 / 154288 | SHIP |  |
| tally-invite-tokens | B1 | 1 | accepted | $0.0646 | 2m55s | 1 | 44 / 36191 / 1591379 / 86790 |  |  |
| tally-invite-tokens | D | 1 | accepted | $0.0494 | 3m10s | 2 | 56 / 32969 / 1491459 / 89866 | SHIP |  |
| tally-invoice-list-performance | B1 | 1 | accepted | $0.1763 | 4m35s | 1 | 58 / 45908 / 2592252 / 110467 |  |  |
| tally-invoice-list-performance | D | 1 | accepted | $0.2094 | 5m22s | 2 | 56 / 57544 / 2353186 / 153583 | SHIP |  |
| tally-month-end-renewals | B1 | 1 | accepted | $0.0318 | 2m3s | 1 | 26 / 26299 / 671289 / 59458 |  |  |
| tally-month-end-renewals | D | 1 | accepted | $0.0549 | 3m37s | 2 | 44 / 46425 / 1225895 / 97005 | SHIP |  |
| tally-proration | B1 | 1 | accepted | $0.3018 | 4m39s | 1 | 70 / 59868 / 3464591 / 126591 |  |  |
| tally-proration | D | 1 | accepted | $0.2975 | 9m8s | 6 | 140 / 115406 / 4611383 / 316049 | REVISE, REVISE, SHIP |  |
| tally-renewal-dedupe | B1 | 1 | accepted | $0.1100 | 6m18s | 1 | 44 / 45425 / 1706421 / 97707 |  |  |
| tally-renewal-dedupe | D | 1 | accepted | $0.1863 | 5m9s | 2 | 48 / 71782 / 1821218 / 152836 | SHIP |  |
| tally-role-permissions | B1 | 1 | accepted | $0.1393 | 3m49s | 1 | 60 / 50140 / 2508535 / 108416 |  |  |
| tally-role-permissions | D | 1 | accepted | $0.1609 | 6m15s | 4 | 78 / 80864 / 2278141 / 211655 | REVISE, SHIP |  |
| tally-seat-limits | B1 | 1 | accepted | $0.0678 | 6m19s | 1 | 48 / 31771 / 1867453 / 89118 |  |  |
| tally-snapshot-v3 | B1 | 1 | accepted | $0.0664 | 2m51s | 1 | 36 / 37899 / 1257406 / 88467 |  |  |
| tally-snapshot-v3 | D | 1 | accepted | $0.1019 | 4m24s | 2 | 54 / 58962 / 1806922 / 126705 | SHIP |  |
| tally-soft-delete-customers | B1 | 1 | accepted | $0.2624 | 3m52s | 1 | 68 / 44410 / 3144906 / 122702 |  |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.1704 | 4m9s | 2 | 50 / 55607 / 1890296 / 148295 | SHIP |  |
| tally-tenant-isolation | B1 | 1 | accepted | $0.2571 | 4m5s | 1 | 56 / 51203 / 2606652 / 135304 |  |  |
| tally-tenant-isolation | D | 1 | accepted | $0.3015 | 5m15s | 4 | 98 / 59648 / 3766595 / 291474 | REVISE, SHIP |  |
| tally-tiered-pricing | B1 | 1 | accepted | $0.0555 | 2m40s | 1 | 38 / 32596 / 1415153 / 85995 |  |  |
| tally-tiered-pricing | D | 1 | accepted | $0.1568 | 6m39s | 6 | 100 / 87220 / 2678349 / 256922 | REVISE, REVISE, SHIP |  |
| tally-timezone-reports | B1 | 1 | accepted | $0.0263 | 1m35s | 1 | 24 / 20836 / 543552 / 52403 |  |  |
| tally-timezone-reports | D | 1 | accepted | $0.0460 | 2m44s | 2 | 48 / 33962 / 1232159 / 83589 | SHIP |  |
| tally-webhook-retries | B1 | 1 | accepted | $0.0971 | 3m9s | 1 | 36 / 39747 / 1362143 / 96170 |  |  |
| tally-webhook-retries | D | 1 | accepted | $0.0648 | 3m51s | 2 | 50 / 49923 / 1655095 / 116452 | SHIP |  |
| tally-webhook-signatures | B1 | 1 | accepted | $0.0381 | 2m18s | 1 | 32 / 28436 / 879455 / 75578 |  |  |
| tally-webhook-signatures | D | 1 | accepted | $0.0555 | 3m21s | 2 | 40 / 43935 / 1179272 / 108588 | SHIP |  |
| tally-webhook-ssrf | B1 | 1 | accepted | $0.0222 | 1m13s | 1 | 18 / 16326 / 411633 / 49745 |  |  |
| tally-webhook-ssrf | D | 1 | accepted | $0.0333 | 2m20s | 2 | 36 / 22901 / 787012 / 69787 | SHIP |  |
| tally-zero-line-invoices | B1 | 1 | accepted | $0.0251 | 59s | 1 | 30 / 10877 / 798294 / 58510 |  |  |
| tally-zero-line-invoices | D | 1 | accepted | $0.0331 | 1m34s | 2 | 44 / 17247 / 952046 / 74668 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

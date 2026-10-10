Trial 2026-10-10-skills-none; Drydock 60019f9be6f2; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 46 | 45 | $4.3668 | $0.0970 | $0.0583 ($0.0207 to $0.4096) | 4m0s (1m38s to 18m34s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-api-key-hashing | D | 2 | 2 | $0.0480 ($0.0454 to $0.0506) | 3m8s (2m45s to 3m31s) |
| tally-audit-log | D | 2 | 2 | $0.2973 ($0.2658 to $0.3289) | 12m31s (6m28s to 18m34s) |
| tally-coupons | D | 2 | 2 | $0.3838 ($0.3580 to $0.4096) | 8m6s (7m38s to 8m34s) |
| tally-csv-import | D | 2 | 2 | $0.0629 ($0.0495 to $0.0763) | 4m51s (3m44s to 5m58s) |
| tally-currency-minor-units | D | 2 | 2 | $0.0237 ($0.0207 to $0.0266) | 1m46s (1m38s to 1m54s) |
| tally-graceful-shutdown | D | 2 | 1 | $0.0469 ($0.0428 to $0.0511) | 4m3s (3m48s to 4m17s) |
| tally-idempotent-payments | D | 2 | 2 | $0.0780 ($0.0741 to $0.0819) | 4m21s (3m27s to 5m16s) |
| tally-invite-tokens | D | 2 | 2 | $0.0456 ($0.0449 to $0.0463) | 3m13s (3m12s to 3m15s) |
| tally-invoice-list-performance | D | 2 | 2 | $0.0841 ($0.0564 to $0.1118) | 5m7s (4m5s to 6m9s) |
| tally-month-end-renewals | D | 2 | 2 | $0.0409 ($0.0348 to $0.0470) | 3m18s (2m57s to 3m39s) |
| tally-proration | D | 2 | 2 | $0.1426 ($0.1304 to $0.1548) | 4m54s (4m31s to 5m16s) |
| tally-renewal-dedupe | D | 2 | 2 | $0.0805 ($0.0337 to $0.1273) | 5m34s (2m37s to 8m30s) |
| tally-role-permissions | D | 2 | 2 | $0.1043 ($0.0727 to $0.1359) | 5m48s (5m16s to 6m19s) |
| tally-seat-limits | D | 2 | 2 | $0.0455 ($0.0402 to $0.0508) | 3m0s (2m50s to 3m10s) |
| tally-snapshot-v3 | D | 2 | 2 | $0.0584 ($0.0512 to $0.0656) | 3m44s (3m18s to 4m10s) |
| tally-soft-delete-customers | D | 2 | 2 | $0.1032 ($0.0770 to $0.1295) | 4m26s (4m16s to 4m37s) |
| tally-tenant-isolation | D | 2 | 2 | $0.2539 ($0.2285 to $0.2792) | 4m49s (4m37s to 5m1s) |
| tally-tiered-pricing | D | 2 | 2 | $0.0639 ($0.0601 to $0.0676) | 3m55s (3m54s to 3m55s) |
| tally-timezone-reports | D | 2 | 2 | $0.0401 ($0.0385 to $0.0417) | 3m13s (3m1s to 3m25s) |
| tally-webhook-retries | D | 2 | 2 | $0.0607 ($0.0544 to $0.0670) | 4m23s (4m7s to 4m39s) |
| tally-webhook-signatures | D | 2 | 2 | $0.0667 ($0.0651 to $0.0684) | 4m40s (4m34s to 4m46s) |
| tally-webhook-ssrf | D | 2 | 2 | $0.0231 ($0.0222 to $0.0239) | 1m42s (1m40s to 1m44s) |
| tally-zero-line-invoices | D | 2 | 2 | $0.0292 ($0.0291 to $0.0293) | 1m51s (1m43s to 1m59s) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | D | 1 | accepted | $0.0506 | 3m31s | 3 | 50 / 35121 / 1113650 / 109560 | SHIP |  |
| tally-api-key-hashing | D | 2 | accepted | $0.0454 | 2m45s | 2 | 62 / 29016 / 1373616 / 85570 | SHIP |  |
| tally-audit-log | D | 1 | accepted | $0.3289 | 18m34s | 2 | 50 / 81989 / 2260142 / 179424 | SHIP |  |
| tally-audit-log | D | 2 | accepted | $0.2658 | 6m28s | 2 | 54 / 82728 / 2180586 / 175472 | SHIP |  |
| tally-coupons | D | 1 | accepted | $0.3580 | 8m34s | 4 | 86 / 104625 / 3024828 / 234023 | REVISE, SHIP |  |
| tally-coupons | D | 2 | accepted | $0.4096 | 7m38s | 2 | 86 / 104431 / 3959037 / 192093 | SHIP |  |
| tally-csv-import | D | 1 | accepted | $0.0763 | 5m58s | 4 | 100 / 59903 / 1891857 / 137110 | REVISE, SHIP |  |
| tally-csv-import | D | 2 | accepted | $0.0495 | 3m44s | 2 | 50 / 45306 / 1018940 / 83323 | SHIP |  |
| tally-currency-minor-units | D | 1 | accepted | $0.0207 | 1m38s | 2 | 32 / 15685 / 344549 / 47258 | SHIP |  |
| tally-currency-minor-units | D | 2 | accepted | $0.0266 | 1m54s | 2 | 46 / 17718 / 653358 / 56011 | SHIP |  |
| tally-graceful-shutdown | D | 1 | accepted | $0.0428 | 3m48s | 4 | 48 / 43289 / 487900 / 81194 | REVISE, SHIP |  |
| tally-graceful-shutdown | D | 2 | rejected | $0.0511 | 4m17s | 4 | 58 / 48952 / 672584 / 99257 | REVISE, SHIP | the hidden acceptance check failed |
| tally-idempotent-payments | D | 1 | accepted | $0.0741 | 5m16s | 4 | 86 / 57497 / 2061842 / 123861 | REVISE, SHIP |  |
| tally-idempotent-payments | D | 2 | accepted | $0.0819 | 3m27s | 2 | 38 / 42043 / 1207385 / 113036 | SHIP |  |
| tally-invite-tokens | D | 1 | accepted | $0.0449 | 3m15s | 2 | 58 / 34787 / 1221903 / 76616 | SHIP |  |
| tally-invite-tokens | D | 2 | accepted | $0.0463 | 3m12s | 2 | 56 / 33872 / 1206158 / 86349 | SHIP |  |
| tally-invoice-list-performance | D | 1 | accepted | $0.1118 | 6m9s | 2 | 80 / 55723 / 2575405 / 125364 | SHIP |  |
| tally-invoice-list-performance | D | 2 | accepted | $0.0564 | 4m5s | 2 | 50 / 45689 / 1265386 / 104493 | SHIP |  |
| tally-month-end-renewals | D | 1 | accepted | $0.0348 | 2m57s | 2 | 32 / 34304 / 482772 / 64278 | SHIP |  |
| tally-month-end-renewals | D | 2 | accepted | $0.0470 | 3m39s | 2 | 44 / 46808 / 833085 / 76532 | SHIP |  |
| tally-proration | D | 1 | accepted | $0.1304 | 4m31s | 2 | 60 / 58189 / 2011076 / 124698 | SHIP |  |
| tally-proration | D | 2 | accepted | $0.1548 | 5m16s | 2 | 54 / 70879 / 1650490 / 132870 | SHIP |  |
| tally-renewal-dedupe | D | 1 | accepted | $0.0337 | 2m37s | 2 | 30 / 30477 / 497544 / 67637 | SHIP |  |
| tally-renewal-dedupe | D | 2 | accepted | $0.1273 | 8m30s | 7 | 118 / 106171 / 2502230 / 246131 | REVISE, REVISE, SHIP |  |
| tally-role-permissions | D | 1 | accepted | $0.0727 | 5m16s | 4 | 60 / 64701 / 1083452 / 147371 | REVISE, SHIP |  |
| tally-role-permissions | D | 2 | accepted | $0.1359 | 6m19s | 4 | 108 / 74743 / 2777338 / 178770 | REVISE, SHIP |  |
| tally-seat-limits | D | 1 | accepted | $0.0402 | 2m50s | 2 | 42 / 31269 / 825717 / 81461 | SHIP |  |
| tally-seat-limits | D | 2 | accepted | $0.0508 | 3m10s | 2 | 54 / 39988 / 1372174 / 85381 | SHIP |  |
| tally-snapshot-v3 | D | 1 | accepted | $0.0656 | 4m10s | 2 | 62 / 49404 / 1876031 / 110863 | SHIP |  |
| tally-snapshot-v3 | D | 2 | accepted | $0.0512 | 3m18s | 2 | 44 / 42556 / 1046254 / 97045 | SHIP |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.0770 | 4m37s | 2 | 94 / 55081 / 2701912 / 112274 | SHIP |  |
| tally-soft-delete-customers | D | 2 | accepted | $0.1295 | 4m16s | 2 | 58 / 52931 / 1874763 / 132061 | SHIP |  |
| tally-tenant-isolation | D | 1 | accepted | $0.2285 | 5m1s | 2 | 88 / 59775 / 3191559 / 175227 | SHIP |  |
| tally-tenant-isolation | D | 2 | accepted | $0.2792 | 4m37s | 2 | 64 / 55649 / 2597912 / 170581 | SHIP |  |
| tally-tiered-pricing | D | 1 | accepted | $0.0601 | 3m55s | 4 | 68 / 46157 / 1304479 / 119896 | REVISE, SHIP |  |
| tally-tiered-pricing | D | 2 | accepted | $0.0676 | 3m54s | 2 | 86 / 50250 / 2237100 / 100472 | SHIP |  |
| tally-timezone-reports | D | 1 | accepted | $0.0417 | 3m25s | 2 | 52 / 38163 / 824633 / 71607 | SHIP |  |
| tally-timezone-reports | D | 2 | accepted | $0.0385 | 3m1s | 2 | 40 / 36944 / 642946 / 68036 | SHIP |  |
| tally-webhook-retries | D | 1 | accepted | $0.0544 | 4m7s | 2 | 38 / 54002 / 769753 / 98558 | SHIP |  |
| tally-webhook-retries | D | 2 | accepted | $0.0670 | 4m39s | 4 | 66 / 59295 / 1062836 / 133665 | REVISE, SHIP |  |
| tally-webhook-signatures | D | 1 | accepted | $0.0651 | 4m34s | 4 | 82 / 51592 / 1324152 / 130283 | REVISE, SHIP |  |
| tally-webhook-signatures | D | 2 | accepted | $0.0684 | 4m46s | 4 | 80 / 55239 / 1306249 / 138431 | REVISE, SHIP |  |
| tally-webhook-ssrf | D | 1 | accepted | $0.0222 | 1m40s | 2 | 38 / 17440 / 470639 / 43907 | SHIP |  |
| tally-webhook-ssrf | D | 2 | accepted | $0.0239 | 1m44s | 2 | 30 / 20237 / 379138 / 49990 | SHIP |  |
| tally-zero-line-invoices | D | 1 | accepted | $0.0293 | 1m43s | 2 | 36 / 17624 / 561661 / 74394 | SHIP |  |
| tally-zero-line-invoices | D | 2 | accepted | $0.0291 | 1m59s | 2 | 54 / 17050 / 854704 / 60293 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

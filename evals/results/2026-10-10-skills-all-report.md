Trial 2026-10-10-skills-all; Drydock 60019f9be6f2; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 46 | 43 | $8.2401 | $0.1916 | $0.1221 ($0.0407 to $0.5641) | 3m51s (1m47s to 13m46s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-api-key-hashing | D | 2 | 2 | $0.0717 ($0.0579 to $0.0855) | 3m36s (2m47s to 4m25s) |
| tally-audit-log | D | 2 | 2 | $0.4975 ($0.4308 to $0.5641) | 7m2s (6m9s to 7m56s) |
| tally-coupons | D | 2 | 2 | $0.5212 ($0.4817 to $0.5606) | 8m24s (7m51s to 8m56s) |
| tally-csv-import | D | 2 | 2 | $0.1483 ($0.1214 to $0.1752) | 6m4s (5m6s to 7m2s) |
| tally-currency-minor-units | D | 2 | 2 | $0.0444 ($0.0434 to $0.0453) | 1m52s (1m49s to 1m56s) |
| tally-graceful-shutdown | D | 2 | 0 | $0.0766 ($0.0614 to $0.0919) | 5m33s (5m12s to 5m54s) |
| tally-idempotent-payments | D | 2 | 2 | $0.1762 ($0.1228 to $0.2296) | 3m36s (3m20s to 3m53s) |
| tally-invite-tokens | D | 2 | 2 | $0.0631 ($0.0544 to $0.0717) | 3m6s (2m56s to 3m17s) |
| tally-invoice-list-performance | D | 2 | 2 | $0.1952 ($0.1857 to $0.2046) | 5m16s (4m45s to 5m46s) |
| tally-month-end-renewals | D | 2 | 2 | $0.0594 ($0.0558 to $0.0629) | 3m14s (2m58s to 3m29s) |
| tally-proration | D | 2 | 2 | $0.2820 ($0.2700 to $0.2941) | 6m29s (5m2s to 7m56s) |
| tally-renewal-dedupe | D | 2 | 1 | $0.3255 ($0.1924 to $0.4585) | 9m14s (4m42s to 13m46s) |
| tally-role-permissions | D | 2 | 2 | $0.2983 ($0.2834 to $0.3133) | 7m30s (5m59s to 9m1s) |
| tally-seat-limits | D | 2 | 2 | $0.0937 ($0.0589 to $0.1285) | 2m46s (2m20s to 3m13s) |
| tally-snapshot-v3 | D | 2 | 2 | $0.1515 ($0.1279 to $0.1751) | 3m32s (3m28s to 3m37s) |
| tally-soft-delete-customers | D | 2 | 2 | $0.2618 ($0.2260 to $0.2977) | 5m16s (4m21s to 6m12s) |
| tally-tenant-isolation | D | 2 | 2 | $0.3812 ($0.3385 to $0.4239) | 4m57s (4m7s to 5m48s) |
| tally-tiered-pricing | D | 2 | 2 | $0.1502 ($0.1167 to $0.1836) | 3m30s (3m11s to 3m49s) |
| tally-timezone-reports | D | 2 | 2 | $0.0449 ($0.0443 to $0.0455) | 2m18s (2m16s to 2m21s) |
| tally-webhook-retries | D | 2 | 2 | $0.1028 ($0.0927 to $0.1129) | 3m26s (3m22s to 3m31s) |
| tally-webhook-signatures | D | 2 | 2 | $0.0815 ($0.0654 to $0.0975) | 3m55s (2m52s to 4m58s) |
| tally-webhook-ssrf | D | 2 | 2 | $0.0430 ($0.0407 to $0.0453) | 2m5s (2m1s to 2m9s) |
| tally-zero-line-invoices | D | 2 | 2 | $0.0504 ($0.0500 to $0.0507) | 2m1s (1m47s to 2m15s) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | D | 1 | accepted | $0.0579 | 2m47s | 2 | 46 / 28490 / 1504540 / 142979 | SHIP |  |
| tally-api-key-hashing | D | 2 | accepted | $0.0855 | 4m25s | 4 | 76 / 47814 / 2300252 / 193120 | REVISE, SHIP |  |
| tally-audit-log | D | 1 | accepted | $0.5641 | 7m56s | 2 | 94 / 94809 / 5913057 / 254831 | SHIP |  |
| tally-audit-log | D | 2 | accepted | $0.4308 | 6m9s | 2 | 72 / 80107 / 3814006 / 238625 | SHIP |  |
| tally-coupons | D | 1 | accepted | $0.5606 | 8m56s | 4 | 122 / 111409 / 5886232 / 310463 | REVISE, SHIP |  |
| tally-coupons | D | 2 | accepted | $0.4817 | 7m51s | 4 | 98 / 101059 / 4495736 / 294322 | REVISE, SHIP |  |
| tally-csv-import | D | 1 | accepted | $0.1752 | 7m2s | 6 | 82 / 77685 / 2616930 / 241101 | REVISE, REVISE, SHIP |  |
| tally-csv-import | D | 2 | accepted | $0.1214 | 5m6s | 4 | 78 / 51016 / 2364621 / 188096 | REVISE, SHIP |  |
| tally-currency-minor-units | D | 1 | accepted | $0.0453 | 1m56s | 2 | 46 / 19114 / 1224502 / 117384 | SHIP |  |
| tally-currency-minor-units | D | 2 | accepted | $0.0434 | 1m49s | 2 | 44 / 17010 / 1174480 / 115889 | SHIP |  |
| tally-graceful-shutdown | D | 1 | rejected | $0.0614 | 5m12s | 4 | 38 / 45737 / 900727 / 147598 | REVISE, SHIP | the hidden acceptance check failed |
| tally-graceful-shutdown | D | 2 | rejected | $0.0919 | 5m54s | 8 | 80 / 65513 / 1890179 / 201154 | REVISE, REVISE, REVISE, SHIP | the hidden acceptance check failed |
| tally-idempotent-payments | D | 1 | accepted | $0.2296 | 3m53s | 2 | 48 / 43143 / 2193613 / 175781 | SHIP |  |
| tally-idempotent-payments | D | 2 | accepted | $0.1228 | 3m20s | 2 | 38 / 41968 / 1386294 / 159115 | SHIP |  |
| tally-invite-tokens | D | 1 | accepted | $0.0544 | 2m56s | 2 | 44 / 31126 / 1314686 / 128566 | SHIP |  |
| tally-invite-tokens | D | 2 | accepted | $0.0717 | 3m17s | 2 | 72 / 36447 / 2543773 / 140248 | SHIP |  |
| tally-invoice-list-performance | D | 1 | accepted | $0.2046 | 5m46s | 2 | 80 / 59959 / 3236704 / 179689 | SHIP |  |
| tally-invoice-list-performance | D | 2 | accepted | $0.1857 | 4m45s | 2 | 72 / 57750 / 2736772 / 179522 | SHIP |  |
| tally-month-end-renewals | D | 1 | accepted | $0.0629 | 3m29s | 2 | 44 / 42042 / 1396207 / 139463 | SHIP |  |
| tally-month-end-renewals | D | 2 | accepted | $0.0558 | 2m58s | 2 | 40 / 34996 / 1202907 / 131517 | SHIP |  |
| tally-proration | D | 1 | accepted | $0.2700 | 5m2s | 2 | 58 / 61384 / 2571803 / 189596 | SHIP |  |
| tally-proration | D | 2 | accepted | $0.2941 | 7m56s | 4 | 114 / 102504 / 4475226 / 276161 | REVISE, SHIP |  |
| tally-renewal-dedupe | D | 1 | accepted | $0.1924 | 4m42s | 2 | 40 / 57326 / 1619571 / 172222 | SHIP |  |
| tally-renewal-dedupe | D | 2 | failed | $0.4585 | 13m46s | 10 | 192 / 162524 / 7161942 / 496717 | REVISE, REVISE, REVISE, REVISE, REVISE | Drydock asked the human (question): Iteration limit reached: a human needs to decide |
| tally-role-permissions | D | 1 | accepted | $0.2834 | 9m1s | 6 | 146 / 108279 / 5318858 / 317088 | REVISE, REVISE, SHIP |  |
| tally-role-permissions | D | 2 | accepted | $0.3133 | 5m59s | 4 | 102 / 72111 / 4362269 / 229493 | REVISE, SHIP |  |
| tally-seat-limits | D | 1 | accepted | $0.1285 | 3m13s | 2 | 52 / 38577 / 2052267 / 159613 | SHIP |  |
| tally-seat-limits | D | 2 | accepted | $0.0589 | 2m20s | 2 | 46 / 27382 / 1641351 / 143735 | SHIP |  |
| tally-snapshot-v3 | D | 1 | accepted | $0.1751 | 3m28s | 2 | 52 / 42873 / 2181931 / 166668 | SHIP |  |
| tally-snapshot-v3 | D | 2 | accepted | $0.1279 | 3m37s | 2 | 64 / 44346 / 2503778 / 157575 | SHIP |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.2260 | 4m21s | 2 | 50 / 54738 / 2141205 / 186073 | SHIP |  |
| tally-soft-delete-customers | D | 2 | accepted | $0.2977 | 6m12s | 4 | 96 / 77474 / 3699506 / 265864 | REVISE, SHIP |  |
| tally-tenant-isolation | D | 1 | accepted | $0.3385 | 4m7s | 2 | 80 / 47701 / 3873019 / 210235 | SHIP |  |
| tally-tenant-isolation | D | 2 | accepted | $0.4239 | 5m48s | 2 | 110 / 67997 / 5624858 / 242607 | SHIP |  |
| tally-tiered-pricing | D | 1 | accepted | $0.1836 | 3m49s | 2 | 72 / 45786 / 3071912 / 167541 | SHIP |  |
| tally-tiered-pricing | D | 2 | accepted | $0.1167 | 3m11s | 2 | 66 / 39497 / 2559972 / 153485 | SHIP |  |
| tally-timezone-reports | D | 1 | accepted | $0.0443 | 2m21s | 2 | 30 / 26082 / 771497 / 117590 | SHIP |  |
| tally-timezone-reports | D | 2 | accepted | $0.0455 | 2m16s | 2 | 30 / 28468 / 794779 / 116341 | SHIP |  |
| tally-webhook-retries | D | 1 | accepted | $0.0927 | 3m22s | 2 | 32 / 42382 / 1014332 / 161391 | SHIP |  |
| tally-webhook-retries | D | 2 | accepted | $0.1129 | 3m31s | 2 | 32 / 44681 / 1096629 / 161963 | SHIP |  |
| tally-webhook-signatures | D | 1 | accepted | $0.0975 | 4m58s | 4 | 82 / 58963 / 2568058 / 211761 | REVISE, SHIP |  |
| tally-webhook-signatures | D | 2 | accepted | $0.0654 | 2m52s | 2 | 58 / 36708 / 1928717 / 138999 | SHIP |  |
| tally-webhook-ssrf | D | 1 | accepted | $0.0407 | 2m9s | 2 | 32 / 20727 / 820966 / 110413 | SHIP |  |
| tally-webhook-ssrf | D | 2 | accepted | $0.0453 | 2m1s | 2 | 40 / 22455 / 1105690 / 114814 | SHIP |  |
| tally-zero-line-invoices | D | 1 | accepted | $0.0500 | 2m15s | 2 | 40 / 21116 / 1178866 / 138449 | SHIP |  |
| tally-zero-line-invoices | D | 2 | accepted | $0.0507 | 1m47s | 2 | 50 / 19461 / 1523205 / 128533 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

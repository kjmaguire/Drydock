Trial 2026-10-10-skills-relevant; Drydock 60019f9be6f2; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 46 | 45 | $4.3790 | $0.0973 | $0.0586 ($0.0240 to $0.3890) | 3m55s (1m21s to 7m31s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-api-key-hashing | D | 2 | 2 | $0.0449 ($0.0416 to $0.0481) | 3m1s (2m44s to 3m18s) |
| tally-audit-log | D | 2 | 2 | $0.2358 ($0.1257 to $0.3459) | 7m10s (7m4s to 7m17s) |
| tally-coupons | D | 2 | 2 | $0.3696 ($0.3501 to $0.3890) | 6m53s (6m52s to 6m53s) |
| tally-csv-import | D | 2 | 2 | $0.0858 ($0.0697 to $0.1020) | 6m28s (5m40s to 7m16s) |
| tally-currency-minor-units | D | 2 | 2 | $0.0257 ($0.0254 to $0.0259) | 1m42s (1m41s to 1m42s) |
| tally-graceful-shutdown | D | 2 | 2 | $0.0310 ($0.0284 to $0.0336) | 2m49s (2m38s to 3m0s) |
| tally-idempotent-payments | D | 2 | 2 | $0.1366 ($0.0807 to $0.1925) | 4m55s (4m55s to 4m55s) |
| tally-invite-tokens | D | 2 | 2 | $0.0502 ($0.0501 to $0.0502) | 3m23s (3m17s to 3m28s) |
| tally-invoice-list-performance | D | 2 | 2 | $0.0667 ($0.0652 to $0.0681) | 4m51s (4m12s to 5m31s) |
| tally-month-end-renewals | D | 2 | 2 | $0.0378 ($0.0350 to $0.0406) | 2m56s (2m47s to 3m5s) |
| tally-proration | D | 2 | 1 | $0.1279 ($0.1097 to $0.1460) | 6m10s (4m53s to 7m28s) |
| tally-renewal-dedupe | D | 2 | 2 | $0.0680 ($0.0603 to $0.0757) | 4m54s (4m27s to 5m21s) |
| tally-role-permissions | D | 2 | 2 | $0.1813 ($0.0967 to $0.2659) | 6m9s (4m47s to 7m31s) |
| tally-seat-limits | D | 2 | 2 | $0.0511 ($0.0493 to $0.0529) | 2m54s (2m52s to 2m56s) |
| tally-snapshot-v3 | D | 2 | 2 | $0.0561 ($0.0544 to $0.0579) | 3m36s (3m29s to 3m43s) |
| tally-soft-delete-customers | D | 2 | 2 | $0.0781 ($0.0555 to $0.1008) | 5m8s (3m41s to 6m36s) |
| tally-tenant-isolation | D | 2 | 2 | $0.2699 ($0.2663 to $0.2735) | 4m58s (4m45s to 5m12s) |
| tally-tiered-pricing | D | 2 | 2 | $0.0638 ($0.0593 to $0.0683) | 3m59s (3m37s to 4m22s) |
| tally-timezone-reports | D | 2 | 2 | $0.0342 ($0.0331 to $0.0354) | 2m46s (2m45s to 2m48s) |
| tally-webhook-retries | D | 2 | 2 | $0.0492 ($0.0467 to $0.0517) | 3m46s (3m25s to 4m7s) |
| tally-webhook-signatures | D | 2 | 2 | $0.0691 ($0.0664 to $0.0718) | 4m46s (4m41s to 4m52s) |
| tally-webhook-ssrf | D | 2 | 2 | $0.0314 ($0.0297 to $0.0331) | 2m23s (2m16s to 2m29s) |
| tally-zero-line-invoices | D | 2 | 2 | $0.0252 ($0.0240 to $0.0264) | 1m29s (1m21s to 1m38s) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | D | 1 | accepted | $0.0481 | 3m18s | 2 | 66 / 32949 / 1570816 / 79688 | SHIP |  |
| tally-api-key-hashing | D | 2 | accepted | $0.0416 | 2m44s | 2 | 38 / 32587 / 823677 / 85461 | SHIP |  |
| tally-audit-log | D | 1 | accepted | $0.3459 | 7m17s | 2 | 82 / 86098 / 3652393 / 189047 | SHIP |  |
| tally-audit-log | D | 2 | accepted | $0.1257 | 7m4s | 5 | 76 / 83882 / 1751535 / 226321 | REVISE, SHIP |  |
| tally-coupons | D | 1 | accepted | $0.3890 | 6m53s | 2 | 76 / 87360 / 3440805 / 187916 | SHIP |  |
| tally-coupons | D | 2 | accepted | $0.3501 | 6m52s | 2 | 88 / 88006 / 3934184 / 178506 | SHIP |  |
| tally-csv-import | D | 1 | accepted | $0.0697 | 5m40s | 4 | 68 / 62471 / 1119707 / 136260 | REVISE, SHIP |  |
| tally-csv-import | D | 2 | accepted | $0.1020 | 7m16s | 4 | 116 / 78558 / 2881420 / 169419 | REVISE, SHIP |  |
| tally-currency-minor-units | D | 1 | accepted | $0.0259 | 1m42s | 2 | 42 / 16407 / 614507 / 57967 | SHIP |  |
| tally-currency-minor-units | D | 2 | accepted | $0.0254 | 1m41s | 2 | 36 / 17772 / 498838 / 57660 | SHIP |  |
| tally-graceful-shutdown | D | 1 | accepted | $0.0336 | 3m0s | 2 | 30 / 36188 / 404491 / 57439 | SHIP |  |
| tally-graceful-shutdown | D | 2 | accepted | $0.0284 | 2m38s | 2 | 24 / 32851 / 302994 / 44945 | SHIP |  |
| tally-idempotent-payments | D | 1 | accepted | $0.1925 | 4m55s | 2 | 48 / 55624 / 1864126 / 137902 | SHIP |  |
| tally-idempotent-payments | D | 2 | accepted | $0.0807 | 4m55s | 3 | 52 / 62310 / 1448230 / 123813 | SHIP |  |
| tally-invite-tokens | D | 1 | accepted | $0.0501 | 3m28s | 2 | 72 / 36514 / 1606690 / 78827 | SHIP |  |
| tally-invite-tokens | D | 2 | accepted | $0.0502 | 3m17s | 2 | 74 / 34517 / 1678489 / 80917 | SHIP |  |
| tally-invoice-list-performance | D | 1 | accepted | $0.0681 | 5m31s | 2 | 78 / 48427 / 2190004 / 109835 | SHIP |  |
| tally-invoice-list-performance | D | 2 | accepted | $0.0652 | 4m12s | 2 | 70 / 47740 / 1854199 / 114155 | SHIP |  |
| tally-month-end-renewals | D | 1 | accepted | $0.0350 | 2m47s | 2 | 42 / 30181 / 728121 / 63291 | SHIP |  |
| tally-month-end-renewals | D | 2 | accepted | $0.0406 | 3m5s | 2 | 40 / 38090 / 690462 / 73246 | SHIP |  |
| tally-proration | D | 1 | rejected | $0.1460 | 7m28s | 4 | 68 / 93651 / 1710205 / 209422 | REVISE, SHIP | the hidden acceptance check failed |
| tally-proration | D | 2 | accepted | $0.1097 | 4m53s | 2 | 54 / 59220 / 1830695 / 115049 | SHIP |  |
| tally-renewal-dedupe | D | 1 | accepted | $0.0603 | 4m27s | 4 | 68 / 49246 / 1465508 / 105156 | SHIP |  |
| tally-renewal-dedupe | D | 2 | accepted | $0.0757 | 5m21s | 4 | 64 / 67631 / 1130035 / 153033 | REVISE, SHIP |  |
| tally-role-permissions | D | 1 | accepted | $0.2659 | 7m31s | 4 | 84 / 91882 / 2622400 / 212746 | REVISE, SHIP |  |
| tally-role-permissions | D | 2 | accepted | $0.0967 | 4m47s | 2 | 54 / 65727 / 1595605 / 115644 | SHIP |  |
| tally-seat-limits | D | 1 | accepted | $0.0493 | 2m52s | 2 | 52 / 32678 / 1403718 / 94474 | SHIP |  |
| tally-seat-limits | D | 2 | accepted | $0.0529 | 2m56s | 2 | 66 / 33937 / 1728821 / 93416 | SHIP |  |
| tally-snapshot-v3 | D | 1 | accepted | $0.0544 | 3m29s | 2 | 48 / 41823 / 1375312 / 98404 | SHIP |  |
| tally-snapshot-v3 | D | 2 | accepted | $0.0579 | 3m43s | 2 | 46 / 47086 / 1342164 / 104586 | SHIP |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.1008 | 6m36s | 4 | 76 / 79296 / 1789899 / 174805 | REVISE, SHIP |  |
| tally-soft-delete-customers | D | 2 | accepted | $0.0555 | 3m41s | 2 | 54 / 44765 / 1314084 / 99968 | SHIP |  |
| tally-tenant-isolation | D | 1 | accepted | $0.2735 | 5m12s | 2 | 88 / 59771 / 3758041 / 158913 | SHIP |  |
| tally-tenant-isolation | D | 2 | accepted | $0.2663 | 4m45s | 2 | 122 / 51562 / 4905676 / 166532 | SHIP |  |
| tally-tiered-pricing | D | 1 | accepted | $0.0683 | 4m22s | 4 | 84 / 51978 / 1669218 / 128005 | REVISE, SHIP |  |
| tally-tiered-pricing | D | 2 | accepted | $0.0593 | 3m37s | 2 | 54 / 45048 / 1592271 / 104052 | SHIP |  |
| tally-timezone-reports | D | 1 | accepted | $0.0354 | 2m48s | 2 | 28 / 33172 / 530872 / 67381 | SHIP |  |
| tally-timezone-reports | D | 2 | accepted | $0.0331 | 2m45s | 2 | 30 / 35592 / 438071 / 54557 | SHIP |  |
| tally-webhook-retries | D | 1 | accepted | $0.0517 | 4m7s | 2 | 42 / 46759 / 876576 / 97959 | SHIP |  |
| tally-webhook-retries | D | 2 | accepted | $0.0467 | 3m25s | 2 | 44 / 40246 / 945190 / 85847 | SHIP |  |
| tally-webhook-signatures | D | 1 | accepted | $0.0664 | 4m41s | 4 | 78 / 51383 / 1410131 / 132789 | REVISE, SHIP |  |
| tally-webhook-signatures | D | 2 | accepted | $0.0718 | 4m52s | 4 | 66 / 60030 / 1194181 / 149293 | REVISE, SHIP |  |
| tally-webhook-ssrf | D | 1 | accepted | $0.0297 | 2m16s | 4 | 58 / 21784 / 637689 / 62362 | REVISE, SHIP |  |
| tally-webhook-ssrf | D | 2 | accepted | $0.0331 | 2m29s | 2 | 44 / 27741 / 766770 / 57756 | SHIP |  |
| tally-zero-line-invoices | D | 1 | accepted | $0.0264 | 1m38s | 2 | 50 / 15516 / 710977 / 57779 | SHIP |  |
| tally-zero-line-invoices | D | 2 | accepted | $0.0240 | 1m21s | 2 | 40 / 12798 / 584901 / 58765 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

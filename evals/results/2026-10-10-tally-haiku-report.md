Trial 2026-10-10-tally-haiku; Drydock babb3c1a149b+dirty, cba4f9eaa7b8; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 64 | 63 | $8.5938 | $0.1364 | $0.0877 ($0.0305 to $0.5487) | 4m9s (1m34s to 9m39s) |
| B1 | 64 | 61 | $7.9455 | $0.1303 | $0.0784 ($0.0222 to $0.4247) | 3m13s (59s to 7m15s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-api-key-hashing | D | 3 | 3 | $0.0555 ($0.0513 to $0.0595) | 3m4s (2m58s to 3m16s) |
| tally-api-key-hashing | B1 | 3 | 3 | $0.0534 ($0.0380 to $0.0661) | 2m38s (2m13s to 3m13s) |
| tally-audit-log | D | 3 | 3 | $0.4271 ($0.3626 to $0.5487) | 6m48s (6m4s to 9m39s) |
| tally-audit-log | B1 | 3 | 3 | $0.3256 ($0.2814 to $0.3337) | 4m42s (4m34s to 4m54s) |
| tally-coupons | D | 3 | 3 | $0.3807 ($0.3755 to $0.4310) | 7m40s (5m57s to 7m59s) |
| tally-coupons | B1 | 2 | 2 | $0.3692 ($0.3137 to $0.4247) | 5m34s (4m31s to 6m38s) |
| tally-csv-import | D | 3 | 3 | $0.1373 ($0.1311 to $0.1580) | 6m6s (4m44s to 7m4s) |
| tally-csv-import | B1 | 3 | 3 | $0.0485 ($0.0358 to $0.0921) | 3m1s (2m9s to 3m22s) |
| tally-currency-minor-units | D | 3 | 3 | $0.0364 ($0.0318 to $0.0520) | 2m4s (1m50s to 2m38s) |
| tally-currency-minor-units | B1 | 3 | 3 | $0.0265 ($0.0264 to $0.0310) | 1m29s (1m19s to 1m30s) |
| tally-graceful-shutdown | D | 3 | 3 | $0.0392 ($0.0367 to $0.0968) | 3m6s (2m54s to 6m18s) |
| tally-graceful-shutdown | B1 | 3 | 2 | $0.0289 ($0.0287 to $0.0294) | 2m45s (2m19s to 4m24s) |
| tally-idempotent-payments | D | 3 | 3 | $0.1379 ($0.0581 to $0.2587) | 5m28s (3m33s to 5m54s) |
| tally-idempotent-payments | B1 | 3 | 3 | $0.1735 ($0.1281 to $0.1898) | 3m57s (3m27s to 4m22s) |
| tally-invite-tokens | D | 3 | 3 | $0.0420 ($0.0416 to $0.0494) | 2m37s (2m35s to 3m10s) |
| tally-invite-tokens | B1 | 3 | 3 | $0.0681 ($0.0646 to $0.1872) | 3m3s (2m55s to 6m12s) |
| tally-invoice-list-performance | D | 3 | 3 | $0.2094 ($0.1150 to $0.2624) | 5m22s (4m18s to 7m47s) |
| tally-invoice-list-performance | B1 | 2 | 2 | $0.1949 ($0.1763 to $0.2135) | 4m54s (4m35s to 5m13s) |
| tally-month-end-renewals | D | 3 | 3 | $0.0549 ($0.0485 to $0.1071) | 3m37s (3m12s to 6m47s) |
| tally-month-end-renewals | B1 | 3 | 3 | $0.0322 ($0.0318 to $0.0356) | 2m6s (2m3s to 2m28s) |
| tally-proration | D | 3 | 2 | $0.2073 ($0.1546 to $0.2975) | 5m18s (4m34s to 9m8s) |
| tally-proration | B1 | 3 | 2 | $0.3018 ($0.2993 to $0.3058) | 4m39s (4m22s to 4m51s) |
| tally-renewal-dedupe | D | 3 | 3 | $0.1502 ($0.0547 to $0.1863) | 4m41s (3m25s to 5m9s) |
| tally-renewal-dedupe | B1 | 3 | 3 | $0.1100 ($0.0630 to $0.1468) | 6m18s (3m34s to 7m15s) |
| tally-role-permissions | D | 2 | 2 | $0.1737 ($0.1609 to $0.1864) | 6m41s (6m15s to 7m8s) |
| tally-role-permissions | B1 | 3 | 3 | $0.1500 ($0.1393 to $0.2987) | 4m6s (3m49s to 5m20s) |
| tally-seat-limits | D | 2 | 2 | $0.0616 ($0.0458 to $0.0774) | 3m22s (2m31s to 4m13s) |
| tally-seat-limits | B1 | 2 | 2 | $0.0662 ($0.0646 to $0.0678) | 4m21s (2m23s to 6m19s) |
| tally-snapshot-v3 | D | 2 | 2 | $0.0783 ($0.0546 to $0.1019) | 3m48s (3m12s to 4m24s) |
| tally-snapshot-v3 | B1 | 2 | 2 | $0.0967 ($0.0664 to $0.1269) | 3m11s (2m51s to 3m32s) |
| tally-soft-delete-customers | D | 2 | 2 | $0.1703 ($0.1701 to $0.1704) | 4m14s (4m9s to 4m20s) |
| tally-soft-delete-customers | B1 | 3 | 3 | $0.2354 ($0.1860 to $0.2624) | 3m14s (3m2s to 3m52s) |
| tally-tenant-isolation | D | 3 | 3 | $0.2962 ($0.1589 to $0.3015) | 4m46s (4m7s to 5m15s) |
| tally-tenant-isolation | B1 | 3 | 3 | $0.2393 ($0.2268 to $0.2571) | 3m51s (3m41s to 4m5s) |
| tally-tiered-pricing | D | 3 | 3 | $0.0870 ($0.0865 to $0.1568) | 4m53s (3m46s to 6m39s) |
| tally-tiered-pricing | B1 | 3 | 3 | $0.0555 ($0.0455 to $0.1128) | 2m40s (2m27s to 3m13s) |
| tally-timezone-reports | D | 3 | 3 | $0.0409 ($0.0371 to $0.0460) | 2m41s (2m29s to 2m44s) |
| tally-timezone-reports | B1 | 3 | 3 | $0.0289 ($0.0263 to $0.0324) | 1m51s (1m35s to 2m0s) |
| tally-webhook-retries | D | 3 | 3 | $0.0648 ($0.0576 to $0.0974) | 3m51s (3m42s to 4m9s) |
| tally-webhook-retries | B1 | 3 | 3 | $0.0971 ($0.0886 to $0.0999) | 3m12s (3m9s to 3m18s) |
| tally-webhook-signatures | D | 2 | 2 | $0.0720 ($0.0555 to $0.0885) | 4m7s (3m21s to 4m54s) |
| tally-webhook-signatures | B1 | 2 | 2 | $0.0832 ($0.0381 to $0.1282) | 2m53s (2m18s to 3m28s) |
| tally-webhook-ssrf | D | 3 | 3 | $0.0320 ($0.0305 to $0.0333) | 2m4s (2m4s to 2m20s) |
| tally-webhook-ssrf | B1 | 3 | 2 | $0.0298 ($0.0222 to $0.0324) | 1m47s (1m13s to 1m56s) |
| tally-zero-line-invoices | D | 3 | 3 | $0.0374 ($0.0331 to $0.0382) | 1m42s (1m34s to 1m44s) |
| tally-zero-line-invoices | B1 | 3 | 3 | $0.0251 ($0.0245 to $0.0261) | 1m8s (59s to 1m9s) |

D against B1, task by task (counted runs):

| Task | Class | D accepted | B1 accepted | D cost per run, median | B1 cost per run, median | D wall, median | B1 wall, median |
|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | security review | 3 of 3 | 3 of 3 | $0.0555 | $0.0534 | 3m4s | 2m38s |
| tally-audit-log | iteration | 3 of 3 | 3 of 3 | $0.4271 | $0.3256 | 6m48s | 4m42s |
| tally-coupons | iteration | 3 of 3 | 2 of 2 | $0.3807 | $0.3692 | 7m40s | 5m34s |
| tally-csv-import | iteration | 3 of 3 | 3 of 3 | $0.1373 | $0.0485 | 6m6s | 3m1s |
| tally-currency-minor-units | test-fix loop | 3 of 3 | 3 of 3 | $0.0364 | $0.0265 | 2m4s | 1m29s |
| tally-graceful-shutdown | high-risk | 3 of 3 | 2 of 3 | $0.0392 | $0.0289 | 3m6s | 2m45s |
| tally-idempotent-payments | high-risk | 3 of 3 | 3 of 3 | $0.1379 | $0.1735 | 5m28s | 3m57s |
| tally-invite-tokens | security review | 3 of 3 | 3 of 3 | $0.0420 | $0.0681 | 2m37s | 3m3s |
| tally-invoice-list-performance | tested refactor | 3 of 3 | 2 of 2 | $0.2094 | $0.1949 | 5m22s | 4m54s |
| tally-month-end-renewals | test-fix loop | 3 of 3 | 3 of 3 | $0.0549 | $0.0322 | 3m37s | 2m6s |
| tally-proration | iteration | 2 of 3 | 2 of 3 | $0.2073 | $0.3018 | 5m18s | 4m39s |
| tally-renewal-dedupe | test-fix loop | 3 of 3 | 3 of 3 | $0.1502 | $0.1100 | 4m41s | 6m18s |
| tally-role-permissions | security review | 2 of 2 | 3 of 3 | $0.1737 | $0.1500 | 6m41s | 4m6s |
| tally-seat-limits | iteration | 2 of 2 | 2 of 2 | $0.0616 | $0.0662 | 3m22s | 4m21s |
| tally-snapshot-v3 | iteration | 2 of 2 | 2 of 2 | $0.0783 | $0.0967 | 3m48s | 3m11s |
| tally-soft-delete-customers | iteration | 2 of 2 | 3 of 3 | $0.1703 | $0.2354 | 4m14s | 3m14s |
| tally-tenant-isolation | security review | 3 of 3 | 3 of 3 | $0.2962 | $0.2393 | 4m46s | 3m51s |
| tally-tiered-pricing | iteration | 3 of 3 | 3 of 3 | $0.0870 | $0.0555 | 4m53s | 2m40s |
| tally-timezone-reports | iteration | 3 of 3 | 3 of 3 | $0.0409 | $0.0289 | 2m41s | 1m51s |
| tally-webhook-retries | iteration | 3 of 3 | 3 of 3 | $0.0648 | $0.0971 | 3m51s | 3m12s |
| tally-webhook-signatures | security review | 2 of 2 | 2 of 2 | $0.0720 | $0.0832 | 4m7s | 2m53s |
| tally-webhook-ssrf | security review | 3 of 3 | 2 of 3 | $0.0320 | $0.0298 | 2m4s | 1m47s |
| tally-zero-line-invoices | test-fix loop | 3 of 3 | 3 of 3 | $0.0374 | $0.0251 | 1m42s | 1m8s |

| Class | D accepted | B1 accepted |
|---|---|---|
| high-risk | 6 of 6 | 5 of 6 |
| iteration | 26 of 27 | 26 of 27 |
| security review | 16 of 16 | 16 of 17 |
| test-fix loop | 12 of 12 | 12 of 12 |
| tested refactor | 3 of 3 | 2 of 2 |

D was accepted more often than B1 on 2 task(s), less often on 0 and as often on 21 (23 tasks run by both, at least 2 counted run(s) per arm and task).
This is a pilot: decision P34 asks for at least 20 tasks with 3 counted runs of each arm before any claim, so no test is computed.

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-api-key-hashing | B1 | 1 | accepted | $0.0661 | 3m13s | 1 | 44 / 29714 / 1596071 / 103010 |  |  |
| tally-api-key-hashing | B1 | 2 | accepted | $0.0380 | 2m13s | 1 | 28 / 24692 / 964832 / 80222 |  |  |
| tally-api-key-hashing | B1 | 3 | accepted | $0.0534 | 2m38s | 1 | 56 / 32051 / 2088202 / 82444 |  |  |
| tally-api-key-hashing | D | 1 | accepted | $0.0513 | 2m58s | 2 | 46 / 33582 / 1322204 / 106242 | SHIP |  |
| tally-api-key-hashing | D | 2 | accepted | $0.0555 | 3m16s | 3 | 54 / 36132 / 1386671 / 117668 | SHIP |  |
| tally-api-key-hashing | D | 3 | accepted | $0.0595 | 3m4s | 2 | 52 / 34946 / 1748589 / 122512 | SHIP |  |
| tally-audit-log | B1 | 1 | accepted | $0.3337 | 4m54s | 1 | 50 / 65188 / 2482524 / 147539 |  |  |
| tally-audit-log | B1 | 2 | accepted | $0.2814 | 4m34s | 1 | 48 / 61351 / 2203504 / 139542 |  |  |
| tally-audit-log | B1 | 3 | accepted | $0.3256 | 4m42s | 1 | 54 / 66219 / 2742172 / 145884 |  |  |
| tally-audit-log | D | 1 | accepted | $0.3626 | 6m4s | 2 | 76 / 81896 / 3461075 / 195771 | SHIP |  |
| tally-audit-log | D | 2 | accepted | $0.5487 | 9m39s | 4 | 154 / 104249 / 7196711 / 275253 | REVISE, SHIP |  |
| tally-audit-log | D | 3 | accepted | $0.4271 | 6m48s | 2 | 80 / 93243 / 4006361 / 211116 | SHIP |  |
| tally-coupons | B1 | 1 | accepted | $0.3137 | 4m31s | 1 | 56 / 65150 / 2916623 / 140881 |  |  |
| tally-coupons | B1 | 2 | accepted | $0.4247 | 6m38s | 1 | 72 / 73262 / 4037010 / 151036 |  |  |
| tally-coupons | D | 1 | accepted | $0.4310 | 7m59s | 4 | 82 / 104729 / 3694787 / 272098 | REVISE, SHIP |  |
| tally-coupons | D | 2 | accepted | $0.3807 | 7m40s | 4 | 90 / 98960 / 3711990 / 237401 | REVISE, SHIP |  |
| tally-coupons | D | 3 | accepted | $0.3755 | 5m57s | 2 | 74 / 81108 / 3625438 / 182760 | SHIP |  |
| tally-csv-import | B1 | 1 | accepted | $0.0358 | 2m9s | 1 | 24 / 26929 / 757386 / 73563 |  |  |
| tally-csv-import | B1 | 2 | accepted | $0.0485 | 3m1s | 1 | 42 / 35196 / 1438125 / 82381 |  |  |
| tally-csv-import | B1 | 3 | accepted | $0.0921 | 3m22s | 1 | 36 / 46268 / 1311165 / 88829 |  |  |
| tally-csv-import | D | 1 | accepted | $0.1311 | 6m6s | 4 | 78 / 73633 / 2413043 / 186621 | REVISE, SHIP |  |
| tally-csv-import | D | 2 | accepted | $0.1580 | 7m4s | 6 | 90 / 91260 / 2591787 / 240093 | REVISE, SHIP |  |
| tally-csv-import | D | 3 | accepted | $0.1373 | 4m44s | 2 | 52 / 58997 / 1952011 / 129753 | SHIP |  |
| tally-currency-minor-units | B1 | 1 | accepted | $0.0264 | 1m19s | 1 | 26 / 14768 / 608373 / 64643 |  |  |
| tally-currency-minor-units | B1 | 2 | accepted | $0.0265 | 1m29s | 1 | 32 / 17189 / 790272 / 50102 |  |  |
| tally-currency-minor-units | B1 | 3 | accepted | $0.0310 | 1m30s | 1 | 46 / 17862 / 1193095 / 50457 |  |  |
| tally-currency-minor-units | D | 1 | accepted | $0.0520 | 2m38s | 2 | 58 / 26181 / 1224187 / 150252 | SHIP |  |
| tally-currency-minor-units | D | 2 | accepted | $0.0364 | 2m4s | 2 | 44 / 21758 / 1017981 / 76750 | SHIP |  |
| tally-currency-minor-units | D | 3 | accepted | $0.0318 | 1m50s | 2 | 32 / 20662 / 660042 / 74528 | SHIP |  |
| tally-graceful-shutdown | B1 | 1 | accepted | $0.0287 | 2m45s | 1 | 18 / 30546 / 385001 / 48118 |  |  |
| tally-graceful-shutdown | B1 | 2 | rejected | $0.0289 | 2m19s | 1 | 20 / 28883 / 446608 / 50150 |  | the hidden acceptance check failed |
| tally-graceful-shutdown | B1 | 3 | accepted | $0.0294 | 4m24s | 1 | 22 / 29410 / 482096 / 49573 |  |  |
| tally-graceful-shutdown | D | 1 | accepted | $0.0367 | 2m54s | 2 | 22 / 35982 / 415769 / 72987 | SHIP |  |
| tally-graceful-shutdown | D | 2 | accepted | $0.0392 | 3m6s | 2 | 22 / 38193 / 464781 / 77456 | SHIP |  |
| tally-graceful-shutdown | D | 3 | accepted | $0.0968 | 6m18s | 6 | 84 / 74998 / 1824692 / 205016 | REVISE, REVISE, SHIP |  |
| tally-idempotent-payments | B1 | 1 | accepted | $0.1281 | 3m27s | 1 | 36 / 44334 / 1440240 / 100470 |  |  |
| tally-idempotent-payments | B1 | 2 | accepted | $0.1898 | 3m57s | 1 | 40 / 52307 / 1796331 / 112110 |  |  |
| tally-idempotent-payments | B1 | 3 | accepted | $0.1735 | 4m22s | 1 | 46 / 48533 / 1920232 / 108999 |  |  |
| tally-idempotent-payments | D | 1 | accepted | $0.2587 | 5m28s | 2 | 62 / 65823 / 2761639 / 154288 | SHIP |  |
| tally-idempotent-payments | D | 2 | accepted | $0.0581 | 3m33s | 2 | 44 / 43926 / 1424183 / 109464 | SHIP |  |
| tally-idempotent-payments | D | 3 | accepted | $0.1379 | 5m54s | 4 | 72 / 70367 / 1998675 / 194443 | REVISE, SHIP |  |
| tally-invite-tokens | B1 | 1 | accepted | $0.0646 | 2m55s | 1 | 44 / 36191 / 1591379 / 86790 |  |  |
| tally-invite-tokens | B1 | 2 | accepted | $0.0681 | 3m3s | 1 | 46 / 39360 / 1604318 / 86780 |  |  |
| tally-invite-tokens | B1 | 3 | accepted | $0.1872 | 6m12s | 1 | 58 / 52695 / 2401445 / 110925 |  |  |
| tally-invite-tokens | D | 1 | accepted | $0.0494 | 3m10s | 2 | 56 / 32969 / 1491459 / 89866 | SHIP |  |
| tally-invite-tokens | D | 2 | accepted | $0.0420 | 2m37s | 2 | 42 / 30907 / 1030858 / 81326 | SHIP |  |
| tally-invite-tokens | D | 3 | accepted | $0.0416 | 2m35s | 2 | 42 / 29874 / 974898 / 84791 | SHIP |  |
| tally-invoice-list-performance | B1 | 1 | accepted | $0.1763 | 4m35s | 1 | 58 / 45908 / 2592252 / 110467 |  |  |
| tally-invoice-list-performance | B1 | 2 | accepted | $0.2135 | 5m13s | 1 | 50 / 58037 / 2236026 / 120538 |  |  |
| tally-invoice-list-performance | D | 1 | accepted | $0.2094 | 5m22s | 2 | 56 / 57544 / 2353186 / 153583 | SHIP |  |
| tally-invoice-list-performance | D | 2 | accepted | $0.1150 | 4m18s | 2 | 78 / 51078 / 2735808 / 129399 | SHIP |  |
| tally-invoice-list-performance | D | 3 | accepted | $0.2624 | 7m47s | 2 | 104 / 70097 / 4443435 / 162595 | SHIP |  |
| tally-month-end-renewals | B1 | 1 | accepted | $0.0318 | 2m3s | 1 | 26 / 26299 / 671289 / 59458 |  |  |
| tally-month-end-renewals | B1 | 2 | accepted | $0.0356 | 2m28s | 1 | 32 / 30671 / 840672 / 59290 |  |  |
| tally-month-end-renewals | B1 | 3 | accepted | $0.0322 | 2m6s | 1 | 26 / 27130 / 639530 / 61064 |  |  |
| tally-month-end-renewals | D | 1 | accepted | $0.0549 | 3m37s | 2 | 44 / 46425 / 1225895 / 97005 | SHIP |  |
| tally-month-end-renewals | D | 2 | accepted | $0.1071 | 6m47s | 4 | 84 / 88207 / 2285527 / 200887 | REVISE, SHIP |  |
| tally-month-end-renewals | D | 3 | accepted | $0.0485 | 3m12s | 2 | 38 / 41611 / 985263 / 89381 | SHIP |  |
| tally-proration | B1 | 1 | accepted | $0.3018 | 4m39s | 1 | 70 / 59868 / 3464591 / 126591 |  |  |
| tally-proration | B1 | 2 | accepted | $0.2993 | 4m22s | 1 | 48 / 59257 / 2449941 / 130946 |  |  |
| tally-proration | B1 | 3 | rejected | $0.3058 | 4m51s | 1 | 60 / 66601 / 3083972 / 132629 |  | the hidden acceptance check failed |
| tally-proration | D | 1 | accepted | $0.2975 | 9m8s | 6 | 140 / 115406 / 4611383 / 316049 | REVISE, REVISE, SHIP |  |
| tally-proration | D | 2 | rejected | $0.1546 | 4m34s | 2 | 56 / 59215 / 2084211 / 137031 | SHIP | the hidden acceptance check failed |
| tally-proration | D | 3 | accepted | $0.2073 | 5m18s | 2 | 56 / 71444 / 2252716 / 156087 | SHIP |  |
| tally-renewal-dedupe | B1 | 1 | accepted | $0.1100 | 6m18s | 1 | 44 / 45425 / 1706421 / 97707 |  |  |
| tally-renewal-dedupe | B1 | 2 | accepted | $0.0630 | 3m34s | 1 | 30 / 44744 / 987437 / 87008 |  |  |
| tally-renewal-dedupe | B1 | 3 | accepted | $0.1468 | 7m15s | 1 | 46 / 56810 / 1863119 / 104477 |  |  |
| tally-renewal-dedupe | D | 1 | accepted | $0.1863 | 5m9s | 2 | 48 / 71782 / 1821218 / 152836 | SHIP |  |
| tally-renewal-dedupe | D | 2 | accepted | $0.0547 | 3m25s | 2 | 42 / 41135 / 1278215 / 106698 | SHIP |  |
| tally-renewal-dedupe | D | 3 | accepted | $0.1502 | 4m41s | 2 | 64 / 59633 / 2447658 / 123564 | SHIP |  |
| tally-role-permissions | B1 | 1 | accepted | $0.1393 | 3m49s | 1 | 60 / 50140 / 2508535 / 108416 |  |  |
| tally-role-permissions | B1 | 2 | accepted | $0.2987 | 5m20s | 1 | 50 / 68686 / 2604884 / 137023 |  |  |
| tally-role-permissions | B1 | 3 | accepted | $0.1500 | 4m6s | 1 | 54 / 54880 / 2174902 / 101924 |  |  |
| tally-role-permissions | D | 1 | accepted | $0.1609 | 6m15s | 4 | 78 / 80864 / 2278141 / 211655 | REVISE, SHIP |  |
| tally-role-permissions | D | 2 | accepted | $0.1864 | 7m8s | 4 | 84 / 87026 / 2566922 / 218018 | REVISE, SHIP |  |
| tally-seat-limits | B1 | 1 | accepted | $0.0678 | 6m19s | 1 | 48 / 31771 / 1867453 / 89118 |  |  |
| tally-seat-limits | B1 | 2 | accepted | $0.0646 | 2m23s | 1 | 50 / 29104 / 1894228 / 87980 |  |  |
| tally-seat-limits | D | 1 | accepted | $0.0774 | 4m13s | 4 | 72 / 50552 / 1686729 / 176463 | REVISE, SHIP |  |
| tally-seat-limits | D | 2 | accepted | $0.0458 | 2m31s | 2 | 34 / 30761 / 934638 / 105168 | SHIP |  |
| tally-snapshot-v3 | B1 | 1 | accepted | $0.0664 | 2m51s | 1 | 36 / 37899 / 1257406 / 88467 |  |  |
| tally-snapshot-v3 | B1 | 2 | accepted | $0.1269 | 3m32s | 1 | 34 / 44220 / 1410678 / 106510 |  |  |
| tally-snapshot-v3 | D | 1 | accepted | $0.1019 | 4m24s | 2 | 54 / 58962 / 1806922 / 126705 | SHIP |  |
| tally-snapshot-v3 | D | 2 | accepted | $0.0546 | 3m12s | 2 | 40 / 40921 / 1264467 / 107695 | SHIP |  |
| tally-soft-delete-customers | B1 | 1 | accepted | $0.2624 | 3m52s | 1 | 68 / 44410 / 3144906 / 122702 |  |  |
| tally-soft-delete-customers | B1 | 2 | accepted | $0.1860 | 3m2s | 1 | 40 / 42220 / 1676258 / 116208 |  |  |
| tally-soft-delete-customers | B1 | 3 | accepted | $0.2354 | 3m14s | 1 | 48 / 46013 / 2203756 / 123482 |  |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.1704 | 4m9s | 2 | 50 / 55607 / 1890296 / 148295 | SHIP |  |
| tally-soft-delete-customers | D | 2 | accepted | $0.1701 | 4m20s | 2 | 56 / 55897 / 2152969 / 147127 | SHIP |  |
| tally-tenant-isolation | B1 | 1 | accepted | $0.2571 | 4m5s | 1 | 56 / 51203 / 2606652 / 135304 |  |  |
| tally-tenant-isolation | B1 | 2 | accepted | $0.2393 | 3m41s | 1 | 42 / 47405 / 1916578 / 131806 |  |  |
| tally-tenant-isolation | B1 | 3 | accepted | $0.2268 | 3m51s | 1 | 48 / 51374 / 2254446 / 130064 |  |  |
| tally-tenant-isolation | D | 1 | accepted | $0.3015 | 5m15s | 4 | 98 / 59648 / 3766595 / 291474 | REVISE, SHIP |  |
| tally-tenant-isolation | D | 2 | accepted | $0.1589 | 4m7s | 2 | 62 / 53318 / 2304457 / 184839 | SHIP |  |
| tally-tenant-isolation | D | 3 | accepted | $0.2962 | 4m46s | 2 | 72 / 59071 / 3219138 / 206917 | SHIP |  |
| tally-tiered-pricing | B1 | 1 | accepted | $0.0555 | 2m40s | 1 | 38 / 32596 / 1415153 / 85995 |  |  |
| tally-tiered-pricing | B1 | 2 | accepted | $0.1128 | 3m13s | 1 | 44 / 41987 / 1809649 / 96167 |  |  |
| tally-tiered-pricing | B1 | 3 | accepted | $0.0455 | 2m27s | 1 | 46 / 32743 / 1419430 / 74511 |  |  |
| tally-tiered-pricing | D | 1 | accepted | $0.1568 | 6m39s | 6 | 100 / 87220 / 2678349 / 256922 | REVISE, REVISE, SHIP |  |
| tally-tiered-pricing | D | 2 | accepted | $0.0870 | 4m53s | 4 | 72 / 59586 / 2174797 / 177391 | REVISE, SHIP |  |
| tally-tiered-pricing | D | 3 | accepted | $0.0865 | 3m46s | 2 | 58 / 50939 / 2037754 / 115811 | SHIP |  |
| tally-timezone-reports | B1 | 1 | accepted | $0.0263 | 1m35s | 1 | 24 / 20836 / 543552 / 52403 |  |  |
| tally-timezone-reports | B1 | 2 | accepted | $0.0289 | 2m0s | 1 | 26 / 23849 / 622056 / 53946 |  |  |
| tally-timezone-reports | B1 | 3 | accepted | $0.0324 | 1m51s | 1 | 28 / 24410 / 757214 / 63328 |  |  |
| tally-timezone-reports | D | 1 | accepted | $0.0460 | 2m44s | 2 | 48 / 33962 / 1232159 / 83589 | SHIP |  |
| tally-timezone-reports | D | 2 | accepted | $0.0371 | 2m29s | 2 | 32 / 29829 / 707211 / 75772 | SHIP |  |
| tally-timezone-reports | D | 3 | accepted | $0.0409 | 2m41s | 2 | 40 / 33716 / 877723 / 76437 | SHIP |  |
| tally-webhook-retries | B1 | 1 | accepted | $0.0971 | 3m9s | 1 | 36 / 39747 / 1362143 / 96170 |  |  |
| tally-webhook-retries | B1 | 2 | accepted | $0.0999 | 3m18s | 1 | 26 / 41082 / 931804 / 105074 |  |  |
| tally-webhook-retries | B1 | 3 | accepted | $0.0886 | 3m12s | 1 | 28 / 42769 / 945372 / 96722 |  |  |
| tally-webhook-retries | D | 1 | accepted | $0.0648 | 3m51s | 2 | 50 / 49923 / 1655095 / 116452 | SHIP |  |
| tally-webhook-retries | D | 2 | accepted | $0.0974 | 3m42s | 2 | 40 / 48771 / 1338440 / 122674 | SHIP |  |
| tally-webhook-retries | D | 3 | accepted | $0.0576 | 4m9s | 2 | 40 / 47421 / 1192006 / 110016 | SHIP |  |
| tally-webhook-signatures | B1 | 1 | accepted | $0.0381 | 2m18s | 1 | 32 / 28436 / 879455 / 75578 |  |  |
| tally-webhook-signatures | B1 | 2 | accepted | $0.1282 | 3m28s | 1 | 42 / 46993 / 1622667 / 106943 |  |  |
| tally-webhook-signatures | D | 1 | accepted | $0.0555 | 3m21s | 2 | 40 / 43935 / 1179272 / 108588 | SHIP |  |
| tally-webhook-signatures | D | 2 | accepted | $0.0885 | 4m54s | 4 | 82 / 59239 / 2203731 / 183945 | REVISE, SHIP |  |
| tally-webhook-ssrf | B1 | 1 | accepted | $0.0222 | 1m13s | 1 | 18 / 16326 / 411633 / 49745 |  |  |
| tally-webhook-ssrf | B1 | 2 | rejected | $0.0324 | 1m56s | 1 | 40 / 20546 / 1101084 / 55396 |  | the hidden acceptance check failed |
| tally-webhook-ssrf | B1 | 3 | accepted | $0.0298 | 1m47s | 1 | 34 / 20787 / 875224 / 53290 |  |  |
| tally-webhook-ssrf | D | 1 | accepted | $0.0333 | 2m20s | 2 | 36 / 22901 / 787012 / 69787 | SHIP |  |
| tally-webhook-ssrf | D | 2 | accepted | $0.0320 | 2m4s | 2 | 32 / 23181 / 697365 / 67279 | SHIP |  |
| tally-webhook-ssrf | D | 3 | accepted | $0.0305 | 2m4s | 2 | 26 / 24003 / 521446 / 66494 | SHIP |  |
| tally-zero-line-invoices | B1 | 1 | accepted | $0.0251 | 59s | 1 | 30 / 10877 / 798294 / 58510 |  |  |
| tally-zero-line-invoices | B1 | 2 | accepted | $0.0245 | 1m8s | 1 | 26 / 11623 / 685957 / 59227 |  |  |
| tally-zero-line-invoices | B1 | 3 | accepted | $0.0261 | 1m9s | 1 | 26 / 13494 / 690809 / 62191 |  |  |
| tally-zero-line-invoices | D | 1 | accepted | $0.0331 | 1m34s | 2 | 44 / 17247 / 952046 / 74668 | SHIP |  |
| tally-zero-line-invoices | D | 2 | accepted | $0.0374 | 1m44s | 2 | 46 / 17826 / 1114449 / 86777 | SHIP |  |
| tally-zero-line-invoices | D | 3 | accepted | $0.0382 | 1m42s | 2 | 42 / 20347 / 1022837 / 89163 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

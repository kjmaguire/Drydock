Trial 2026-10-10-skills-probe; Drydock 60019f9be6f2; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 6 | 6 | $0.9363 | $0.1561 | $0.0950 ($0.0432 to $0.3751) | 4m30s (2m47s to 6m13s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| tally-audit-log | D | 1 | 1 | $0.2761 (n=1) | 5m53s (n=1) |
| tally-coupons | D | 1 | 1 | $0.3751 (n=1) | 6m13s (n=1) |
| tally-csv-import | D | 1 | 1 | $0.0680 (n=1) | 4m46s (n=1) |
| tally-idempotent-payments | D | 1 | 1 | $0.0519 (n=1) | 3m36s (n=1) |
| tally-seat-limits | D | 1 | 1 | $0.0432 (n=1) | 2m47s (n=1) |
| tally-soft-delete-customers | D | 1 | 1 | $0.1220 (n=1) | 4m13s (n=1) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| tally-audit-log | D | 1 | accepted | $0.2761 | 5m53s | 2 | 72 / 75977 / 3025957 / 168730 | SHIP |  |
| tally-coupons | D | 1 | accepted | $0.3751 | 6m13s | 2 | 82 / 83321 / 4021447 / 172252 | SHIP |  |
| tally-csv-import | D | 1 | accepted | $0.0680 | 4m46s | 4 | 78 / 57102 / 1623203 / 115962 | REVISE, SHIP |  |
| tally-idempotent-payments | D | 1 | accepted | $0.0519 | 3m36s | 2 | 40 / 45386 / 931091 / 99372 | SHIP |  |
| tally-seat-limits | D | 1 | accepted | $0.0432 | 2m47s | 2 | 42 / 34127 / 952846 / 83092 | SHIP |  |
| tally-soft-delete-customers | D | 1 | accepted | $0.1220 | 4m13s | 2 | 48 / 49857 / 1638181 / 126689 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

Trial 2026-10-10-pilot; Drydock 895299ed5eba; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 1 | 1 | $0.0227 | $0.0227 | $0.0227 (n=1) | 1m49s (n=1) |
| B1 | 1 | 1 | $0.0150 | $0.0150 | $0.0150 (n=1) | 53s (n=1) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| saasapi-csv-export | D | 1 | 1 | $0.0227 (n=1) | 1m49s (n=1) |
| saasapi-csv-export | B1 | 1 | 1 | $0.0150 (n=1) | 53s (n=1) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| saasapi-csv-export | B1 | 1 | accepted | $0.0150 | 53s | 1 | 16 / 11066 / 281702 / 33329 |  |  |
| saasapi-csv-export | D | 1 | accepted | $0.0227 | 1m49s | 2 | 32 / 16252 / 482489 / 48485 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

Trial 2026-10-10-pilot-checks; Drydock 895299ed5eba; 2.1.296 (Claude Code); model asked haiku, reported claude-haiku-5-5; host linux/amd64.

| Arm | Runs | Accepted | Cost, all runs | Cost per accepted task | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|---|
| D | 12 | 12 | $0.3255 | $0.0271 | $0.0229 ($0.0135 to $0.0501) | 1m22s (46s to 3m9s) |
| B1 | 12 | 12 | $0.1972 | $0.0164 | $0.0165 ($0.0061 to $0.0292) | 1m8s (29s to 3m55s) |

| Task | Arm | Runs | Accepted | Cost per run, median (range) | Wall time per run, median (range) |
|---|---|---|---|---|---|
| saasapi-csv-export | D | 3 | 3 | $0.0235 ($0.0191 to $0.0389) | 1m21s (1m6s to 2m7s) |
| saasapi-csv-export | B1 | 3 | 3 | $0.0154 ($0.0125 to $0.0162) | 1m2s (58s to 1m8s) |
| saasapi-invoice-pages | D | 3 | 3 | $0.0407 ($0.0387 to $0.0501) | 2m41s (2m21s to 3m5s) |
| saasapi-invoice-pages | B1 | 3 | 3 | $0.0231 ($0.0223 to $0.0292) | 1m37s (1m36s to 3m55s) |
| saasapi-invoice-rules | D | 3 | 3 | $0.0142 ($0.0135 to $0.0163) | 50s (46s to 54s) |
| saasapi-invoice-rules | B1 | 3 | 3 | $0.0086 ($0.0061 to $0.0091) | 30s (29s to 1m8s) |
| saasapi-rate-limit | D | 3 | 3 | $0.0223 ($0.0205 to $0.0278) | 1m23s (1m21s to 3m9s) |
| saasapi-rate-limit | B1 | 3 | 3 | $0.0188 ($0.0167 to $0.0192) | 1m12s (1m5s to 1m13s) |

Every run:

| Task | Arm | # | Outcome | Cost | Wall | Sessions | Tokens in / out / cache read / cache write | Reviews | Why |
|---|---|---|---|---|---|---|---|---|---|
| saasapi-csv-export | B1 | 1 | accepted | $0.0125 | 1m8s | 1 | 14 / 11121 / 251454 / 22003 |  |  |
| saasapi-csv-export | B1 | 2 | accepted | $0.0154 | 58s | 1 | 12 / 12502 / 199550 / 35775 |  |  |
| saasapi-csv-export | B1 | 3 | accepted | $0.0162 | 1m2s | 1 | 16 / 13179 / 277830 / 34182 |  |  |
| saasapi-csv-export | D | 1 | accepted | $0.0235 | 1m21s | 2 | 24 / 16128 / 429991 / 55871 | SHIP |  |
| saasapi-csv-export | D | 2 | accepted | $0.0191 | 1m6s | 2 | 16 / 12596 / 253101 / 51186 | SHIP |  |
| saasapi-csv-export | D | 3 | accepted | $0.0389 | 2m7s | 4 | 44 / 24401 / 713526 / 97619 | REVISE, SHIP |  |
| saasapi-invoice-pages | B1 | 1 | accepted | $0.0223 | 1m36s | 1 | 16 / 21326 / 301376 / 43302 |  |  |
| saasapi-invoice-pages | B1 | 2 | accepted | $0.0231 | 1m37s | 1 | 18 / 21301 / 366907 / 43956 |  |  |
| saasapi-invoice-pages | B1 | 3 | accepted | $0.0292 | 3m55s | 1 | 30 / 24689 / 725863 / 47906 |  |  |
| saasapi-invoice-pages | D | 1 | accepted | $0.0387 | 2m21s | 2 | 36 / 32300 / 779581 / 73789 | SHIP |  |
| saasapi-invoice-pages | D | 2 | accepted | $0.0407 | 2m41s | 2 | 36 / 34763 / 830952 / 74847 | SHIP |  |
| saasapi-invoice-pages | D | 3 | accepted | $0.0501 | 3m5s | 5 | 50 / 35162 / 927011 / 116170 | REVISE, SHIP |  |
| saasapi-invoice-rules | B1 | 1 | accepted | $0.0061 | 29s | 1 | 12 / 5546 / 179971 / 7557 |  |  |
| saasapi-invoice-rules | B1 | 2 | accepted | $0.0091 | 1m8s | 1 | 12 / 6258 / 169095 / 21358 |  |  |
| saasapi-invoice-rules | B1 | 3 | accepted | $0.0086 | 30s | 1 | 10 / 6252 / 135366 / 20482 |  |  |
| saasapi-invoice-rules | D | 1 | accepted | $0.0163 | 54s | 2 | 22 / 10269 / 336154 / 38927 | SHIP |  |
| saasapi-invoice-rules | D | 2 | accepted | $0.0135 | 46s | 2 | 18 / 7756 / 255387 / 35342 | SHIP |  |
| saasapi-invoice-rules | D | 3 | accepted | $0.0142 | 50s | 2 | 20 / 8641 / 291576 / 34747 | SHIP |  |
| saasapi-rate-limit | B1 | 1 | accepted | $0.0192 | 1m12s | 1 | 20 / 14892 / 397693 / 38832 |  |  |
| saasapi-rate-limit | B1 | 2 | accepted | $0.0167 | 1m5s | 1 | 16 / 13323 / 294856 / 35645 |  |  |
| saasapi-rate-limit | B1 | 3 | accepted | $0.0188 | 1m13s | 1 | 18 / 15222 / 349147 / 38296 |  |  |
| saasapi-rate-limit | D | 1 | accepted | $0.0223 | 1m23s | 2 | 22 / 15388 / 355340 / 55264 | SHIP |  |
| saasapi-rate-limit | D | 2 | accepted | $0.0278 | 3m9s | 2 | 32 / 19910 / 576569 / 60234 | SHIP |  |
| saasapi-rate-limit | D | 3 | accepted | $0.0205 | 1m21s | 2 | 18 / 15599 / 294307 / 48657 | SHIP |  |

Cost is Claude Code's own `total_cost_usd` (its estimate at API prices, not what a subscription costs); "unknown" counts sessions that reported none, which makes a figure a lower bound. Accepted: the hidden acceptance check passed. The numbers are a small sample: medians and ranges, no averages, no claim of a difference beyond these runs.

# The head-to-head trial

Drydock (arm **D**) against plain Claude Code (arm **B1**, "one premium agent, no team") on the same tasks, from the same start
commit, with the same model, judged by acceptance checks neither arm ever sees. Design and results: `docs/evals.md` section 13.

- `tasks/<id>.yaml`: the suite (the format of `docs/evals.md` 6.1). Unknown fields are refused.
- `testdata/hidden/<id>/`: each task's hidden acceptance files, laid over a copy of an arm's result before the acceptance
  commands run. They live under `testdata`, so the go tool never builds them as part of this module.
- `testdata/solutions/<id>/`: a reference solution per task. `drydock-trial selfcheck` (and the package's tests) prove each
  hidden check fails on the untouched fixture and passes on the reference solution.
- `results/<date>_<trial>_<arm>_<task>_<k>/`: one directory per run: `run.json` (the record of `docs/evals.md` 6.2), `diff.patch`
  (what the arm changed), and either `transcript.jsonl` (B1: Claude Code's own stream) or `events.json`, `usage.json` and
  `evidence.json` (D: the core's events, usage rows and check evidence for the task). `results/<trial>-report.md` is the
  generated report. Nothing in a run directory is typed by hand.

The fixture is `testdata/fixtures/saasapi`: a small multi-tenant billing API in standard-library Go, so both arms work offline.

```sh
go run ./cmd/drydock-trial selfcheck
go run ./cmd/drydock-trial run -trial 2026-10-10-pilot -k 2 -model haiku -ceiling-usd 2 -total-usd 10
go run ./cmd/drydock-trial report -trial 2026-10-10-pilot
```

A run spends real money on the signed-in Claude account: at most `-ceiling-usd` per run (B1: `--max-budget-usd`; D: a project
budget), and the runner starts no run that could pass `-total-usd`. Arm D's worker runs its commands in the per-task sandbox by
default (`-d-mode sandbox`, which needs a Docker-compatible engine) or runs the project's checks on this machine
(`-d-mode checks`). B1 runs with Bash on this machine, the way a user runs `claude -p` with Bash allowed; `-work` must therefore be
outside this repository, and a B1 session whose tool calls name the hidden checks is reported but not counted.

# Drydock: GitHub flow (`gh`, pr_flow, CI, review round trip)

Scope: this document specifies how Drydock integrates GitHub through the `gh` CLI behind the `GitHost` interface (brief section 13, "GitHub (13b)"; Slice 1E). It covers the `gh` Connection and auth detection, the two merge strategies `local_merge` and `pr_flow`, PR creation with a generated description, the `ci` check with failing logs fed back to the worker, the review-comment round trip, issue import, polling with ETags from Go, rate limits, error handling, and the real gaps found in Phase 0: `gh pr view --json` has no review threads (so thread read and resolve use `gh api graphql`), `gh repo create` and `gh release create` have no `--json`, `gh run view --log-failed` is text only, and `gh api` has no verified 304 handling (D14). Merge is always a human action; auto-merge is never enabled; all GitHub text is untrusted data. Decisions in [`decisions.md`](decisions.md) win over the brief.

Evidence tags: `[tool §3]` = [`phase0/tooling-integrations.md`](phase0/tooling-integrations.md) section 3 (GitHub CLI), `[tool §6]` = its MCP catalog, `[go §n]` = [`phase0/go-platform.md`](phase0/go-platform.md), `Dn` = decisions.md, `Pn` = process decisions. Anything not backed by these is **design** or **unverified**; in particular, GitHub REST and GraphQL schema details not read in Phase 0 are marked and must be checked by introspection in Slice 1E. Related: [`connections.md`](connections.md) (Connection model), [`mcp.md`](mcp.md) (the `gh` MCP catalog entry), [`integrations.md`](integrations.md) (`external_items`).

## 1. The `gh` Connection

`gh` v2.102.0 (MIT) is a user-installed Connection, not bundled (decisions.md section 2); a Windows Defender false-positive report for `gh` exists (issue #13306, search snippet) [tool §3]. Manifest-driven like other tools ([`connections.md`](connections.md)): `kind: tool`, `detect: gh --version`, tested version recorded in `compat.yaml` (minimum unverified; start at the tested version).

| Probe | Command | Handling |
|---|---|---|
| Installed, version | `gh --version` | `not_installed` / `unsupported_version`. |
| Auth | `gh auth status --json hosts` (optional `--active`; `--show-token` only for section 6) | Each entry carries `state, error, active, host, login, tokenSource, token, scopes, gitProtocol`. The command **exits 1 if any account has a problem**, so always decode stdout JSON regardless of exit code. The top-level shape of `hosts` and the `state` values were not observed (no login in Phase 0): decode permissively and treat unknown as `degraded`. |
| Scopes | `scopes` of the active entry | `gh auth login` requests `repo`, `read:org`, `gist` by default; Drydock requires `repo` (and `read:org` for org repos). Missing scope: `degraded`, `reason=scope_missing:repo`. |
| Token source | `tokenSource` | Lookup order is env `GH_TOKEN`/`GITHUB_TOKEN`, then config, then the system credential store (Windows Credential Manager through go-keyring, inferred) [tool §3]. If an env token is active, warn that it overrides the stored login. |
| Login | `gh auth login` in the embedded terminal | Never `--with-token` from Drydock; credentials never pass through our code for login. |

**As built.** There is no `gh` Connection card, manifest or `compat.yaml` entry. `github.status` (`internal/core/github_account.go`) finds `gh` on PATH and calls `gh api /user`: it reports not installed, not signed in ("run `gh auth login`"), GitHub not answering within 20 s, or the login. The project wizard and the repository picker (also used in Project settings to attach a repository) show it. Scopes, token source and version are not checked, and Drydock has no Log in button: the human runs `gh auth login` themselves. Every GitHub call is `gh api` over REST (`internal/git/host`, `GhCLI`), with `GH_PROMPT_DISABLED=1`; Drydock never reads or stores a GitHub token.

A WSL `gh` has its own credential store; run mode is a separate Connection instance. Windows config dir precedence: `GH_CONFIG_DIR`, `XDG_CONFIG_HOME`, `AppData` [tool §3]. Only `github.com` is in scope for v1; multiple hosts (GHES) appear in the `hosts` list and are shown but unsupported (open question).

Runner rules: all `gh` calls go through the Process Manager with argv arrays (never a shell string), `cwd` set to the repo, stdin closed, a per-call timeout, stdout and stderr captured to the run log with tokens redacted, no interactive prompts (every argument is passed explicitly). Strict JSON decoding tolerates unknown fields so `gh` upgrades that add JSON fields do not break parsing; a removed field raises `adapter.drift`-style `github.schema_drift`.

```go
type GitHost interface {
    Status(ctx context.Context) (HostStatus, error)
    CreatePR(ctx context.Context, in CreatePR) (PR, error)                 // pr_flow only
    GetPR(ctx context.Context, ref PRRef) (PR, error)
    Checks(ctx context.Context, ref PRRef) (CIResult, error)
    FailedLog(ctx context.Context, runID int64) (LogRef, error)           // raw to disk, shaped copy returned
    ReviewThreads(ctx context.Context, ref PRRef) ([]Thread, error)       // GraphQL
    ReplyToThread(ctx context.Context, th ThreadID, body string) error    // approval-gated
    ResolveThread(ctx context.Context, th ThreadID) error                 // approval-gated
    ListIssues(ctx context.Context, f IssueFilter, cur Cursor) ([]Issue, Cursor, error)
    Merge(ctx context.Context, ref PRRef, m MergeMethod, by HumanAction) error  // human only
    CreateRepo(ctx context.Context, in NewRepo) (string, error)           // approval-gated
    CreateRelease(ctx context.Context, in NewRelease) (string, error)     // human-initiated
}
```

## 2. What `gh` gives and the gaps [tool §3]

| Need | Available | Gap and approach |
|---|---|---|
| PR data | `gh pr view/list --json` with: assignees, author, body, closed, comments, createdAt, closedAt, id, labels, milestone, number, state, title, updatedAt, url, additions, autoMergeRequest, baseRefName, baseRefOid, changedFiles, closingIssuesReferences, commits, deletions, files, headRefName, headRefOid, headRepository, headRepositoryOwner, isCrossRepository, isDraft, latestReviews, maintainerCanModify, mergeable, mergeCommit, mergedAt, mergedBy, mergeStateStatus, reviewDecision, reviewRequests, reviews, statusCheckRollup | `comments` are issue-style comments; `reviews` carry id, author, authorAssociation, submittedAt, body, state, commit. **No inline review comments and no thread resolved-state.** |
| Review threads | none in `gh pr view --json`; no `gh pr` subcommand resolves threads | Use `gh api graphql` (section 7). The GitHub MCP server handles threads natively (`pull_request_read method=get_review_comments`, `pull_request_review_write` with `threadId`) and is offered to agents read-only ([`mcp.md`](mcp.md)); the app itself uses GraphQL. |
| PR create | `gh pr create` | No `--json` evidence; parse the printed URL, then `gh pr view <url> --json ...`. |
| Checks | `gh pr checks --json name,state,startedAt,completedAt,link,bucket,event,workflow,description`; **exit code 8 = checks pending** | `bucket` value set unverified. |
| Runs and logs | `gh run view --json name,displayTitle,headBranch,headSha,status,conclusion,event,number,databaseId,workflowName,url,jobs,...`; `--log-failed` | Logs are **text only**; `--log` and `--log-failed` cannot be combined. |
| Issues | `gh issue list --json` shared fields plus isPinned, stateReason, closedByPullRequestsReferences, issueType, parent, subIssues, subIssuesSummary, blockedBy, blocking | none |
| Repo and release creation | `gh repo create`, `gh release create` | **No `--json`** (no `AddJSONFlags` in source): parse stdout URL, then `gh repo view --json` or `gh api`. |
| Conditional requests | `gh api -i`, `-H`, `--cache <duration>` (client-side TTL cache) | **No 304 handling found in `pkg/cmd/api`**; passing `-H 'If-None-Match: ...'` through `gh api` is unverified. ETag polling therefore uses Go's HTTP client directly (D14), section 6. |

## 3. Strategies: `local_merge` and `pr_flow`

Per-project setting `merge_strategy`, plus `merge_method` (`merge`, `squash`, `rebase`) and `base_branch`. Both strategies share the local pipeline: isolated worktree, safe points under `refs/aet/<task>/<attempt>`, deterministic checks (`build`, `test`, `lint`, `typecheck`, `deps`, `secrets`, `e2e`, `preview` where configured), second-agent review with evidence, and human diff review.

| Step | `local_merge` | `pr_flow` |
|---|---|---|
| Publish | none; no GitHub needed | first push per project asks for approval naming the remote; later pushes of task branches are covered by the project's `pr_flow` setting. `git push -u origin <branch>` through the `git` CLI (brief section 2). Force push and branch deletion always need approval. |
| Describe | n/a | PR title and body are generated from the immutable Room minutes (brief section 11), with task id, evidence links, check summary, and `Closes #n` when imported from an issue. Written to a temp file and passed with `--body-file`. |
| Open | n/a | `gh pr create --title ... --body-file ... --base <base> --head <branch>` (draft optional). |
| Verify | local checks only | local checks, then the remote `ci` check (section 5). |
| Review | in-app human diff review | in-app review plus GitHub review round trip (section 7). |
| Merge | human clicks Merge; Drydock performs the git merge per `merge_method` in a dedicated merge worktree; refuses if the target moved (branch behind target: a task step merges or rebases the target into the task branch first) | human clicks Merge in Drydock (runs `gh pr merge` with the chosen method after confirmation) or merges on github.com. Drydock **never passes auto-merge flags**, never deletes the branch implicitly (deletion needs approval), and warns when `autoMergeRequest` is non-null. |
| Complete | task `VERIFICATION -> COMPLETED` on checks pass plus human accept | same; the human merge (`mergedBy` is a human) counts as the accept event; a PR closed unmerged leaves the task in `WAITING_HUMAN`, never auto-cancelled |

Same-repository branches only in v1: PRs where `isCrossRepository` is true (forks) are shown read-only.

**As built (`internal/pipeline/prflow.go`, graph `iterate-until-verified-pr` and the `-pr` variants of the templates).** The project's delivery mode is `local_merge` or `pr_flow` with a merge strategy `merge`, `squash` or `rebase` (`project.update`; `pr_flow` is refused when the repository's remote is not on GitHub). The base branch is the project's default branch. After the independent reviewer says SHIP, `push_pr` runs `git push --set-upstream origin <branch>` with no approval step and opens the PR through the REST API (title = task title; body = summary, acceptance criteria, evidence, review verdicts, `Closes #n` for an imported issue); a later pass pushes again and reuses the PR. Then `ci` (section 5) and `pr_feedback` (section 7), then the human's "Review and merge" request. Approving it merges through GitHub with the project's strategy, only when the PR head is still the commit that was reviewed (the task worktree's head; the merge call also carries that SHA as GitHub's head guard). If the head moved, or the worktree is gone, nothing is merged and a question in Needs you explains why. **Branch protection is respected, never bypassed.** Before merging, Drydock reads GitHub's merge state (`mergeable_state` of `GET /pulls/{n}`, the REST name of GraphQL's `mergeStateStatus`; read again up to 4 times, 2 s apart, while GitHub still works it out). A pull request that is `blocked` (a required review or check is missing or has not passed), `behind` (the protection wants the branch up to date first), `dirty` (conflicts), a draft, or still `unknown` is not merged: a question in Needs you says so in plain words ("Not merged: pull request #12 is behind main, and the branch protection of main wants it up to date before a merge. Check again, or stop."); the task page offers Check again (the merge step runs again) and Stop (the run stops). `clean`, `unstable` and `has_hooks` go ahead. The merge itself is the REST merge through `gh api` (`PUT /pulls/{n}/merge` carrying only the merge method and the reviewed head SHA): Drydock never asks for administrator rights or a bypass and never runs `gh pr merge --admin` (a test checks the exact `gh` arguments of every merge method). The merge state is read first because GitHub can let an administrator's merge through the API past protection rules that leave administrators out, so its refusal is not relied on. A merge GitHub refuses anyway (405/409: required checks or reviews, conflicts) is also a question, never a bypass; a PR a human already merged on GitHub counts as merged. After the merge Drydock deletes the remote task branch, completes the task and removes the worktree. Auto-merge is never enabled. Not built: the first-push approval, draft PRs, a Merge button in the Pull Requests view (merging is only through the approval), fork PRs.

## 4. Workflow and task state mapping

`pr_flow` runs as a workflow on the engine ([`state-machines.md`](state-machines.md)): `agent_task` (work) -> `check` nodes -> `request` (human review) -> `check(push)` -> `check(ci)` -> loop edge to the worker on failure -> `request` (merge). Each side effect is idempotent and keyed by `(run_id, node_id, step, attempt)` (brief section 4): a repeated push is a no-op, `gh pr create` is preceded by `gh pr list --head <branch> --json number,url,state` so a resume never opens a duplicate PR. Task states: REVIEW on PR open with evidence; IN_PROGRESS when CI fails or changes are requested; WAITING_HUMAN when approval or triage is needed; BLOCKED while GitHub is unavailable or the branch is behind and the update is pending; COMPLETED only after checks pass and a human accepts.

**As built.** The graph is `prepare -> work -> verify -> finalize -> review -> push_pr -> ci -> pr_feedback -> approve -> merge`; a CI failure and a collaborator's change request loop back to `work` (bounded, then the limit question). A resumed `push_pr` reuses the PR stored on the task. Task states: IN_PROGRESS on a CI failure or a change request, COMPLETED after the merge. A GitHub problem does not move the task to BLOCKED: the run waits on a question (section 9).

## 5. The `ci` check

A tool member `CI` in the Room (zero cost, brief section 11). Input: PR ref and `headRefOid`. Poll `gh pr checks --json ...` (exit 8 means pending, not failure) or the REST check-runs endpoint with ETags (section 6); results are keyed to the head SHA so a later push resets the state.

| Result | Meaning | Action |
|---|---|---|
| `pass` | all required checks succeeded | task continues to the merge request |
| `pending` | checks still running | keep polling; show running checks |
| `fail` | at least one check failed | fetch failing logs, hand off to the worker |
| `none` | no checks reported | surface to the human; never treated as pass (as built: a question in Needs you, unless a human declared that the repository has no CI; see below) |
| `error` | `gh` or API failure | retry with backoff; `github.outage` after repeated failure |

Failure feedback: find the workflow run id (from `gh pr checks` `link`, or `gh run list` filtered by `headSha`; the URL shape is unverified), run `gh run view <id> --log-failed`, store the raw text under `<app data>/runs/<run_id>/ci/<attempt>.log`, and create a handoff artifact with a shaped excerpt (failing step names, last 200 lines per failed step, at most 16 KiB inline, rest by `fetch_range`). Log text is untrusted: build output can contain anything. The worker starts a fresh session seeded with only the excerpt. Limits: at most 3 CI-fix iterations per PR (**design**); the same failure signature with an unchanged diff hash counts as an error echo and trips the watchdog (brief section 7); then the task goes to `WAITING_HUMAN`. Flaky-test reruns are not automated.

**As built.** Task rooms have a `CI` tool member, but the `ci` node does not post to it (its results are task evidence). The node first reads which checks the base branch's protection requires: the branch view (`GET /repos/{o}/{r}/branches/{base}`, readable by anyone who can read the repository), the protection endpoint (`.../branches/{base}/protection/required_status_checks`) when the branch is protected but the view does not list them, and the repository rulesets that apply (`GET /repos/{o}/{r}/rules/branches/{base}`). The protection endpoint answers only administrators (403 or 404 for everyone else): that is not an error, the evidence notes it and every check that reports is waited for. Then the node polls the pushed head commit through `gh api` every 15 s: its check runs (`/commits/{sha}/check-runs`) and its commit statuses (`/commits/{sha}/status`, the older status API that many outside CI services use). Both count as CI. Running checks keep it waiting, a required check is waited for by name until it reports and finishes, and a rate limit makes it wait longer and try again. A check run that concluded `failure`, `timed_out`, `cancelled` or `action_required`, or a status `failure` or `error`, fails CI: its job log (`/actions/jobs/{id}/logs`; a status has none, so its description and link) is fetched, timestamps are stripped, it is cut to 6,000 bytes and handed to the worker as the next turn's feedback, without a human, and the task goes back to IN_PROGRESS.

**CI is a hard gate.** Drydock never passes a commit that no CI tested on its own. When nothing reports on the commit within 2 minutes, when a required check never reports, or when checks are still running after 30 minutes, the run waits on a question in Needs you, in plain words ("No CI ran on pull request #12 within 2 minutes. Check again, merge without CI this time, or stop."). The task page answers it: **Check CI again** waits again; **Merge without CI this time** (answer data `{"waive_ci": true}`) passes the step with `ci` evidence "CI waived by human:<name> for this commit: ..."; **Stop** ends the run. Only a `human:` identity can waive CI: an agent's waiver is refused, the next question says so, and CI is checked again. A waiver covers CI that did not report or did not finish, for that commit only, and never a failed check (CI is read once more first, and a failure still goes back to the worker). The human's merge approval still follows. The CI question is answered through its approvals row rather than delivered into the node's inbox: the `ci` node is a check node, and the watchdog's check middleware treats an answer delivered into a check node's inbox as the answer to its own pause.

**No CI, declared by a human.** The project setting **CI before merge** (Project settings, development flow; `ciMode` in `projectdev.get/set`) is `required` by default. Only a human identity can change it, and a project import sets it back to `required`. `none` is a human's statement that the repository has no CI: a commit nothing reports on then passes after the review, with `ci` evidence "CI declared absent by human:<name> on <date>: ...", and the setting keeps who declared it and when (`ciNoneBy`, `ciNoneAt`, set by the core from the caller's identity, never from the client). Checks that do report are still waited for and still fail the step, and checks still running at the deadline are still a question. Evidence `ci` records each result; events `github.ci_started`, `github.ci_passed`, `github.ci_failed`, `github.ci_needs_human`, `github.ci_waived`, `github.protection_unread` and `github.merge_blocked`. The 3-iteration cap above is not built: CI fixes are bounded by the graph's loop limit (default 5), then the limit question goes to the human. **Verified** against the fake GitHub API only; `TestRealGitHubMergeThroughGh` (`internal/git/host`, set `DRYDOCK_REAL_GITHUB_REPO=owner/name` to a throwaway repository) runs the CI wait, the merge-state read and the guarded merge against real GitHub through `gh`.

## 6. Polling with ETags from Go

`gh api` cannot be relied on for `304`, so a small Go REST client (`net/http`, TLS verified) polls conditionally with `If-None-Match`. It needs the token: Drydock obtains it with `gh auth status --json hosts --show-token --active`, holds it only in memory, never writes it to SQLite, logs, events, prompts or memory, redacts it from captured output, and re-reads it after a 401. This is a deliberate consequence of D14 and an open item for owner review (section 11). Polling covers state changes only; actions still go through `gh`.

**As built.** None of this polling exists. The app never takes the token (no `--show-token`) and has no `github_poll_state` table. `GhCLI.Do` can send `If-None-Match` through `gh api -i`, and a live read-only test saw a `304` come back (mapped to `ErrNotModified`), but no caller uses it yet. GitHub is read only when needed: the `ci` node reads the base branch's required checks once per pass and polls check runs and commit statuses (section 5), `pr_feedback` reads reviews and comments once per pass, the merge step reads the pull request's merge state just before the merge (section 3), and the Pull Requests view reads the PR, its checks and reviews when it is opened. No rate-limit budget or `github.rate_limited` event; only the `ci` node backs off on a rate limit.

| Resource (standard GitHub REST paths; **unverified** against the live API) | Poll for |
|---|---|
| `GET /repos/{o}/{r}/pulls/{n}` | state, head SHA, mergeability, `mergedAt` |
| `GET /repos/{o}/{r}/commits/{sha}/check-runs`, `GET /repos/{o}/{r}/commits/{sha}/status` | CI state (check runs and commit statuses) |
| `GET /repos/{o}/{r}/branches/{b}`, `.../branches/{b}/protection/required_status_checks`, `GET /repos/{o}/{r}/rules/branches/{b}` | the checks branch protection requires |
| `GET /repos/{o}/{r}/pulls/{n}/comments`, `.../reviews`, `GET /repos/{o}/{r}/issues/{n}/comments` | review activity (change detection only) |
| `GET /repos/{o}/{r}/issues?since=<watermark>&state=all` | issue sync (filter out PRs) |

State table `github_poll_state(resource_key PK, etag, last_modified, last_checked_at, next_due_at, interval_s)`. A `304` updates `last_checked_at` only. A `200` with a changed body triggers the detail fetch (for threads, one GraphQL call). Intervals (**design defaults**): 20 s while CI is pending on a watched PR, 60 s for an open PR awaiting review, 300 s when the project is not focused or the app is idle, and none for closed PRs; honor a server-sent poll-interval or `Retry-After` header when present. Polling pauses when the app is closed or idle and resumes on focus.

Rate limits [tool §3]: authenticated REST is 5,000 requests per hour per user (15,000 for GHEC-owned apps); unauthenticated 60 per hour; "making a conditional request does not count against your primary rate limit if a `304` response is returned and the request was made while correctly authorized with an `Authorization` header". GraphQL has a separate limit that was not read. Drydock budgets at most 25% of the hourly allowance for background polling, tracks `x-ratelimit-remaining` and reset headers when present (GitHub documents them; not verified in Phase 0), backs off to the reset time on 403/429 and emits `github.rate_limited`. The quota ledger records GitHub as `source: header`.

## 7. Review round trip

1. **Fetch** threads with GraphQL; the REST ETag poll only tells us something changed. Query (field names are the public GitHub GraphQL schema; **not verified in Phase 0**, confirm by introspection in Slice 1E):

```
gh api graphql -f query='query($o:String!,$r:String!,$n:Int!,$after:String){ repository(owner:$o,name:$r){ pullRequest(number:$n){
  reviewThreads(first:50, after:$after){ pageInfo{hasNextPage endCursor} nodes{ id isResolved isOutdated path line
    comments(first:20){ nodes{ id author{login} body createdAt url } } } } } } }' -F o=OWNER -F r=REPO -F n=NUMBER
```

2. **Normalize** to `review_items` (thread id, path, line, outdated flag, author login and `authorAssociation`, bot flag, comment text, URL). All comment text is untrusted and wrapped/marked wherever it reaches a prompt; it is never executed and can never change permissions, answer approvals or select a model (brief section 16).
3. **Triage by a human**: the Pull Requests view lists unresolved threads; the human selects which to address (a stored rule may pre-select threads from named reviewers, but still creates only BACKLOG/READY work). Selecting creates a worker task whose context contains the quoted comment as data, the file path and line anchor, and the current diff range.
4. **Change and push** through the same `pr_flow` pipeline; a new head SHA resets `ci`.
5. **Reply and resolve** are outbound writes: drafts are shown, and posting a reply or running the `resolveReviewThread` mutation (name from the GitHub GraphQL API; not found in the `gh` source [tool §3]) needs an approval bound to the exact payload hash. Outdated threads are flagged rather than auto-resolved.
6. **Review decisions**: `reviewDecision` and `latestReviews` from `gh pr view --json` drive "changes requested" (task back to IN_PROGRESS) and "approved" states. Drydock never submits a review on a user's behalf without approval.

**As built (`PRFeedback` and `replyAddressed` in `prflow.go`).** No GraphQL thread fetch and no human triage step. After CI passes, `pr_feedback` reads the PR's reviews, inline review comments and issue comments over REST. A `CHANGES_REQUESTED` review or a new top-level inline comment from an `OWNER`, `MEMBER` or `COLLABORATOR` becomes rework feedback at once: quoted as data, the task goes back to IN_PROGRESS and the loop returns to `work`. Text from anyone else (outside contributors, bots) is stored as `pr_comments` evidence for the human to read and is never acted on. Comments Drydock posted carry a hidden `<!-- drydock -->` marker and are skipped. On the next push Drydock **automatically** replies on each inline comment that caused the rework: "Addressed in `<commit>` (Drydock re-ran the checks; the independent reviewer will look again). Resolving this thread stays with you." These replies are not approval-gated. Threads are never resolved by Drydock (`ResolveThread` is not called) and no review is submitted. A PR with no actionable comment goes on to the human's "Review and merge" request.

## 8. Issue import

First import and manual refresh use `gh issue list --json number,title,body,state,stateReason,labels,assignees,milestone,updatedAt,url,closedByPullRequestsReferences,parent,subIssues,issueType` with filters (state, labels, assignee, milestone; flag names beyond `--json` unverified here). Rows land in `external_items` (`integration_id = github`, `external_id = <owner/repo>#<n>`; schema in [`integrations.md`](integrations.md) section 3) with `untrusted = 1`; periodic sync uses the ETag poll above with a `since` watermark. An issue becomes a task only through a human action ("Create task") or a stored rule; the task gets BACKLOG status and a copy of title and body inside an "External source (untrusted)" block. `parent`, `subIssues` and `blockedBy` fields map to task dependencies when present. Linking: generated PR bodies include `Closes #n`; `closedByPullRequestsReferences` is read back to show the link.

**As built (`internal/core/github.go`).** The Pull Requests view lists the repository's open issues on request (`github.issues`, `GET /repos/{o}/{r}/issues`, pull requests left out, optional label filter) and "Import" creates a BACKLOG task (`github.importIssue`) whose description quotes the issue as data with its author. Since decision P37 the issue's title and body get the same cleaning as a connection's items (`integrations.Sanitizer.OutsideTask`: invisible and control characters removed, secrets redacted by the app's scanner, the body cut to 4,000 characters, a title that reads like instructions withheld, warnings above the quote) and an issue scoring at or over the injection threshold is refused by the core; before, the text was only prefixed with `> `. An imported task stays in BACKLOG until a human moves it on, also with automatic scheduling on; the task's issue ref puts `Closes #n` in the PR body. No `external_items` rows, no periodic sync, no stored rules, no dependency mapping and no read-back of the closing link.

## 9. Errors and recovery (brief section 17)

| Condition | Behavior |
|---|---|
| `gh` missing or below tested version | Connection `not_installed` / `unsupported_version`; `pr_flow` disabled, `local_merge` unaffected. |
| Not authenticated, token revoked, 401 | `unauthenticated`; open embedded terminal for `gh auth login`; pending GitHub steps pause as BLOCKED, never fail the task; work stays in the worktree. |
| Missing scope, SSO not authorized | `degraded` with the scope or SSO hint from stderr. |
| Network failure, GitHub outage (5xx) | Exponential backoff with jitter, `github.outage` event after the third failure, task BLOCKED, resume on recovery; no local work is lost. |
| Rate limited (403/429, remaining 0) | Wait until reset, reduce polling, `github.rate_limited`. |
| Push rejected (non-fast-forward, branch protection, hook) | Surface stderr to the human; never force-push; a force push is a separate approval. |
| PR already exists for the branch | Adopt it (idempotent create via `gh pr list --head`). |
| Merge conflicts / `mergeable` conflicting / `mergeStateStatus` behind | Task step to update the branch (merge target into it); `gh pr update-branch` exists but is human-initiated; unresolved conflicts go to `WAITING_HUMAN` with the conflict list. As built: the merge step reads the merge state first and asks the human (section 3); no task step updates the branch. |
| CI failure | Section 5. CI never configured: `none`, a question for the human unless a human declared that the repository has no CI. |
| Review requests changes | Section 7; task to IN_PROGRESS after triage. |
| Auto-merge enabled by someone else | Warning banner; Drydock does not toggle it. |
| JSON field removed or renamed after a `gh` upgrade | `github.schema_drift`, Connection `degraded`, fall back to `gh api` for the missing data. |
| Defender quarantines `gh.exe` | `not_installed` with the reason; guidance only. |
| Dirty worktree at cleanup | Never deleted without approval (brief section 13). |

**As built.** A GitHub or network failure while pushing, opening, reading or merging the PR (`gh` signed out, an outage, a refused push) does not fail the run: it becomes a question in Needs you ("Could not <step> on GitHub: <error>. Fix the cause (for example run `gh auth login`), then answer to try again."). Answering runs the same step again and nothing else is redone; rejecting stops the run. The task keeps its state (no BLOCKED), and the Tasks page lists it under "Waiting on you". There is no automatic backoff or `github.outage` event. An error reading CI other than a rate limit, or reading the PR's reviews, still ends the step with an error. CI that gives no answer and a pull request GitHub would not merge (branch protection, behind, conflicts, a draft) are questions too, never a pass and never a bypass (sections 3 and 5). The PR-already-exists case is handled by reusing the PR stored on the task, not by `gh pr list --head`.

## 10. Security and events

Outbound GitHub writes that need approval: replies, thread resolution, review submission, repository creation, force push, branch deletion, release creation (also human-initiated), and any MCP write tool. The `secrets` check (gitleaks) runs before every push. PR descriptions come from immutable minutes and are scanned. A worktree is not a sandbox; the agent can push with the user's git credentials if it runs `git push`, so the command policy ([`security.md`](security.md)) restricts `git push` and `gh` calls from agent processes to Drydock-mediated actions. Events: `github.pr.opened`, `github.pr.updated`, `github.ci.updated`, `github.review.received`, `github.thread.resolved`, `github.merged`, `github.rate_limited`, `github.outage`, `github.schema_drift`, `github.poll.error`.

## 11. Open questions / unverified

- No `gh` command was run against GitHub in Phase 0 (github.com and api.github.com were unreachable, no login): the `gh auth status --json hosts` output shape and `state` values, Windows credential storage, `bucket` values, `gh pr create` output, run-id extraction from check links, and `If-None-Match` through `gh api` are all unverified (phase0 section 7, item 10).
- The GraphQL query, the `resolveReviewThread` mutation and the REST paths above come from the public GitHub API, not from Phase 0 evidence; the GraphQL rate limit was not read; secondary rate limits are not covered.
- Obtaining the token with `--show-token` for Go polling puts a GitHub credential in process memory. This follows D14 but should get an explicit owner nod; the alternative is `gh api` with `-i` and `-H If-None-Match` if a networked test proves 304 passthrough, or polling without conditional requests at a higher rate cost.
- Whether `git push` authenticates via the user's git credential helper or needs `gh auth setup-git` is unverified; GHES hosts and fork PRs are out of v1 scope.
- Polling intervals, the 25% budget, the 3-iteration CI-fix cap and the 16 KiB log excerpt are design defaults to be tuned with measurements. Contradiction note: brief section 13 says "human-initiated merge via GitHub"; this document allows the human to trigger `gh pr merge` from Drydock, which is still a human-initiated merge executed through GitHub, but the owner may prefer merge on github.com only.

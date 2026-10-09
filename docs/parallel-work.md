# Parallel work: keeping agents from colliding

Status: implemented and tested on Linux with scripted agents: Go tests in `internal/pipeline`, `internal/git`, `internal/tasks` and
`internal/core`, and a Playwright test through the real core (`ui/e2e/round8.spec.ts`). Not run on Windows. Not tested: a real
agent (no scripted one) handed conflict markers, so how well a real worker resolves a conflict is unknown; the mechanism around it
is tested (section 6).

The question this answers: when several agents work on one project at the same time, what stops one from overwriting or breaking
another's work, and what happens when two of them need the same code?

## 1. Layers, from the one that cannot fail to the ones that need thought

1. **Separate worktrees (isolation).** Every task works in its own git worktree on its own branch. Two agents cannot overwrite
   each other's files while they work, whatever they do. Nothing reaches the main branch except through a human-approved merge.
2. **Collision guard (waiting).** If an earlier, still active task changes (or is planned to change) the same code, the later
   task waits instead of racing it. This is the "if they are going to cross paths, wait for the other agent to finish" rule.
3. **Sync with the base (integration).** Before a task is reviewed, and again before it is merged, the base branch is brought into
   the task's branch and the checks run again. Two changes that each passed alone can still break each other; this is where that
   is found, on the task's own branch, and not on main.
4. **Serialized, stale-proof merge.** Merges into one repository run one at a time, and a merge is refused when the branch is
   behind its target (the task goes back to sync).
5. **Human approval.** Every task ends at a human approval. The approval is remembered as evidence (by patch id), so a change the
   human approved that merges unchanged after a clean sync is not asked about again; any change to the change asks again.

Layers 1, 3, 4 and 5 are guarantees. Layer 2 is an optimization that saves work and tokens and puts the order in front of the
human; if it misses a collision (see 3) layers 3 and 4 still catch it.

## 2. Footprint and the guard

A task's **footprint** is the set of paths it is planned to touch (`Task.Files`: set by the planner or the human, replaced by the
files the task really changed once it has finished a pass) together with what its worktree has changed so far against the
merge base (`git.Repo.ChangedPaths`: committed, uncommitted and untracked; a rename counts as old and new path). Entries can be
files, directories (`internal/auth/`) or globs (`internal/auth/**`, `*.go`).

A task **waits** while an earlier task that is still active has an overlapping footprint. *Earlier* means *started first*
(first transition to IN_PROGRESS, ties by sequence number), so the order is total and two tasks can never wait for each other.
*Active* means CLAIMED, IN_PROGRESS, REVIEW, VERIFICATION, WAITING_HUMAN or BLOCKED: a task parked for the human's approval still
holds its place until it is merged, cancelled or failed.

Where it acts:

- **Scheduler admission.** A READY task whose planned `Files` overlap a running task's is not started; the skip reason
  (`scheduler.skipped`) names the task and the paths. It starts when the other is merged, cancelled or failed. This is the only
  point where waiting avoids doing the work twice, so the planner is told to give parallel tasks different files.
- **The `guard` node** (after `work`, before the checks, in every graph that merges). It compares the footprint, now including
  what the worker really changed, with earlier tasks. On overlap the task becomes BLOCKED with the reason
  (`waiting for "Variant A" (both change calc.go) to finish`), `task.waiting_on` is published and evidence of kind `guard` is
  recorded. No agent runs while it waits and it holds no agent slot. When the earlier task is gone it goes back to IN_PROGRESS
  (`task.guard_released`) and continues; its branch then gets the earlier task's work through the sync step.
- **Release.** A human can let a waiting task go on (`task.guard.release`, button "Let it go on now" on the task). Only a
  `human:` identity can. The merge is still synced, re-checked and approved.
- **Switch.** `collisionGuard` in the project's development settings; on unless the human turns it off.

## 3. What the guard cannot know (stated, not hidden)

- Two tasks that start in the same instant race to be "earlier" (the order is the moment each first went IN_PROGRESS), so which of them waits is decided by timing. Only the order is arbitrary; one of them still waits for the other. The tests start the tasks one after the other for that reason.
- It cannot see the future. Two tasks that both start without planned files and write to the same file at about the same time are
  not detected until the later one reaches the guard and the earlier one has already written. The task that finishes later then
  absorbs the integration through sync (layer 3), which is correct, only less efficient.
- A footprint is paths, not meaning. Two tasks can change different files that break each other (an API and its caller); only the
  checks after sync find that. That is why checks run again on the merged result and not only on each branch.
- Observing a worktree that an agent is writing to is a snapshot (`git add -A` into a temporary index, never the real one).

## 4. Sync (`internal/pipeline/sync.go`)

Runs after `finalize` (the branch is committed and the checks passed) and again when merge finds the branch behind.

| Situation | Result |
|---|---|
| Branch already contains the base | on to review, or straight to merge if the change is the one a human approved |
| Base brought in cleanly | task reopened (without using an iteration), **checks run again** on the result, then review |
| Base conflicts | merge left in progress in the worktree with markers; the task's own worker is told which files and to keep what BOTH changes were for; `Verify` fails (`merge-conflicts`) while markers remain |
| Branch behind when merging | back to sync; never merged unchecked |

Reopening for a sync passes `TransitionOpts.Resync`: it needs no review feedback and does not spend one of the task's iterations
(nothing was wrong with the work). Only the system can do it; the graph's loop limits bound how often it can repeat.

## 5. Correctness fixes found on the way

- Diffs, diffstats and file lists compare with the **merge base**, not the moving tip of the target branch. Before, once any other
  task had merged, a later task's review showed the other task's changes as reversals of its own.
- `Merge` takes a per-repository lock, so two merges at once cannot interleave their git operations.

## 6. Tested

`internal/git`: diff against the merge base, `MergeIn` clean and conflicting, merges serialized. `internal/pipeline`: four tasks
under the scheduler with a limit of 2 where the fourth collides with the third (the fourth is BLOCKED naming the task and the
file, is released by the merge, gets the base, its worker is handed the conflict, and it comes back for approval; it is never forced
into main); a task approved before the base moved merges after a clean sync without asking again and without another worker turn;
the human release (and the refusal of an agent's); the switch; scheduler admission by planned files; footprint/overlap/ordering unit
tests (`internal/pipeline/scheduler_test.go`, `sync_test.go`, `guard_internal_test.go`). `internal/tasks`: the resync transition.
`internal/core`: the setting and the release RPC (`ext_guard_test.go`). UI: the toggle and the release button; the browser test
`ui/e2e/round8.spec.ts` drives two tasks that change the same file through the real core: the second waits, names the first task
and the file, and goes on after "Let it go on now", while the first is left alone. All of these use the scripted worker.

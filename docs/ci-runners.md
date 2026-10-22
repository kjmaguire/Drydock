# Drydock: CI runners on your PC (decision P56)

Status (2026-10-15): built and tested on Linux with labelled test doubles. CI checks the real runner release on every push
(downloaded, SHA-256 checked, unpacked on Windows, built into the Linux image, started; nothing registered). **Not yet run on
a PC against a real private repository** (see "Not verified").

## 1. What it is for

Drydock's pull-request flow already loops on CI (`docs/github.md` section 5): a task pushes its branch, waits for the checks
on that commit, gives a failing job's log to the worker, which fixes the code, and Drydock pushes again (at most 5 rounds,
then a question; the merge is always a person's). That loop reads GitHub's check results, so it does not care where CI runs.

The CI runners feature is only about *where* CI runs: on the person's PC instead of GitHub's machines, for their private
repositories. Drydock builds apps of any kind, so it offers both kinds of runner a Windows PC can host and lets each
workflow pick with `runs-on`:

| Kind | Where a job runs | A workflow asks for it with |
|---|---|---|
| Windows | On the PC, as the person, in a fresh folder for each job: on the first Dev Drive (`X:\DrydockRunners`) when there is one, else in Drydock's data folder, or a folder the person chooses (with the window's folder dialog, or typed) | `runs-on: [self-hosted, Windows, drydock]` |
| Linux | In a fresh container for each job on the container engine (Docker Desktop, which runs on WSL 2) | `runs-on: [self-hosted, Linux, drydock]` |

macOS jobs cannot run on a Windows PC; they stay on GitHub's hosted runners.

## 2. Rules

- **Private repositories only.** On a public repository anyone's fork could open a pull request whose workflow runs its own
  code on the PC (GitHub's own advice). Drydock reads the repository (`GET /repos/{owner}/{repo}`) and refuses anything that is
  not `private` (public and internal alike), when the runner is added, again before every registration, and every 3 minutes while
  a runner waits for a job: a repository made public later gets no new runner, and the one that waits is stopped, its registration
  deleted and its files removed at the next look (a job already running finishes; the runner is ephemeral and is not registered again). Archived repositories and ones the signed-in account does not administer are refused too.
- **Runners only run CI.** They never push or change code; Drydock's own pipeline reads the results and does the fixing, the
  local checks, the review and the pushing. A runner holds no credential that can write to the repository.
- **A person decides.** Adding, removing, switching on or off and choosing the folder are a person's (human identity only).
  A Windows runner needs the person to accept, per repository, that its jobs run on the PC as them with their files and
  network: Windows has no cheap container per job. Linux jobs get one. The same goes for a workflow an agent adds to a task
  branch: once the branch is pushed it runs on the runner. Drydock asks a person before it pushes a branch that changes
  CI definitions (`.github/workflows/`, `.github/actions/`, and other CI services' files; docs/github.md section 3), naming the
  paths; a script a workflow already calls is not watched, so a Windows runner belongs to a repository whose workflows and
  tests you trust, and a Linux (container) runner is the one for work an agent has shaped.
- **No stored GitHub token.** Registration goes through `gh` as the signed-in account (`host.GhCLI`), like the rest of
  Drydock's GitHub calls.
- **At most 8 runners.**

## 3. How a runner lives

Each runner is **ephemeral and registered just in time** (`internal/runners/pool.go`):

1. Check the repository (private, administered), the kind (a Linux runner needs a container engine that answers) and the
   free space where Windows jobs write (a job waits while less than 10 GB is free; the page shows why).
2. Get the newest runner release (`GET /repos/actions/runner/releases/latest`, kept for an hour). Only the plain archive of
   that version, served from `https://github.com/actions/runner/releases/download/`, and only when the release notes carry its
   SHA-256 between `<!-- BEGIN SHA … -->` markers, is used. The download is checked against that SHA-256 and deleted when it
   does not match (`fetch.go`). A Windows zip is unpacked once (entries that leave the folder, absolute paths and links are
   refused); the Linux archive is never unpacked on the PC: the image is built from a two-file context (the Dockerfile and
   the checked archive, which `ADD` unpacks inside Linux). Old versions are deleted.
3. Ask GitHub for a one-job registration (`POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig`), named
   `drydock-<pc>-<kind>-<random>`, labels `self-hosted`, `Windows` or `Linux`, `X64` or `ARM64`, `drydock`.
4. Start the runner with that configuration **in its environment** (`ACTIONS_RUNNER_INPUT_JITCONFIG`, which the runner reads
   as its `--jitconfig` option and removes before a job starts), never on a command line, in a file, the database, a log or
   an event. The rest of its environment is the scrubbed one every agent process gets (no app variables, no other tools'
   tokens; the container engine client also keeps its `DOCKER_*` settings).
   - **Windows**: a fresh copy of the runner in `<folder>\jobs\<runner name>`, started with `run.cmd` inside a Windows job
     object, so everything the job starts ends with it.
   - **Linux**: `docker run --rm --init` of the image `drydock-runner:<version>` (Ubuntu 26.04 LTS, git, curl, jq, the runner's own
     dependencies; jobs run as user 1001 with sudo inside the container), limits of 4 processors, 8 GB and 4,096 processes, no
     mount of the PC's drives and no engine socket, so a job cannot start containers beside it or reach Windows' files.
5. The runner waits, takes one job and exits. Its output is read for "Running job: …" and "Job … completed with result: …"
   (the page shows the job and the last result) and kept (the last 200 lines, "Output" on the page).
6. Afterwards: anything still running is stopped, the job's folder or container is deleted, the registration is deleted, and
   a new one is made for the next job. A runner that stops without a job is a failure (its last output line is the reason)
   and the next try waits 10 s, then 20 s, up to 10 minutes.

**The folder for Windows jobs and Dev Drives.** Drydock looks for Dev Drives once at start, without administrator rights:
for every fixed drive formatted with ReFS (a Dev Drive always is) it opens the volume (`\\.\X:`, no access right asked) and
sends `FSCTL_QUERY_PERSISTENT_VOLUME_STATE` (ntifs.h), whose answer carries the `PERSISTENT_VOLUME_STATE_DEV_VOLUME` and
`PERSISTENT_VOLUME_STATE_TRUSTED_VOLUME` bits (Windows 11 22H2 with the September 2023 update and later). Only where that
query fails (an older Windows, a server SKU that rejects the mask) does it fall back to `fsutil devdrv query X:`, which needs
administrator rights and so usually answers an unelevated Drydock with an error. When no Dev Drive is found, the page says why
after "No Dev Drive was found:", in the core's words: "no fixed drive on this PC uses ReFS, and a Dev Drive always does";
"D: uses ReFS but Windows does not mark it as a Dev Drive" (or "D: and E: use ReFS but Windows does not mark them as Dev
Drives"); "the Dev Drive check is not available on this Windows version (the volume state query for D: failed: <error>), and
fsutil devdrv query, which needs administrator rights, said "<its first line>"" (or "said nothing"); "Windows did not list this
PC's drives: <error>". The same answer feeds the new project's folder hint (`fs.folderHints`, decision P58). The folder itself
is set on the GitHub page: **Choose folder…** opens the window's folder dialog and saves the choice at once (it stays in
the box when the save is refused, for example while a Windows runner is on), or a path is typed or pasted and saved with
**Use this folder**; **Back to automatic** returns to the Dev Drive or Drydock's folder.

Switching a runner off or removing it stops it and deletes its registration. When Drydock stops, every runner is stopped and
unregistered. When it starts, it removes what an earlier run left (job folders, runner containers) and this PC's runners that
GitHub still lists as offline for the repository (only names starting `drydock-<this pc>-`).

**Disk.** Because every job starts from a fresh copy and its folder or container is deleted, `_work`, the tool cache
(`_work/_tool`), checkouts and caches never pile up, and jobs have no access to Docker, so they leave no images, volumes or
build cache behind. Drydock keeps one runner archive and one runner image. What it does not manage: Docker Desktop's own WSL
disk file grows and does not shrink by itself (`wsl --manage docker-desktop-data --set-sparse true`, or Docker Desktop's
"Clean / Purge data"), and anything else on the engine.

## 4. Where it is

- `internal/runners`: `release.go` (release and SHA-256 markers), `fetch.go` (download, check, unzip, copy), `github.go`
  (repository check, just-in-time registration, removal, listing), `launch.go` (the Windows and container launchers, the
  Dockerfile), `pool.go` (the loop above), `disk_*.go` (free space; Dev Drives from each ReFS volume's persistent state
  flags, `fsutil devdrv query` only as the fallback, and the reason when none is found).
- `internal/core/ext_runners.go`: `runners.get`, `runners.add {repo, kind, acceptRunsAsYou}`, `runners.remove`,
  `runners.enable {enabled}`, `runners.root.set {path}` (refused while a Windows runner is on), `runners.log`. Settings
  `runners:slots` and `runners:root`. Event `runners.changed` (key, state, job, count; never a configuration).
- UI: GitHub page (`/github`), "CI runners" (`ui/app/screens/github/ci-runners.tsx`; on the Connections page until 2026-10-22).
- Tests: `internal/runners` (release parsing, checksum refusal, unzip refusals, the Windows launcher with a stand-in runner
  program, the container launcher with a stand-in engine client, the pool with the in-memory GitHub and launcher of
  `runnerstest`, the Dev Drive decision and its reasons in `disk_test.go`, and on Windows `disk_windows_test.go`, which runs
  the volume state query against C: on CI's Windows runner: C: is NTFS, so the answer must be "not a Dev Drive" whether the
  query answers or the fsutil fallback runs, never a panic), `internal/core/ext_runners_test.go` (the reason reaches
  `runners.get` and `fs.folderHints` through the `DevDrives` test double), the browser tests and `ui/e2e/runners.spec.ts` (the
  harness gives the core the same test doubles). `TestRealRunnerRelease` (opt-in, `DRYDOCK_REAL_RUNNER_RELEASE=1`, run by CI on Windows and on
  the Linux Docker job) downloads the real newest release, checks it, unpacks it or builds the image, runs
  `Runner.Listener --version`, and starts the runner with a configuration that is not valid to show it reads it from the
  environment. It never registers a runner.

## 5. Not verified

- A real job on a real private repository, on a PC: the registration call, the labels GitHub adds itself to a just-in-time
  runner, the runner taking a job, its self-update when a new version comes out mid-job, and the deletion afterwards.
  Everything else up to "start the runner" is checked by CI on GitHub's Windows and Linux machines.
- Docker Desktop on Windows (the Linux image is built and run on Docker on Linux in CI), and the volume state query against a
  real Dev Drive (CI's Windows runner has none: the query is exercised against C:, which is not one; `fsutil devdrv query`'s
  wording is parsed from Microsoft's documented messages). A Dev Drive that is not recognised only means jobs run in
  Drydock's data folder, and the page says why it was not.
- Jobs that need Docker themselves (`container:` jobs, service containers, `docker build` steps) do not work on the Linux
  runner by design (no engine socket), and on the Windows runner only if Docker is installed and the person accepts that.

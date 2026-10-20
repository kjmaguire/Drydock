# Drydock: the per-task sandbox

Status (2026-10-10): built and tested on Linux with a real container engine (Docker 29.8) and real Claude Code 2.1.296. **Not
run on Windows yet** (decision P2): Docker Desktop's bind mounts, its host gateway and the Windows path handling are unverified.

## 1. What it is

Without the sandbox a Claude Code worker cannot run commands at all, unless the project lets it run the exact check commands
(decision P24), and then those commands run the project's own code on the human's machine with the human's rights. Neither
fits building a real application: a worker needs to install packages, run migrations against a database, start the app and run
its tests, and none of that should touch the human's machine.

With the sandbox on, every task gets its own containers on the machine's container engine:

- a **workspace** container with the task's worktree mounted at `/workspace`, where the agent may do anything (it runs as the
  image's user, root by default);
- the **services** the project lists (a database, a cache), on a private network, reachable by name (`db`, `redis`);
- by default a small **egress proxy** container, the only way out of the private network, which lets through only the hosts on
  the project's allowlist.

The worker's CLI still runs on the human's machine, but in Claude Code's restricted mode (`--restricted`): no tool that runs
commands or code, file tools confined to the working folder, writes to `.claude/` settings and `.git/` refused. It runs commands
only through five tools of Drydock's MCP gateway, which run them in the workspace container. The project's checks (build, test,
lint, typecheck, and the test-first templates' red and green checks) run in the container too, so the project's code never
runs on the human's machine while the sandbox is on. When the task completes, fails or is cancelled, its containers, networks
and volumes are removed.

## 2. Turning it on

1. Install a Docker-compatible container engine (Docker Desktop, or Rancher Desktop with dockerd) and use Linux containers.
   Drydock talks to it only through the `docker` command (`DRYDOCK_CONTAINER_CLI` names another compatible client; Podman is
   untested).
2. Commit a definition at `.drydock/sandbox.json` on the project's **default branch** (section 3). Drydock reads it from that
   branch only, never from a task worktree, so a change an agent makes to it takes effect only after a human merged it. A
   merged change still does not run until you confirm it: switching the sandbox on records the definition you saw, and a task
   whose definition differs is held in Needs you with the list of what changed (network, allowed hosts, image, setup commands,
   services). Confirm it in the project's sandbox settings (`sandbox.trust`, which takes the digest of the definition you were
   shown and is recorded in the audit log) or switch the sandbox off and on again, then answer to try again. A sandbox that was
   switched on before this existed is asked once.
   Project settings > Sandbox suggests a starting point from the project's files.
3. Project settings > Sandbox: check that the engine and the definition are found, press **Test the sandbox** (it starts the
   sandbox once without the setup commands, runs a command and removes it), then switch on **Run the agents' commands in a
   container for each task**. Only a human identity may switch it (`sandbox.set`, refused for an agent); the switch records
   who and when. It is a setting of its own (`sandbox:<project id>`), not part of the development settings, and a project
   import does not carry it: the other machine may have no container engine.

## 3. The definition (`.drydock/sandbox.json`)

```json
{
  "image": "node:24-trixie",
  "setup": ["npm ci"],
  "env": { "DATABASE_URL": "postgres://app:app@db:5432/app" },
  "services": {
    "db": { "image": "postgres:17", "env": { "POSTGRES_USER": "app", "POSTGRES_PASSWORD": "app" } }
  },
  "network": "allowlist",
  "allowHosts": ["api.stripe.com"],
  "volumes": ["/workspace/node_modules"]
}
```

| Field | Meaning | Default |
|---|---|---|
| `image` | The workspace image. Required. | none |
| `setup` | Commands run once, in order, when the sandbox starts (`sh -c`, in `/workspace`). The first failure stops the rest; the agent sees what failed and can fix it. | none |
| `env` | Set for every command. Visible to the agent: never put secrets here. `HTTP(S)_PROXY`, `NO_PROXY` and `DRYDOCK_*` are Drydock's. | none |
| `services` | Name to `{image, env, command}`; at most 8. Reached by name from the workspace. Not reachable from outside the task. | none |
| `network` | `allowlist` (only listed hosts, through the proxy), `open` (ordinary internet access, no proxy), `none` (no internet; services still reachable). | `allowlist` |
| `allowHosts` | Host names (`api.stripe.com`), domains (`*.example.com`: every name under it) or addresses, added to the defaults. | none |
| `noDefaultHosts` | `true` starts the allowlist from nothing instead of the defaults. | `false` |
| `volumes` | Container paths kept on a volume of their own for the task, for example `/workspace/node_modules`: fast Linux files, and no Linux binaries in the worktree. | none |
| `user` | A user name or `uid[:gid]` for the commands; a volume is handed to that user. | the image's user |
| `cpus`, `memory`, `pids` | Limits of the workspace container. | `2`, `4g`, `2048` |

Unknown fields are refused, so a typo is an error rather than a silent default. The default allowlist covers the package
registries most projects need: npm, Yarn and Node headers, Go modules, PyPI, crates.io, Debian, Ubuntu and Alpine packages,
GitHub downloads (read-only: no credentials are ever put in the sandbox) and Playwright's browser downloads
(`sandbox.DefaultAllowHosts`).

## 4. A task with the sandbox on

- **Before a worker turn** (the plain work step, the template implementation step, and the template roles that write, such as
  the test writer) Drydock makes sure the task's sandbox runs: it pulls missing images, creates the network, starts the proxy and
  the services, starts the workspace and runs the setup commands. That can take minutes the first time. If it fails (no engine,
  a bad definition, an image that cannot be pulled), the task waits in Needs you with the reason; answering tries again.
- **The worker turn** starts Claude Code with `--restricted` and `--permission-mode acceptEdits` (restricted mode refuses
  `bypassPermissions`), Drydock's own instructions plus the project's CLAUDE.md (restricted mode does not load it; contract
  CLAUDE-RESTRICTED-02), a description of the sandbox, and these gateway tools:
  - `sandbox_run {command, dir?, timeout_seconds?}`: runs `sh -c` in the workspace and waits; returns the exit code and the
    output, its first 8 KB and last 24 KB when it is long (the whole output is kept in `<app data>/sandbox-logs/` for the human).
    Default timeout 10 minutes, at most 60; past it the command is stopped with everything it started.
  - `sandbox_start {name, command}`, `sandbox_logs {name}`, `sandbox_stop {name}`: background processes such as a dev server.
  - `sandbox_info`: the image, the services, what the network allows and what it refused recently, and how setup went.
  Each command is recorded as a `sandbox.command` event on the task's timeline (secrets in the command line redacted).
- **The CLI waits for long commands.** Claude Code may move a tool call that runs longer than 120 seconds to the background and
  end the turn without its result (observed with 2.1.296 when `CLAUDE_AUTO_BACKGROUND_TASKS` was on). A sandboxed worker
  therefore starts with `CLAUDE_AUTO_BACKGROUND_TASKS=false`, `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and
  `MCP_TOOL_TIMEOUT=3900000`; measured: a 130-second command's result reached the model in the same turn.
- **Checks** run in the workspace with `docker exec`, the check's own command line quoted for `sh`, with `CI=1`; the evidence
  records the engine command, so a check that ran on the human's machine would show. The in-process checks (the secret scan of
  the diff and the dependency audit) read files and stay on the machine.
- **The e2e check and the preview** would run the project's code on the human's machine, so in a sandboxed project they do not
  run there: the template step records `e2e skipped` with the reason, and the on-demand buttons refuse. The worker runs
  end-to-end tests in the sandbox itself.
- **Reviewers, room replies, planners and the team lead** are read-only and do not get the sandbox.
- **When the task ends** its sandbox is removed. At start Drydock removes sandboxes left by tasks that ended or no longer exist
  (only when some project uses the sandbox, so a machine without an engine is never asked).

## 5. The network allowlist

The workspace and the services sit on an internal network with no route out. The egress proxy (`cmd/drydock-egress`, about
7 MB, built from this repository) is the only container attached to both that network and a network of its own with a route
out. Tools in the workspace find it through `HTTP_PROXY`/`HTTPS_PROXY`; a tool that ignores those variables has no way out at
all. The proxy:

- allows a host name, a domain (`*.example.com`) or an address on the list, on ports 80 and 443 only;
- resolves names itself and connects to the address it checked, and refuses a name that resolves only to a private, loopback,
  link-local, carrier-grade-NAT or reserved address, or an IPv6 address that embeds one (NAT64, 6to4) (the human's machine, the
  engine's host gateway, a cloud metadata service) unless that address is itself on the list (the same list as Drydock's own
  outgoing connections: `internal/netguard`);
- closes a tunnel that has carried nothing in either direction for five minutes, and limits the size of a request's headers;
- logs one JSON line per decision; refused requests appear in `sandbox_info` and the agent is told to ask the human to add a
  host.

The proxy image is made locally with `docker import` from the binary that ships next to `drydock.exe`
(`drydock-egress-linux-amd64` and `-arm64`, picked by the engine's architecture; `DRYDOCK_EGRESS_BIN` names another file); no
registry is involved. `task build:egress` builds both into `bin/`, and `task build:windows` builds `drydock.exe` next to them. Without
that file the allowlist cannot start, and the project settings say so; `network: open` or `none` work without it.

## 6. What it protects, and what it does not

Protected: the human's files outside the task's worktree, the human's credentials (the Claude Code login stays with the CLI on
the host; nothing else is mounted), the human's machine's network services, the internet beyond the allowlist, other tasks
(each has its own containers and networks), and the human's machine from the project's code (it never runs there).

The workspace and every service run with no new privileges and only the Linux capabilities development tools use (CHOWN,
DAC_OVERRIDE, FOWNER, FSETID, KILL, SETGID, SETUID, SETFCAP, NET_BIND_SERVICE: Docker's default set without raw sockets, device
nodes, chroot, audit writes and changing the capability set), and with the project's CPU, memory (swap included) and process
limits; a service gets the workspace's limits (audit 2026-10-16, L7). Not verified here: the same flags against a real engine
(no container engine runs in the development environment; the sandbox tests that start containers run in CI).

Not protected:

- The container is not a security boundary against the container engine itself: an engine or kernel vulnerability could let
  code out. On Windows, Docker Desktop runs the containers in a Linux virtual machine, which adds a boundary; that is unverified
  here.
- The worktree is mounted read-write: the agent can change any file in it, as without the sandbox. Every change still goes
  through the checks, the reviewer and the human's approval.
- An allowlisted host receives whatever the agent sends it. The defaults are registries and GitHub, which need credentials to
  publish; a project that adds its own hosts accepts that.
- `network: open` gives the workspace ordinary internet access.

## 7. Limits and not built

- Only Claude Code agents can work in a sandboxed project. Another CLI's turn becomes a question for the human instead of
  running commands on the machine (its ACP terminal requests are not routed into the container yet).
- No port forwarding from the sandbox to the human's machine, so the human cannot open the app a worker started in the sandbox.
- On Windows: a worktree checked out with CRLF line endings breaks shell scripts in a Linux container (`.gitattributes` with
  `* text=auto eol=lf` avoids it); bind mounts of Windows folders are slow for many small files (use `volumes` for
  `node_modules`). Both unverified.
- `git` does not work inside the container: the task's worktree is a linked Git worktree whose `.git` file names the main
  repository's folder on the human's machine, which is not mounted. Drydock makes the commits; the agent sees its changes with
  the file tools. Measured: `go build` of a main package still works (Go 1.27 skips version stamping when Git cannot answer).
- The proxy does not let through other ports (SSH Git remotes, databases on the internet).
- Podman and Rancher Desktop's containerd mode are untested.

## 8. Tested

- `internal/sandbox`: definition parsing and validation, argument quoting, output shortening; against a real engine with images
  built offline from this machine's programs (`internal/sandbox/sandboxtest`): commands in the worktree, environment, setup and
  its first failure, time limits that stop the whole process group, interruption, long output, services by name, background
  processes, the allowlist (an allowed host through the proxy, a refused host, no route without the proxy), reuse and
  replacement after a definition change, cleanup and pruning, a non-root user with a volume, a project check through the checks
  runner.
- `internal/sandbox/egress`: allowlist matching, names resolving to private addresses, CONNECT and plain HTTP, ports, logging.
- `internal/pipeline`: worker turns, the question when the sandbox cannot start or the CLI is not Claude Code, checks.
- `internal/core`: the whole loop with the scripted Claude stand-in and a real MCP client against the real gateway and a real
  container (`TestASandboxedTaskRunsItsCommandsAndChecksInAContainer`), the human-only switch, and with **real Claude Code**
  (`TestRealClaudeWorksInTheSandbox`, `DRYDOCK_REAL_CLAUDE=1`): the worker had no shell, ran `go test ./...` and a 130-second
  command through `sandbox_run`, the checks passed in the container, the change merged and the sandbox was removed.
- `ui`: the Sandbox settings panel.

# Security policy

Drydock is a Windows desktop application that runs AI coding agents on a person's own computer, so a security problem in it
can mean code running with that person's rights. Reports are welcome and are taken seriously.

## Reporting a vulnerability

Please report it **privately**, not in a public issue or pull request:

- Use GitHub's private reporting: the repository's **Security** tab, then **Report a vulnerability**
  (<https://github.com/kjmaguire/drydock/security/advisories/new>). Only the maintainer sees it.
- Say what you found, how to reproduce it (the smallest steps you have), which version or commit it is in, and what you think
  it allows. A proof of concept is welcome; please do not run it against anyone else's machine or accounts.
- Do not put secrets, personal data or a victim's files in a report.

The project is looked after by one person, so there is no promised response time; a report is read, and what was decided is
written on the advisory. A fix ships in the next release, and the advisory is published once people can update.

## What is in scope

- The application and its helpers in this repository (the Go core, the window, the updater, the MCP gateway, the sandbox,
  the egress proxy), and the build and release definitions under `.github/`.
- How Drydock treats what it did not write: a repository's files and configuration, an agent's output, an archive someone
  hands it, a page or a tool description from the network. Drydock's design assumes all of these can be hostile
  (`docs/security.md`); a way to make one of them run a program, read or write a file outside its place, reach a network
  address it should not, or approve something without a person, is a vulnerability.

## What is not

- The agent CLIs themselves (Claude Code, Codex, Gemini CLI, OpenCode, Goose and the others) and the models behind them:
  report those to their makers. Drydock does not read, copy or move their sign-in.
- A person who runs an agent with a permission mode that skips approvals has chosen to trust it; Drydock asks them to
  confirm that choice where they make it, and says what it does and does not hold back.
- Findings that need the attacker to already control the person's account or computer.

## Supported versions

Only the newest release, and the `main` branch, are supported. Drydock updates itself, and an update is installed only when its
manifest carries a signature that verifies against the key built into the running version.

## What the repository checks by itself

Every push runs the tests, a linter and a scan for secrets in the tracked files (`internal/security`); a weekly run checks the
Go and npm dependencies against the vulnerability databases and runs CodeQL (`.github/workflows/security.yml`); Dependabot
proposes dependency updates. These do not replace a report from a person.

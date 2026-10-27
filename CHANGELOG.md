# Changelog

Each release has a section headed `## <version>`; the release workflow (`.github/workflows/release.yml`) puts that section in the
draft release's notes and refuses a tag that has none. Say what changed for the person using Drydock, any schema migration, and
anything that breaks.

## 0.1.0-beta.1

The first build meant for people other than its developers: a private beta.

- **Install:** a per-user installer (no administrator rights) into `%LOCALAPPDATA%\Programs\Drydock`, with a Start menu entry
  and an Apps & features entry. Uninstalling keeps your data in `%LOCALAPPDATA%\Drydock` unless you ask for it to be deleted.
- **Not signed yet.** Windows SmartScreen will say "Windows protected your PC": choose More info, then Run anyway. Check the
  file against `SHA256SUMS.txt` first.
- **Updates are not switched on in this build.** Install a newer beta over this one; your data stays.
- **Agents:** Claude Code, Codex, Gemini CLI, OpenCode and Goose, each using your own installed and signed-in CLI. Drydock never
  reads or copies their credentials.
- **What was proven on a clean Windows runner before this was published:** install without administrator rights, first start,
  a project on a real repository, every workspace page, a backup, a support bundle, a second start handing over to the first,
  installing again over it on the same data, and uninstalling with the data kept (the `installed-app-proof` artifact of the
  release run has the screenshots and timings).
- **Not proven yet:** real signed-in agent CLIs on Windows, the updater, SmartScreen with a signed build, Windows 10.

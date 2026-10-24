# drydock.exe's Windows identity: icon, version, manifest, signing

What Windows shows about Drydock and what makes it trust the app. Packaging as a whole (the installer, the updater, licensing)
is `docs/packaging.md`; this page is the part that lives inside `drydock.exe` and the signing step in CI. Nothing here has been
looked at on a real Windows desktop yet: the resources are read back from the built exe on Linux and in CI, not seen in
Explorer.

## 1. What is embedded

Every build of `drydock.exe` (CI's `app` job and `task build:windows`) links three resources, defined in
`build/windows/winres.json` and turned into `rsrc_windows_amd64.syso` by go-winres (v0.3.3, ISC licence) just before `go build`:

| Resource | What it holds | Who reads it |
|---|---|---|
| Icon group 3 (`RT_GROUP_ICON`, the only one) | `build/windows/icon.ico`: Drydock's mark at 16, 20, 24, 32, 40, 48, 64, 96, 128 (32-bit BMP) and 256 pixels (PNG) | Explorer, the Start menu, shortcuts, a pinned taskbar button (the first icon group of the file); Wails loads ID 3 for every window; `app/icon_windows.go` loads it for the title bar and the taskbar button |
| Version information (`RT_VERSION` 1) | CompanyName, FileDescription "Drydock", ProductName "Drydock", InternalName "drydock", OriginalFilename "drydock.exe", LegalCopyright, FileVersion and ProductVersion | Properties > Details, Task Manager's name for the process, SmartScreen and Defender's file records |
| Application manifest (`RT_MANIFEST` 1) | `build/windows/drydock.exe.manifest` | the Windows loader |

The manifest asks for:

- `asInvoker`: Drydock never asks for administrator rights (the installer is per-user, decision D8).
- Per-monitor (v2) DPI awareness, with `true/pm` as the older systems' fallback. Wails v3 sets the same thing by API when the
  manifest has not (`application_windows.go`), so it is now set before any window exists instead.
- `supportedOS` Windows 10 (Windows 11 has the same ID; Go itself needs Windows 10), so Windows does not apply the
  compatibility shims it gives programs that declare nothing.
- Common Controls 6: the themed controls for the native dialogs and message boxes.
- `longPathAware`: paths longer than 260 characters work where the machine's LongPathsEnabled policy is on (deep worktrees,
  `node_modules`). Go's own file calls already cope; this covers the Windows APIs that Wails and the C code call.
- `activeCodePage` UTF-8 (Windows 10 1903 and newer): the process's ANSI code page is UTF-8, the encoding Go strings already
  use. Drydock and Wails call the wide (-W) Windows functions, the SQLite driver is pure Go and converts paths itself, and the
  repo map's C code opens no files, so nothing in the process reads ANSI text; the setting does not reach child processes (each
  has its own code page) or the console code page of a ConPTY. Kept because it makes any ANSI call a library might make agree
  with Go's strings; **not yet run on Windows**, so if anything turns up mangled text it is the first thing to remove.

The `.syso` is made at every build and never committed (`.gitignore`): it is a 220 KB binary that would have to be remade for
every version, and the build already has go-winres. A plain `go build .` without it still works and has no icon, which is
what `go run` and the tests use.

## 2. One version

`app.Version` (`app/version.go`, default `0.0.0-dev`) is the build's version: the updater compares it, `core.AppVersion` is
set from it, and the version resource carries it. `build/windows/winres.json` carries the same default (a test,
`app/version_test.go`, keeps the two equal). A release build sets all of it from one value:

    task build:windows VERSION=1.2.3

passes `--product-version 1.2.3 --file-version 1.2.3` to go-winres and `-X github.com/kjmaguire/drydock/app.Version=1.2.3` to
the linker. Windows shows the numbers `1.2.3.0`; a pre-release such as `1.5.0-beta.2` shows as `1.5.0.0` with the full text in
the FileVersion and ProductVersion strings. CI builds the default version (there is no release pipeline yet).

## 3. The title bar and the taskbar (what Wails needed)

Read from Wails v3.0.0-beta.34 (`pkg/application/webview_window_windows.go`, `application_windows.go`):

- Every window gets the icon group with **resource ID 3** of the executable (`NewIconFromResource(GetModuleHandle(""), 3)`),
  falling back to the bytes of `application.Options.Icon` only when there is no such resource. That is why the group is ID 3
  (Wails' own `wails3 generate syso` uses the same). `Options.Icon` is left empty: the bytes are not needed at run time.
- Wails loads it once with `LoadIcon` (the system's large-icon size) and sets it as the window's big icon only. The window
  class's icons are `LoadIcon(instance, IDI_APPLICATION)`, which finds nothing in an exe that has no resource 32512.
- So without more, the title bar would show the 32 pixel image shrunk by Windows instead of the drawn 16 and 20 pixel ones,
  and a window moved to a monitor with another scale would keep the first size. `app/icon_windows.go` therefore sets both of
  the window's icons (`WM_SETICON` small and big) from resource 3 at the sizes the window's DPI asks for
  (`GetSystemMetricsForDpi(SM_CXSMICON / SM_CXICON)`), when the window is shown and again when its DPI changes. An exe
  without the resource keeps what Wails gave it.
- The taskbar button of a running, unpinned Drydock and Alt-Tab use the window's big icon; a pinned button and the Start menu
  use the shortcut's icon, which is the exe's first icon group.

## 4. Regenerating the icon

The mark is `ui/public/favicon.svg` (a white anchor, stroke 2.2, on a 32 x 32 rounded square with the #6e79e8 to #9b6cf0
gradient). At 16 and 20 pixels that stroke blurs, so those two sizes have their own sources, `build/windows/icon-16.svg` and
`icon-20.svg`: the same ring, shank and arms with thicker strokes laid on the pixel grid and a bigger ring hole. Every other
size is drawn from the favicon itself.

    cd ui && npm ci && cd ..
    node build/windows/make-icon.mjs --preview <a scratch folder>

The script (Node 24, Playwright's Chromium from `ui/`; `CHROMIUM=<path>` names another one) rasterizes each size straight
from the vector, writes `build/windows/icon.ico` and the 1024 pixel master `build/windows/icon-1024.png`, and with
`--preview` every size as a PNG to look at. Commit the `.ico` and the PNG when the mark changes; another Chromium can shift
the anti-aliasing by a shade, so they are not remade on every build. `internal/winident`'s tests check that the script makes
the sizes the check wants and that the committed `.ico` has them.

## 5. The checks

- `go run ./cmd/drydock-winident -exe drydock.exe [-version 1.2.3]` reads the built exe's resources (`internal/winident`, with
  the `github.com/tc-hib/winres` library, ISC licence) and fails unless the first icon group is 3 with every size, the version
  resource names Drydock and carries the version (the definition's default without `-version`), and the manifest has every
  setting above. CI's `app` job and `task build:windows` run it on the exe they built, and again after signing.
- The Linux tests (`go test ./internal/winident/... ./app ./cmd/drydock-winident`) check the committed definition (the
  `.ico`'s sizes and formats, the manifest, the version strings and that the default equals `app.Version`), that the Taskfile
  and CI run the same go-winres command, and the checker itself on small Windows executables they cross-compile and link with
  the same resources (and without them, with another version, and with another icon group first).

## 6. What makes Windows trust the app

**Authenticode signing.** Unsigned, a downloaded `drydock.exe` gets SmartScreen's "Windows protected your PC" with publisher
"Unknown publisher", and Windows 11's Smart App Control blocks it outright. Signing names the publisher (the certificate's
subject; the version resource's CompanyName should match it once there is one) and lets reputation build. Sign with a SHA-256
file digest and an RFC 3161 timestamp, so the signature stays valid after the certificate expires; sign the exe and the
installer, every release, with the same identity. Decision D8 leaves the choice open:

- **Azure Artifact Signing** (formerly Trusted Signing): Microsoft-managed short-lived certificates, no hardware token, signed
  from CI. `docs/packaging.md` section 3 has the price and eligibility notes from Phase 0 (about $9.99 a month for Basic;
  eligibility for a new company and for an individual is **not re-verified** here: Microsoft's pages could not be read from
  this environment).
- **An OV certificate** from a certificate authority, kept on a hardware token or a cloud HSM as the CA/Browser Forum rules
  now require; signed with `signtool sign /fd SHA256 /tr <timestamp URL> /td SHA256`. Not wired into CI (the step below is
  Artifact Signing only).
- **EV certificates no longer skip SmartScreen.** Until 2024 an EV signature had reputation from the first download; Microsoft
  removed that in 2024, and EV-signed files now build reputation like OV-signed ones (Microsoft's SmartScreen reputation page,
  read through search results only, and DigiCert's notice). EV costs more and buys stricter identity checks, not fewer
  warnings.

**SmartScreen reputation.** SmartScreen decides per file and per signer: a new build of a well-known signer is usually let
through, a file from an unknown signer is warned about until enough people have downloaded and run it without trouble.
Microsoft publishes no threshold or timeline and there is no way to request reputation. Signing every release with the same
identity is what lets later releases inherit it; a new certificate (a renewal of an OV one, or a change of signing service)
may start over. Until then the warning on early releases is expected and the release notes should say so (docs/packaging.md
section 3).

**The installer (D8).** The per-user NSIS installer that `wails3 package` builds installs without administrator rights and
creates the Start menu shortcut; it is what the Start menu, search and a pinned taskbar button point at, so it is signed
too. Wails' template (`project.nsi.tmpl`) creates the shortcut without an AppUserModelID, and Drydock sets none for its
process, so Windows derives one from the exe's path and the shortcut and the running window group together. If Drydock later
sets an explicit AppUserModelID (for toast notifications or jump lists), the shortcut must carry the same one
(`System.AppUserModel.ID`), or a pinned Drydock and a running one show as two taskbar buttons. **Not built yet.**

**Mark of the Web.** A file downloaded by a browser carries a `Zone.Identifier` stream (the Mark of the Web), and
Explorer's own zip extraction copies it to every file it unpacks; that mark is what makes SmartScreen and Smart App Control
check `drydock.exe` at its first start, and why the download as a zip shows the warning even though the zip itself is not
run. Some other archivers do not copy the mark (**unverified** which). A signed installer as the download, rather than a zip
of the exe, keeps the warning to one signed file. Telling people to remove the mark (Properties > Unblock) is a workaround,
not a fix.

**Microsoft Defender false positives.** Go programs, and one that starts processes, uses Job Objects and ConPTY as Drydock
does, are sometimes flagged by Defender's machine-learning detections. The supported fix is to submit the file as a software
developer at Microsoft's file submission portal (Microsoft Security Intelligence, `https://www.microsoft.com/wdsi/filesubmission`)
with the release's hash, and hold the update feed until the verdict clears (docs/packaging.md section 12). A consistently
signed publisher makes such a detection less likely and quicker to clear.

## 7. Signing in CI (prepared, off)

`.github/workflows/ci.yml`'s `app` job signs `drydock.exe` with `azure/artifact-signing-action` (v2, the newest release,
v2.0.0; the action that `Azure/trusted-signing-action` now redirects to) when, and only when, it runs for a push to `main`
and the owner has configured it; on every pull request, and while nothing is configured, the step is skipped with a notice
and the download is unsigned. The step signs with SHA-256 and timestamps with `http://timestamp.acs.microsoft.com` (RFC 3161,
SHA-256), then checks that the signature is valid and timestamped (`Get-AuthenticodeSignature`) and that the resources are
still there. It signs before the download (`drydock-windows`) is assembled; the artifact `drydock-windows-unsigned` keeps the
unsigned exe.

What the owner sets up (repository settings, Secrets and variables > Actions):

| Name | Kind | What |
|---|---|---|
| `AZURE_ARTIFACT_SIGNING_TENANT_ID` | secret | the Entra ID tenant |
| `AZURE_ARTIFACT_SIGNING_CLIENT_ID` | secret | an app registration with the "Artifact Signing Certificate Profile Signer" role on the account |
| `AZURE_ARTIFACT_SIGNING_CLIENT_SECRET` | secret | that app's client secret |
| `AZURE_ARTIFACT_SIGNING_ENDPOINT` | variable | the account's endpoint, e.g. `https://eus.codesigning.azure.net/` |
| `AZURE_ARTIFACT_SIGNING_ACCOUNT` | variable | the signing account's name |
| `AZURE_ARTIFACT_SIGNING_PROFILE` | variable | the certificate profile's name |

The secrets reach the action as inputs only; GitHub masks them in the log and the workflow never prints them. The action's
own documentation recommends OpenID Connect (a federated credential, `azure/login`, `id-token: write` on the job) over a
client secret; that removes the stored secret and is the better choice once the account exists. Before the first signed
release, set the version resource's CompanyName and LegalCopyright (`build/windows/winres.json`) to the name on the
certificate.

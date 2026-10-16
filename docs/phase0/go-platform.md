> **Evidence appendix (raw agent report, gathered 2026-10-08 on Linux).** Paths such as `/tmp/.../scratchpad/...` refer to an ephemeral sandbox that is not preserved; small reproducible probes are kept in [`evidence/phase0/`](../../evidence/phase0/). Synthesis and decisions needed: [`docs/phase0.md`](../phase0.md).

# Phase 0 evidence report (Windows-only Go desktop app)

Gathered 2026-10-08 on Linux (go1.24.7 host, Go toolchain auto-switched to **go1.27.1** from proxy.golang.org; gcc 13.3.0; 4 cores).
All paths below are under `P=/tmp/claude-0/-home-user-Drydock/927585e5-898b-57de-a294-91a4b635e67b/scratchpad/p0a`.
Module zips were fetched from proxy.golang.org into an isolated GOMODCACHE (`$P/mc`); `$P/env.sh` sets the env. Scratch programs are in `$P/<name>/`.
"Verified on Linux" means built and/or executed here. Nothing was executed on Windows. Windows cross-compiles (`GOOS=windows`) prove only that the code compiles and links.

Notes on method: `proxy.golang.org/<mod>/@latest` is NOT reliable (it returned cel-go v0.30.0 while `@v/list` had v0.32.0, and tree-sitter v0.24.0 while v0.25.0 exists). I used `@v/list` highest non-prerelease plus `.info` timestamps.

## Cross-cutting findings (read first)

1. **Go version floor is high.** Declared `go` directives (from each module's go.mod): modernc.org/sqlite v1.60.1 = 1.26.0; goose v3.28.0 = 1.26.0; otel v1.47.0 = 1.26.0; mvdan.cc/sh v3.14.1 = 1.26.0; x/sys v0.48.0 = 1.26.0; agent-framework-go v0.1.0 = 1.26.0; go-task v3.54.0 = 1.26.4; osv-scanner v2.6.0 and gh v2.102.0 = 1.27.0 (CLIs only); wails v3 beta.28, openai-go v3.74.0, mcp go-sdk v1.8.0, go-pty v0.2.3 = 1.25.0. The app module must be `go 1.26.0` at minimum. Host Go 1.24.7 only worked because GOTOOLCHAIN=auto downloaded go1.27.1 (needs sum.golang.org + proxy reachable).
2. **Module path changes / traps** (all verified by running `go mod tidy`):
   - cel-go now lives at **`cel.dev/cel-go`** (v0.31.0+, repo github.com/cel-expr/cel-go). `github.com/google/cel-go@v0.32.0` fails: "module declares its path as: cel.dev/cel-go".
   - gitleaks library path is **`github.com/zricethezav/gitleaks/v8`** (go.mod of v8.30.1 declares that). `github.com/gitleaks/gitleaks/v8` fails with "module declares its path as: github.com/zricethezav/gitleaks/v8". (The brief's alternative path is wrong for `go get`.)
   - openai-go current major is **`github.com/openai/openai-go/v3`** (v3.74.0); un-suffixed path tops out at v1.12.0 (2025-07-30).
   - go-licenses current is **`github.com/google/go-licenses/v2`** (v2.0.1, 2025-09-08); v1.6.0 is from 2023.
   - DerekStride tree-sitter-sql tagged Go module **does not build** (`src/parser.c` is gitignored) - see item 7.
3. **GenAI OTel semconv moved out of the main repo and Go semconv v1.42/v1.43 have no gen_ai** - see item 8. Attribute name drift for cache-write tokens.
4. **Windows child-process cleanup**: `x/sys/windows` v0.48.0 does NOT define `PROC_THREAD_ATTRIBUTE_JOB_LIST`; go-pty creates its attribute list with capacity 1 (ConPTY only) - see items 10/14.
5. Dependency weight: the OTLP/HTTP exporter pulls grpc + protobuf (397 packages, 21.5 MB hello binary). agent-framework-go is a single module whose go.mod requires Azure SDK, google genai, grpc, a2a, copilot SDK (graph is huge, though `workflow` itself imports only ~15 non-module packages).

---

## 1. Wails v3

**Findings**
- Latest tag `v3.0.0-beta.28` (2026-10-05T15:15:35Z, 3 days old); 28 betas plus ~122 alpha tags in `@v/list`. **Prerelease: beta.** No stable v3.0.0 tag. Repo README table: "v3 | Beta | `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`". `@wailsio/runtime` npm latest = `3.0.0-beta.28`, license MIT (registry.npmjs.org).
- License: MIT (`LICENSE`, "Copyright (c) 2018-Present Lea Anthony").
- Go required: `go 1.25.0`.
- **Pure Go on Windows**: no `import "C"` in any `*_windows.go`; `GOOS=windows CGO_ENABLED=0 go build -tags production -ldflags="-w -s -H windowsgui"` of a hello app with a service built OK (14.8 s cold, 10.4 MB exe, 255 deps). Uses `go-ole`, `x/sys`, WebView2 via an internal `internal/webview2` + loader (no CGO). Linux/macOS use CGO (GTK/Cocoa) - irrelevant for Windows-only.
- Services API (`pkg/application/services.go`): `application.NewService(&T{})`, `NewServiceWithOptions(inst, ServiceOptions{Name, Route, MarshalError})`; optional interfaces `ServiceName`, `ServiceStartup(ctx, options) error` (ctx cancelled just before shutdown; startup in registration order; error aborts `App.Run`), `ServiceShutdown() error` (reverse order). If the instance implements `http.Handler` it is mounted on the asset server at `Route`. Built-in services in `pkg/services/`: dock, fileserver, kvstore, log, notifications, sqlite (the sqlite service imports `modernc.org/sqlite`; wails go.mod pins v1.44.3, MVS will raise to ours).
- Events (`pkg/application/event_manager.go`): `app.Event.Emit(name, data...) bool` (returns cancelled), `On(name, cb) func()` (unsubscribe), `OnMultiple`, `Off`, `Reset`, application-event hooks; typed registration `application.RegisterEvent[Data](name)` validates payloads and feeds binding generation.
- Binding generation: `wails3 generate bindings [-ts] [-d dir] [-i] [-b] [-names] ...` (flags from `internal/flags/bindings.go`). **Ran it on Linux** against a scratch service; output `frontend/bindings/wltest/greeter.ts`: `export function Greet(name: string): $CancellablePromise<string> { return $Call.ByID(3747494691, name); }` importing from `@wailsio/runtime`. Reported "Processed: 259 Packages, 1 Service, 1 Method" in 3.4 s. Calls are by numeric ID (stable-ID option `--obfuscated`, `-names` for names).
- Packaging on Windows (`internal/commands/build_assets/windows/Taskfile.yml`, `wails3 package` = Taskfile `package` task): default **NSIS** (`create:nsis:installer`, `makensis`, `project.nsi.tmpl`, WebView2 bootstrapper `/silent /install` in `wails_tools.nsh.tmpl`, `INSTALL_SCOPE=user|machine`) and **MSIX** (`FORMAT=msix` -> `wails3 tool msix` with `makeappx` or MSIX Packaging Tool; `--cert`, `--publisher`). **No WiX**: `grep -ri wix` in `internal/commands` returned nothing. Signing: `wails3 tool sign` (Authenticode, cert password in OS keychain), `sign:installer`. Build default `CGO_ENABLED=0`; if `CGO_ENABLED=1` on a non-Windows host it uses a Docker+Zig image (`build:docker`). `generate:syso` embeds icon/manifest/version info. Garble obfuscation supported.
- Wails embeds a fork of go-task: `github.com/wailsapp/task/v3 v3.40.1-patched3` (go.mod line 34); `wails3 task` runs Taskfiles.
- Other CLI: `wails3 dev`, `doctor`, `mcp` (project MCP server), `update`, `setup signing`.

**Evidence**: `$P/mc/github.com/wailsapp/wails/v3@v3.0.0-beta.28/{LICENSE,go.mod,pkg/application/services.go,pkg/application/event_manager.go,internal/commands/build_assets/windows/Taskfile.yml,internal/commands/build_assets/windows/nsis/project.nsi.tmpl}`; raw README https://raw.githubusercontent.com/wailsapp/wails/master/README.md lines 94-98; scratch app `$P/wl` (build log `$P/wl_build.txt`); generated TS `$P/wl/frontend/bindings/wltest/greeter.ts`; CLI built with `CGO_ENABLED=0 go build github.com/wailsapp/wails/v3/cmd/wails3` (`wails3 version` -> `v3.0.0-beta.28`).

**Verified on Linux**: compile for windows/amd64 CGO=0; bindings generation; CLI builds. **Needs Windows**: actually running the window/WebView2, NSIS/MSIX packaging (`makensis`, `makeappx` are Windows/NSIS tools not available here), signing, installer UX, WebView2 runtime presence, single-instance (`single_instance_windows.go` exists), event latency under streaming load.

**Concerns**: still **beta** (API "stable" per project but no 3.0 GA); nightly cadence. Known issues list is **UNVERIFIED**: api.github.com/github.com HTML unreachable and `v3.wails.io` blocked (403 proxy). A web search surfaced only release-note fixes (WebView2 init deadline, mixed-DPI, menus on Win10 1809) - source: https://docs-preview.wails.io/changelog/ - not an issue tracker. Need a human pass over open WebView2/Windows issues. Docs live in repo `docs/` outside the v3 module; paths guessed on raw returned 404.

---

## 2. modernc.org/sqlite

**Findings**
- Latest `v1.60.1` (2026-09-29), go 1.26.0. License **BSD-3-Clause** (`LICENSE`: 3 clauses incl. "Neither the name of the copyright holder"); SQLite itself public domain (`LICENSE-SQLITE`), plus `sqlite-vec` MIT (`LICENSE-SQLITE_VEC`), see `LICENSE-3RD-PARTY.md`, `SBOM.md`. **No CGO.**
- Bundled SQLite = **3.53.4** (`select sqlite_version()` on Linux).
- **FTS5 compiled in**: `lib/sqlite_windows.go` header (generated for windows/amd64) contains `-DSQLITE_ENABLE_FTS5 ... -DSQLITE_ENABLE_RTREE -DSQLITE_ENABLE_JSON1 -DSQLITE_ENABLE_MATH_FUNCTIONS -DSQLITE_ENABLE_STAT4 -DSQLITE_ENABLE_SESSION ... -DSQLITE_MUTEX_NOOP -DSQLITE_OS_WIN=1`, build tag `windows && (amd64 || arm64)`. Same flags in linux files. Runtime `pragma compile_options` on Linux printed `ENABLE_FTS5`, `ENABLE_RTREE`, `THREADSAFE=1`.
- **Executed on Linux** (`$P/sq/main.go`, output `$P/sqlite_out.txt`): FTS5 table `fts5(title, body, tokenize='porter unicode61')`, `where docs match 'reader*' order by bm25(docs)` returned the hit with `bm25=-0.5384`; `snippet()` worked; `json_extract` worked; external-content FTS5 with triggers worked via goose (item 4). DSN `file:...?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)` -> `journal_mode: wal`.
- Concurrency (Linux, WAL, busy_timeout=5000, `SetMaxOpenConns(8)`): 1 writer inserting 20,000 rows in 500-row txs took 407 ms while 4 reader goroutines completed 36,946 `count(*)` reads with **0 errors**. 4 concurrent writers x 200 deferred-BEGIN txs: ok=800 err=0 (serialized by busy_timeout).
- Gotcha reproduced (`$P/sq2`, `$P/sqlite_out2.txt`): a deferred tx that read, then another connection committed a write, then the first tries to write -> `database is locked (517)` (SQLITE_BUSY_SNAPSHOT) **immediately** (busy_timeout does not help). `BEGIN IMMEDIATE` makes a competing writer wait busy_timeout and then fail with `database is locked (5) (SQLITE_BUSY)`. Driver supports `_txlock=immediate` (sqlite.go:395). Recommendation: single write connection (or `_txlock=immediate`) + pooled readers.
- Benchmark (**Linux-only, 4-core VM, tmpfs/overlay unknown, not indicative of Windows NTFS/Defender**): 100,000 rows `events(run_id,seq,ts,type,payload ~110B)` with one index, prepared stmt, WAL, synchronous=NORMAL: batch=1000 per tx -> 708 ms (**141,187 rows/s**); batch=100 -> 1.54 s (64,837 rows/s); 5,000 single-row autocommit inserts -> 198 ms (25,254 rows/s).
- Windows cross-compile: `GOOS=windows CGO_ENABLED=0` amd64 and arm64 OK; 10.2 MB exe (`$P/sq_win_build.txt`), Linux binary 10.4 MB.

**Evidence**: `$P/mc/modernc.org/sqlite@v1.60.1/lib/sqlite_windows.go` line 1; `LICENSE`; README "BSD-3-Clause"; outputs above.

**Verified on Linux**: FTS5/bm25, WAL, concurrency, perf. **Needs Windows**: WAL on NTFS with antivirus scanning, `-DSQLITE_MUTEX_NOOP` on the Windows build (driver does its own locking - behavior under multi-goroutine use should be re-run on Windows), file locking when DB is on OneDrive/network paths, fsync cost (benchmark will differ), long-path handling.

**Concerns**: Go 1.26 floor. Windows lib is compiled with `SQLITE_MUTEX_NOOP` (read from header) - re-run concurrency test on Windows before trusting it. No blocker.

---

## 3. zalando/go-keyring

**Findings**
- Latest `v0.2.8` (2026-03-23), MIT (`LICENSE`: "Copyright (c) 2016 Zalando SE"), go 1.18. Pure Go (Windows impl uses `github.com/danieljoos/wincred v1.2.3`, MIT, which calls `windows.NewLazySystemDLL("advapi32.dll")` `CredReadW/CredWriteW/CredDeleteW/CredEnumerateW`; no cgo).
- Windows source (`keyring_windows.go`): `Set` rejects `len(password) > 2560` bytes with `ErrSetDataTooBig` (comment cites jaraco/keyring issue 540), rejects `len(service) >= 512` and `> 30*1024`; credential target name is `service + ":" + username`; blob stored as raw bytes; `Persist = PersistLocalMachine` (wincred.go:40,72). `Get` maps `syscall.ERROR_NOT_FOUND` -> `ErrNotFound`.
- **2560-byte limit confirmed by Microsoft docs** (via Microsoft Learn MCP, CREDENTIALW structure): "`CredentialBlobSize` ... cannot be larger than **CRED_MAX_CREDENTIAL_BLOB_SIZE (5*512) bytes**". Also TargetName max 32767 chars (generic), UserName max 513 chars, Comment 256.
- Store unavailable: no fallback on Windows; `Set/Get` return the raw Win32 errno from advapi32. `keyring_fallback.go` returns `ErrUnsupportedPlatform` only on unsupported OS. On Linux without a Secret Service the call errors (I ran it: `exec: "dbus-launch": executable file not found in $PATH`). `keyring_mock.go` provides `MockInit()` for tests.
- Windows cross-compile OK (`$P/allwin`, CGO=0, amd64+arm64).

**Needs Windows**: real Credential Manager round-trip, behavior in non-interactive/service logon sessions or Windows Server Core (errno not observed), roaming/enterprise policies. **Concern**: 2560-byte cap means store only API keys/refresh tokens (<2.5 KB); for larger secrets use an encrypted file with a DPAPI/keyring-held master key. OAuth JWT bundles can exceed 2560 bytes.

---

## 4. pressly/goose

**Findings**
- Latest `v3.28.0` (2026-09-02), **MIT** ("Original work Copyright (c) 2012 Liam Staskawicz / Modified work (c) 2016 Vojtech Vitek"), go 1.26.0. go.mod pins modernc.org/sqlite v1.57.0 (MVS lifts it to 1.60.1 in our module; tested).
- Library API: `goose.NewProvider(goose.DialectSQLite3, *sql.DB, fs.FS, opts...)`; `fs.FS` works with `//go:embed`. Also legacy global `goose.SetBaseFS`. Options: `WithGoMigrations`, `WithAllowOutofOrder`, `WithSessionLocker`, `WithDisableVersioning`, `WithExcludeNames`. `-- +goose NO TRANSACTION`, `-- +goose StatementBegin/End` supported.
- **Executed on Linux** (`$P/gs`): embedded `migrations/00001_init.sql` (table + FTS5 external-content virtual table + AFTER INSERT trigger inside StatementBegin/End) and `00002_events.sql` ran with `database/sql` driver name `sqlite` (modernc) -> "applied: 00001_init.sql 1.5ms, 00002_events.sql 0.5ms; db version: 2; fts rows via trigger: 1; down: 2 <nil>". Library-only import graph = 170 packages (drivers are only in `cmd/goose`). Windows CGO=0 build OK.

**Needs Windows**: nothing special expected (SQL + database/sql). **Concern**: Go 1.26 floor only.

---

## 5. coder/acp-go-sdk and ACP spec

**Findings**
- SDK latest `v0.13.5` (2026-06-02), **Apache-2.0**, go.mod `go 1.21`, pure Go, `ProtocolVersionNumber = 1` (`constants_gen.go`). Pre-1.0 (0.x) with `Unstable*` types in the public API.
- Spec repo `agentclientprotocol/agent-client-protocol`: **Apache-2.0** (LICENSE header + README: contributions Apache-2.0). README: "**The current stable ACP protocol version is `1`.**" JSON schemas in `schema/v1` and `schema/v2` (v2 meta exists with e.g. `auth/login`; v2 stability **UNVERIFIED**). Positioning: editors <-> coding agents.
- Client-side API (the Go app as ACP client driving a subprocess over stdio): implement `acp.Client` = `ReadTextFile, WriteTextFile, RequestPermission, SessionUpdate, CreateTerminal, KillTerminal, TerminalOutput, ReleaseTerminal, WaitForTerminalExit`; `conn := acp.NewClientSideConnection(client, stdinToAgent, stdoutFromAgent)`; calls `conn.Initialize`, `Authenticate`, `NewSession`, `LoadSession`, `ResumeSession`, `ListSessions`, `CloseSession`, `Prompt` (returns `StopReason` end_turn/max_tokens/max_turn_requests/refusal/cancelled), `Cancel` (notification), `SetSessionMode`, `SetSessionConfigOption`, `Logout`, `CallExtension/NotifyExtension` (methods starting `_`), `Done()`. `SessionUpdate` is a union: user/agent message chunk, agent thought chunk, tool_call, tool_call_update, plan, etc. MCP passthrough: `NewSessionRequest.McpServers []McpServer` with `Stdio` (all agents MUST support), `Http`, `Sse` (capability-gated), `Acp` (UNSTABLE).
- **Executed on Linux** (`$P/acp`): built SDK `example/agent` and `example/client`; client spawned agent over stdio: "Connected to agent (protocol v1)", "Created session: sess_...", streamed message chunks, tool_call and tool_call_update, and `RequestPermission` with options allow_once/reject_once. So initialize, session/new, session/prompt, session/update, session/request_permission were exercised end to end (with the demo agent, not a real model). Windows cross-compile of example client OK.
- Windows notes: SDK has **no Windows CI** (`.github/workflows/ci.yaml` runs `ubuntu-latest` only). Subprocess spawning is the host app's job (examples use plain `exec.CommandContext`, no process-group/Job Object handling; they call `npx` directly, which on Windows is `npx.cmd`). The example client's `ReadTextFile/WriteTextFile` do no sandbox/path-containment checks beyond "path must be absolute" - the real client must enforce workspace boundaries itself. Terminal methods in the example are stubs.

**Needs Windows**: spawning real agents (claude-code-acp via npx.cmd, gemini CLI) with stdio pipes, CRLF/encoding, killing the whole agent process tree, path format (`C:\` absolute paths in `fs/read_text_file`). **Concern**: pre-1.0 SDK, single vendor-ish maintainer (Coder), no Windows CI; v2 schema looming. Pin version, wrap behind an interface.

---

## 6. anthropic-sdk-go and openai-go

**Findings** (all behaviors below were **executed against local httptest fakes** in `$P/llm/main.go`; real-API behavior UNVERIFIED)
- Anthropic: `github.com/anthropics/anthropic-sdk-go` **v1.79.1** (2026-10-08), license **MIT** (LICENSE: "Copyright 2023 Anthropic, PBC. Permission is hereby granted..."), go 1.24 (+toolchain go1.25.8). Beta API surface is large (agents, memory stores, ...).
- OpenAI: `github.com/openai/openai-go/v3` **v3.74.0** (2026-10-08), **Apache-2.0** ("Copyright 2026 OpenAI"), go 1.25.0.
- Streaming: Anthropic `client.Messages.NewStreaming(ctx, params)` -> `ssestream.Stream[MessageStreamEventUnion]`; `message.Accumulate(event)` builds the final `Message`. OpenAI `client.Chat.Completions.NewStreaming` + `openai.ChatCompletionAccumulator`; set `StreamOptions: {IncludeUsage: true}` (verified in request body).
- Retry: both default `MaxRetries: 2` (anthropic requestconfig.go:174, openai :285); `option.WithMaxRetries(n)`; honors `retry-after-ms`/`Retry-After`/`x-should-retry`; sends `X-Stainless-Retry-Count`. Verified: a 529 (Anthropic) and a 429 (OpenAI) with `retry-after-ms: 10` were retried once and succeeded (also on the streaming path, before the stream starts).
- Usage fields: Anthropic `Usage{InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens, CacheCreation{Ephemeral5mInputTokens, Ephemeral1hInputTokens}, ServiceTier, InferenceGeo, OutputTokensDetails}`. Streaming accumulate gave in=12 out=7 cache_write=2000 cache_read=500. OpenAI chat `CompletionUsage.PromptTokensDetails{CachedTokens, CacheWriteTokens, AudioTokens, ...}` (v3.74.0 has `CacheWriteTokens` json `cache_write_tokens`); Responses API `ResponseUsageInputTokensDetails{CacheWriteTokens, CachedTokens}`. Non-stream parse gave cached=1920 cache_write=80.
- **Bug/gotcha**: `openai.ChatCompletionAccumulator` (streamaccumulator.go:485-486) sums `AudioTokens` and `CachedTokens` only; **`CacheWriteTokens` is dropped** (my run: stream usage cached=1920, cache_write=0 although chunk had 80). Read the final chunk's `chunk.Usage` yourself.
- cache_control (Anthropic): `anthropic.TextBlockParam{Text: ..., CacheControl: anthropic.NewCacheControlEphemeralParam()}` on system/user blocks/tools; 1 hour TTL via `anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h}`; request body verified to contain `"cache_control":{"type":"ephemeral"}` and `"ttl":"1h"`. OpenAI chat has an explicit `PromptCacheBreakpoint` content-part field (`mode: explicit`) in v3.74.0; automatic prefix caching otherwise.
- Custom BaseURL: `option.WithBaseURL("https://openrouter.ai/api/v1/")` (OpenAI; verified path `/api/v1/chat/completions`, `Authorization: Bearer`), `anthropic option.WithBaseURL(url)`; env `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`. Extra headers via `option.WithHeader`. Custom `HTTPClient`/`WithMiddleware` available.
- Response headers: `option.WithResponseInto(&resp *http.Response)` per call (works for non-stream and stream; verified reading `anthropic-ratelimit-requests-remaining`, `x-ratelimit-remaining-tokens`).

**Needs Windows / real network**: nothing OS-specific (pure Go). **Concerns**: Anthropic `input_tokens` excludes cached tokens while OTel GenAI `gen_ai.usage.input_tokens` "SHOULD include all types of input tokens, including cached tokens" (item 8) - normalize in one place. OpenRouter-specific fields (cache_write_tokens) surfacing through the OpenAI SDK against a real OpenRouter response is UNVERIFIED (SDK struct has the field; fake server only).

---

## 7. tree-sitter Go bindings

**Findings**
- Official `github.com/tree-sitter/go-tree-sitter` **v0.25.0** (tag time 2025-02-02), MIT ("Copyright (c) 2024 Amaan Qureshi"), go 1.23, **requires CGO** (every file has `#cgo CFLAGS: -std=c11 ...`; vendors the C runtime in `src/`; `dup_windows.go` has Windows-specific cgo). Library ABI constants: `TREE_SITTER_LANGUAGE_VERSION 15`, min compatible 13. Last tag is ~8 months old; repo CI (`.github/workflows/ci.yml`) builds and tests on `[ubuntu-latest, macos-latest, windows-latest]` with Go 1.23 - this is the Windows evidence in the repo (job logs not seen).
- Community `github.com/smacker/go-tree-sitter` (pseudo-version v0.0.0-20240827094217-dd81d9e9be82): MIT, CGO, bundles grammars in-repo (bash c cpp csharp css golang html java javascript kotlin lua php python ruby rust scala **sql** svelte swift toml typescript yaml ...). Its CI is ubuntu-only (`test.yml`, Go 1.20-1.23). Last commit 2024-08-27 (stale).
- Grammar Go modules for official bindings (all MIT; `bindings/go` package; versions = highest tag; verified built unless noted):

| Lang | module path | ver | Language func |
|---|---|---|---|
| Go | github.com/tree-sitter/tree-sitter-go | v0.25.0 | `Language()` (ABI 15) |
| JavaScript | github.com/tree-sitter/tree-sitter-javascript | v0.25.0 | `Language()` (15) |
| Python | github.com/tree-sitter/tree-sitter-python | v0.25.0 | `Language()` (15) |
| TypeScript + TSX | github.com/tree-sitter/tree-sitter-typescript | v0.23.2 (2024-11-11) | `LanguageTypescript()`, `LanguageTSX()` (ABI 14) |
| PHP | github.com/tree-sitter/tree-sitter-php | v0.25.1 (2026-10-06) | `LanguagePHP()` / `LanguagePHPOnly` (15) |
| C# | github.com/tree-sitter/tree-sitter-c-sharp | v0.23.5 | `Language()` (15) |
| Java | github.com/tree-sitter/tree-sitter-java | v0.23.5 | `Language()` (14) |
| Rust | github.com/tree-sitter/tree-sitter-rust | v0.24.2 | `Language()` (15) |
| SQL | github.com/DerekStride/tree-sitter-sql | tag v0.3.11 **unbuildable**; works at pseudo-version `v0.0.0-20260919000438-39fdb0064037` from branch `gh-pages` | `Language()` (15) |

  SQL problem: tagged module has `bindings/go/binding.go` including `../../src/parser.c`, but `src/` contains only `scanner.c` because `/src/parser.c` is in `.gitignore`; `go build` fails "fatal error: ../../src/parser.c: No such file". The `gh-pages` branch contains generated sources and built fine. This SQL grammar is MIT ("Copyright (c) 2021 Derek Stride"). Also the grammar is a generic SQL; dialect fidelity unverified.
- **Executed on Linux** (`$P/ts`): single binary with official bindings + all 10 grammars parsed one snippet per language, all `hasError=false`; a Go tags query `(function_declaration name: (identifier) @name.definition.function)` captured `Add`, `Sub`. Build: **cold build (empty GOCACHE), all 10 grammars, gcc 13.3.0, 4 cores: 32.2 s; binary 27.8 MB (26.1 MB stripped `-s -w`)**; warm rebuild after editing main.go 0.64 s. Minimal (bindings + Go + Python only): cold 13.9 s, 3.7 MB (2.5 MB stripped). (`$P/ts_build_time.txt`)
- **Windows cross-compile with Zig** (`pip install ziglang` -> zig 0.17.0; `CC="zig cc -target x86_64-windows-gnu"`, `GOOS=windows CGO_ENABLED=1`): all 10 grammars **built and linked into a PE32+ x86-64 exe** (`file`: "PE32+ executable (console) x86-64, for MS Windows"), 104 s cold, 28.5 MB (`$P/ts_win_build.txt`). Not executed.
- Grammar repos' own CI includes Windows: tree-sitter-go `ci.yml` os matrix `[ubuntu-latest, windows-latest, macos-15]` with `parser-test-action` `test-go: true`; typescript, php, c-sharp same; DerekStride sql `[macos-latest, ubuntu-latest, windows-2025]`.

**Remains to be proven on Windows**: (a) parse correctness/behavior of the cgo binary on a real Windows host (never run); (b) native `mingw-w64` gcc build (the brief's MinGW path) - only zig cc was tried; (c) Windows Defender / SmartScreen reactions to a large CGO exe and to cgo test binaries; (d) CGO toolchain on developer/CI machines (windows-latest runners ship MinGW gcc - not verified here); (e) `go test` of tree-sitter packages on Windows; (f) memory/time for large repos.

**Concerns**: CGO is required for tree-sitter, contradicting a pure-Go-only Windows build (Wails and sqlite are CGO-free). Wails' own Taskfile switches to Docker/Zig for CGO cross-builds (`build:docker`), so CGO is "supported but off the happy path". TypeScript grammar is at ABI 14 / v0.23.2 (older than others), so check that query files written against newer grammars still match. SQL needs a pinned pseudo-version or vendored parser.c. Alternative route: skip tree-sitter in v1 (regex/ctags) - see item 13.

---

## 8. OpenTelemetry Go

**Findings**
- `go.opentelemetry.io/otel` **v1.47.0** (2026-10-02), Apache-2.0, go 1.26.0. All of otel, sdk, trace, sdk/metric, otlptrace/{http,grpc}, otlpmetric/otlpmetrichttp, stdouttrace latest = **v1.47.0** (proxy lists). Stable (v1) for traces/metrics APIs.
- **GenAI semconv status**: Development (not stable). The upstream `open-telemetry/semantic-conventions` `docs/gen-ai/README.md` on main is now a stub: "**Moved: Generative AI semantic conventions** ... have moved to https://github.com/open-telemetry/semantic-conventions-genai". The new repo (Apache-2.0) `docs/gen-ai/README.md` says "**Status**: [Development]"; attributes carry the Development badge, e.g. `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_write.input_tokens`, `gen_ai.usage.input_tokens` ("This value SHOULD include all types of input tokens, including cached tokens"). Scope also includes agents, tool execution, evaluation, MCP.
- **Go semconv packages**: `go.opentelemetry.io/otel/semconv/v1.41.0` includes `genaiconv` and `mcpconv` packages; **`v1.42.0` and `v1.43.0` (latest) contain no gen_ai at all** (`grep gen_ai` over semconv dirs lists v1.26-v1.41 only). v1.41.0 names the cache-write attribute `gen_ai.usage.cache_creation.input_tokens` (attribute_group.go:7009) while the new genai repo registry uses `gen_ai.usage.cache_write.input_tokens` (grep `cache_creation` in registry docs: 0 hits) - **attribute drift**. Plan: define our own constants (string keys) rather than depend on a semconv Go version; gate with `OTEL_SEMCONV_STABILITY_OPT_IN` style flag.
- Dependency weight (scratch `$P/ot`: otel + sdk + otlptracehttp + semconv v1.41.0 genaiconv): `go mod graph` 255 lines, `go list -m all` 90 modules, `go list -deps` 397 packages, Linux binary **21.5 MB**; the HTTP exporter still transitively imports grpc, protobuf, grpc-gateway, genproto, x/net, x/text. Windows CGO=0 build OK.

**Needs Windows**: nothing special. **Concerns**: GenAI conventions are Development and now in a different repo/schema URL; Go semconv lags/dropped them. Collector/exporter is optional for a desktop app - consider a local SQLite span/event sink and export OTLP only on demand to save ~15 MB.

---

## 9. modelcontextprotocol/go-sdk

**Findings**
- Latest stable `v1.8.0` (2026-09-04; pre-releases v1.8.0-pre.1/2 exist). go 1.25.0. **License in transition**: LICENSE begins "The MCP project is undergoing a licensing transition from the MIT License to the Apache License, Version 2.0 ... Contributions made by authors who originally licensed their work under the MIT License and who have not yet granted explicit permission to relicense remain licensed under the MIT License", followed by the Apache-2.0 text (docs CC-BY-4.0). Treat as **mixed MIT/Apache-2.0**; both permissive. Go source headers say "MIT-style license" (cmd.go).
- Spec support (README table): v1.7.0+ -> latest MCP spec **2026-07-28**; also 2025-11-25 (client OAuth experimental), 2025-06-18, 2025-03-26, 2024-11-05. Roots, sampling, logging are deprecated as of 2026-07-28 (SEP-2577) but still supported.
- Coverage: client + server: tools (typed handlers with input/output schema inference/validation), prompts, resources/templates, list-changed notifications, completion, elicitation, multi-round-trip requests, cacheable list results, capabilities config; `auth` and `oauthex` packages for OAuth.
- Transports (types in `mcp/`): `StdioTransport` (server side), **`CommandTransport{Command *exec.Cmd, TerminateDuration}`** (client spawns a stdio server), `StreamableClientTransport`, `StreamableServerTransport` + `NewStreamableHTTPHandler`, `SSEClientTransport`/`NewSSEHandler`, `InMemoryTransport`, `IOTransport`, `LoggingTransport`.
- Windows: no Windows CI (all workflows `runs-on: ubuntu-latest`). `CommandTransport` close sequence = close stdin, wait `TerminateDuration` (5 s), `Process.Signal(syscall.SIGTERM)`, then `Process.Kill()`. On Windows `os.Process.Signal` only supports Kill, so SIGTERM fails and it falls to `Kill` (read from source; not run). **`Kill` only kills the direct child**; `npx.cmd`/`uvx` wrappers leave grandchildren alive unless the process is in a Job Object (items 10/14). Cross-compile with CommandTransport OK (`$P/allwin`).
- Executed on Linux: bad command through `CommandTransport` returned `connection closed: calling "initialize": client is closing: EOF` (clean error). A full client<->server round-trip was not run.

**Needs Windows**: stdio against real Windows MCP servers (npx.cmd, python, node), process-tree kill, line endings (code handles `\r\n`, transport.go:534). **Concern**: licensing text is unusual - get legal sign-off; wrap behind our own interface; no Windows CI upstream.

---

## 10. cel-go, mvdan/sh, gitleaks, go-pty, x/ansi, task, go-licenses, osv-scanner, gh

| Tool | Module / path actually usable | Version (date) | License (read LICENSE) | go directive | Use |
|---|---|---|---|---|---|
| CEL | **`cel.dev/cel-go`** (old google path breaks) | v0.32.0 (2026-08-19) | Apache-2.0 | 1.23.0 | library |
| shell parser/interp | `mvdan.cc/sh/v3` | v3.14.1 (2026-09-06) | BSD-3-Clause ("Copyright (c) 2016, Daniel Marti") | 1.26.0 | library |
| gitleaks | **`github.com/zricethezav/gitleaks/v8`** | v8.30.1 (2026-02-21) | MIT ("Copyright (c) 2019 Zachary Rice") | 1.24.11 | library + CLI |
| PTY | `github.com/aymanbagabas/go-pty` | v0.2.3 (2026-05-17) | MIT | 1.25.0 | library |
| ANSI | `github.com/charmbracelet/x/ansi` | v0.11.8 (2026-08-11) | MIT | 1.24.2 | library |
| Task | `github.com/go-task/task/v3` | v3.54.0 (2026-10-01) | MIT | 1.26.4 | external CLI (Wails embeds a fork) |
| go-licenses | `github.com/google/go-licenses/v2` | v2.0.1 (2025-09-08) (v1.6.0 = 2023) | Apache-2.0 | 1.23.0 | external CLI |
| OSV-Scanner | `github.com/google/osv-scanner/v2` | v2.6.0 (2026-09-14) (v1 line ends v1.9.2) | Apache-2.0 | **1.27.0** | external CLI |
| GitHub CLI | `github.com/cli/cli/v2` | v2.102.0 (2026-09-30) | MIT ("Copyright (c) 2019 GitHub Inc.") | **1.27.0** | external CLI (do not embed) |

- **Executed on Linux** (`$P/misc`): gitleaks `detect.NewDetectorDefaultConfig()` + `DetectString` on a fake AWS key found rules `aws-access-token` and `generic-api-key` (222 default rules loaded) -> embedding as a library works; cel-go compiled and evaluated `tool == "write_file" && path.startsWith("/etc") ? "deny" : "allow"` -> `deny`; mvdan/sh parsed `echo hi | wc -c; rm -rf "$HOME"/x && git push --force` and walked CallExprs -> `[echo wc rm git]`; `interp.New` ran `echo from-interp; echo $((1+2))` -> `from-interp`, `3`; x/ansi `Strip` and `StringWidth` worked. Combined binary 30.5 MB (Linux) / 31.0 MB (windows/amd64 CGO=0 cross-build OK), 464 packages.
- OSV-Scanner invocation (README of v2.6.0): `osv-scanner scan source -r /path`, `osv-scanner scan image name:tag`, offline: `osv-scanner --offline --download-offline-databases ./dir` (README: network use sends package names/versions/hashes to osv.dev unless offline). Invoke as subprocess and parse JSON output.
- **go-pty on Windows (source `cmd_windows.go`, `pty_windows.go`)**: uses ConPTY via `windows.CreatePseudoConsole` (x/sys) with two `os.Pipe`s, initial size 80x25, `Resize` -> `ResizePseudoConsole`; spawns with `CreateProcess[AsUser]` + `EXTENDED_STARTUPINFO_PRESENT` and `windows.NewProcThreadAttributeList(1)` - **capacity 1, used only for `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` (0x20016)**; it closes `pi.Thread` right after start and uses `os.FindProcess` for the handle; context cancel = `c.Process.Kill()` (direct child only). `Cmd.SysProcAttr.CreationFlags` is honored. Upstream CI matrix includes `windows-latest` (`.github/workflows/build.yml`). README notes ConPTY attribute cannot be set via os/exec (golang/go#62708). Windows cross-compile OK.

**Verified on Linux**: library behavior of gitleaks/cel/sh/ansi. **Needs Windows**: go-pty ConPTY run (never executed), ANSI behavior of ConPTY output (ConPTY re-renders / emits VT with its own cursor sequences), Windows-flavoured shell semantics (mvdan/sh is a POSIX shell interpreter - it does not run cmd/PowerShell), gitleaks speed with Defender scanning.

**Concerns**: (1) import paths for cel and gitleaks (above); (2) go-pty cannot add a JOB_LIST attribute (capacity 1) and discards the thread handle, so you cannot CREATE_SUSPENDED -> assign job -> resume through it; use "put the app process itself in a KILL_ON_JOB_CLOSE job at startup" (children inherit job membership; nested jobs need Windows 8+ - UNVERIFIED) or fork a small wrapper; (3) gh and osv-scanner require Go 1.27 to build from source - ship released binaries or require users to install them; (4) mvdan/sh is for parsing/policy and a POSIX-sh interpreter, not for executing user commands on Windows.

---

## 11. Microsoft agent-framework-go (workflow)

**Findings**
- Exists at `github.com/microsoft/agent-framework-go`, only tag **v0.1.0** (2026-09-01), **MIT** ("Copyright (c) Microsoft Corporation"), go 1.26.0. README: "Microsoft Agent Framework for Go is in **public preview** and is currently evolving outside the core upstream codebase." .NET/Python live in `microsoft/agent-framework` (not read). Feature-comparison doc: Go lacks handoff/Magentic builders, declarative workflows, DevUI; "Foundry-hosted deployment is not implemented".
- Workflow engine (`workflow/`, `workflow/inproc`, `workflow/checkpoint`, `workflow/internal/*`): graph `Builder` (`AddEdge`, `AddFanOutEdge`, `AddFanInBarrierEdge`, switch/conditional edges, subworkflows), executors (`workflow.NewExecutor(id, handler)`, typed `Handle(ctx, msg)`), Pregel-style **supersteps**: `runner.RunSuperStep` -> `runSuperstep` delivers each step's messages to all receivers **concurrently in an `errgroup`** (runner.go), then joins subworkflows, then **writes the checkpoint after the whole superstep** (`r.checkpoint(ctx)`), then emits `SuperStepCompletedEvent` with `CheckpointInfo`.
- Checkpoint contents (`internal/checkpoint/checkpoint.go`): `StepNumber, WorkflowInfo, RunnerData{InstantiatedExecutors, QueuedMessages, OutstandingRequests, RequestOwners, ResponsePortOwners}, StateData (scoped shared state), EdgeStateData, Parent`. Storage: `checkpoint.Manager` via `NewInMemoryManager()` or `NewJSONManager(Store[json.RawMessage])`; `Store[T]` interface = `CreateCheckpoint, RetrieveCheckpoint, RetrieveIndex` (so a SQLite-backed store is easy). Built-in `FileSystemJSONStore` (index.jsonl + per-checkpoint file, `os.Root`, `gofrs/flock` exclusive lock: "store ... is already in use" for a second opener; single process).
- Request ports (human in the loop): `workflow.RequestPort{ID, Request, Response}`, `ExternalRequest/ExternalResponse{RequestID, PortInfo, Data}`; `StreamingRun.SendResponse`; outstanding requests persisted in the checkpoint and republished on restore (`WithPendingRequestRepublish`).
- Time travel / restore: `Run.RestoreCheckpoint(ctx, info)` (in-place) and `env.Resume/ResumeStreaming(ctx, wf, info)` (new run object, same session); each commit records `Parent`, `RetrieveIndex(session, withParent)` lists children (branches). Restore fails if superstep running ("cannot restore a checkpoint while a workflow superstep is running") or workflow topology mismatches (`WorkflowInfo.Match`).
- Middleware: `agent.Middleware` / `MiddlewareFunc` wraps **agent** runs (`agent/middleware.go`); `grep -i middleware workflow/` finds nothing and there is no retry/backoff in workflow executors. Workflow observability via `workflow/observability/opentelemetry`.
- **Experiment** (`$P/maf/main.go`, output `$P/maf_out.txt`): workflow `start -> fan-out [A, B, C] -> fan-in barrier -> join`, counters for side effects; B fails in run 1.
  - Run 1: C and A completed, B FAILED; ErrorEvent; only the post-`start` checkpoint exists (no checkpoint for the failed superstep). Counters after run1: A=1 B=1 C=1.
  - Resume #1 from that checkpoint (new run): A, B, C all re-executed; counters A=2 B=2 C=2; output produced.
  - **Duplicate resume** #2 from the same checkpoint: A, B, C re-executed again (A=3 B=3 C=3) and the workflow emitted a **second OUTPUT**; both resumes added sibling checkpoints under the same parent. No dedupe/idempotency token anywhere.

**Weaknesses relevant to our design (all evidenced above)**
1. *Partial superstep*: checkpoint is taken only after every parallel node of the step finishes; a failure/crash mid-step loses the already-successful siblings' results and they are **re-run on resume** (A and C re-ran). Side effects must be idempotent or recorded by us (own idempotency keys / event-sourced tool-call ledger).
2. *Duplicate resume*: nothing prevents two resumes of one checkpoint (fork); `completeRequest` rejects a second response only within a live runner (`no pending request with ID`), but after a restore the same request is outstanding again, so a repeated human approval is accepted again. Need our own run lease/state machine (e.g. SQLite row `runs(id, status, lease)`) around resume.
3. *Manual resume*: no automatic crash recovery; host must find `LatestCheckpoint`, rebuild the workflow identically (executor IDs, hosted-agent IDs/names must match), and call Resume. Executor-local fields are **not** snapshotted automatically; each executor must implement `OnCheckpoint/OnCheckpointRestored` and use scoped state (see `examples/03-workflows/checkpoint/checkpoint_and_resume/main.go`), otherwise state silently resets.
4. Preview v0.1.0, single tag, no Windows CI evidence collected; pulls heavy deps (Azure, genai, grpc, a2a); `go 1.26`.
5. `FileSystemJSONStore` file lock + percent-encoded file names are untested on Windows (UNVERIFIED); prefer a SQLite `Store`.

**Needs Windows**: file store locking/rename semantics; otherwise pure Go (workflow+inproc+checkpoint cross-compile OK, `$P/maf`, 15 external non-module packages). **Recommendation**: use only as a reference or for the in-proc graph executor; own durability/idempotency layer is required either way.

---

## 12. Agent Room

**Findings**
- Repo `github.com/agent-room-alkl/agent-room`, **MIT** ("Copyright (c) 2026 Agent Room contributors", `LICENSE` read from raw main). Root package.json `version 0.0.0` (private monorepo); README says "protocol v0.1". TypeScript.
- File: `packages/shared/src/toolCallRecovery.ts` (6,948 bytes) exports `looksLikeLeakedToolCall` and `recoverLeakedRunCode`. It recovers **only the `run_code` tool** leaked as text by some OpenAI-compatible providers (comment: DeepSeek, "deepseek-v4-flash especially").
- Algorithm (<=15 lines):
  1. Cheap gate: text must contain `run_code` AND one of markers (`invoke name="run_code"`, `tool_calls`, `tool_call`, `tool▁call`, `DSML`, `run_code`).
  2. Tag form (higher confidence): regex for `<…invoke name="run_code"…> … </…/invoke…>` tolerant of arbitrary delimiter junk (e.g. `<｜｜DSML｜｜invoke ...>`), then inner `<…parameter name="K"…>V</…parameter…>` pairs -> params map.
  3. Require non-empty `code`; `language` defaults to `python`; also parse flags `expose_port` (positive number), `background` (true/"true"), `deliver_path` (non-empty string).
  4. Only if no tag-form call found: JSON form regex `run_code` within 40 chars of `{ ... "code" ... }` (fenced or bare), `JSON.parse`, same field handling.
  5. Record matched spans; remove back-to-front from the text; `scrubResidue` strips leftover `<…tool_calls>`, `<｜…>` tokens, `DSML` fragments, collapses 3+ newlines.
  6. Return `{calls, cleaned}`; if the model emitted only a leaked call, `cleaned` is "" (treat as empty reply, do not post markup). Never throws; returns nothing when no runnable code body.
- Weak points: hard-coded to one tool and one provider's leak shape; lazy JSON regex (`\{[\s\S]*?"code"[\s\S]*?\}`) stops at the first `}` after `"code"` so JSON whose `code` string or other fields contain braces fails `JSON.parse` and is skipped silently; no tool-name allowlist/arg schema validation; no ID generation.
- **Recommendation: reimplement the idea, not the code**, in Go output parsers. Generalize: (a) detect leaks only when the provider returned zero structured tool_calls and the text matches a known marker set; (b) per-provider recognizers (DSML/invoke-parameter tags, ```json fenced `{"name":..,"arguments":..}`, `<tool_call>` Hermes/Qwen style) behind one interface; (c) use a real JSON decoder with brace/string-aware scanning (`json.Decoder` on candidate offset) instead of lazy regex; (d) validate tool name against the registered tools and args against the JSON schema, synthesize a call ID, mark the result "recovered" in telemetry; (e) strip the span from user-visible text. Small (<300 LOC) with table-driven tests built from captured provider outputs. The repo lacks Windows relevance (pure string handling).

**Evidence**: `$P/ar/main_LICENSE`, `$P/ar/src_toolCallRecovery.ts`, https://raw.githubusercontent.com/agent-room-alkl/agent-room/main/packages/shared/src/toolCallRecovery.ts. Verified: read-only. Needs Windows: none.

---

## 13. Aider repo map (`aider/repomap.py`, 867 lines, Apache-2.0)

Aider `__version__ = "0.86.3.dev"` (aider/__init__.py on main); repo LICENSE.txt = Apache-2.0. Source read from https://raw.githubusercontent.com/Aider-AI/aider/main/aider/repomap.py (`$P/aider/repomap.py`).

**Algorithm (as implemented)**
1. **Tags**: for each file, `get_tags(fname, rel_fname)` with a disk cache (diskcache/SQLite, key=fname, value `{mtime, data}`; invalid if mtime changed; cache dir `.aider.tags.cache.v{3|4}`; on sqlite errors it recreates the cache, then falls back to a dict). `get_tags_raw`: pick tree-sitter language from filename, load `queries/<pack>/<lang>-tags.scm`, parse, run query captures; a capture named `name.definition.*` -> `Tag(kind="def")`, `name.reference.*` -> `kind="ref"`; `Tag = (rel_fname, fname, line, name, kind)` with line = node start row. If a file has defs but **no refs** (e.g. C++ queries), backfill refs by lexing the whole file with Pygments and emitting every `Token.Name` as `ref` (line -1).
2. **Per-file collections** (`get_ranked_tags`): `defines[ident] -> set(files)`, `references[ident] -> list(files)` (one entry per occurrence), `definitions[(file, ident)] -> set(Tag)`. If there are no references at all, references := defines.
3. **Personalization**: `personalize = 100 / len(fnames)`; files in the chat (editable/read-only added files) get `+personalize`; a file whose rel path is in `mentioned_fnames` gets `max(cur, personalize)`; a file whose path components (dirs, basename with/without extension) intersect `mentioned_idents` gets another `+personalize`. Files with score > 0 form the personalization vector (also used as `dangling`).
4. **Graph**: `nx.MultiDiGraph` over files. For each `ident` in `defines ∩ references`: `mul = 1.0`; x10 if ident is in `mentioned_idents`; x10 if (snake_case or kebab-case or camelCase) and `len(ident) >= 8`; x0.1 if it starts with `_`; x0.1 if defined in more than 5 files. For every `(referencer file, count)` and every `definer file`: edge referencer -> definer with `weight = mul(×50 if the referencer is a chat file) × sqrt(count)`, attribute `ident`. Self-edges allowed. Idents defined but never referenced get a self-edge `weight=0.1`.
5. **PageRank**: `nx.pagerank(G, weight="weight", personalization=..., dangling=...)` (fallback without personalization on ZeroDivisionError; returns [] if still failing).
6. **Distribute rank to definitions**: for each source node, split its rank across its out-edges proportional to edge weight; accumulate into `ranked_definitions[(definer_file, ident)]`. Sort descending by (rank, key). Emit tags for those (skipping defs in files already in chat), then append remaining files with no tags in PageRank order (`(fname,)` entries), then any `other_fnames` not seen. Also prepend "special" important files (`filter_important_files`: README/config etc.) not already included.
7. **Token budget (binary search)** (`get_ranked_tags_map_uncached`): `lower=0, upper=num_tags, middle=min(max_map_tokens // 25, num_tags)`; loop: render `to_tree(ranked_tags[:middle])`, count tokens (`token_count`: exact if text < 200 chars, else sample every `step=lines//100` lines and extrapolate), accept best tree if `tokens <= budget` and larger than previous best, OR `|tokens-budget|/budget < 0.15` (then stop immediately); if tokens < budget raise lower bound else lower the upper bound; `middle = (lower+upper)//2`.
8. **Budget sizing** (`get_repo_map`): if there are **no chat files**, budget = `min(int(map_tokens * map_mul_no_files(=8)), max_context_window - 4096)`; default `map_tokens=1024`.
9. **Rendering** (`to_tree`/`render_tree`): group tags by file, skip chat files; for each file use `grep_ast.TreeContext` (tree-sitter) to print only lines-of-interest (def lines) with their enclosing scope headers (`child_context=False`, `last_line=False`, `margin=0`, `loi_pad=0`); files with only a bare path print the name; every output line truncated to 100 chars. Output is `relpath:` followed by the code-line excerpts and `⋮` ellipsis lines.
10. **Caching**: `refresh` modes `auto` (use the cached map only if the last generation took > 1.0 s, key = chat files, other files, budget, mentioned files and idents), `files`, `manual`, `always`; `tree_cache` keyed by `(rel_fname, sorted lines, mtime)`.

**Go reimplementation notes**: no networkx; implement weighted PageRank (power iteration, alpha 0.85, tolerance ~1e-6, dangling/personalization vectors like networkx) and mimic MultiDiGraph by summing parallel edges per (src,dst) while keeping per-ident weights for step 6. Query files exist upstream for `tree-sitter-language-pack/`: csharp, go, python, javascript, java, rust (HTTP 200); `tree-sitter-languages/`: typescript, php, c_sharp, go, python, javascript, java, rust (200). **No upstream tags query for SQL** (404 in both dirs) and no separate tsx file; we must author `.scm` for SQL/TSX/PHP in the module's capture naming. The scope-header rendering (`grep_ast`, PyPI 0.9.0, license field empty in PyPI metadata - check separately) is the part needing a Go rewrite on top of tree-sitter nodes.

**Verified**: source read only (algorithm summary above is from the code, not memory). **Needs Windows**: n/a for the algorithm; Aider itself notes a Windows `ValueError` on `relpath` across drives (`get_rel_fname` fallback) - mirror it.

---

## 14. golang.org/x/sys/windows Job Objects (v0.48.0, BSD-3 `LICENSE`, go 1.26.0)

**Confirmed from source (`$P/mc/golang.org/x/sys@v0.48.0/windows`)**
- `CreateJobObject(jobAttr *SecurityAttributes, name *uint16) (Handle, error)` (zsyscall_windows.go:1907); `SetInformationJobObject(job Handle, JobObjectInformationClass uint32, JobObjectInformation uintptr, JobObjectInformationLength uint32) (ret int, err error)` (:3388); `AssignProcessToJobObject(job, process Handle) error` (:1785); `QueryInformationJobObject` (:3107); `TerminateJobObject` (:3492); `CreateIoCompletionPort`, `GetQueuedCompletionStatus` (for job notifications).
- Types/consts (types_windows.go): `JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation JOBOBJECT_BASIC_LIMIT_INFORMATION; IoInfo; ProcessMemoryLimit; JobMemoryLimit; PeakProcessMemoryUsed; PeakJobMemoryUsed}` (:2572), `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000` (:2552), `JOB_OBJECT_LIMIT_BREAKAWAY_OK`, `JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK`, `JobObjectExtendedLimitInformation = 9`, `JobObjectAssociateCompletionPortInformation = 7`.
- Attribute lists: `NewProcThreadAttributeList(maxAttrCount uint32) (*ProcThreadAttributeListContainer, error)` (exec_windows.go:209), `.Update(attribute uintptr, value unsafe.Pointer, size uintptr) error`, `.Delete()`, `.List()`; `StartupInfoEx{StartupInfo; ProcThreadAttributeList}`; `CreateProcess`; constants `PROC_THREAD_ATTRIBUTE_{PARENT_PROCESS,HANDLE_LIST,GROUP_AFFINITY,PREFERRED_NODE,IDEAL_PROCESSOR,MITIGATION_POLICY,UMS_THREAD,PROTECTION_LEVEL,PSEUDOCONSOLE}`; `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole`.
- **`PROC_THREAD_ATTRIBUTE_JOB_LIST` is NOT defined** in x/sys/windows v0.48.0 (`grep -rni 'JOB_LIST\|0x0002000d'` over the module: no hits), nor does Go's `syscall.SysProcAttr` expose a job list (grep in `$GOROOT/src/syscall/exec_windows.go` shows only `AdditionalInheritedHandles`, `ParentProcess`). `Update` takes an arbitrary `uintptr`, so define the constant yourself: from Windows SDK `WinBase.h` (microsoft/win32metadata raw, `$P/winbase.h`): `ProcThreadAttributeJobList = 13` and `#define PROC_THREAD_ATTRIBUTE_JOB_LIST ProcThreadAttributeValue(ProcThreadAttributeJobList, FALSE, TRUE, FALSE)` with `PROC_THREAD_ATTRIBUTE_INPUT = 0x00020000` => **0x0002000D** (derived arithmetic). Microsoft Learn (UpdateProcThreadAttribute): "`PROC_THREAD_ATTRIBUTE_JOB_LIST` ... pointer to a list of job handles to be assigned to the child process, in the order specified. Supported in **Windows 10 and newer**." and "lpValue must persist until the attribute list is destroyed".
- Compiled (not run) with `GOOS=windows GOARCH=amd64` (`$P/job/main.go`): CreateJobObject + SetInformationJobObject(KILL_ON_JOB_CLOSE) + NewProcThreadAttributeList(1) + Update(0x2000D, &jobHandle, size) + CreateProcess(EXTENDED_STARTUPINFO_PRESENT|CREATE_SUSPENDED); `go vet` and `go build` OK.

**Needs Windows (cannot test)**: whether job assignment via attribute list works with our spawn path; nested-job behavior when the app is itself launched inside a job (e.g. by a launcher or CI); `SetInformationJobObject` returning ERROR_ACCESS_DENIED in some contexts; handle inheritance; Windows 10 floor (a Win7/8 target would need AssignProcessToJobObject after CREATE_SUSPENDED).

**Concern**: go-pty children cannot use JOB_LIST (item 10); simplest robust pattern is to assign the app's own process to a KILL_ON_JOB_CLOSE job at startup.

---

## 15. Skipped (per brief).

---

## Items that remain UNVERIFIED (reason)
- Wails v3 open issue list / known problems (GitHub UI/API unreachable; docs site blocked).
- Anything executed on real Windows: WebView2, NSIS/MSIX packaging, Credential Manager, ConPTY, Job Objects, Defender/SmartScreen reactions, native MinGW build, Windows NTFS SQLite performance.
- Real-provider behavior (Anthropic/OpenAI/OpenRouter responses); only SDK parsing against local fakes was run.
- ACP schema v2 stability; MCP go-sdk end-to-end with a real server; Aider `grep_ast` license.
- Go `os/exec` + `.cmd` launcher (npx.cmd) argument-escaping behavior on Windows for ACP/MCP subprocesses.

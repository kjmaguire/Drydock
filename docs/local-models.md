# Drydock: models on your PC with llama.cpp (decision P65)

Status (2026-10-15): built and tested on Linux with labelled test doubles and protocol fixtures; nothing here has run against
the real llama.cpp, GitHub or Hugging Face yet (see "Not verified").

## 1. What it is for

Some people want a model that never leaves their computer: for the cost, for the privacy, or because they have a GPU idle.
Drydock already treats a self-hosted OpenAI-compatible server (vLLM, SGLang, Ollama) as a provider of `kind=host`
(`docs/model-routing.md` section 4). This feature makes Drydock the one that installs and runs such a server, on Windows,
with llama.cpp: the person installs a build, downloads a model and starts it from the Providers page; the server then is an
ordinary provider that the router and the agents use like any other.

The engine is **llama.cpp** (ggml-org, MIT), through its `llama-server` program, for the reasons the owner gave: it is the
reference engine for GGUF models, it runs on every GPU a Windows PC has (CUDA, Vulkan, ROCm) and on the CPU, it publishes a
weekly Windows build, and its server speaks the OpenAI chat completions API that Drydock's wire layer already knows
(`internal/llm/openai`).

## 2. Where the pieces come from, and how each is checked

| Piece | Source | Check before use |
|---|---|---|
| The llama.cpp build | the newest GitHub release of `ggml-org/llama.cpp` that carries a Windows x64 archive for the chosen backend (the weekly builds, tagged `bNNNN`, marked pre-release; the tagged `vX.Y.Z` releases carry no binaries) | the archive's SHA-256 must match the digest GitHub recorded when the maintainer uploaded it (the releases API's `digest` field); an asset without a digest is never offered; only `https://github.com/ggml-org/llama.cpp/releases/download/...` is fetched |
| The CUDA runtime (CUDA builds only) | the `cudart-llama-bin-win-cuda-*.zip` of the same release | the same digest check; its DLLs are unpacked beside `llama-server.exe` |
| Models (GGUF files) | public repositories on the Hugging Face Hub (`huggingface.co`); gated and private repositories are not offered (Drydock holds no Hugging Face token) | each file's SHA-256 must match the one the Hub records for it (the LFS object id); a file without one is not offered; a mismatch deletes the file |
| The TurboQuant fork (opt-in) | `AtomicBot-ai/atomic-llama-cpp-turboquant`, its tagged releases (its "Dev latest" pre-releases are skipped) | the same digest check, and a warning on the page: a community build Drydock has not reviewed |

The archive is unpacked with the same guarded unzip the CI runners use (`internal/runners`: no absolute names, no `..`, no
links, size limits), into Drydock's own folder: `<data>\local-models\<source>-<tag>-<backend>\`. Models go to
`<data>\local-models\models\<owner>\<repo>\<variant>\`. Nothing is written anywhere else, and nothing is added to PATH.

**Built in, not side-loaded.** The person installs nothing by hand: the runtime card says so before Install ("Drydock
downloads and runs llama.cpp itself: the newest Windows build of the source below, for the backend you pick, checked against
the digest GitHub recorded and unpacked into Drydock's own folder. Nothing has to be installed by hand, and nothing is added
to the PC's PATH"), and the installed card says the build was downloaded and is run by Drydock, with Update fetching the
newest one the same way. The Source box offers the official build (default) and the TurboQuant fork, the fork behind its
warning (2026-10-16, on the owner's question whether llama.cpp and TurboQuant could be "built right into the app").

**What leaves the machine.** The release listing (`api.github.com`, unauthenticated, 20 releases), the archives
(`github.com`), the model listings and files (`huggingface.co` and its download hosts). No code, no prompt and no project
content: the server runs on `127.0.0.1` and never calls out. Drydock sends no token to either site.

## 3. Backends

| Backend | For | Official archive |
|---|---|---|
| Vulkan (suggested when no NVIDIA driver is found) | any recent GPU through its Vulkan driver (AMD, Intel, NVIDIA) | `llama-<tag>-bin-win-vulkan-x64.zip` |
| CUDA (suggested when `nvidia-smi` is installed) | NVIDIA GPUs with a driver from 2025 or newer (CUDA 13) | `llama-<tag>-bin-win-cuda-13.*-x64.zip` (the newest CUDA 13 toolkit the release carries; 13.4 on 2026-10-15) + the `cudart-llama-bin-win-cuda-<same version>-x64.zip` beside it |
| CUDA 12 | NVIDIA GPUs with an older driver | `llama-<tag>-bin-win-cuda-12.*-x64.zip` + its cudart |
| ROCm | recent AMD GPUs through HIP | `llama-<tag>-bin-win-rocm-*-x64.zip` (10.0 on 2026-10-15) |
| CPU only | no GPU | `llama-<tag>-bin-win-cpu-x64.zip` |

The toolkit version in the CUDA and ROCm names is matched as a pattern, not pinned: when upstream moves from CUDA 13.4 to
13.5 the next weekly build still installs. A new CUDA major (14) would need a Drydock release, since the drivers it needs
are a decision, not a version bump.

After an install Drydock runs `llama-server --list-devices` once and shows what the build found ("CUDA0: NVIDIA GeForce RTX
4060 Ti (15944 MiB, 14143 MiB free)"); a build that finds no device runs on the CPU.

## 4. Models

**Finding one.** "Show suggestions" lists ggml-org's own GGUF repositories, most downloaded first: the models llama.cpp's
authors convert and keep current. Search finds any public GGUF repository by name. While a list is shown, "Close
suggestions" (or "Close results") takes it and the repository open in it off the page again, so the section shrinks back;
Show suggestions then returns (the owner asked for this, 2026-10-16). A repository's files are grouped into
**variants** by quantization (`Q4_K_M`, `Q8_0`, `MXFP4`...; a model split into `-00001-of-00003.gguf` shards is one variant
of several files); projector files for vision (`mmproj`) are left out, Drydock serves text models.

**Fit.** Each variant is marked by a simple rule (`localllm.FitOf`): a file that fits the free memory of one GPU "Fits the
GPU"; one that fits three quarters of the free RAM "Runs on the CPU (slow)" (partly offloaded when there is a GPU); the rest
is "Not recommended". The badge stays short; its tooltip and, for the two that will not run well, a sentence in the row say it
in full with the numbers the core measured (`Fit.availableBytes`, the larger of the free VRAM and the RAM budget): "Not
recommended: too big for this PC (needs about 80.0 GB, 15.0 GB free)", "Runs on the CPU (slow): no GPU has room for it, so
llama.cpp keeps it in RAM (needs about 32.0 GB, 24.0 GB free)". A variant that is not recommended cannot be downloaded (the
button is disabled and says why): a model llama.cpp could not load would only take gigabytes of disk. This counts the weights
only, not the KV cache; a model near the limit may still not load with a large context. For the rest the person decides;
the fit is a hint.

**Downloading.** Files download in the background (several at once if asked), resume a part that was cut off, and are
checked before they count as present. A drive with less than the file plus 2 GB free refuses the download.

## 5. The server

One `llama-server` runs at a time, on one model:

```
llama-server --host 127.0.0.1 --port 9933 -m <file.gguf> --alias <owner/repo:VARIANT> --jinja --no-webui -ngl auto
             -ctk q8_0 -ctv q8_0 [-c <context>]
```

- `--alias` makes the model's id in `GET /v1/models` Drydock's own (`ggml-org/gpt-oss-20b-GGUF:MXFP4`), so the catalog, the
  router and the usage rows name it the same way the Hub does (and the way `llama-server -hf` does).
- `--jinja` turns on the chat template engine, which is what makes tool calling work for models whose template has it.
- `-ngl auto` lets llama.cpp put as many layers on the GPU as fit.
- **KV cache** (`-ctk`/`-ctv`): `q8_0` by default (near-lossless, half the memory of `f16`); `q4_0` for a quarter. Since
  April 2026 llama.cpp rotates the attention inputs with a Hadamard transform before quantizing them (its PR #21038), which
  gives the quantized cache most of the quality gain Google's TurboQuant paper describes. TurboQuant's own types
  (`turbo3`, `turbo4`) exist only in community forks; the maintainers closed the pull request adding them (June 2026) for lack
  of evidence they beat the existing types at equal bits. The official build therefore offers `q8_0`, `q4_0`, `f16`; the
  fork, when a person opts into it, adds `turbo4` and `turbo3`.
- The port is 9933 by default (one above llama.cpp's own 9931, so a `llama-server` the person runs by hand keeps its port).
- The server binds the loopback address only. It is started with a new random API key for every start (the environment variable
  `LLAMA_API_KEY`, never the command line, which other programs can read) and demands it on every request but `/health` and the
  model list; the provider row made for it holds the key (in the vault, like any provider's), so Drydock's own calls carry it and
  another program on the PC gets a 401 (audit 2026-10-16, L32). The server starts with the same trimmed environment the device
  probe uses (`proc.BaseEnvNames`), none of the app's tokens. A model's folder name is made safe (`.` and `..` are not names), so a
  file the Hub calls `...gguf` is not offered and no model id can name a folder outside the store (L33).

Drydock waits for `GET /health` to answer 200 (the model is loaded; up to 15 minutes) and for `GET /props` to accept the key (so
a different program that took the port is not taken for the server), then makes the server a provider:
name "Models on this PC (llama.cpp)", `kind=host`, `flavor=llamacpp`, `data_class=private`, `tier=cheap`,
`max_concurrent=1`, base URL `http://127.0.0.1:9933/v1`. The provider probe (`internal/providers`) tells a llama-server apart
by `GET /props`, lists its model as taking tool calls (the listing says nothing about it; the server's template handles
them) with the context the server runs with (`default_generation_settings.n_ctx`), and warns below 64k. The health loop
checks `/health` every 30 s like any host, so a stopped server shows as down and the router skips it.

Stopping the server keeps the provider row (its prices, quotas and history); the model cannot be removed while served.
"Start the server with Drydock" (on by default once a model was served) serves the last model again at the next start.

Changing the cache type, the context size or the port takes effect at the next start.

## 6. Settings

| Setting | Default | Stored |
|---|---|---|
| Source | official | `settings` key `local:settings` (JSON: source, backend, cacheType, contextSize, port, modelId, providerId, autostart) |
| Backend | the suggested one (CUDA with an NVIDIA driver, else Vulkan) | |
| KV cache type | `q8_0` | |
| Context size | 0: the model's own | |
| Port | 9933 | |
| Autostart | on | |

Only a person may install, download, start, stop, remove or change these: they bring programs and gigabytes onto the PC
and take its GPU.

## 7. Interfaces

Core methods (`internal/core/ext_localllm.go`): `local.get` (the whole view: runtime, devices, models with fit, downloads
in progress, server state, settings, memory, disk), `local.install {source?, backend?}`, `local.runtime.remove`,
`local.settings.set` (any subset of the settings), `local.models.suggested`, `local.models.search {query}`,
`local.model.variants {repo}`, `local.model.download {repo, variant}`, `local.model.download.cancel {id}` (also clears a
finished row), `local.model.remove {id}`, `local.server.start {modelId}`, `local.server.stop`. Event: `local.changed`
`{what: install | download | server | settings | models, ...}`; progress is read from the view, not from events.

UI: the "Models on this PC" section at the top of the Providers page (`ui/app/screens/providers/local-models.tsx`).

## 8. Tests

- `internal/localllm`: release picking (digest required, drafts skipped, the fork's dev builds skipped, CUDA needs its
  runtime), install (download, check, unpack, cudart merged, old builds pruned, a mismatch or a missing program refused),
  the device list parser and probe, the fit rule, the Hub (listings, gated repositories hidden, variants and shards,
  download with resume and a deleted mismatch), the store, the server (start waits for the load, arguments, log, stop, a
  program that fails or never becomes ready, a port in use). The stand-in `llama-server` is the test binary itself
  (`internal/localllm/llamatest`, a labelled test double); GitHub and the Hub are protocol fixtures in the same package.
- `internal/core/ext_localllm_test.go`: the whole path through the RPC methods, ending with a provider of flavor `llamacpp`
  whose model takes tool calls; a failing build; a restart.
- `internal/providers/probe_test.go`: the llama-server probe.
- `ui/e2e/local-models.spec.ts` and the section's browser tests, against the e2e harness's fixtures.

## 9. Not verified

- Anything against the real llama.cpp: that the weekly release's asset names stay as read on 2026-10-15
  (`llama-b11669-bin-win-{cpu,vulkan,cuda-12.4,cuda-13.4,rocm-10.0}-x64.zip`, `cudart-llama-bin-win-cuda-*-x64.zip`), that
  `llama-server.exe` sits at the archive's top, that the CUDA build finds the cudart DLLs beside it, the `--list-devices`
  output, how long a real model takes to load, and that a real model answers tool calls through `--jinja`.
- Anything against the real Hub: the listing and tree API shapes were written from their documentation.
- The TurboQuant fork: its archive layout and whether its CUDA archives carry the CUDA runtime (no cudart is fetched for it).
- The owner must run it once on the PC: install (Vulkan or CUDA), download a small model (ggml-org's gpt-oss-20b at MXFP4
  is 12 GB; a Qwen3 4B at Q4_K_M is 2.5 GB), serve it, run Test on the provider, and give one task to an agent on it.

## 10. Not built

- Several models loaded at once (llama-server's router mode, `--models-dir`): one model at a time, switched by a restart.
- Vision models (`mmproj`), embeddings, speculative decoding, draft models.
- A Hugging Face token for gated models.
- Linux and macOS: the archives offered are Windows x64 only (Drydock is Windows-only; the development host runs the
  stand-in).

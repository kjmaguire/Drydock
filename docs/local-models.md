# Drydock: models on your PC with llama.cpp (decision P65)

Status (2026-10-15; several models, vision, drafts, embeddings and gated models added 2026-10-29, decision P111, section 11;
one model loaded or unloaded beside the others, the embedding model ranking pack searches, MTP and EAGLE-3 draft heads added
2026-10-29, decision P120, section 12):
built and tested on Linux with labelled test doubles and protocol fixtures; nothing here has run against the real llama.cpp,
GitHub or Hugging Face yet (see "Not verified").

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
| Models (GGUF files) | public repositories on the Hugging Face Hub (`huggingface.co`), and gated ones with the person's Hugging Face token (section 11); private repositories are not offered | each file's SHA-256 must match the one the Hub records for it (the LFS object id); a file without one is not offered; a mismatch deletes the file |
| The TurboQuant fork (opt-in) | `AtomicBot-ai/atomic-llama-cpp-turboquant`, its tagged releases (its "Dev latest" pre-releases are skipped) | the same digest check, and a warning on the page: a community build Drydock has not reviewed |

The archive is unpacked with the same guarded unzip the CI runners use (`internal/runners`: no absolute names, no `..`, no
links, size limits), into Drydock's own folder: `<data>\local-models\<source>-<tag>-<backend>\`. Models go to
`<data>\local-models\models\<owner>\<repo>\<variant>\`. Nothing is written anywhere else, and nothing is added to PATH.

**Built in, not side-loaded (decision P95).** The engine is Drydock's own and the page never calls it llama.cpp. It is the
TurboQuant build: from the download's tools folder at the first start, or, with `AutoSetup` (the desktop app) when the folder
did not carry it, fetched from the fork's releases at start with the digest check above, for the suggested backend when the
fork builds it, else Vulkan, with `turbo4` as the cache type unless the person chose one. While that runs the card says
"Setting up local models" with the progress; when the start could not do it, the card offers **Set up** (the same, with
nothing to choose) and, after a failure, **Retry**. An installed engine reads "TurboQuant build <tag> · <backend>" and has one
button, **Check for updates**, which installs a newer build of the same source and backend (it reads **Update to <tag>** when
the twice-daily check found one). The page offers no source, backend, Update or Remove for the engine any more; the official
build stays a source the core can install (`local.install`), and a build installed earlier keeps its source.

**What leaves the machine.** The release listing (`api.github.com`, unauthenticated, 20 releases), the archives
(`github.com`), the model listings and files (`huggingface.co` and its download hosts). No code, no prompt and no project
content: the server runs on `127.0.0.1` and never calls out. Drydock sends no token to GitHub; the person's Hugging Face token,
when there is one, goes only as the Authorization header of a file download from `huggingface.co` itself (section 11).

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
of several files); a projector file for vision (`mmproj`) is no variant of its own but goes with each variant (section 11).

**Fit.** Each variant is marked by a simple rule (`localllm.FitOf`): a file that fits the free memory of one GPU "Fits the
GPU"; one that fits three quarters of the free RAM "Runs on the CPU (slow)" (partly offloaded when there is a GPU); the rest
is "Not recommended". The badge stays short; its tooltip and, for the two that will not run well, a sentence in the row say it
in full with the numbers the core measured (`Fit.availableBytes`, the larger of the free VRAM and the RAM budget): "Not
recommended: too big for this PC (needs about 80.0 GB, 15.0 GB free)", "Runs on the CPU (slow): no GPU has room for it, so
llama.cpp keeps it in RAM (needs about 32.0 GB, 24.0 GB free)". A variant that is not recommended cannot be downloaded (the
button is disabled and says why): a model llama.cpp could not load would only take gigabytes of disk. A variant on the Hub is
counted by its weights only. A downloaded model's fit counts its KV cache too (decision P97, `Fit.kvBytes`, `Fit.needBytes`):
the cache is estimated from the model's GGUF header (`localllm.ReadGGUF`, pure Go, only the metadata is read): for each layer
the attention heads that keep keys and values (`<arch>.attention.head_count_kv`, per layer when the model gives a list) times
the key and value lengths (`attention.key_length`/`value_length`, else `embedding_length / head_count`), times the context size
(the setting, else the model's own `context_length`), times the bytes a value takes in the cache type (f16 2, q8_0 34/32, q4_0
18/32, and the other ggml block types). A model with sliding-window layers or a compressed (MLA) cache is counted at the full
size and the estimate says it is an upper bound. The TurboQuant build's `turbo3` and `turbo4` are not documented by the fork, so
their estimate is unknown (`kv.known` false, with the reason), as is a model whose header has no attention heads (a recurrent
model) or cannot be read; the fit then counts the weights alone. For the rest the person decides; the fit is a hint.

**Downloading.** Files download in the background (several at once if asked), resume a part that was cut off, and are
checked before they count as present. A drive with less than the file plus 2 GB free refuses the download.

## 5. The server

One chat `llama-server` runs at a time (beside it, the embedding server of section 11). On one model it is started so; on
several it runs llama.cpp's router mode (section 11):

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
name "Models on this PC" (one registered before decision P95 keeps "Models on this PC (llama.cpp)"), `kind=host`, `flavor=llamacpp`, `data_class=private`, `tier=cheap`,
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
| Source | official | `settings` key `local:settings` (JSON: source, backend, cacheType, contextSize, port, modelId, providerId, autostart, loaded, drafts, embeddingModel, embeddingPort, rankPacks) |
| Backend | the suggested one (CUDA with an NVIDIA driver, else Vulkan) | |
| KV cache type | `q8_0` | |
| Context size | 0: the model's own | |
| Port | 9933 | |
| Autostart | on (serves the models loaded last time, and the embedding model) | |
| Models kept loaded | the ones served last (`loaded`; `modelId` is the first) | |
| Draft of a model | none (`drafts`, by the model's id: a draft model's id, or `mtp` for its own MTP heads) | |
| Embedding model, embeddings port | the one served last; 9934 | |
| Rank pack searches by meaning | off (`rankPacks`) | |
| Embedding model or not | what the model's header says | the model's own `model.json` (`embedding`, the person's mark) |
| Hugging Face token | none | the credential store (the vault: Windows Credential Manager), reference `localmodels:hf-token`; never in `settings` |

Only a person may install, download, start, stop, remove or change these: they bring programs and gigabytes onto the PC
and take its GPU.

## 7. Interfaces

Core methods (`internal/core/ext_localllm.go`, `ext_localllm_models.go`): `local.get` (the whole view: runtime, devices,
models with fit, `kv`, `projector`, `embedding`, `drafts`, `draft`, `loadBytes`, `mtp` and `eagle3`, downloads in progress (a download the Hub
refused for a gated model has `licence`, its page), server state (with `startedAt`, `autostarted`: Drydock started it at its
own start, the `cacheType` and `contextSize` it runs with, `models` it keeps loaded, `router`, the models it is `loading` beside
them and the `note` that says why it was started again when a change could not be made on it), `served` (the served models'
`modelBytes`, `kvBytes` together, `kv`, `models`, and the `device`, gpu or ram, with its `deviceTotal` and `deviceFree` as read
by the device probe, for the memory bar), `load` (`max`, `budgetBytes`, `loadedBytes`), `embed` (the embedding server's
state), `hfToken` (`set`, `masked`), `usedBy` (every agent, of any project, whose provider is the server's row: `agentId`,
`name`, `projectId`, `projectName`), settings, memory, disk), `local.install {source?, backend?}`, `local.runtime.remove`,
`local.settings.set` (any subset of the settings), `local.models.suggested`, `local.models.search {query}`,
`local.model.variants {repo}`, `local.model.download {repo, variant}`, `local.model.download.cancel {id}` (also clears a
finished row), `local.model.remove {id}`, `local.server.start {modelId}` or `{modelIds}` (the models to keep loaded together),
`local.server.stop`, `local.model.set {id, embedding?, draft?}` (`draft: "mtp"` for a model's own MTP heads), `local.embeddings.start {modelId}`, `local.embeddings.stop`,
`local.hfToken.set {token}`, `local.hfToken.remove`. Event: `local.changed` `{what: install | download | server | embeddings |
settings | models, ...}`; progress is read from the view, not from events.

UI: Models on this PC, the this-pc detail of the Models page (`ui/app/screens/models/this-pc.tsx`, helpers in `models.ts`).

## 8. Tests

- `internal/localllm`: release picking (digest required, drafts skipped, the fork's dev builds skipped, CUDA needs its
  runtime), install (download, check, unpack, cudart merged, old builds pruned, a mismatch or a missing program refused),
  the device list parser and probe, the fit rule, the Hub (listings, a gated repository marked, variants and shards, the
  projector picked and downloaded with its variant, download with resume and a deleted mismatch, a gated download with and
  without the token, the token sent to the Hub's download address only and not to the CDN it redirects to), the store (the
  projector and the embedding mark kept), the header (tokenizer, pooling, which model can draft for which), the server (start
  waits for the load, arguments, log, stop, a program that fails or never becomes ready, a port in use; a projector and a
  draft on one model; an embedding server; the router with two models: its preset, its arguments, its cache folder, the wait
  for every model, routing by the model field; a model the router cannot load, more than four, a path the preset cannot
  carry). The stand-in `llama-server` is the test binary itself (`internal/localllm/llamatest`, a labelled test double, both
  of llama-server's modes); GitHub and the Hub are protocol fixtures in the same package (the Hub with gating and a CDN).
- `internal/core/ext_localllm_test.go`: the whole path through the RPC methods, ending with a provider of flavor `llamacpp`
  whose model takes tool calls; a failing build; a restart. `ext_localllm_models_test.go`: two models kept loaded with a
  draft and a projector (the router, the provider's two models), what does not fit refused, the embedding model served on its
  own, a gated model downloaded with the token (which is in no answer, setting or event).
- `internal/providers/probe_test.go`: the llama-server probe, and the router's listing (only loaded models, each with its
  own context).
- `ui/e2e/models.spec.ts` (Load too and Unload against the real core and the stand-in: the first Load too starts the server
  again as the router, then Unload and Load too run on it, as its log shows) and the page's browser tests, against the e2e
  harness's fixtures.
- Decision P120 (section 12): `internal/localllm/server_change_test.go` (one model loaded and unloaded on the running router
  while the others answer, the router's calls in order, down to one model and back; what needs a restart, a server without the
  calls, a model that fails to load), `draftheads_test.go` (MTP heads read from the header and the tensor list, a split file,
  EAGLE-3 pairs, the spec types on the command line and in the preset), `internal/core/ext_localllm_inplace_test.go` (Load too
  and Unload through the RPC methods without a new start or key, a restart that says why, the draft heads on the page's view)
  and `ext_localllm_embed_test.go` (pack searches ranked by meaning).

## 9. Not verified

- Anything against the real llama.cpp: that the weekly release's asset names stay as read on 2026-10-15
  (`llama-b11669-bin-win-{cpu,vulkan,cuda-12.4,cuda-13.4,rocm-10.0}-x64.zip`, `cudart-llama-bin-win-cuda-*-x64.zip`), that
  `llama-server.exe` sits at the archive's top, that the CUDA build finds the cudart DLLs beside it, the `--list-devices`
  output, how long a real model takes to load, and that a real model answers tool calls through `--jinja`.
- The router's `/models/load`, `/models/unload` and `GET /models?reload=1` against a real build (decision P120): their shapes
  were read from `tools/server/README.md` and `tools/server/server-models.cpp` at b11936 and the TurboQuant fork's README at
  b10298-2.0.0; that the fork's router reloads its preset the same way, that a kept model whose preset section is unchanged
  stays loaded through the reload, and how long a real load takes beside running models were not seen. MTP heads and EAGLE-3
  drafts on a real build: the flags (`--spec-type draft-mtp`, `draft-eagle3`), the architecture `eagle3` and the MTP tensor
  name were read from both builds' `common/arg.cpp`, `common/speculative.cpp` and `src/llama-arch.cpp`; the fork names an
  EAGLE-3 draft's layer list `eagle3.extract_layers` where b11936 names it `eagle3.target_layers`, so an EAGLE-3 file converted
  for one may not load in the other (not seen either way); whether either speeds a real model up was not measured.
- Router mode, projectors, drafts and embeddings against a real build: the flags and the preset keys were read from
  llama.cpp's `tools/server/README.md`, `common/arg.cpp` and `common/preset.cpp` at build b11936 and from the TurboQuant fork's
  README at b10298-2.0.0 (both have `--models-preset`, `--models-max`, `--mmproj`, `--model-draft`, `--spec-type
  draft-simple` and `--embeddings`); that the router's instances inherit `-ngl`, `-ctk`, `-ctv` and `-c` from its command line
  as documented, that `LLAMA_CACHE` keeps the person's own llama.cpp cache out of its listing, and that a real draft pair
  speeds a model up were not seen. The Job Object that ends the server also ends the router's instances (they are its
  children); not seen on Windows either.
- Anything against the real Hub: the listing, the single-repository and tree API shapes, and the 401 and 403 a gated
  download gets, were written from their documentation; the token has not been sent to the real Hub.
- The TurboQuant fork: its archive layout and whether its CUDA archives carry the CUDA runtime (no cudart is fetched for it).
- The owner must run it once on the PC: install (Vulkan or CUDA), download a small model (ggml-org's gpt-oss-20b at MXFP4
  is 12 GB; a Qwen3 4B at Q4_K_M is 2.5 GB), serve it, run Test on the provider, and give one task to an agent on it; then
  Load too a second small model, a vision model (ggml-org's gemma-3-4b-it), a draft pair (a Qwen3 0.6B for a Qwen3 8B) and
  an embedding model (Qwen3-Embedding-0.6B), and a gated model with a token; with three loaded, Unload one and Load too it
  again while an agent works on another (decision P120), choose a model's own MTP heads and an EAGLE-3 draft (AngelSlim's
  Qwen3-4B one, converted for the installed build), and search a pack with ranking by meaning on and off.

## 10. Not built

- Linux and macOS: the archives offered are Windows x64 only (Drydock is Windows-only; the development host runs the
  stand-in).
- A projector's memory in the KV estimate beyond its file: what the vision encoder takes while it reads an image depends on
  the image and the batch, which no GGUF number says, so it is not counted (as in llama.cpp's own router measurements; decision
  P129 looked and left it so). The MTP context's memory is counted since P129 (section 13).
- The TurboQuant fork's older MTP layouts (a Gemma 4 assistant of before its b10018 sync, `gemma4_assistant`, and the
  standalone `qwen35*_mtp` NextN files) run on neither build at the tags read: they are marked and offered to nothing (section
  14). A Gemma 4 assistant of today's layout is offered since P132.
- The first Load too on a server of one model starts it again as the router (a server of one model has no router calls); from
  then on models are loaded and unloaded on it.
- An embedding index: the embedding model only orders the passages BM25 found (section 12).
- Not verified (P129): a real DFlash, DSpark or MTP head file against either build, the MTP context's real memory against the
  estimate, Windows. Not verified (P132): a real Gemma 4 assistant against either build, and whether it speeds its model up.

## 11. Several models, vision, drafts, embeddings and gated models (decision P111)

**Several models at once.** A person keeps up to four downloaded models loaded (llama.cpp's own `--models-max` default). Each
row has **Load too** while the server runs, and a loaded row **Unload** while there are several; the Server group says
"Serving 2 models" and the room they take ("2 of 4 models loaded · 18 of 24 GB"). The room is the fit rule's: the free memory
of the GPU with the most, or three quarters of the free RAM, whichever is more; each model counts its files, its KV cache at
the server's settings and its draft's (`loadBytes`). What does not fit beside the loaded ones is refused, on the page (Load
too disabled, its reason as the tooltip) and by the core (`local.server.start` with `modelIds`); one model alone is never
refused (the fit stays a hint). Two models or more run llama-server's router mode:

```
llama-server --host 127.0.0.1 --port 9933 --models-preset <data>\local-models\router\models.ini --models-max 4 --jinja --no-webui
             -ngl auto -ctk q8_0 -ctv q8_0 [-c <context>]
```

with `LLAMA_CACHE=<data>\local-models\router\cache` (an empty folder, so the router lists no model a person downloaded with
llama.cpp by hand) and the key in `LLAMA_API_KEY` as before. The preset has a section per model, named by its id (the name the
router lists and routes by): `model`, `mmproj`, `model-draft` and `spec-type`, `load-on-startup = true`. llama.cpp's preset
reader ends a value at `;` or `#`, so a path holding one is refused rather than cut. The start waits until the router's listing
says every model is `loaded`; one whose instance ends fails the start with its name. The provider is the same row: the probe
reads the router's listing, offers each model that is loaded (or asleep), never one loading, unloaded or failed, and reads each
one's context from `/props?model=`, since the router's own `/props` names none. A request names its model in the `model`
field, which Drydock's wire layer always sends. A change to the set while the router runs is made on it, one model at a time (decision
P120, section 12).

**Vision.** A repository's projector (`mmproj`, the one in the variant's folder first, F16 before BF16 before Q8_0) is
downloaded and checked with each variant, counted in its size, kept in its `model.json`, and passed with `--mmproj` (one
model) or `mmproj` (the router). The row says "vision".

**Drafts (speculative decoding).** A model can be drafted for by a downloaded model that is at most half its size, of the same
family (the architecture without its `moe` suffix: a dense Qwen3 for a Qwen3 MoE) and with the same tokenizer (its kind, the
same first and last tokens, a vocabulary within llama.cpp's 128 tokens of slack), read from both GGUF headers
(`localllm.DraftFits`). Only such a model is offered in the row's Draft choice; the draft runs with `--model-draft` and
`--spec-type draft-simple` (llama.cpp infers no type from a plain draft model's header). A draft that is gone or no longer
fits is left out at the start, and logged; a draft cannot be removed while its model is served.

**Embeddings.** A model whose header names a pooling type, or has an encoder architecture (BERT and its kin), is an embedding
model; the person can say otherwise with the row's "Embedding model" box (kept in the model's `model.json`). An embedding
model is never served for chat and is not among the models kept loaded: **Serve embeddings** starts a second llama-server of
its own, with `--embeddings` and without `--jinja`, at the embeddings port (9934), with a key of its own for every start. The
Embeddings group shows it and Stop. It is not a provider (the router would offer it for chat, which it cannot answer); its one use is ranking pack searches (section 12).

**Gated models.** The Hub's listing marks a gated repository (`gated`: "auto" or "manual"), which the page shows. Without a
token the download is refused before anything is fetched: "<repo> is a gated model: Hugging Face shares its files only with an
account that accepted its licence. Accept it at https://huggingface.co/<repo>, then add a Hugging Face token on this page.";
the variant says "needs a token" with a link to that page. The token (Server settings, a password field) is checked to start
with `hf_`, kept in the credential store like a provider's key (`localmodels:hf-token`), taught to the redaction scanner,
shown as `hf_…1234`, and removable. It is sent as `Authorization: Bearer` only with a file download whose address is the Hub's
own; a redirect to another host (the Hub sends the bytes from a CDN at a signed address) drops it, since Go would keep it for
a subdomain such as `cdn-lfs.huggingface.co`. Listings never carry it. A download the Hub refuses (401 without a token, 403 for
an account without access) says which, with the page to accept the licence on.

## 12. One model in or out of the router, embeddings in use, MTP and EAGLE-3 drafts (decision P120)

**One model in or out while the others run.** While the router runs, Load too and Unload (and Serve or Switch model to another
set) change it in place (`localllm.Server.Change`): the models that go are unloaded (`POST /models/unload {model}`), the preset
is written with the new set and read again (`GET /models?reload=1`: a model taken out is dropped from the listing, a new one is
listed unloaded), each model that comes is loaded (`POST /models/load {model}`), and the call waits until the listing says each
is `loaded`, as llama.cpp's `tools/server/README.md` and `server-models.cpp` at b11936 describe them (the fork's README at
b10298-2.0.0 has the same calls). Meanwhile the models kept answer as before, the server's state lists the ones `loading`
(their rows say "loading"), and the process, its start time and its key stay the same, so the provider row is only probed
again. The router is started with `--models-max 4` (not the number it starts with), so loading one more never makes it unload
the one used least recently; down to one model it stays a router, so the next Load too is again in place. A change that cannot
be made there starts the server again on the new set as before: a server of one model (it has no router), a setting of the
router's command line changed since the start (cache type, context, port, the build), or a kept model whose section of the
preset changed (its draft or projector), since the router would unload it anyway. A server that does not answer the calls as
documented (a 404 for the call, an error, no answer) is started again too, and then says so: the Server group shows the note
"Started again on the new set of models: <what it answered>." and the log has the line. A model that fails to load (its
instance ends) fails the change with its name and leaves the others loaded. The page's tooltips say which: "Load it beside the
models served: they keep running while it loads", "Unload it: the other models keep running", and on a server of one model
"the server starts again with both".

**The embedding server in use.** Its one use is ranking pack searches by meaning, the person's choice ("Rank pack searches by
meaning" under Embeddings, `rankPacks`, off by default): see [`expert-packs.md`](expert-packs.md), "As built: ranking by meaning".
`localllm.Server.Embed` is its client: at most 64 texts a call, 127.0.0.1, the key of the embedding server's start, 20 s.

**MTP heads.** A model whose GGUF header names MTP layers (`<arch>.nextn_predict_layers`) and whose tensor list holds the tensor
llama.cpp itself looks for (`blk.<last block>.nextn.eh_proj.weight`, in `common_speculative_types_from_gguf`; for a model split
into several files, in any of them) carries its own multi-token prediction heads (GLM-4.5 and Qwen3.5 among them, when their
converter kept them): the row says "MTP" and its Draft choice offers "Its own MTP heads" (`drafts` holds `mtp`). Chosen, the
model runs with `--spec-type draft-mtp` and no draft file (both builds create the MTP context against the model itself), in the
preset `spec-type = draft-mtp`. A model that names the layers without the tensors (left out by its converter) is not offered
them, and a person cannot choose them for it.

**EAGLE-3 drafts.** A downloaded model of architecture `eagle3` is an EAGLE-3 draft: a one-layer model converted for one target
(`convert_hf_to_gguf.py --target-model-dir`), whose tokenizer it inherits. It is offered as the draft of a model with that
tokenizer (the checks of a draft model: kind, size within 128 tokens, the same first and last tokens, at most half the size) and,
when the file gives it (`eagle3.target_hidden_size`, the TurboQuant build's key), the same embedding length; the family rule
does not apply to it. Chosen, the model runs with `--model-draft <file> --spec-type draft-eagle3`. The draft's row says
"EAGLE-3 draft" and "drafts for its model": it is never served for chat (refused by the core too), has no Load too and no
embedding mark. Neither build infers a spec type from an EAGLE-3 file, so it is always named.

## 13. DFlash, DSpark and MTP head drafts, the MTP context's memory, a catalog that follows the server (decision P129)

What the flags and the files are was read in both builds' sources at the tags the rest of this page names (b11936 and the
TurboQuant fork's b10298-2.0.0): `common/arg.cpp`, `common/speculative.cpp`, `common/download.cpp`, `docs/speculative.md`,
`src/llama-arch.cpp`, `src/llama-model.cpp` (`create_memory`) and the converter (`convert_hf_to_gguf.py`, `conversion/qwen.py`).

**DFlash and DSpark drafts.** A downloaded model of architecture `dflash` is a DFlash draft: a small block-diffusion model
converted for one target (`--target-model-dir`, whose tokenizer and token embeddings it inherits) that drafts a whole block of
tokens in one pass from the target's hidden states. One whose tensor list holds `markov_w1.weight` (the Markov head; the
official build's `common_speculative_types_from_gguf` looks for that very tensor) is a DSpark draft. Each is offered to a model
with its tokenizer (the checks of a draft model) whose layers reach the deepest one it reads (`dflash.target_layers`, written
as the layer's index plus one); the family rule does not apply, as for EAGLE-3. Chosen, the model runs with `--model-draft
<file> --spec-type draft-dflash` (or `draft-dspark`), in the preset `model-draft` and `spec-type`. The builds' examples also pass
`--spec-draft-n-max` and `-fa on`; Drydock passes neither: the draft size is clamped to the draft's trained block size and the
default of 3 runs, flash attention defaults to `auto`, and the one refusal in the code (`DSpark draft has no confidence head:
please set --spec-draft-p-min 0`) only fires when `--spec-draft-p-min` is above its default of 0. A person who wants other
values sets them on the server's command line themselves.

**An MTP head shipped as a file of its own.** `convert_hf_to_gguf.py --mtp` writes a model's MTP heads alone (the file name
starts `mtp-`): the model's own architecture, its block count with the MTP layers, `<arch>.nextn_predict_layers`, the token
embeddings, the output and the MTP blocks, and no tensor of the blocks before them. Drydock reads the whole tensor list of a file
that names MTP layers: one with the MTP tensor and none of the main blocks is an MTP head file (`mtpHead`), which only drafts.
It is offered to a model of the same architecture, embedding length and main layers (the block count less the MTP layers, so a
model converted with `--no-nextn` and one that names its layers both match), with its tokenizer, and runs with `--model-draft
<file> --spec-type draft-mtp`: the official build loads the draft file into an MTP context when the type is `draft-mtp`
(`common_speculative_init_result`), and the fork's `--mtp-head` is only another name for `--model-draft`, which the official
build alone has. The row says "MTP head", the drafted model "with an MTP head".

**The MTP context's memory.** llama.cpp keeps a model's MTP layers out of its own KV cache (the layer filter of `create_memory`:
`il < n_layer()`) and gives them to the MTP draft context, which is as long as the model's context and has its cache type. The
model's KV estimate therefore counts its main layers only (`EstimateKV`), and the MTP context counts the MTP layers
(`EstimateMTPKV`); a head file's own estimate is its MTP context's. A model drafting with its own heads adds that context to
what it takes loaded (`loadBytes`, shown as `mtpKvBytes`), to its fit and to the served memory; a head file's context is its
draft's cache. Before P129 a model's estimate counted the MTP layers in its own cache and their context not at all, which over
counted a model that did not draft with them; it also added another model's size to the served memory of a model drafting with
its own heads (the draft id "mtp" was looked up as a model), which P129 fixed.

**The catalog follows the server.** After every probe of the local provider that the server answered (a start, a change on the
router, a model that failed to load), the catalog rows of models the listing no longer has are removed
(`providers.Registry.DropUnlisted`): the local server's list is Drydock's own, so a model it does not list was unloaded or left
out of a start on fewer models, and the router must not choose it. Every other provider keeps such rows, as before. A probe
that fails changes nothing.

Tests: `internal/localllm/drafts129_test.go` (the headers of a DFlash, a DSpark and an MTP head file, a model with its own heads in
either tensor order; which models each fits; the MTP context's estimate apart from the model's; the flags on one server and in
the preset), `draftheads_test.go`; `internal/core/ext_localllm_p129_test.go` (offered by kind, refused for chat, run with their
flags, `loadBytes`, `mtpKvBytes`, the fit and the served memory; an Unload and a start on one model drop the catalog rows and a
turn pinned to the dropped model finds no candidate), `ext_localllm_inplace_test.go`; the Models page's unit and browser tests.

## 14. The TurboQuant build's own MTP files (decision P132)

What these files are and how they run was read in the TurboQuant fork's source at b10298-2.0.0 (`src/llama-arch.cpp`,
`src/llama-model.cpp`, `src/models/gemma4-assistant.cpp`, `conversion/gemma.py`, `common/arg.cpp`, `common/speculative.cpp`,
`MTP.md`, `NEXTN.md`) and in the official build at b11936 (`src/llama-arch.cpp`, `src/llama-model.cpp`, `common/arg.cpp`,
`common/speculative.cpp`), fetched through the proxy.

**A Gemma 4 assistant.** Google's drafter for a Gemma 4 model: a few transformer blocks that read the last hidden state of the
model and its KV cache and guess the next tokens. Both builds name its architecture `gemma4-assistant`; the converter
(`Gemma4AssistantModel`) writes every block as an MTP layer (`nextn_predict_layers` is the block count) and the model's hidden
width as `gemma4-assistant.embedding_length_out` (its `backbone_hidden_size`). Drydock reads that key (`EmbeddingLengthOut`) and
marks the file "Gemma 4 assistant" (`draftKind` `assistant`): it is offered to a Gemma 4 model (architecture `gemma4`) whose
hidden state is as wide (its own `embedding_length_out`, else its embedding length; `common_speculative_impl_draft_mtp` asserts
the two widths are equal), that has its tokenizer and is much larger, and runs with `--model-draft <file> --spec-type
draft-mtp`, in the preset `model-draft` and `spec-type = draft-mtp`: both builds load a draft file into an MTP context when the
type is `draft-mtp` (`common_speculative_init_result`); the fork's `--mtp-head` and `-md` are other names of `--model-draft`
and its "mtp" and "nextn" aliases of `draft-mtp` since its b10018 sync (its `MTP.md` still describes the older
`llama_model_load_mtp_from_file` path, which the code no longer has). Both builds give it the model's own cache
(`create_memory` shares the model's last full and sliding-window layers with it), so its estimate is known and nothing:
"It reads its model's cache and keeps none of its own." Since the official build loads it the same way, it is offered on both.
It never chats (refused by the core), has no Load too and drafts for nothing but a Gemma 4 model.

**The fork's older layouts.** Two kinds of MTP file the fork once took run on neither build at these tags: a Gemma 4 assistant
converted before the fork's b10018 sync (architecture `gemma4_assistant`, which the fork's `llama_model_create` refuses by name:
"this Gemma 4 assistant GGUF predates the b10018 sync and is no longer supported", and the official build does not know), and a
standalone Qwen 3.5 NextN file (`qwen35_mtp`, `qwen35moe_mtp`: `NEXTN.md` calls it a fallback, but neither build's
`llama-arch.cpp` names those architectures any more; a Qwen 3.5 model's own heads, in its own file, are the MTP heads of
section 12). Drydock marks such a file "Old MTP file" ("no engine here loads it", `draftKind` `old-mtp`): it is offered to no
model, has no Load too, and the core refuses it for chat with "an MTP file made for an older TurboQuant build: no engine Drydock
installs loads it". Downloading a newer conversion is the way out.

Tests: `internal/localllm/assistant132_test.go` (the headers of an assistant and of the three old kinds; which model an assistant
fits and why another does not; no cache of its own; the flags on one server and in the preset; fixture GGUF headers only),
`internal/core/ext_localllm_p132_test.go` (offered by kind, refused for chat, an old file offered to nothing, what the model takes
loaded, run with its flags); the Models page's `models.test.ts` and `models-screen.test.browser.tsx`. Not verified: a real
assistant file against either build.

// The local models methods (internal/core/ext_localllm.go, decision P65): llama.cpp on this PC. A person installs an official
// build, downloads GGUF models from the Hugging Face Hub and serves one; the server is then an ordinary provider (kind host,
// flavor llamacpp). Installs and downloads run in the background; their progress is in the view (local.get) and announced by
// the event local.changed. Like the rest of the client: no behavior beyond turning Go's nil slices (JSON null) into arrays.

import type { Transport } from './transport.ts';

export type LocalBackend = 'cpu' | 'vulkan' | 'cuda' | 'cuda-legacy' | 'rocm';
export type LocalJobState = 'running' | 'done' | 'failed' | 'cancelled';
export type LocalServerStatus = 'stopped' | 'starting' | 'running' | 'failed';
export type FitTarget = 'gpu' | 'cpu' | 'none' | 'unknown';

/** Where a llama.cpp build comes from (localllm.Source). */
export interface Source {
  id: string;
  name: string;
  repo: string;
  /** Shown before a person picks a source that is not the official one. */
  warning?: string;
  /** The KV cache types the build supports, the recommended one first. */
  cacheTypes: string[];
}

export interface BackendInfo {
  id: LocalBackend;
  label: string;
  for: string;
}

export interface Device {
  id: string;
  /** CUDA | Vulkan | ROCm | CPU | other */
  kind: string;
  name: string;
  totalBytes: number;
  freeBytes: number;
}

export interface Devices {
  /** false: the program did not answer; nothing is known. */
  probed: boolean;
  devices: Device[];
  error?: string;
  /** The display adapters Windows has a driver for, whatever the program found (none off Windows). */
  adapters?: string[];
  /** What the program said about its backends when it listed no GPU, in its own words. */
  note?: string;
}

export interface Memory {
  totalBytes: number;
  availableBytes: number;
  known: boolean;
}

/** Whether a model file of a size can be loaded here (localllm.Fit). */
export interface Fit {
  target: FitTarget;
  fits: boolean;
  availableBytes: number;
  /** A downloaded model's estimated KV cache, and the model with it, which target and fits then compare (decision P97). Absent when the cache could not be estimated. */
  kvBytes?: number;
  needBytes?: number;
}

/** The estimated KV cache of a downloaded model at a context size and cache type (localllm.KVCache, read from its GGUF header). */
export interface KVCache {
  /** false when it cannot be estimated (note says why: the TurboQuant types, a recurrent model, an unreadable header). */
  known: boolean;
  bytes?: number;
  /** The tokens it is for: the setting, or the model's own context when the setting is 0. */
  contextSize: number;
  cacheType: string;
  note?: string;
}

/** An install or a download in progress, or how the last one ended. */
export interface LocalJob {
  kind: 'install' | 'download';
  target: string;
  state: LocalJobState;
  phase?: string;
  file?: string;
  done: number;
  total: number;
  error?: string;
  started: string;
  ended?: string;
  /** The gated model's page on Hugging Face, where its licence is accepted, when the Hub refused the download for it (decision P111). */
  licence?: string;
}

/** The build on this PC. */
export interface LocalRuntime {
  installed: boolean;
  source?: string;
  sourceName?: string;
  tag?: string;
  backend?: LocalBackend;
  server?: string;
  installedAt?: string;
  devices: Devices;
  job?: LocalJob;
  /** A newer build of the same source and backend, found by the update check (decision P77). */
  latest?: string;
  /** The installed build is the one that came with Drydock's download (decision P77). */
  bundled?: boolean;
}

export interface LocalSettings {
  source: string;
  /** "" until chosen: the suggested one is used. */
  backend: LocalBackend | '';
  cacheType: string;
  /** 0: the model's own. */
  contextSize: number;
  port: number;
  modelId: string;
  providerId: string;
  autostart: boolean;
  /** The models the server keeps loaded (decision P111): more than one runs the router. */
  loaded?: string[];
  /** A model's draft model, by the model's id. */
  drafts?: Record<string, string>;
  /** The embedding model served last, on its own server at embeddingPort. */
  embeddingModel?: string;
  embeddingPort?: number;
  /** Pack searches are ranked by meaning with the embedding model while one is served (decision P120; off by default). */
  rankPacks?: boolean;
}

/** The settings a person changes; a field left out stays as it is. */
export interface LocalSettingsEdit {
  source?: string;
  backend?: LocalBackend | '';
  cacheType?: string;
  contextSize?: number;
  port?: number;
  autostart?: boolean;
  embeddingPort?: number;
  rankPacks?: boolean;
}

/** A model repository on the Hub. */
export interface Repo {
  id: string;
  downloads: number;
  likes: number;
  updated: string;
  tags?: string[];
  /** "auto" or "manual" when the Hub shares the files only with an account that accepted the licence (decision P111). */
  gated?: string;
}

export interface File {
  path: string;
  sizeBytes: number;
  sha256: string;
}

/** One quantization of a repository's model: one GGUF file, or the shards of one split file. */
export interface Variant {
  name: string;
  files: File[];
  sizeBytes: number;
  quant?: string;
  fit?: Fit;
  notes?: string[];
  /** The multimodal projector (mmproj) downloaded with the variant, so the model reads images (decision P111). */
  projector?: File;
}

/** A model on this PC: a repository's variant, downloaded and checked. */
export interface LocalModel {
  /** "owner/repo:VARIANT" */
  id: string;
  repo: string;
  variant: string;
  files: string[];
  sizeBytes: number;
  downloadedAt: string;
  fit?: Fit;
  /** The KV cache at the settings the next start uses (for the served model: the ones it runs with). */
  kv?: KVCache;
  /** Its projector (it reads images), decision P111. */
  projector?: string;
  /** An embedding model: served on its own, never for chat. */
  embedding?: boolean;
  /** The downloaded models that can draft for it, and the one chosen ("mtp": its own MTP heads, decision P120). */
  drafts?: string[];
  draft?: string;
  /** It carries its own multi-token prediction heads, which can draft for it (decision P120). */
  mtp?: boolean;
  /** An EAGLE-3 draft: it drafts for the model it was made for, and never chats (decision P120). */
  eagle3?: boolean;
  /** The kind of a file that only drafts (decisions P129 and P132): an EAGLE-3, DFlash or DSpark draft, an MTP head of its own, a
   * Gemma 4 assistant, or an MTP file of an older TurboQuant layout that no engine loads. */
  draftKind?: 'eagle3' | 'dflash' | 'dspark' | 'mtp-head' | 'assistant' | 'old-mtp';
  /** What it takes loaded: its files, its KV cache and its draft's (with the MTP context of MTP heads). */
  loadBytes?: number;
  /** The memory of the MTP draft context when its own MTP heads or a head file draft for it (decision P129). */
  mtpKvBytes?: number;
}

export interface ServerState {
  state: LocalServerStatus;
  modelId?: string;
  port?: number;
  baseUrl?: string;
  error?: string;
  startedAt?: string;
  pid?: number;
  /** Drydock started it when it started (the Autostart setting); false when a person did. */
  autostarted?: boolean;
  /** What it was started with (contextSize 0 or absent: the model's own). */
  cacheType?: string;
  contextSize?: number;
  /** Every model it keeps loaded (modelId is the first); router: llama-server's router mode; embeddings: the embedding server. */
  models?: string[];
  router?: boolean;
  embeddings?: boolean;
  /** The models the router is loading beside the ones it keeps (they are among models meanwhile), decision P120. */
  loading?: string[];
  /** Why the server was started again when a change of its models could not be made on it (decision P120). */
  note?: string;
}

/** An agent, of any project, whose provider is the server's provider row. */
export interface LocalUser {
  agentId: string;
  name: string;
  projectId: string;
  projectName: string;
}

/** The served model's memory, for the memory bar (decision P97). deviceFree is what the device probe read, before the model loaded as a rule. */
export interface LocalServed {
  modelId: string;
  /** Every model loaded; modelBytes and kvBytes are theirs together. */
  models?: string[];
  modelBytes: number;
  /** 0 when kv.known is false. */
  kvBytes: number;
  kv: KVCache;
  /** The GPU with the most free memory, else the RAM. */
  device: 'gpu' | 'ram';
  deviceName?: string;
  deviceTotal: number;
  deviceFree: number;
  fit: Fit;
}

/** The Providers page's "Models on this PC" section. */
export interface LocalView {
  supported: boolean;
  reason?: string;
  runtime: LocalRuntime;
  sources: Source[];
  backends: BackendInfo[];
  suggestedBackend: LocalBackend;
  models: LocalModel[];
  downloads: LocalJob[];
  server: ServerState;
  settings: LocalSettings;
  memory: Memory;
  freeBytes: number;
  modelsBytes: number;
  log: string[];
  /** The agents that use the models on this PC (the client always gives an array). */
  usedBy?: LocalUser[];
  /** Set while the server starts or runs. */
  served?: LocalServed;
  /** The room for models loaded together (decision P111): at most max, within budgetBytes (0: not measured). */
  load?: LocalLoad;
  /** The embedding model's server. */
  embed?: ServerState;
  /** The Hugging Face token as the page shows it (the token itself never leaves the core). */
  hfToken?: LocalToken;
}

export interface LocalLoad {
  max: number;
  budgetBytes: number;
  loadedBytes: number;
}

export interface LocalToken {
  set: boolean;
  /** "hf_…1234" */
  masked?: string;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const repo = (r: Repo): Repo => ({ ...r, tags: arr(r.tags) });
const variant = (v: Variant): Variant => ({ ...v, files: arr(v.files), notes: arr(v.notes) });
const view = (v: LocalView): LocalView => ({
  ...v,
  runtime: { ...v.runtime, devices: { ...(v.runtime?.devices ?? { probed: false }), devices: arr(v.runtime?.devices?.devices) } },
  sources: arr(v.sources).map((s) => ({ ...s, cacheTypes: arr(s.cacheTypes) })),
  backends: arr(v.backends),
  models: arr(v.models).map((m) => ({ ...m, files: arr(m.files), drafts: arr(m.drafts) })),
  server: { ...v.server, models: arr(v.server?.models), loading: arr(v.server?.loading) },
  embed: { state: 'stopped', ...(v.embed ?? {}), models: arr(v.embed?.models) },
  load: v.load ?? { max: 4, budgetBytes: 0, loadedBytes: 0 },
  hfToken: v.hfToken ?? { set: false },
  downloads: arr(v.downloads),
  log: arr(v.log),
  usedBy: arr(v.usedBy),
});

export interface LocalClient {
  localGet(): Promise<LocalView>;
  /** Installs (or updates to) the newest build of the source for the backend, in the background. '' keeps the current setting. */
  localInstall(source?: string, backend?: LocalBackend | ''): Promise<LocalView>;
  /** Looks up the newest build of the installed source and backend now; a newer one comes back as runtime.latest. */
  localCheckUpdates(): Promise<LocalView>;
  /** Stops the server and deletes every build. */
  localRuntimeRemove(): Promise<LocalView>;
  /** Changes just the settings given. */
  localSettingsSet(e: LocalSettingsEdit): Promise<LocalView>;
  /** ggml-org's own GGUF repositories, most downloaded first. */
  localModelsSuggested(): Promise<Repo[]>;
  localModelsSearch(query: string): Promise<Repo[]>;
  /** A repository's downloadable files with whether each fits this PC. */
  localModelVariants(repo: string): Promise<Variant[]>;
  /** Fetches a variant in the background; the view's downloads[] follows it. */
  localModelDownload(repo: string, variant: string): Promise<LocalView>;
  /** Cancels a running download; clears a finished or failed row. */
  localModelDownloadCancel(id: string): Promise<LocalView>;
  /** Deletes a model's files (refused while it is served). */
  localModelRemove(id: string): Promise<LocalView>;
  /** Serves a downloaded model and registers the server as a provider. */
  localServerStart(modelId: string): Promise<LocalView>;
  /** Keeps these models loaded together (the router when there are several; refused past the memory or the cap). */
  localServerStartMany(modelIds: string[]): Promise<LocalView>;
  localServerStop(): Promise<LocalView>;
  /** Marks a model as an embedding model (or not), or picks its draft model ('' for none). */
  localModelSet(id: string, e: { embedding?: boolean; draft?: string }): Promise<LocalView>;
  /** Serves an embedding model on its own server. */
  localEmbeddingsStart(modelId: string): Promise<LocalView>;
  localEmbeddingsStop(): Promise<LocalView>;
  /** Keeps a Hugging Face token in the credential store, for gated models. */
  localHfTokenSet(token: string): Promise<LocalView>;
  localHfTokenRemove(): Promise<LocalView>;
}

export function createLocalClient(t: Transport): LocalClient {
  return {
    localGet: async () => view(await t.call<LocalView>('local.get')),
    localInstall: async (source = '', backend = '') => view(await t.call<LocalView>('local.install', { source, backend })),
    localCheckUpdates: async () => view(await t.call<LocalView>('local.checkUpdates')),
    localRuntimeRemove: async () => view(await t.call<LocalView>('local.runtime.remove')),
    localSettingsSet: async (e) => view(await t.call<LocalView>('local.settings.set', e)),
    localModelsSuggested: async () => arr(await t.call<Repo[] | null>('local.models.suggested')).map(repo),
    localModelsSearch: async (query) => arr(await t.call<Repo[] | null>('local.models.search', { query })).map(repo),
    localModelVariants: async (r) => arr(await t.call<Variant[] | null>('local.model.variants', { repo: r })).map(variant),
    localModelDownload: async (r, v) => view(await t.call<LocalView>('local.model.download', { repo: r, variant: v })),
    localModelDownloadCancel: async (id) => view(await t.call<LocalView>('local.model.download.cancel', { id })),
    localModelRemove: async (id) => view(await t.call<LocalView>('local.model.remove', { id })),
    localServerStart: async (modelId) => view(await t.call<LocalView>('local.server.start', { modelId })),
    localServerStartMany: async (modelIds) => view(await t.call<LocalView>('local.server.start', { modelIds })),
    localServerStop: async () => view(await t.call<LocalView>('local.server.stop')),
    localModelSet: async (id, e) => view(await t.call<LocalView>('local.model.set', { id, ...e })),
    localEmbeddingsStart: async (modelId) => view(await t.call<LocalView>('local.embeddings.start', { modelId })),
    localEmbeddingsStop: async () => view(await t.call<LocalView>('local.embeddings.stop')),
    localHfTokenSet: async (token) => view(await t.call<LocalView>('local.hfToken.set', { token })),
    localHfTokenRemove: async () => view(await t.call<LocalView>('local.hfToken.remove')),
  };
}

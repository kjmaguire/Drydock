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
}

/** The settings a person changes; a field left out stays as it is. */
export interface LocalSettingsEdit {
  source?: string;
  backend?: LocalBackend | '';
  cacheType?: string;
  contextSize?: number;
  port?: number;
  autostart?: boolean;
}

/** A model repository on the Hub. */
export interface Repo {
  id: string;
  downloads: number;
  likes: number;
  updated: string;
  tags?: string[];
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
}

export interface ServerState {
  state: LocalServerStatus;
  modelId?: string;
  port?: number;
  baseUrl?: string;
  error?: string;
  startedAt?: string;
  pid?: number;
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
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const repo = (r: Repo): Repo => ({ ...r, tags: arr(r.tags) });
const variant = (v: Variant): Variant => ({ ...v, files: arr(v.files), notes: arr(v.notes) });
const view = (v: LocalView): LocalView => ({
  ...v,
  runtime: { ...v.runtime, devices: { ...(v.runtime?.devices ?? { probed: false }), devices: arr(v.runtime?.devices?.devices) } },
  sources: arr(v.sources).map((s) => ({ ...s, cacheTypes: arr(s.cacheTypes) })),
  backends: arr(v.backends),
  models: arr(v.models).map((m) => ({ ...m, files: arr(m.files) })),
  downloads: arr(v.downloads),
  log: arr(v.log),
});

export interface LocalClient {
  localGet(): Promise<LocalView>;
  /** Installs (or updates to) the newest build of the source for the backend, in the background. '' keeps the current setting. */
  localInstall(source?: string, backend?: LocalBackend | ''): Promise<LocalView>;
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
  localServerStop(): Promise<LocalView>;
}

export function createLocalClient(t: Transport): LocalClient {
  return {
    localGet: async () => view(await t.call<LocalView>('local.get')),
    localInstall: async (source = '', backend = '') => view(await t.call<LocalView>('local.install', { source, backend })),
    localRuntimeRemove: async () => view(await t.call<LocalView>('local.runtime.remove')),
    localSettingsSet: async (e) => view(await t.call<LocalView>('local.settings.set', e)),
    localModelsSuggested: async () => arr(await t.call<Repo[] | null>('local.models.suggested')).map(repo),
    localModelsSearch: async (query) => arr(await t.call<Repo[] | null>('local.models.search', { query })).map(repo),
    localModelVariants: async (r) => arr(await t.call<Variant[] | null>('local.model.variants', { repo: r })).map(variant),
    localModelDownload: async (r, v) => view(await t.call<LocalView>('local.model.download', { repo: r, variant: v })),
    localModelDownloadCancel: async (id) => view(await t.call<LocalView>('local.model.download.cancel', { id })),
    localModelRemove: async (id) => view(await t.call<LocalView>('local.model.remove', { id })),
    localServerStart: async (modelId) => view(await t.call<LocalView>('local.server.start', { modelId })),
    localServerStop: async () => view(await t.call<LocalView>('local.server.stop')),
  };
}

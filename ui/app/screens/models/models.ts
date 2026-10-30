// Pure helpers of Models on this PC (models-screen.tsx, decisions P65, P97): sizes, fit and state words, the memory bars'
// arithmetic, the Serving card's lines, progress; the models loaded together, their room and drafts (decision P111).

import type { Device, Fit, LocalJob, LocalModel, LocalRuntime, LocalServerStatus, LocalToken, LocalUser, LocalView, ServerState } from '../../api/localClient.ts';
import { agoWords } from '../../lib/format.ts';
import type { Tone } from '../../ui/tokens.ts';

const GiB = 2 ** 30;
const MiB = 2 ** 20;

/** A size in GB with one decimal ("8.5 GB"); under a gigabyte in whole MB ("24 MB"), so a small test model is not "0.0 GB". */
export function gb(bytes: number | undefined | null): string {
  const b = bytes ?? 0;
  if (!Number.isFinite(b) || b <= 0) return '0 MB';
  if (b < GiB) return `${Math.max(1, Math.round(b / MiB))} MB`;
  return `${(b / GiB).toFixed(1)} GB`;
}

/** A number of GB as the memory bars say it: one decimal, none when it is whole ("12.4", "16", "0.2"). */
export function gbNumber(bytes: number): string {
  const b = Number.isFinite(bytes) && bytes > 0 ? bytes : 0;
  const s = (b / GiB).toFixed(1);
  return s.endsWith('.0') ? s.slice(0, -2) : s;
}

/** "12.4 of 16 GB": what is used of a memory. */
export function usedOf(used: number, total: number): string {
  return `${gbNumber(Math.min(used, total))} of ${gbNumber(total)} GB`;
}

/** The fit badge's words and tone, with the full sentence for its tooltip. */
export interface FitWords {
  /** The badge: short, so it does not wrap a row ("Fits · GPU", "Too big for 48 GB"). */
  text: string;
  /** The full sentence, with the numbers when they are known: the badge's title. */
  long: string;
  tone: Tone;
}

/**
 * The fit badge of the model table (the mockup's "Serving · GPU", "Fits · GPU", "Fits · GPU + RAM", "Too big for 48 GB"):
 * where a model would run, from the fit the core measured, and nothing when nothing could be measured. `cpu` is "GPU + RAM" on
 * a PC with a GPU (llama.cpp then offloads what fits) and "RAM" without one. `serving` says the model is the one served.
 * The size compared is the model with its KV cache when the core could estimate the cache (fit.needBytes).
 */
export function fitWords(fit: Fit | undefined, sizeBytes: number | undefined, o: { gpu: boolean; serving?: boolean }): FitWords | undefined {
  const need = fit?.needBytes || sizeBytes || 0;
  const withKV = fit?.needBytes ? ' with its KV cache' : '';
  const sizes = fit && need > 0 && fit.availableBytes > 0 ? ` (needs about ${gb(need)}${withKV}, ${gb(fit.availableBytes)} free)` : '';
  const lead = o.serving ? 'Serving' : 'Fits';
  switch (fit?.target) {
    case 'gpu':
      return { text: `${lead} · GPU`, long: `Runs on the GPU${sizes}.`, tone: 'ok' };
    case 'cpu':
      return o.gpu
        ? { text: `${lead} · GPU + RAM`, long: `Runs on the GPU and in RAM (slower): no GPU has room for all of it${sizes}.`, tone: 'warn' }
        : { text: `${lead} · RAM`, long: `Runs on the CPU, in RAM (slow): no GPU was found${sizes}.`, tone: 'warn' };
    case 'none':
      return o.serving
        ? { text: 'Serving · too big', long: `Too big for this PC${sizes}: it may run very slowly or fail.`, tone: 'err' }
        : { text: `Too big for ${gbNumber(fit.availableBytes)} GB`, long: `Too big for this PC${sizes}: Drydock does not download a model it could not load.`, tone: 'err' };
    default:
      return o.serving ? { text: 'Serving', long: 'Served; the memory of this PC could not be measured.', tone: 'ok' } : undefined;
  }
}

/** "gpt-oss-20b" from "ggml-org/gpt-oss-20b-GGUF": a repository's model, without the owner and the GGUF suffix. */
export function modelName(repo: string): string {
  const base = repo.split('/').pop() || repo;
  return base.replace(/[-_.]gguf$/i, '') || base;
}

/** A model id "owner/repo:VARIANT" split into its repository and variant (a download's target has the same form). */
export function splitModelId(id: string): { repo: string; variant: string } {
  const i = id.lastIndexOf(':');
  return i > 0 ? { repo: id.slice(0, i), variant: id.slice(i + 1) } : { repo: id, variant: '' };
}

/** The server's state dot tone. */
export function serverTone(state: LocalServerStatus | string): Tone {
  switch (state) {
    case 'running':
      return 'ok';
    case 'starting':
      return 'info';
    case 'failed':
      return 'err';
    default:
      return 'muted';
  }
}

/** "127.0.0.1:9933" from "http://127.0.0.1:9933/v1": the server's address as the Serving card shows it. */
export function hostPort(baseUrl: string | undefined, port?: number): string {
  if (baseUrl) {
    try {
      return new URL(baseUrl).host;
    } catch {
      return baseUrl;
    }
  }
  return port ? `127.0.0.1:${port}` : '';
}

/** "32k context", "40000 context", "the model's own context" (0: the model's own, its size not known). */
export function contextWords(tokens: number | undefined): string {
  const n = tokens ?? 0;
  if (!(n > 0)) return "the model's own context";
  return n % 1024 === 0 ? `${n / 1024}k context` : `${n} context`;
}

/** The Serving card's "Used by": the agents whose provider is the server, named with their project when there are several. */
export function usedByWords(users: readonly LocalUser[] | undefined): string {
  const list = users ?? [];
  if (list.length === 0) return 'No agent yet';
  const projects = new Set(list.map((u) => u.projectId));
  return list.map((u) => (projects.size > 1 && u.projectName ? `${u.name} (${u.projectName})` : u.name)).join(', ');
}


/** The Serving card's "Started": "at login, 3 h ago" when Drydock started it (the Autostart setting), else "3 h ago". */
export function startedWords(startedAt: string | undefined, autostarted: boolean | undefined, now = Date.now()): string {
  const t = startedAt ? Date.parse(startedAt) : NaN;
  const ago = Number.isFinite(t) && t > 0 ? agoWords(t, now) : '';
  if (autostarted) return ago ? `at login, ${ago}` : 'at login';
  return ago;
}

/** One part of a memory bar. */
export interface BarPart {
  kind: 'model' | 'kv' | 'other';
  bytes: number;
}

/** A memory bar: what is used of a total, in parts. */
export interface Bar {
  total: number;
  used: number;
  parts: BarPart[];
}

/**
 * A memory bar of a total. With a model in it, the model and its KV cache are parts of their own and `other` is what else was
 * in use (`otherUsed`; for a GPU what was taken when the devices were read, for the RAM what is taken besides the model now).
 * Parts are cut so they never pass the total; empty parts are left out.
 */
export function memoryBar(total: number, otherUsed: number, model = 0, kv = 0): Bar {
  const cap = Math.max(0, total);
  const parts: BarPart[] = [];
  let left = cap;
  for (const [kind, bytes] of [['model', model], ['kv', kv], ['other', otherUsed]] as const) {
    const b = Math.min(Math.max(0, bytes || 0), left);
    if (b > 0) parts.push({ kind, bytes: b });
    left -= b;
  }
  return { total: cap, used: cap - left, parts };
}

/** The GPU bars and the RAM bar of the Memory card, from the view: the served model in the memory it was measured against. */
export function memoryBars(v: LocalView): { gpus: { device: Device; bar: Bar; serving: boolean }[]; ram?: Bar } {
  const served = v.served;
  const list = gpus(v.runtime.devices.devices);
  // the device the served model is measured against: the GPU with the most free memory (served.deviceName names it)
  const most = list.reduce<Device | undefined>((a, d) => (!a || d.freeBytes > a.freeBytes ? d : a), undefined);
  const best =
    served?.device === 'gpu'
      ? (list.find((d) => d.name === served.deviceName && d.totalBytes === served.deviceTotal) ?? list.find((d) => d.name === served.deviceName) ?? most)
      : undefined;
  const out = list.map((d) => {
    const before = Math.max(0, d.totalBytes - d.freeBytes);
    const serving = !!served && d === best;
    return { device: d, serving, bar: serving ? memoryBar(d.totalBytes, before, served!.modelBytes, served!.kvBytes) : memoryBar(d.totalBytes, before) };
  });
  let ram: Bar | undefined;
  if (v.memory.known && v.memory.totalBytes > 0) {
    const used = Math.max(0, v.memory.totalBytes - v.memory.availableBytes);
    ram =
      served?.device === 'ram'
        ? memoryBar(v.memory.totalBytes, Math.max(0, used - served.modelBytes - served.kvBytes), served.modelBytes, served.kvBytes)
        : memoryBar(v.memory.totalBytes, used);
  }
  return { gpus: out, ram };
}

/** The words of a bar for assistive technology: "12.4 of 16 GB used: model 10.2 GB, KV cache 2.0 GB, other 0.2 GB". */
export function barLabel(name: string, b: Bar): string {
  const words: Record<BarPart['kind'], string> = { model: 'model', kv: 'KV cache', other: 'other programs' };
  const parts = b.parts.map((p) => `${words[p.kind]} ${gb(p.bytes)}`).join(', ');
  return `${name}: ${usedOf(b.used, b.total)} used${parts ? `: ${parts}` : ''}`;
}

/** How far a job is, 0..1, or undefined when its size is not known yet. */
export function jobFraction(job: LocalJob | undefined): number | undefined {
  if (!job || !(job.total > 0)) return undefined;
  return Math.min(1, Math.max(0, job.done / job.total));
}

/** The line beside a job's bar: bytes done of total when the size is known, else the phase. */
export function progressText(job: LocalJob): string {
  if (job.total > 0) {
    const pct = Math.round((Math.min(job.done, job.total) / job.total) * 100);
    return `${gb(job.done)} of ${gb(job.total)} (${pct}%)${job.phase ? `, ${job.phase}` : ''}`;
  }
  return job.phase || 'working';
}

/** A download count in short form ("900k", "1.2M"). */
export function compactCount(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

/** "TurboQuant build b10298-2.0.0 · Vulkan": the engine is Drydock's own and is never called llama.cpp here (decision P95). */
export function runtimeLine(r: LocalRuntime, backendLabel: (id: string) => string): string {
  const parts = [`${r.sourceName || 'Model engine'} ${r.tag ?? ''}`.trim()];
  if (r.backend) parts.push(backendLabel(r.backend));
  return parts.join(' · ');
}

/** "CUDA0: NVIDIA GeForce RTX 4060 Ti, 13.8 GB free of 15.6 GB". */
export function deviceLine(d: Device): string {
  return `${d.id}: ${d.name}, ${gb(d.freeBytes)} free of ${gb(d.totalBytes)}`;
}

/**
 * What to say when the engine listed no GPU: that there is none, or, when Windows has a graphics card the engine did not list,
 * that this build could not use it (the card is there; the person should not be told it is not).
 */
export function noGpuWords(adapters: readonly string[] | undefined): string {
  const cards = (adapters ?? []).filter((a) => a.trim());
  if (cards.length === 0) return 'No GPU found: runs on the CPU';
  const names = cards.length === 1 ? cards[0] : `${cards.slice(0, -1).join(', ')} and ${cards.at(-1)}`;
  return `Windows has ${names}, but the engine could not use ${cards.length === 1 ? 'it' : 'them'}: models run on the CPU. An up-to-date graphics driver usually fixes this.`;
}

/** The devices llama.cpp can compute on (its CPU entry, when it lists one, is not a GPU). */
export function gpus(devices: Device[]): Device[] {
  return devices.filter((d) => d.kind !== 'CPU');
}

/** "Models on disk: 26 MB · Free disk: 120.0 GB". */
export function diskLine(v: LocalView): string {
  const parts = [`Models on disk: ${gb(v.modelsBytes)}`];
  if (v.freeBytes > 0) parts.push(`Free disk: ${gb(v.freeBytes)}`);
  return parts.join(' · ');
}

/** What Switch model (or Serve a model) offers: the downloaded models, the one served checked. */
export function switchChoices(models: readonly LocalModel[], servedId: string | undefined): { id: string; label: string; current: boolean }[] {
  return models.map((m) => ({ id: m.id, label: `${modelName(m.repo)} · ${m.variant}`, current: m.id === servedId }));
}

/** A number typed in a settings box: a whole number, or undefined when it is not one. */
export function wholeNumber(s: string): number | undefined {
  const n = Number(s.trim());
  return s.trim() !== '' && Number.isInteger(n) && n >= 0 ? n : undefined;
}

/** The key of the local engine's row on the Models page (/models/this-pc), which opens when no other row is asked for. */
export const THIS_PC = 'this-pc';
/** The key of the "Add a provider" row (/models/new). */
export const NEW_PROVIDER = 'new';

/** A served model's name as a row says it: "gpt-oss-20b · MXFP4". */
export function servedName(modelId: string): string {
  const { repo, variant } = splitModelId(modelId);
  return variant ? `${modelName(repo)} · ${variant}` : modelName(repo);
}

/**
 * The "Models on this PC" row of the Models page and its detail's pill: the state in a word or two and the line under the name
 * (the model served, else whether the engine is set up). `none` is the words only: a call to set up, not a state.
 */
export function localRow(v: Pick<LocalView, 'supported' | 'runtime' | 'server'>): { text: string; tone: Tone | 'none'; sub: string } {
  if (!v.supported) return { text: 'Not available', tone: 'muted', sub: 'Not set up' };
  const s = v.server;
  // several models loaded: the first, and how many more
  const more = s ? loadedIds(s).length - 1 : 0;
  const name = (fallback: string) => (s?.modelId ? `${servedName(s.modelId)}${more > 0 ? ` and ${more} more` : ''}` : fallback);
  if (s?.state === 'running') return { text: 'Serving', tone: 'ok', sub: name('Serving') };
  if (s?.state === 'starting') return { text: 'Starting', tone: 'info', sub: name('Starting') };
  if (!v.runtime?.installed) return v.runtime?.job?.state === 'running' ? { text: 'Setting up', tone: 'info', sub: 'Not set up' } : { text: 'Set up', tone: 'none', sub: 'Not set up' };
  if (s?.state === 'failed') return { text: 'Failed', tone: 'err', sub: 'Nothing served' };
  return { text: 'Stopped', tone: 'muted', sub: 'Nothing served' };
}

/** The models the server keeps loaded while it starts or runs (decision P111): its list, else its one model. */
export function loadedIds(server: Pick<ServerState, 'state' | 'modelId' | 'models'>): string[] {
  if (server.state !== 'running' && server.state !== 'starting') return [];
  const list = server.models ?? [];
  if (list.length > 0) return list;
  return server.modelId ? [server.modelId] : [];
}

/** What a model takes loaded, as the core counted it (its files, KV cache and draft), else its fit's need, else its size. */
export function loadBytesOf(m: LocalModel): number {
  return m.loadBytes || m.fit?.needBytes || m.sizeBytes || 0;
}

/**
 * Whether a model can be loaded beside the ones loaded now (the row's "Load too", decision P111), and why not: an embedding
 * model is served on its own, at most `load.max` stay loaded, and together they must fit the memory the core measured (a budget
 * of 0 is not measured: the person decides).
 */
export function loadToo(v: Pick<LocalView, 'server' | 'load'>, m: LocalModel): { ok: boolean; why?: string } {
  const loaded = loadedIds(v.server);
  const load = v.load ?? { max: 4, budgetBytes: 0, loadedBytes: 0 };
  if (m.embedding) return { ok: false, why: 'An embedding model is served on its own, under Embeddings.' };
  const kind = draftOnly(m);
  if (kind) return { ok: false, why: `${DRAFT_KINDS[kind].article} does not chat: choose it as its model's draft.` };
  if (loaded.includes(m.id)) return { ok: false, why: 'Loaded already.' };
  if (loaded.length >= load.max) return { ok: false, why: `At most ${load.max} models stay loaded at once: unload one first.` };
  const need = loadBytesOf(m);
  if (load.budgetBytes > 0 && load.loadedBytes + need > load.budgetBytes) {
    const left = Math.max(0, load.budgetBytes - load.loadedBytes);
    return { ok: false, why: `Does not fit beside the models loaded: it needs about ${gb(need)}, and ${gb(left)} of ${gb(load.budgetBytes)} is left.` };
  }
  return { ok: true };
}

/** "2 of 4 models loaded · 18.2 of 24 GB": the room the loaded models take (the memory part only when it was measured). */
export function loadLine(v: Pick<LocalView, 'server' | 'load'>): string {
  const n = loadedIds(v.server).length;
  const load = v.load ?? { max: 4, budgetBytes: 0, loadedBytes: 0 };
  const count = `${n} of ${load.max} models loaded`;
  return load.budgetBytes > 0 ? `${count} · ${usedOf(load.loadedBytes, load.budgetBytes)}` : count;
}

/** The draft a model chooses when its own MTP heads draft for it (decision P120): no model is called so. */
export const DRAFT_MTP = 'mtp';

export type DraftKind = NonNullable<LocalModel['draftKind']>;

/**
 * The words for each kind of file that only drafts (decisions P120 and P129): its badge, its prefix in a draft choice, the
 * row's word once chosen, the kind with its article, and the badge's tooltip.
 */
export const DRAFT_KINDS: Record<DraftKind, { badge: string; prefix: string; with: string; article: string; title: string }> = {
  eagle3: {
    badge: 'EAGLE-3 draft',
    prefix: 'EAGLE-3',
    with: 'with EAGLE-3',
    article: 'An EAGLE-3 draft',
    title: "An EAGLE-3 draft: it reads its model's hidden states to guess ahead. It drafts for that model and does not chat.",
  },
  dflash: {
    badge: 'DFlash draft',
    prefix: 'DFlash',
    with: 'with DFlash',
    article: 'A DFlash draft',
    title: "A DFlash draft: it guesses a whole block of tokens at once from its model's hidden states. It drafts for that model and does not chat.",
  },
  dspark: {
    badge: 'DSpark draft',
    prefix: 'DSpark',
    with: 'with DSpark',
    article: 'A DSpark draft',
    title: 'A DSpark draft: a DFlash draft with a head that reads the token before, so more of its guesses hold. It drafts for its model and does not chat.',
  },
  'mtp-head': {
    badge: 'MTP head',
    prefix: 'MTP head',
    with: 'with an MTP head',
    article: 'An MTP head',
    title: "The multi-token prediction (MTP) heads of a model, shipped as a file of their own. They draft for that model and do not chat.",
  },
};

/** The kind of a file that only drafts, or undefined for a model that chats (an older core says only eagle3). */
export function draftOnly(m: Pick<LocalModel, 'draftKind' | 'eagle3'>): DraftKind | undefined {
  return m.draftKind ?? (m.eagle3 ? 'eagle3' : undefined);
}

/**
 * The draft choices of a model: none, its own MTP heads when its file carries them, then each downloaded model that can draft
 * for it, a file that only drafts named by its kind (decisions P120 and P129).
 */
export function draftChoices(m: LocalModel, models: readonly LocalModel[]): { value: string; label: string }[] {
  const label = (id: string) => {
    if (id === DRAFT_MTP) return 'Its own MTP heads';
    const found = models.find((x) => x.id === id);
    const name = found ? `${modelName(found.repo)} · ${found.variant}` : id;
    const kind = found ? draftOnly(found) : undefined;
    return `${kind ? DRAFT_KINDS[kind].prefix : 'Draft'}: ${name}`;
  };
  return [{ value: '', label: 'No draft' }, ...(m.drafts ?? []).map((id) => ({ value: id, label: label(id) }))];
}

/** The row's word for the draft chosen: its own MTP heads, a file that only drafts, or a draft model (undefined for none). */
export function draftWords(m: LocalModel, models: readonly LocalModel[]): string | undefined {
  if (!m.draft) return undefined;
  if (m.draft === DRAFT_MTP) return 'with MTP';
  const found = models.find((x) => x.id === m.draft);
  const kind = found ? draftOnly(found) : undefined;
  return kind ? DRAFT_KINDS[kind].with : 'with a draft';
}

/**
 * What Load too and Unload do, as their tooltips (decision P120): on the router the model is loaded or unloaded while the others
 * keep running; a server of one model has no router, so Load too starts it again with both.
 */
export function loadTooTitle(server: Pick<ServerState, 'router'>): string {
  return server.router
    ? 'Load it beside the models served: they keep running while it loads'
    : 'Keep it loaded beside the model served: the server starts again with both, since one model alone runs without the router';
}

export function unloadTitle(server: Pick<ServerState, 'router'>): string {
  return server.router ? 'Unload it: the other models keep running' : 'Start the server again without this model';
}

/** The token as the page shows it: "hf_…1234" as the core masked it, "none" without one. */
export function tokenWords(tk: LocalToken | undefined): string {
  return tk?.set ? tk.masked || 'set' : 'none';
}

/** The Hugging Face page of a repository, where a gated model's licence is accepted. */
export function licenceUrl(repo: string): string {
  return `https://huggingface.co/${repo}`;
}

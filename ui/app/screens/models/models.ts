// Pure helpers of Models on this PC (models-screen.tsx, decisions P65, P97): sizes, fit and state words, the memory bars'
// arithmetic, the Serving card's lines, progress.

import type { Device, Fit, LocalJob, LocalModel, LocalRuntime, LocalServerStatus, LocalUser, LocalView } from '../../api/localClient.ts';
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

/** "just now", "14 min ago", "3 h ago", "2 d ago". */
export function agoWords(ms: number, now = Date.now()): string {
  if (!ms) return '';
  const s = Math.max(0, Math.floor((now - ms) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
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
  if (s?.state === 'running') return { text: 'Serving', tone: 'ok', sub: s.modelId ? servedName(s.modelId) : 'Serving' };
  if (s?.state === 'starting') return { text: 'Starting', tone: 'info', sub: s.modelId ? servedName(s.modelId) : 'Starting' };
  if (!v.runtime?.installed) return v.runtime?.job?.state === 'running' ? { text: 'Setting up', tone: 'info', sub: 'Not set up' } : { text: 'Set up', tone: 'none', sub: 'Not set up' };
  if (s?.state === 'failed') return { text: 'Failed', tone: 'err', sub: 'Nothing served' };
  return { text: 'Stopped', tone: 'muted', sub: 'Nothing served' };
}

// Pure helpers of the "Models on this PC" section (local-models.tsx): sizes, fit and state words, progress arithmetic.

import type { Device, Fit, LocalJob, LocalRuntime, LocalServerStatus, LocalView } from '../../api/localClient.ts';
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

/** The fit badge's words and tone, with the long form for its tooltip and the row. */
export interface FitWords {
  /** The badge: short, so it does not wrap a row. */
  text: string;
  /** The full sentence: the badge's title, and shown in the row when the model will not run well. */
  long: string;
  tone: Tone;
}

/**
 * The fit badge: where a model would run, in words, and its tone; nothing when nothing could be measured. With the model's
 * size and the memory the core measured, the long form says the numbers ("needs about 80.0 GB, 15.0 GB free"); a model that
 * does not fit is "Not recommended", one that fits the RAM only "Runs on the CPU (slow)".
 */
export function fitText(fit: Fit | undefined, sizeBytes?: number): FitWords | undefined {
  const sizes = fit && (sizeBytes ?? 0) > 0 && fit.availableBytes > 0 ? ` (needs about ${gb(sizeBytes)}, ${gb(fit.availableBytes)} free)` : '';
  switch (fit?.target) {
    case 'gpu':
      return { text: 'Fits the GPU', long: `Fits the GPU${sizes}.`, tone: 'ok' };
    case 'cpu':
      return { text: 'Runs on the CPU (slow)', long: `Runs on the CPU (slow): no GPU has room for it, so llama.cpp keeps it in RAM${sizes}.`, tone: 'warn' };
    case 'none':
      return { text: 'Not recommended', long: `Not recommended: too big for this PC${sizes}.`, tone: 'err' };
    default:
      return undefined;
  }
}

/** The server's state badge tone. */
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

/** "llama.cpp b11669 · Vulkan · llama.cpp (official weekly build)". */
export function runtimeLine(r: LocalRuntime, backendLabel: (id: string) => string): string {
  const parts = [`llama.cpp ${r.tag ?? ''}`.trim()];
  if (r.backend) parts.push(backendLabel(r.backend));
  if (r.sourceName) parts.push(r.sourceName);
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

/** "RAM: 20.0 GB free of 32.0 GB · Models on disk: 26 MB · Free disk: 120.0 GB". */
export function memoryLine(v: LocalView): string {
  const parts: string[] = [];
  if (v.memory.known) parts.push(`RAM: ${gb(v.memory.availableBytes)} free of ${gb(v.memory.totalBytes)}`);
  parts.push(`Models on disk: ${gb(v.modelsBytes)}`);
  if (v.freeBytes > 0) parts.push(`Free disk: ${gb(v.freeBytes)}`);
  return parts.join(' · ');
}

/** A number typed in a settings box: a whole number, or undefined when it is not one. */
export function wholeNumber(s: string): number | undefined {
  const n = Number(s.trim());
  return s.trim() !== '' && Number.isInteger(n) && n >= 0 ? n : undefined;
}

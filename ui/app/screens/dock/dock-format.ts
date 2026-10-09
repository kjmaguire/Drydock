// Pure helpers for the dock panels (no DOM, no framework): sizes, diff summaries, the log line buffer and the color
// conversions the editor and terminal themes need.

import type { DiffFile } from '../../api/dockClient.ts';

export const STATUS_LETTER: Record<DiffFile['status'], string> = { added: 'A', deleted: 'D', modified: 'M', renamed: 'R' };

export function formatSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} bytes`;
}

/** Totals over a file list: binary files count as changed files but add no lines. */
export function diffTotals(files: readonly DiffFile[]): { files: number; additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const f of files) {
    if (f.binary) continue;
    additions += f.additions;
    deletions += f.deletions;
  }
  return { files: files.length, additions, deletions };
}

/** The file name and its directory, for a two-part label in the file list. */
export function splitPath(path: string): { name: string; dir: string } {
  const i = path.lastIndexOf('/');
  return i < 0 ? { name: path, dir: '' } : { name: path.slice(i + 1), dir: path.slice(0, i) };
}

/** How much of a log is read at a time, and the most lines kept in memory (older ones are fetched again on request). */
export const LOG_CHUNK = 64 << 10;
export const LOG_MAX_LINES = 5000;

/**
 * The lines of a log being tailed. A read can end mid-line; that part waits in `partial` for the rest instead of becoming
 * a line of its own. At most `max` lines are kept (the oldest are dropped when appending, the newest when prepending).
 */
export class LogBuffer {
  lines: string[] = [];
  partial = '';
  readonly max: number;
  constructor(max = LOG_MAX_LINES) {
    this.max = max;
  }

  append(text: string): void {
    if (!text) return;
    const all = this.partial + text;
    const cut = all.lastIndexOf('\n');
    this.partial = all.slice(cut + 1);
    if (cut < 0) return;
    const out = this.lines.concat(all.slice(0, cut).split('\n'));
    this.lines = out.length > this.max ? out.slice(out.length - this.max) : out;
  }

  /** Earlier output read on request, put before what is shown. */
  prepend(text: string): void {
    const add = text.replace(/\n$/, '').split('\n');
    this.lines = add.concat(this.lines).slice(0, this.max);
  }

  text(): string {
    return this.lines.join('\n') + (this.partial ? (this.lines.length ? '\n' : '') + this.partial : '');
  }
}

/** Parse a computed CSS color (`rgb(r, g, b)`, `rgba(r, g, b, a)`, `rgb(r g b / a)` or `#rrggbb`). */
export function parseColor(s: string): { r: number; g: number; b: number; a: number } | undefined {
  const v = s.trim();
  const hex = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i.exec(v);
  if (hex) {
    const n = parseInt(hex[1]!, 16);
    return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255, a: hex[2] ? parseInt(hex[2], 16) / 255 : 1 };
  }
  const m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[,\/]\s*([\d.]+%?))?\s*\)$/i.exec(v);
  if (!m) return undefined;
  let a = 1;
  if (m[4] !== undefined) a = m[4].endsWith('%') ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
  return { r: Math.round(Number(m[1])), g: Math.round(Number(m[2])), b: Math.round(Number(m[3])), a };
}

const hex2 = (n: number) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0');

/** `#rrggbb` (or `#rrggbbaa` when translucent), the form Monaco's theme colors require; undefined when unparseable. */
export function toHex(s: string): string | undefined {
  const c = parseColor(s);
  if (!c) return undefined;
  return `#${hex2(c.r)}${hex2(c.g)}${hex2(c.b)}${c.a < 1 ? hex2(c.a * 255) : ''}`;
}

/** The color with a different alpha, as `rgba(...)` (xterm's selection needs a translucent color). */
export function withAlpha(s: string, a: number): string | undefined {
  const c = parseColor(s);
  return c ? `rgba(${c.r}, ${c.g}, ${c.b}, ${a})` : undefined;
}

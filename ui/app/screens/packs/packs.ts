// Pure helpers for the Expert packs screen.

import type { Pack, PackAttachment, PackRefreshResult, PackSearchResult } from '../../api/extTypes.ts';
import { plural } from '../../lib/format.ts';

export const zeroTime = (s?: string): boolean => !s || s.startsWith('0001-');

/** What a refresh did, as the core counted it, and why pages were not indexed. */
export const refreshText = (r: PackRefreshResult): string =>
  `Version ${r.version}: ${r.pages} pages, ${r.added} added, ${r.changed} changed, ${r.unchanged} unchanged, ${r.skipped} skipped, ${r.failed} failed, ${r.quarantined} quarantined.` +
  reasonsText(r.reasons);

/** The core's reasons ("1 PDF with no text layer (a scan: run it through OCR first)"), '' when there are none. */
function reasonsText(reasons: Record<string, number> | undefined): string {
  const parts = Object.entries(reasons ?? {}).map(([why, n]) => `${n} ${why}`);
  return parts.length ? ` Not indexed: ${parts.join('; ')}.` : '';
}

/** Who in this project the pack is attached to ('no' when nobody). */
export function attachedText(mine: readonly PackAttachment[]): string {
  return mine.length === 0 ? 'no' : mine.map((a) => `${a.role || 'all roles'}${a.agentId ? ` (agent ${a.agentId})` : ''}`).join(', ');
}

export function freshnessText(p: Pack): string {
  return `${p.freshness || 'unknown'}${p.version ? `, ${plural(p.ageDays, 'day')} old` : ''}${p.offline ? ', offline' : ''}`;
}

/** The packs a search or question goes to: the chosen ones, or all when none is chosen. */
export const packIds = (selected: readonly string[], all: readonly Pack[]): string[] => (selected.length ? [...selected] : all.map((p) => p.id));

export function searchHeadline(r: PackSearchResult): string {
  const hits = r.hits ?? [];
  return `${r.found ? plural(hits.length, 'hit') : 'Nothing found'} in ${(r.searched ?? []).join(', ') || 'no pack'}`;
}

/** A pack id made from a title: lowercase letters, digits and hyphens, starting with a letter, at most 40 long ('' when none can be made). */
export function packIdFromTitle(title: string): string {
  let id = title
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^[^a-z]+/, '')
    .slice(0, 40)
    .replace(/-+$/, '');
  if (id.length === 1) id += '-docs';
  return id;
}

/** Whether an id is one the core takes (internal/packs packIDRe). */
export const validPackId = (id: string): boolean => /^[a-z][a-z0-9-]{1,39}$/.test(id);

/** The files a pack reads, by extension (internal/packs uploadExts). */
export const PACK_FILE_ACCEPT = '.pdf,.docx,.md,.markdown,.mdx,.txt,.html,.htm';
const DOC_MAX = 32 << 20;
const TEXT_MAX = 16 << 20;
const MAX_FILES = 50;

/** Why these files cannot be given to a pack, or '' when they can (the core checks again, and what is inside them too). */
export function checkPackFiles(files: readonly { name: string; size: number }[]): string {
  if (files.length === 0) return 'Choose at least one file.';
  if (files.length > MAX_FILES) return `Choose at most ${MAX_FILES} files at a time.`;
  for (const f of files) {
    const dot = f.name.lastIndexOf('.');
    const ext = dot >= 0 ? f.name.slice(dot).toLowerCase() : '';
    if (!PACK_FILE_ACCEPT.split(',').includes(ext)) return `${f.name} is not a document a pack reads (PDF, DOCX, Markdown, text or HTML).`;
    if (f.size === 0) return `${f.name} is empty.`;
    const max = ext === '.pdf' || ext === '.docx' ? DOC_MAX : TEXT_MAX;
    if (f.size > max) return `${f.name} is larger than ${max >> 20} MB.`;
  }
  return '';
}

/** Whether a pack reads its own files folder (a pack made from files), so files can be added to it on this page. */
export const hasOwnFiles = (p: Pack): boolean => (p.manifest?.sources ?? []).some((s) => s.type === 'local_dir' && s.path === 'files');

/** The size of a file in words people read. */
export function fileSize(n: number): string {
  if (n < 1024) return `${n} bytes`;
  if (n < 1 << 20) return `${Math.round(n / 1024)} KB`;
  return `${(n / (1 << 20)).toFixed(1)} MB`;
}

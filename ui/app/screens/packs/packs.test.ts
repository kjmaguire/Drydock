import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Pack } from '../../api/extTypes.ts';
import {
  attachedText,
  checkPackFiles,
  fileSize,
  freshnessText,
  hasOwnFiles,
  packIdFromTitle,
  packIds,
  refreshText,
  searchHeadline,
  validPackId,
  zeroTime,
} from './packs.ts';

const pack = (over: Partial<Pack> = {}): Pack => ({
  id: 'godocs',
  title: 'Go docs',
  kind: 'indexed',
  visibility: 'private',
  version: 'v1',
  status: 'ready',
  pages: 3,
  chunks: 9,
  quarantined: 1,
  ageDays: 2,
  freshness: 'fresh',
  offline: false,
  ...over,
});

describe('Pack helpers', () => {
  it('describes a refresh with every count', () => {
    expect(
      refreshText({
        packId: 'p',
        version: 'v2',
        newVersion: true,
        pages: 3,
        added: 1,
        changed: 0,
        removed: 0,
        unchanged: 2,
        skipped: 0,
        failed: 0,
        quarantined: 1,
        redactions: 0,
      }),
    ).toBe('Version v2: 3 pages, 1 added, 0 changed, 2 unchanged, 0 skipped, 0 failed, 1 quarantined.');
    // the core's reasons for pages it could not index are said with the counts
    expect(
      refreshText({
        packId: 'p',
        version: 'v1',
        newVersion: true,
        pages: 2,
        added: 1,
        changed: 0,
        removed: 0,
        unchanged: 0,
        skipped: 0,
        failed: 1,
        quarantined: 0,
        redactions: 0,
        reasons: { 'PDF with no text layer (a scan: run it through OCR first)': 1 },
      }),
    ).toBe(
      'Version v1: 2 pages, 1 added, 0 changed, 0 unchanged, 0 skipped, 1 failed, 0 quarantined. Not indexed: 1 PDF with no text layer (a scan: run it through OCR first).',
    );
  });

  it('makes a pack id from a title the way the core takes it', () => {
    expect(packIdFromTitle('Operations Handbook 2026')).toBe('operations-handbook-2026');
    expect(packIdFromTitle('  2026 Café  manual!! ')).toBe('cafe-manual');
    expect(packIdFromTitle('X')).toBe('x-docs');
    expect(packIdFromTitle('123')).toBe('');
    expect(packIdFromTitle('a'.repeat(60)).length).toBe(40);
    expect(validPackId('ops')).toBe(true);
    expect(validPackId('Ops')).toBe(false);
    expect(validPackId('o')).toBe(false);
    expect(validPackId('1ops')).toBe(false);
  });

  it('checks picked files before they are sent', () => {
    expect(checkPackFiles([])).toBe('Choose at least one file.');
    expect(checkPackFiles([{ name: 'a.pdf', size: 10 }, { name: 'b.DOCX', size: 10 }, { name: 'c.md', size: 1 }])).toBe('');
    expect(checkPackFiles([{ name: 'tool.exe', size: 10 }])).toContain('is not a document a pack reads');
    expect(checkPackFiles([{ name: 'noext', size: 10 }])).toContain('is not a document a pack reads');
    expect(checkPackFiles([{ name: 'empty.txt', size: 0 }])).toBe('empty.txt is empty.');
    expect(checkPackFiles([{ name: 'big.pdf', size: 33 << 20 }])).toBe('big.pdf is larger than 32 MB.');
    expect(checkPackFiles([{ name: 'big.md', size: 17 << 20 }])).toBe('big.md is larger than 16 MB.');
    expect(checkPackFiles(Array.from({ length: 51 }, (_, i) => ({ name: `${i}.md`, size: 1 })))).toBe('Choose at most 50 files at a time.');
    expect([fileSize(10), fileSize(2048), fileSize(3 << 20)]).toEqual(['10 bytes', '2 KB', '3.0 MB']);
  });

  it('knows a pack made from files', () => {
    expect(hasOwnFiles(pack({ manifest: { sources: [{ type: 'local_dir', path: 'files' }] } }))).toBe(true);
    expect(hasOwnFiles(pack({ manifest: { sources: [{ type: 'local_dir', path: 'C:/docs' }] } }))).toBe(false);
    expect(hasOwnFiles(pack())).toBe(false);
  });

  it('says who a pack is attached to', () => {
    expect(attachedText([])).toBe('no');
    expect(
      attachedText([
        { projectId: 'p', packId: 'x', role: 'backend', createdAt: '' },
        { projectId: 'p', packId: 'x', agentId: 'a1', createdAt: '' },
      ]),
    ).toBe('backend, all roles (agent a1)');
  });

  it('freshness, the zero time and the packs a search uses', () => {
    expect(freshnessText(pack())).toBe('fresh, 2 days old');
    expect(freshnessText(pack({ version: '', freshness: undefined, offline: true }))).toBe('unknown, offline');
    expect(zeroTime('0001-01-01T00:00:00Z')).toBe(true);
    expect(zeroTime(undefined)).toBe(true);
    expect(packIds([], [pack(), pack({ id: 'b' })])).toEqual(['godocs', 'b']);
    expect(packIds(['b'], [pack(), pack({ id: 'b' })])).toEqual(['b']);
  });

  it('a search headline says nothing was found honestly', () => {
    expect(
      searchHeadline({
        found: false,
        hits: [],
        searched: ['godocs'],
        confidence: 'none',
        sourceAgeDays: 0,
      }),
    ).toBe('Nothing found in godocs');
    expect(
      searchHeadline({
        found: true,
        hits: [{} as never, {} as never],
        searched: null,
        confidence: 'high',
        sourceAgeDays: 0,
      }),
    ).toBe('2 hits in no pack');
  });
});

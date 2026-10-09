import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Pack } from '../../api/extTypes.ts';
import { attachedText, freshnessText, packIds, refreshText, searchHeadline, zeroTime } from './packs.ts';

const pack = (over: Partial<Pack> = {}): Pack => ({ id: 'godocs', title: 'Go docs', kind: 'indexed', visibility: 'private', version: 'v1', status: 'ready', pages: 3, chunks: 9, quarantined: 1, ageDays: 2, freshness: 'fresh', offline: false, ...over });

describe('Pack helpers', () => {
  it('describes a refresh with every count', () => {
    expect(refreshText({ packId: 'p', version: 'v2', newVersion: true, pages: 3, added: 1, changed: 0, removed: 0, unchanged: 2, skipped: 0, failed: 0, quarantined: 1, redactions: 0 })).toBe(
      'Version v2: 3 pages, 1 added, 0 changed, 2 unchanged, 0 skipped, 0 failed, 1 quarantined.',
    );
  });

  it('says who a pack is attached to', () => {
    expect(attachedText([])).toBe('no');
    expect(attachedText([{ projectId: 'p', packId: 'x', role: 'backend', createdAt: '' }, { projectId: 'p', packId: 'x', agentId: 'a1', createdAt: '' }])).toBe('backend, all roles (agent a1)');
  });

  it('freshness, the zero time and the packs a search uses', () => {
    expect(freshnessText(pack())).toBe('fresh, 2 day(s) old');
    expect(freshnessText(pack({ version: '', freshness: undefined, offline: true }))).toBe('unknown, offline');
    expect(zeroTime('0001-01-01T00:00:00Z')).toBe(true);
    expect(zeroTime(undefined)).toBe(true);
    expect(packIds([], [pack(), pack({ id: 'b' })])).toEqual(['godocs', 'b']);
    expect(packIds(['b'], [pack(), pack({ id: 'b' })])).toEqual(['b']);
  });

  it('a search headline says nothing was found honestly', () => {
    expect(searchHeadline({ found: false, hits: [], searched: ['godocs'], confidence: 'none', sourceAgeDays: 0 })).toBe('Nothing found in godocs');
    expect(searchHeadline({ found: true, hits: [{} as never, {} as never], searched: null, confidence: 'high', sourceAgeDays: 0 })).toBe('2 hit(s) in no pack');
  });
});

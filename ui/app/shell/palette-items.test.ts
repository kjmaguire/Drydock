import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { buildItems, filterItems, score } from './palette-items.ts';

const rooms = [
  { id: 'r1', projectId: 'p', name: 'general', kind: 'channel', archived: false },
  { id: 'r2', projectId: 'p', name: 'dm:human:me+a1', kind: 'dm', archived: false },
  { id: 'r3', projectId: 'p', name: 'old', kind: 'channel', archived: true },
] as never[];
const agents = [{ id: 'a1', name: 'Ada', role: 'backend' }] as never[];
const tasks = [{ id: 't1', seq: 7, title: 'Add Multiply', status: 'READY', runId: 'run1' }] as never[];

describe('command palette items', () => {
  it('offers rooms (not archived ones), screens, tasks, agents and harmless actions', () => {
    const items = buildItems(rooms, tasks, agents);
    const labels = items.map((i) => i.label);
    expect(labels).toContain('#general');
    expect(labels).toContain('Direct message Ada');
    expect(labels.some((l) => l.includes('old'))).toBe(false);
    expect(labels).toContain('Team');
    expect(labels).toContain('#7 Add Multiply');
    expect(labels).toContain('Workflow of #7 Add Multiply');
    expect(labels).toContain('Inspect Ada');
    // nothing that approves, merges, deletes or stops
    expect(labels.some((l) => /approve|merge|delete|stop|cancel/i.test(l))).toBe(false);
  });

  it('ranks an exact screen name first', () => {
    const items = buildItems(rooms, tasks, agents);
    expect(filterItems(items, 'team')[0]!.label).toBe('Team');
    expect(filterItems(items, 'multiply')[0]!.label).toBe('#7 Add Multiply');
    expect(filterItems(items, '7')[0]!.id).toBe('task:t1');
    // the MCP servers page was folded into Integrations (decision P47): "mcp" still finds it
    expect(items.map((i) => i.label)).not.toContain('MCP servers of this project');
    expect(filterItems(items, 'mcp')[0]!.label).toBe('Integrations');
  });

  it('matches letters in order as a last resort and drops non-matches', () => {
    const it0 = { id: 'x', group: 'Navigate' as const, label: 'Settings and About' };
    expect(score(it0, 'stab')).toBeGreaterThan(0);
    expect(score(it0, 'zzz')).toBe(0);
    expect(score(it0, 'settings')).toBeGreaterThan(score(it0, 'about'));
  });
});

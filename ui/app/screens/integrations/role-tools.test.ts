import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RolePolicyView } from '../../api/mcpClient.ts';
import { customIdProblem } from '../agents/agents.ts';
import { cellLabel, changedColumns, columnNote, nextCell, ruleProblem } from './role-tools.ts';

const view = (over: Partial<RolePolicyView> = {}): RolePolicyView => ({
  projectId: 'prj_1',
  columns: [
    { key: 'read_range', label: 'Read files', help: 'read_range', tools: ['read_range'] },
    { key: 'act', label: 'Act', help: 'task, room, handoff', tools: ['task', 'room', 'handoff'] },
    { key: 'docs', label: 'Docs servers', help: 'docs' },
    { key: 'repo_map', label: 'Repo map', help: 'repo_map', tools: ['repo_map'] },
  ],
  rows: [
    {
      role: 'pm',
      label: 'PM',
      cells: {
        read_range: { allowed: true, default: false, changed: true },
        act: { allowed: true, default: true, changed: false },
        docs: { allowed: false, default: false, changed: true },
        repo_map: { allowed: true, default: true, changed: false },
      },
    },
  ],
  rules: [],
  actTools: true,
  repoMap: true,
  ...over,
});

describe('the per-role tool table (P115)', () => {
  it('sends the other value, or null when that is the default (the cell goes back to it)', () => {
    expect(nextCell({ allowed: false, default: false, changed: false })).toBe(true);
    expect(nextCell({ allowed: true, default: false, changed: true })).toBe(null);
    expect(nextCell({ allowed: true, default: true, changed: false })).toBe(false);
    expect(nextCell({ allowed: false, default: true, changed: true })).toBe(null);
  });

  it('lists the changed cells of a row, even one set to its default', () => {
    expect(changedColumns(view(), 'pm')).toEqual(['read_range', 'docs']);
    expect(changedColumns(view(), 'qa')).toEqual([]);
  });

  it('says why a column gives nothing now', () => {
    expect(columnNote(view(), 'act')).toBe('');
    expect(columnNote(view({ actTools: false }), 'act')).toContain('may not act');
    expect(columnNote(view({ repoMap: false }), 'repo_map')).toContain('no repo map');
    expect(columnNote(view({ actTools: false }), 'docs')).toBe('');
  });

  it('names each checkbox by its row and column, and checks a rule before sending it', () => {
    expect(cellLabel('PM', 'Read files')).toBe('PM: Read files');
    expect(ruleProblem('', 'true')).toContain('name');
    expect(ruleProblem('x', ' ')).toContain('expression');
    expect(ruleProblem('x'.repeat(121), 'true')).toContain('120');
    expect(ruleProblem('no trackers', 'column == "trackers"')).toBe('');
  });
});

describe('a custom CLI id (P115)', () => {
  it('is checked on the page before anything is sent', () => {
    expect(customIdProblem('', [])).toContain('Give');
    expect(customIdProblem('My CLI', [])).toContain('lowercase');
    expect(customIdProblem('custom', [])).toContain('taken');
    expect(customIdProblem('claude', ['claude', 'codex'])).toContain('taken');
    expect(customIdProblem(' my-cli ', ['claude'])).toBe('');
  });
});

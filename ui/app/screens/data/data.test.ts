import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Project } from '../../api/types.ts';
import { exportKey, exportProjectId, pendingRows } from './data.ts';

const p = (id: string) => ({ id, name: id }) as Project;

describe('export and import helpers', () => {
  it('exports the pick while it exists, else the selected project, else the first', () => {
    expect(exportProjectId([p('a'), p('b')], 'b', p('a'))).toBe('b');
    expect(exportProjectId([p('a'), p('b')], 'gone', p('a'))).toBe('a');
    expect(exportProjectId([p('a'), p('b')], '', undefined)).toBe('a');
    expect(exportProjectId([], '', undefined)).toBe('');
    expect(exportKey('a', true)).not.toBe(exportKey('a', false));
  });

  it('lists every untrusted kind with its count, counting missing newer fields as zero', () => {
    const rows = pendingRows({
      memoryPending: 2,
      skillsPending: 1,
      requirementChunksQuarantined: 3,
      routingRulesDisabled: 1,
      runsNotResumed: 1,
      tasksBlocked: 1,
      approvalsCancelled: 2,
      providersNeedingKeys: null,
    });
    expect(rows.length).toBe(9);
    expect(rows[0]!.slice(0, 2)).toEqual(['Memory entries proposed', 2]);
    expect(rows.find((r) => r[0] === 'Agent purposes cleared')![1]).toBe(0);
    expect(rows.find((r) => r[0] === 'Agent permission modes lowered')![2]).toContain('acceptEdits');
  });
});

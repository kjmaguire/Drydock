import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Project } from '../../api/types.ts';
import { RUNNING_STATUSES } from '../../state/workspace.ts';
import { pickPill, pickUp } from './workspace.ts';

const p = (id: string, created: number) => ({ id, name: id, repoPath: '', created }) as Project;

describe('Pick up where you left off (P142)', () => {
  it('orders the projects by their latest activity and says what runs, what needs you and what is happening', () => {
    const items = pickUp(
      [p('quiet', 5_000_000_000_000), p('busy', 1), p('waiting', 1), p('done', 1)],
      [
        { projectId: 'busy', status: 'IN_PROGRESS', title: 'Add Multiply', updatedAt: 9_000_000_000_000, doing: 'Editing calc.go' },
        { projectId: 'busy', status: 'REVIEW', title: 'Add Divide', updatedAt: 8_000_000_000_000 },
        { projectId: 'waiting', status: 'REVIEW', title: 'Pricing', updatedAt: 7_000_000_000_000 },
        { projectId: 'done', status: 'COMPLETED', title: 'Retry webhooks', updatedAt: 6_000_000_000_000 },
      ],
      [{ projectId: 'waiting', title: 'Pricing page ready to merge' }, { projectId: '', title: 'elsewhere' }],
      RUNNING_STATUSES,
    );
    expect(items.map((x) => [x.project.id, x.running, x.needs, x.line])).toEqual([
      ['busy', 2, 0, 'Add Multiply: Editing calc.go'],
      ['waiting', 1, 1, 'Pricing page ready to merge'],
      ['done', 0, 0, 'Last: Retry webhooks (completed)'],
      ['quiet', 0, 0, 'Nothing started yet.'],
    ]);
  });

  it("counts a project's chats: a chat is something to pick up, and the newer of a chat and a task is the line (critique 2026-11-08)", () => {
    const items = pickUp(
      [p('chatty', 1), p('both', 1), p('quiet', 1)],
      [{ projectId: 'both', status: 'COMPLETED', title: 'Add Divide', updatedAt: 2_000_000_000_000 }],
      [],
      RUNNING_STATUSES,
      [
        { projectId: 'chatty', title: 'How does parse work?', updatedAt: 3_000_000_000 },
        { projectId: 'both', title: 'Older chat', updatedAt: 1_500_000_000_000 },
      ],
    );
    expect(items.map((x) => [x.project.id, x.line, x.last])).toEqual([
      ['chatty', 'Chat: How does parse work?', 3_000_000_000_000],
      ['both', 'Last: Add Divide (completed)', 2_000_000_000_000],
      ['quiet', 'Nothing started yet.', 1000],
    ]);
  });

  it('reads a creation time in seconds or milliseconds', () => {
    expect(pickUp([p('s', 1_700_000_000)], [], [], RUNNING_STATUSES)[0]!.last).toBe(1_700_000_000_000);
    expect(pickUp([p('ms', 1_700_000_000_000)], [], [], RUNNING_STATUSES)[0]!.last).toBe(1_700_000_000_000);
  });
});

describe('the pill a project carries (B9)', () => {
  it('says needs you, then how many run, then when something last happened', () => {
    const now = Date.parse('2026-11-02T12:00:00Z');
    expect(pickPill({ needs: 1, running: 2, last: now }, now)).toEqual({ tone: 'warn', text: 'needs you' });
    expect(pickPill({ needs: 0, running: 2, last: now }, now)).toEqual({ tone: 'run', text: '2 running' });
    expect(pickPill({ needs: 0, running: 0, last: now - 2 * 86_400_000 }, now)).toEqual({ tone: '', text: '2d' });
  });
});

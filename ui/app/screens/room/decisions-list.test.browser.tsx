import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { RoomDecision } from '../../api/runClient.ts';
import type { Room, TaskCreateReq } from '../../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { RoomDecisions } from './decisions-list.tsx';

afterEach(cleanup);

const decisions: Room = { id: 'r9', projectId: 'p1', kind: 'channel', name: 'decisions', mode: 'auto', archived: false, createdAt: '', version: 1 };
const general: Room = { ...decisions, id: 'r1', name: 'general' };
const dec = (over: Partial<RoomDecision>): RoomDecision => ({
  title: 'Sessions in SQLite',
  status: 'proposed',
  context: '',
  decision: 'Use SQLite',
  consequences: '',
  alternatives: [],
  source_message_ids: ['m1'],
  proposed_by: 'human:kyle',
  roomId: 'r1',
  roomName: 'general',
  roomKind: 'channel',
  messageId: 'm1',
  ...over,
});

describe('RoomDecisions (X110)', () => {
  it("lists every room's decisions in #decisions with where each was taken, and makes an ADR task in one click", async () => {
    const made: TaskCreateReq[] = [];
    const s = testServices({
      roomDecisions: async () => [dec({}), dec({ title: 'Cache with Redis', status: 'accepted', roomId: 'r2', roomName: 'task-3', roomKind: 'task', messageId: 'm7' })],
      taskCreate: async (req: TaskCreateReq) => (made.push(req), { id: 't44', seq: 44 }) as never,
    });
    const r = mount(<RoomDecisions room={decisions} />, s);
    const rows = await waitFor(() => {
      const rs = r.$$('[data-testid="room-decision"]');
      if (rs.length !== 2) throw new Error('rows');
      return rs;
    }, r);
    expect(rows[0]!.textContent).toContain('Sessions in SQLite');
    expect(rows[0]!.querySelector('a.where')!.textContent).toBe('in #general');
    expect(rows[1]!.textContent).toContain('accepted');
    await r.act(() => (rows[0]!.querySelector('[data-testid="room-decision-make-task"]') as HTMLElement).click());
    await waitFor(() => expect(made.map((m) => [m.projectId, m.title, m.labels])).toEqual([['p1', 'ADR: Sessions in SQLite', ['adr']]]), r);
    const link = await waitFor(() => r.$('[data-testid="room-decision-task"]')!, r);
    expect(link.getAttribute('href')).toBe('/task/t44');
    expect(r.$$('[data-testid="room-decision-make-task"]')).toHaveLength(1);
  });

  it("shows only a room's own decisions elsewhere, and says when there are none", async () => {
    const r = mount(<RoomDecisions room={general} />, testServices({ roomDecisions: async () => [dec({ roomId: 'r2' })] }));
    await waitFor(() => expect(r.$('[data-testid="room-decisions"] p')?.textContent).toBe('No decisions in this room yet.'), r);
  });
});

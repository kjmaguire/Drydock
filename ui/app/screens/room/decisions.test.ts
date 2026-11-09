import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { RoomDecision } from '../../api/runClient.ts';
import { adrTaskRequest, decisionsFor, decisionWhere } from './decisions.ts';

const d = (over: Partial<RoomDecision>): RoomDecision => ({
  title: 'Sessions in SQLite',
  status: 'proposed',
  context: '',
  decision: 'Use SQLite for sessions',
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

describe('decisions (X110)', () => {
  it('lists every room in #decisions and the room its own elsewhere', () => {
    const all = [d({}), d({ roomId: 'r2', roomName: 'task-3', roomKind: 'task' })];
    expect(decisionsFor(all, { id: 'r9', kind: 'channel', name: 'decisions' })).toHaveLength(2);
    expect(decisionsFor(all, { id: 'r2', kind: 'task', name: 'task-3' }).map((x) => x.roomId)).toEqual(['r2']);
    expect(decisionWhere(d({ roomKind: 'dm' }))).toBe('a direct message');
  });

  it('makes an ADR task from a decision, with what was decided and where', () => {
    const req = adrTaskRequest('p1', d({ context: 'Logins must survive restarts', alternatives: ['Redis', 'JWT'], source_message_ids: ['m0', 'm1'] }));
    expect(req.projectId).toBe('p1');
    expect(req.title).toBe('ADR: Sessions in SQLite');
    expect(req.labels).toEqual(['adr']);
    expect(req.description).toContain('taken in #general');
    expect(req.description).toContain('Decision: Use SQLite for sessions');
    expect(req.description).toContain('Context: Logins must survive restarts');
    expect(req.description).toContain('Alternatives considered: Redis; JWT');
    expect(req.description).toContain('Source messages: m0, m1');
  });
});

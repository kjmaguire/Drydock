import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Agent, RoomMember } from '../../api/types.ts';
import { defaultModerator, defaultOrder, inOrder, modeWords } from './turn-taking.ts';

const agent = (id: string, name: string, role: string): Agent => ({ id, projectId: 'p', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: '', status: '' });
const member = (kind: 'human' | 'agent', id: string, state = 'active'): RoomMember => ({ roomId: 'r', member: { kind, id }, role: '', state, addedAt: '', lastReadSeq: 0 });

describe('turn-taking in words (X14)', () => {
  it('the default order is the active agents in the order they joined', () => {
    expect(defaultOrder([member('human', 'kyle'), member('agent', 'a2'), member('agent', 'a1'), member('agent', 'a3', 'observer')])).toEqual(['a2', 'a1']);
    expect(inOrder(['Ada', 'Bo', 'Cy'])).toBe('Ada, then Bo, then Cy');
  });

  it('the moderator defaults to the lead when it is in the room, otherwise to you', () => {
    const agents = [agent('a1', 'Ada', 'backend'), agent('l1', 'Lee', 'team lead')];
    expect(defaultModerator([member('human', 'kyle'), member('agent', 'a1'), member('agent', 'l1')], agents)).toBe('l1');
    expect(defaultModerator([member('human', 'kyle'), member('agent', 'a1')], agents)).toBe('human:kyle');
    expect(defaultModerator([], agents)).toBe('');
  });

  it('the chip says who answers', () => {
    const agents = [agent('l1', 'Lee', 'lead')];
    expect(modeWords({ mode: 'auto' }, agents)).toBe('Agents answer when mentioned');
    expect(modeWords({ mode: 'sequential' }, agents)).toBe('Agents take turns');
    expect(modeWords({ mode: 'moderator', modeParams: { moderator: 'l1' } }, agents)).toBe('Lee picks who answers');
    expect(modeWords({ mode: 'moderator', modeParams: { moderator: 'human:kyle' } }, agents)).toBe('You pick who answers');
  });
});

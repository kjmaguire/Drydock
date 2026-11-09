import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Message, RoomMember } from '../../api/types.ts';
import { askableAgents, askAgainOf, newestHumanMessage, outcomeWords } from './asks.ts';

const sys = (over: Partial<Message>): Message => ({ id: 'x', room_id: 'r', seq: 1, type: 'ERROR', author: { kind: 'system', id: 'system' }, body: '', trust: 'trusted', created_at: '', ...over });

describe('asking an agent to answer (X12)', () => {
  it('says why nobody answered in words, and nothing when an agent did', () => {
    expect(outcomeWords({ turns: [], bounded: false, why: 'nobody addressed' })).toBe('No agent answered: nobody was mentioned.');
    expect(outcomeWords({ turns: null, bounded: false, why: 'only invited speakers speak' })).toBe('No agent answered: in this room the moderator picks who answers.');
    expect(outcomeWords({ turns: [], bounded: false, stopped: 'agent replies are switched off for this project' })).toBe("No agent answered: agents do not answer in this project's rooms.");
    expect(outcomeWords({ turns: [], bounded: true })).toContain('round limit');
    expect(outcomeWords({ turns: [{ turnId: 't', agentId: 'a', status: 'done', reason: 'mention' }], bounded: false })).toBe('');
    expect(outcomeWords(null)).toBe('');
  });

  it('offers Ask again on the lines of a turn that gave no answer, for that agent and message', () => {
    const person = sys({ id: 'm1', type: 'CHAT', author: { kind: 'human', id: 'kyle' } });
    const of = (id: string) => (id === 'm1' ? person : undefined);
    expect(askAgainOf(sys({ reply_to_id: 'm1', payload: { code: 'agent_turn_failed', agent_id: 'a1' } }), of)).toEqual({ messageId: 'm1', agentId: 'a1' });
    expect(askAgainOf(sys({ type: 'SYSTEM', reply_to_id: 'm1', payload: { code: 'agent_turn_interrupted', agent_id: 'a1' } }), of)).toEqual({ messageId: 'm1', agentId: 'a1' });
    expect(askAgainOf(sys({ reply_to_id: 'm1', payload: { code: 'empty_reply', agent_id: 'a1' } }), of)).toEqual({ messageId: 'm1', agentId: 'a1' });
    expect(askAgainOf(sys({ reply_to_id: 'm1', payload: { code: 'replies_off' } }), of)).toBeUndefined();
    expect(askAgainOf(sys({ payload: { code: 'agent_turn_failed', agent_id: 'a1' } }), of)).toBeUndefined();
    expect(askAgainOf(sys({ author: { kind: 'tool', id: 'lint' }, reply_to_id: 'm1', payload: { code: 'agent_turn_failed', agent_id: 'a1' } }), of)).toBeUndefined();
  });

  it("does not offer Ask again on a chained turn, whose trigger is another agent's reply", () => {
    const ada = sys({ id: 'm2', type: 'CHAT', author: { kind: 'agent', id: 'a1' }, reply_to_id: 'm1' });
    const steer = sys({ id: 'm3', type: 'CHAT', author: { kind: 'human', id: 'kyle' }, payload: { via: 'steer' } });
    const of = (id: string) => (id === 'm2' ? ada : id === 'm3' ? steer : undefined);
    expect(askAgainOf(sys({ reply_to_id: 'm2', payload: { code: 'agent_turn_failed', agent_id: 'a2' } }), of)).toBeUndefined();
    expect(askAgainOf(sys({ reply_to_id: 'm3', payload: { code: 'agent_turn_failed', agent_id: 'a2' } }), of)).toBeUndefined();
    // a message that is not loaded is not known to be the person's: no button rather than one that fails
    expect(askAgainOf(sys({ reply_to_id: 'm9', payload: { code: 'agent_turn_failed', agent_id: 'a2' } }), of)).toBeUndefined();
  });

  it('asks agents that are not muted, about the newest message the person wrote', () => {
    const m = (id: string, kind: string, state: string) => ({ roomId: 'r', member: { kind, id }, state, addedAt: '', lastReadSeq: 0 }) as RoomMember;
    expect(askableAgents([m('kyle', 'human', 'active'), m('a1', 'agent', 'active'), m('a2', 'agent', 'observer'), m('a3', 'agent', 'muted')])).toEqual(['a1', 'a2']);
    const msgs = [sys({ id: 'm1', type: 'CHAT', author: { kind: 'human', id: 'kyle' } }), sys({ id: 'm2', type: 'CHAT', author: { kind: 'human', id: 'kyle' }, payload: { via: 'steer' } }), sys({ id: 'm3', type: 'CHAT', author: { kind: 'agent', id: 'a1' } })];
    expect(newestHumanMessage(msgs)).toBe('m1');
    expect(newestHumanMessage([])).toBeUndefined();
  });
});

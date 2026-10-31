import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { createClient } from './client.ts';
import type { Transport } from './transport.ts';

// a TEST DOUBLE of the transport: records each call and answers what the core would (Go's nil slices as null)
function recording(answers: Record<string, unknown>) {
  const calls: [string, unknown][] = [];
  const t = {
    call: async (method: string, params?: unknown) => {
      calls.push([method, params]);
      return answers[method] ?? null;
    },
  } as unknown as Transport;
  return { c: createClient(t), calls };
}

describe('the room calls of P130', () => {
  it('names each core method with its parameters and turns null lists into empty ones', async () => {
    const { c, calls } = recording({ 'room.stop': { stopped: 2 }, 'room.setMuted': { roomId: 'r', muted: true } });
    expect(await c.roomStop('r')).toBe(2);
    expect((await c.roomSetMuted('r', true)).muted).toBe(true);
    expect(await c.roomJoinRules('p')).toEqual([]);
    await c.roomJoinRuleAdd({ projectId: 'p', agentId: 'a' });
    await c.roomJoinRuleRemove('jr1');
    await c.roomAnswerJoin('jq1', false);
    expect(calls).toEqual([
      ['room.stop', { roomId: 'r' }],
      ['room.setMuted', { roomId: 'r', muted: true }],
      ['room.joinRules', { projectId: 'p' }],
      ['room.joinRule.add', { pathPrefix: '', state: '', projectId: 'p', agentId: 'a' }],
      ['room.joinRule.remove', { id: 'jr1' }],
      ['room.answerJoin', { id: 'jq1', allow: false }],
    ]);
  });
});

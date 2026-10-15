import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { RoomTurn } from '../../api/extTypes.ts';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { RoomAgentsPanel } from './room-agents.tsx';

afterEach(cleanup);

const agent = (id: string, name: string, role: string): Agent => ({ id, projectId: 'prj_1', name, adapter: 'claude', role, model: '', effort: '', providerId: '', permissionMode: '', status: '' });
const room: Room = { id: 'room_1', projectId: 'prj_1', kind: 'channel', name: 'general', mode: 'auto', modeParams: { max_rounds: 3 }, archived: false, createdAt: '', version: 2 };
const members: RoomMember[] = [
  { roomId: 'room_1', member: { kind: 'human', id: 'kyle' }, role: 'owner', state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'agent', id: 'a1' }, role: 'backend', state: 'active', addedAt: '', lastReadSeq: 0 },
];
const agents = [agent('a1', 'Dana', 'backend'), agent('a2', 'Rex', 'reviewer')];
const button = (r: { container: HTMLElement }, name: string) => byText<HTMLButtonElement>(r.container, 'button', name);

describe('RoomAgentsPanel', () => {
  it('is a collapsible bar that says what it is', () => {
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, testServices({ roomTurns: async () => [] }));
    const d = r.container.querySelector<HTMLDetailsElement>('details[data-testid="room-agents"]')!;
    expect(d.querySelector('summary')!.textContent).toContain('Agents in this room and how they answer');
    expect(d.textContent).toContain('Current mode: auto');
  });

  it('explains each mode in plain language and saves it with the right parameters', async () => {
    const calls: unknown[] = [];
    const s = testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [], roomInvite: async () => [], roomReply: async () => ({}) });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const text = () => r.container.querySelector('[data-testid="mode-text"]')!.textContent;
    expect(text()).toContain('No model chooses the speaker');
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Mode'), 'moderator'));
    expect(text()).toContain('names who speaks next');
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Moderator'), 'a1'));
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'moderator', order: [], moderator: 'a1', maxRounds: 3 }]), r);
    await r.act(() => type(byLabel<HTMLSelectElement>(r.container, 'Mode'), 'sequential'));
    expect(text()).toContain('one turn each per round');
    await r.act(() => button(r, 'Add Dana').click());
    await r.act(() => button(r, 'Add kyle').click());
    expect(r.container.textContent).toContain('Speaking order: Dana > kyle');
    expect(button(r, 'Add Dana').disabled).toBe(true);
    await r.act(() => type(byLabel<HTMLInputElement>(r.container, 'Round limit'), '5'));
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(calls[1]).toEqual({ roomId: 'room_1', mode: 'sequential', order: ['a1', 'human:kyle'], moderator: '', maxRounds: 5 }), r);
  });

  it('shows the core refusal', async () => {
    const s = testServices({
      roomSetMode: async () => {
        throw new Error('moderator mode needs a moderator');
      },
      roomTurns: async () => [],
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('moderator mode needs a moderator'), r);
  });

  it('invites an agent, removes one, asks for a reply and lists turns with cost not reported when zero', async () => {
    const invites: unknown[][] = [];
    const removes: unknown[][] = [];
    const replies: string[] = [];
    const turn: RoomTurn = { turnId: 'tr1', roomId: 'room_1', triggerId: 'msg_1', agentId: 'a1', round: 1, reason: 'mention', mode: 'auto', status: 'completed', contextMessages: 3, contextTokens: 100, inputTokens: 100, outputTokens: 20, costUsd: 0, startedAt: 1, endedAt: 2 };
    const s = testServices({
      roomInvite: async (...a: unknown[]) => (invites.push(a), []),
      roomRemoveMember: async (...a: unknown[]) => (removes.push(a), []),
      roomReply: async (id: string) => (replies.push(id), {}),
      roomSetMode: async () => room,
      roomTurns: async () => [turn],
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const invite = byLabel<HTMLSelectElement>(r.container, 'Invite an agent');
    expect(Array.from(invite.options).map((o) => o.textContent)).toEqual(['Invite an agent...', 'Rex (reviewer)']);
    expect(button(r, 'Invite').disabled).toBe(true);
    await r.act(() => type(invite, 'a2'));
    await r.act(() => button(r, 'Invite').click());
    await waitFor(() => expect(invites).toEqual([['room_1', 'a2']]), r);
    await waitFor(() => expect(invite.value).toBe(''), r);
    await r.act(() => button(r, 'Remove').click());
    await waitFor(() => expect(removes).toEqual([['room_1', 'a1']]), r);
    await r.act(() => button(r, 'Ask agents to reply to my last message').click());
    await waitFor(() => expect(replies).toEqual(['msg_1']), r);
    const row = await waitFor(() => r.container.querySelector('[data-testid="room-turn"]')!, r);
    expect(row.textContent).toContain('not reported');
    expect(row.textContent).toContain('mention');
    expect(row.textContent).toContain('100/20 tokens');
  });

  it('shows the model a routed reply ran on and opens its "Why this model?"; an older turn says the model was not recorded', async () => {
    const asked: string[] = [];
    const base: RoomTurn = { turnId: 'tr1', roomId: 'room_1', triggerId: 'msg_1', agentId: 'a1', round: 1, reason: 'mention', mode: 'auto', status: 'done', contextMessages: 3, contextTokens: 100, inputTokens: 100, outputTokens: 20, costUsd: 0, startedAt: 2, endedAt: 3 };
    const s = testServices({
      roomTurns: async () => [
        { ...base, model: 'sonnet', decisionId: 'dec_r1' },
        { ...base, turnId: 'tr0', startedAt: 1 },
      ],
      routeExplain: async (id: string) => (
        asked.push(id),
        { text: '- Turn: room reply; a reply is usually light.', decision: { ID: id, Provider: 'agent:claude', Model: 'sonnet', Effort: '', Reason: '', Confidence: 1, EstCostUSD: null, DecidedBy: 'rules' } } as never
      ),
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    const cells = await waitFor(() => {
      const x = r.$$('[data-testid="room-turn-model"]');
      if (x.length !== 2) throw new Error('not yet');
      return x;
    }, r);
    expect(cells[0]!.textContent).toContain('sonnet');
    expect(cells[1]!.textContent).toContain('not recorded');
    expect(cells[1]!.querySelector('button')).toBeNull();
    await r.act(() => byText(cells[0]!, 'button', 'Why this model?').click());
    await waitFor(() => expect(r.container.querySelector('[data-testid="explain"]')!.textContent).toContain('Turn: room reply'), r);
    expect(asked).toEqual(['dec_r1']);
  });

  it('says when no agent is in the room and none has answered', async () => {
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members.slice(0, 1)} />, testServices({ roomTurns: async () => [] }));
    await waitFor(() => expect(r.container.textContent).toContain('No agent has answered in this room.'), r);
    expect(r.container.textContent).toContain('No agent is in this room.');
    expect(r.container.textContent).not.toContain('Ask agents to reply');
  });
});

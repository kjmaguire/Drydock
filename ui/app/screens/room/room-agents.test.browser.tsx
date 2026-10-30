import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { RoomTurn } from '../../api/extTypes.ts';
import type { JoinRule } from '../../api/roomClient.ts';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { byLabel, byText, cleanup, errorOf, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { ROUNDS_ERROR, RoomAgentsPanel, RoomTab } from './room-agents.tsx';

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
  it('lays out the mode, the agents and the project\'s join rules for the Room tab, with the turns folded away (UI review P1-5, P130)', () => {
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, testServices({ roomTurns: async () => [], roomJoinRules: async () => [] }));
    const panel = r.container.querySelector<HTMLElement>('[data-testid="room-agents"]')!;
    expect(panel.tagName).toBe('DIV');
    expect(panel.textContent).toContain('Current mode: auto');
    expect(Array.from(panel.querySelectorAll('section')).map((x) => x.getAttribute('aria-label'))).toEqual(['Turn-taking mode', 'Agents', 'Join rules']);
    const turns = panel.querySelector<HTMLDetailsElement>('details[data-testid="room-turns"]')!;
    expect([turns.open, turns.querySelector('summary')!.textContent]).toEqual([false, 'Recent agent turns']);
  });

  it('the Room tab reads the room, its members and the newest human message itself; an archived room only says so', async () => {
    const replies: string[] = [];
    let archived = false;
    const msg = (id: string, kind: 'human' | 'agent', seq: number) => ({ id, roomId: 'room_1', seq, author: { kind, id: kind === 'human' ? 'kyle' : 'a1' }, type: 'CHAT', body: 'x', createdAt: '' }) as never;
    const s = testServices({
      roomGet: async () => ({ ...room, archived }),
      roomMembers: async () => members,
      roomMessagesLatest: async () => [msg('m1', 'human', 1), msg('m2', 'agent', 2)],
      roomTurns: async () => [],
      roomReply: async (id: string) => (replies.push(id), {}) as never,
      agentList: async () => agents,
    });
    s.workspace.start();
    s.project.start();
    const r = mount(<RoomTab roomId="room_1" />, s);
    await waitFor(() => expect(r.container.textContent).toContain('Current mode: auto'), r);
    await waitFor(() => button(r, 'Ask agents to reply to my last message'), r);
    await r.act(() => button(r, 'Ask agents to reply to my last message').click());
    await waitFor(() => expect(replies).toEqual(['m1']), r);
    cleanup();
    archived = true;
    const r2 = mount(<RoomTab roomId="room_1" />, s);
    await waitFor(() => expect(r2.container.textContent).toContain('This channel is archived'), r2);
    expect(r2.container.querySelector('[data-testid="room-agents"]')).toBeNull();
  });

  it('explains each mode in plain language and saves it with the right parameters', async () => {
    const calls: unknown[] = [];
    const s = testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [], roomInvite: async () => [], roomReply: async () => ({}) });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const text = () => r.container.querySelector('[data-testid="mode-text"]')!.textContent;
    expect(text()).toContain('No model chooses the speaker');
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'moderator'));
    expect(text()).toContain('names who speaks next');
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Moderator'), 'a1'));
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'moderator', order: [], moderator: 'a1', maxRounds: 3 }]), r);
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'sequential'));
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

  it('a round limit that is not a whole number is told under the field and nothing is sent; an empty one is the default (M25)', async () => {
    const calls: unknown[] = [];
    const s = testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [] });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    const limit = () => byLabel<HTMLInputElement>(r.container, 'Round limit');
    for (const bad of ['three', '3.5', '1e3', '0', '-2', '5 rounds']) {
      await r.act(() => type(limit(), bad));
      await r.act(() => button(r, 'Save mode').click());
      await waitFor(() => expect(errorOf(limit())).toBe(ROUNDS_ERROR), r);
      // the keyboard goes to the field that needs fixing, and the page is not left saying it saved
      await waitFor(() => expect(document.activeElement).toBe(limit()), r);
    }
    expect(calls).toEqual([]);
    // typing again takes the old error away; a whole number saves
    await r.act(() => type(limit(), '4'));
    expect(errorOf(limit())).toBe(null);
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'auto', order: [], moderator: '', maxRounds: 4 }]), r);
    // an empty field still means "the core's default" (0)
    await r.act(() => type(limit(), ''));
    await r.act(() => button(r, 'Save mode').click());
    await waitFor(() => expect(calls[1]).toEqual({ roomId: 'room_1', mode: 'auto', order: [], moderator: '', maxRounds: 0 }), r);
    expect(errorOf(limit())).toBe(null);
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
    const invite = byLabel<HTMLElement>(r.container, 'Invite an agent');
    expect(optionsOf(invite).map((o) => o.label)).toEqual(['Invite an agent...', 'Rex (reviewer)']);
    expect(button(r, 'Invite').disabled).toBe(true);
    await r.act(() => type(invite, 'a2'));
    await r.act(() => button(r, 'Invite').click());
    await waitFor(() => expect(invites).toEqual([['room_1', 'a2']]), r);
    await waitFor(() => expect(valueOf(invite)).toBe(''), r);
    await r.act(() => button(r, 'Remove').click());
    await r.act(() => button(r, 'Yes, remove Dana from this room').click());
    await waitFor(() => expect(removes).toEqual([['room_1', 'a1']]), r);
    await r.act(() => button(r, 'Ask agents to reply to my last message').click());
    await waitFor(() => expect(replies).toEqual(['msg_1']), r);
    const row = await waitFor(() => r.container.querySelector('[data-testid="room-turn"]')!, r);
    expect(row.textContent).toContain('not reported');
    expect(row.textContent).toContain('mention');
    expect(row.textContent).toContain('100/20 tokens');
  });

  // audit 2026-10-16, L40: a removed member loses its place in the room, so the removal asks first
  it('removing an agent from the room asks first: one click sends nothing, Keep cancels, Yes removes it once', async () => {
    const removes: unknown[][] = [];
    const s = testServices({ roomRemoveMember: async (...a: unknown[]) => (removes.push(a), []), roomTurns: async () => [] });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await r.act(() => button(r, 'Remove').click());
    expect(removes).toEqual([]);
    expect(r.$('[role="group"]')!.getAttribute('aria-label')).toBe('Remove Dana from this room? It stops taking part here, and inviting it again starts it fresh.');
    await r.act(() => button(r, 'Keep Dana in this room').click());
    await waitFor(() => button(r, 'Remove'), r);
    expect(removes).toEqual([]);
    await r.act(() => button(r, 'Remove').click());
    await r.act(() => {
      const yes = button(r, 'Yes, remove Dana from this room');
      yes.click();
      yes.click();
    });
    await waitFor(() => expect(removes).toEqual([['room_1', 'a1']]), r);
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

describe('JoinRules (P130)', () => {
  it('lists the project\'s rules in words, adds one with its folder and state, and removes one', async () => {
    const added: unknown[] = [];
    const removed: string[] = [];
    let rules: JoinRule[] = [{ id: 'jr1', projectId: 'prj_1', agentId: 'a2', pathPrefix: 'auth/', state: 'observer', createdBy: 'human:kyle', createdAt: 1 }];
    const s = testServices({
      roomTurns: async () => [],
      roomJoinRules: async () => rules,
      roomJoinRuleAdd: async (rule) => {
        added.push(rule);
        const r = { id: 'jr2', projectId: rule.projectId, agentId: rule.agentId, pathPrefix: rule.pathPrefix, state: rule.state ?? 'observer', createdBy: 'human:kyle', createdAt: 2 };
        rules = [...rules, r];
        return r;
      },
      roomJoinRuleRemove: async (id) => {
        removed.push(id);
        rules = rules.filter((r) => r.id !== id);
      },
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="join-rule"]').length).toBe(1), r);
    expect(r.container.querySelector('[data-testid="join-rule"]')!.textContent).toContain('Rex joins as an observer: tasks naming a file under auth/');
    await r.act(() => type(byLabel(r.container, 'Agent for the rule'), 'a1'));
    await r.act(() => type(byLabel(r.container, 'Folder (optional)'), ' web/ '));
    await r.act(() => type(byLabel(r.container, 'Joins as'), 'active'));
    await r.act(() => button(r, 'Add rule').click());
    await waitFor(() => expect(added).toEqual([{ projectId: 'prj_1', agentId: 'a1', pathPrefix: 'web/', state: 'active' }]), r);
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="join-rule"]').length).toBe(2), r);
    expect(r.container.querySelectorAll('[data-testid="join-rule"]')[1]!.textContent).toContain('Dana joins as an active member: tasks naming a file under web/');
    await r.act(() => byLabel<HTMLButtonElement>(r.container, 'Remove the rule for Rex').click());
    await waitFor(() => expect(removed).toEqual(['jr1']), r);
  });

  it('is not shown in a direct message', () => {
    const dm: Room = { ...room, kind: 'dm', name: 'dm:human:kyle+a1', agentId: 'a1' };
    const r = mount(<RoomAgentsPanel room={dm} agents={agents} members={members} />, testServices({ roomTurns: async () => [], roomJoinRules: async () => [] }));
    expect(r.container.querySelector('[data-testid="join-rules"]')).toBe(null);
  });
});

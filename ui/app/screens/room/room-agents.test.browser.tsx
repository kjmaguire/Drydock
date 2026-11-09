import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { RoomTurn } from '../../api/extTypes.ts';
import type { JoinRule } from '../../api/roomClient.ts';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { byLabel, byText, cleanup, errorOf, mount, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
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

  it('choosing a mode applies it; the default order and the round limit show in words; the moderator starts as the lead or you (X14)', async () => {
    const calls: unknown[] = [];
    const s = testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [], roomInvite: async () => [], roomReply: async () => ({ trigger: '', turns: [], bounded: false }) });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const text = () => r.container.querySelector('[data-testid="mode-text"]')!.textContent;
    expect(text()).toContain('No model chooses the speaker');
    expect(r.container.textContent).not.toContain('Save mode');
    expect(r.container.querySelector('[data-testid="round-limit"]')!.textContent).toContain('Round limit 3');
    // no lead in the room: you are the moderator, and the mode is saved on the choice
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'moderator'));
    expect(text()).toContain('names who speaks next');
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'moderator', order: [], moderator: 'human:kyle', maxRounds: 3 }]), r);
    expect(valueOf(byLabel<HTMLElement>(r.container, 'Moderator'))).toBe('human:kyle');
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Moderator'), 'a1'));
    await waitFor(() => expect(calls[1]).toEqual({ roomId: 'room_1', mode: 'moderator', order: [], moderator: 'a1', maxRounds: 3 }), r);
    // sequential: the order the core uses is said in words; Change sets another
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'sequential'));
    await waitFor(() => expect(calls[2]).toEqual({ roomId: 'room_1', mode: 'sequential', order: [], moderator: '', maxRounds: 3 }), r);
    expect(text()).toContain('one turn each per round');
    expect(r.container.querySelector('[data-testid="speaking-order"]')!.textContent).toContain('Order: Dana (the order they joined)');
    await r.act(() => button(r, 'Change order').click());
    await r.act(() => button(r, 'Add kyle').click());
    await r.act(() => button(r, 'Add Dana').click());
    expect(r.container.querySelector('[data-testid="speaking-order"]')!.textContent).toContain('Order: kyle, then Dana');
    expect(button(r, 'Add Dana').disabled).toBe(true);
    await r.act(() => button(r, 'Save order').click());
    await waitFor(() => expect(calls[3]).toEqual({ roomId: 'room_1', mode: 'sequential', order: ['human:kyle', 'a1'], moderator: '', maxRounds: 3 }), r);
    await r.act(() => button(r, 'Change round limit').click());
    await r.act(() => type(byLabel<HTMLInputElement>(r.container, 'Round limit'), '5'));
    await r.act(() => button(r, 'Save round limit').click());
    await waitFor(() => expect(calls[4]).toEqual({ roomId: 'room_1', mode: 'sequential', order: ['human:kyle', 'a1'], moderator: '', maxRounds: 5 }), r);
  });

  it('a moderator room starts with the team lead when it is in the room (X14)', async () => {
    const calls: unknown[] = [];
    const lead = agent('l1', 'Lee', 'team lead');
    const withLead = [...members, { roomId: 'room_1', member: { kind: 'agent', id: 'l1' }, role: 'team lead', state: 'active', addedAt: '', lastReadSeq: 0 } as RoomMember];
    const r = mount(<RoomAgentsPanel room={room} agents={[...agents, lead]} members={withLead} />, testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [] }));
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'moderator'));
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'moderator', order: [], moderator: 'l1', maxRounds: 3 }]), r);
  });

  it('shows the core refusal and goes back to the room\'s mode', async () => {
    const s = testServices({
      roomSetMode: async () => {
        throw new Error('moderator mode needs a moderator');
      },
      roomTurns: async () => [],
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await r.act(() => type(byLabel<HTMLElement>(r.container, 'Mode'), 'sequential'));
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('moderator mode needs a moderator'), r);
    await waitFor(() => expect(valueOf(byLabel<HTMLElement>(r.container, 'Mode'))).toBe('auto'), r);
  });

  it('a round limit that is not a whole number is told under the field and nothing is sent; an empty one is the default (M25)', async () => {
    const calls: unknown[] = [];
    const s = testServices({ roomSetMode: async (req: unknown) => (calls.push(req), room), roomTurns: async () => [] });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await r.act(() => button(r, 'Change round limit').click());
    const limit = () => byLabel<HTMLInputElement>(r.container, 'Round limit');
    for (const bad of ['three', '3.5', '1e3', '0', '-2', '5 rounds']) {
      await r.act(() => type(limit(), bad));
      await r.act(() => button(r, 'Save round limit').click());
      await waitFor(() => expect(errorOf(limit())).toBe(ROUNDS_ERROR), r);
      // the keyboard goes to the field that needs fixing, and the page is not left saying it saved
      await waitFor(() => expect(document.activeElement).toBe(limit()), r);
    }
    expect(calls).toEqual([]);
    // typing again takes the old error away; a whole number saves
    await r.act(() => type(limit(), '4'));
    expect(errorOf(limit())).toBe(null);
    await r.act(() => button(r, 'Save round limit').click());
    await waitFor(() => expect(calls).toEqual([{ roomId: 'room_1', mode: 'auto', order: [], moderator: '', maxRounds: 4 }]), r);
    // an empty field still means "the core's default" (0)
    await r.act(() => button(r, 'Change round limit').click());
    await r.act(() => type(limit(), ''));
    await r.act(() => button(r, 'Save round limit').click());
    await waitFor(() => expect(calls[1]).toEqual({ roomId: 'room_1', mode: 'auto', order: [], moderator: '', maxRounds: 0 }), r);
  });

  it('invites an agent in one click, removes one, asks for a reply and lists turns with cost not reported when zero (X15)', async () => {
    const invites: unknown[][] = [];
    const removes: unknown[][] = [];
    const replies: string[] = [];
    const turn: RoomTurn = { turnId: 'tr1', roomId: 'room_1', triggerId: 'msg_1', agentId: 'a1', round: 1, reason: 'mention', mode: 'auto', status: 'completed', contextMessages: 3, contextTokens: 100, inputTokens: 100, outputTokens: 20, costUsd: 0, startedAt: 1, endedAt: 2 };
    const s = testServices({
      roomInvite: async (...a: unknown[]) => (invites.push(a), []),
      roomRemoveMember: async (...a: unknown[]) => (removes.push(a), []),
      roomReply: async (id: string) => (replies.push(id), { trigger: id, turns: [], bounded: false, why: 'nobody addressed' }),
      roomSetMode: async () => room,
      roomTurns: async () => [turn],
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const invite = r.container.querySelector<HTMLElement>('[data-testid="room-invite"]')!;
    expect(Array.from(invite.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['Invite Rex']);
    await r.act(() => button(r, 'Invite Rex').click());
    await waitFor(() => expect(invites).toEqual([['room_1', 'a2']]), r);
    await r.act(() => button(r, 'Remove').click());
    await r.act(() => button(r, 'Yes, remove Dana from this room').click());
    await waitFor(() => expect(removes).toEqual([['room_1', 'a1']]), r);
    await r.act(() => button(r, 'Ask agents to reply to my last message').click());
    await waitFor(() => expect(replies).toEqual(['msg_1']), r);
    // the responder's outcome is said, not dropped (X12)
    await waitFor(() => expect(r.$('[data-testid="room-reply-note"]')?.textContent).toBe('No agent answered: nobody was mentioned.'), r);
    const row = await waitFor(() => r.container.querySelector('[data-testid="room-turn"]')!, r);
    expect(row.textContent).toContain('not reported');
    expect(row.textContent).toContain('mention');
    expect(row.textContent).toContain('100/20 tokens');
  });

  it('offers Ask again on a turn that gave no answer, once, and asks that agent on that message (X12)', async () => {
    const asked: unknown[][] = [];
    const base: RoomTurn = { turnId: 'tr1', roomId: 'room_1', triggerId: 'msg_1', agentId: 'a1', round: 1, reason: 'mention', mode: 'auto', status: 'failed', contextMessages: 0, contextTokens: 0, inputTokens: 0, outputTokens: 0, costUsd: 0, startedAt: 1, endedAt: 2, askable: true };
    const s = testServices({
      roomReply: async (...a: unknown[]) => (asked.push(a), { trigger: 'msg_1', turns: [{ turnId: 'tr3', agentId: 'a1', status: 'done', reason: 'asked' }], bounded: false }),
      roomSetMode: async () => room,
      // tr4 failed in a chain: its trigger is another agent's reply, which an agent is never asked to answer, so no Ask again
      roomTurns: async () => [{ ...base, turnId: 'tr4', triggerId: 'msg_agent', status: 'failed', startedAt: 4, askable: false }, { ...base, turnId: 'tr2', triggerId: 'msg_2', status: 'done', startedAt: 3 }, base],
    });
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, s);
    await waitFor(() => expect(r.$$('[data-testid="room-turn"]')).toHaveLength(3), r);
    const again = r.$$('[data-testid="room-turn-ask-again"]');
    expect(again).toHaveLength(1);
    await r.act(() => (again[0] as HTMLElement).click());
    await waitFor(() => expect(asked).toEqual([['msg_1', 'a1']]), r);
  });

  it('has no Invite at all when the whole crew is already in the room (X15)', () => {
    const r = mount(<RoomAgentsPanel room={room} agents={agents.slice(0, 1)} members={members} />, testServices({ roomTurns: async () => [] }));
    expect(r.container.querySelector('[data-testid="room-invite"]')).toBeNull();
    expect(r.container.textContent).not.toContain('Invite');
  });

  it('a direct message shows only its agent, with Inspect and Edit, and the turns: no turn-taking, Remove or Invite (X16)', async () => {
    const dm: Room = { ...room, kind: 'dm', name: 'dm:human:kyle+a1', agentId: 'a1' };
    const s = testServices({ roomTurns: async () => [], roomJoinRules: async () => [] });
    const r = mount(<RoomAgentsPanel room={dm} agents={agents} members={members} lastHumanMessageId="msg_1" />, s);
    const panel = r.container.querySelector<HTMLElement>('[data-testid="room-agents"]')!;
    expect(Array.from(panel.querySelectorAll('section')).map((x) => x.getAttribute('aria-label'))).toEqual(['Agents']);
    expect(panel.textContent).not.toContain('Remove');
    expect(panel.textContent).not.toContain('Invite');
    expect(panel.querySelector('[data-testid="room-turns"]')).not.toBeNull();
    expect(byLabel<HTMLAnchorElement>(panel, 'Edit Dana').getAttribute('href')).toBe('/team?edit=a1');
    await r.act(() => byLabel<HTMLButtonElement>(panel, 'Inspect Dana').click());
    expect(s.ui.inspectId).toBe('a1');
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

describe('JoinRules (P130, X17)', () => {
  it("says each agent's rule in words with Change to its editor; the rules are set there, not here", async () => {
    const rules: JoinRule[] = [
      { id: 'jr1', projectId: 'prj_1', agentId: 'a2', pathPrefix: 'auth/', state: 'observer', createdBy: 'human:kyle', createdAt: 1 },
      { id: 'jr2', projectId: 'prj_1', agentId: 'a1', pathPrefix: '', state: 'active', createdBy: 'human:kyle', createdAt: 2 },
      // a second folder rule of Rex is named too, not hidden behind the first
      { id: 'jr3', projectId: 'prj_1', agentId: 'a2', pathPrefix: 'api/', state: 'observer', createdBy: 'human:kyle', createdAt: 3 },
    ];
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, testServices({ roomTurns: async () => [], roomJoinRules: async () => rules }));
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="join-rule"]').length).toBe(2), r);
    const lines = Array.from(r.container.querySelectorAll('[data-testid="join-rule"]')).map((li) => li.querySelector('.what')!.textContent);
    expect(lines).toEqual(['Rex joins the rooms of tasks that touch auth/ or api/ as an observer', 'Dana joins every task room as an active member']);
    expect(byLabel<HTMLAnchorElement>(r.container, 'Change the join rule of Rex').getAttribute('href')).toBe('/team?edit=a2');
    expect(r.container.querySelector('[data-testid="join-rules"] input')).toBe(null);
    expect(r.container.textContent).not.toContain('Add rule');
  });

  it('says where to set one when none is set', async () => {
    const r = mount(<RoomAgentsPanel room={room} agents={agents} members={members} />, testServices({ roomTurns: async () => [], roomJoinRules: async () => [] }));
    await waitFor(() => expect(r.container.querySelector('[data-testid="join-rules-none"]')!.textContent).toBe("No agent joins task rooms by rule. Set it on an agent's editor, under More settings."), r);
  });

  it('is not shown in a direct message', () => {
    const dm: Room = { ...room, kind: 'dm', name: 'dm:human:kyle+a1', agentId: 'a1' };
    const r = mount(<RoomAgentsPanel room={dm} agents={agents} members={members} />, testServices({ roomTurns: async () => [], roomJoinRules: async () => [] }));
    expect(r.container.querySelector('[data-testid="join-rules"]')).toBe(null);
  });
});

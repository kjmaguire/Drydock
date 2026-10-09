import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Agent, Message, Room } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { Conversation, RoomScreen } from './room-screen.tsx';

afterEach(cleanup);

const q = <T extends Element = HTMLElement>(r: { container: HTMLElement }, sel: string) => r.container.querySelector<T>(sel);
const qa = (r: { container: HTMLElement }, sel: string) => Array.from(r.container.querySelectorAll<HTMLElement>(sel));

const msg = (over: Partial<Message>): Message => ({
  id: `m${over.seq ?? 1}`,
  room_id: 'r',
  seq: 1,
  type: 'CHAT',
  author: { kind: 'human', id: 'kyle' },
  body: 'hello',
  trust: 'trusted',
  created_at: '2026-01-01T00:00:00Z',
  ...over,
});

const taskRoom: Room = { id: 'r', projectId: 'p', kind: 'task', name: 'task-1', mode: 'auto', archived: false, createdAt: '', version: 1, taskId: 't1' };
const backend: Agent = { id: 'agt1', projectId: 'p', name: 'Backend #1', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '' };

/** Services with a selected project whose shared reads answer from the test doubles. */
function servicesWith(client: Partial<Client>, rooms: Room[] = []) {
  const s = testServices({
    projectList: async () => [{ id: 'p', name: 'gocalc' } as never],
    agentList: async () => [backend],
    roomList: async () => rooms,
    roomUnread: async () => [],
    presenceList: async () => [],
    taskList: async () => [],
    needsYouList: async () => [],
    roomMembers: async () => [],
    roomTurns: async () => [],
    roomMarkRead: async () => true,
    roomMessages: async () => [],
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const messagesOf = (r: { container: HTMLElement }) => qa(r, '[data-testid="message"]');

describe('Conversation', () => {
  it('renders author kinds, markers, check cards with failure summary, and inert agent text', async () => {
    const messages: Message[] = [
      msg({ seq: 1, body: 'plain **human**' }),
      msg({ seq: 2, author: { kind: 'agent', id: 'agt1' }, trust: 'untrusted', body: 'done <script>window.__xss=1</script>', markers: [{ marker: 'DECISION', line: 1 }] }),
      msg({ seq: 3, type: 'RESULT', author: { kind: 'tool', id: 'tests' }, body: 'Tests: test PASS', payload: { check_kind: 'test', check_status: 'pass', duration_ms: 12, log_ref: '/x/log' } }),
      msg({
        seq: 4,
        type: 'ERROR',
        author: { kind: 'tool', id: 'lint' },
        body: 'Lint: lint FAIL',
        payload: { check_kind: 'lint', check_status: 'fail', summary: 'calc.go:3: unused variable', failure_signature: 'ab12' },
      }),
      msg({ seq: 5, type: 'SYSTEM', author: { kind: 'system', id: 'system' }, body: 'task started' }),
    ];
    const s = servicesWith({ roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after) });
    const r = mount(<Conversation room={taskRoom} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(5), r);
    expect(messagesOf(r).map((m) => m.getAttribute('data-author-kind'))).toEqual(['human', 'agent', 'tool', 'tool', 'system']);
    await waitFor(() => expect(qa(r, '.msg-author').some((e) => e.textContent === 'Backend #1')).toBe(true), r);
    expect(qa(r, '[data-testid="message"] span').some((e) => e.textContent === 'DECISION')).toBe(true);
    expect(messagesOf(r)[1]!.textContent).toContain('untrusted');
    const cards = qa(r, '[data-testid="check-card"]');
    expect(cards.map((c) => [c.getAttribute('data-tool'), c.getAttribute('data-status')])).toEqual([
      ['Tests', 'pass'],
      ['Lint', 'fail'],
    ]);
    expect(cards[1]!.textContent).toContain('calc.go:3: unused variable');
    expect(q(r, 'script')).toBeNull();
    expect(r.container.textContent).toContain('<script>window.__xss=1</script>');
    expect(q(r, '.msg-error')).not.toBeNull();
    expect(q(r, '[role="log"][aria-live="polite"]')!.getAttribute('aria-label')).toBe('Messages in task-1');
    // the task channel links its task
    expect(byText<HTMLAnchorElement>(r.container, 'a', 'Task details').getAttribute('href')).toBe('/task/t1');
  });

  it('posts through room.post and shows the core error if refused', async () => {
    const posts: unknown[][] = [];
    const s = servicesWith({
      roomPost: async (...a: unknown[]) => {
        posts.push(a);
        throw new Error('blocked by secret scan');
      },
    });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Message to #general'), 'key=sk-123'));
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Send').click());
    await waitFor(() => expect(q(r, '[role="alert"]')!.textContent).toContain('blocked by secret scan'), r);
    expect(posts).toEqual([['r', 'key=sk-123', undefined]]);
  });

  it('disables the composer for an archived room, shows the badge and hides the agents bar', async () => {
    const r = mount(<Conversation room={{ ...taskRoom, archived: true }} />, servicesWith({}));
    expect(byLabel<HTMLTextAreaElement>(r.container, 'Message to #task-1').disabled).toBe(true);
    expect(qa(r, 'header span').some((e) => e.textContent === 'archived')).toBe(true);
    expect(q(r, '[data-testid="room-agents"]')).toBeNull();
  });

  it('groups consecutive messages, keeps the author name for continuations, and divides days', async () => {
    const messages = [
      msg({ seq: 1, created_at: '2026-01-01T10:00:00Z', body: 'one' }),
      msg({ seq: 2, created_at: '2026-01-01T10:01:00Z', body: 'two' }),
      msg({ seq: 3, created_at: '2026-01-02T10:01:00Z', body: 'three' }),
    ];
    const s = servicesWith({ roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after), roomMembers: async () => [{ roomId: 'r', member: { kind: 'human', id: 'kyle', display: 'Kyle' }, state: 'active', addedAt: '', lastReadSeq: 0 }] });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(3), r);
    const [a, b, c] = messagesOf(r);
    expect(a!.getAttribute('data-head')).toBe('true');
    expect(b!.getAttribute('data-head')).toBeNull();
    expect(b!.querySelector('.sr-only')!.textContent).toBe('kyle');
    expect(c!.getAttribute('data-head')).toBe('true');
    expect(qa(r, '[role="separator"]')).toHaveLength(2);
    // members popover
    await waitFor(() => expect(q(r, 'details.members summary')!.getAttribute('aria-label')).toBe('Members (1)'), r);
    expect(q(r, 'details.members')!.textContent).toContain('Kyle');
  });

  it('marks the room read up to the newest message, and extends when a message is posted', async () => {
    let messages = [msg({ seq: 1 }), msg({ seq: 2, body: 'second' })];
    const reads: unknown[][] = [];
    const s = servicesWith({
      roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after),
      roomMarkRead: async (...a: unknown[]) => (reads.push(a), true),
    });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(2), r);
    await waitFor(() => expect(reads).toEqual([['r', 2]]), r);
    messages = [...messages, msg({ seq: 3, author: { kind: 'agent', id: 'agt1' }, body: 'an answer' })];
    s.emit({ type: 'room.message_posted', data: { room: 'other' } });
    s.emit({ type: 'room.message_posted', data: { room: 'r' } });
    await waitFor(() => expect(messagesOf(r)).toHaveLength(3), r);
    expect(messagesOf(r)[2]!.textContent).toContain('an answer');
    await waitFor(() => expect(reads).toEqual([['r', 2], ['r', 3]]), r);
  });

  it('opens a thread from the hover toolbar and shows the reply count under the root', async () => {
    const messages = [msg({ seq: 1, body: 'who owns the parser?' }), msg({ seq: 2, thread_root_id: 'm1', author: { kind: 'agent', id: 'agt1' }, body: 'me' })];
    const s = servicesWith({ roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after) });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(2), r);
    const root = messagesOf(r)[0]!;
    const replies = Array.from(root.querySelectorAll('button')).find((b) => /1 reply/.test(b.getAttribute('aria-label') ?? ''))!;
    expect(replies.textContent).toContain('1 reply');
    const open = Array.from(root.querySelectorAll('button')).filter((b) => /Open thread/.test(b.getAttribute('aria-label') ?? ''));
    expect(open).toHaveLength(1);
    await r.act(() => open[0]!.click());
    expect(s.ui.thread).toEqual({ roomId: 'r', rootId: 'm1' });
    expect(s.ui.tab).toBe('thread');
    // the reply itself says it is in a thread and has no thread button of its own
    const reply = messagesOf(r)[1]!;
    expect(reply.textContent).toContain('reply in thread');
    expect(Array.from(reply.querySelectorAll('button')).some((b) => /Open thread/.test(b.getAttribute('aria-label') ?? ''))).toBe(false);
  });

  it('highlights a message that mentions a human', async () => {
    const messages = [msg({ seq: 1, author: { kind: 'agent', id: 'agt1' }, body: '@kyle look', mentions: [{ kind: 'human', id: 'kyle', span: [0, 5] }] }), msg({ seq: 2, author: { kind: 'agent', id: 'agt1' }, body: 'plain', created_at: '2026-01-01T01:00:00Z' })];
    const s = servicesWith({ roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after) });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(2), r);
    const [a, b] = messagesOf(r);
    expect(a!.getAttribute('data-mention')).toBe('true');
    expect(b!.getAttribute('data-mention')).toBeNull();
  });

  it('shows a "new messages" line after the read cursor when the room is opened', async () => {
    const messages = [msg({ seq: 1 }), msg({ seq: 2, author: { kind: 'agent', id: 'agt1' }, body: 'news' })];
    const s = servicesWith({
      roomMessages: async (_r: string, after = 0) => messages.filter((m) => m.seq > after),
      roomUnread: async () => [{ roomId: 'r', roomName: 'general', kind: 'channel', unread: 1, mentions: 0, muted: false, lastReadSeq: 1 }],
    });
    await waitFor(() => expect(s.project.unread.data).toHaveLength(1));
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(q(r, '[role="separator"][aria-label="New messages"]')).not.toBeNull(), r);
  });

  it('says so when a room has no messages, and when they cannot be read', async () => {
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, servicesWith({}));
    await waitFor(() => expect(r.container.textContent).toContain('No messages yet.'), r);
    expect(r.container.textContent).toContain('Welcome to #general');
    const bad = mount(
      <Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />,
      servicesWith({
        roomMessages: async () => {
          throw new Error('room store is locked');
        },
      }),
    );
    await waitFor(() => expect(q(bad, '[role="alert"]')!.textContent).toContain('room store is locked'), bad);
  });
});

describe('RoomScreen', () => {
  it('opens a room from the project list', async () => {
    const s = servicesWith({}, [{ ...taskRoom, kind: 'channel', name: 'general' }]);
    const r = mount(<RoomScreen id="r" />, s);
    await waitFor(() => byLabel(r.container, 'Message to #general'), r);
    expect(q(r, 'h1')!.textContent).toContain('general');
  });

  it('falls back to room.get so an archived task channel still opens', async () => {
    const gets: string[] = [];
    const s = servicesWith({ roomGet: async (id: string) => (gets.push(id), { ...taskRoom, archived: true }) }, []);
    const r = mount(<RoomScreen id="r" />, s);
    await waitFor(() => expect(byLabel<HTMLTextAreaElement>(r.container, 'Message to #task-1').disabled).toBe(true), r);
    expect(gets).toEqual(['r']);
  });

  it('says "Channel not found" with the core error when there is no such room', async () => {
    const s = servicesWith({
      roomGet: async () => {
        throw new Error('room not found: zzz');
      },
    });
    const r = mount(<RoomScreen id="zzz" />, s);
    await waitFor(() => expect(r.container.textContent).toContain('Channel not found'), r);
    await waitFor(() => expect(q(r, '[role="alert"]')!.textContent).toContain('room not found: zzz'), r);
  });
});

import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { Agent, Message, Room } from '../../api/types.ts';
import { roomMessagesDouble } from '../../test/fakes.ts';
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
    ...roomMessagesDouble(() => []),
    ...client,
  });
  s.workspace.start();
  s.project.start();
  return s;
}

const messagesOf = (r: { container: HTMLElement }) => qa(r, '[data-testid="message"]');

/** The test page is not a focused window; a room marks read only in one. Pretend it is (undone after each test). */
function focusWindow(): void {
  Object.defineProperty(document, 'hasFocus', { configurable: true, value: () => true });
}
afterEach(() => {
  delete (document as unknown as { hasFocus?: unknown }).hasFocus;
});

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
    const s = servicesWith({ ...roomMessagesDouble(() => messages) });
    const r = mount(<Conversation room={taskRoom} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(5), r);
    expect(messagesOf(r).map((m) => m.getAttribute('data-author-kind'))).toEqual(['human', 'agent', 'tool', 'tool', 'system']);
    await waitFor(() => expect(qa(r, '.msg-author').some((e) => e.textContent === 'Backend #1')).toBe(true), r);
    // one author line: the name, then the agent's role (or the author kind) as a quiet label; people get none
    expect(messagesOf(r).map((m) => m.querySelector('.msg-role')?.textContent ?? null)).toEqual([null, 'backend', 'tool', 'tool', 'system']);
    expect(messagesOf(r)[1]!.querySelector('[data-kind="agent"]')).not.toBeNull();
    expect(messagesOf(r)[0]!.querySelector('[data-kind="human"]')).not.toBeNull();
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
    const s = servicesWith({ ...roomMessagesDouble(() => messages), roomMembers: async () => [{ roomId: 'r', member: { kind: 'human', id: 'kyle', display: 'Kyle' }, state: 'active', addedAt: '', lastReadSeq: 0 }] });
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
      ...roomMessagesDouble(() => messages),
      roomMarkRead: async (...a: unknown[]) => (reads.push(a), true),
    });
    focusWindow();
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

  it('marks read only while the window is focused and the log is at the bottom (F20)', async () => {
    let messages = Array.from({ length: 40 }, (_, i) => msg({ seq: i + 1, body: `line ${i + 1}` }));
    const reads: unknown[][] = [];
    const s = servicesWith({
      ...roomMessagesDouble(() => messages),
      roomMarkRead: async (...a: unknown[]) => (reads.push(a), true),
    });
    let focused = false;
    Object.defineProperty(document, 'hasFocus', { configurable: true, value: () => focused });
    {
      const r = mount(
        <div style={{ height: '320px', display: 'flex', flexDirection: 'column' }}>
          <Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />
        </div>,
        s,
      );
      // the room marks read (or not) in the same step that shows the messages: once the newest is shown, it has decided
      await waitFor(() => expect(messagesOf(r).at(-1)!.textContent).toContain('line 40'), r);
      expect(reads).toEqual([]); // the window is in the background: nothing is marked read
      focused = true;
      await r.act(() => window.dispatchEvent(new Event('focus')));
      await waitFor(() => expect(reads).toEqual([['r', 40]]), r);
      // scrolled up to read history: a new message is not marked read until the log is back at the bottom
      const log = q<HTMLDivElement>(r, '[role="log"]')!;
      expect(log.scrollHeight > log.clientHeight).toBe(true);
      await r.act(() => {
        log.scrollTop = 0;
        log.dispatchEvent(new Event('scroll'));
      });
      messages = [...messages, msg({ seq: 41, author: { kind: 'agent', id: 'agt1' }, body: 'news' })];
      s.emit({ type: 'room.message_posted', data: { room: 'r' } });
      // the count on the jump button comes from the same step that would have marked it read
      await waitFor(() => expect(byText(r.container, 'button', '1 new message')).not.toBeNull(), r);
      expect(reads).toEqual([['r', 40]]);
      // away from the bottom the new row is not rendered: the count is said in a status instead
      expect(q(r, '[data-testid="room-unseen"]')!.textContent).toBe('1 new message below');
      await r.act(() => {
        log.scrollTop = log.scrollHeight;
        log.dispatchEvent(new Event('scroll'));
      });
      await waitFor(() => expect(reads).toEqual([['r', 40], ['r', 41]]), r);
      await waitFor(() => expect(messagesOf(r).at(-1)!.textContent).toContain('news'), r);
      expect(q(r, '[data-testid="room-unseen"]')!.textContent).toBe('');
    }
  });

  it('opens on the newest page and loads older messages on demand (F6)', async () => {
    const messages = Array.from({ length: 150 }, (_, i) => msg({ seq: i + 1, body: `line ${i + 1}`, created_at: '2026-01-01T10:00:00Z' }));
    const reads = roomMessagesDouble(() => messages); // TEST DOUBLE: room.messages as the core answers it
    const s = servicesWith({ ...reads });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(100), r);
    expect(messagesOf(r)[0]!.textContent).toContain('line 51');
    expect(messagesOf(r)[99]!.textContent).toContain('line 150');
    expect(r.container.textContent).not.toContain('This is the start of the general channel.');
    // the live region did not announce the first page; it announces what comes after
    const log = q(r, '[role="log"]')!;
    await waitFor(() => expect(log.getAttribute('aria-live')).toBe('polite'), r);
    await r.act(() => byText(r.container, 'button', 'Load older messages').click());
    await waitFor(() => expect(messagesOf(r)).toHaveLength(150), r);
    expect(messagesOf(r)[0]!.textContent).toContain('line 1');
    expect(r.container.textContent).toContain('This is the start of the general channel.');
    // the newest page in one read, then the page before the oldest shown (one more than a page asks whether older exist)
    expect(reads.calls).toEqual([
      ['latest', 101],
      ['before', 51, 101],
    ]);
  });

  it('windows a long history: few rows in the DOM, at the bottom, and every older page keeps the view (F6)', async () => {
    const start = Date.parse('2026-01-01T08:00:00Z');
    // 2,000 messages of different heights: two authors, a long one every seventh, a new day every 600
    const messages = Array.from({ length: 2000 }, (_, i) =>
      msg({
        seq: i + 1,
        author: i % 5 === 0 ? { kind: 'agent', id: 'agt1' } : { kind: 'human', id: 'kyle' },
        body: i % 7 === 0 ? `long ${i + 1}\n\n${'a paragraph that wraps over several lines '.repeat(12)}` : `line ${i + 1}`,
        created_at: new Date(start + i * 144_000).toISOString(),
      }),
    );
    const reads = roomMessagesDouble(() => messages); // TEST DOUBLE: room.messages as the core answers it
    let release: (() => void) | undefined;
    const s = servicesWith({
      ...reads,
      // an older page lands only when the test lets it, so the view can be measured before and after
      roomMessagesBefore: async (...a: Parameters<typeof reads.roomMessagesBefore>) => {
        await new Promise<void>((res) => (release = res));
        return reads.roomMessagesBefore(...a);
      },
    });
    const r = mount(
      <div style={{ height: '600px', display: 'flex', flexDirection: 'column' }}>
        <Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />
      </div>,
      s,
    );
    const log = q<HTMLDivElement>(r, '[role="log"]')!;
    const rows = () => qa(r, '[data-vkey]');
    const atBottom = () => log.scrollHeight - log.scrollTop - log.clientHeight < 1;
    const topOf = (key: string) => q(r, `[data-vkey="${key}"]`)!.getBoundingClientRect().top - log.getBoundingClientRect().top;
    /** At most four views of rows (the view, an overscan on each side, the slack before a re-plan) at 20 px or more. */
    const bound = () => Math.ceil((4 * log.clientHeight + 600) / 20);
    /** Scroll as a reader does: the browser's own scroll event reaches the log. */
    const scrollTo = async (top: number) => {
      const before = log.scrollTop;
      const moved = new Promise((res) => log.addEventListener('scroll', res, { once: true }));
      log.scrollTop = top;
      if (log.scrollTop !== before) await moved;
      r.root.flush();
    };
    await waitFor(() => expect(messagesOf(r).at(-1)!.textContent).toContain('line 2000'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    expect(rows().length).toBeLessThanOrEqual(bound());
    let most = rows().length;
    // read the whole history the way a reader does: scroll to the top, an older page is asked for, it lands above
    for (let page = 1; page < 20; page++) {
      await scrollTo(0);
      // the scroll asked for the older page at once (the double holds it until released)
      expect(release).toBeDefined();
      // the row at the top of the view before the page lands
      const viewTop = log.getBoundingClientRect().top;
      const key = rows().find((e) => e.getBoundingClientRect().bottom > viewTop && e.querySelector('[data-testid="message"]'))!.getAttribute('data-vkey')!;
      const at = topOf(key);
      const go = release!;
      release = undefined;
      go();
      // once the page is in, the same row is where it was: the page went above it, the view did not jump
      await waitFor(() => {
        expect(q(r, '[aria-busy="true"]')).toBeNull();
        expect(Math.abs(topOf(key) - at)).toBeLessThan(1);
      }, r);
      expect(log.scrollTop).toBeGreaterThan(100 * 20);
      most = Math.max(most, rows().length);
    }
    expect(reads.calls.filter((c) => c[0] === 'before')).toHaveLength(19);
    expect(r.container.textContent).toContain('This is the start of the general channel.');
    // 2,000 messages and their day dividers loaded; only a window of them was ever in the DOM
    expect(most).toBeLessThanOrEqual(bound());
    expect(most).toBeLessThan(100);
    // back to the newest, and a new message keeps the log at the bottom
    await r.act(() => byText(r.container, 'button', 'Jump to the latest').click());
    await waitFor(() => expect(messagesOf(r).at(-1)!.textContent).toContain('line 2000'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    messages.push(msg({ seq: 2001, author: { kind: 'agent', id: 'agt1' }, body: 'the newest', created_at: new Date(start + 2000 * 144_000).toISOString() }));
    s.emit({ type: 'room.message_posted', data: { room: 'r' } });
    await waitFor(() => expect(messagesOf(r).at(-1)!.textContent).toContain('the newest'), r);
    await waitFor(() => expect(atBottom()).toBe(true), r);
    expect(rows().length).toBeLessThanOrEqual(bound());
  });

  it('keeps the live region quiet while the history loads', async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((res) => (release = res));
    const s = servicesWith({ roomMessagesLatest: async () => (await gate, [msg({ seq: 1 })]) });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(q(r, '[role="log"]')!.getAttribute('aria-live')).toBe('off'), r);
    expect(q(r, '[role="log"]')!.getAttribute('aria-busy')).toBe('true');
    release();
    await waitFor(() => expect(messagesOf(r)).toHaveLength(1), r);
    await waitFor(() => expect(q(r, '[role="log"]')!.getAttribute('aria-live')).toBe('polite'), r);
    expect(q(r, '[role="log"]')!.getAttribute('aria-busy')).toBeNull();
  });

  it('opens a thread from the hover toolbar and shows the reply count under the root', async () => {
    const messages = [msg({ seq: 1, body: 'who owns the parser?' }), msg({ seq: 2, thread_root_id: 'm1', author: { kind: 'agent', id: 'agt1' }, body: 'me' })];
    const s = servicesWith({ ...roomMessagesDouble(() => messages) });
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
    const s = servicesWith({ ...roomMessagesDouble(() => messages) });
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(messagesOf(r)).toHaveLength(2), r);
    const [a, b] = messagesOf(r);
    expect(a!.getAttribute('data-mention')).toBe('true');
    expect(b!.getAttribute('data-mention')).toBeNull();
  });

  it('shows a "new messages" line after the read cursor when the room is opened', async () => {
    const messages = [msg({ seq: 1 }), msg({ seq: 2, author: { kind: 'agent', id: 'agt1' }, body: 'news' })];
    const s = servicesWith({
      ...roomMessagesDouble(() => messages),
      roomUnread: async () => [{ roomId: 'r', roomName: 'general', kind: 'channel', unread: 1, mentions: 0, muted: false, lastReadSeq: 1 }],
    });
    await waitFor(() => expect(s.project.unread.data).toHaveLength(1));
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, s);
    await waitFor(() => expect(q(r, '[role="separator"][aria-label="New messages"]')).not.toBeNull(), r);
  });

  it('says so when a room has no messages, and when they cannot be read', async () => {
    const r = mount(<Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />, servicesWith({}));
    await waitFor(() => expect(r.container.textContent).toContain('No messages yet.'), r);
    expect(r.container.textContent).toContain('This is the start of the general channel.');
    const bad = mount(
      <Conversation room={{ ...taskRoom, kind: 'channel', name: 'general' }} />,
      servicesWith({
        roomMessagesLatest: async () => {
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

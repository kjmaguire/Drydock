import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Message } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ThreadPanel } from './thread-panel.tsx';

afterEach(cleanup);

const root: Message = { id: 'root', room_id: 'r', seq: 1, type: 'CHAT', author: { kind: 'human', id: 'kyle' }, body: 'question <b>x</b>', trust: 'trusted', created_at: '2026-01-01T00:00:00Z' };

describe('ThreadPanel', () => {
  it('a thread reply is posted with the root id and the thread reloads', async () => {
    let reads = 0;
    const posts: unknown[][] = [];
    const s = testServices({
      roomThread: async () => (reads++, [root]),
      roomPost: async (...a: unknown[]) => (posts.push(a), {}),
    });
    const r = mount(<ThreadPanel roomId="r" rootId="root" onClose={() => undefined} />, s);
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="thread-message"]')).toHaveLength(1), r);
    expect(r.container.querySelector('[data-testid="thread"] b')).toBeNull(); // inert text
    expect(r.container.textContent).toContain('question <b>x</b>');
    const send = byText<HTMLButtonElement>(r.container, 'button', 'Reply');
    expect(send.disabled).toBe(true);
    const box = byLabel<HTMLTextAreaElement>(r.container, 'Reply in thread');
    await r.act(() => type(box, 'an answer'));
    await r.act(() => send.click());
    await waitFor(() => expect(posts).toEqual([['r', 'an answer', 'root']]), r);
    await waitFor(() => expect(reads).toBe(2), r);
    expect(box.value).toBe('');
  });

  it('shows the replies under the root, re-reads on a message in this room, and closes', async () => {
    let msgs: Message[] = [root];
    let closed = 0;
    const s = testServices({ roomThread: async () => msgs });
    const r = mount(<ThreadPanel roomId="r" rootId="root" onClose={() => closed++} />, s);
    await waitFor(() => expect(r.container.textContent).toContain('No replies yet'), r);
    msgs = [root, { ...root, id: 'm2', seq: 2, thread_root_id: 'root', author: { kind: 'agent', id: 'a1' }, body: 'the backend agent', trust: 'untrusted' }];
    s.emit({ type: 'room.message_posted', data: { room: 'r' } });
    await waitFor(() => expect(r.container.querySelectorAll('[data-testid="thread-message"]')).toHaveLength(2), r);
    expect(r.container.textContent).toContain('1 reply');
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Close').click());
    expect(closed).toBe(1);
  });

  it('shows the core refusal of a reply and keeps the text', async () => {
    const s = testServices({
      roomThread: async () => [root],
      roomPost: async () => {
        throw new Error('blocked by secret scan');
      },
    });
    const r = mount(<ThreadPanel roomId="r" rootId="root" onClose={() => undefined} />, s);
    const box = byLabel<HTMLTextAreaElement>(r.container, 'Reply in thread');
    await r.act(() => type(box, 'key=sk-1'));
    await r.act(() => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true })));
    await waitFor(() => expect(r.container.querySelector('[role="alert"]')!.textContent).toContain('blocked by secret scan'), r);
    expect(box.value).toBe('key=sk-1');
  });
});

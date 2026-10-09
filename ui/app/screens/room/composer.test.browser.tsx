import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Agent, Message, Room, RoomMember } from '../../api/types.ts';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { Attachments } from './attachments.tsx';
import { Composer } from './composer.tsx';

afterEach(cleanup);

const room: Room = { id: 'room_1', projectId: 'prj_1', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1 };
const agents = [
  { id: 'agt_1', name: 'Backend One', role: 'backend' },
  { id: 'agt_2', name: 'Reviewer #1', role: 'reviewer' },
] as Agent[];
const members: RoomMember[] = [
  { roomId: 'room_1', member: { kind: 'human', id: 'kyle', display: 'Kyle' }, state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'agent', id: 'agt_1' }, role: 'backend', state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'agent', id: 'agt_2' }, role: 'reviewer', state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'tool', id: 'tests' }, state: 'active', addedAt: '', lastReadSeq: 0 },
];

const q = <T extends Element = HTMLElement>(r: { container: HTMLElement }, sel: string) => r.container.querySelector<T>(sel);
const qa = (r: { container: HTMLElement }, sel: string) => Array.from(r.container.querySelectorAll<HTMLElement>(sel));
const key = (el: Element, k: string, mods: KeyboardEventInit = {}) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...mods }));

function setup(client: Parameters<typeof testServices>[0], extra: { onNavigate?: (href: string) => void; disabled?: boolean } = {}) {
  const s = testServices(client);
  const r = mount(
    <Composer
      room={room}
      agents={agents}
      members={members}
      rooms={[room, { ...room, id: 'room_2', name: 'task-3-subtract', kind: 'task' }]}
      disabled={!!extra.disabled}
      onSent={() => {}}
      onNavigate={extra.onNavigate}
    />,
    s,
  );
  const box = byLabel<HTMLTextAreaElement>(r.container, 'Message to #general');
  return { r, s, box };
}

describe('the composer', () => {
  it('offers mentions as you type and inserts the chosen one with the keyboard', async () => {
    const posts: unknown[][] = [];
    const { r, box } = setup({ roomPost: async (...a: unknown[]) => (posts.push(a), {}) });
    await r.act(() => type(box, 'ask @rev'));
    const lb = r.$('[role="listbox"]')!;
    expect(lb.getAttribute('aria-label')).toBe('Suggestions');
    expect(qa(r, '[role="option"]')[0]!.textContent).toContain('Reviewer #1');
    expect(box.getAttribute('aria-expanded')).toBe('true');
    await r.act(() => key(box, 'Enter'));
    expect(box.value).toBe('ask @agt_2 ');
    expect(r.$('[role="listbox"]')).toBeNull();
    await r.act(() => type(box, 'ask @agt_2 to look'));
    await r.act(() => key(box, 'Enter', { ctrlKey: true }));
    await waitFor(() => expect(posts).toEqual([['room_1', 'ask @agt_2 to look', undefined]]), r);
    await waitFor(() => expect(box.value).toBe(''), r);
  });

  it('moves through suggestions with the arrows, picks with the mouse, and Escape closes them', async () => {
    const { r, box } = setup({});
    await r.act(() => type(box, '#'));
    expect(qa(r, '[role="option"]').map((o) => o.querySelector('strong')!.textContent)).toEqual(['#general', '#task-3-subtract']);
    await r.act(() => key(box, 'ArrowDown'));
    expect(qa(r, '[role="option"]')[1]!.getAttribute('aria-selected')).toBe('true');
    await r.act(() => key(box, 'Escape'));
    expect(r.$('[role="listbox"]')).toBeNull();
    await r.act(() => type(box, 'see /'));
    expect(r.$('[role="listbox"]')).toBeNull(); // a slash command only at the start
    await r.act(() => type(box, '@'));
    const opt = qa(r, '[role="option"]').find((o) => o.textContent?.includes('Backend One'))!;
    await r.act(() => opt.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })));
    expect(box.value).toBe('@backend-one ');
  });

  it('runs slash commands: markers, task creation, invite, help; unknown commands are refused', async () => {
    const posts: unknown[][] = [];
    const tasks: unknown[] = [];
    const invites: unknown[][] = [];
    const went: string[] = [];
    const { r, box } = setup(
      {
        roomPost: async (...a: unknown[]) => (posts.push(a), {}),
        taskCreate: async (p: unknown) => (tasks.push(p), { id: 'task_9' }) as never,
        roomInvite: async (...a: unknown[]) => (invites.push(a), []),
      },
      { onNavigate: (h) => went.push(h) },
    );
    const send = async (v: string) => {
      await r.act(() => type(box, v));
      await r.act(() => key(box, 'Escape'));
      await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Send').click());
    };
    await send('/decision Keep SQLite');
    await waitFor(() => expect(posts).toEqual([['room_1', '[DECISION] Keep SQLite', undefined]]), r);
    await send('/invite @backend-one');
    await waitFor(() => expect(invites).toEqual([['room_1', 'agt_1']]), r);
    await waitFor(() => expect(r.$('[role="status"]')!.textContent).toBe('Backend One was added to the room.'), r);
    await send('/help');
    await waitFor(() => expect(r.$('[role="region"][aria-label="Commands"]')).not.toBeNull(), r);
    await send('/rm everything');
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('is not a command'), r);
    await send('/task Add Multiply');
    await waitFor(() => expect(tasks).toEqual([{ projectId: 'prj_1', title: 'Add Multiply' }]), r);
    await waitFor(() => expect(went).toEqual(['/task/task_9']), r);
  });

  it('attaches files to the message and sends them together; a file cannot go with a command', async () => {
    const attached: unknown[][] = [];
    const { r, box } = setup({ roomAttach: async (...a: unknown[]) => (attached.push(a), {}), roomPost: async () => ({}) });
    const input = q<HTMLInputElement>(r, '[data-testid="composer-file"]')!;
    const dt = new DataTransfer();
    dt.items.add(new File(['hello'], 'notes.txt', { type: 'text/plain' }));
    input.files = dt.files;
    await r.act(() => input.dispatchEvent(new Event('change', { bubbles: true })));
    await waitFor(() => expect(r.$('[aria-label="Files to attach"]')!.textContent).toContain('notes.txt'), r);
    await r.act(() => type(box, '/invite @backend-one'));
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Send').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('Files go with a message, not with a command.'), r);
    await r.act(() => type(box, 'see notes'));
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Send').click());
    await waitFor(() => expect(attached).toHaveLength(1), r);
    const [rid, body, files] = attached[0] as [string, string, { name: string; data: Uint8Array }[]];
    expect([rid, body, files[0]!.name, new TextDecoder().decode(files[0]!.data)]).toEqual(['room_1', 'see notes', 'notes.txt', 'hello']);
    await waitFor(() => expect(r.$('[aria-label="Files to attach"]')).toBeNull(), r);
  });

  it('refuses a fifth file', async () => {
    const { r } = setup({});
    const input = q<HTMLInputElement>(r, '[data-testid="composer-file"]')!;
    const dt = new DataTransfer();
    for (let i = 0; i < 5; i++) dt.items.add(new File(['x'], `f${i}.txt`));
    input.files = dt.files;
    await r.act(() => input.dispatchEvent(new Event('change', { bubbles: true })));
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('At most 4 files go with one message.'), r);
  });

  it('is disabled for an archived room', () => {
    const { r, box } = setup({}, { disabled: true });
    expect(box.disabled).toBe(true);
    expect(box.placeholder).toBe('This channel is archived');
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Send').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Attach').disabled).toBe(true);
  });
});

describe('attachments on a message', () => {
  it('lists the files and saves one through the core', async () => {
    const asked: string[] = [];
    const s = testServices({
      roomAttachment: async (id: string) => (asked.push(id), { id: 'att_1', name: 'notes.txt', mime: 'text/plain', size: 5, data: new TextEncoder().encode('hello') }),
    });
    const m = {
      id: 'msg_1',
      payload: {
        attachments: [
          { id: 'att_1', name: 'notes.txt', mime: 'text/plain', size: 5 },
          { id: 'att_2', name: 'logo.png', mime: 'image/png', size: 2048 },
        ],
      },
    } as unknown as Message;
    const r = mount(<Attachments m={m} />, s);
    expect(qa(r, '[data-testid="attachment"]')).toHaveLength(2);
    expect(qa(r, 'button').filter((b) => b.textContent?.trim() === 'Show image')).toHaveLength(1);
    await r.act(() => qa(r, 'button').find((b) => b.textContent?.trim() === 'Save')!.click());
    const link = await waitFor(() => byText<HTMLAnchorElement>(r.container, 'a', 'Download notes.txt'), r);
    expect(link.getAttribute('download')).toBe('notes.txt');
    expect(link.href.startsWith('blob:')).toBe(true);
    expect(asked).toEqual(['att_1']);
  });

  it('previews a raster image as a data: URL and shows a read error', async () => {
    let fail = true;
    const s = testServices({
      roomAttachment: async () => {
        if (fail) throw new Error('attachment is gone');
        return { id: 'att_2', name: 'logo.png', mime: 'image/png', size: 3, data: new Uint8Array([1, 2, 3]) };
      },
    });
    const m = { id: 'msg_1', payload: { attachments: [{ id: 'att_2', name: 'logo.png', mime: 'image/png', size: 3 }] } } as unknown as Message;
    const r = mount(<Attachments m={m} />, s);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Show image').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('attachment is gone'), r);
    fail = false;
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Show image').click());
    const img = await waitFor(() => q<HTMLImageElement>(r, 'img')!, r);
    expect(img.getAttribute('src')!.startsWith('data:image/png;base64,')).toBe(true);
    expect(img.alt).toBe('logo.png');
  });
});

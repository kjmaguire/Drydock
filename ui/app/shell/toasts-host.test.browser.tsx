import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { NeedsYouItem, Project } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { Toasts } from './toasts-host.tsx';

afterEach(cleanup);

const item = (key: string, extra: Partial<NeedsYouItem> = {}): NeedsYouItem =>
  ({ key, kind: 'question', severity: 0, title: 'Which database should I use?', projectId: 'p1', createdAt: '2026-11-03T10:00:00Z', ...extra }) as NeedsYouItem;

function setup(here: { inbox: boolean; taskId?: string; chatId?: string }, lifeMs = 60_000) {
  let needs: NeedsYouItem[] = [item('a')];
  const s = testServices({
    projectList: async () => [{ id: 'p1', name: 'gocalc' } as Project],
    needsYouList: async () => needs,
  });
  s.workspace.start();
  const went: string[] = [];
  const r = mount(<Toasts here={here} lifeMs={lifeMs} onNavigate={(h) => went.push(h)} />, s);
  const texts = () => Array.from(r.container.querySelectorAll('[data-testid="toast-text"]')).map((n) => n.textContent);
  return { s, r, went, texts, setNeeds: (n: NeedsYouItem[]) => (needs = n) };
}

describe('Toasts (G2)', () => {
  it('a new Inbox item elsewhere shows one toast with its kind and project, and Open goes where it is answered', async () => {
    const t = setup({ inbox: false });
    await waitFor(() => expect(t.s.workspace.needs.data?.length).toBe(1), t.r);
    expect(t.texts()).toEqual([]); // what waited before is the count, not news
    t.setNeeds([item('a'), item('b', { roomId: 'r1', messageId: 'm1' })]);
    t.s.emit({ type: 'room.message_posted', projectId: 'p1' });
    await waitFor(() => expect(t.texts()).toEqual(['Needs you: question']), t.r);
    const toast = t.r.container.querySelector('[data-testid="toast"]')!;
    expect(toast.textContent).toContain('gocalc');
    expect(toast.textContent).not.toContain('Which database');
    (toast.querySelector('[data-testid="toast-action"]') as HTMLButtonElement).click();
    expect(t.went.length).toBe(1);
    await waitFor(() => expect(t.texts()).toEqual([]), t.r);
  });

  it('on the Inbox a new item shows no toast', async () => {
    const t = setup({ inbox: true });
    await waitFor(() => expect(t.s.workspace.needs.data?.length).toBe(1), t.r);
    t.setNeeds([item('a'), item('b')]);
    t.s.emit({ type: 'room.message_posted', projectId: 'p1' });
    await waitFor(() => expect(t.s.workspace.needs.data?.length).toBe(2), t.r);
    expect(t.texts()).toEqual([]);
  });

  it('a task that ended elsewhere, not the one open; at most three, the oldest leaves', async () => {
    const t = setup({ inbox: false, taskId: 'here' });
    t.s.emit({ type: 'task.completed', taskId: 'here' });
    t.r.root.flush();
    expect(t.texts()).toEqual([]);
    t.s.emit({ type: 'task.completed', taskId: 't1' });
    t.s.emit({ type: 'task.failed', taskId: 't2' });
    t.s.emit({ type: 'task.cancelled', taskId: 't3' });
    t.s.emit({ type: 'chat.turn', data: { chatId: 'c1', status: 'done' } });
    await waitFor(() => expect(t.texts()).toEqual(['Task failed', 'Task cancelled', 'A chat answered']), t.r);
  });

  it('a toast leaves by itself; a warning a page left for the next stays until closed', async () => {
    const t = setup({ inbox: false }, 30);
    t.s.ui.flash('gocalc was deleted.');
    t.s.ui.flash('gocalc: the GitHub repository could not be made.', 'warn');
    await waitFor(() => expect(t.texts()).toEqual(['gocalc was deleted.', 'gocalc: the GitHub repository could not be made.']), t.r);
    await waitFor(() => expect(t.texts()).toEqual(['gocalc: the GitHub repository could not be made.']), t.r);
    (t.r.container.querySelector('[data-testid="toast"] button[aria-label="Close"]') as HTMLButtonElement).click();
    await waitFor(() => expect(t.texts()).toEqual([]), t.r);
  });
});

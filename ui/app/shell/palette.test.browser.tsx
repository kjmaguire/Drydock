import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, type, waitFor } from '../test/render.tsx';
import { CommandPalette } from './palette.tsx';

afterEach(cleanup);

function press(down: Element, clickTarget: Element) {
  down.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'mouse' }));
  clickTarget.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

// TEST DOUBLES: a partial client; the palette only reads the chats while it is open.
async function opened() {
  const s = testServices({ chatList: async () => [] });
  const r = mount(<CommandPalette onMessage={() => {}} />, s);
  s.ui.setPalette(true);
  const dlg = await waitFor(() => {
    const d = r.container.querySelector<HTMLDialogElement>('dialog[aria-label="Command palette"]')!;
    if (!d.open) throw new Error('not open yet');
    return d;
  }, r);
  return { s, r, dlg, input: dlg.querySelector('input')! };
}

describe('closing the command palette by its backdrop (L41)', () => {
  it('closes when the press and the click are both on the backdrop', async () => {
    const { s, r, dlg } = await opened();
    press(dlg, dlg);
    r.root.flush();
    expect(s.ui.paletteOpen).toBe(false);
  });

  it('stays open when a drag starts in the search box and ends on the backdrop', async () => {
    const { s, r, dlg, input } = await opened();
    press(input, dlg);
    r.root.flush();
    expect(s.ui.paletteOpen).toBe(true);
    expect(dlg.open).toBe(true);
    // a click on the list or the box never closes it
    press(input, input);
    expect(s.ui.paletteOpen).toBe(true);
    // and the next click on the backdrop closes it
    press(dlg, dlg);
    expect(s.ui.paletteOpen).toBe(false);
  });
});

describe('searching a task list that is cut (M21)', () => {
  // TEST DOUBLES: the project's shared reads; the palette searches the tasks the project model holds.
  async function search(total: number | undefined) {
    const t = { id: 't1', projectId: 'p1', seq: 1, title: 'Add Multiply', description: '', status: 'BACKLOG', priority: 0, iteration: 0, maxIterations: 3, costUsd: 0, created: 0, updated: 0, version: 1 };
    const s = testServices({
      chatList: async () => [],
      projectList: async () => [{ id: 'p1', name: 'gocalc' } as never],
      agentList: async () => [],
      roomList: async () => [],
      roomUnread: async () => [],
      needsYouList: async () => [],
      presenceList: async () => [],
      taskList: async () => [t],
      ...(total === undefined ? {} : { taskCount: async () => ({ total }) }),
    });
    s.workspace.start();
    s.project.start();
    const r = mount(<CommandPalette onMessage={() => {}} />, s);
    s.ui.setPalette(true);
    const dlg = await waitFor(() => {
      const d = r.container.querySelector<HTMLDialogElement>('dialog[aria-label="Command palette"]')!;
      if (!d.open) throw new Error('not open yet');
      return d;
    }, r);
    await waitFor(() => expect(s.project.taskList).toHaveLength(1), r);
    return { r, dlg, input: dlg.querySelector('input')! };
  }

  it('says which tasks were searched when nothing matches and the project has more', async () => {
    const { r, dlg, input } = await search(700);
    type(input, 'zzz-no-such-thing');
    await waitFor(() => expect(dlg.textContent).toContain('Nothing matches.'), r);
    expect(dlg.textContent).toContain('Only the newest 1 of 700 tasks are searched.');
  });

  it('says only "Nothing matches." while every task was searched', async () => {
    const { r, dlg, input } = await search(1);
    type(input, 'zzz-no-such-thing');
    await waitFor(() => expect(dlg.textContent).toContain('Nothing matches.'), r);
    expect(dlg.textContent).not.toContain('are searched');
  });
});

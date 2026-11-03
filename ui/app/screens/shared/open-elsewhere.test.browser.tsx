import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { OpenPathReq } from '../../api/shellClient.ts';
import { byText, cleanup, drain, mount, testServices, waitFor } from '../../test/render.tsx';
import { joinPath, OpenElsewhere } from './open-elsewhere.tsx';

afterEach(cleanup);

const openItems = async (r: ReturnType<typeof mount>, label: string) => {
  const trigger = await waitFor(() => byText(r.container, 'button', label), r);
  await r.act(() => trigger.click());
  await waitFor(() => expect(trigger.getAttribute('aria-expanded')).toBe('true'), r);
  return Array.from(document.querySelectorAll('[popover]:popover-open [role="menuitem"]')) as HTMLElement[];
};
/** shell.editors has answered and the menu is drawn with it. */
const settle = async (r: ReturnType<typeof mount>) => {
  await drain();
  r.root.flush();
};
const labels = (items: HTMLElement[]) => items.map((i) => i.querySelector('.label')!.textContent);

describe('More ways to open (X26)', () => {
  it('offers Explorer, the editors found and Copy path, and asks the core to open what the page names', async () => {
    const calls: OpenPathReq[] = [];
    const s = testServices({
      shellEditors: async () => ({ available: true, editors: [{ id: 'vscode', name: 'Visual Studio Code' }] }),
      shellOpenPath: async (p) => void calls.push(p),
    });
    const r = mount(<OpenElsewhere where={{ taskId: 't1', path: 'internal/calc.go' }} fullPath={'C:\\w\\internal\\calc.go'} label="More ways to open calc.go" />, s);
    await settle(r);
    let items = await openItems(r, 'More ways to open calc.go');
    expect(labels(items)).toEqual(['Reveal in Explorer', 'Open in Visual Studio Code', 'Copy path']);
    await r.act(() => items[0]!.click());
    await waitFor(() => expect(calls).toEqual([{ taskId: 't1', path: 'internal/calc.go', with: 'explorer' }]), r);
    // the menu once more, on a page of its own (a menu reopened under the runner's clicks stays shut)
    cleanup();
    const again = mount(<OpenElsewhere where={{ taskId: 't1', path: 'internal/calc.go' }} fullPath={'C:\\w\\internal\\calc.go'} label="More ways to open calc.go" />, s);
    await settle(again);
    items = await openItems(again, 'More ways to open calc.go');
    await again.act(() => items[1]!.click());
    await waitFor(() => expect(calls[1]).toEqual({ taskId: 't1', path: 'internal/calc.go', with: 'editor', editor: 'vscode' }), again);
  });

  it('copies the path and says so only once the clipboard took it; a refusal of the core is a warning', async () => {
    const copied: string[] = [];
    const real = Object.getOwnPropertyDescriptor(Navigator.prototype, 'clipboard');
    // TEST DOUBLE for the clipboard: the runner's page may not be allowed to write to the real one
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => void copied.push(t) } });
    try {
      const s = testServices({
        shellEditors: async () => ({ available: true, editors: [] }),
        shellOpenPath: async () => Promise.reject(new Error('the folder is not there any more')),
      });
      const r = mount(<OpenElsewhere where={{ projectId: 'p1' }} fullPath={'C:\\code\\gocalc'} />, s);
      await settle(r);
      let items = await openItems(r, 'More ways to open');
      expect(labels(items)).toEqual(['Open in Explorer', 'Copy path']);
      await r.act(() => items[1]!.click());
      await waitFor(() => expect(copied).toEqual(['C:\\code\\gocalc']), r);
      await waitFor(() => expect(s.ui.toasts.map((t) => t.text)).toEqual(['Path copied']), r);
      cleanup();
      const again = mount(<OpenElsewhere where={{ projectId: 'p1' }} fullPath={'C:\\code\\gocalc'} />, s);
      await settle(again);
      items = await openItems(again, 'More ways to open');
      await again.act(() => items[0]!.click());
      await waitFor(() => expect(s.ui.toasts.map((t) => [t.text, t.detail, t.tone])[1]).toEqual(['Could not open it', 'the folder is not there any more', 'warn']), again);
    } finally {
      delete (navigator as unknown as Record<string, unknown>).clipboard;
      if (real) Object.defineProperty(Navigator.prototype, 'clipboard', real);
    }
  });

  it('a window that cannot start another program offers Copy path alone, and nothing without a path', async () => {
    const r = mount(<OpenElsewhere where={{ taskId: 't1' }} fullPath="/w/t1" />, testServices({ shellEditors: async () => ({ available: false, editors: [] }) }));
    await settle(r);
    expect(labels(await openItems(r, 'More ways to open'))).toEqual(['Copy path']);
    cleanup();
    const none = mount(<OpenElsewhere where={{ taskId: 't1' }} />, testServices({ shellEditors: async () => ({ available: false, editors: [] }) }));
    await drain();
    none.root.flush();
    expect(none.$('button')).toBeNull();
  });

  it('joins a path as the folder writes paths', () => {
    expect(joinPath('C:\\w\\t1', 'internal/calc.go')).toBe('C:\\w\\t1\\internal\\calc.go');
    expect(joinPath('/home/w/t1', 'internal/calc.go')).toBe('/home/w/t1/internal/calc.go');
    expect(joinPath('C:\\w\\', 'a.go')).toBe('C:\\w\\a.go');
    expect(joinPath('/w', '')).toBe('/w');
  });
});

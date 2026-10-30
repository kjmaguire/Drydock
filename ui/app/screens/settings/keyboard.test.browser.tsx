// Keyboard shortcuts in Settings (decision P132): a person changes a shortcut's keys, the page refuses keys it cannot take
// before anything is sent, Save hands every binding to the core, Reset and Reset all go back to the defaults, and the About
// keyboard map shows the keys in use.
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import { comboFor, useBindings } from '../../lib/keys.ts';
import { cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { AboutDrydock } from './about.tsx';
import { KeyboardSection } from './keyboard.tsx';

afterEach(() => {
  useBindings({});
  cleanup();
});

const row = (root: ParentNode, id: string) => {
  const el = root.querySelector<HTMLElement>(`[data-testid="keys-row"][data-id="${id}"]`);
  if (!el) throw new Error(`no row ${id}`);
  return el;
};
const kbd = (el: Element) =>
  Array.from(el.querySelectorAll('kbd'))
    .map((k) => k.textContent)
    .join('+');
const press = (target: EventTarget, key: string, mods: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...mods });
  target.dispatchEvent(e);
  return e;
};

describe('keyboard shortcuts (decision P132)', () => {
  it('changes a shortcut, refuses keys it cannot take, resets one and all; About shows the keys in use', async () => {
    const sent: Record<string, string>[] = [];
    let palette = 0;
    const count = () => palette++;
    window.addEventListener('keydown', count);
    const s = testServices({
      appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: 'C:\\d' }),
      licenseStatus: async () => null as never,
      projectList: async () => [],
      keysSet: async (b) => (sent.push(b), { bindings: b }),
    } as Partial<Client>);
    s.workspace.start();
    const r = mount(
      <>
        <KeyboardSection />
        <AboutDrydock />
      </>,
      s,
    );
    const inbox = await waitFor(() => row(r.container, 'inbox'), r);
    expect(kbd(inbox)).toBe('Ctrl+Shift+K');
    expect(inbox.querySelector('[data-testid="keys-reset"]')).toBe(null);
    await r.act(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-change"]')!.click());
    const capture = await waitFor(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-capture"]')!, r);
    expect(document.activeElement).toBe(capture);
    // the keys of another shortcut: said at once, Save stays off, nothing is sent, and the window's shortcut does not act
    const before = palette;
    await r.act(() => press(capture, 'b', { ctrlKey: true }));
    expect(palette).toBe(before);
    expect(inbox.querySelector('[data-testid="keys-problem"]')!.textContent).toBe('Ctrl+B already does this: Fold or unfold the sidebar (also [ when not typing).');
    expect(inbox.querySelector<HTMLButtonElement>('[data-testid="keys-save"]')!.disabled).toBe(true);
    await r.act(() => press(capture, 'i', { shiftKey: true }));
    expect(inbox.querySelector('[data-testid="keys-problem"]')!.textContent).toBe('Hold Ctrl or Alt with it, so it never fires while you type.');
    await r.act(() => press(capture, 'i', { ctrlKey: true, altKey: true }));
    expect(inbox.querySelector('[data-testid="keys-problem"]')).toBe(null);
    expect(kbd(inbox.querySelector('[data-testid="keys-capture"]')!)).toBe('Ctrl+Alt+I');
    await r.act(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-save"]')!.click());
    await waitFor(() => expect(sent).toEqual([{ inbox: 'Ctrl+Alt+I' }]), r);
    await waitFor(() => expect(kbd(row(r.container, 'inbox'))).toBe('Ctrl+Alt+I'), r);
    expect(comboFor('inbox')).toBe('Ctrl+Alt+I');
    // the About map shows the keys in use
    const about = Array.from(r.container.querySelectorAll('[data-testid="about-keymap"] tbody tr')).find((tr) => tr.textContent!.includes('Open the Inbox'))!;
    expect(kbd(about)).toBe('Ctrl+Alt+I');

    // Esc cancels a change and keeps the keys
    await r.act(() => row(r.container, 'dock').querySelector<HTMLButtonElement>('[data-testid="keys-change"]')!.click());
    const cap2 = await waitFor(() => row(r.container, 'dock').querySelector<HTMLButtonElement>('[data-testid="keys-capture"]')!, r);
    await r.act(() => press(cap2, 'Escape'));
    await waitFor(() => expect(row(r.container, 'dock').querySelector('[data-testid="keys-capture"]')).toBe(null), r);

    // Reset puts one back; Reset all every one
    await r.act(() => row(r.container, 'inbox').querySelector<HTMLButtonElement>('[data-testid="keys-reset"]')!.click());
    await waitFor(() => expect(sent.at(-1)).toEqual({}), r);
    await waitFor(() => expect(kbd(row(r.container, 'inbox'))).toBe('Ctrl+Shift+K'), r);
    s.ui.setKeys({ dock: 'Ctrl+Alt+J', terminal: 'Alt+`' });
    await waitFor(() => r.container.querySelector<HTMLButtonElement>('[data-testid="keys-reset-all"]')!, r);
    await r.act(() => r.container.querySelector<HTMLButtonElement>('[data-testid="keys-reset-all"]')!.click());
    await waitFor(() => expect(sent.at(-1)).toEqual({}), r);
    await waitFor(() => expect(r.container.querySelector('[data-testid="keys-reset-all"]')).toBe(null), r);
    window.removeEventListener('keydown', count);
  });

  it("shows the core's refusal and keeps the keys as they were", async () => {
    const s = testServices({
      appInfo: async () => ({ name: 'Drydock', user: 'kyle', human: 'human:kyle', dataDir: 'C:\\d' }),
      licenseStatus: async () => null as never,
      projectList: async () => [],
      keysSet: async () => {
        throw new Error('inbox: Ctrl+Alt+I is already the keys of dock');
      },
    } as Partial<Client>);
    s.workspace.start();
    const r = mount(<KeyboardSection />, s);
    const inbox = await waitFor(() => row(r.container, 'inbox'), r);
    await r.act(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-change"]')!.click());
    const capture = await waitFor(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-capture"]')!, r);
    await r.act(() => press(capture, 'i', { ctrlKey: true, altKey: true }));
    await r.act(() => inbox.querySelector<HTMLButtonElement>('[data-testid="keys-save"]')!.click());
    await waitFor(() => expect(r.container.textContent).toContain('Ctrl+Alt+I is already the keys of dock'), r);
    expect(comboFor('inbox')).toBe('Ctrl+Shift+K');
  });
});

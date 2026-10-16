import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
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

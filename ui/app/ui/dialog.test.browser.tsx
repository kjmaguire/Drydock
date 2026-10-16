import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain, waitFor } from '../test/render.tsx';
import { Dialog } from './dialog.tsx';

afterEach(cleanup);

/** What the browser does for a press at `down` and a release at `up`: pointerdown on one, click on their common ancestor. */
function press(down: Element, clickTarget: Element) {
  down.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'mouse' }));
  clickTarget.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

async function open(closed: number[]) {
  const r = renderPlain(
    <Dialog open title="Rename chat" onClose={() => void closed.push(1)}>
      <input aria-label="Title" />
    </Dialog>,
  );
  const dlg = await waitFor(() => {
    const d = r.container.querySelector('dialog')!;
    if (!d.open) throw new Error('not open yet');
    return d;
  }, r);
  return { dlg, field: dlg.querySelector('input')! };
}

describe('closing a Dialog by its backdrop (L41)', () => {
  it('closes when the press and the click are both on the backdrop', async () => {
    const closed: number[] = [];
    const { dlg } = await open(closed);
    press(dlg, dlg); // the backdrop is the <dialog> element itself
    expect(closed).toEqual([1]);
  });

  it('does not close when a drag starts inside the panel and ends on the backdrop (selecting text in a field)', async () => {
    const closed: number[] = [];
    const { dlg, field } = await open(closed);
    press(field, dlg); // pointerdown in the field; the click lands on the common ancestor, the dialog
    expect(closed).toEqual([]);
    // the next real click on the backdrop still closes it: the drag left nothing behind
    press(dlg, dlg);
    expect(closed).toEqual([1]);
  });

  it('does not close for a click inside the panel, or for a click that no press began', async () => {
    const closed: number[] = [];
    const { dlg, field } = await open(closed);
    press(field, field);
    press(dlg.querySelector('h2')!, dlg.querySelector('h2')!);
    dlg.dispatchEvent(new MouseEvent('click', { bubbles: true })); // no pointerdown before it
    expect(closed).toEqual([]);
  });
});

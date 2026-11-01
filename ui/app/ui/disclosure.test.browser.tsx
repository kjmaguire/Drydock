import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { cleanup, renderPlain } from '../test/render.tsx';
import { Disclosure } from './disclosure.tsx';

afterEach(cleanup);

describe('Disclosure', () => {
  it('draws a chevron before its summary, which turns when the group opens (a flex summary has no triangle of its own)', () => {
    const { container } = renderPlain(
      <div>
        <Disclosure summary="Advanced" testId="closed">
          <p>detail</p>
        </Disclosure>
        <Disclosure summary="Details" open testId="open">
          <p>detail</p>
        </Disclosure>
      </div>,
    );
    const mark = (id: string) => getComputedStyle(container.querySelector(`[data-testid="${id}"] > summary`)!, '::before');
    const closed = mark('closed');
    const open = mark('open');
    expect(closed.content).toBe('""');
    expect(parseFloat(closed.width)).toBe(6);
    expect(closed.transform === 'none').toBe(false);
    expect(open.transform === closed.transform).toBe(false);
  });

  it('stays open when the person opened it and the parent draws again (the agent editor panel, P137)', async () => {
    let bump: () => void = () => {};
    function Parent(handle: Handle) {
      let n = 0;
      bump = () => ((n += 1), void handle.update());
      return () => (
        <div data-n={n}>
          <Disclosure summary="More settings" testId="more">
            <p>detail {n}</p>
          </Disclosure>
        </div>
      );
    }
    const r = renderPlain(<Parent />);
    const details = r.container.querySelector<HTMLDetailsElement>('[data-testid="more"]')!;
    expect(details.open).toBe(false);
    details.querySelector('summary')!.click();
    await new Promise((res) => setTimeout(res, 20)); // the toggle event is queued
    expect(details.open).toBe(true);
    bump();
    r.root.flush();
    expect(r.container.textContent).toContain('detail 1');
    expect(details.open).toBe(true);
  });
});

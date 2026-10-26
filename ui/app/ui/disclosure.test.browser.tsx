import { expect } from 'remix/assert';
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
});

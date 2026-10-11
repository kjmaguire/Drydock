import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { on, ref, type Handle } from 'remix/component';
import { cleanup, renderPlain, waitFor } from '../test/render.tsx';
import { VirtualList } from './virtual-list.tsx';

afterEach(cleanup);

interface Item {
  id: string;
  /** The row's real height: the list never knows it before measuring. */
  h: number;
}

/** A feed under test: plain rows of known height in a scroller of a fixed height. Not a test double: real DOM, real layout. */
interface Harness {
  items: Item[];
  list?: VirtualList;
  update?: () => void;
  renders: number;
}

function Feed(handle: Handle<{ h: Harness; height?: number }>) {
  const h = handle.props.h;
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal });
  h.list = list;
  h.update = () => void handle.update();
  return () => {
    h.renders++;
    const rows = list.rows(
      h.items,
      (it) => it.id,
      (it) => (
        <div data-testid="row" data-id={it.id} style={{ height: `${it.h}px`, borderBottom: '1px solid #ccc' }}>
          {it.id}
          <button type="button">act on {it.id}</button>
          {it.id.endsWith('7') ? (
            <details>
              <summary>more</summary>
              <p style={{ height: '40px', margin: '0' }}>detail</p>
            </details>
          ) : null}
        </div>
      ),
    );
    handle.queueTask(() => list.settle());
    return (
      <div
        data-testid="scroller"
        tabIndex={0}
        style={{ height: `${handle.props.height ?? 400}px`, overflowY: 'auto', position: 'relative' }}
        aria-live={list.quiet ? 'off' : 'polite'}
        mix={[ref(list.scroller), on('scroll', () => list.onScroll())]}
      >
        <p data-testid="above" style={{ height: '30px', margin: '0' }}>
          above the list
        </p>
        {rows}
      </div>
    );
  };
}

// heights from 24 to 154 px, in a fixed pseudo-random order (the same every run)
const heightOf = (i: number) => 24 + ((i * 37) % 131);
const items = (from: number, to: number): Item[] => Array.from({ length: to - from }, (_, k) => ({ id: `r${from + k}`, h: heightOf(from + k) }));

function setup(list: Item[], height = 400) {
  const h: Harness = { items: list, renders: 0 };
  const r = renderPlain(<Feed h={h} height={height} />);
  const sc = r.container.querySelector<HTMLDivElement>('[data-testid="scroller"]')!;
  const rows = () => Array.from(r.container.querySelectorAll<HTMLElement>('[data-testid="row"]'));
  const ids = () => rows().map((e) => e.getAttribute('data-id'));
  /** Where a row is, relative to the top of the scroller's view. */
  const topOf = (id: string) => r.container.querySelector(`[data-id="${id}"]`)!.getBoundingClientRect().top - sc.getBoundingClientRect().top;
  const atBottom = () => sc.scrollHeight - sc.scrollTop - sc.clientHeight < 1;
  /** Scroll as a reader does: the browser's own scroll event, in its own task, reaches the list. */
  const scrollTo = async (top: number) => {
    const before = sc.scrollTop;
    const moved = new Promise((res) => sc.addEventListener('scroll', res, { once: true }));
    sc.scrollTop = top;
    if (sc.scrollTop !== before) await moved;
    r.root.flush();
  };
  return { h, r, sc, rows, ids, topOf, atBottom, scrollTo };
}

/** The most rows a window can hold: the view, an overscan of one view on each side, the hysteresis, at 24 px a row. */
const bound = (view: number) => Math.ceil((view * 4 + 2 * 154) / 25);

describe('VirtualList', () => {
  it('renders a bounded window of 2,000 rows of varying height, opening at the bottom', async () => {
    const t = setup(items(0, 2000));
    await waitFor(() => expect(t.ids().at(-1)).toBe('r1999'), t.r);
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    expect(t.rows().length).toBeLessThanOrEqual(bound(400));
    expect(t.rows().length).toBeGreaterThan(400 / 154);
    expect(t.h.list!.renderedCount).toBe(t.rows().length);
    // the rows rendered are contiguous and end at the newest
    const n = t.ids().map((id) => Number(id!.slice(1)));
    expect(n.every((x, i) => i === 0 || x === n[i - 1]! + 1)).toBe(true);
    // the rows above are padding of about their height (not yet measured: the mean of the measured rows)
    expect(t.sc.scrollHeight).toBeGreaterThan(2000 * 24);
  });

  it('stays bounded while scrolled through, and measures every row it showed', async () => {
    const t = setup(items(0, 150));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    const real = new Map<string, number>();
    let most = 0;
    // from the bottom to the top, a little less than a view at a time (each step is the browser's own scroll event)
    for (let i = 0; i < 2000; i++) {
      for (const el of t.rows()) real.set(el.getAttribute('data-id')!, el.parentElement!.getBoundingClientRect().height);
      most = Math.max(most, t.rows().length);
      if (t.sc.scrollTop <= 0) break;
      await t.scrollTo(Math.max(0, t.sc.scrollTop - 390));
      t.r.root.flush();
    }
    expect(most).toBeLessThanOrEqual(bound(400));
    expect(real.size).toBe(150);
    // every row measured: the padding is the real height of the rows it stands for, to the pixel
    const sum = [...real.values()].reduce((a, b) => a + b, 0);
    expect(Math.abs(t.sc.scrollHeight - (30 + sum))).toBeLessThan(2);
    expect(t.ids()[0]).toBe('r0');
  });

  it('keeps a new row at the bottom while the reader is there, and announces it', async () => {
    const t = setup(items(0, 2000));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    await waitFor(() => expect(t.sc.getAttribute('aria-live')).toBe('polite'), t.r);
    t.h.items = [...t.h.items, { id: 'r2000', h: 150 }];
    await t.r.act(() => t.h.update!());
    expect(t.ids().at(-1)).toBe('r2000');
    expect(t.atBottom()).toBe(true);
    // an added row is new: the live region was not silenced for it
    expect(t.sc.getAttribute('aria-live')).toBe('polite');
    expect(t.h.list!.stick).toBe(true);
  });

  it('does not move what is being read when rows arrive below, and an older page is added above', async () => {
    const t = setup(items(1000, 2000));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    // the reader scrolls up into the middle
    await t.scrollTo(t.sc.scrollTop - 5000);
    t.r.root.flush();
    expect(t.h.list!.stick).toBe(false);
    const id = t.ids().find((x) => t.topOf(x!) > 0)!;
    const at = t.topOf(id);
    const scrolled = t.sc.scrollTop;
    // rows arrive below: the view stays, the new rows are not rendered
    t.h.items = [...t.h.items, ...items(2000, 2010)];
    await t.r.act(() => t.h.update!());
    expect(t.topOf(id)).toBeCloseTo(at, 0);
    expect(t.ids()).not.toContain('r2009');
    // an older page of 1,000 rows is added above: the same row is at the same place, further down the scroll range
    t.h.items = [...items(0, 1000), ...t.h.items];
    await t.r.act(() => t.h.update!());
    t.r.root.flush();
    expect(Math.abs(t.topOf(id) - at)).toBeLessThan(1);
    expect(t.sc.scrollTop).toBeGreaterThan(scrolled + 1000 * 24);
    expect(t.rows().length).toBeLessThanOrEqual(bound(400));
  });

  it('keeps the view when a row above it changes height', async () => {
    const t = setup(items(0, 400));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    await t.scrollTo(t.sc.scrollTop - 3000);
    t.r.root.flush();
    const shown = t.ids();
    // a row rendered above the view (in the overscan) grows by 200 px, as an image that loads would
    const above = shown.find((x) => t.topOf(x!) < -300)!;
    const id = shown.find((x) => t.topOf(x!) > 0)!;
    const at = t.topOf(id);
    t.h.items = t.h.items.map((it) => (it.id === above ? { ...it, h: it.h + 200 } : it));
    await t.r.act(() => t.h.update!());
    await waitFor(() => expect(Math.abs(t.topOf(id) - at)).toBeLessThan(1), t.r);
  });

  it('silences the live region for rows revealed by scrolling, then speaks again', async () => {
    const t = setup(items(0, 2000));
    await waitFor(() => expect(t.sc.getAttribute('aria-live')).toBe('polite'), t.r);
    await t.scrollTo(0);
    t.r.root.flush();
    // the rows at the top entered the DOM in this render: they are history, not news
    expect(t.ids()[0]).toBe('r0');
    expect(t.sc.getAttribute('aria-live')).toBe('off');
    // two frames later the region is live again (for rows that are really new)
    await waitFor(() => expect(t.sc.getAttribute('aria-live')).toBe('polite'), t.r);
  });

  it('moves focus to the scroller when the focused row leaves the window, and keeps a row\'s details open', async () => {
    const t = setup(items(0, 2000));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    const row = t.r.container.querySelector<HTMLElement>('[data-id="r1997"]')!;
    row.querySelector('details')!.open = true;
    const btn = row.querySelector('button')!;
    btn.focus();
    expect(document.activeElement).toBe(btn);
    await t.scrollTo(0);
    t.r.root.flush();
    expect(t.r.container.querySelector('[data-id="r1997"]')).toBeNull();
    expect(document.activeElement).toBe(t.sc);
    // back at the bottom, the row comes back with its details as they were
    await t.r.act(() => t.h.list!.toBottom());
    t.r.root.flush();
    await waitFor(() => expect(t.r.container.querySelector<HTMLDetailsElement>('[data-id="r1997"] details')!.open).toBe(true), t.r);
    // only that one: the others came back closed
    const open = Array.from(t.r.container.querySelectorAll<HTMLDetailsElement>('details')).filter((d) => d.open);
    expect(open.map((d) => d.closest('[data-id]')!.getAttribute('data-id'))).toEqual(['r1997']);
  });

  it('stays at the bottom when the view gets smaller or larger', async () => {
    const t = setup(items(0, 500));
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    t.sc.style.height = '200px';
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    t.sc.style.height = '700px';
    await waitFor(() => expect(t.atBottom()).toBe(true), t.r);
    await waitFor(() => expect(t.ids().at(-1)).toBe('r499'), t.r);
    expect(t.rows().length).toBeLessThanOrEqual(bound(700));
  });

  it('renders every row of a short list, and nothing for an empty one', async () => {
    const t = setup(items(0, 3));
    await waitFor(() => expect(t.ids()).toEqual(['r0', 'r1', 'r2']), t.r);
    const e = setup([]);
    expect(e.rows()).toHaveLength(0);
    expect(e.r.container.querySelector('[data-virtual-list]')!.getAttribute('style')).toContain('padding-top: 0px');
  });
});

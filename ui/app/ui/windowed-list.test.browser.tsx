import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { type Handle } from 'remix/component';
import { cleanup, renderPlain, waitFor } from '../test/render.tsx';
import { listOf, WindowedList } from './windowed-list.tsx';

afterEach(cleanup);

interface Row {
  id: string;
  /** The real height of the row, which the list learns only by measuring it. */
  h: number;
}
interface Harness {
  rows: Row[];
  update?: () => void;
}

// Not a test double: real rows in real layout. The rows have different heights, as findings do.
function Page(handle: Handle<{ h: Harness; plainUpTo?: number }>) {
  handle.props.h.update = () => void handle.update();
  return () => {
    const rows = handle.props.h.rows;
    return (
      <WindowedList
        {...listOf(
          rows,
          (row) => row.id,
          (row) => (
            <div data-testid="row" data-id={row.id} style={{ height: `${row.h}px`, borderBottom: '1px solid #ccc' }}>
              {row.id}
            </div>
          ),
        )}
        label="Things"
        testId="things"
        maxHeight={300}
        estimate={30}
        plainUpTo={handle.props.plainUpTo}
      />
    );
  };
}

const rowsOf = (from: number, to: number): Row[] => Array.from({ length: to - from }, (_, k) => ({ id: `r${from + k}`, h: 20 + (((from + k) * 37) % 41) }));

/** Two animation frames: the layout that follows a render, and the measuring and re-planning that follow that. */
const frames = () => new Promise<void>((res) => requestAnimationFrame(() => requestAnimationFrame(() => res())));

function setup(rows: Row[], plainUpTo?: number) {
  const h: Harness = { rows };
  const r = renderPlain(<Page h={h} plainUpTo={plainUpTo} />);
  const list = () => r.container.querySelector<HTMLElement>('[data-testid="things"]')!;
  const ids = () => Array.from(r.container.querySelectorAll<HTMLElement>('[data-testid="row"]')).map((e) => e.getAttribute('data-id')!);
  /** Scroll as a reader does: the browser's own scroll event, in its own task, reaches the list. */
  const scrollTo = async (top: number) => {
    const sc = list();
    const before = sc.scrollTop;
    const moved = new Promise((res) => sc.addEventListener('scroll', res, { once: true }));
    sc.scrollTop = top;
    if (sc.scrollTop !== before) await moved;
    r.root.flush();
  };
  return { h, r, list, ids, scrollTo };
}

describe('WindowedList (L37)', () => {
  it('draws a short list whole, in the page, without a scroller', () => {
    const t = setup(rowsOf(0, 30));
    expect(t.list().getAttribute('data-windowed')).toBe('false');
    expect(t.ids()).toHaveLength(30);
    expect(t.list().getAttribute('tabindex')).toBeNull();
    expect(t.list().getAttribute('aria-label')).toBe('Things');
    expect(t.list().getAttribute('role')).toBe('group');
  });

  it('draws only the rows near the view of a list of 20,000, and the first and the last rows when it is scrolled to them', async () => {
    const t = setup(rowsOf(0, 20_000));
    await waitFor(() => expect(t.list().getAttribute('data-windowed')).toBe('true'), t.r);
    // a window: the view (300 px) and about a view on each side, at about 20 to 60 px a row
    await waitFor(() => expect(t.ids().length).toBeGreaterThan(5), t.r);
    expect(t.ids().length).toBeLessThan(150);
    expect(t.ids()[0]).toBe('r0');
    expect(t.ids()).not.toContain('r19999');
    // a scroller of a most height, named, reachable by keyboard
    const sc = t.list();
    expect(getComputedStyle(sc).overflowY).toBe('auto');
    expect(sc.clientHeight).toBeLessThanOrEqual(300);
    expect(sc.scrollHeight).toBeGreaterThan(20_000 * 20);
    expect(sc.getAttribute('tabindex')).toBe('0');
    expect(sc.getAttribute('aria-label')).toBe('Things');
    expect(sc.getAttribute('data-rows')).toBe('20000');

    // to the end: the last row is there, and the list is still a window
    await t.scrollTo(sc.scrollHeight);
    await waitFor(() => expect(t.ids()).toContain('r19999'), t.r);
    await waitFor(() => expect(t.ids()).not.toContain('r0'), t.r);
    expect(t.ids().length).toBeLessThan(150);

    // and back to the start
    await t.scrollTo(0);
    await waitFor(() => expect(t.ids()[0]).toBe('r0'), t.r);
    expect(t.ids().length).toBeLessThan(150);
  });

  it('reaches a row in the middle by scrolling, in order and without a gap', async () => {
    const t = setup(rowsOf(0, 5_000));
    await waitFor(() => expect(t.ids().length).toBeGreaterThan(5), t.r);
    await frames(); // the first layout, with every row measured once, comes before the reader's first scroll
    const sc = t.list();
    await t.scrollTo(sc.scrollHeight / 2);
    await waitFor(() => expect(t.ids()[0]).not.toBe('r0'), t.r);
    const n = t.ids().map((id) => Number(id.slice(1)));
    expect(n.every((x, i) => i === 0 || x === n[i - 1]! + 1)).toBe(true);
    expect(n[0]!).toBeGreaterThan(1000);
    expect(n.at(-1)!).toBeLessThan(4000);
  });

  it('becomes a whole list when it shrinks to the plain length, and a window again when it grows', async () => {
    const t = setup(rowsOf(0, 400));
    await waitFor(() => expect(t.list().getAttribute('data-windowed')).toBe('true'), t.r);
    expect(t.ids().length).toBeLessThan(400);
    t.h.rows = rowsOf(0, 40);
    await t.r.act(() => t.h.update!());
    await waitFor(() => expect(t.list().getAttribute('data-windowed')).toBe('false'), t.r);
    expect(t.ids()).toHaveLength(40);
    t.h.rows = rowsOf(0, 400);
    await t.r.act(() => t.h.update!());
    await waitFor(() => expect(t.list().getAttribute('data-windowed')).toBe('true'), t.r);
    await waitFor(() => expect(t.ids()[0]).toBe('r0'), t.r);
    expect(t.ids().length).toBeLessThan(400);
  });

  it('draws nothing for an empty list', () => {
    const t = setup([]);
    expect(t.ids()).toEqual([]);
    expect(t.list().getAttribute('data-windowed')).toBe('false');
  });
});

describe('listOf (L37)', () => {
  it('gives the count, and the key and the row of each place with the item and its place', () => {
    const seen: string[] = [];
    const list = listOf(
      ['a', 'b', 'c'],
      (item, i) => `${item}${i}`,
      (item, i) => (seen.push(`${item}@${i}`), item.toUpperCase()),
    );
    expect(list.count).toBe(3);
    expect([0, 1, 2].map((i) => list.rowKey(i))).toEqual(['a0', 'b1', 'c2']);
    expect([0, 1, 2].map((i) => list.renderRow(i))).toEqual(['A', 'B', 'C']);
    expect(seen).toEqual(['a@0', 'b@1', 'c@2']);
  });

  it('answers a place that is not in the list with the place as the key and no row, and asks the caller for nothing', () => {
    let asked = 0;
    const list = listOf(
      ['a'],
      () => (asked++, 'k'),
      () => (asked++, 'row'),
    );
    expect(list.rowKey(3)).toBe('3');
    expect(list.renderRow(3)).toBe(null);
    expect(list.rowKey(-1)).toBe('-1');
    expect(list.renderRow(-1)).toBe(null);
    expect(asked).toBe(0);
    expect(listOf([], () => 'k', () => 'row').count).toBe(0);
  });
});

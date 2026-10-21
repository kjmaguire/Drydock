// A long list read from the top, drawn in a box that scrolls with only the rows near the view in the page (audit 2026-10-16, L37).
// It puts the windowing of ui/virtual-list.tsx (made for feeds that follow their newest row) behind a small component for lists
// that do not: findings, the lines of a long patch, a project's notes. A list of up to `plainUpTo` rows is drawn whole, in the
// page, as it was before; a longer one gets a scroller of a most height, named and reachable by keyboard.
//
// The list is given by its length and two functions of a row's place in it, so the caller keeps its own array and nothing is
// copied or typed twice.
//
// What windowing costs, and the reader should know: the rows outside the window are not in the page, so Select all and Copy take
// the rows drawn (the view and about one view above and below it), and the browser's find-in-page sees only those.

import { css, on, ref, type Handle, type Props, type RemixNode } from 'remix/component';
import { t } from './tokens.ts';
import { VirtualList } from './virtual-list.tsx';

/** A row box, as in virtual-list.tsx: it contains its children's margins, so the row's measured height is the space it takes. */
const rowBox = css({ display: 'flow-root' });
const scrollerCss = css({ overflow: 'auto', '&:focus-visible': { outline: 'none', boxShadow: t.focus } });

/** Rows up to this many are drawn whole, without a scroller. */
export const PLAIN_ROWS = 100;

export interface WindowedListProps {
  /** How many rows the list has. */
  count: number;
  /** Names the place of row `i`: its key, which the window anchors the view on. It must not change while the row stays where it is. */
  rowKey: (i: number) => string;
  /** Row `i`, drawn. */
  renderRow: (i: number) => RemixNode;
  /** The accessible name of the list (and of its scroller). */
  label: string;
  /** The role of the list and its scroller. A group by default; "table" and "list" need rows with the matching roles. */
  role?: Props<'div'>['role'];
  testId?: string;
  /** The scroller's most height in px. */
  maxHeight?: number;
  /** A row's height before it was measured. */
  estimate?: number;
  /** A list of this many rows or fewer is drawn whole, without a scroller. */
  plainUpTo?: number;
  /** Drawn before the rows, inside the scroller (a table's header row; make it sticky with position: sticky). */
  header?: RemixNode;
  /** The number of rows of the whole list for assistive technology (aria-rowcount), for a table: the rows not in the page are counted. */
  ariaRowCount?: number;
  /** More style for the list, in both forms. */
  mix?: ReturnType<typeof css>[];
}

/**
 * The `count`, `rowKey` and `renderRow` of a WindowedList for the items of an array, each given with its place (spread them:
 * `<WindowedList {...listOf(items, key, render)} label="..." />`). A place that is not in the array has its number for a key and
 * draws nothing; the window asks only for places inside the list it was given, so this is what a total function has to say.
 */
export function listOf<T>(
  items: readonly T[],
  key: (item: T, i: number) => string,
  render: (item: T, i: number) => RemixNode,
): Pick<WindowedListProps, 'count' | 'rowKey' | 'renderRow'> {
  return {
    count: items.length,
    rowKey: (i) => {
      const item = items[i];
      return item === undefined ? String(i) : key(item, i);
    },
    renderRow: (i) => {
      const item = items[i];
      return item === undefined ? null : render(item, i);
    },
  };
}

export function WindowedList(handle: Handle<WindowedListProps>) {
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal, estimate: handle.props.estimate });
  // read from the top, not stuck to the bottom like a feed
  list.stick = false;
  // the places 0..count-1, kept while the count stays (the window plans over them)
  let places: number[] = [];
  return () => {
    const { count, rowKey, renderRow, label, role = 'group', testId, maxHeight = 560, plainUpTo = PLAIN_ROWS, header, ariaRowCount, mix = [] } = handle.props;
    if (count <= plainUpTo) {
      return (
        <div key="whole" role={role} aria-label={label} aria-rowcount={ariaRowCount} data-testid={testId} data-windowed="false" mix={mix}>
          {header}
          {Array.from({ length: count }, (_, i) => {
            const k = rowKey(i);
            return (
              <div key={k} data-vkey={k} mix={rowBox}>
                {renderRow(i)}
              </div>
            );
          })}
        </div>
      );
    }
    if (places.length !== count) places = Array.from({ length: count }, (_, i) => i);
    const rows = list.rows(places, rowKey, renderRow);
    handle.queueTask(() => list.settle());
    return (
      <div
        key="windowed"
        role={role}
        aria-label={label}
        aria-rowcount={ariaRowCount}
        tabIndex={0}
        data-testid={testId}
        data-windowed="true"
        data-rows={count}
        style={{ maxHeight: `${maxHeight}px` }}
        mix={[scrollerCss, ...mix, ref(list.scroller), on('scroll', () => list.onScroll())]}
      >
        {header}
        {rows}
      </div>
    );
  };
}

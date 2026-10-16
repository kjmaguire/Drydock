// The arithmetic of a windowed (virtualised) list, with no DOM: where each row starts, which rows meet a band of the list,
// and which rows to render for a view. ui/virtual-list.tsx measures the rows and keeps the scroll position; this file only
// plans, so it is tested in Node (virtual.test.ts).
//
// Coordinates are "list coordinates": 0 is the top of the first row, offsets[n] the height of all n rows.

/** Row tops: offsets[i] is where row i starts, offsets[n] the total height. */
export function offsetsOf(heights: readonly number[]): number[] {
  const out = new Array<number>(heights.length + 1);
  out[0] = 0;
  for (let i = 0; i < heights.length; i++) out[i + 1] = out[i]! + heights[i]!;
  return out;
}

/** The row that contains y (the first row whose bottom is below y), clamped to [0, n - 1]; 0 for an empty list. */
export function rowAt(offsets: readonly number[], y: number): number {
  const n = offsets.length - 1;
  if (n <= 0) return 0;
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (offsets[mid + 1]! <= y) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** The rows [start, end) that meet the band [top, bottom); at least one row when the list is not empty. */
export function rowsBetween(offsets: readonly number[], top: number, bottom: number): { start: number; end: number } {
  const n = offsets.length - 1;
  if (n <= 0) return { start: 0, end: 0 };
  const start = rowAt(offsets, top);
  const end = Math.max(start + 1, rowAt(offsets, Math.max(top, bottom - 0.001)) + 1);
  return { start, end: Math.min(n, end) };
}

export interface WindowPlan {
  start: number;
  end: number;
  /** Height of the rows above the window (drawn as padding). */
  padTop: number;
  /** Height of the rows below the window. */
  padBottom: number;
}

export interface PlanInput {
  offsets: readonly number[];
  /** The top of the view in list coordinates (may be negative when something above the list is in view). */
  viewTop: number;
  viewHeight: number;
  /** Rows kept rendered beyond the view on each side, in px. */
  overscan: number;
  /** The rows rendered last time, as indices into this list: kept while they still cover the view (fewer DOM changes). */
  keep?: { start: number; end: number };
}

/**
 * The rows to render. The window covers the view plus the overscan on each side; a window rendered before is kept while it
 * still covers the view plus half the overscan and is not more than three overscans larger than needed, so scrolling
 * re-renders once every half overscan or so, not on every scroll event.
 */
export function planWindow({ offsets, viewTop, viewHeight, overscan, keep }: PlanInput): WindowPlan {
  const n = offsets.length - 1;
  if (n <= 0) return { start: 0, end: 0, padTop: 0, padBottom: 0 };
  const total = offsets[n]!;
  const plan = (start: number, end: number): WindowPlan => ({ start, end, padTop: offsets[start]!, padBottom: total - offsets[end]! });
  const needTop = Math.max(0, viewTop - overscan / 2);
  const needBottom = Math.min(total, viewTop + viewHeight + overscan / 2);
  if (keep && keep.start >= 0 && keep.end <= n && keep.start < keep.end) {
    const top = offsets[keep.start]!;
    const bottom = offsets[keep.end]!;
    const covers = (keep.start === 0 || top <= needTop) && (keep.end === n || bottom >= needBottom);
    const lean = bottom - top <= viewHeight + overscan * 3;
    if (covers && lean) return plan(keep.start, keep.end);
  }
  const { start, end } = rowsBetween(offsets, viewTop - overscan, viewTop + viewHeight + overscan);
  return plan(start, end);
}

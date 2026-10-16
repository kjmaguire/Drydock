// A windowed, bottom-anchored list for message feeds (docs/ui.md 6.4): only the rows near the view are in the DOM, the rows
// above and below are padding of their measured (or estimated) height.
//
// The screen owns its scroller and renders `list.rows(...)` inside it; this controller does the rest:
// - measures every rendered row (its height after render, then a ResizeObserver for images, opened details, a new width)
//   and keeps the heights by row key; a row never measured counts as the mean of the measured ones;
// - plans the window (ui/virtual.ts): the view plus about one view of overscan on each side, re-planned only when the view
//   gets within half an overscan of the window's edge;
// - keeps the list stuck to the bottom while the reader is there (`stick`), and otherwise keeps the row at the top of the
//   view where it was (an anchor: its key and its distance from the top of the view), so an older page added above, a
//   measurement that differs from the estimate, or a row that grows does not move what is being read;
// - says when rows were revealed rather than added (`quiet`), so a live region announces only rows that are new;
// - moves focus to the scroller when the focused row leaves the window, and keeps each row's <details> open or closed
//   across leaving and re-entering the window.
//
// Call `rows()` during render (before reading `quiet`), `settle()` from handle.queueTask after each render, and
// `onScroll()` from the scroller's scroll event. `onChange` re-renders the screen.

import { css, on, ref, type RemixNode } from 'remix/component';
import { offsetsOf, planWindow, type WindowPlan } from './virtual.ts';

/** A row box contains its children's margins (flow-root), so its measured height is the space it takes. */
const rowBox = css({ display: 'flow-root' });

export interface VirtualListOptions {
  /** Re-render the screen (handle.update()). */
  onChange: () => void;
  /** Stop observing and scheduling when this aborts (handle.signal). */
  signal?: AbortSignal;
  /** Height of a row before any row was measured. */
  estimate?: number;
  /** The least overscan in px on each side; the overscan is at least one view. */
  overscan?: number;
  /** Within this distance of the bottom the list counts as at the bottom. */
  stickWithin?: number;
}

const keyOf = (el: Element): string | null => el.getAttribute('data-vkey');

export class VirtualList {
  /** The reader is at the bottom: new rows keep the list at the bottom. */
  stick = true;
  /** Rows were revealed (scrolling, a page added above, a re-plan), not added at the end: a live region should stay silent. */
  quiet = false;

  #onChange: () => void;
  #estimate: number;
  #overscan: number;
  #within: number;
  #dead = false;

  #sc: HTMLElement | undefined;
  #list: HTMLElement | undefined;
  #ro: ResizeObserver | undefined;

  /** Measured heights by row key. */
  #h = new Map<string, number>();
  #sum = 0;
  /** The keys at the last render and the window rendered for them. */
  #keys: string[] = [];
  #plan: WindowPlan = { start: 0, end: 0, padTop: 0, padBottom: 0 };
  #rendered = new Set<string>();
  /** The top of the list inside the scroller's content (what is above it: a "Load older" button, an intro). */
  #listTop = 0;
  /**
   * The rows in the view, top first, with their distance from the top of the view, while not stuck to the bottom. The
   * first one still in the list is the anchor (a row can leave the list: a day divider moves when its day grows above).
   */
  #anchors: { key: string; top: number }[] = [];
  #quietFrame = 0;
  /** The scroll position last read or set: a different one at restore time is the reader's scroll, its event still to come. */
  #seen: number | undefined;
  /** Where the controller itself last scrolled to: the scroll event that follows is not the reader's. */
  #own: number | undefined;

  #focused: Element | undefined;
  #refocus = false;
  #fresh = new Set<Element>();
  #open = new Map<string, boolean[]>();

  constructor(opts: VirtualListOptions) {
    this.#onChange = opts.onChange;
    this.#estimate = opts.estimate ?? 48;
    this.#overscan = opts.overscan ?? 240;
    this.#within = opts.stickWithin ?? 40;
    opts.signal?.addEventListener('abort', () => this.#dispose(), { once: true });
    if (typeof ResizeObserver !== 'undefined') this.#ro = new ResizeObserver((entries) => this.#resized(entries));
  }

  /** How many rows are in the DOM (for tests and diagnostics). */
  get renderedCount(): number {
    return this.#plan.end - this.#plan.start;
  }

  /** ref() for the scroller (the element with overflow: auto). */
  scroller = (node: Element, signal: AbortSignal): void => {
    this.#sc = node as HTMLElement;
    // the controller keeps the position itself; the browser's own scroll anchoring would correct it a second time
    this.#sc.style.overflowAnchor = 'none';
    this.#ro?.observe(node);
    signal.addEventListener('abort', () => {
      this.#ro?.unobserve(node);
      if (this.#sc === node) this.#sc = undefined;
    });
  };

  /**
   * The list: its rows in the window, wrapped in boxes that are measured, with the rows outside it as padding. Call during
   * render; `render` draws one item.
   */
  rows<T>(items: readonly T[], key: (item: T) => string, render: (item: T, index: number) => RemixNode): RemixNode {
    const keys = items.map(key);
    const p = this.#planFor(keys);
    const shown: RemixNode[] = [];
    for (let i = p.start; i < p.end; i++) {
      shown.push(
        <div key={keys[i]} data-vkey={keys[i]} mix={[rowBox, ref(this.#row)]}>
          {render(items[i]!, i)}
        </div>,
      );
    }
    return (
      <div data-virtual-list="" style={{ paddingTop: `${p.padTop}px`, paddingBottom: `${p.padBottom}px` }} mix={[ref(this.#listRef), on<HTMLDivElement, 'focusin'>('focusin', this.#focusIn)]}>
        {shown}
      </div>
    );
  }

  /** After a render: measure the rows, keep the position (bottom or anchor), and re-render if the window should change. */
  settle(): void {
    const sc = this.#sc;
    const list = this.#list;
    if (this.#dead || !sc || !list) return;
    this.#restoreDetails();
    this.#measure(list);
    this.#listTop = list.getBoundingClientRect().top - sc.getBoundingClientRect().top - sc.clientTop + sc.scrollTop;
    this.#restore();
    if (!this.stick && !this.#anchors.length) this.#capture();
    if (this.#refocus) {
      this.#refocus = false;
      const a = document.activeElement;
      if (!a || a === document.body) sc.focus({ preventScroll: true });
    }
    if (this.#stale()) this.#onChange();
  }

  /** From the scroller's scroll event: at the bottom or not, the anchor, and a new window when the view nears its edge. */
  onScroll(): void {
    const sc = this.#sc;
    if (this.#dead || !sc) return;
    const own = this.#own !== undefined && Math.abs(sc.scrollTop - this.#own) < 1;
    this.#own = undefined;
    if (own) {
      // the controller's own scroll (to the bottom, or back to the anchor): the reader did not move. The view may have
      // changed size since (a resize between the scroll and its event), so put it back rather than read it as leaving.
      this.#restore();
      if (this.#stale()) this.#onChange();
      return;
    }
    const was = this.stick;
    this.#read();
    if (this.stick !== was || this.#stale()) this.#onChange();
  }

  /** Jump to the newest row and stay there. */
  toBottom(): void {
    this.stick = true;
    this.#anchors = [];
    if (this.#sc) this.#scrollTo(this.#sc.scrollHeight);
    this.#onChange();
  }

  // ---- planning

  #mean(): number {
    return this.#h.size ? this.#sum / this.#h.size : this.#estimate;
  }

  #compute(keys: readonly string[]): { plan: WindowPlan; offsets: number[] } {
    const est = this.#mean();
    const offsets = offsetsOf(keys.map((k) => this.#h.get(k) ?? est));
    const total = offsets[keys.length]!;
    const sc = this.#sc;
    const viewHeight = sc && sc.clientHeight > 0 ? sc.clientHeight : typeof window !== 'undefined' ? window.innerHeight || 800 : 800;
    const overscan = Math.max(this.#overscan, viewHeight);
    let viewTop: number;
    let ai = -1;
    let anchor: { key: string; top: number } | undefined;
    if (!this.stick) {
      for (const a of this.#anchors) {
        ai = keys.indexOf(a.key);
        if (ai >= 0) {
          anchor = a;
          break;
        }
      }
    }
    if (this.stick) viewTop = total - viewHeight;
    else if (anchor) viewTop = offsets[ai]! - anchor.top;
    else viewTop = (sc?.scrollTop ?? 0) - this.#listTop;
    // the rows rendered last time, by key, so a list that grew at either end keeps them
    let keep: { start: number; end: number } | undefined;
    const prev = this.#keys;
    if (this.#plan.end > this.#plan.start) {
      const first = prev[this.#plan.start]!;
      const last = prev[this.#plan.end - 1]!;
      const s = keys.indexOf(first);
      let e = s < 0 ? -1 : keys.indexOf(last, s) + 1;
      // at the bottom, rows added after the old end join the window
      if (e > 0 && this.stick && this.#plan.end === prev.length) e = keys.length;
      if (s >= 0 && e > s) keep = { start: s, end: e };
    }
    return { plan: planWindow({ offsets, viewTop, viewHeight, overscan, keep }), offsets };
  }

  #planFor(keys: string[]): WindowPlan {
    const { plan } = this.#compute(keys);
    // a row that enters the window at or above the old last row was revealed, not added: keep the live region silent
    const prevLast = this.#keys.at(-1);
    if (prevLast !== undefined && this.#rendered.size) {
      const li = keys.lastIndexOf(prevLast);
      for (let i = plan.start; i < plan.end; i++) {
        if (!this.#rendered.has(keys[i]!) && (li < 0 || i <= li)) {
          this.#hush();
          break;
        }
      }
    }
    this.#keys = keys;
    this.#plan = plan;
    this.#rendered = new Set(keys.slice(plan.start, plan.end));
    return plan;
  }

  /** The window planned now differs from the one rendered. */
  #stale(): boolean {
    const { plan } = this.#compute(this.#keys);
    const r = this.#plan;
    return plan.start !== r.start || plan.end !== r.end || Math.abs(plan.padTop - r.padTop) > 1 || Math.abs(plan.padBottom - r.padBottom) > 1;
  }

  /** Quiet until two frames have passed, so the revealed rows are in the accessibility tree before the region speaks again. */
  #hush(): void {
    this.quiet = true;
    if (this.#quietFrame || typeof requestAnimationFrame === 'undefined') return;
    this.#quietFrame = requestAnimationFrame(() => {
      this.#quietFrame = requestAnimationFrame(() => {
        this.#quietFrame = 0;
        if (this.#dead) return;
        this.quiet = false;
        this.#onChange();
      });
    });
  }

  // ---- measuring and position

  #setHeight(key: string, h: number): boolean {
    const old = this.#h.get(key);
    if (old !== undefined && Math.abs(old - h) < 0.5) return false;
    this.#sum += h - (old ?? 0);
    this.#h.set(key, h);
    return true;
  }

  #measure(list: HTMLElement): boolean {
    let changed = false;
    for (const el of Array.from(list.children)) {
      const k = keyOf(el);
      if (k !== null && this.#setHeight(k, el.getBoundingClientRect().height)) changed = true;
    }
    return changed;
  }

  #rowEl(key: string): HTMLElement | undefined {
    for (const el of Array.from(this.#list?.children ?? [])) if (keyOf(el) === key) return el as HTMLElement;
    return undefined;
  }

  /**
   * Remember the rendered rows that are in the view, and where they are. None when the view shows only padding (a jump
   * with the scroll bar): the next window is then planned from the scroll position, and settle() anchors after it.
   */
  #capture(): void {
    const sc = this.#sc;
    const list = this.#list;
    this.#anchors = [];
    if (!sc || !list || this.stick) return;
    const box = sc.getBoundingClientRect();
    const viewTop = box.top + sc.clientTop;
    const viewBottom = viewTop + sc.clientHeight;
    for (const el of Array.from(list.children)) {
      const k = keyOf(el);
      if (k === null) continue;
      const r = el.getBoundingClientRect();
      if (r.top >= viewBottom || this.#anchors.length >= 8) return;
      if (r.bottom > viewTop) this.#anchors.push({ key: k, top: r.top - viewTop });
    }
  }

  /** Read the reader's position: at the bottom or not, and the anchor. */
  #read(): void {
    const sc = this.#sc!;
    this.stick = sc.scrollHeight - sc.scrollTop - sc.clientHeight < this.#within;
    this.#seen = sc.scrollTop;
    this.#capture();
  }

  /** Put the view back: at the bottom while stuck, else the anchor row where it was. */
  #restore(): void {
    const sc = this.#sc;
    if (!sc) return;
    // the reader scrolled since the position was last read (a resize or a render came first in this frame): their
    // position wins, it is read now rather than undone
    if (this.#seen !== undefined && Math.abs(sc.scrollTop - this.#seen) >= 1) {
      const was = this.stick;
      this.#read();
      if (this.stick !== was) this.#onChange();
      return;
    }
    if (this.stick) {
      const bottom = sc.scrollHeight - sc.clientHeight;
      if (Math.abs(sc.scrollTop - bottom) >= 1) this.#scrollTo(sc.scrollHeight);
      return;
    }
    let a: { key: string; top: number } | undefined;
    let el: HTMLElement | undefined;
    for (const c of this.#anchors) {
      el = this.#rowEl(c.key);
      if (el) {
        a = c;
        break;
      }
    }
    if (!a || !el) return;
    const now = el.getBoundingClientRect().top - (sc.getBoundingClientRect().top + sc.clientTop);
    if (Math.abs(now - a.top) >= 1) this.#scrollTo(sc.scrollTop + now - a.top);
  }

  #scrollTo(top: number): void {
    const sc = this.#sc!;
    const before = sc.scrollTop;
    sc.scrollTop = top;
    if (sc.scrollTop !== before) this.#own = sc.scrollTop;
    this.#seen = sc.scrollTop;
  }

  #resized(entries: ResizeObserverEntry[]): void {
    if (this.#dead) return;
    let any = false;
    for (const e of entries) {
      if (e.target === this.#sc) {
        any = true;
        continue;
      }
      const k = keyOf(e.target);
      if (k !== null && e.target.isConnected && this.#setHeight(k, e.target.getBoundingClientRect().height)) any = true;
    }
    if (!any) return;
    this.#restore();
    if (this.#stale()) this.#onChange();
  }

  // ---- rows entering and leaving

  #listRef = (node: Element, signal: AbortSignal): void => {
    this.#list = node as HTMLElement;
    signal.addEventListener('abort', () => {
      if (this.#list === node) this.#list = undefined;
    });
  };

  #row = (node: Element, signal: AbortSignal): void => {
    this.#ro?.observe(node);
    this.#fresh.add(node);
    signal.addEventListener('abort', () => {
      this.#ro?.unobserve(node);
      this.#fresh.delete(node);
      const k = keyOf(node);
      if (k === null) return;
      const ds = node.querySelectorAll('details');
      if (ds.length) this.#open.set(k, Array.from(ds, (d) => d.open));
      // the focused row left the window: focus goes to the scroller, not to the page
      if (this.#focused && node.contains(this.#focused)) {
        this.#focused = undefined;
        this.#refocus = true;
      }
    });
  };

  #focusIn = (e: FocusEvent): void => {
    this.#focused = e.target instanceof Element ? e.target : undefined;
  };

  #restoreDetails(): void {
    for (const node of this.#fresh) {
      const k = keyOf(node);
      const st = k === null ? undefined : this.#open.get(k);
      if (st) node.querySelectorAll('details').forEach((d, i) => i < st.length && (d.open = st[i]!));
    }
    this.#fresh.clear();
  }

  #dispose(): void {
    this.#dead = true;
    this.#ro?.disconnect();
    if (this.#quietFrame) cancelAnimationFrame(this.#quietFrame);
  }
}

import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { drain } from '../test/render.tsx';
import { sleep, whenVisible } from './wait.ts';

/** Counts the abort listeners that are registered on signals and not removed (a listener that fired once is removed by the browser itself). */
function trackAbortListeners() {
  const live = new Map<unknown, AbortSignal>();
  const add = EventTarget.prototype.addEventListener;
  const remove = EventTarget.prototype.removeEventListener;
  EventTarget.prototype.addEventListener = function (this: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions) {
    if (this instanceof AbortSignal && type === 'abort' && listener) live.set(listener, this);
    return add.call(this, type, listener, options);
  } as typeof add;
  EventTarget.prototype.removeEventListener = function (this: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | EventListenerOptions) {
    if (this instanceof AbortSignal && type === 'abort' && listener) live.delete(listener);
    return remove.call(this, type, listener, options);
  } as typeof remove;
  return {
    get size() {
      return live.size;
    },
    restore() {
      EventTarget.prototype.addEventListener = add;
      EventTarget.prototype.removeEventListener = remove;
    },
  };
}

describe('sleep', () => {
  it('waits for the time and leaves no abort listener behind, however many times it is used on one signal (L47)', async (t) => {
    const timers = t.useFakeTimers();
    const tracker = trackAbortListeners();
    try {
      const signal = new AbortController().signal;
      for (let i = 0; i < 25; i++) {
        let woke = false;
        const p = sleep(1000, signal).then(() => (woke = true));
        await timers.advanceAsync(999);
        await drain();
        expect(woke).toBe(false);
        await timers.advanceAsync(1);
        await p;
        expect(woke).toBe(true);
      }
      expect(tracker.size).toBe(0);
    } finally {
      tracker.restore();
    }
  });

  it('ends at once when the signal aborts, and clears its timer', async (t) => {
    const timers = t.useFakeTimers();
    const ac = new AbortController();
    let woke = false;
    const p = sleep(60_000, ac.signal).then(() => (woke = true));
    await drain();
    expect(woke).toBe(false);
    ac.abort();
    await p;
    expect(woke).toBe(true);
    timers.advance(120_000); // the timer is gone: nothing fires later
    await expect(sleep(5, ac.signal)).resolves.toBeUndefined(); // an aborted signal does not wait at all
  });
});

describe('whenVisible', () => {
  // TEST DOUBLE: a document with a settable visibility
  function fakeDoc(state: 'visible' | 'hidden') {
    const target = new EventTarget();
    const doc = {
      visibilityState: state,
      addEventListener: target.addEventListener.bind(target),
      removeEventListener: target.removeEventListener.bind(target),
      set(s: 'visible' | 'hidden') {
        doc.visibilityState = s;
        target.dispatchEvent(new Event('visibilitychange'));
      },
      listeners: 0,
    };
    const add = doc.addEventListener;
    const remove = doc.removeEventListener;
    doc.addEventListener = ((...a: Parameters<typeof add>) => (doc.listeners++, add(...a))) as typeof add;
    doc.removeEventListener = ((...a: Parameters<typeof remove>) => (doc.listeners--, remove(...a))) as typeof remove;
    return doc;
  }

  it('resolves at once for a visible page', async () => {
    await expect(whenVisible(new AbortController().signal, fakeDoc('visible') as unknown as Document)).resolves.toBeUndefined();
  });

  it('waits while the page is hidden, resumes when it shows, and leaves no listener behind', async () => {
    const tracker = trackAbortListeners();
    try {
      const doc = fakeDoc('hidden');
      const signal = new AbortController().signal;
      let resumed = false;
      const p = whenVisible(signal, doc as unknown as Document).then(() => (resumed = true));
      await drain();
      expect(resumed).toBe(false);
      doc.set('hidden'); // a change that is not showing the page
      await drain();
      expect(resumed).toBe(false);
      doc.set('visible');
      await p;
      expect(resumed).toBe(true);
      expect(doc.listeners).toBe(0);
      expect(tracker.size).toBe(0);
    } finally {
      tracker.restore();
    }
  });

  it('stops waiting when the signal aborts', async () => {
    const doc = fakeDoc('hidden');
    const ac = new AbortController();
    const p = whenVisible(ac.signal, doc as unknown as Document);
    ac.abort();
    await p;
    expect(doc.listeners).toBe(0);
  });
});

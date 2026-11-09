import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { FOCUS_REREAD_MS, rereadOnFocus } from './focus.ts';

// TEST DOUBLES (labelled): a clock the test moves and an event target standing in for the window.
function rig(busy = () => false) {
  const ac = new AbortController();
  const time = { ms: 1_000 };
  const target = new EventTarget();
  let reads = 0;
  rereadOnFocus({ reload: () => void reads++, busy, signal: ac.signal, now: () => time.ms, target });
  const focus = () => target.dispatchEvent(new Event('focus'));
  return { ac, time, focus, reads: () => reads };
}

describe('rereadOnFocus', () => {
  it('reads again on focus once the gap has passed, at most once a minute', () => {
    const r = rig();
    r.focus();
    expect(r.reads()).toBe(0); // the page has just read
    r.time.ms += FOCUS_REREAD_MS;
    r.focus();
    expect(r.reads()).toBe(1);
    r.time.ms += 10_000;
    r.focus();
    expect(r.reads()).toBe(1);
    r.time.ms += FOCUS_REREAD_MS;
    r.focus();
    expect(r.reads()).toBe(2);
  });

  it('starts no read while one is on its way', () => {
    let busy = true;
    const r = rig(() => busy);
    r.time.ms += FOCUS_REREAD_MS;
    r.focus();
    expect(r.reads()).toBe(0);
    busy = false;
    r.focus();
    expect(r.reads()).toBe(1);
  });

  it('stops with the signal', () => {
    const r = rig();
    r.ac.abort();
    r.time.ms += FOCUS_REREAD_MS;
    r.focus();
    expect(r.reads()).toBe(0);
  });
});

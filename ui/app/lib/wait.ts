// Waiting for a poll's next turn without leaving anything behind (audit 2026-10-16, L47). A poll that adds an abort listener to its
// component's signal on every turn and never removes it piles up one listener per turn for as long as the component lives.

/** Resolves after `ms`, or at once when `signal` aborts. The timer and the abort listener are gone either way. */
export function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise<void>((resolve) => {
    if (signal.aborted) return resolve();
    const done = () => {
      clearTimeout(timer);
      signal.removeEventListener('abort', done);
      resolve();
    };
    const timer = setTimeout(done, ms);
    signal.addEventListener('abort', done, { once: true });
  });
}

/**
 * Resolves when the page is visible - at once if it already is - or when `signal` aborts. A window that is minimised or
 * covered (document.visibilityState "hidden") has nobody reading what a poll fetches. Not verified in WebView2 on Windows.
 */
export function whenVisible(signal: AbortSignal, doc: Document = document): Promise<void> {
  return new Promise<void>((resolve) => {
    if (signal.aborted || doc.visibilityState !== 'hidden') return resolve();
    const done = () => {
      doc.removeEventListener('visibilitychange', onChange);
      signal.removeEventListener('abort', done);
      resolve();
    };
    const onChange = () => {
      if (doc.visibilityState !== 'hidden') done();
    };
    doc.addEventListener('visibilitychange', onChange);
    signal.addEventListener('abort', done, { once: true });
  });
}

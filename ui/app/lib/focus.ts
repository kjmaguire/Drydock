// A read again when the window gets focus back (effort review F11). A sign-in or an install done in another window publishes no
// event, so a page that lists what is installed or signed in reads again when the person comes back to Drydock. Some of those
// reads are expensive (connection.list probes every agent CLI afresh), so a focus read happens at most once a minute and never
// while a read is already on its way; the repository configuration banner (shell/repo-config-banner.tsx) reads on every focus.
//
// Pure apart from the clock and the event target it is given.

/** The least time between two reads started by focus. */
export const FOCUS_REREAD_MS = 60_000;
/** The gap the pages use when they pass none; a browser test sets it to 0 (and back) so a focus reads at once. */
export const focusRereadGap = { ms: FOCUS_REREAD_MS };

export interface FocusRereadOptions {
  /** The read. */
  reload: () => unknown;
  /** Whether a read is already on its way (a focus then starts none). */
  busy?: () => boolean;
  /** Ends the listener (the component is gone). */
  signal: AbortSignal;
  /** The least time between two focus reads, in ms. */
  minGapMs?: number;
  /** The clock in ms; replaceable in a test. */
  now?: () => number;
  /** What sends the focus event; the window by default. */
  target?: EventTarget;
}

/**
 * Reads again on focus, at most once per `minGapMs` and not while `busy()`. The page has just read when this is set up, so the
 * first focus read waits for the gap too.
 */
export function rereadOnFocus(o: FocusRereadOptions): void {
  const { reload, busy = () => false, signal, minGapMs, now = () => Date.now(), target = window } = o;
  let last = now();
  target.addEventListener(
    'focus',
    () => {
      const t = now();
      if (t - last < (minGapMs ?? focusRereadGap.ms) || busy()) return;
      last = t;
      void reload();
    },
    { signal },
  );
}

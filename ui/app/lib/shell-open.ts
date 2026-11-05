// A click on a Windows notification (decision G1, app/notify_windows.go) brings the window to the front and opens the item: the
// Inbox, or the task the item is in. Once the page is running, the shell sends the page to open on the Wails event
// "drydock:open" (before that it sets the window's address instead), and this module hands it to the router. Only the Inbox and
// one task's page are opened, the same two the shell itself allows (app/notify.go, safeRoute): a notification's arguments come
// back from Windows, so the page checks them again. This has not been run in WebView2.

/** The Wails event the shell sends a page to open on (app/notify_windows.go, OpenEventName). */
export const SHELL_OPEN_EVENT = 'drydock:open';

const TASK = /^\/task\/[A-Za-z0-9_-]{1,64}$/;

/** The page to open for the shell's event data, or null for anything that is not the Inbox or one task's page. */
export function shellOpenRoute(data: unknown): string | null {
  const d: unknown = Array.isArray(data) ? data[0] : data; // a single Emit argument arrives unwrapped
  if (typeof d !== 'string') return null;
  if (d === '/needs-you' || TASK.test(d)) return d;
  return null;
}

/** Listens for the shell's "open this page" and navigates there; returns the unsubscribe. */
export function installShellOpen(on: (name: string, cb: (ev: { data: unknown }) => void) => () => void, go: (href: string) => void): () => void {
  return on(SHELL_OPEN_EVENT, (ev) => {
    const href = shellOpenRoute(ev?.data);
    if (href) go(href);
  });
}

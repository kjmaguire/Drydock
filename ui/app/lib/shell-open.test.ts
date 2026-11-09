import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { WailsTransport } from '../api/wails.ts';
import { installShellOpen, SHELL_OPEN_EVENT, shellOpenRoute } from './shell-open.ts';

describe('a click on a Windows notification opens the item (G1)', () => {
  it('opens only the Inbox or one task page', () => {
    expect(shellOpenRoute('/needs-you')).toBe('/needs-you');
    expect(shellOpenRoute('/task/task_0a1b2c3d4e5f')).toBe('/task/task_0a1b2c3d4e5f');
    expect(shellOpenRoute(['/needs-you'])).toBe('/needs-you');
    for (const bad of ['/settings', '/task/', '/task/a/b', '/task/x?y=1', 'https://example.com', '//evil', '/needs-you/x', 7, null, undefined, {}]) {
      expect(shellOpenRoute(bad)).toBe(null);
    }
  });

  it('navigates on the shell event and ignores anything else', () => {
    const listeners = new Map<string, (ev: { data: unknown }) => void>();
    let unsubscribed = false;
    const went: string[] = [];
    const off = installShellOpen(
      (name, cb) => {
        listeners.set(name, cb);
        return () => {
          unsubscribed = true;
        };
      },
      (href) => went.push(href),
    );
    const fire = listeners.get(SHELL_OPEN_EVENT)!;
    fire({ data: '/task/task_1' });
    fire({ data: 'javascript:alert(1)' });
    fire({ data: '/needs-you' });
    expect(went).toEqual(['/task/task_1', '/needs-you']);
    off();
    expect(unsubscribed).toBe(true);
  });

  it('listens on the Wails transport for the shell event by its own name', () => {
    // TEST DOUBLE: the Wails runtime's bridge and events
    const names: string[] = [];
    let cb: ((ev: { data: unknown }) => void) | undefined;
    const t = new WailsTransport(
      { Call: async () => '', Subscribe: async () => undefined },
      {
        On: (name, f) => {
          names.push(name);
          cb = f;
          return () => {};
        },
      },
      { heartbeatMs: 0 },
    );
    const went: string[] = [];
    installShellOpen((name, f) => t.onShellEvent(name, f), (href) => went.push(href));
    cb!({ data: '/needs-you' });
    expect(names).toEqual([SHELL_OPEN_EVENT]);
    expect(went).toEqual(['/needs-you']);
  });
});

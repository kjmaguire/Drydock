import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { RpcError } from '../api/transport.ts';
import type { AppInfo } from '../api/types.ts';
import { cleanup, mount, testServices, waitFor } from '../test/render.tsx';
import { coreDown, ErrorNote } from '../ui/feedback.tsx';
import { CoreBanner, coreDownWords } from './core-banner.tsx';

afterEach(() => {
  cleanup();
  coreDown.set(false);
});

function need<T>(v: T | null | undefined, what: string): T {
  if (v === null || v === undefined) throw new Error(`no ${what} yet`);
  return v;
}

function setup(canRestart: boolean) {
  let restarts = 0;
  const s = testServices({
    appInfo: async () => ({ name: 'Drydock', canRestart }) as AppInfo,
    shellRestart: async () => void restarts++,
  });
  s.workspace.start();
  s.store.start();
  s.transport.current.h.onOpen();
  const r = mount(
    <div>
      <CoreBanner afterMs={30} />
      <ErrorNote error={new RpcError('task.list', 'network', 'Failed to fetch')} />
    </div>,
    s,
  );
  const banner = () => r.container.querySelector<HTMLElement>('[data-testid="core-banner"]');
  const button = (text: string) => Array.from(r.container.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent === text);
  return { s, r, banner, button, restarts: () => restarts };
}

describe('CoreBanner (G4)', () => {
  it('says nothing for a break that heals within a few seconds', async () => {
    const { s, r, banner } = setup(true);
    s.transport.current.h.onError();
    s.transport.current.h.onOpen?.(); // a stale handler: ignored by the store
    await new Promise((res) => setTimeout(res, 5));
    expect(banner()).toBe(null);
    s.store.retryNow();
    s.transport.current.h.onOpen();
    await new Promise((res) => setTimeout(res, 60));
    r.root.flush();
    expect(banner()).toBe(null);
  });

  it('after a few seconds down: one banner, the network notes go quiet, Try now reopens the stream, and it leaves when the core answers', async () => {
    const { s, r, banner, button } = setup(false);
    expect(r.container.querySelector('[role="alert"]')!.textContent).toBe("Drydock's core did not answer.");
    s.transport.current.h.onError();
    const el = await waitFor(() => need(banner(), 'banner'), r);
    expect(el.textContent).toContain(coreDownWords('fake'));
    expect(coreDown.down).toBe(true);
    expect(r.container.querySelectorAll('[role="alert"]').length).toBe(1); // the banner only
    expect(button('Restart Drydock...')).toBe(undefined); // this window cannot restart Drydock
    const opens = s.transport.opens.length;
    need(button('Try now'), 'Try now').click();
    expect(s.transport.opens.length).toBe(opens + 1);
    s.transport.current.h.onOpen();
    await waitFor(() => (banner() ? need(null, 'banner gone') : true), r);
    expect(coreDown.down).toBe(false);
    expect(r.container.querySelectorAll('[role="alert"]').length).toBe(1); // the note speaks again
  });

  it('Restart Drydock is asked twice and says what it ends', async () => {
    const t = setup(true);
    await waitFor(() => need(t.s.workspace.info.data, 'info'), t.r);
    t.s.transport.current.h.onError();
    await waitFor(() => need(t.button('Restart Drydock...'), 'restart'), t.r).then((b) => b.click());
    const confirm = await waitFor(() => need(t.r.container.querySelector('[data-testid="restart-confirm"]'), 'confirm'), t.r);
    expect(confirm.textContent).toContain('ends every running agent');
    expect(t.restarts()).toBe(0);
    need(t.button('Cancel'), 'Cancel').click();
    await waitFor(() => need(t.button('Restart Drydock...'), 'restart again'), t.r).then((b) => b.click());
    await waitFor(() => need(t.button('Restart Drydock'), 'confirm button'), t.r).then((b) => b.click());
    await waitFor(() => (t.restarts() === 1 ? true : need(null, 'restart call')), t.r);
  });

  it('words the break by transport', () => {
    expect(coreDownWords('http')).toContain('start it again');
    expect(coreDownWords('wails')).toContain('keeps trying');
  });
});

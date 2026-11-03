import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { SandboxForward, SandboxForwards } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { SandboxApp } from './sandbox-app.tsx';

afterEach(cleanup);

/** The core's forwarding as a labelled test double: what is forwarded is kept and listed sorted by port, the way sandbox.forward.* does. */
function double(opts: { running?: boolean; canView?: boolean; max?: number; listening?: { port: number; name?: string }[] } = {}) {
  const forwards: SandboxForward[] = [];
  const calls: string[] = [];
  /** The ports whose window is open (X314): the viewer's one window per forward. */
  const windows = new Set<number>();
  const view = (): SandboxForwards => ({ running: opts.running ?? true, forwards: [...forwards].sort((a, b) => a.port - b.port), canView: opts.canView ?? true, max: opts.max ?? 5, viewing: [...windows], listening: [...(opts.listening ?? [])] });
  const client: Partial<Client> = {
    sandboxForwards: async () => view(),
    sandboxForwardOpen: async (taskId, port) => {
      calls.push(`open ${port}`);
      const f = { taskId, port, hostPort: 49000 + port, address: `127.0.0.1:${49000 + port}` };
      forwards.push(f);
      return f;
    },
    sandboxForwardView: async (_t, port) => {
      calls.push(`view ${port}`);
      windows.add(port);
    },
    // sandbox.forward.show: forwards (the same forward for a port already forwarded), then opens the viewer
    sandboxForwardShow: async (taskId, port) => {
      calls.push(`show ${port}`);
      let f = forwards.find((x) => x.port === port);
      if (!f) {
        f = { taskId, port, hostPort: 49000 + port, address: `127.0.0.1:${49000 + port}` };
        forwards.push(f);
      }
      windows.add(port);
      return f;
    },
    sandboxForwardClose: async (_t, port) => {
      calls.push(`close ${port}`);
      windows.delete(port);
      forwards.splice(0, forwards.length, ...forwards.filter((f) => f.port !== port));
    },
  };
  return { client, calls, windows };
}

describe("A task's sandbox app (P134)", () => {
  it('a port whose window is open says Show, Open brings it to the front, and Stop closes it (X314)', async () => {
    const { client, calls, windows } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    const add = () => r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement;
    const input = byLabel<HTMLInputElement>(r.container, 'Port in the sandbox');
    await r.act(() => {
      input.value = '3000';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await r.act(() => add().click());
    // the window is open: the field's port and its row say Show
    await waitFor(() => expect(add().textContent).toBe('Show the app'), r);
    const open = () => r.$('[data-testid="sandbox-forward-open-3000"]') as HTMLButtonElement;
    expect(open().textContent).toBe('Show');
    expect(open().getAttribute('title')).toBe('Bring its window to the front');
    // a window the person closed is known at the next read: Show then opens it again
    windows.delete(3000);
    await r.act(() => open().click());
    await waitFor(() => expect(calls).toEqual(['show 3000', 'view 3000']), r);
    await waitFor(() => expect(open().textContent).toBe('Show'), r);
    await r.act(() => (r.$('[data-testid="sandbox-forward-stop-3000"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="sandbox-forwards"]')).toBe(null), r);
    expect(windows.size).toBe(0);
  });

  it('offers the ports the sandbox listens on as one click each, and folds the typed port under Another port (X31)', async () => {
    const { client, calls } = double({ listening: [{ port: 3000, name: 'dev' }, { port: 5432 }] });
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-listening-3000"]')).not.toBe(null), r);
    const chip = r.$('[data-testid="sandbox-listening-3000"]') as HTMLButtonElement;
    expect(chip.textContent).toBe('3000 · dev');
    expect((r.$('[data-testid="sandbox-listening-5432"]') as HTMLButtonElement).textContent).toBe('5432');
    // the typed field is still there, folded
    const other = r.$('[data-testid="sandbox-another-port"]') as HTMLDetailsElement;
    expect(other.open).toBe(false);
    expect(other.querySelector('summary')?.textContent).toBe('Another port...');
    expect(other.contains(byLabel(r.container, 'Port in the sandbox'))).toBe(true);
    await r.act(() => chip.click());
    await waitFor(() => expect(calls).toEqual(['show 3000']), r);
    await waitFor(() => expect(r.$('[data-testid="sandbox-forward-3000"]')).not.toBe(null), r);
  });

  it('a port something starts listening on later gets its button with no sandbox event (X31)', async () => {
    const listening: { port: number; name?: string }[] = [];
    const { client } = double({ listening });
    const r = mount(<SandboxApp taskId="t1" pollMs={50} />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    expect(r.$('[data-testid="sandbox-listening-5173"]')).toBe(null);
    // the dev server binds its port after sandbox_start returned: no event arrives, the next read finds it
    listening.push({ port: 5173, name: 'dev' });
    await waitFor(() => expect((r.$('[data-testid="sandbox-listening-5173"]') as HTMLButtonElement | null)?.textContent).toBe('5173 · dev'), r);
  });

  it('without listening ports the port field is not folded', async () => {
    const { client } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    expect(r.$('[data-testid="sandbox-another-port"]')).toBe(null);
    expect(byLabel(r.container, 'Port in the sandbox')).not.toBe(null);
  });

  it('shows nothing for a task with no running sandbox', async () => {
    const { client } = double({ running: false });
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).toBe(null), r);
    expect(r.container.textContent ?? '').not.toContain('Forward port');
  });

  it('"Open the app" forwards the typed port and opens it in one click, then offers the same port again (X32)', async () => {
    const { client, calls } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    const add = () => r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement;
    expect(add().textContent).toBe('Open the app');
    expect(add().disabled).toBe(true);
    const input = byLabel<HTMLInputElement>(r.container, 'Port in the sandbox');
    await r.act(() => {
      input.value = '3000';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await r.act(() => add().click());
    await waitFor(() => expect(calls).toEqual(['show 3000']), r);
    // the forwarded ports fold under it, and the field holds the port forwarded last: opening it again is one click
    await waitFor(() => expect(r.$('[data-testid="sandbox-forwards"] summary')!.textContent).toBe('Forwarded ports (1)'), r);
    expect((r.$('[data-testid="sandbox-forwards"]') as HTMLDetailsElement).open).toBe(false);
    expect(byLabel<HTMLInputElement>(r.container, 'Port in the sandbox').value).toBe('3000');
    await r.act(() => add().click());
    await waitFor(() => expect(calls).toEqual(['show 3000', 'show 3000']), r);
  });

  it('keeps the port opened last in the field, not the highest one forwarded (X32)', async () => {
    const { client, calls } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    const add = () => r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement;
    const type = (v: string) =>
      r.act(() => {
        const input = byLabel<HTMLInputElement>(r.container, 'Port in the sandbox');
        input.value = v;
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
    await type('8080');
    await r.act(() => add().click());
    await waitFor(() => expect(r.$('[data-testid="sandbox-forwards"] summary')!.textContent).toBe('Forwarded ports (1)'), r);
    await type('3000');
    await r.act(() => add().click());
    await waitFor(() => expect(r.$('[data-testid="sandbox-forwards"] summary')!.textContent).toBe('Forwarded ports (2)'), r);
    // the core lists 3000 before 8080; the field still holds 3000, the one just opened
    expect(byLabel<HTMLInputElement>(r.container, 'Port in the sandbox').value).toBe('3000');
    await r.act(() => add().click());
    await waitFor(() => expect(calls).toEqual(['show 8080', 'show 3000', 'show 3000']), r);
    // once it is stopped, the field falls back to what is still forwarded
    await r.act(() => (r.$('[data-testid="sandbox-forward-stop-3000"]') as HTMLButtonElement).click());
    await waitFor(() => expect(byLabel<HTMLInputElement>(r.container, 'Port in the sandbox').value).toBe('8080'), r);
  });

  it('forwards a port, opens it in the Drydock window and stops it', async () => {
    const { client, calls } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    expect((r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement).disabled).toBe(true);
    const input = byLabel<HTMLInputElement>(r.container, 'Port in the sandbox');
    await r.act(() => {
      input.value = '3000';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await r.act(() => (r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('[data-testid="sandbox-forward-3000"]')!.textContent).toContain('127.0.0.1:52000'), r);
    await r.act(() => (r.$('[data-testid="sandbox-forward-open-3000"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual(['show 3000', 'view 3000']), r);
    await r.act(() => (r.$('[data-testid="sandbox-forward-stop-3000"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual(['show 3000', 'view 3000', 'close 3000']), r);
    await waitFor(() => expect(r.$('[data-testid="sandbox-forward-3000"]')).toBe(null), r);
  });

  it('refuses a port that is not a port before asking the core', async () => {
    const { client, calls } = double();
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).not.toBe(null), r);
    const input = byLabel<HTMLInputElement>(r.container, 'Port in the sandbox');
    await r.act(() => {
      input.value = '70000';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await r.act(() => (r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.container.textContent).toContain('Enter a port from 1 to 65535.'), r);
    expect(calls).toEqual([]);
  });

  it('cannot open the app in a window that has no viewer, and says it never falls back to a browser', async () => {
    const { client } = double({ canView: false });
    await client.sandboxForwardOpen!('t1', 3000);
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-forward-open-3000"]')).not.toBe(null), r);
    expect((r.$('[data-testid="sandbox-forward-open-3000"]') as HTMLButtonElement).disabled).toBe(true);
    expect(r.container.textContent).toContain('use the Drydock desktop app');
    expect(byText(r.container, 'button', 'Stop')).not.toBe(null);
    // without a viewer the button only forwards
    expect((r.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement).textContent).toBe('Forward port');
  });

  it('stops offering new ports at the limit; with a viewer the forwarded one still opens', async () => {
    const { client } = double({ max: 1, canView: false });
    await client.sandboxForwardOpen!('t1', 3000);
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.container.textContent).toContain('1 ports are forwarded, the most a task can have'), r);
    expect(r.$('[data-testid="sandbox-forward-add"]')).toBe(null);
    const viewer = double({ max: 1 });
    await viewer.client.sandboxForwardOpen!('t1', 3000);
    const r2 = mount(<SandboxApp taskId="t1" />, testServices(viewer.client));
    await waitFor(() => expect((r2.$('[data-testid="sandbox-forward-add"]') as HTMLButtonElement).disabled).toBe(false), r2);
    expect(byLabel<HTMLInputElement>(r2.container, 'Port in the sandbox').value).toBe('3000');
  });
});

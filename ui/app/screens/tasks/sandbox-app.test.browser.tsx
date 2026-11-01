import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { SandboxForward, SandboxForwards } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { SandboxApp } from './sandbox-app.tsx';

afterEach(cleanup);

/** The core's forwarding as a labelled test double: what is forwarded is kept and listed, the way sandbox.forward.* does. */
function double(opts: { running?: boolean; canView?: boolean; max?: number } = {}) {
  const forwards: SandboxForward[] = [];
  const calls: string[] = [];
  const view = (): SandboxForwards => ({ running: opts.running ?? true, forwards: [...forwards], canView: opts.canView ?? true, max: opts.max ?? 5 });
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
    },
    sandboxForwardClose: async (_t, port) => {
      calls.push(`close ${port}`);
      forwards.splice(0, forwards.length, ...forwards.filter((f) => f.port !== port));
    },
  };
  return { client, calls };
}

describe("A task's sandbox app (P134)", () => {
  it('shows nothing for a task with no running sandbox', async () => {
    const { client } = double({ running: false });
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.$('[data-testid="sandbox-app"]')).toBe(null), r);
    expect(r.container.textContent ?? '').not.toContain('Forward port');
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
    await waitFor(() => expect(calls).toEqual(['open 3000', 'view 3000']), r);
    await r.act(() => (r.$('[data-testid="sandbox-forward-stop-3000"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual(['open 3000', 'view 3000', 'close 3000']), r);
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
  });

  it('stops offering ports at the limit', async () => {
    const { client } = double({ max: 1 });
    await client.sandboxForwardOpen!('t1', 3000);
    const r = mount(<SandboxApp taskId="t1" />, testServices(client));
    await waitFor(() => expect(r.container.textContent).toContain('1 ports are forwarded, the most a task can have'), r);
    expect(r.$('[data-testid="sandbox-forward-add"]')).toBe(null);
  });
});

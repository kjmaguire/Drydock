import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { SandboxStatus } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { SandboxPanel, sandboxReady, sandboxSummary } from './project-sandbox.tsx';

afterEach(cleanup);

// waitFor retries what throws: a missing element is an error until it appears
const need = <T,>(x: T | null): T => {
  if (x === null) throw new Error('not there yet');
  return x;
};

const engine = { client: 'docker', version: '29.8.2', os: 'linux', arch: 'amd64' };
const ready: SandboxStatus = {
  switch: { enabled: false },
  engine,
  found: true,
  branch: 'main',
  configPath: '.drydock/sandbox.json',
  config: { image: 'node:24-bookworm', setup: ['npm ci'], services: { db: { image: 'postgres:17' } }, network: 'allowlist' },
  hosts: ['registry.npmjs.org', 'api.stripe.com'],
};

function panel(client: Partial<Client>) {
  const s = testServices(client);
  return mount(<SandboxPanel projectId="p1" />, s);
}

describe('Sandbox settings', () => {
  it('summarises a definition and says when the switch can go on', () => {
    expect(sandboxSummary(ready)).toBe('image node:24-bookworm; services db; network allowlist of 2 hosts; setup: npm ci');
    expect(sandboxReady(ready)).toBe(true);
    expect(sandboxReady({ ...ready, engine: undefined, engineError: 'no container engine is reachable' })).toBe(false);
    expect(sandboxReady({ ...ready, found: false })).toBe(false);
    expect(sandboxReady({ ...ready, egressError: 'the network allowlist needs drydock-egress-linux-amd64' })).toBe(false);
  });

  it('a project without a definition is shown one to start from, and cannot switch the sandbox on yet', async () => {
    const r = panel({
      sandboxStatus: async () => ({
        switch: { enabled: false },
        engine,
        found: false,
        branch: 'main',
        configPath: '.drydock/sandbox.json',
        configError: 'the project has no .drydock/sandbox.json on main: add one (the project settings suggest a starting point)',
        starter: '{\n  "image": "node:24-bookworm"\n}',
      }),
    });
    const starter = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-starter"]')), r);
    expect(starter.textContent).toContain('"image": "node:24-bookworm"');
    expect(starter.textContent).toContain('on main');
    expect(r.container.querySelector('[data-testid="sandbox-definition"]')!.textContent).toContain('has no .drydock/sandbox.json');
    expect(byLabel<HTMLInputElement>(r.container, "Run the agents' commands in a container for each task").disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Test the sandbox').disabled).toBe(true);
  });

  it('without a container engine it says how to get one', async () => {
    const r = panel({ sandboxStatus: async () => ({ ...ready, engine: undefined, engineError: 'no container engine is reachable: docker is not installed or not on PATH' }) });
    const e = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-engine"]')), r);
    expect(e.textContent).toContain('not installed');
    expect(e.textContent).toContain('Install Docker Desktop');
  });

  it('switches on through the core and then says who did it', async () => {
    const calls: [string, boolean][] = [];
    let current = ready;
    const r = panel({
      sandboxStatus: async () => current,
      sandboxSet: async (p, on) => {
        calls.push([p, on]);
        current = { ...ready, switch: { enabled: on, by: 'human:kyle', at: Date.UTC(2026, 9, 10, 12, 0) } };
        return current.switch;
      },
    });
    const box = await waitFor(() => byLabel<HTMLInputElement>(r.container, "Run the agents' commands in a container for each task"), r);
    expect(r.container.querySelector('[data-testid="sandbox-definition"]')!.textContent).toContain('.drydock/sandbox.json on main: image node:24-bookworm');
    expect(box.disabled).toBe(false);
    await r.act(() => box.click());
    await waitFor(() => expect(calls).toEqual([['p1', true]]), r);
    await waitFor(() => expect(r.container.querySelector('[data-testid="sandbox-switch-note"]')!.textContent).toContain('Switched on by kyle'), r);
    expect(r.container.querySelector('[data-testid="sandbox-explain"]')!.textContent).toContain('no shell on this machine');
  });

  it('runs the test and shows each step', async () => {
    const r = panel({
      sandboxStatus: async () => ready,
      sandboxTest: async () => ({
        steps: [
          { name: 'container engine', ok: true, detail: 'docker 29.8.2 (linux/amd64)', durationMs: 40 },
          { name: 'start (pulls missing images, starts the services)', ok: false, detail: 'could not get image postgres:17', durationMs: 2300 },
        ],
      }),
    });
    const btn = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', 'Test the sandbox'), r);
    await r.act(() => btn.click());
    const steps = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-steps"]')), r);
    const items = Array.from(steps.querySelectorAll('li'));
    expect(items.map((li) => li.getAttribute('data-ok'))).toEqual(['true', 'false']);
    expect(items[1]!.textContent).toContain('could not get image postgres:17');
  });
});

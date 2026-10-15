import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RunnerKindName, RunnerSlot, RunnersView } from '../../api/runnersClient.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { CiRunners, gb, runnerStateText } from './ci-runners.tsx';

afterEach(cleanup);

const GiB = 2 ** 30;
const slot = (over: Partial<RunnerSlot>): RunnerSlot => ({
  key: 'kjmaguire/shop:linux',
  repo: 'kjmaguire/shop',
  kind: 'linux',
  enabled: true,
  state: 'idle',
  jobs: 0,
  since: '2026-10-15T02:00:00Z',
  by: 'human:kyle',
  ...over,
});
const view = (over: Partial<RunnersView> = {}): RunnersView => ({
  kinds: [
    { kind: 'windows', available: true, where: 'On this PC, as you, in a fresh folder for each job', freeBytes: 120 * GiB, runsOn: '[self-hosted, Windows, drydock]' },
    { kind: 'linux', available: true, where: 'In a fresh Linux container for each job (Docker Desktop, WSL 2)', runsOn: '[self-hosted, Linux, drydock]' },
  ],
  root: 'D:\\DrydockRunners',
  rootSource: 'devdrive',
  devDrives: [{ letter: 'D:', trusted: true }],
  slots: [],
  minFreeBytes: 10 * GiB,
  max: 8,
  ...over,
});

function show(client: Partial<Client>) {
  const s = testServices({ licenseStatus: async () => ({}) as never, appInfo: async () => ({}) as never, projectList: async () => [], ...client });
  const r = mount(<CiRunners />, s);
  return { r, s };
}

const q = (r: ReturnType<typeof show>['r'], sel: string) => r.$(sel) as HTMLElement | null;

describe('CI runners', () => {
  it('shows where jobs can run, the Dev Drive folder, each runner with its state, and that only private repositories are served', async () => {
    const { r } = show({
      runnersGet: async () =>
        view({
          kinds: [
            view().kinds[0]!,
            { kind: 'linux', available: false, reason: 'No Linux container engine answered: install Docker Desktop.', where: 'In a fresh Linux container', runsOn: '[self-hosted, Linux, drydock]' },
          ],
          slots: [
            slot({ key: 'kjmaguire/shop:windows', kind: 'windows', state: 'busy', job: 'build', jobs: 2, lastJob: { name: 'test', result: 'Succeeded', at: '' }, version: '2.330.0' }),
            slot({ key: 'kjmaguire/shop:linux', state: 'blocked', reason: 'No Linux container engine answered.' }),
          ],
        }),
    });
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(2), r);
    const win = q(r, '[data-testid="runner-kind"][data-kind="windows"]')!;
    expect(win.textContent).toContain('120 GB free');
    expect(win.textContent).toContain('runs-on: [self-hosted, Windows, drydock]');
    const lin = q(r, '[data-testid="runner-kind"][data-kind="linux"]')!;
    expect(lin.getAttribute('data-available')).toBe('false');
    expect(lin.textContent).toContain('install Docker Desktop');
    expect(q(r, '[data-testid="runner-root"]')!.textContent).toContain('D:\\DrydockRunners, on your Dev Drive D: (trusted');
    expect(q(r, '[data-testid="runner-private-only"]')!.textContent).toContain('Private repositories only');
    const busy = q(r, '[data-testid="runner-slot"][data-key="kjmaguire/shop:windows"]')!;
    expect(busy.querySelector('[data-testid="runner-state"]')!.textContent).toBe('running build');
    expect(busy.querySelector('[data-testid="runner-what"]')!.textContent).toContain('Last job: test, succeeded. 2 jobs since Drydock started. Runner 2.330.0.');
    const blocked = q(r, '[data-testid="runner-slot"][data-key="kjmaguire/shop:linux"]')!;
    expect(blocked.querySelector('[data-testid="runner-state"]')!.textContent).toBe('cannot run now');
    expect(blocked.textContent).toContain('No Linux container engine answered.');
    // the add form offers only what is available
    expect((q(r, '[data-testid="runner-kind-linux"]') as HTMLInputElement).disabled).toBe(true);
    // the Windows folder cannot change under a running Windows runner
    expect((q(r, '[data-testid="runner-root-input"]') as HTMLInputElement).disabled).toBe(true);
  });

  it('adds a Linux runner, and a Windows one only after the person accepts that its jobs run as them', async () => {
    const added: [string, RunnerKindName, boolean][] = [];
    let state = view();
    const { r } = show({
      runnersGet: async () => state,
      runnersAdd: async (repo, kind, accept) => {
        added.push([repo, kind, accept]);
        state = view({ slots: [...state.slots, slot({ key: `${repo}:${kind}`, repo, kind, state: 'starting' })] });
        return state;
      },
    });
    await waitFor(() => expect(q(r, '[data-testid="runner-add"]')).not.toBe(null), r);
    const addBtn = () => q(r, '[data-testid="runner-add-button"]') as HTMLButtonElement;
    expect(addBtn().disabled).toBe(true);
    type(q(r, '[data-testid="runner-repo"]') as HTMLInputElement, 'kjmaguire/shop');
    await r.act(() => (q(r, '[data-testid="runner-kind-linux"]') as HTMLInputElement).click());
    await waitFor(() => expect(addBtn().disabled).toBe(false), r);
    await r.act(() => addBtn().click());
    await waitFor(() => expect(added).toEqual([['kjmaguire/shop', 'linux', false]]), r);
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(1), r);
    expect((q(r, '[data-testid="runner-repo"]') as HTMLInputElement).value).toBe('');

    type(q(r, '[data-testid="runner-repo"]') as HTMLInputElement, 'kjmaguire/desktop-app');
    await r.act(() => (q(r, '[data-testid="runner-kind-windows"]') as HTMLInputElement).click());
    await waitFor(() => expect(q(r, '[data-testid="runner-add"]')!.textContent).toContain('run on this PC as me'), r);
    expect(addBtn().disabled).toBe(true);
    const accept = Array.from(q(r, '[data-testid="runner-add"]')!.querySelectorAll('input[type="checkbox"]'))[0] as HTMLInputElement;
    await r.act(() => accept.click());
    await waitFor(() => expect(addBtn().disabled).toBe(false), r);
    await r.act(() => addBtn().click());
    await waitFor(() => expect(added[1]).toEqual(['kjmaguire/desktop-app', 'windows', true]), r);
  });

  it('switches a runner off, shows its output, removes it, and reloads when the core says a runner changed', async () => {
    const calls: string[] = [];
    let state = view({ slots: [slot({ state: 'idle' })] });
    const { r, s } = show({
      runnersGet: async () => state,
      runnersEnable: async (repo, kind, on) => {
        calls.push(`enable ${repo} ${kind} ${on}`);
        state = view({ slots: [slot({ enabled: on, state: on ? 'starting' : 'paused' })] });
        return state;
      },
      runnersLog: async () => ['2026-10-15 02:00:00Z: Listening for Jobs'],
      runnersRemove: async (repo, kind) => {
        calls.push(`remove ${repo} ${kind}`);
        state = view();
        return state;
      },
    });
    const one = () => q(r, '[data-testid="runner-slot"]');
    await waitFor(() => expect(one()?.getAttribute('data-state')).toBe('idle'), r);
    const toggle = one()!.querySelector('input[type="checkbox"]') as HTMLInputElement;
    await r.act(() => toggle.click());
    await waitFor(() => expect(one()?.getAttribute('data-state')).toBe('paused'), r);
    expect(calls).toEqual(['enable kjmaguire/shop linux false']);

    await r.act(() => (one()!.querySelector('[data-testid="runner-log"]') as HTMLButtonElement).click());
    await waitFor(() => expect(q(r, '[data-testid="runner-output"]')?.textContent).toContain('Listening for Jobs'), r);

    // the core reports a change: the list is read again
    state = view({ slots: [slot({ state: 'busy', job: 'lint' })] });
    s.emit({ type: 'runners.changed', data: { key: 'kjmaguire/shop:linux', state: 'busy' } as never });
    await waitFor(() => expect(one()?.querySelector('[data-testid="runner-state"]')?.textContent).toBe('running lint'), r);

    await r.act(() => (one()!.querySelector('[data-testid="runner-remove"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(0), r);
    expect(calls[1]).toBe('remove kjmaguire/shop linux');
  });

  it('chooses the folder for Windows jobs and goes back to automatic', async () => {
    const roots: string[] = [];
    let state = view({ rootSource: 'default', root: 'C:\\Users\\kyle\\AppData\\Local\\Drydock\\runners\\work', devDrives: [] });
    const { r } = show({
      runnersGet: async () => state,
      runnersRootSet: async (path) => {
        roots.push(path);
        state = path ? view({ root: path, rootSource: 'chosen' }) : view({ rootSource: 'default', root: 'C:\\x', devDrives: [] });
        return state;
      },
    });
    await waitFor(() => expect(q(r, '[data-testid="runner-root"]')?.textContent).toContain('no Dev Drive was found'), r);
    type(q(r, '[data-testid="runner-root-input"]') as HTMLInputElement, 'E:\\ci');
    const save = await waitFor(() => {
      const b = q(r, '[data-testid="runner-root-save"]') as HTMLButtonElement | null;
      if (!b || b.disabled) throw new Error('not yet');
      return b;
    }, r);
    await r.act(() => save.click());
    await waitFor(() => expect(q(r, '[data-testid="runner-root"]')?.textContent).toContain('E:\\ci, the folder you chose'), r);
    await r.act(() => (q(r, '[data-testid="runner-root-reset"]') as HTMLButtonElement).click());
    await waitFor(() => expect(roots).toEqual(['E:\\ci', '']), r);
  });

  it('words', () => {
    expect(gb(120 * GiB)).toBe('120 GB');
    expect(gb(3.25 * GiB)).toBe('3.3 GB');
    expect(runnerStateText(slot({ state: 'error' })).tone).toBe('err');
    expect(runnerStateText(slot({ state: 'paused' })).text).toBe('off');
  });
});

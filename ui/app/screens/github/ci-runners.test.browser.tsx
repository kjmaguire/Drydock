import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { RunnerKindName, RunnerSlot, RunnersView } from '../../api/runnersClient.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { CiRunners, gb, runnerStateText, runnerWhat, sentence } from './ci-runners.tsx';

afterEach(cleanup);

// TEST DOUBLES only: a partial client answering with the shapes runners.* returns. Nothing here runs a runner or reaches GitHub.

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
type R = ReturnType<typeof show>['r'];

const q = (r: R, sel: string) => r.$(sel) as HTMLElement | null;
const ready = <T extends HTMLElement>(r: R, sel: string) =>
  waitFor(() => {
    const el = q(r, sel) as T | null;
    if (!el || (el as unknown as HTMLButtonElement).disabled) throw new Error(`no ${sel} yet`);
    return el;
  }, r);

/** Opens the add dialog and returns its form. */
async function openAdd(r: R): Promise<HTMLElement> {
  const open = await ready<HTMLButtonElement>(r, '[data-testid="runner-add-open"]');
  await r.act(() => open.click());
  return waitFor(() => {
    const f = q(r, 'dialog[open] [data-testid="runner-add"]');
    if (!f) throw new Error('the dialog is not open');
    return f;
  }, r);
}

/** Opens a runner's menu and picks an item by its words. */
async function pick(r: R, row: HTMLElement, label: string): Promise<void> {
  const trigger = row.querySelector('[data-testid="runner-menu"]') as HTMLButtonElement;
  // a chosen item flashes and its menu fades before it takes a click again: wait until the last one has closed
  await waitFor(() => {
    if (trigger.getAttribute('aria-expanded') === 'true' || !row.querySelector('[data-close-animation="none"]')) throw new Error('the menu is still closing');
  }, r);
  await r.act(() => trigger.click());
  await waitFor(() => {
    if (trigger.getAttribute('aria-expanded') !== 'true') throw new Error(`the menu did not open for ${label}`);
  }, r);
  const item = Array.from(row.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((i) => i.textContent?.includes(label));
  if (!item) throw new Error(`no menu item ${label}`);
  await r.act(() => item.click());
}

describe('CI runners', () => {
  it('shows where jobs can run as chips, the Dev Drive folder, each runner with its state in words, and that only private repositories are served', async () => {
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
    expect(win.textContent).toBe('Windows · 120 GB free');
    expect(win.getAttribute('data-available')).toBe('true');
    expect(win.getAttribute('title')).toContain('runs-on: [self-hosted, Windows, drydock]');
    const lin = q(r, '[data-testid="runner-kind"][data-kind="linux"]')!;
    expect(lin.getAttribute('data-available')).toBe('false');
    expect(lin.textContent).toBe('Linux · not available');
    // why it is not available, in the core's words, on a line of its own
    expect(q(r, '[data-testid="runner-kind-reason"][data-kind="linux"]')!.textContent).toBe('Linux: No Linux container engine answered: install Docker Desktop.');
    expect(q(r, '[data-testid="runner-kind-reason"][data-kind="windows"]')).toBeNull();
    expect(q(r, '[data-testid="runner-root"]')!.textContent).toContain('D:\\DrydockRunners, on your Dev Drive D: (trusted');
    // the rule as one line, with the Guide one link away
    const rule = q(r, '[data-testid="runner-private-only"]')!;
    expect(rule.textContent).toContain('Private repositories only');
    expect(rule.querySelector('a[data-testid="learn-more"]')!.getAttribute('href')).toBe('/guide/runners');
    // the runners are a table: repository, where it runs, its state in words, what it did
    expect(q(r, 'table[data-testid="runner-slots"]')!.getAttribute('aria-label')).toBe('Your runners');
    expect(Array.from(r.$$('table[data-testid="runner-slots"] th')).map((th) => th.textContent)).toEqual(['Repository', 'Runs on', 'Status', 'Last job', 'On and actions']);
    const busy = q(r, '[data-testid="runner-slot"][data-key="kjmaguire/shop:windows"]')!;
    expect(busy.querySelector('[data-testid="runner-state"]')!.textContent).toBe('Running build');
    expect(busy.querySelector('[data-testid="runner-state"]')!.getAttribute('data-tone')).toBe('accent');
    expect(busy.querySelector('[data-testid="runner-what"]')!.textContent).toBe('Last job: test, succeeded. 2 jobs since Drydock started. Runner 2.330.0.');
    const blocked = q(r, '[data-testid="runner-slot"][data-key="kjmaguire/shop:linux"]')!;
    expect(blocked.querySelector('[data-testid="runner-state"]')!.textContent).toBe('Cannot run now');
    expect(blocked.textContent).toContain('No Linux container engine answered.');
    // the Windows folder cannot change under a running Windows runner
    expect((q(r, '[data-testid="runner-root-input"]') as HTMLInputElement).disabled).toBe(true);
    // the add form is not on the page until Add runner opens it; there it offers only what is available
    expect(q(r, '[data-testid="runner-add"]')).toBeNull();
    const form = await openAdd(r);
    expect((form.querySelector('[data-testid="runner-kind-linux"]') as HTMLInputElement).disabled).toBe(true);
  });

  it('adds a Linux runner from the dialog, and a Windows one only after the person accepts that its jobs run as them', async () => {
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
    await waitFor(() => expect(q(r, '[data-testid="runner-slots"]')?.textContent).toBe('No runners yet. Add one for a private repository.'), r);
    let form = await openAdd(r);
    expect(r.$('dialog[open]')!.getAttribute('aria-labelledby')).toBeTruthy();
    expect(r.$('dialog[open] h2')!.textContent).toBe('Add a runner');
    const addBtn = () => q(r, '[data-testid="runner-add-button"]') as HTMLButtonElement;
    expect(addBtn().disabled).toBe(true);
    type(form.querySelector('[data-testid="runner-repo"]') as HTMLInputElement, 'kjmaguire/shop');
    await r.act(() => (form.querySelector('[data-testid="runner-kind-linux"]') as HTMLInputElement).click());
    // what a workflow writes to use it, for the kind chosen
    await waitFor(() => expect(q(r, '[data-testid="runner-runs-on"]')?.textContent).toContain('runs-on: [self-hosted, Linux, drydock]'), r);
    await waitFor(() => expect(addBtn().disabled).toBe(false), r);
    await r.act(() => addBtn().click());
    await waitFor(() => expect(added).toEqual([['kjmaguire/shop', 'linux', false]]), r);
    // added: the dialog closes and the runner is listed
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(1), r);
    expect(q(r, '[data-testid="runner-slot"] [data-testid="runner-state"]')!.textContent).toBe('Getting ready');

    form = await openAdd(r);
    expect((form.querySelector('[data-testid="runner-repo"]') as HTMLInputElement).value).toBe('');
    type(form.querySelector('[data-testid="runner-repo"]') as HTMLInputElement, 'kjmaguire/desktop-app');
    await r.act(() => (form.querySelector('[data-testid="runner-kind-windows"]') as HTMLInputElement).click());
    await waitFor(() => expect(q(r, '[data-testid="runner-add"]')!.textContent).toContain('run on this PC as me'), r);
    expect(addBtn().disabled).toBe(true);
    const accept = Array.from(q(r, '[data-testid="runner-add"]')!.querySelectorAll('input[type="checkbox"]'))[0] as HTMLInputElement;
    await r.act(() => accept.click());
    await waitFor(() => expect(addBtn().disabled).toBe(false), r);
    await r.act(() => addBtn().click());
    await waitFor(() => expect(added[1]).toEqual(['kjmaguire/desktop-app', 'windows', true]), r);
  });

  it('keeps the dialog open with the core\'s refusal, and Add runner is off when nothing can run or the runners are full', async () => {
    const { r } = show({
      runnersGet: async () => view(),
      runnersAdd: async () => {
        throw new Error('e2e/public-site is public: a fork could run code on this PC');
      },
    });
    const form = await openAdd(r);
    type(form.querySelector('[data-testid="runner-repo"]') as HTMLInputElement, 'e2e/public-site');
    await r.act(() => (form.querySelector('[data-testid="runner-kind-linux"]') as HTMLInputElement).click());
    await r.act(() => (q(r, '[data-testid="runner-add-button"]') as HTMLButtonElement).click());
    await waitFor(() => expect(q(r, 'dialog[open] [role="alert"]')?.textContent).toContain('a fork could run code'), r);
    expect(r.$$('[data-testid="runner-slot"]').length).toBe(0);
    // closing the dialog takes the refusal with it
    await r.act(() => (r.$('dialog[open] button[aria-label="Close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$('dialog[open]')).toBeNull(), r);
    expect(r.$('[role="alert"]')).toBeNull();

    cleanup();
    const none = show({ runnersGet: async () => view({ kinds: view().kinds.map((k) => ({ ...k, available: false, reason: 'not on this PC' })) }) });
    await waitFor(() => expect(q(none.r, '[data-testid="runner-add-open"]')).not.toBeNull(), none.r);
    await waitFor(() => expect(q(none.r, '[data-testid="runner-kind-reason"]')).not.toBeNull(), none.r);
    expect((q(none.r, '[data-testid="runner-add-open"]') as HTMLButtonElement).disabled).toBe(true);

    cleanup();
    const full = show({ runnersGet: async () => view({ max: 1, slots: [slot({})] }) });
    await waitFor(() => expect(full.r.$$('[data-testid="runner-slot"]').length).toBe(1), full.r);
    expect((q(full.r, '[data-testid="runner-add-open"]') as HTMLButtonElement).disabled).toBe(true);
    expect(q(full.r, '[data-testid="runner-add-open"]')!.getAttribute('title')).toBe('At most 1 runners');
  });

  it('switches a runner off, shows its output from the menu, removes it, and reloads when the core says a runner changed', async () => {
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
    expect(toggle.closest('label')!.textContent).toBe('On');
    await r.act(() => toggle.click());
    await waitFor(() => expect(one()?.getAttribute('data-state')).toBe('paused'), r);
    expect(one()!.querySelector('[data-testid="runner-state"]')!.textContent).toBe('Off');
    expect(calls).toEqual(['enable kjmaguire/shop linux false']);

    // the menu is named after the runner it acts on
    expect(one()!.querySelector('[data-testid="runner-menu"]')!.getAttribute('aria-label')).toBe('Actions for the Linux runner of kjmaguire/shop');
    await pick(r, one()!, 'Show output');
    await waitFor(() => expect(q(r, '[data-testid="runner-slot"] [data-testid="runner-output"]')?.textContent).toContain('Listening for Jobs'), r);
    await pick(r, one()!, 'Hide output');
    await waitFor(() => expect(q(r, '[data-testid="runner-output"]')).toBeNull(), r);

    // the core reports a change: the list is read again
    state = view({ slots: [slot({ state: 'busy', job: 'lint' })] });
    s.emit({ type: 'runners.changed', data: { key: 'kjmaguire/shop:linux', state: 'busy' } as never });
    await waitFor(() => expect(one()?.querySelector('[data-testid="runner-state"]')?.textContent).toBe('Running lint'), r);

    await pick(r, one()!, 'Remove');
    await r.act(() => (one()!.querySelector('[data-testid="runner-remove-confirm"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(0), r);
    expect(calls[1]).toBe('remove kjmaguire/shop linux');
  });

  // audit 2026-10-16, L40: removing a runner takes it away until it is set up again, so it asks first
  it('removing a runner asks first: the menu sends nothing, Keep cancels, Yes removes it once', async () => {
    const calls: string[] = [];
    let state = view({ slots: [slot({ state: 'idle' }), slot({ key: 'kjmaguire/shop:windows', kind: 'windows', state: 'idle' })] });
    let answer: () => void = () => {};
    const { r } = show({
      runnersGet: async () => state,
      runnersRemove: async (repo, kind) => {
        calls.push(`remove ${repo} ${kind}`);
        await new Promise<void>((res) => (answer = res));
        state = view({ slots: state.slots.filter((x) => x.kind !== kind) });
        return state;
      },
    });
    const row = (kind: string) => q(r, `[data-testid="runner-slot"][data-key="kjmaguire/shop:${kind}"]`)!;
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(2), r);
    await pick(r, row('linux'), 'Remove');
    // only that runner asks; the words name the kind and the repository, and Keep has the focus
    expect(calls).toEqual([]);
    expect(row('windows').querySelector('[data-testid="runner-menu"]')).not.toBe(null);
    const group = await waitFor(() => row('linux').querySelector('[role="group"]')!, r);
    expect(group.getAttribute('aria-label')).toBe('Remove the Linux runner of kjmaguire/shop? It stops taking jobs, and adding it again sets it up from the start.');
    const yes = () => row('linux').querySelector('[data-testid="runner-remove-confirm"]') as HTMLButtonElement;
    expect(yes().getAttribute('aria-label')).toBe('Yes, remove the Linux runner of kjmaguire/shop');
    const keep = Array.from(row('linux').querySelectorAll('button')).find((b) => b.getAttribute('aria-label') === 'Keep the Linux runner of kjmaguire/shop') as HTMLButtonElement;
    await waitFor(() => expect(document.activeElement).toBe(keep), r);
    await r.act(() => keep.click());
    await waitFor(() => expect(row('linux').querySelector('[data-testid="runner-menu"]')).not.toBe(null), r);
    await waitFor(() => expect(document.activeElement).toBe(row('linux').querySelector('[data-testid="runner-menu"]')), r);
    expect(calls).toEqual([]);
    await pick(r, row('linux'), 'Remove');
    await r.act(() => {
      yes().click();
      yes().click();
    });
    await waitFor(() => expect(yes().disabled).toBe(true), r);
    answer();
    await waitFor(() => expect(calls).toEqual(['remove kjmaguire/shop linux']), r);
    await waitFor(() => expect(r.$$('[data-testid="runner-slot"]').length).toBe(1), r);
    expect(row('windows')).not.toBe(null);
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
    // one settings row: its label, the folder, the controls
    expect(q(r, '[data-testid="runner-root"] [data-part="label"]')!.textContent).toBe('Windows jobs folder');
    type(q(r, '[data-testid="runner-root-input"]') as HTMLInputElement, 'E:\\ci');
    const save = await ready<HTMLButtonElement>(r, '[data-testid="runner-root-save"]');
    await r.act(() => save.click());
    await waitFor(() => expect(q(r, '[data-testid="runner-root"]')?.textContent).toContain('E:\\ci, the folder you chose'), r);
    await r.act(() => (q(r, '[data-testid="runner-root-reset"]') as HTMLButtonElement).click());
    await waitFor(() => expect(roots).toEqual(['E:\\ci', '']), r);
  });

  it('says why no Dev Drive was found, in the core\'s words', async () => {
    const reason = 'the Dev Drive check is not available on this Windows version (the volume state query for D: failed: The parameter is incorrect.), and fsutil devdrv query, which needs administrator rights, said "Error:  Access is denied."';
    const { r } = show({ runnersGet: async () => view({ rootSource: 'default', root: 'C:\\Users\\kyle\\AppData\\Local\\Drydock\\runners\\work', devDrives: [], devDriveReason: reason }) });
    const line = await waitFor(() => {
      const el = q(r, '[data-testid="runner-devdrive-reason"]');
      if (!el) throw new Error('not yet');
      return el;
    }, r);
    expect(line.textContent).toBe(`No Dev Drive was found: ${reason}.`);
    expect(q(r, '[data-testid="runner-root"]')!.textContent).toContain("in Drydock's own folder. A job waits");
    expect(q(r, '[data-testid="runner-root"]')!.textContent).not.toContain('(no Dev Drive was found)');
  });

  it('shows no reason line when a Dev Drive is in use or a folder was chosen', async () => {
    const { r } = show({ runnersGet: async () => view({ devDriveReason: 'left over, not shown' }) });
    await waitFor(() => expect(q(r, '[data-testid="runner-root"]')?.textContent).toContain('on your Dev Drive D:'), r);
    expect(q(r, '[data-testid="runner-devdrive-reason"]')).toBeNull();
  });

  it('chooses the folder for Windows jobs with the window\'s folder dialog and saves it at once; the typed path still works', async () => {
    const roots: string[] = [];
    const titles: string[] = [];
    let state = view({ rootSource: 'default', root: 'C:\\x', devDrives: [], devDriveReason: 'no fixed drive on this PC uses ReFS, and a Dev Drive always does' });
    const { r } = show({
      runnersGet: async () => state,
      fsFolderHints: async () => ({ canPick: true, windows: true, devDrives: [] }),
      fsPickFolder: async (title?: string) => (titles.push(title ?? ''), 'E:\\ci'),
      runnersRootSet: async (path) => {
        roots.push(path);
        state = view({ root: path, rootSource: 'chosen' });
        return state;
      },
    });
    const browse = await ready<HTMLButtonElement>(r, '[data-testid="runner-root-browse"]');
    expect(browse.textContent).toContain('Choose folder');
    await r.act(() => browse.click());
    await waitFor(() => expect(roots).toEqual(['E:\\ci']), r);
    expect(titles).toEqual(['Choose the folder for Windows jobs']);
    await waitFor(() => expect(q(r, '[data-testid="runner-root"]')?.textContent).toContain('E:\\ci, the folder you chose'), r);
    // saved: the box is empty again for the next choice, and typing a path still saves with "Use this folder"
    expect((q(r, '[data-testid="runner-root-input"]') as HTMLInputElement).value).toBe('');
    type(q(r, '[data-testid="runner-root-input"]') as HTMLInputElement, 'F:\\jobs');
    const save = await ready<HTMLButtonElement>(r, '[data-testid="runner-root-save"]');
    await r.act(() => save.click());
    await waitFor(() => expect(roots).toEqual(['E:\\ci', 'F:\\jobs']), r);
  });

  it('keeps the chosen folder in the box when the save is refused, and offers no dialog where the window has none', async () => {
    const { r } = show({
      runnersGet: async () => view({ rootSource: 'default', root: 'C:\\x', devDrives: [] }),
      fsFolderHints: async () => ({ canPick: true, windows: true, devDrives: [] }),
      fsPickFolder: async () => 'E:\\ci',
      runnersRootSet: async () => {
        throw new Error('could not use E:\\ci: access is denied');
      },
    });
    const browse = await ready<HTMLButtonElement>(r, '[data-testid="runner-root-browse"]');
    await r.act(() => browse.click());
    await waitFor(() => expect(r.container.textContent).toContain('access is denied'), r);
    expect((q(r, '[data-testid="runner-root-input"]') as HTMLInputElement).value).toBe('E:\\ci');

    cleanup();
    let asked = false;
    const plain = show({
      runnersGet: async () => view({ rootSource: 'default', root: 'C:\\x', devDrives: [] }),
      fsFolderHints: async () => {
        asked = true;
        return { canPick: false, windows: false, devDrives: [] };
      },
    });
    await waitFor(() => expect(asked).toBe(true), plain.r);
    await waitFor(() => expect(q(plain.r, '[data-testid="runner-root-save"]')).not.toBeNull(), plain.r);
    expect(q(plain.r, '[data-testid="runner-root-browse"]')).toBeNull();
  });

  it('says that it is reading, and the core\'s error when it cannot', async () => {
    const { r } = show({ runnersGet: () => new Promise<RunnersView>(() => {}) });
    await waitFor(() => expect(r.container.textContent).toContain('Reading the runners'), r);
    cleanup();
    const bad = show({ runnersGet: async () => Promise.reject(new Error('the runner pool did not start')) });
    await waitFor(() => expect(bad.r.$('[role="alert"]')?.textContent).toContain('the runner pool did not start'), bad.r);
  });

  it('words', () => {
    expect(gb(120 * GiB)).toBe('120 GB');
    expect(gb(3.25 * GiB)).toBe('3.3 GB');
    expect(runnerStateText(slot({ state: 'error' })).tone).toBe('err');
    expect(runnerStateText(slot({ state: 'paused' })).text).toBe('off');
    expect(sentence('waiting for a job')).toBe('Waiting for a job');
    expect(sentence('')).toBe('');
    expect(runnerWhat(slot({}))).toBe('');
    expect(runnerWhat(slot({ jobs: 1, reason: 'Paused by you.' }))).toBe('Paused by you. 1 job since Drydock started.');
  });
});

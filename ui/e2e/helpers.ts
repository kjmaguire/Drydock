import { expect, type Locator, type Page } from '@playwright/test';
import { closeSync, openSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { inflateRawSync } from 'node:zlib';

export const appUrl = (): string => {
  const u = process.env.DRYDOCK_E2E_URL;
  if (!u) throw new Error('DRYDOCK_E2E_URL is not set (global setup did not run)');
  return u;
};

/**
 * Claims the first entry of a list that no test has claimed yet. The claims are files made with O_EXCL in a folder global
 * setup empties, so they hold across the worker processes Playwright starts again after a failure (a counter in this
 * module would start over and hand an earlier test's repository to the next one).
 */
function claim(kind: string, list: string[], short: string): string {
  const dir = process.env.DRYDOCK_E2E_CLAIMS;
  if (!dir) throw new Error('DRYDOCK_E2E_CLAIMS is not set (global setup did not run)');
  for (let i = 0; i < list.length; i++) {
    try {
      closeSync(openSync(join(dir, `${kind}-${i}`), 'wx'));
      return list[i]!;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'EEXIST') throw err;
    }
  }
  throw new Error(short);
}

/** A fresh git repository (copy of testdata/fixtures/gocalc) that no other test has touched. */
export function freshRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_REPOS ?? '[]') as string[];
  return claim('repo', repos, 'out of fresh repositories: raise REPOS in global-setup.ts');
}

/** The top bar (the app's main navigation): the project switcher, the tabs, the bell and the gear. */
export const sidebar = (page: Page): Locator => page.getByRole('navigation', { name: 'Main' });

/**
 * Which hub holds a link that is not in the sidebar itself (Crew HQ, decision P64; P140; P141): a project's pages are its tab bar
 * (Work is nav-overview, Crew nav-team, Settings nav-project-settings); the board, the pull requests and the rooms are links under
 * Work's list; a project's settings, its Integrations, Activity, Budgets, Evals, Security, Plan, Milestones and Knowledge are
 * sections of its Settings tab (a section of more than one page shows them as pills); every workspace or app settings page is a
 * section of Settings; My agents, Starters, Skills and All crews are the Agents library's pills; Running and My tasks are tabs of
 * the Inbox. navTo opens the hubs in turn.
 */
const HUB_OF: Record<string, string> = {
  'nav-tasks': 'nav-overview',
  'nav-pulls': 'nav-overview',
  'nav-rooms': 'nav-overview',
  'nav-project': 'nav-project-settings',
  'nav-project-integrations': 'nav-project-settings',
  'nav-knowledge': 'nav-project-settings',
  'nav-planning': 'nav-project-settings',
  'nav-activity': 'nav-project-settings',
  'nav-budgets': 'nav-project-settings',
  'nav-evals': 'nav-project-settings',
  'nav-security': 'nav-project-settings',
  'nav-plan': 'nav-planning',
  'nav-milestones': 'nav-planning',
  'nav-requirements': 'nav-knowledge',
  'nav-packs': 'nav-knowledge',
  'nav-skills': 'nav-knowledge',
  'nav-memory': 'nav-knowledge',
  'nav-repo-map': 'nav-knowledge',
  // the sections of Settings (decision P140)
  'nav-settings-about': 'nav-settings',
  'nav-side-agents': 'nav-settings',
  'nav-side-models': 'nav-settings',
  'nav-routing': 'nav-settings',
  'nav-side-integrations': 'nav-settings',
  'nav-github': 'nav-settings',
  'nav-security-scan': 'nav-settings',
  'nav-backups': 'nav-settings',
  'nav-shortcuts': 'nav-settings',
  'nav-help': 'nav-settings',
  // the pages of a section
  'nav-backups-page': 'nav-backups',
  'nav-data': 'nav-backups',
  'nav-about': 'nav-help',
  'nav-support': 'nav-help',
  'nav-license': 'nav-help',
  'nav-guide': 'nav-help',
  // the Agents library's pills
  'nav-library-mine': 'nav-library',
  'nav-library-starters': 'nav-library',
  'nav-library-skills': 'nav-library',
  'nav-crew': 'nav-library',
  // the Inbox's tabs
  'inbox-tab-needs': 'nav-needs-you',
  'inbox-tab-running': 'nav-needs-you',
  'inbox-tab-done': 'nav-needs-you',
  'inbox-tab-mine': 'nav-needs-you',
};

/** Click a navigation link by test id: the tab or pill itself, or its hub first (and the hub's hub) when it is not on the page. */
async function navTo(page: Page, testId: string): Promise<void> {
  const link = page.getByTestId(testId);
  if (!(await link.isVisible())) {
    const hub = HUB_OF[testId];
    if (hub) await navTo(page, hub);
  }
  await link.click();
}

/**
 * A room's link, in the Rooms rail. Rooms are named without a "#" (an icon, then the name,
 * then an unread count such as ", 3 unread"), so the accessible name starts with the room name and nothing word-like follows it.
 */
export function roomLink(page: Page, name: string): Locator {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return page.locator('[data-rooms]').getByRole('link', { name: new RegExp(`^${escaped}(?![\\w-])`) });
}

/** Open a room: from the Rooms rail when it is on the page, else through Rooms under Work's list (decision P141). */
export async function openRoom(page: Page, name: string): Promise<void> {
  const link = roomLink(page, name);
  if (!(await link.isVisible())) {
    if (!(await page.getByTestId('nav-overview').isVisible())) await page.goto(new URL('/', page.url()).toString());
    await navTo(page, 'nav-rooms');
  }
  await link.click();
}

/**
 * Open a workspace page (Agents, Settings and its sections, the Inbox's tabs...) from any page: every one is in the sidebar's
 * foot (Agents, Settings), a section or pill under them, or a tab of the Inbox (decision P140). An old test id names where its
 * page went.
 */
export async function workspaceNav(page: Page, testId: string): Promise<void> {
  await navTo(page, OLD_NAV[testId] ?? testId);
}

/** The test ids of pages that moved (decisions P98, P140), and where they are now. */
const OLD_NAV: Record<string, string> = {
  'nav-connections': 'nav-side-agents',
  'nav-integrations': 'nav-side-integrations',
  'nav-mcp': 'nav-side-integrations',
  'nav-providers': 'nav-side-models',
  'nav-running': 'inbox-tab-running',
  'nav-my-tasks': 'inbox-tab-mine',
  'nav-guide-top': 'nav-guide',
};

/**
 * Open a page of a project (Crew, Tasks, Project settings...). A project's pages have its tab bar (Work, Crew, Settings; decision
 * P141); on a workspace page there is none, so the project is opened first: the one named (its line in the sidebar's Projects
 * list), else the one the app remembers (its Work, "/").
 */
export async function projectNav(page: Page, testId: string, project?: string): Promise<void> {
  if (!(await page.getByTestId('nav-overview').isVisible())) {
    if (project) await projectItem(page, project).click();
    else await page.goto(new URL('/', page.url()).toString());
  }
  await navTo(page, testId);
}

/** A project's line in the sidebar's Projects list (decision P140: the list is how a project is opened). */
export const projectItem = (page: Page, name: string): Locator => sidebar(page).getByTestId('nav-project-item').filter({ has: page.getByText(name, { exact: true }) });

/** Open a project from the sidebar's Projects list: its Work, with its tab bar (decision P141). */
export async function switchProject(page: Page, name: string): Promise<void> {
  await projectItem(page, name).click();
  await expect(projectItem(page, name)).toHaveAttribute('aria-expanded', 'true');
}

/** A call of the core's RPC with the harness's token (what the page itself sends), for a spec's setup. */
export async function rpc<T = unknown>(page: Page, method: string, data: unknown = {}): Promise<T> {
  const url = new URL(appUrl());
  const r = await page.request.post(`${url.origin}/rpc/${method}`, { headers: { 'X-Drydock-Token': url.searchParams.get('token') ?? '' }, data });
  if (!r.ok()) throw new Error(`${method}: ${await r.text()}`);
  return ((await r.json()) as { result: T }).result;
}

/**
 * Home's New project dialog (decision P143), opened from its button; the page is loaded first when the browser is not on the
 * app yet. The old wizard's address (/project/new) opens the same dialog.
 */
export async function openNewProject(page: Page): Promise<void> {
  const dialog = page.getByTestId('new-project-dialog');
  if (await dialog.isVisible()) return;
  if (!page.url().startsWith('http')) await page.goto(appUrl()); // a page already on an app (the licensed harness's too) stays on it
  await sidebar(page).waitFor();
  if (!(await page.getByTestId('home-new-project').isVisible())) await page.goto(new URL('/home', page.url()).toString());
  await page.getByTestId('home-new-project').click();
  await expect(dialog).toBeVisible();
}

/**
 * Makes a project through the core, as the old wizard did with its defaults (project.setup: a local repository or none, no
 * connectors, local merge) and no agents (specs add their own on the Crew page), then opens it on its Work. Specs that are about
 * making a project use the New project dialog itself (start.spec.ts, workspace.spec.ts).
 */
async function setupProject(page: Page, req: { name: string; brief?: string; repo: { kind: 'local'; path: string } | { kind: 'none' } }): Promise<void> {
  await rpc(page, 'project.setup', { name: req.name, brief: req.brief ?? '', repo: req.repo, mode: 'local_merge', libraryServers: [], catalogServers: [], agents: [], installSkills: true });
  await page.goto(appUrl());
  await switchProject(page, req.name);
  // the new project opens on its Work (decision P141)
  await expect(page.getByTestId('work')).toBeVisible();
}

/** A fresh repository whose baseline tests pass and that carries a repository skill (.aet/skills/go-style). */
export function freshGreenRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_GREEN_REPOS ?? '[]') as string[];
  return claim('green', repos, 'out of green repositories: raise GREEN in global-setup.ts');
}

/** A folder with one Markdown page, for an expert pack of type local_dir. */
export const docsDir = (): string => process.env.DRYDOCK_E2E_DOCS ?? '';

/** Open the app with the token, create a project for a fresh repo (or the one given), and open it. */
export async function createProject(page: Page, name: string, repo?: string): Promise<void> {
  await setupProject(page, { name, repo: { kind: 'local', path: repo ?? freshRepo() } });
}

/**
 * Open the app and create a project with no repository (a repository is attached later in Project settings). It takes none of the
 * harness's fresh repositories (REPOS in global-setup.ts is a fixed pool), so a spec that only needs a project to stand in, to
 * reach the shell and its pages or to hold tasks that the core makes, does not use one up.
 */
export async function createProjectWithoutRepo(page: Page, name: string, brief = ''): Promise<void> {
  await setupProject(page, { name, brief, repo: { kind: 'none' } });
}

/**
 * Choose an option in a dropdown: the kit's Select (ui/app/ui/select.tsx, a combobox button over a listbox) or a native
 * <select>. By value, or by `{ label }` as Playwright's selectOption takes it. The chosen value is readable on the button as
 * `data-value` (`toHaveAttribute('data-value', ...)` stands in for `toHaveValue`).
 */
export async function choose(control: Locator, option: string | { label: string }): Promise<void> {
  // a model field is a text box until its CLI's models load, then a list (shared/model-field.tsx): wait for the list
  await expect.poll(() => control.evaluate((el) => el.tagName === 'SELECT' || el.hasAttribute('aria-controls')), { message: 'the control is a list to choose from' }).toBe(true);
  const tag = await control.evaluate((el) => el.tagName);
  if (tag === 'SELECT') {
    await control.selectOption(option);
    return;
  }
  await control.click();
  const listId = await control.getAttribute('aria-controls');
  const list = control.page().locator(`#${listId}`);
  const item = typeof option === 'string' ? list.locator(`[role="option"][data-value=${cssString(option)}]`) : list.getByRole('option', { name: option.label, exact: true });
  await item.click();
  // the list has closed (or the control turned into something else, as the model field does for "Another model...")
  await expect(control).not.toHaveAttribute('aria-expanded', 'true');
}

/**
 * `v` as a quoted CSS string, for an attribute selector: a backslash and a double quote are escaped, and a line break, which a
 * CSS string cannot hold as itself, is written as its hex escape.
 */
export function cssString(v: string): string {
  return `"${v.replace(/[\\"]/g, '\\$&').replace(/[\n\r\f]/g, (c) => `\\${c.charCodeAt(0).toString(16)} `)}"`;
}

/**
 * Opens a tile's panel (ui/app/ui/tiles.tsx): a click on a tile that is not chosen yet. A click on the chosen one would close its
 * panel, and a page may open its first tile by itself.
 */
export async function openTile(tile: Locator): Promise<void> {
  await expect(tile).toBeVisible();
  if ((await tile.getAttribute('aria-pressed')) !== 'true') await tile.click();
  await expect(tile).toHaveAttribute('aria-pressed', 'true');
}

/**
 * The options of a dropdown, as a locator, or the one option with `value`: the kit's Select keeps them in the page while the
 * list is closed (hidden), so they can be counted and read without opening it; a native <select> gives its <option>s.
 */
export async function optionsOf(control: Locator, value?: string): Promise<Locator> {
  const tag = await control.evaluate((el) => el.tagName);
  const one = value === undefined ? '' : `[${tag === 'SELECT' ? 'value' : 'data-value'}=${cssString(value)}]`;
  if (tag === 'SELECT') return control.locator(`option${one}`);
  const listId = await control.getAttribute('aria-controls');
  return control.page().locator(`#${listId} [role="option"]${one}`);
}

/**
 * Opens the Crew page's New agent dialog (the form to make an agent is in it). The page can be replaced while it is still
 * settling after a project change (seen once: the field detached and the test waited out its whole timeout), so opening the
 * page, the dialog and reaching its first field is one step that is retried as a whole.
 */
export async function openNewAgent(page: Page, nav = true): Promise<void> {
  const dialog = page.locator('dialog[open]');
  const form = dialog.getByTestId('agent-form');
  await expect(async () => {
    if (!(await form.isVisible())) {
      if (!(await dialog.getByTestId('new-agent-configure').isVisible())) {
        if (nav) await projectNav(page, 'nav-team');
        await page.getByRole('button', { name: /^New agent/ }).click({ timeout: 5_000 });
      }
      // New agent opens on Describe (decision P138); the full form is the Configure tab
      await dialog.getByTestId('new-agent-configure').click({ timeout: 5_000 });
    }
    await expect(form.getByLabel('Agent name')).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
}

/** Opens New agent on its Describe tab (decision P138). */
export async function openDescribeAgent(page: Page, nav = true): Promise<void> {
  const box = page.locator('dialog[open]').getByTestId('describe-agent');
  await expect(async () => {
    if (!(await box.isVisible())) {
      if (nav) await projectNav(page, 'nav-team');
      await page.getByRole('button', { name: /^New agent/ }).click({ timeout: 5_000 });
    }
    await expect(box.getByLabel('What should this agent do?')).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
}

/** Opens the agent editor panel from the model chip of an agent's Crew card (decision P137). */
export async function openAgentPanel(page: Page, name: string, nav = true): Promise<Locator> {
  if (nav) await projectNav(page, 'nav-team');
  await page.locator(`[data-testid="agent"][data-name="${name}"]`).getByTestId('agent-model-chip').click();
  const panel = page.locator('dialog[open]').getByTestId('agent-editor');
  await expect(panel).toBeVisible();
  return panel;
}

/** On an agent's own page, opens the Inspector from the "..." menu beside Message and Edit. */
export async function inspectAgent(page: Page): Promise<void> {
  await page.getByTestId('agent-more').click();
  await page.getByRole('menuitem', { name: 'Inspect' }).click();
}

/** Opens an agent's own page from its card on the Crew page (Message and Edit are there; Inspect and Remove in its "..." menu). */
export async function openAgentPage(page: Page, name: string): Promise<void> {
  await projectNav(page, 'nav-team');
  await page.locator(`[data-testid="agent"][data-name="${name}"]`).getByTestId('agent-link').click();
  await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
}

export async function createAgent(page: Page, name: string, role: string): Promise<void> {
  await openNewAgent(page);
  await page.getByLabel('Agent name').fill(name);
  await page.getByLabel('Role', { exact: true }).fill(role);
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  // by its name exactly: another card's text (a room chip such as "reviews") may contain this one's name
  await expect(page.locator(`[data-testid="agent"][data-name="${name}"]`)).toBeVisible();
}

/** Create a task, assign it, and stop before Start (so a test can pick a template). */
export async function createAssignedTask(page: Page, title: string, worker: string, reviewer: string, workerRole = 'backend', reviewerRole = 'reviewer'): Promise<void> {
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill(title);
  await page.getByLabel('Description').fill('Add a Multiply function to the gocalc package.');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: new RegExp(title) })).toBeVisible();
  await choose(page.getByLabel('Worker', { exact: true }), { label: `${worker} (${workerRole})` });
  await choose(page.getByLabel('Reviewer', { exact: true }), { label: `${reviewer} (${reviewerRole})` });
  await page.getByRole('button', { name: 'Assign' }).click();
  await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeEnabled();
}

export async function createAndStartTask(page: Page, title: string, worker: string, reviewer: string): Promise<void> {
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill(title);
  await page.getByLabel('Description').fill('Subtract returns 0; make it return a - b.');
  await page.getByLabel('Acceptance criteria').fill('Given 5 and 3 When Subtract is called Then 2');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: new RegExp(title) })).toBeVisible();
  await choose(page.getByLabel('Worker', { exact: true }), { label: `${worker} (backend)` });
  await choose(page.getByLabel('Reviewer', { exact: true }), { label: `${reviewer} (reviewer)` });
  await page.getByRole('button', { name: 'Assign' }).click();
  await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Start', exact: true }).click();
}

/** The `-licensed` TEST HARNESS (a build with a throwaway license key, starting with no license). */
export const licensedUrl = (): string => {
  const u = process.env.DRYDOCK_E2E_LICENSED_URL;
  if (!u) throw new Error('DRYDOCK_E2E_LICENSED_URL is not set (global setup did not run)');
  return u;
};
export const licenseTokens = (): { active: string; expired: string } => ({
  active: process.env.DRYDOCK_E2E_LICENSE_ACTIVE ?? '',
  expired: process.env.DRYDOCK_E2E_LICENSE_EXPIRED ?? '',
});
export function licensedRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_LICENSED_REPOS ?? '[]') as string[];
  return claim('licensed', repos, 'out of licensed-harness repositories');
}

/** Reads a zip written by the core (stored/deflate entries only) and returns name -> bytes, so a test can look inside a file the UI created. */
export function readZip(path: string): Record<string, Buffer> {
  const buf = readFileSync(path);
  let eocd = -1;
  for (let i = buf.length - 22; i >= 0; i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error(`${path} is not a zip`);
  const count = buf.readUInt16LE(eocd + 10);
  let p = buf.readUInt32LE(eocd + 16);
  const out: Record<string, Buffer> = {};
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error('bad central directory');
    const method = buf.readUInt16LE(p + 10);
    const csize = buf.readUInt32LE(p + 20);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const local = buf.readUInt32LE(p + 42);
    const name = buf.toString('utf8', p + 46, p + 46 + nameLen);
    const lnLen = buf.readUInt16LE(local + 26);
    const leLen = buf.readUInt16LE(local + 28);
    const data = buf.subarray(local + 30 + lnLen + leLen, local + 30 + lnLen + leLen + csize);
    out[name] = method === 0 ? Buffer.from(data) : inflateRawSync(data);
    p += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

/** Opens every closed group of checks in the room on screen (consecutive checks are one row, closed when all passed; P85). */
export async function openChecks(page: Page): Promise<void> {
  const groups = page.locator('details[data-testid=checks]');
  await expect(groups.first()).toBeVisible();
  for (const g of await groups.all()) {
    if (!(await g.evaluate((d) => (d as HTMLDetailsElement).open))) await g.locator('summary').click();
  }
}

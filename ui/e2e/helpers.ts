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
 * Which hub holds a link that is not in the top bar itself (Crew HQ, decision P64): the sub-pages of a tab are pills under
 * it (a project's settings and its Integrations are the last pills of Overview), and every workspace or app settings page is in
 * the Settings rail behind the gear.
 */
const HUB_OF: Record<string, string> = {
  'nav-plan': 'nav-tasks',
  'nav-pulls': 'nav-tasks',
  'nav-milestones': 'nav-tasks',
  'nav-activity': 'nav-overview',
  'nav-budgets': 'nav-overview',
  'nav-evals': 'nav-overview',
  'nav-security': 'nav-overview',
  'nav-project': 'nav-overview',
  'nav-project-integrations': 'nav-overview',
  'nav-requirements': 'nav-knowledge',
  'nav-packs': 'nav-knowledge',
  'nav-skills': 'nav-knowledge',
  'nav-memory': 'nav-knowledge',
  'nav-repo-map': 'nav-knowledge',
  'nav-library-skills': 'nav-library',
  'nav-integrations': 'nav-settings',
  'nav-mcp': 'nav-settings',
  'nav-connections': 'nav-settings',
  'nav-github': 'nav-settings',
  'nav-providers': 'nav-settings',
  'nav-routing': 'nav-settings',
  'nav-license': 'nav-settings',
  'nav-backups': 'nav-settings',
  'nav-data': 'nav-settings',
  'nav-support': 'nav-settings',
};

/** Click a navigation link by test id: the tab or pill itself, or its hub first when the link is not on the page yet. */
async function navTo(page: Page, testId: string): Promise<void> {
  const link = page.getByTestId(testId);
  if (!(await link.isVisible())) {
    const hub = HUB_OF[testId];
    if (hub) await page.getByTestId(hub).click();
  }
  await link.click();
}

/**
 * A room's link: in the Rooms rail, or in the Overview's rooms card. Rooms are named without a "#" (an icon, then the name,
 * then an unread count such as ", 3 unread"), so the accessible name starts with the room name and nothing word-like follows it.
 */
export function roomLink(page: Page, name: string): Locator {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return page.locator('[data-rooms]').getByRole('link', { name: new RegExp(`^${escaped}(?![\\w-])`) });
}

/** Open a room: from the Overview's rooms card or the Rooms rail when one is on the page, else through the Rooms tab. */
export async function openRoom(page: Page, name: string): Promise<void> {
  const link = roomLink(page, name);
  if (!(await link.isVisible())) {
    const tab = sidebar(page).getByTestId('nav-rooms');
    if (await tab.isVisible()) await tab.click();
    else await page.goto(new URL('/', page.url()).toString());
  }
  await link.click();
}

/**
 * Open a workspace page (Library, Integrations, Connections, Settings...). A settings page is behind the gear on any page;
 * the others are tabs (or pills) of the workspace bar, which a project's page reaches through the Drydock mark (Home).
 */
export async function workspaceNav(page: Page, testId: string): Promise<void> {
  if (HUB_OF[testId] !== 'nav-settings' && testId !== 'nav-settings' && !(await sidebar(page).getByTestId('nav-chats').isVisible())) {
    await sidebar(page).getByTestId('nav-workspace').click();
  }
  await navTo(page, testId);
}

/**
 * Open a page of a project (Crew, Tasks, Project settings...). On a workspace page the bar shows the workspace, so the
 * project is opened first: the one named (its card on Home), else the one the app remembers (its home, "/").
 */
export async function projectNav(page: Page, testId: string, project?: string): Promise<void> {
  if (!(await sidebar(page).getByTestId('nav-overview').isVisible())) {
    if (project) {
      if (!(await page.getByTestId('home-project').first().isVisible())) await sidebar(page).getByTestId('nav-workspace').click();
      await page.getByTestId('home-project').filter({ hasText: project }).click();
    } else await page.goto(new URL('/', page.url()).toString());
  }
  await navTo(page, testId);
}

/** The project switcher at the top of the sidebar: opens the menu of Home, the projects, "New project…" and "App settings". */
export const projectSwitcher = (page: Page): Locator => page.getByRole('button', { name: 'Switch project' });

/** Switch to a project through the switcher's menu (exact name: "gocalc-approve" must not match a button "Approve"). */
export async function switchProject(page: Page, name: string): Promise<void> {
  await projectSwitcher(page).click();
  await page.getByRole('menuitemradio', { name, exact: true }).click();
  await expect(projectSwitcher(page)).toContainText(name);
}

/** The new-project wizard: the first run shows it directly; later runs open it from the switcher's "New project…". */
export async function showNewProjectForm(page: Page): Promise<void> {
  const field = page.getByLabel('Project name', { exact: true });
  await field.or(projectSwitcher(page)).first().waitFor();
  if (!(await field.isVisible())) {
    await projectSwitcher(page).click();
    await page.getByRole('menuitem', { name: 'New project…', exact: true }).click();
  }
  await expect(field).toBeVisible();
}

/** The wizard's Next button. */
export async function wizardNext(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Next', exact: true }).click();
}

/**
 * Walk the open wizard to its Review step: Basics (the name, when given), Repository (a local folder, or Later), no
 * connectors, and an empty team (the suggested agents are removed: tests add their own agents on the Team page).
 */
export async function wizardToReview(page: Page, opts: { name?: string; repo?: string; later?: boolean }): Promise<void> {
  if (opts.name) await page.getByLabel('Project name', { exact: true }).fill(opts.name);
  await wizardNext(page);
  const source = page.getByRole('radiogroup', { name: 'Repository source' });
  if (opts.later) {
    await source.getByRole('radio', { name: 'Later' }).check();
  } else {
    await source.getByRole('radio', { name: 'Local folder' }).check();
    await page.getByLabel('Repository path').fill(opts.repo ?? freshRepo());
  }
  await wizardNext(page); // Connectors
  await wizardNext(page); // Team
  await expect(page.getByTestId('team-list')).toHaveAttribute('data-ready', 'true');
  const removeAll = page.getByRole('button', { name: 'Remove all', exact: true });
  if (await removeAll.isVisible()) await removeAll.click();
  await expect(page.getByTestId('team-member')).toHaveCount(0);
  await wizardNext(page); // Review
  await expect(page.locator('form[data-step="Review"]')).toBeVisible();
}

/** Create from the Review step and open the new project. */
export async function wizardCreate(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Create project' }).click();
  await page.getByRole('button', { name: 'Open project' }).click();
}

/** A fresh repository whose baseline tests pass and that carries a repository skill (.aet/skills/go-style). */
export function freshGreenRepo(): string {
  const repos = JSON.parse(process.env.DRYDOCK_E2E_GREEN_REPOS ?? '[]') as string[];
  return claim('green', repos, 'out of green repositories: raise GREEN in global-setup.ts');
}

/** A folder with one Markdown page, for an expert pack of type local_dir. */
export const docsDir = (): string => process.env.DRYDOCK_E2E_DOCS ?? '';

/** Open the app with the token, create a project for a fresh repo (or the one given), and return its name. */
export async function createProject(page: Page, name: string, repo?: string): Promise<void> {
  await page.goto(appUrl());
  await showNewProjectForm(page);
  await wizardToReview(page, { name, repo: repo ?? freshRepo() });
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
}

/**
 * Open the app and create a project with no repository (the wizard's "Later"). It takes none of the harness's fresh repositories
 * (REPOS in global-setup.ts is a fixed pool), so a spec that only needs a project to stand in, to reach the shell and its pages
 * or to hold tasks that the core makes, does not use one up.
 */
export async function createProjectWithoutRepo(page: Page, name: string): Promise<void> {
  await page.goto(appUrl());
  await showNewProjectForm(page);
  await wizardToReview(page, { name, later: true });
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
}

/**
 * Choose an option in a dropdown: the kit's Select (ui/app/ui/select.tsx, a combobox button over a listbox) or a native
 * <select>. By value, or by `{ label }` as Playwright's selectOption takes it. The chosen value is readable on the button as
 * `data-value` (`toHaveAttribute('data-value', ...)` stands in for `toHaveValue`).
 */
export async function choose(control: Locator, option: string | { label: string }): Promise<void> {
  const tag = await control.evaluate((el) => el.tagName);
  if (tag === 'SELECT') {
    await control.selectOption(option);
    return;
  }
  await control.click();
  const listId = await control.getAttribute('aria-controls');
  const list = control.page().locator(`#${listId}`);
  const item = typeof option === 'string' ? list.locator(`[role="option"][data-value="${option.replace(/"/g, '\\"')}"]`) : list.getByRole('option', { name: option.label, exact: true });
  await item.click();
  // the list has closed (or the control turned into something else, as the model field does for "Another model...")
  await expect(control).not.toHaveAttribute('aria-expanded', 'true');
}

/**
 * The options of a dropdown, as a locator, or the one option with `value`: the kit's Select keeps them in the page while the
 * list is closed (hidden), so they can be counted and read without opening it; a native <select> gives its <option>s.
 */
export async function optionsOf(control: Locator, value?: string): Promise<Locator> {
  const tag = await control.evaluate((el) => el.tagName);
  const one = value === undefined ? '' : `[${tag === 'SELECT' ? 'value' : 'data-value'}="${value.replace(/"/g, '\\"')}"]`;
  if (tag === 'SELECT') return control.locator(`option${one}`);
  const listId = await control.getAttribute('aria-controls');
  return control.page().locator(`#${listId} [role="option"]${one}`);
}

export async function createAgent(page: Page, name: string, role: string): Promise<void> {
  // The form can be replaced while the page is still settling after a project change (seen once: the field detached and the
  // test waited out its whole timeout), so open the page and fill the name as one step that is retried as a whole.
  await expect(async () => {
    await projectNav(page, 'nav-team');
    await page.getByLabel('Agent name').fill(name, { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByLabel('Role', { exact: true }).fill(role);
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: name })).toBeVisible();
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
  await expect(page.getByRole('button', { name: 'Start' })).toBeEnabled();
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
  await expect(page.getByRole('button', { name: 'Start' })).toBeEnabled();
  await page.getByRole('button', { name: 'Start' }).click();
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

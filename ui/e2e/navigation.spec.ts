// The six places of the sidebar, one Settings and one Agents library (decision P140), against the real Go core: the sidebar
// holds exactly New, Home, Inbox, Chats, the projects, Agents and Settings; every page that left it opens from Settings and by
// its old address, inside the Settings frame with its section marked; the Settings search filters the sections and their rows;
// the Agents library's tabs; and the command menu reaches Models and Security scan.
import { expect, test, type Page } from '@playwright/test';
import { appUrl, createProjectWithoutRepo, sidebar } from './helpers';

const rail = (page: Page) => page.getByRole('navigation', { name: 'Settings' });
const at = (path: string) => {
  const u = new URL(appUrl());
  u.pathname = path;
  return u.toString();
};

test('the sidebar has exactly its six places, and New opens the start screen with the cursor in its box (Ctrl+N too)', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await createProjectWithoutRepo(page, 'nav-six');
  const ids = await sidebar(page).locator('a[data-nav][data-testid]').evaluateAll((as) => as.map((a) => a.getAttribute('data-testid')));
  // the projects are one line each: a project's Work, Crew and Settings are its tab bar (decision P141)
  expect(ids).toEqual(['nav-new', 'nav-workspace', 'nav-needs-you', 'nav-chats', ...ids.slice(4, -2), 'nav-library', 'nav-settings']);
  expect(ids.slice(4, -2).every((id) => id === 'nav-project-item')).toBe(true);
  await expect(sidebar(page).getByTestId('nav-library')).toHaveText('Agents');
  await expect(sidebar(page).getByTestId('nav-new')).toContainText('New');
  for (const gone of ['nav-running', 'nav-my-tasks', 'nav-crew', 'nav-security-scan', 'nav-side-agents', 'nav-side-integrations', 'nav-side-models', 'nav-library-skills', 'nav-guide-top']) {
    await expect(sidebar(page).getByTestId(gone)).toHaveCount(0);
  }
  // New is the start screen (decision P142), the cursor in its box; the wizard is behind "More options..."
  await sidebar(page).getByTestId('nav-new').click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId('start-text')).toBeFocused();
  await page.getByTestId('start-more').click();
  await expect(page).toHaveURL(/\/project\/new$/);
  await expect(page.getByLabel('Project name', { exact: true })).toBeVisible();
  // Ctrl+N from another page does the same
  await sidebar(page).getByTestId('nav-chats').click();
  await sidebar(page).getByTestId('nav-library').click();
  await page.locator('#main').focus();
  await page.keyboard.press('Control+KeyN');
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId('start-text')).toBeFocused();
});

test('every page that left the sidebar opens from Settings and by its old address, inside the Settings frame', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await page.goto(appUrl());
  await sidebar(page).getByTestId('nav-settings').click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.locator('#main h1')).toHaveText('General');
  await expect(rail(page).getByRole('heading', { name: 'Settings' })).toBeVisible();
  await expect(rail(page).getByRole('list', { name: 'Sections' }).locator(':scope > li > a')).toHaveText([
    'General',
    'Agent CLIs',
    'Models',
    'Routing & budgets',
    'Integrations',
    'GitHub',
    'Security scan',
    'Backups & data',
    'Shortcuts',
    'Help & about',
  ]);
  // each section from the list: its address, its title, the section marked and Settings current in the sidebar
  const sections: [string, RegExp, string][] = [
    ['nav-side-agents', /\/agents$/, 'Agent CLIs'],
    ['nav-side-models', /\/models$/, 'Models'],
    ['nav-routing', /\/routing$/, 'Routing'],
    ['nav-side-integrations', /\/integrations$/, 'Integrations'],
    ['nav-github', /\/github$/, 'GitHub'],
    ['nav-security-scan', /\/security-scan$/, 'Security scan'],
    ['nav-backups', /\/backups$/, 'Backups'],
    ['nav-shortcuts', /\/settings\/shortcuts$/, 'Shortcuts'],
    ['nav-help', /\/settings\/about$/, 'About'],
    ['nav-settings-about', /\/settings$/, 'General'],
  ];
  for (const [id, url, title] of sections) {
    await rail(page).getByTestId(id).click();
    await expect(page).toHaveURL(url);
    await expect(page.locator('#main h1').first()).toHaveText(title);
    await expect(rail(page).getByTestId(id)).toHaveAttribute('aria-current', 'page');
    await expect(sidebar(page).getByTestId('nav-settings')).toHaveAttribute('aria-current', 'page');
  }
  // a section of more than one screen shows them as pills
  await rail(page).getByTestId('nav-backups').click();
  await page.getByRole('navigation', { name: 'Section' }).getByTestId('nav-data').click();
  await expect(page).toHaveURL(/\/data$/);
  await expect(page.locator('#main h1')).toHaveText('Export and import');
  await expect(rail(page).getByTestId('nav-backups')).toHaveAttribute('aria-current', 'page');

  // the old addresses, deep links among them, open the same screens with their section marked
  const old: [string, string, RegExp?][] = [
    ['/models', 'nav-side-models'],
    ['/providers', 'nav-side-models', /\/models$/],
    ['/agents/claude', 'nav-side-agents'],
    ['/connections', 'nav-side-agents', /\/agents$/],
    ['/integrations/linear', 'nav-side-integrations'],
    ['/mcp', 'nav-side-integrations', /\/integrations$/],
    ['/routing', 'nav-routing'],
    ['/github', 'nav-github'],
    ['/security-scan', 'nav-security-scan'],
    ['/data', 'nav-backups'],
    ['/support', 'nav-help'],
    ['/license', 'nav-help'],
  ];
  for (const [path, id, url] of old) {
    await page.goto(at(path));
    await expect(page).toHaveURL(url ?? new RegExp(`${path.replace(/\//g, '\\/')}$`));
    await expect(rail(page).getByTestId(id)).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('#main h1').first()).toBeVisible();
  }
  // the Guide stays a page of its own, linked from Help & about
  await page.goto(at('/settings/about'));
  await page.getByRole('navigation', { name: 'Section' }).getByTestId('nav-guide').click();
  await expect(page).toHaveURL(/\/guide$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Guide' })).toBeVisible();
  await expect(rail(page)).toHaveCount(0);
});

test('the Settings search filters the sections and names the rows it found', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await page.goto(at('/settings'));
  const box = rail(page).getByRole('searchbox', { name: 'Search settings' });
  const sections = rail(page).getByRole('list', { name: 'Sections' }).locator(':scope > li > a');
  await expect(sections).toHaveCount(10);
  await box.fill('theme');
  await expect(sections).toHaveText(['General']);
  await expect(rail(page).getByTestId('settings-hit')).toHaveText(['Theme']);
  await box.fill('ssh nothing');
  await expect(sections).toHaveCount(0);
  await expect(rail(page).getByTestId('settings-no-match')).toBeVisible();
  await box.fill('export');
  await expect(sections).toHaveText(['Backups & data']);
  await rail(page).getByTestId('settings-hit').filter({ hasText: 'Export and import' }).click();
  await expect(page).toHaveURL(/\/data$/);
  await box.fill('keyboard');
  await expect(sections).toHaveText(['Shortcuts']);
  await box.press('Escape');
  await expect(sections).toHaveCount(10);
});

test('Agents: My agents, Starters and Skills as tabs, All crews set apart, and Use makes a starter one of my agents', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await createProjectWithoutRepo(page, 'nav-agents');
  await sidebar(page).getByTestId('nav-library').click();
  await expect(page).toHaveURL(/\/library$/);
  const tabs = page.getByRole('navigation', { name: 'Section' });
  await expect(tabs).toContainText('Agents');
  await expect(tabs.getByRole('link')).toHaveText(['My agents', 'Starters', 'Skills', 'All crews']);
  await expect(tabs.getByTestId('nav-library-mine')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { level: 1, name: 'My agents' })).toBeVisible();

  await tabs.getByTestId('nav-library-starters').click();
  await expect(page).toHaveURL(/\/library\/starters$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Starters' })).toBeVisible();
  const starters = page.getByTestId('library-prebuilt');
  await expect(starters.getByTestId('prebuilt').first()).toBeVisible();
  // Use: the agent form, filled from the starter; saving makes it one of my agents
  await starters.getByRole('button', { name: 'Use Backend Engineer' }).click();
  const form = page.getByTestId('template-form');
  await expect(form.getByLabel('Template name')).toHaveValue('Backend Engineer');
  await form.getByLabel('Template name').fill('Backend Engineer (nav)');
  await form.getByRole('button', { name: 'Create template' }).click();
  await expect(page).toHaveURL(/\/library$/);
  const card = page.getByTestId('lib-template-card').filter({ hasText: 'Backend Engineer (nav)' });
  await expect(card).toBeVisible();
  await expect(page.getByTestId('library-note')).toContainText('is one of your agents now');
  // its ⋯ menu deletes it after a confirmation
  await card.getByTestId('lib-template-more').click();
  await page.getByRole('menuitem', { name: 'Delete' }).click();
  await page.getByTestId('lib-template-delete').getByRole('button', { name: 'Yes, delete' }).click();
  await expect(card).toHaveCount(0);

  await tabs.getByTestId('nav-library-skills').click();
  await expect(page).toHaveURL(/\/library\/skills$/);
  await expect(page.getByTestId('skill-library')).toBeVisible();
  await tabs.getByTestId('nav-crew').click();
  await expect(page).toHaveURL(/\/crew$/);
  await expect(page.getByRole('heading', { level: 1, name: 'All crews' })).toBeVisible();
  await expect(sidebar(page).getByTestId('nav-library')).toHaveAttribute('aria-current', 'page');
});

test('Ctrl+K reaches Models and Security scan, which left the sidebar', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await page.goto(appUrl());
  await expect(sidebar(page).getByTestId('nav-settings')).toBeVisible();
  for (const [query, name, url] of [
    ['models', /^Models/, /\/models$/],
    ['security scan of any', /^Security scan of any repository/, /\/security-scan$/],
  ] as const) {
    await page.locator('#main').focus();
    await page.keyboard.press('Control+k');
    await page.getByRole('combobox', { name: 'Command' }).fill(query);
    await page.getByRole('option', { name }).first().click();
    await expect(page).toHaveURL(url);
    await expect(sidebar(page).getByTestId('nav-settings')).toHaveAttribute('aria-current', 'page');
  }
});

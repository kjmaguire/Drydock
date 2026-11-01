// A project is one workspace (decision P141) against the real Go core with the scripted agent (a TEST DOUBLE, cmd/drydock-e2e):
// the tab bar Work · Crew · Settings; Work's list of the project's tasks and chats (Needs you / Running / Earlier) with the open
// one in the centre, named in the URL so Reload and Back keep it; the crew rail; and every old address of a project page still
// opening, with its tab marked.
import { expect, test } from '@playwright/test';
import { createAgent, createAndStartTask, createProject, projectItem, sidebar } from './helpers';

test('Work lists the tasks and chats, opens one in the centre, and shows the crew', async ({ page }) => {
  test.setTimeout(240_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await createProject(page, 'work-e2e');
  await createAgent(page, 'Ada', 'backend');
  await createAgent(page, 'Rex', 'reviewer');
  await createAndStartTask(page, 'Implement Subtract in Work', 'Ada', 'Rex');
  await expect(page.getByTestId('review-panel').getByRole('button', { name: 'Approve' })).toBeVisible({ timeout: 90_000 });

  // the project in the sidebar is one line; it opens Work, the tab bar marks it
  await projectItem(page, 'work-e2e').click();
  await expect(page).toHaveURL(/\/$/);
  const tabs = page.getByRole('navigation', { name: 'Project' });
  await expect(tabs.getByTestId('nav-overview')).toHaveText('Work');
  await expect(tabs.getByTestId('nav-overview')).toHaveAttribute('aria-current', 'page');
  await expect(sidebar(page).getByTestId('nav-tasks')).toHaveCount(0);

  // the task waits on the review: it is under Needs you, and open in the centre by itself
  const row = page.getByTestId('work-item').filter({ hasText: 'Implement Subtract in Work' });
  await expect(row).toHaveAttribute('data-group', 'needs');
  await expect(row).toHaveAttribute('aria-current', 'true');
  await expect(page.getByTestId('work-centre').getByRole('heading', { level: 1, name: 'Implement Subtract in Work' })).toBeVisible();

  // the crew rail: each agent with its model and effort; a click opens its editor in place
  const crew = page.getByRole('complementary', { name: 'Crew' });
  await expect(crew.getByTestId('work-agent')).toHaveCount(2);
  await expect(crew.locator('[data-testid="work-agent"][data-name="Ada"]')).toBeVisible();
  await expect(crew.getByTestId('work-spend')).toContainText('Spent so far');
  await crew.locator('[data-testid="work-agent"][data-name="Rex"]').click();
  await expect(page.getByTestId('agent-editor')).toHaveAttribute('aria-label', 'Edit Rex');
  await expect(page).toHaveURL(/\/$/);
  await page.keyboard.press('Escape');

  // a chat in the project: made at once, opened in the centre, listed with the tasks; the URL names it
  await page.getByTestId('work-new-chat').click();
  await expect(page).toHaveURL(/\/\?item=chat%3A/);
  const chatId = decodeURIComponent(new URL(page.url()).searchParams.get('item') ?? '');
  const chatRow = page.locator(`[data-testid="work-item"][data-item="${chatId}"]`);
  await expect(chatRow).toHaveAttribute('data-kind', 'chat');
  await expect(chatRow).toHaveAttribute('aria-current', 'true');
  await expect(page.getByTestId('work-centre').getByRole('textbox').first()).toBeVisible();

  // Back returns to the task, Reload keeps the chat
  await page.goBack();
  await expect(row).toHaveAttribute('aria-current', 'true');
  await page.goForward();
  await page.reload();
  await expect(chatRow).toHaveAttribute('aria-current', 'true');

  // the search narrows the list
  await page.getByLabel('Search this project').fill('subtract');
  await expect(page.getByTestId('work-item')).toHaveCount(1);
  await page.getByLabel('Search this project').fill('');

  // New task is on Work's tab bar
  await tabs.getByTestId('work-new-task').click();
  await expect(page).toHaveURL(/\/tasks\/new$/);
});

test("every old address of a project page still opens, under Work, Crew or the project's Settings", async ({ page }) => {
  await createProject(page, 'work-old-urls');
  const tabs = page.getByRole('navigation', { name: 'Project' });
  const cases: [string, string, RegExp][] = [
    ['/tasks', 'nav-overview', /Tasks|work-old-urls/],
    ['/rooms', 'nav-overview', /general/],
    ['/pulls', 'nav-overview', /Pull requests/],
    ['/team', 'nav-team', /Crew/],
    ['/project', 'nav-project-settings', /Project settings|General|work-old-urls/],
    ['/project/integrations', 'nav-project-settings', /Integrations/],
    ['/activity', 'nav-project-settings', /Activity/],
    ['/budgets', 'nav-project-settings', /Budgets/],
    ['/evals', 'nav-project-settings', /Evals/],
    ['/security', 'nav-project-settings', /Security/],
    ['/knowledge', 'nav-project-settings', /Knowledge/],
    ['/plan', 'nav-project-settings', /Plan/],
  ];
  for (const [path, tab, heading] of cases) {
    await page.goto(new URL(path, page.url()).toString());
    await expect(tabs.getByTestId(tab), path).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('#main h1').first(), path).toHaveText(heading);
  }
  // the project's Settings tab: its sections in a rail, the section of the page marked
  await page.goto(new URL('/budgets', page.url()).toString());
  const rail = page.getByRole('navigation', { name: 'Project settings' });
  await expect(rail.getByTestId('nav-budgets')).toHaveAttribute('aria-current', 'page');
  await rail.getByTestId('nav-knowledge').click();
  await expect(page).toHaveURL(/\/knowledge$/);
  await page.getByRole('navigation', { name: 'Section' }).getByTestId('nav-memory').click();
  await expect(page).toHaveURL(/\/memory$/);
  await expect(rail.getByTestId('nav-knowledge')).toHaveAttribute('aria-current', 'page');
});

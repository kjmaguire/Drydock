// The command menu over a task (the graphite mockup "Command menu", decision P97) against the real Go core: the chip names the
// task of the page, the "What happens" panel shows the core's own preview of the assignment (the real branch), and Enter hands
// the task to the agent. The task is made through the core's task.create (test setup); everything the menu shows and does is
// the core's.
import { expect, test } from '@playwright/test';
import { appUrl, createAgent, createProjectWithoutRepo } from './helpers';

test('the command menu assigns the task of the page to an agent, after showing the branch it will work on', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  const url = appUrl();
  const origin = new URL(url).origin;
  const token = new URL(url).searchParams.get('token') ?? '';
  const rpc = async <T>(method: string, data: unknown): Promise<T> => {
    const r = await page.request.post(`${origin}/rpc/${method}`, { headers: { 'X-Drydock-Token': token }, data });
    expect(r.ok(), `${method}: ${await r.text()}`).toBe(true);
    return ((await r.json()) as { result: T }).result;
  };

  await createProjectWithoutRepo(page, 'menu-assign');
  await createAgent(page, 'Ada', 'backend');
  // the core hands a task to an agent only when another one can review its work
  await createAgent(page, 'Rex', 'reviewer');
  const project = (await rpc<{ id: string; name: string; key?: string }[]>('project.list', {})).find((p) => p.name === 'menu-assign');
  if (!project) throw new Error('the project that was just created is not in project.list');
  const ada = (await rpc<{ id: string; name: string }[]>('agent.list', { projectId: project.id })).find((a) => a.name === 'Ada');
  if (!ada) throw new Error('Ada is not on the team');
  const task = await rpc<{ id: string; seq: number }>('task.create', { projectId: project.id, title: 'Support hex input', description: 'Parse 0x1F as 31.' });
  // what the core says assigning would do (changes nothing): the panel must show the same branch
  const preview = await rpc<{ branch: string }>('task.assignPreview', { id: task.id, assignee: ada.id });
  expect(preview.branch).toMatch(/^drydock\//);

  // the task's page; Ctrl+K opens the menu over it
  await page.goto(new URL(`/task/${task.id}`, url).toString());
  await expect(page.getByRole('heading', { level: 1, name: /Support hex input/ })).toBeVisible();
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('Control+k');
  const menu = page.getByRole('dialog', { name: 'Command menu' });
  await expect(menu).toBeVisible();
  await expect(menu.getByRole('combobox', { name: 'Command' })).toHaveAttribute('placeholder', /^Go to, open or do/);
  const key = project.key ? `${project.key}-${task.seq}` : `#${task.seq}`;
  await expect(menu.getByTestId('palette-context')).toHaveText(`${key}Support hex input`);

  // Assign comes first, Ada highlighted, with where her CLI runs
  const listbox = menu.getByRole('listbox', { name: 'Commands' });
  await expect(listbox.getByRole('group', { name: 'Assign' })).toBeVisible();
  await expect(listbox.getByRole('group', { name: 'Go to' }).getByRole('option', { name: /Crew/ })).toBeVisible();
  const option = listbox.getByRole('option', { name: /Assign to Ada/ });
  await expect(option).toHaveAttribute('aria-selected', 'true');
  await expect(option).toContainText('Claude Code');
  await expect(listbox.getByRole('option', { name: /Assign to me/ })).toBeVisible();
  // no lead on this team: nobody to split it
  await expect(listbox.getByRole('option', { name: /Ask the team lead/ })).toHaveCount(0);

  // What happens: the agent, and the core's real branch
  const panel = menu.getByRole('complementary', { name: 'What happens' });
  await expect(panel.getByTestId('palette-who')).toContainText('Ada');
  await expect(panel.getByTestId('palette-steps')).toContainText(`Starts in its own worktree on ${preview.branch}`);
  await expect(panel.locator('code')).toHaveText(preview.branch);
  await expect(panel.getByTestId('palette-steps')).toContainText('Asks you before it merges');

  // Enter assigns it (no reviewer named) and closes the menu
  await page.keyboard.press('Enter');
  await expect(menu).toBeHidden();
  await expect
    .poll(async () => (await rpc<{ task: { assignee?: string } }>('task.get', { id: task.id })).task.assignee, { timeout: 15_000 })
    .toBe(ada.id);

  // "#" narrows to the tasks, found by the key
  await page.keyboard.press('Control+k');
  await menu.getByRole('combobox', { name: 'Command' }).fill(`#${key}`);
  await expect(listbox.getByRole('option').first()).toContainText(`${key} Support hex input`);
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();

  // G then C goes to Crew
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('g');
  await page.keyboard.press('c');
  await expect(page).toHaveURL(/\/team$/);
});

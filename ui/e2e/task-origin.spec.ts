// Tasks the team lead split off (the task-origin badge), against the real core. The core marks such a task `origin: "lead:N"` and
// serves it like any other; the board, the list and the task's page say who made it and that it waits for a person. The harness
// runs no team lead to split a task, so the tasks are made through the core's own task.create, which takes the origin a split
// gives (test setup, not a stand-in: the core stores and serves them exactly as it does the lead's).
import { expect, test } from '@playwright/test';
import { appUrl, createProjectWithoutRepo, projectNav } from './helpers';

test('a task the team lead split off says so on the board, the list and its page; a task a person made does not', async ({ page }) => {
  const url = appUrl();
  const origin = new URL(url).origin;
  const token = new URL(url).searchParams.get('token') ?? '';
  const rpc = async <T>(method: string, data: unknown): Promise<T> => {
    const r = await page.request.post(`${origin}/rpc/${method}`, { headers: { 'X-Drydock-Token': token }, data });
    expect(r.ok(), `${method}: ${await r.text()}`).toBe(true);
    return ((await r.json()) as { result: T }).result;
  };

  // a project without a repository: the spec only needs tasks, so it takes none of the harness's fresh repositories
  await createProjectWithoutRepo(page, 'lead-split');
  const project = (await rpc<{ id: string; name: string }[]>('project.list', {})).find((p) => p.name === 'lead-split');
  if (!project) throw new Error('the project that was just created is not in project.list');
  await rpc('task.create', { projectId: project.id, title: 'Add a changelog entry', description: 'A person made this one.' });
  await rpc('task.create', { projectId: project.id, title: 'Follow up: cover the empty case', description: 'The lead split this off.', origin: 'lead:2' });
  await rpc('task.create', { projectId: project.id, title: 'Follow up: first step', description: 'The lead split this off too.', origin: 'lead:1' });
  // the core does not take an origin that is not a lead's: a person cannot make a task look like something else
  const refused = await page.request.post(`${origin}/rpc/task.create`, { headers: { 'X-Drydock-Token': token }, data: { projectId: project.id, title: 'Strange', origin: 'lead:99' } });
  expect(refused.ok()).toBe(false);

  // the board: the badge on the lead's follow-ups, with the step in its tooltip, none on the task a person made
  await projectNav(page, 'nav-tasks');
  const rowOf = (title: string) => page.getByTestId('task-row').filter({ hasText: title });
  await expect(rowOf('Follow up: cover the empty case').getByTestId('task-origin')).toHaveText('Split off by the team lead');
  await expect(rowOf('Follow up: cover the empty case').getByTestId('task-origin')).toHaveAttribute('title', 'Step 2 from a task you made');
  await expect(rowOf('Follow up: first step').getByTestId('task-origin')).toHaveAttribute('title', 'Step 1 from a task you made');
  await expect(rowOf('Add a changelog entry')).toHaveCount(1);
  await expect(rowOf('Add a changelog entry').getByTestId('task-origin')).toHaveCount(0);
  await expect(rowOf('Strange')).toHaveCount(0);

  // the list view says the same
  await page.getByTestId('task-view-list').click();
  await expect(rowOf('Follow up: cover the empty case').getByTestId('task-origin')).toHaveText('Split off by the team lead');
  await expect(rowOf('Add a changelog entry').getByTestId('task-origin')).toHaveCount(0);

  // the task's page: the badge, and under it what it means; the task a person made has neither
  await rowOf('Follow up: cover the empty case').getByRole('link', { name: /Follow up: cover the empty case/ }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: /Follow up: cover the empty case/ })).toBeVisible();
  const note = page.getByTestId('task-origin-note');
  await expect(note.getByTestId('task-origin')).toHaveText('Split off by the team lead');
  await expect(note).toContainText('It waits in the backlog until you move it on; the lead cannot start it by itself.');

  await projectNav(page, 'nav-tasks');
  await rowOf('Add a changelog entry').getByRole('link', { name: /Add a changelog entry/ }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: /Add a changelog entry/ })).toBeVisible();
  await expect(page.getByTestId('task-origin-note')).toHaveCount(0);
  await expect(page.getByTestId('task-origin')).toHaveCount(0);
});

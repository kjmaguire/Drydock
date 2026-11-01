// Home is the start screen (decision P142): one box, chips for where and who, Enter makes the project (when the place is new),
// its crew and the first piece of work, then lands in the project on it. Against the real core; the agent CLI is the harness's
// scripted stand-in (cmd/drydock-e2e, a TEST DOUBLE that speaks Claude Code's stream-json).
import { existsSync, mkdtempSync, realpathSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import { appUrl, freshRepo, projectItem, sidebar } from './helpers';

/** The core's own answer to an RPC, with the page's token. */
async function rpc<T>(page: Page, method: string, data: unknown): Promise<T> {
  const url = new URL(appUrl());
  const r = await page.request.post(`${url.origin}/rpc/${method}`, { headers: { 'X-Drydock-Token': url.searchParams.get('token') ?? '' }, data });
  expect(r.ok(), `${method}: ${await r.text()}`).toBe(true);
  return ((await r.json()) as { result: T }).result;
}

async function home(page: Page): Promise<void> {
  await page.goto(appUrl());
  await sidebar(page).getByTestId('nav-workspace').click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByRole('heading', { level: 1, name: 'What do you want to build?' })).toBeVisible();
}

/** The item the start landed on: /?item=<kind>:<id> (lib/project-work.ts). */
async function landedOn(page: Page, kind: 'task' | 'chat'): Promise<string> {
  await expect(page).toHaveURL(new RegExp(`/\\?item=${kind}(:|%3A)`));
  return decodeURIComponent(new URL(page.url()).searchParams.get('item')!).split(':')[1]!;
}

test('a new folder with Solo: Enter makes the project and a chat in it, sent the description', async ({ page }) => {
  const parent = realpathSync.native(mkdtempSync(join(tmpdir(), 'drydock-e2e-start-')));
  const folder = join(parent, 'csv2json');
  try {
    await home(page);
    const box = page.getByTestId('start-text');
    await expect(box).toBeFocused();
    await box.fill('A small Go CLI that converts CSV to JSON');
    // the new folder: the usual code folder and the description's first words, editable
    await expect(page.getByTestId('start-where')).toContainText('New folder: go-cli-converts');
    await page.getByTestId('start-where').click();
    await page.getByTestId('where-new-path').fill(folder);
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('start-where')).toContainText('New folder: csv2json');
    await expect(page.getByTestId('start-who')).toContainText('Solo');
    await box.press('Enter');
    const chatId = await landedOn(page, 'chat');
    await expect(projectItem(page, 'csv2json')).toHaveAttribute('aria-expanded', 'true');
    const chat = await rpc<{ chat: { projectId: string } }>(page, 'chat.get', { id: chatId });
    expect(chat.chat.projectId).toBeTruthy();
    await page.goto(new URL(`/chats/${chatId}`, page.url()).toString());
    await expect(page.getByTestId('chat-log')).toContainText('A small Go CLI that converts CSV to JSON');
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test('an opened folder with Small team: a task assigned to the builder and the reviewer, started; then the Recent project again', async ({ page }) => {
  const repo = freshRepo();
  await home(page);
  await page.getByTestId('start-text').fill('Add a Multiply function to the calculator. With tests.');
  // Open a folder...: the folder field of the wizard, in a small dialog
  await page.getByTestId('or-folder').click();
  const dialog = page.getByTestId('start-folder-dialog');
  await dialog.getByLabel('Folder', { exact: true }).fill(repo);
  await expect(dialog.getByTestId('folder-check')).toHaveAttribute('data-state', 'repository');
  await dialog.getByTestId('folder-use').click();
  await expect(page.getByTestId('start-where')).toContainText(repo.split(/[\\/]/).pop()!);
  await page.getByTestId('start-who').click();
  await page.getByTestId('who-team').click();
  await expect(page.getByTestId('start-who')).toContainText('Small team');
  await page.getByTestId('start-button').click();
  const taskId = await landedOn(page, 'task');
  const detail = await rpc<{ task: { title: string; assignee: string; reviewer: string; status: string; runId?: string } }>(page, 'task.get', { id: taskId });
  expect(detail.task.title).toBe('Add a Multiply function to the calculator');
  expect(detail.task.assignee).toBeTruthy();
  expect(detail.task.reviewer).toBeTruthy();
  expect(detail.task.assignee).not.toBe(detail.task.reviewer);
  expect(detail.task.runId).toBeTruthy();

  // the same project from Recent: its own crew, no new project, another task
  const projects = await rpc<{ id: string; name: string }[]>(page, 'project.list', {});
  const before = projects.length;
  const name = repo.split(/[\\/]/).pop()!;
  const id = projects.find((p) => p.name === name)!.id;
  await home(page);
  await page.getByTestId('start-where').click();
  await page.locator(`[data-testid="where-recent"][data-project="${id}"]`).click();
  // the chips remembered for this project: its crew
  await expect(page.getByTestId('start-who')).toContainText('Project crew');
  await expect(page.getByTestId('start-model')).toHaveCount(0);
  await page.getByTestId('start-text').fill('Add a Divide function');
  await page.getByTestId('start-text').press('Enter');
  const second = await landedOn(page, 'task');
  expect(second).not.toBe(taskId);
  expect((await rpc<unknown[]>(page, 'project.list', {})).length).toBe(before);
});

test('Just chat: a chat without a project, sent what was typed', async ({ page }) => {
  await home(page);
  await page.getByTestId('start-text').fill('What is a monad, in one line?');
  await page.getByTestId('or-chat').click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  await expect(page.getByTestId('chat-log')).toContainText('What is a monad, in one line?');
});

test('New and Ctrl+N open the start screen; it has no way to the old wizard (P143)', async ({ page }) => {
  await page.goto(appUrl());
  await sidebar(page).getByTestId('nav-chats').click();
  await sidebar(page).getByTestId('nav-new').click();
  await expect(page).toHaveURL(/\/home$/);
  await expect(page.getByTestId('start-text')).toBeFocused();
  await expect(page.getByTestId('start-more')).toHaveCount(0);
  await page.getByTestId('start-where').click();
  await expect(page.getByTestId('where-new')).toBeVisible();
  await expect(page.getByTestId('where-more')).toHaveCount(0);
});

// New project (decision P143): a button beside Home's heading opens a small dialog, a name and where the code lives. Create makes
// the project with the start box's defaults and starts nothing; the person lands on its Work.
test('New project: a name and a new folder make a project with nothing started, and it opens on Work', async ({ page }) => {
  const parent = realpathSync.native(mkdtempSync(join(tmpdir(), 'drydock-e2e-new-')));
  const folder = join(parent, 'pay-portal');
  try {
    await home(page);
    await page.getByTestId('home-new-project').click();
    const dialog = page.getByTestId('new-project-dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('radiogroup', { name: 'Where its code lives' }).getByRole('radio', { name: 'New folder' })).toHaveAttribute('aria-checked', 'true');
    await dialog.getByLabel('Project name', { exact: true }).fill('Pay Portal');
    // the new folder follows the name, in the usual code folder; it can be changed
    await expect(dialog.getByTestId('new-project-folder')).toHaveValue(/[\\/]pay-portal$/);
    await dialog.getByTestId('new-project-folder').fill(folder);
    await dialog.getByTestId('new-project-create').click();
    await expect(page.getByTestId('work')).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
    await expect(projectItem(page, 'Pay Portal')).toHaveAttribute('aria-expanded', 'true');
    expect(existsSync(join(folder, '.git'))).toBe(true);
    // nothing started: no task, no chat; the crew is Solo's builder
    const project = (await rpc<{ id: string; name: string }[]>(page, 'project.list', {})).find((p) => p.name === 'Pay Portal')!;
    expect((await rpc<unknown[] | null>(page, 'task.list', { projectId: project.id })) ?? []).toHaveLength(0);
    expect((await rpc<unknown[] | null>(page, 'chat.list', { projectId: project.id })) ?? []).toHaveLength(0);
    await expect(page.getByTestId('work-agent')).toHaveCount(1);
    await expect(page.getByTestId('work-empty')).toBeVisible();
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test('New project: Open a folder names the project after it; the old wizard address and Ctrl+K open the same dialog', async ({ page }) => {
  const repo = freshRepo();
  const name = repo.split(/[\\/]/).pop()!;
  await page.goto(appUrl());
  await sidebar(page).waitFor();
  // the five-step wizard's address keeps working: it opens New project on Home
  await page.goto(new URL('/project/new', page.url()).toString());
  await expect(page).toHaveURL(/\/home$/);
  const dialog = page.getByTestId('new-project-dialog');
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  // Ctrl+K: New project
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press('Control+k');
  const menu = page.getByRole('dialog', { name: 'Command menu' });
  await menu.getByRole('combobox', { name: 'Command' }).fill('>New project');
  await menu.getByRole('listbox', { name: 'Commands' }).getByRole('option', { name: /New project/ }).first().click();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('radio', { name: 'Open a folder' }).click();
  await dialog.getByLabel('Folder', { exact: true }).fill(repo);
  await expect(dialog.getByTestId('folder-check')).toHaveAttribute('data-state', 'repository');
  await expect(dialog.getByLabel('Project name', { exact: true })).toHaveValue(name);
  // a typed name wins (the harness's repositories share one folder name)
  await dialog.getByLabel('Project name', { exact: true }).fill('opened-folder');
  await dialog.getByTestId('new-project-create').click();
  await expect(page.getByTestId('work')).toBeVisible();
  await expect(projectItem(page, 'opened-folder')).toHaveAttribute('aria-expanded', 'true');
});

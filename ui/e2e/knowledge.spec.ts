// The Knowledge hub (decision P64) against the real core: the source cards count what the project really has, the
// requirements panel shows how far a task covers each one, the latest memory note is there with who wrote it, and a
// question in the ask box starts a chat in the project with that question. The agent CLI is the harness's scripted
// stand-in (cmd/drydock-e2e, a TEST DOUBLE that speaks Claude Code's stream-json): it answers a message carrying
// "drydock-e2e chat".
import { expect, test } from '@playwright/test';
import { createProject, projectNav, sidebar } from './helpers';

test('Knowledge: cards with real counts, requirement coverage, the latest note, and a question that starts a chat', async ({ page }) => {
  await createProject(page, 'gocalc-knowledge');

  // one requirement, no task linked yet
  await projectNav(page, 'nav-requirements');
  await expect(page.getByRole('heading', { name: 'Add requirements' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Checkout');
  await page.getByLabel('Text', { exact: true }).fill('# Checkout\n\n- Given a paid cart, when the order is placed, then an order record exists\n');
  await page.getByRole('button', { name: 'Add requirement' }).click();
  await expect(page.getByTestId('requirement-detail')).toBeVisible();

  // one memory note, accepted by the person
  await projectNav(page, 'nav-memory');
  await expect(page.getByRole('heading', { name: 'Propose an entry' })).toBeVisible();
  await page.getByLabel('Title', { exact: true }).fill('Use gofmt');
  await page.getByLabel('Body').fill('Run gofmt before every commit.');
  await page.getByRole('button', { name: 'Propose' }).click();
  const mem = page.locator('[data-testid=memory][data-title="Use gofmt"]');
  await mem.getByRole('button', { name: 'Accept', exact: true }).click();
  await expect(mem).toHaveAttribute('data-status', 'accepted');

  // the hub: every card counts what is really there and links to its page
  await sidebar(page).getByTestId('nav-knowledge').click();
  const hub = page.getByTestId('knowledge');
  await expect(hub).toBeVisible();
  await expect(hub.getByRole('heading', { level: 1 })).toContainText('Knowledge');
  await expect(hub).toContainText('What the crew knows about gocalc-knowledge');
  const requirements = page.getByTestId('knowledge-requirements');
  await expect(requirements).toHaveAttribute('data-count', '1');
  await expect(requirements).toContainText('none covered by a task yet');
  await expect(requirements).toHaveAttribute('href', '/requirements');
  const memory = page.getByTestId('knowledge-memory');
  await expect(memory).toHaveAttribute('data-count', '1');
  await expect(memory).toContainText('1 accepted');
  await expect(page.getByTestId('knowledge-packs')).toHaveAttribute('data-count', '0');
  await expect(page.getByTestId('knowledge-decisions')).toHaveAttribute('data-count', '0');
  await expect(page.getByTestId('knowledge-decisions')).toHaveAttribute('href', /^\/room\/room_/);
  await expect(page.getByTestId('knowledge-repo-map')).toHaveAttribute('href', '/repo-map');

  // the requirements panel: the requirement, with no task yet; the latest note with who accepted it
  const row = page.getByTestId('knowledge-requirement');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('Checkout');
  await expect(row).toHaveAttribute('data-coverage', 'no task yet');
  await expect(row.getByTestId('knowledge-coverage')).toHaveText('no task yet');
  const note = page.getByTestId('knowledge-note');
  await expect(note).toHaveCount(1);
  await expect(note).toContainText('Use gofmt');
  await expect(note).toContainText('convention');
  await expect(note).toHaveAttribute('data-status', 'accepted');

  // a question starts a chat in the project with that question; the stand-in answers it there
  const ask = page.getByLabel('Ask anything about gocalc-knowledge');
  await ask.fill('drydock-e2e chat: where do we round currency?');
  await ask.press('Enter');
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const log = page.getByTestId('chat-log');
  await expect(log).toContainText('drydock-e2e chat: where do we round currency?');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();
  // in the project's folder, not the private folder a chat without a project gets
  await page.getByTestId('chat-more').click();
  await page.getByRole('button', { name: 'Properties' }).click();
  await expect(page.getByTestId('chat-cwd')).toBeVisible();
  await expect(page.getByTestId('chat-cwd')).not.toHaveAttribute('title', /[\\/]chats[\\/]/);
});

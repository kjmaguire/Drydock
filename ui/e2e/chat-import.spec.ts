// Import from Claude Code (decision P109) against the real core. The harness (cmd/drydock-e2e) points CLAUDE_CONFIG_DIR at a
// fixture Claude Code config folder with one session transcript and a stand-in sign-in file; the agent CLI is the harness's
// scripted stand-in (a TEST DOUBLE that speaks Claude Code's stream-json and continues a session it is given with --resume).
import { expect, test } from '@playwright/test';
import { appUrl, workspaceNav } from './helpers';

test('a Claude Code chat is imported once, opens with its messages, and continues its session', async ({ page }) => {
  await page.goto(appUrl());
  await workspaceNav(page, 'nav-chats');
  const list = page.getByRole('complementary', { name: 'Chats' });
  await list.getByTestId('chat-import-open-dialog').click();
  const dialog = page.getByRole('dialog', { name: 'Import from Claude Code' });
  const row = dialog.getByTestId('chat-import-row').filter({ hasText: 'Notes from Claude Code' });
  await expect(row).toContainText('Windows');
  await expect(row).toContainText('2 messages');
  await expect(row.getByRole('checkbox')).toBeChecked();
  await dialog.getByRole('button', { name: 'Import 1 chat' }).click();
  await expect(dialog.getByTestId('chat-import-result')).toHaveText('Imported 1 chat.');
  await expect(row).toContainText('imported');
  await expect(row.getByRole('checkbox')).toBeDisabled();

  await row.getByRole('button', { name: 'Open the chat' }).click();
  await expect(dialog).toBeHidden();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const log = page.getByTestId('chat-log');
  await expect(log.getByText('What is in my notes folder?')).toBeVisible();
  await expect(log.getByText('A shopping list and a draft.')).toBeVisible();
  await expect(log).toContainText('Imported from Claude Code');

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('drydock-e2e chat: and after the import?');
  await message.press('Enter');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');
  await expect(log.getByText('Continued in a new session')).toHaveCount(0);

  // a second import finds it imported already
  await list.getByTestId('chat-import-open-dialog').click();
  const again = page.getByRole('dialog', { name: 'Import from Claude Code' });
  await expect(again.getByTestId('chat-import-row').filter({ hasText: 'Notes from Claude Code' })).toContainText('imported');
  await expect(again.getByRole('button', { name: 'Import', exact: true })).toBeDisabled();
});

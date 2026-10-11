// Chats against the real core. The agent CLI is the harness's scripted stand-in (cmd/drydock-e2e, a TEST DOUBLE that
// speaks Claude Code's stream-json): it answers messages that carry "drydock-e2e chat", refuses a Bash call for
// "drydock-e2e touch" in the default permission mode, and runs it once the rule is allowed and Retry sends the retry text.
import { expect, test } from '@playwright/test';
import { createProject, sidebar } from './helpers';

test('a chat keeps its history across a reload, continues its session, and a refused tool can be allowed and retried', async ({ page }) => {
  await createProject(page, 'chats-e2e');
  await sidebar(page).getByTestId('nav-chats').click();
  await expect(page.getByRole('complementary', { name: 'Chats' })).toBeVisible();

  // new chat: the current project, Claude Code, reading other chats on by default
  await page.getByTestId('chat-new').click();
  const dialog = page.getByRole('dialog', { name: 'New chat' });
  await expect(dialog.getByLabel('Project', { exact: true })).toHaveValue(/prj_/);
  await expect(dialog.getByLabel('Agent CLI', { exact: true })).toHaveValue('claude');
  await expect(dialog.getByLabel(/Let this chat read my other chats/)).toBeChecked();
  await dialog.getByRole('button', { name: 'Start chat' }).click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const chatUrl = page.url();

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('drydock-e2e chat: hello there');
  await message.press('Control+Enter');
  const log = page.getByTestId('chat-log');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');
  // the first line became the title, in the header and the list
  await expect(page.getByTestId('chat-title')).toContainText('drydock-e2e chat: hello there');
  await expect(page.getByTestId('chat-row').first()).toContainText('drydock-e2e chat: hello there');

  // the history is kept
  await page.reload();
  await expect(page).toHaveURL(chatUrl);
  await expect(log.getByText('drydock-e2e chat: hello there')).toBeVisible();
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();

  // the next message continues the same session (no fallback note)
  await message.fill('drydock-e2e chat: and again');
  await message.press('Control+Enter');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toHaveCount(2);
  // the reply shows before the turn ends, and the composer sends nothing while a turn runs: wait for the end
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');
  await expect(page.getByText('could not be continued')).toHaveCount(0);
  await expect(log.getByText('Continued in a new session')).toHaveCount(0);

  // a refused tool: allow the suggested rule for this chat, then Retry
  await message.fill('drydock-e2e touch a probe file');
  await message.press('Control+Enter');
  await expect(message).toHaveValue(''); // sent
  const denied = page.getByTestId('chat-denied');
  await expect(denied).toContainText('Claude was not allowed to run Bash');
  await expect(denied).toContainText('Bash(touch chat_probe.txt)');
  await denied.getByRole('button', { name: 'Allow for this chat' }).click();
  await expect(denied).toContainText('Allowed for this chat');
  await expect(page.getByRole('button', { name: 'Allowed tools (1)' })).toBeVisible();
  await denied.getByRole('button', { name: 'Retry' }).click();
  await expect(log.getByText('Done: chat_probe.txt is there now.')).toBeVisible();
  await expect(page.getByTestId('chat-denied')).toHaveCount(1); // the retry ran the tool without a new refusal
});

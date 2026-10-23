// Chats against the real core. The agent CLI is the harness's scripted stand-in (cmd/drydock-e2e, a TEST DOUBLE that
// speaks Claude Code's stream-json): it answers messages that carry "drydock-e2e chat", refuses a Bash call for
// "drydock-e2e touch" in the default permission mode, and runs it once Allow and retry allows the rule and sends the retry text.
import { expect, test } from '@playwright/test';
import { choose, createProject, createProjectWithoutRepo, workspaceNav } from './helpers';

test('a chat keeps its history across a reload, continues its session, and a refused tool can be allowed and retried', async ({ page }) => {
  await createProject(page, 'chats-e2e');
  await workspaceNav(page, 'nav-chats');
  await expect(page.getByRole('complementary', { name: 'Chats' })).toBeVisible();

  // new chat: the current project, Claude Code, reading other chats on by default
  await page.getByTestId('chat-new').click();
  const dialog = page.getByRole('dialog', { name: 'New chat' });
  await expect(dialog.getByLabel('Project', { exact: true })).toHaveAttribute('data-value', /prj_/);
  await expect(dialog.getByLabel('Agent CLI', { exact: true })).toHaveAttribute('data-value', 'claude');
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

  // a refused tool: allow the suggested rule for this chat and retry
  await message.fill('drydock-e2e touch a probe file');
  await message.press('Control+Enter');
  await expect(message).toHaveValue(''); // sent
  const denied = page.getByTestId('chat-denied');
  await expect(denied).toContainText('Claude was not allowed to run Bash');
  await expect(denied).toContainText('Bash(touch chat_probe.txt)');
  // Allow and retry (UI review P1-3): the rule is allowed and the turn runs again in one step
  await denied.getByRole('button', { name: 'Allow and retry' }).click();
  await expect(denied).toContainText('Allowed for this chat');
  await expect(page.getByRole('button', { name: 'Allowed tools (1)' })).toBeVisible();
  await expect(log.getByText('Done: chat_probe.txt is there now.')).toBeVisible();
  // the retry Drydock sent shows as a note, not as words the person typed
  await expect(log.locator('[data-retry="true"]')).toHaveText('You allowed the tool and asked to continue.');
  await expect(page.getByTestId('chat-denied')).toHaveCount(1); // the retry ran the tool without a new refusal
});

// The graphite Chats screen (decision P97) against the real core: Ctrl+N starts a chat, here without a project; the header says
// where it runs; "Move to a project" moves it into one (chat.update projectId), the header and the list follow, the move is a
// note in the conversation, and the chat answers in the project's folder in a new session given the conversation so far.
test('Ctrl+N starts a chat without a project, and Move to a project moves it into one, where it answers', async ({ page }) => {
  await createProjectWithoutRepo(page, 'chats-move-e2e');
  await workspaceNav(page, 'nav-chats');
  const list = page.getByRole('complementary', { name: 'Chats' });
  await expect(list).toBeVisible();
  await page.locator('body').press('Control+n');
  const dialog = page.getByRole('dialog', { name: 'New chat' });
  await expect(dialog).toBeVisible();
  await choose(dialog.getByLabel('Project', { exact: true }), { label: 'No project' });
  await dialog.getByRole('button', { name: 'Start chat' }).click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  await expect(page.getByTestId('chat-place')).toHaveText('No project · runs in its own folder');
  await expect(page.getByTestId('chip-mode')).toHaveText('Asks before commands');

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('drydock-e2e chat: before the move');
  await message.press('Enter');
  const log = page.getByTestId('chat-log');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');

  await page.getByTestId('chat-move').click();
  const move = page.getByRole('dialog', { name: 'Move to a project' });
  await choose(move.getByLabel('Project', { exact: true }), { label: 'chats-move-e2e' });
  await expect(move.getByTestId('chat-move-effect')).toContainText("It joins chats-move-e2e and runs in the project's folder.");
  await move.getByRole('button', { name: 'Move' }).click();
  await expect(move).toBeHidden();
  await expect(page.getByTestId('chat-place')).toHaveText("chats-move-e2e · runs in the project's folder");
  await expect(page.getByTestId('chat-move')).toHaveText('Move to another project');
  await expect(log).toContainText('Now in a project.');
  // the list shows the current project's chats: the moved chat is among them, its line names the project
  const row = list.getByTestId('chat-row').filter({ hasText: 'drydock-e2e chat: before the move' });
  await expect(row).toContainText('chats-move-e2e · Claude Code');
  await expect(list.getByTestId('chat-list-open')).toHaveCount(0);

  await message.fill('drydock-e2e chat: after the move');
  await message.press('Enter');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toHaveCount(2);
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');
});

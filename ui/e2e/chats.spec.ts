// Chats against the real core. The agent CLI is the harness's scripted stand-in (cmd/drydock-e2e, a TEST DOUBLE that
// speaks Claude Code's stream-json): it answers messages that carry "drydock-e2e chat", refuses a Bash call for
// "drydock-e2e touch" in the default permission mode, and runs it once Allow and retry allows the rule and sends the retry text.
import { expect, test, type Page } from '@playwright/test';
import { appUrl, choose, createProject, createProjectWithoutRepo, workspaceNav } from './helpers';

/** The chat's ⋯ panel (decision P139): its CLI and state, where it runs, Allowed tools, Properties, Fork, Move to a project. */
async function openMore(page: Page) {
  await page.getByTestId('chat-more').click();
  await expect(page.getByTestId('chat-more-panel').getByTestId('chat-place')).toBeVisible();
}
async function closeMore(page: Page) {
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('chat-place')).toHaveCount(0);
}

test('a chat keeps its history across a reload, continues its session, and a refused tool can be allowed and retried', async ({ page }) => {
  await createProject(page, 'chats-e2e');
  await workspaceNav(page, 'nav-chats');
  await expect(page.getByRole('complementary', { name: 'Chats' })).toBeVisible();

  // New chat opens the chat at once (decision P107): no project, Claude Code, its own folder; no dialog first
  await page.getByTestId('chat-new').click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  await expect(page.getByRole('dialog', { name: 'New chat' })).toHaveCount(0);
  await expect(page.getByTestId('chip-adapter')).toContainText('Claude Code');
  await openMore(page);
  await expect(page.getByTestId('chat-place')).toHaveText('No project · runs in its own folder');
  await closeMore(page);
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
  await openMore(page);
  await expect(page.getByRole('button', { name: 'Allowed tools (1)' })).toBeVisible();
  await closeMore(page);
  await expect(log.getByText('Done: chat_probe.txt is there now.')).toBeVisible();
  // the retry Drydock sent shows as a note, not as words the person typed
  await expect(log.locator('[data-retry="true"]')).toHaveText('You allowed the tool and asked to continue.');
  await expect(page.getByTestId('chat-denied')).toHaveCount(1); // the retry ran the tool without a new refusal
});

// The graphite Chats screen (decision P97) against the real core: Ctrl+N starts a chat, without a project (P107); the header says
// where it runs; "Move to a project" moves it into one (chat.update projectId), the header and the list follow, the move is a
// note in the conversation, and the chat answers in the project's folder in a new session given the conversation so far.
test('Ctrl+N starts a chat without a project, and Move to a project moves it into one, where it answers', async ({ page }) => {
  await createProjectWithoutRepo(page, 'chats-move-e2e');
  await workspaceNav(page, 'nav-chats');
  const list = page.getByRole('complementary', { name: 'Chats' });
  await expect(list).toBeVisible();
  await page.locator('body').press('Control+n');
  await expect(page).toHaveURL(/\/chats\/chat_/);
  await expect(page.getByTestId('chip-mode')).toHaveText('Asks before commands');

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('drydock-e2e chat: before the move');
  await message.press('Enter');
  const log = page.getByTestId('chat-log');
  await expect(log.getByText('Hello from the scripted chat stand-in')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');

  await openMore(page);
  await expect(page.getByTestId('chat-place')).toHaveText('No project · runs in its own folder');
  await page.getByTestId('chat-move').click();
  const move = page.getByRole('dialog', { name: 'Move to a project' });
  await choose(move.getByLabel('Project', { exact: true }), { label: 'chats-move-e2e' });
  await expect(move.getByTestId('chat-move-effect')).toContainText("It joins chats-move-e2e and runs in the project's folder.");
  await move.getByRole('button', { name: 'Move' }).click();
  await expect(move).toBeHidden();
  // the header's chip names the project now (P139); ⋯ says where it runs
  await expect(page.getByTestId('chat-folder')).toHaveText('chats-move-e2e');
  await openMore(page);
  await expect(page.getByTestId('chat-place')).toHaveText("chats-move-e2e · runs in the project's folder");
  await expect(page.getByTestId('chat-move')).toHaveText('Move to another project');
  await closeMore(page);
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

// The calm chat (decision P139) against the real core. The scripted stand-in (cmd/drydock-e2e, a TEST DOUBLE) answers
// "drydock-e2e calm" with two reads, a command and an edit, then an offer; "drydock-e2e calm fail" with a command that fails.
test('the calm chat: a new chat is the composer; tool calls fold into one line, edits into one card; replies follow an offer; Ctrl+O shows every call', async ({ page }) => {
  await page.goto(appUrl());
  await workspaceNav(page, 'nav-chats');
  await page.getByTestId('chat-new').click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  // a new chat is just the composer, with the cursor in it: no paragraph
  const message = page.getByLabel('Message', { exact: true });
  await expect(message).toBeFocused();
  await expect(page.getByTestId('chat-scroll').locator('p')).toHaveCount(0);
  await expect(page.getByTestId('chat-cli-warning')).toHaveCount(0);

  await message.fill('drydock-e2e calm: subtract');
  await message.press('Enter');
  const log = page.getByTestId('chat-log');
  await expect(log.getByText('Want me to add a test for it?')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');

  // the four calls are one line, opened to their cards and folded again
  const burst = page.getByTestId('chat-burst');
  await expect(burst).toHaveCount(1);
  const line = burst.getByTestId('chat-burst-toggle');
  await expect(line).toContainText('Read 2 files, edited 1 file, ran 1 command');
  await expect(page.getByTestId('chat-tool')).toHaveCount(0);
  await line.click();
  await expect(page.getByTestId('chat-tool')).toHaveCount(4);
  await line.click();
  await expect(page.getByTestId('chat-tool')).toHaveCount(0);

  // the turn's edits: one card, Review changes shows the diff
  const changes = page.getByTestId('chat-changes');
  await expect(changes).toContainText('Changed 1 file');
  await changes.getByRole('button', { name: 'Review changes' }).click();
  const review = page.getByRole('dialog', { name: 'Changed 1 file' });
  await expect(review.getByTestId('chat-changes-file')).toHaveAttribute('data-path', 'calc.go');
  await expect(review).toContainText('+ return a - b');
  await page.keyboard.press('Escape');
  await expect(review).toBeHidden();

  // Ctrl+O: every call shown, kept across a reload, and folded again
  await expect(page.getByTestId('chat-verbose-hint')).toHaveText('Ctrl O shows every tool call');
  await page.locator('body').press('Control+o');
  await expect(page.getByTestId('chat-tool')).toHaveCount(4);
  await page.reload();
  await expect(page.getByTestId('chat-tool')).toHaveCount(4);
  await expect(page.getByTestId('chat-verbose-hint')).toHaveText('Ctrl O hides the tool calls');
  await page.locator('body').press('Control+o');
  await expect(page.getByTestId('chat-tool')).toHaveCount(0);

  // the replies the offer invites; one is sent at once
  const replies = page.getByTestId('chat-replies');
  await expect(replies.getByRole('button')).toHaveText(['Yes, add it', 'Not now', 'Explain more']);
  await replies.getByRole('button', { name: 'Yes, add it' }).click();
  await expect(log.getByText('Yes, please add a test for it.')).toBeVisible();
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept');

  // a failed call opens its line by itself, and its card
  await message.fill('drydock-e2e calm fail');
  await message.press('Enter');
  const failed = page.locator('[data-testid="chat-burst"][data-state="failed"]');
  await expect(failed).toHaveAttribute('data-expanded', 'true');
  await expect(failed.getByTestId('chat-burst-toggle')).toContainText('1 failed');
  await expect(failed.locator('[data-testid="chat-tool"][data-state="failed"]')).toHaveAttribute('open', '');
});

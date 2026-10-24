// The WSL run mode end to end (decision P41), against the real core with a stand-in WSL: the harness (cmd/drydock-e2e) gives
// it one distro, "Ubuntu", whose `claude` is a LABELLED TEST DOUBLE that answers in stream-json (internal/wsl/wsltest). The
// stand-in exists only on a Linux host; elsewhere these tests are skipped (CI's wsl job drives a real distro instead).
import { rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { choose, createProject, openNewAgent, optionsOf, workspaceNav, projectNav } from './helpers';

const distro = (): string => process.env.DRYDOCK_E2E_WSL ?? '';

test('Agents: Claude Code inside a WSL distro is its own row with its own sign-in, and its Test runs it in the distro', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-conn');
  await workspaceNav(page, 'nav-connections');
  await expect(page.getByRole('heading', { level: 1, name: 'Agents' })).toBeVisible();
  // a row of its own in the list, "Claude Code WSL" without the distro (the detail's Runs fact names it); the row opens its
  // detail beside the list and puts it in the address
  const row = page.locator(`[data-testid=agent-row][data-key="claude@wsl:${distro()}"]`);
  await expect(row).toBeVisible({ timeout: 60_000 });
  await expect(row).toHaveAttribute('data-run-mode', 'wsl');
  await expect(row).toContainText('Claude Code WSL');
  await expect(row).toContainText('WSL · ');
  await expect(row).not.toContainText(distro());
  await row.click();
  await expect(page).toHaveURL(/\/agents\/claude%40wsl%3A/);
  const card = page.locator(`[data-testid=agent-detail][data-key="claude@wsl:${distro()}"]`);
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute('data-run-mode', 'wsl');
  await expect(card.getByRole('heading', { level: 2 })).toHaveText('Claude Code WSL');
  await expect(card.locator('[data-testid=agent-facts] dt:has-text("Runs") + dd')).toContainText(`Inside the WSL 2 distro ${distro()}`);
  // its own sign-in (decision P50): the detail starts claude's login inside the distro from a button (driven in the next test)
  await expect(card.getByTestId('sign-in')).toHaveAttribute('title', `Runs claude auth login inside ${distro()}`);
  await expect(card).toContainText("this install's sign-in is separate from the Windows one");
  await expect(card.getByTestId('wsl-facts')).toContainText('Windows drives at');

  // the address opens it again after a reload
  await page.reload();
  await expect(card).toBeVisible({ timeout: 60_000 });

  await card.getByTestId('test-connection').click();
  const confirm = page.getByRole('dialog', { name: 'Test connection' });
  await expect(confirm).toContainText(`inside the WSL distro ${distro()}`);
  await confirm.getByRole('button', { name: 'Run the test' }).click();
  const report = card.getByTestId('connection-report');
  await expect(report).toBeVisible({ timeout: 90_000 });
  await expect(report).toContainText('T2 Start and handshake: pass');
  await expect(report).toContainText('T5 Stop and process cleanup: pass');
  await expect(report).toContainText('killed by its marker');

  // the Windows install is still there, a row of its own, and says where it runs
  await page.locator('[data-testid=agent-row][data-key=claude]').click();
  await expect(page.locator('[data-testid=agent-detail][data-key=claude] [data-testid=agent-facts] dt:has-text("Runs") + dd')).toHaveText('On Windows');
});

test("Agents: Sign in runs the CLI's own login inside the distro in a terminal in the detail, and the detail re-checks itself", async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-signin');
  await workspaceNav(page, 'nav-connections');
  const row = page.locator(`[data-testid=agent-row][data-key="claude@wsl:${distro()}"]`);
  const card = page.locator(`[data-testid=agent-detail][data-key="claude@wsl:${distro()}"]`);
  const signedIn = card.locator('[data-testid=agent-facts] dt:has-text("Signed in") + dd');
  await row.click({ timeout: 60_000 });
  const home = (await card.getByTestId('wsl-facts').locator('dt:has-text("Linux home") + dd').textContent())?.trim() ?? '';
  expect(home).toMatch(/^\//);
  // the stand-in claude (a LABELLED TEST DOUBLE) is signed out until its own login runs
  const signedOut = join(home, '.drydock-standin-signed-out');
  writeFileSync(signedOut, '');
  try {
    await page.getByTestId('check-all').click();
    await expect(signedIn).toHaveText('No', { timeout: 60_000 });
    await expect(row).toContainText('Sign in');
    await expect(card.getByTestId('agent-notice')).toContainText(`Not signed in inside ${distro()}`);

    await card.getByTestId('sign-in').click();
    const box = card.getByTestId('sign-in-terminal');
    await expect(box).toBeVisible();
    await expect(box.getByTestId('sign-in-state')).toHaveText('claude auth login is running: answer it here.');
    // it runs as an agent runs in the distro: the run marker, the login shell's PATH, and a terminal for its input
    await expect(box.locator('.xterm-rows')).toContainText('marker=yes login-path=yes tty=yes', { timeout: 30_000 });
    await expect(box.locator('.xterm-rows')).toContainText('Press Enter to finish signing in');
    await box.getByTestId('xterm').click();
    await page.keyboard.press('Enter');
    await expect(box.locator('.xterm-rows')).toContainText('Signed in to the stand-in.', { timeout: 30_000 });
    // the end of the sign-in checks the install afresh (connection.check, past the probe cache): signed in now, without a
    // reload, with the account the stand-in names
    await expect(box.getByTestId('sign-in-state')).toHaveText('The sign-in finished. Signed in as standin@example.invalid (claude.ai).', { timeout: 60_000 });
    await expect(card.getByTestId('signed-in')).toHaveText('Yes, as standin@example.invalid (claude.ai)');
    await expect(signedIn).toHaveText('Yes, as standin@example.invalid (claude.ai)', { timeout: 60_000 });
    await expect(row).not.toContainText('Sign in', { timeout: 60_000 });
    await expect(card.getByTestId('sign-in')).toHaveText('Sign in again');
    await box.getByTestId('sign-in-close').click();
    await expect(box).toHaveCount(0);
    // signed out again inside the distro: "Check again" asks the CLI afresh
    writeFileSync(signedOut, '');
    await card.getByTestId('sign-in-check').click();
    await expect(signedIn).toHaveText('No', { timeout: 60_000 });
    await expect(card.getByTestId('signed-in')).toHaveCount(0);
  } finally {
    rmSync(signedOut, { force: true });
  }
});

test('Chats: a chat with no project runs Claude Code inside the WSL distro, in a private folder in its home, and gets an answer', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-chat');
  await workspaceNav(page, 'nav-chats');
  await page.getByTestId('chat-new').click();
  const dialog = page.getByRole('dialog', { name: 'New chat' });
  await choose(dialog.getByLabel('Project', { exact: true }), '');
  // the distro's install is its own choice, named as on the Agents page (its Connection is probed when the dialog opens)
  const agent = dialog.getByLabel('Agent CLI', { exact: true });
  const choice = await optionsOf(agent, `claude@wsl:${distro()}`);
  await expect(choice).toHaveText(/^Claude Code WSL 2\.1\.306$/, { timeout: 60_000 }); // one distro: not named
  await expect(choice).toBeEnabled();
  await expect(await optionsOf(agent, 'claude')).toContainText('Claude Code on Windows');
  await choose(agent, `claude@wsl:${distro()}`);
  await expect(dialog).toContainText(`Runs inside the WSL distro ${distro()}`);
  await dialog.getByRole('button', { name: 'Start chat' }).click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const chatId = page.url().split('/').pop() ?? '';
  await expect(page.getByTestId('chip-adapter')).toContainText('Claude Code WSL');
  await expect(page.getByTestId('chip-adapter')).toHaveAttribute('title', `Runs inside the WSL distro ${distro()}`);
  // the header (graphite, P97): the folder as the distro names it (a Linux path, its middle cut when long: the stand-in's home is
  // a temporary folder), and where the chat runs in words
  await expect(page.getByTestId('chat-folder')).toHaveText(new RegExp(`^[~/][^\\\\]*/chats/${chatId}$`));
  await expect(page.getByTestId('chat-place')).toHaveText('No project · runs inside the distro');
  // the private folder inside the distro, named the Windows way
  await page.getByRole('button', { name: 'Properties' }).click();
  await expect(page.getByTestId('chat-cwd')).toHaveAttribute('title', new RegExp(`^\\\\\\\\wsl\\.localhost\\\\${distro()}\\\\.*\\\\\\.drydock\\\\chats\\\\${chatId}$`));
  await page.getByRole('dialog', { name: 'Chat properties' }).getByRole('button', { name: 'Cancel' }).click();

  const message = page.getByLabel('Message', { exact: true });
  await message.fill('hello from a chat in WSL');
  await message.press('Control+Enter');
  // the stand-in claude (a LABELLED TEST DOUBLE) says where it ran: in the distro's home folder for this chat, with the run
  // marker, and with Drydock's tools reached through the Windows shim
  await expect(page.getByTestId('chat-status')).toHaveText('Session kept', { timeout: 60_000 });
  const answer = page.getByTestId('chat-log').getByText(/standin turn=1/);
  await expect(answer).toBeVisible();
  await expect(answer).toContainText(`/.drydock/chats/${chatId} `);
  await expect(answer).toContainText('run=set');
  await expect(answer).toContainText('mcpcmdok=yes');
});

test('Team: a Claude Code agent is made to run inside the WSL distro and its card says so', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-team');
  await openNewAgent(page);
  await page.getByLabel('Agent name').fill('Linux worker');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  const runs = page.getByLabel('Runs', { exact: true });
  await expect(await optionsOf(runs)).toHaveCount(3);
  await choose(runs, `wsl:${distro()}`);
  await expect(page.getByTestId('run-mode')).toContainText('one inside the distro is fast');
  await page.getByRole('button', { name: 'Create agent' }).click();
  const card = page.getByTestId('agent').filter({ hasText: 'Linux worker' });
  await expect(card.getByTestId('agent-runs')).toHaveText(/^Claude Code WSL · /);
  await expect(card.getByTestId('agent-runs')).toHaveAttribute('title', `Role: backend. Runs inside the WSL distro ${distro()}`);

  // the project's default for agents that do not choose
  await projectNav(page, 'nav-project');
  const panel = page.getByTestId('run-mode-panel');
  await expect(panel).toBeVisible();
  await choose(panel.getByLabel('Agents run'), `wsl:${distro()}`);
  await expect(panel.getByLabel('Agents run')).toBeEnabled();
  await expect(panel.getByRole('alert')).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId('run-mode-panel').getByLabel('Agents run')).toHaveAttribute('data-value', `wsl:${distro()}`);
});

// The WSL run mode end to end (decision P41), against the real core with a stand-in WSL: the harness (cmd/drydock-e2e) gives
// it one distro, "Ubuntu", whose `claude` is a LABELLED TEST DOUBLE that answers in stream-json (internal/wsl/wsltest). The
// stand-in exists only on a Linux host; elsewhere these tests are skipped (CI's wsl job drives a real distro instead).
import { rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { choose, createProject, optionsOf, workspaceNav, projectNav } from './helpers';

const distro = (): string => process.env.DRYDOCK_E2E_WSL ?? '';

test('Connections: Claude Code inside a WSL distro is its own card with its own sign-in, and its Test runs it in the distro', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-conn');
  await workspaceNav(page, 'nav-connections');
  // a tile of its own on the overview, "Claude Code WSL" with its distro on the line below; the tile opens its card below the grid
  const tile = page.locator(`[data-testid=connection-tile][data-key="claude@wsl:${distro()}"]`);
  await expect(tile).toBeVisible({ timeout: 60_000 });
  await expect(tile).toHaveAttribute('data-run-mode', 'wsl');
  await expect(tile).toContainText('Claude Code WSL');
  await expect(tile).toContainText(`in the WSL distro ${distro()}`);
  await tile.click();
  const card = page.locator(`[data-testid=connection][data-key="claude@wsl:${distro()}"]`);
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute('data-run-mode', 'wsl');
  await expect(card.getByRole('heading', { level: 2 })).toHaveText('Claude Code WSL');
  await expect(card).toContainText(`inside the WSL 2 distro ${distro()}`);
  // its own sign-in (decision P50): the card starts claude's login inside the distro from a button (driven in the next test)
  await expect(card.getByRole('button', { name: `Sign in inside ${distro()}` })).toBeVisible();
  await expect(card).toContainText("this install's sign-in is separate from the Windows one");
  await card.getByText('The distro', { exact: true }).click();
  await expect(card.getByTestId('wsl-facts')).toContainText('Windows drives at');

  await card.getByRole('button', { name: 'Test connection...' }).click();
  await expect(card).toContainText(`inside the WSL distro ${distro()}`);
  await card.getByRole('button', { name: 'Run the test' }).click();
  const report = card.getByTestId('connection-report');
  await expect(report).toBeVisible({ timeout: 90_000 });
  await expect(report).toContainText('T2 Start and handshake: pass');
  await expect(report).toContainText('T5 Stop and process cleanup: pass');
  await expect(report).toContainText('killed by its marker');

  // the Windows install is still there, a tile of its own, and says where it runs
  await page.locator('[data-testid=connection-tile][data-key=claude]').click();
  await expect(page.locator('[data-testid=connection][data-key=claude]')).toContainText('on Windows');
});

test("Connections: Sign in runs the CLI's own login inside the distro in a terminal in the card, and the card re-checks itself", async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-signin');
  await workspaceNav(page, 'nav-connections');
  const tile = page.locator(`[data-testid=connection-tile][data-key="claude@wsl:${distro()}"]`);
  const card = page.locator(`[data-testid=connection][data-key="claude@wsl:${distro()}"]`);
  await tile.click({ timeout: 60_000 });
  await card.getByText('The distro', { exact: true }).click();
  const home = (await card.getByTestId('wsl-facts').locator('dt:has-text("Linux home") + dd').textContent())?.trim() ?? '';
  expect(home).toMatch(/^\//);
  // the stand-in claude (a LABELLED TEST DOUBLE) is signed out until its own login runs
  const signedOut = join(home, '.drydock-standin-signed-out');
  writeFileSync(signedOut, '');
  try {
    await page.reload();
    await tile.click({ timeout: 60_000 });
    await expect(card.locator('dt:has-text("Signed in") + dd')).toHaveText('no', { timeout: 60_000 });

    await card.getByRole('button', { name: `Sign in inside ${distro()}` }).click();
    const box = card.getByTestId('sign-in-terminal');
    await expect(box).toBeVisible();
    await expect(box.getByTestId('sign-in-state')).toHaveText('claude auth login is running: answer it here.');
    // it runs as an agent runs in the distro: the run marker, the login shell's PATH, and a terminal for its input
    await expect(box.locator('.xterm-rows')).toContainText('marker=yes login-path=yes tty=yes', { timeout: 30_000 });
    await expect(box.locator('.xterm-rows')).toContainText('Press Enter to finish signing in');
    await box.getByTestId('xterm').click();
    await page.keyboard.press('Enter');
    await expect(box.locator('.xterm-rows')).toContainText('Signed in to the stand-in.', { timeout: 30_000 });
    // the end of the sign-in checks the card afresh (connection.check, past the probe cache): signed in now, without a reload,
    // with the account the stand-in names
    await expect(box.getByTestId('sign-in-state')).toHaveText('The sign-in finished. Signed in as standin@example.invalid (claude.ai).', { timeout: 60_000 });
    await expect(card.getByTestId('signed-in')).toHaveText('Signed in as standin@example.invalid (claude.ai)');
    await expect(card.locator('dt:has-text("Signed in") + dd')).toHaveText('yes', { timeout: 60_000 });
    await expect(tile).not.toContainText('Not signed in', { timeout: 60_000 });
    await box.getByTestId('sign-in-close').click();
    await expect(box).toHaveCount(0);
    // signed out again inside the distro: "Check again" asks the CLI afresh
    writeFileSync(signedOut, '');
    await card.getByTestId('sign-in-check').click();
    await expect(card.locator('dt:has-text("Signed in") + dd')).toHaveText('no', { timeout: 60_000 });
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
  // the distro's install is its own choice, named as on the Connections page (its Connection is probed when the dialog opens)
  const agent = dialog.getByLabel('Agent CLI', { exact: true });
  const choice = await optionsOf(agent, `claude@wsl:${distro()}`);
  await expect(choice).toHaveText(new RegExp(`^Claude Code in ${distro()} \\(WSL\\) 2\\.1\\.306$`), { timeout: 60_000 });
  await expect(choice).toBeEnabled();
  await expect(await optionsOf(agent, 'claude')).toContainText('Claude Code on Windows');
  await choose(agent, `claude@wsl:${distro()}`);
  await expect(dialog).toContainText(`Runs inside the WSL distro ${distro()}`);
  await dialog.getByRole('button', { name: 'Start chat' }).click();
  await expect(page).toHaveURL(/\/chats\/chat_/);
  const chatId = page.url().split('/').pop() ?? '';
  await expect(page.getByTestId('chip-adapter')).toContainText(`Claude Code in ${distro()} (WSL)`);
  // the private folder inside the distro, named the Windows way
  await expect(page.getByTestId('chat-cwd')).toHaveAttribute('title', new RegExp(`^\\\\\\\\wsl\\.localhost\\\\${distro()}\\\\.*\\\\\\.drydock\\\\chats\\\\${chatId}$`));

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
  await expect(async () => {
    await projectNav(page, 'nav-team');
    await page.getByLabel('Agent name').fill('Linux worker', { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  const runs = page.getByLabel('Runs', { exact: true });
  await expect(await optionsOf(runs)).toHaveCount(3);
  await choose(runs, `wsl:${distro()}`);
  await expect(page.getByTestId('run-mode')).toContainText('one inside the distro is fast');
  await page.getByRole('button', { name: 'Create agent' }).click();
  const card = page.getByTestId('agent').filter({ hasText: 'Linux worker' });
  await expect(card.getByTestId('agent-runs')).toHaveText(`in WSL ${distro()}`);

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

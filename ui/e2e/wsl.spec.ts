// The WSL run mode end to end (decision P41), against the real core with a stand-in WSL: the harness (cmd/drydock-e2e) gives
// it one distro, "Ubuntu", whose `claude` is a LABELLED TEST DOUBLE that answers in stream-json (internal/wsl/wsltest). The
// stand-in exists only on a Linux host; elsewhere these tests are skipped (CI's wsl job drives a real distro instead).
import { rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { createProject } from './helpers';

const distro = (): string => process.env.DRYDOCK_E2E_WSL ?? '';

test('Connections: Claude Code inside a WSL distro is its own card with its own sign-in, and its Test runs it in the distro', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-conn');
  await page.getByTestId('nav-connections').click();
  // a tile of its own on the overview, named by its distro; the tile opens its card below the grid
  const tile = page.locator(`[data-testid=connection-tile][data-key="claude@wsl:${distro()}"]`);
  await expect(tile).toBeVisible({ timeout: 60_000 });
  await expect(tile).toHaveAttribute('data-run-mode', 'wsl');
  await expect(tile).toContainText(`Claude Code (${distro()})`);
  await tile.click();
  const card = page.locator(`[data-testid=connection][data-key="claude@wsl:${distro()}"]`);
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute('data-run-mode', 'wsl');
  await expect(card.getByRole('heading', { level: 2 })).toHaveText(`claude in WSL (${distro()})`);
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
  await page.getByTestId('nav-connections').click();
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
    await expect(box.getByTestId('sign-in-state')).toHaveText('The sign-in finished. This card has re-checked itself.', { timeout: 30_000 });
    // the core's connection.sign_in_finished made the page re-check: signed in now, without a reload
    await expect(card.locator('dt:has-text("Signed in") + dd')).toHaveText('yes', { timeout: 60_000 });
    await box.getByTestId('sign-in-close').click();
    await expect(box).toHaveCount(0);
  } finally {
    rmSync(signedOut, { force: true });
  }
});

test('Team: a Claude Code agent is made to run inside the WSL distro and its card says so', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-team');
  await expect(async () => {
    await page.getByTestId('nav-team').click();
    await page.getByLabel('Agent name').fill('Linux worker', { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await page.getByLabel('Adapter', { exact: true }).selectOption('claude');
  const runs = page.getByLabel('Runs', { exact: true });
  await expect(runs.locator('option')).toHaveCount(3);
  await runs.selectOption(`wsl:${distro()}`);
  await expect(page.getByTestId('run-mode')).toContainText('a repository inside the distro is fast');
  await page.getByRole('button', { name: 'Create agent' }).click();
  const card = page.getByTestId('agent').filter({ hasText: 'Linux worker' });
  await expect(card.getByTestId('agent-runs')).toHaveText(`in WSL ${distro()}`);

  // the project's default for agents that do not choose
  await page.getByTestId('nav-project').click();
  const panel = page.getByTestId('run-mode-panel');
  await expect(panel).toBeVisible();
  await panel.getByLabel('Agents run').selectOption(`wsl:${distro()}`);
  await expect(panel.getByLabel('Agents run')).toBeEnabled();
  await expect(panel.getByRole('alert')).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId('run-mode-panel').getByLabel('Agents run')).toHaveValue(`wsl:${distro()}`);
});

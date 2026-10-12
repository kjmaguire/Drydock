// The WSL run mode end to end (decision P41), against the real core with a stand-in WSL: the harness (cmd/drydock-e2e) gives
// it one distro, "Ubuntu", whose `claude` is a LABELLED TEST DOUBLE that answers in stream-json (internal/wsl/wsltest). The
// stand-in exists only on a Linux host; elsewhere these tests are skipped (CI's wsl job drives a real distro instead).
import { expect, test } from '@playwright/test';
import { createProject } from './helpers';

const distro = (): string => process.env.DRYDOCK_E2E_WSL ?? '';

test('Connections: Claude Code inside a WSL distro is its own card with its own sign-in, and its Test runs it in the distro', async ({ page }) => {
  test.skip(!distro(), 'the stand-in WSL runs only on a Linux host');
  await createProject(page, 'gocalc-wsl-conn');
  await page.getByTestId('nav-connections').click();
  const card = page.locator(`[data-testid=connection][data-key="claude@wsl:${distro()}"]`);
  await expect(card).toBeVisible({ timeout: 60_000 });
  await expect(card).toHaveAttribute('data-run-mode', 'wsl');
  await expect(card.getByRole('heading', { level: 2 })).toHaveText(`claude in WSL (${distro()})`);
  await expect(card).toContainText(`inside the WSL 2 distro ${distro()}`);
  await expect(card).toContainText('its own sign-in, separate from Windows');
  await card.getByText('The distro', { exact: true }).click();
  await expect(card.getByTestId('wsl-facts')).toContainText('Windows drives at');
  // the Windows card is still there, and says where it runs
  await expect(page.locator('[data-testid=connection][data-key=claude]')).toContainText('on Windows');

  await card.getByRole('button', { name: 'Test connection...' }).click();
  await expect(card).toContainText(`inside the WSL distro ${distro()}`);
  await card.getByRole('button', { name: 'Run the test' }).click();
  const report = card.getByTestId('connection-report');
  await expect(report).toBeVisible({ timeout: 90_000 });
  await expect(report).toContainText('T2 Start and handshake: pass');
  await expect(report).toContainText('T5 Stop and process cleanup: pass');
  await expect(report).toContainText('killed by its marker');
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

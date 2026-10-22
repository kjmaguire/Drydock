// CI runners end to end against the real Go core (decision P56). The harness (cmd/drydock-e2e) gives the core TEST DOUBLES:
// a fake GitHub with a private repository (e2e/private-app) and a public one (e2e/public-site), and stand-in runners that
// print the real runner's job lines; the Linux stand-in has one job ("build") queued. Nothing reaches GitHub.
import { expect, test } from '@playwright/test';
import { appUrl, workspaceNav } from './helpers';

test('CI runners: a public repository is refused; a Linux runner runs a job; a Windows one needs the person to accept it; off and remove unregister', async ({ page }) => {
  await page.goto(appUrl());
  // the runners are on the GitHub page (they moved from Connections, 2026-10-22)
  await workspaceNav(page, 'nav-github');
  const section = page.getByTestId('github-page').getByTestId('ci-runners-section');
  await expect(section).toBeVisible();
  await expect(section.getByTestId('runner-private-only')).toContainText('Private repositories only');
  await expect(section.locator('[data-testid=runner-kind][data-kind=linux]')).toHaveAttribute('data-available', 'true');
  await expect(section.locator('[data-testid=runner-kind][data-kind=linux]')).toContainText('runs-on: [self-hosted, Linux, drydock]');

  const add = section.getByTestId('runner-add');
  const repo = add.getByTestId('runner-repo');
  const addButton = add.getByTestId('runner-add-button');

  // a public repository: refused with the reason, nothing listed
  await repo.fill('e2e/public-site');
  await add.getByTestId('runner-kind-linux').check();
  await addButton.click();
  await expect(section.getByRole('alert')).toContainText('fork');
  await expect(section.getByTestId('runner-slot')).toHaveCount(0);

  // the private repository: the runner registers, takes the queued job and waits again
  await repo.fill('https://github.com/e2e/private-app');
  await addButton.click();
  const linux = section.locator('[data-testid=runner-slot][data-key="e2e/private-app:linux"]');
  await expect(linux).toBeVisible();
  await expect(linux.getByTestId('runner-what')).toContainText('Last job: build, succeeded.', { timeout: 30_000 });
  await expect(linux).toHaveAttribute('data-state', 'idle');
  await linux.getByTestId('runner-log').click();
  await expect(linux.getByTestId('runner-output')).toContainText('Running job: build');

  // a Windows runner only once the person accepts that its jobs run as them
  await repo.fill('e2e/private-app');
  await add.getByTestId('runner-kind-windows').check();
  await expect(addButton).toBeDisabled();
  await add.getByLabel(/run on this PC as me/).check();
  await addButton.click();
  const windows = section.locator('[data-testid=runner-slot][data-key="e2e/private-app:windows"]');
  await expect(windows).toHaveAttribute('data-state', 'idle', { timeout: 30_000 });

  // off: the runner stops; the setting stays after a reload
  await linux.getByLabel('On').uncheck();
  await expect(linux).toHaveAttribute('data-state', 'paused');
  await expect(linux.getByTestId('runner-state')).toHaveText('off');
  await page.reload();
  await expect(page.locator('[data-testid=runner-slot][data-key="e2e/private-app:linux"]')).toHaveAttribute('data-state', 'paused');

  // remove both: the first click only asks, "Yes, remove" removes (audit L40)
  for (const key of ['e2e/private-app:linux', 'e2e/private-app:windows']) {
    const slot = page.locator(`[data-testid=runner-slot][data-key="${key}"]`);
    await slot.getByTestId('runner-remove').click();
    await expect(slot).toBeVisible();
    await slot.getByTestId('runner-remove-confirm').click();
  }
  await expect(page.getByTestId('runner-slot')).toHaveCount(0);
});

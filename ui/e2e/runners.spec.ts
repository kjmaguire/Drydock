// CI runners end to end against the real Go core (decision P56). The harness (cmd/drydock-e2e) gives the core TEST DOUBLES:
// a fake GitHub with a private repository (e2e/private-app) and a public one (e2e/public-site), and stand-in runners that
// print the real runner's job lines; the Linux stand-in has one job ("build") queued. Nothing reaches GitHub.
import { expect, test, type Page } from '@playwright/test';
import { appUrl, workspaceNav } from './helpers';

/** Opens GitHub the way a person does: Integrations, then its GitHub row, whose detail opens beside the list. */
const openGitHub = (page: Page): Promise<void> => workspaceNav(page, 'nav-github');

test('CI runners: a public repository is refused; a Linux runner runs a job; a Windows one needs the person to accept it; off and remove unregister', async ({ page }) => {
  await page.goto(appUrl());
  // the runners are on the GitHub page (they moved from Connections, 2026-10-22)
  await openGitHub(page);
  const section = page.getByTestId('github-page').getByTestId('ci-runners-section');
  await expect(section).toBeVisible();
  await expect(section.getByTestId('runner-private-only')).toContainText('Private repositories only');
  const linuxKind = section.locator('[data-testid=runner-kind][data-kind=linux]');
  await expect(linuxKind).toHaveAttribute('data-available', 'true');
  await expect(linuxKind).toHaveAttribute('title', /runs-on: \[self-hosted, Linux, drydock\]/);

  // Add runner opens the form in a dialog
  await section.getByTestId('runner-add-open').click();
  const dialog = page.getByRole('dialog', { name: 'Add a runner' });
  const add = dialog.getByTestId('runner-add');
  const repo = add.getByTestId('runner-repo');
  const addButton = add.getByTestId('runner-add-button');

  // a public repository: refused with the reason, in the dialog; nothing listed
  await repo.fill('e2e/public-site');
  await add.getByTestId('runner-kind-linux').check();
  await expect(add.getByTestId('runner-runs-on')).toContainText('runs-on: [self-hosted, Linux, drydock]');
  await addButton.click();
  await expect(dialog.getByRole('alert')).toContainText('fork');
  await expect(section.getByTestId('runner-slot')).toHaveCount(0);

  // the private repository: the runner registers (the dialog closes), takes the queued job and waits again
  await repo.fill('https://github.com/e2e/private-app');
  await addButton.click();
  await expect(dialog).toHaveCount(0);
  const linux = section.locator('[data-testid=runner-slot][data-key="e2e/private-app:linux"]');
  await expect(linux).toBeVisible();
  await expect(linux.getByTestId('runner-what')).toContainText('Last job: build, succeeded.', { timeout: 30_000 });
  await expect(linux).toHaveAttribute('data-state', 'idle');
  await linux.getByRole('button', { name: 'Actions for the Linux runner of e2e/private-app' }).click();
  await page.getByRole('menuitem', { name: 'Show output' }).click();
  await expect(linux.getByTestId('runner-output')).toContainText('Running job: build');

  // a Windows runner only once the person accepts that its jobs run as them
  await section.getByTestId('runner-add-open').click();
  await add.getByTestId('runner-repo').fill('e2e/private-app');
  await add.getByTestId('runner-kind-windows').check();
  await expect(addButton).toBeDisabled();
  await add.getByLabel(/run on this PC as me/).check();
  await addButton.click();
  await expect(dialog).toHaveCount(0);
  const windows = section.locator('[data-testid=runner-slot][data-key="e2e/private-app:windows"]');
  await expect(windows).toHaveAttribute('data-state', 'idle', { timeout: 30_000 });

  // off: the runner stops; the setting stays after a reload
  await linux.getByLabel('On', { exact: true }).uncheck();
  await expect(linux).toHaveAttribute('data-state', 'paused');
  await expect(linux.getByTestId('runner-state')).toHaveText('Off');
  await page.reload();
  await expect(page.locator('[data-testid=runner-slot][data-key="e2e/private-app:linux"]')).toHaveAttribute('data-state', 'paused');

  // remove both from their menus: Remove only asks, "Yes, remove" removes (audit L40)
  for (const [key, name] of [
    ['e2e/private-app:linux', 'the Linux runner of e2e/private-app'],
    ['e2e/private-app:windows', 'the Windows runner of e2e/private-app'],
  ]) {
    const slot = page.locator(`[data-testid=runner-slot][data-key="${key}"]`);
    await slot.getByRole('button', { name: `Actions for ${name}` }).click();
    await page.getByRole('menuitem', { name: 'Remove…' }).click();
    await expect(slot.getByRole('group', { name: `Remove ${name}? It stops taking jobs, and adding it again sets it up from the start.` })).toBeVisible();
    await expect(slot).toBeVisible();
    await slot.getByTestId('runner-remove-confirm').click();
    await expect(slot).toHaveCount(0);
  }
  await expect(page.getByTestId('runner-slot')).toHaveCount(0);
});

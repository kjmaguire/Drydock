import { expect, test } from '@playwright/test';
import { createAgent, createProject, projectNav, workspaceNav } from './helpers';

// Round 7: what agents inherit, and how isolation is stated, against the real Go core.

test('Settings: extra environment variables are named, never valued; the Inspector states the isolation honestly', async ({ page }) => {
  await createProject(page, 'silo');
  await createAgent(page, 'Pay', 'backend');

  await workspaceNav(page, 'nav-settings');
  const env = page.getByTestId('agent-env');
  await expect(env).toContainText('Agent programs start with a trimmed environment');
  const box = env.getByLabel(/Also pass these variables/);
  // a pasted NAME=value is refused on the page (L38): the core is never asked, and no message repeats the line
  const sent: string[] = [];
  page.on('request', (req) => {
    if (req.url().includes('/rpc/agentenv.set')) sent.push(req.postData() ?? '');
  });
  await box.fill('AWS_PROFILE\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI-e2e-value');
  await env.getByRole('button', { name: 'Save' }).click();
  await expect(env.getByRole('alert')).toHaveText('Line 2 is not a variable name. Give names only, never values.');
  await expect(box).toHaveAttribute('aria-invalid', 'true');
  await expect(box).toBeFocused();
  await expect(page.locator('body')).not.toContainText('wJalrXUtnFEMI-e2e-value');
  expect(sent).toEqual([]);
  await box.fill('AWS_PROFILE\nCLAUDE_CODE_USE_BEDROCK');
  await expect(env.getByRole('alert')).toHaveCount(0);
  await env.getByRole('button', { name: 'Save' }).click();
  await expect(env.getByRole('status')).toContainText('Saved');
  expect(sent).toEqual([JSON.stringify({ names: ['AWS_PROFILE', 'CLAUDE_CODE_USE_BEDROCK'] })]);
  await page.reload();
  await workspaceNav(page, 'nav-settings');
  await expect(page.getByTestId('agent-env').getByLabel(/Also pass these variables/)).toHaveValue('AWS_PROFILE\nCLAUDE_CODE_USE_BEDROCK');

  // isolation, stated plainly, in the Inspector
  await projectNav(page, 'nav-team');
  await page.getByTestId('agent').filter({ hasText: 'Pay' }).getByRole('button', { name: 'Inspect' }).click();
  const iso = page.getByTestId('inspector-isolation');
  await expect(iso).toHaveAttribute('data-level', 'guarded');
  await expect(iso).toContainText('not an operating-system sandbox');

  // a permission mode that skips approvals is called what it is
  await page.getByTestId('agent').filter({ hasText: 'Pay' }).getByRole('button', { name: 'Edit Pay' }).click();
  // (the mode is set through the template/agent API; the editor does not offer it, so only the guarded state is shown here)
});

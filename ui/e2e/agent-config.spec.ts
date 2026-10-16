import { expect, test } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createProject, freshRepo, projectNav } from './helpers';

// The configuration an agent CLI would act on (docs/security.md s3 item 2b; audit 2026-10-16, M3), against the real Go core.
// A repository that holds Gemini CLI settings shows the banner on the project's screens, naming the CLI, the place and the
// files; only the Trust button trusts, and it trusts exactly what was shown (the adapter and the fingerprint of those files);
// Project settings lists the finding afterwards as trusted, and a changed file asks again.

function write(repo: string, rel: string, body: string): void {
  const p = join(repo, ...rel.split('/'));
  mkdirSync(join(p, '..'), { recursive: true });
  writeFileSync(p, body);
}

test('agent CLI configuration in the repository: the banner names the CLI and the files, Trust clears it, Project settings lists it as trusted, a change asks again', async ({ page }) => {
  const repo = freshRepo();
  write(repo, '.gemini/settings.json', '{"hooks":{}}\n');
  await createProject(page, 'cfg-guard', repo);

  const banner = page.getByTestId('agent-config-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Review the configuration the repository brought for agent CLIs.');
  const item = banner.getByTestId('agent-config-finding').filter({ hasText: 'gemini' });
  await expect(item).toContainText('the repository');
  await expect(item.locator('li code')).toHaveText(['.gemini/settings.json']);

  // it stays on another screen of the project; showing it trusts nothing
  await projectNav(page, 'nav-team');
  await expect(banner).toBeVisible();

  // Project settings lists it as not trusted, with the same files
  await projectNav(page, 'nav-project');
  const panel = page.getByTestId('agent-config-panel');
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId('agent-config-summary')).toContainText('1 of 1 place needs your decision');
  await expect(panel.getByTestId('agent-config-finding').filter({ hasText: 'gemini' }).locator('li code')).toHaveText(['.gemini/settings.json']);

  // the Trust button trusts what was shown, and the banner goes
  await banner.getByTestId('agent-config-trust').first().click();
  await expect(banner).toHaveCount(0);
  await expect(panel.getByTestId('agent-config-summary')).toContainText('Everything found is trusted.');
  await expect(panel.getByTestId('agent-config-finding').filter({ hasText: 'gemini' }).getByTestId('agent-config-trusted')).toHaveText('trusted');

  // a trusted configuration stays trusted after a reload
  await page.reload();
  await expect(page.getByTestId('agent-config-panel').getByTestId('agent-config-trusted')).toBeVisible();
  await expect(banner).toHaveCount(0);

  // a file that changes is another state of the files: the banner asks again (the window getting focus reads it again)
  write(repo, '.gemini/settings.json', '{"hooks":{"BeforeTool":[]}}\n');
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(banner).toBeVisible();
  await expect(banner.getByTestId('agent-config-finding').filter({ hasText: 'gemini' })).toContainText('not trusted');
});

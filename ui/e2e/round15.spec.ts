import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createProject, freshRepo, projectNav, openRoom } from './helpers';

// The repository configuration guard (docs/security.md s3), against the real core: a project whose repository gets a new
// git setting outside Drydock shows the banner on its screens, naming the setting; only the Trust button trusts it, and a
// trusted configuration stays trusted after a reload.
test('repository git configuration: a changed setting shows the banner, and trusting it clears it', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'git-guard', repo);
  const banner = page.getByRole('region', { name: 'Repository git configuration changed' });
  await openRoom(page, 'general');
  await expect(page.getByRole('textbox', { name: /^Message to/ })).toBeVisible();
  await expect(banner).toHaveCount(0);

  // a clean filter planted in the repository's own configuration (what an agent could write from a task's worktree)
  execFileSync('git', ['-C', repo, 'config', 'filter.evil.clean', 'cat']);
  await page.reload();
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("The repository's git configuration changed (filter.evil.clean).");
  await expect(banner).toContainText('Trust it only if you made or expect this change.');
  await expect(banner.getByRole('list', { name: 'Changed git configuration items' }).getByRole('listitem')).toHaveText([/^filter\.evil\.clean/]);
  // the value is never shown
  await expect(banner).not.toContainText(' cat');

  // it stays on another screen of the project
  await projectNav(page, 'nav-team');
  await expect(banner).toBeVisible();

  await banner.getByRole('button', { name: 'Trust this configuration' }).click();
  await expect(banner).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId('nav-team')).toBeVisible();
  await expect(banner).toHaveCount(0);
});

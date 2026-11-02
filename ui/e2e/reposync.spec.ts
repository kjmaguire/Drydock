// A project's folder kept in step with its remote (decision P143), against the real core and real git: the "remote" is a bare
// repository on this disk (the harness has no network and no gh), the project's folder a clone of it, and another clone stands
// in for whoever pushes to GitHub. Work's crew rail says where the folder stands; Sync now fetches, and fast-forwards only when
// the folder has no local changes.
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { freshRepo, openNewProject, projectItem } from './helpers';

const git = (dir: string, ...args: string[]) => execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.com', ...args], { cwd: dir, encoding: 'utf8' }).trim();

test('Work says how far behind its remote the folder is; Sync now leaves local changes alone, then fast-forwards', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const base = realpathSync.native(mkdtempSync(join(tmpdir(), 'drydock-e2e-sync-')));
  try {
    const remote = join(base, 'remote.git');
    const folder = join(base, 'synced-app');
    const upstream = join(base, 'upstream');
    git(base, 'clone', '--quiet', '--bare', freshRepo(), remote);
    git(base, 'clone', '--quiet', remote, folder);
    git(base, 'clone', '--quiet', remote, upstream);
    const tracked = git(folder, 'ls-files').split('\n')[0]!;

    // the project, from New project's Open a folder
    await openNewProject(page);
    const dialog = page.getByTestId('new-project-dialog');
    await dialog.getByRole('radio', { name: 'Open a folder' }).click();
    await dialog.getByLabel('Folder', { exact: true }).fill(folder);
    await expect(dialog.getByTestId('folder-check')).toHaveAttribute('data-state', 'repository');
    await dialog.getByTestId('new-project-create').click();
    await expect(page.getByTestId('work')).toBeVisible();
    await expect(projectItem(page, 'synced-app')).toHaveAttribute('aria-expanded', 'true');

    // opening the project checks the remote (a fetch only)
    const state = page.getByTestId('repo-sync-state');
    await expect(state).toHaveText('Up to date with origin', { timeout: 20_000 });
    // a narrow window keeps the line and Sync now (the crew rail leaves under 1180 px; the line is under the list)
    const size = page.viewportSize();
    await page.setViewportSize({ width: 1100, height: 800 });
    await expect(page.getByTestId('repo-sync-now')).toBeVisible();
    if (size) await page.setViewportSize(size);

    // someone pushes a commit; the person has an edit of their own in the folder
    writeFileSync(join(upstream, 'NEWS.md'), 'news\n');
    git(upstream, 'add', 'NEWS.md');
    git(upstream, 'commit', '--quiet', '-m', 'Add news');
    git(upstream, 'push', '--quiet', 'origin', 'HEAD');
    const before = readFileSync(join(folder, tracked), 'utf8');
    writeFileSync(join(folder, tracked), before + '\n// my edit\n');

    await page.getByTestId('repo-sync-now').click();
    await expect(state).toHaveText('1 behind');
    await expect(page.getByTestId('repo-sync-why')).toHaveText('Not updated: the folder has local changes.');
    expect(existsSync(join(folder, 'NEWS.md'))).toBe(false);
    expect(readFileSync(join(folder, tracked), 'utf8')).toBe(before + '\n// my edit\n');

    // the edit is put away: Sync now brings the commit in
    git(folder, 'checkout', '--quiet', '--', tracked);
    await page.getByTestId('repo-sync-now').click();
    await expect(state).toHaveText('Up to date with origin');
    await expect(page.getByTestId('repo-sync-why')).toHaveCount(0);
    expect(existsSync(join(folder, 'NEWS.md'))).toBe(true);
    expect(git(folder, 'rev-parse', 'HEAD')).toBe(git(upstream, 'rev-parse', 'HEAD'));
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

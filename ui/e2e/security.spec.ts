// The security scan end to end against the real Go core: the harness (cmd/drydock-e2e) provides stand-in osv-scanner and
// opengrep tools and a rules folder; gitleaks is built in. The planted key below matches gitleaks' aws-access-token rule and
// is not a real credential; the page must name the rule and the place and never show the value.
import { expect, test } from '@playwright/test';
import { mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { appUrl, createProject, freshRepo, projectNav } from './helpers';

const PLANTED = 'AKIA2W7X3KQ5ZV4YBN6T';

test('one button scans the working tree with three engines, names the findings without the secret, and keeps a history', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'gocalc-security', repo);
  writeFileSync(join(repo, 'notes.txt'), `aws_access_key_id = ${PLANTED}\n`);

  await projectNav(page, 'nav-security');
  await expect(page.getByTestId('security-scan')).toBeVisible();
  await expect(page.locator('[data-testid=tool][data-tool=osv-scanner][data-found=true]')).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('[data-testid=tool][data-tool=opengrep][data-found=true]')).toBeVisible();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'idle');

  await page.getByTestId('scan-button').click();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  const engines = page.getByTestId('scan-engine');
  await expect(engines).toHaveCount(3);
  for (const id of ['secrets', 'deps', 'sast']) {
    await expect(page.locator(`[data-testid=scan-engine][data-engine=${id}]`)).toHaveAttribute('data-status', 'done');
  }
  await expect(page.getByTestId('scan-overall')).toHaveAttribute('aria-valuenow', '100');

  // the secrets finding: rule, file and line; the value stays out of the page
  const secret = page.locator('[data-testid=finding][data-engine=secrets]');
  await expect(secret).toHaveCount(1);
  await expect(secret).toContainText('aws-access-token');
  await expect(secret).toContainText('notes.txt:1');
  await expect(secret).toContainText('The value is not shown');
  // the dependency finding links its advisory
  const dep = page.locator('[data-testid=finding][data-engine=deps]');
  await expect(dep.first()).toContainText('golang.org/x/text@0.3.7');
  await expect(dep.first()).toContainText('GO-2021-0113');
  // the two code patterns, both in calc.go
  const exec = page.locator('[data-testid=finding][data-engine=sast]').filter({ hasText: 'go.lang.security.audit.dangerous-exec-command' });
  await expect(exec).toHaveCount(1);
  await expect(exec).toHaveAttribute('data-severity', 'high');
  await expect(exec).toContainText('calc.go');
  const rand = page.locator('[data-testid=finding][data-engine=sast]').filter({ hasText: 'go.lang.security.audit.crypto.math-random-used' });
  await expect(rand).toHaveCount(1);
  await expect(rand).toHaveAttribute('data-severity', 'medium');
  await expect(rand).toContainText('calc.go');
  await expect(page.locator('body')).not.toContainText(PLANTED);

  await expect(page.getByTestId('scan-history-item')).toHaveCount(1);

  // the high chip leaves only the high rows
  await page.locator('[data-testid=sev-filter][data-severity=high]').click();
  const shown = page.getByTestId('finding');
  await expect(shown.first()).toBeVisible();
  const n = await shown.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) await expect(shown.nth(i)).toHaveAttribute('data-severity', 'high');
  await expect(page.locator('[data-testid=finding][data-severity=medium]')).toHaveCount(0);

  // a second scan joins the history
  await page.getByTestId('scan-button').click();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  await expect(page.getByTestId('scan-history-item')).toHaveCount(2);
  await expect(page.locator('body')).not.toContainText(PLANTED);
});

// "Choose file..." on the tools panel: the harness's stand-in file dialog (cmd/drydock-e2e) answers with a copy of its own
// binary named like the tool; the panel shows the chosen file, a scan runs it, and "Use PATH instead" forgets it.
test("a tool's program is chosen with the file dialog and forgotten again", async ({ page }) => {
  // three waits of up to 60 s below (found, chosen, forgotten) plus a scan: more than the 120 s a test gets by default
  test.setTimeout(300_000);
  const repo = freshRepo();
  await createProject(page, 'gocalc-security-pick', repo);
  await projectNav(page, 'nav-security');
  const osv = page.locator('[data-testid=tool][data-tool=osv-scanner]');
  await expect(osv).toHaveAttribute('data-found', 'true', { timeout: 60_000 });
  const pick = page.locator('[data-testid=tool-pick][data-tool=osv-scanner]');
  await pick.getByTestId('tool-choose').click();
  // the core stores the choice and probes the programs once (both --version runs in parallel, 10 s cap and a 5 s output
  // drain each) before it answers; the row shows that answer. The same budget as the "found" wait above, for a loaded
  // Windows runner; the version proves the chosen program ran, not only that its path was stored
  await expect(osv).toContainText('Chosen file', { timeout: 60_000 });
  await expect(osv).toContainText('picked');
  await expect(osv).toContainText('0.0.0-standin');
  await page.getByTestId('scan-button').click();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  await expect(page.locator('[data-testid=scan-engine][data-engine=deps]')).toHaveAttribute('data-status', 'done');
  await pick.getByTestId('tool-clear').click();
  await expect(osv).not.toContainText('Chosen file', { timeout: 60_000 }); // the same probe as above
  await expect(osv).toContainText('Found at');
  await expect(pick.getByTestId('tool-clear')).toHaveCount(0);
});

// Decision P66: Install makes Drydock download a tool's newest release itself (the harness serves a fixture release and a
// stand-in program), the row says it is installed by Drydock with the release, Update finds nothing newer, the scan runs the
// installed tool, and Remove goes back to the program found on PATH.
test("Install downloads a tool's newest release into Drydock, Update checks for a newer one, Remove deletes it", async ({ page }) => {
  test.setTimeout(300_000);
  const repo = freshRepo();
  await createProject(page, 'gocalc-security-install', repo);
  await projectNav(page, 'nav-security');
  const osv = page.locator('[data-testid=tool][data-tool=osv-scanner]');
  await expect(osv).toHaveAttribute('data-found', 'true', { timeout: 60_000 });
  const row = page.locator('[data-testid=tool-install-row][data-tool=osv-scanner]');
  await expect(row.getByTestId('tool-install')).toHaveText('Install');
  await row.getByTestId('tool-install').click();
  // the install runs in the background; the row shows the installed release once the core announced the end and probed it
  await expect(osv).toHaveAttribute('data-source', 'installed', { timeout: 60_000 });
  await expect(osv).toContainText('Installed by Drydock (v2.7.0)');
  await expect(osv).toContainText('0.0.0-standin');
  await expect(row.getByTestId('tool-job')).toHaveText('Installed v2.7.0.');
  await expect(row.getByTestId('tool-install')).toHaveText('Update');
  // Update: nothing newer in the release listing
  await row.getByTestId('tool-install').click();
  await expect(row.getByTestId('tool-job')).toHaveText('Checked: already the newest release (v2.7.0).', { timeout: 60_000 });
  // the scan runs the installed program
  await page.getByTestId('scan-button').click();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  await expect(page.locator('[data-testid=scan-engine][data-engine=deps]')).toHaveAttribute('data-status', 'done');
  await row.getByTestId('tool-remove').click();
  await expect(osv).not.toHaveAttribute('data-source', 'installed', { timeout: 60_000 });
  await expect(osv).toContainText('Found at');
  await expect(row.getByTestId('tool-install')).toHaveText('Install');
});

// Decision P61: from Home, the workspace's Security scan reads a folder that is not a project (a repository just downloaded):
// the same three engines, the finding named without the key's value, and the scan kept in the recent folder scans.
test('Home opens the Security scan, which scans a folder that is not a project and keeps it in the history', async ({ page }) => {
  // the folder's full path: Windows' temp folder can be a short 8.3 path, and Drydock names the folder it resolves to
  const dir = realpathSync.native(mkdtempSync(join(tmpdir(), 'drydock-e2e-download-')));
  try {
    writeFileSync(join(dir, 'notes.txt'), `aws_access_key_id = ${PLANTED}\n`);
    const home = new URL(appUrl());
    home.pathname = '/home';
    await page.goto(home.toString());
    await page.getByTestId('home-security-scan').click();
    await expect(page).toHaveURL(/\/security-scan$/);
    await expect(page.getByTestId('workspace-security')).toBeVisible();
    await expect(page.getByTestId('scan-reads-only')).toContainText("never runs the repository's code");
    await expect(page.locator('[data-testid=tool][data-tool=osv-scanner][data-found=true]')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId('scan-button')).toBeDisabled();

    await page.getByTestId('scan-folder').fill(dir);
    await page.getByTestId('scan-button').click();
    await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
    for (const id of ['secrets', 'deps', 'sast']) {
      await expect(page.locator(`[data-testid=scan-engine][data-engine=${id}]`)).toHaveAttribute('data-status', 'done');
    }
    await expect(page.getByTestId('scan-dir')).toContainText(dir);
    const secret = page.locator('[data-testid=finding][data-engine=secrets]');
    await expect(secret).toHaveCount(1);
    await expect(secret).toContainText('aws-access-token');
    await expect(secret).toContainText('notes.txt:1');
    await expect(secret).toContainText('The value is not shown');
    await expect(page.locator('body')).not.toContainText(PLANTED);
    // the path inside a CSS attribute selector: a Windows path's backslashes and any quotes escaped
    const item = page.locator(`[data-testid=scan-history-item][data-folder="${dir.replace(/[\\"]/g, '\\$&')}"]`);
    await expect(item).toHaveCount(1);

    // opened again later, the folder scan is in the history and its report reopens, still without the value
    await page.goto(home.toString());
    await page.getByTestId('home-security-scan').click();
    await expect(item).toHaveCount(1);
    await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'idle');
    await item.click();
    await expect(page.locator('[data-testid=finding][data-engine=secrets]')).toHaveCount(1);
    await expect(page.getByTestId('scan-folder')).toHaveValue(dir);
    await expect(page.locator('body')).not.toContainText(PLANTED);
    // nothing became a project
    await page.goto(home.toString());
    await expect(page.getByTestId('workspace-home')).toBeVisible();
    await expect(page.getByTestId('home-project').filter({ hasText: 'drydock-e2e-download' })).toHaveCount(0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

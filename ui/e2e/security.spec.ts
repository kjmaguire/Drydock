// The security scan end to end against the real Go core: the harness (cmd/drydock-e2e) provides stand-in osv-scanner and
// opengrep tools and a rules folder; gitleaks is built in. The planted key below matches gitleaks' aws-access-token rule and
// is not a real credential; the page must name the rule and the place and never show the value, also in the code around the
// finding and in the whole file (decision P97: the core masks it before it leaves).
import { expect, test } from '@playwright/test';
import { mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { appUrl, createAgent, createProject, freshRepo, projectNav } from './helpers';

const PLANTED = 'AKIA2W7X3KQ5ZV4YBN6T';

test("one button scans the working tree with three engines; a finding's pane shows its code without the key, makes a task from it, and dismissing it drops the count", async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'gocalc-security', repo);
  await createAgent(page, 'Builder', 'builder');
  await createAgent(page, 'Reviewer', 'reviewer');
  writeFileSync(join(repo, 'notes.txt'), `aws_access_key_id = ${PLANTED}\n`);

  await projectNav(page, 'nav-security');
  await expect(page.getByTestId('security-scan')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: 'Security', exact: true })).toBeVisible();
  await expect(page.locator('[data-testid=scan-engine][data-engine=deps][data-status=ready]')).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('[data-testid=scan-engine][data-engine=sast][data-status=ready]')).toBeVisible();
  await expect(page.getByTestId('security-tools')).toHaveCount(0); // the tools are Drydock's own (decision P95)
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'idle');
  // the long path the core shows, not the 8.3 short one the temp folder can be named by on Windows (C:\Users\RUNNER~1\...)
  await expect(page.getByTestId('scan-target-path')).toHaveText(realpathSync.native(repo));

  await page.getByTestId('scan-button').click();
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  await expect(page.getByTestId('scan-engine')).toHaveCount(3);
  for (const id of ['secrets', 'deps', 'sast']) {
    const tile = page.locator(`[data-testid=scan-engine][data-engine=${id}]`);
    await expect(tile).toHaveAttribute('data-status', 'done');
    await expect(tile.getByTestId('engine-state')).toHaveText(/^done · /);
  }
  await expect(page.locator('[data-testid=scan-engine][data-engine=secrets] [data-testid=engine-count]')).toHaveText('1 secret, value never shown');
  await expect(page.getByTestId('scan-reads-only')).toHaveText('Only files were read. Nothing in the folder ran.');
  await expect(page.getByTestId('scan-button')).toContainText('Scan again');

  // the secrets finding: rule, file and line; the value stays out of the page
  const secret = page.locator('[data-testid=finding][data-engine=secrets]');
  await expect(secret).toHaveCount(1);
  await expect(secret).toContainText('notes.txt:1 · aws-access-token');
  // the dependency finding names its advisories
  const dep = page.locator('[data-testid=finding][data-engine=deps]');
  await expect(dep.first()).toContainText('golang.org/x/text 0.3.7 has known vulnerabilities');
  await expect(dep.first()).toContainText('GO-2021-0113');
  // the two code patterns, both in calc.go
  const exec = page.locator('[data-testid=finding][data-engine=sast][data-rule="go.lang.security.audit.dangerous-exec-command"]');
  await expect(exec).toHaveCount(1);
  await expect(exec).toHaveAttribute('data-severity', 'high');
  await expect(exec).toContainText('calc.go');
  const rand = page.locator('[data-testid=finding][data-engine=sast][data-rule="go.lang.security.audit.crypto.math-random-used"]');
  await expect(rand).toHaveCount(1);
  await expect(rand).toHaveAttribute('data-severity', 'medium');
  await expect(rand).toContainText('calc.go');
  await expect(page.locator('body')).not.toContainText(PLANTED);
  await expect(page.getByTestId('findings-count')).toHaveText('4 findings');
  await expect(page.getByTestId('scan-history-item')).toHaveCount(1);

  // select the secret: the pane reads the code around it from the repository, with the key drawn as a hidden chip
  await secret.click();
  await expect(secret).toHaveAttribute('aria-current', 'true');
  const fp = await secret.getAttribute('data-fingerprint');
  expect(fp).toBeTruthy();
  const pane = page.getByTestId('finding-pane');
  await expect(pane).toHaveAttribute('data-fingerprint', fp!);
  await expect(pane.getByTestId('finding-pane-severity')).toHaveText('High · Secrets');
  await expect(pane.getByTestId('finding-meaning')).toContainText('AWS access key');
  const lines = pane.getByTestId('finding-lines');
  await expect(lines).toBeVisible();
  await expect(lines.locator('[data-hit=true]')).toContainText('aws_access_key_id =');
  await expect(lines.getByTestId('masked-secret')).toHaveCount(1);
  await expect(pane.getByTestId('finding-steps').locator('li').first()).toBeVisible();
  await expect(page.locator('body')).not.toContainText(PLANTED);
  await expect(page.locator('body')).not.toContainText('REDACTED');

  // the whole file, read-only, the key still hidden
  await pane.getByTestId('finding-open-file').click();
  const dialog = page.getByRole('dialog', { name: 'notes.txt' });
  await expect(dialog.getByTestId('file-lines').getByTestId('masked-secret')).toHaveCount(1);
  await expect(dialog).not.toContainText(PLANTED);
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toHaveCount(0);

  // a task for the project's builder, made from the finding (never with the key)
  const task = pane.getByTestId('finding-task');
  await expect(task).toHaveText('Make a task for Builder');
  await task.click();
  const made = pane.getByTestId('finding-task-made');
  await expect(made).toContainText('Remove the secret in notes.txt:1 and revoke it');
  await expect(made.getByRole('link')).toHaveAttribute('href', /^\/task\//);
  await expect(task).toBeDisabled();

  // "Not a real key": dismissed, the count drops, it waits under "1 dismissed"
  await pane.getByTestId('finding-dismiss').click();
  await page.getByRole('menuitem', { name: 'Not a real key' }).click();
  await expect(page.getByTestId('findings-count')).toHaveText('3 findings');
  await expect(secret).toHaveCount(0);
  await expect(page.locator('[data-testid=scan-engine][data-engine=secrets] [data-testid=engine-count]')).toHaveText('0 secrets, values never shown');
  await expect(page.getByTestId('dismissed-toggle')).toHaveText('1 dismissed');

  // R scans again: the dismissed finding stays dismissed in the new scan, which joins the recent scans
  await page.locator('body').press('r');
  await expect(page.getByTestId('scan-history-item')).toHaveCount(2, { timeout: 60_000 });
  await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
  await expect(page.getByTestId('findings-count')).toHaveText('3 findings');
  await expect(page.getByTestId('dismissed-toggle')).toHaveText('1 dismissed');
  await expect(page.locator('body')).not.toContainText(PLANTED);
});

// Decision P61: from Home, the workspace's Security scan reads a folder that is not a project (a repository just downloaded):
// the same three engines, the finding named without the key's value, and the scan kept in the recent scans.
test('Home opens the Security scan, which scans a folder that is not a project and keeps it in the recent scans', async ({ page }) => {
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
    await expect(page.getByTestId('scan-reads-only')).toHaveText('Only files are read. Nothing in the folder runs.');
    await expect(page.getByTestId('scan-target-note')).toHaveText('a folder, not a project');
    await expect(page.locator('[data-testid=scan-engine][data-engine=deps][data-status=ready]')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId('scan-button')).toBeDisabled();

    await page.getByTestId('scan-folder').fill(dir);
    await page.getByTestId('scan-button').click();
    await expect(page.getByTestId('scan-status')).toHaveAttribute('data-status', 'done', { timeout: 60_000 });
    for (const id of ['secrets', 'deps', 'sast']) {
      await expect(page.locator(`[data-testid=scan-engine][data-engine=${id}]`)).toHaveAttribute('data-status', 'done');
    }
    await expect(page.getByTestId('scan-folder')).toHaveValue(dir);
    const secret = page.locator('[data-testid=finding][data-engine=secrets]');
    await expect(secret).toHaveCount(1);
    await expect(secret).toContainText('notes.txt:1 · aws-access-token');
    // its pane: the code without the key; a folder scan makes no task
    await secret.click();
    await expect(page.getByTestId('finding-lines').getByTestId('masked-secret')).toHaveCount(1);
    await expect(page.getByTestId('finding-task')).toBeDisabled();
    await expect(page.getByTestId('finding-task-folder')).toBeVisible();
    await expect(page.locator('body')).not.toContainText(PLANTED);
    // the path inside a CSS attribute selector: a Windows path's backslashes and any quotes escaped
    const item = page.locator(`[data-testid=scan-history-item][data-folder="${dir.replace(/[\\"]/g, '\\$&')}"]`);
    await expect(item).toHaveCount(1);

    // opened again later, the folder scan is in the recent scans and its report reopens, still without the value
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

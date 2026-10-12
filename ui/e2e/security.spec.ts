// The security scan end to end against the real Go core: the harness (cmd/drydock-e2e) provides stand-in osv-scanner and
// opengrep tools and a rules folder; gitleaks is built in. The planted key below matches gitleaks' aws-access-token rule and
// is not a real credential; the page must name the rule and the place and never show the value.
import { expect, test } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createProject, freshRepo } from './helpers';

const PLANTED = 'AKIA2W7X3KQ5ZV4YBN6T';

test('one button scans the working tree with three engines, names the findings without the secret, and keeps a history', async ({ page }) => {
  const repo = freshRepo();
  await createProject(page, 'gocalc-security', repo);
  writeFileSync(join(repo, 'notes.txt'), `aws_access_key_id = ${PLANTED}\n`);

  await page.getByTestId('nav-security').click();
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

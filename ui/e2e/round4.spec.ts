import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { appUrl, createAgent, createProject, freshRepo, licensedRepo, licensedUrl, licenseTokens, readZip, roomLink, showNewProjectForm, switchProject, wizardCreate, wizardToReview, workspaceNav, projectNav, openRoom } from './helpers';

// Round 4: license, backups, project export and import, support bundle, updates. Real Go core. The default harness is a build
// with NO license keys; the `-licensed` harness is a TEST HARNESS MODE with a throwaway key (see cmd/drydock-e2e/main.go).

const PLANTED = 'ghp_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5';

test('no license keys in this build: no banner, the License page says so plainly, nothing is disabled, and Updates is honest', async ({ page }) => {
  await createProject(page, 'pkg-unlicensed');
  const banner = page.getByTestId('license-banner');
  await expect(banner).toHaveCount(0);

  await workspaceNav(page, 'nav-license');
  const box = page.getByTestId('license-unconfigured');
  await expect(box).toContainText('Licensing is not configured in this build');
  await expect(box).toContainText('Every feature is allowed');
  await expect(page.getByLabel('License token')).toHaveCount(0);
  // still no banner, and a mutating control is not disabled
  await expect(banner).toHaveCount(0);
  await projectNav(page, 'nav-team');
  await expect(page.getByTestId('readonly-note')).toHaveCount(0);
  await page.getByLabel('Agent name').fill('x');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await expect(page.getByRole('button', { name: 'Create agent' })).toBeEnabled();

  await workspaceNav(page, 'nav-settings');
  const up = page.getByTestId('updates-section');
  await expect(up).toContainText('configured when Drydock is built');
  await expect(up).toContainText('not been run on Windows');
  await expect(up.getByRole('button')).toHaveCount(0);
});

test('backups: back up now, list, verify, stage a restore, see it pending after a reload, cancel it', async ({ page }) => {
  await createProject(page, 'pkg-backups');
  await workspaceNav(page, 'nav-backups');
  await expect(page.getByRole('heading', { level: 1, name: 'Backups' })).toBeVisible();

  await page.getByLabel(/^Label \(optional/).fill('e2e-backup');
  await page.getByRole('button', { name: 'Back up now' }).click();
  await expect(page.getByTestId('backup-made')).toContainText('written');
  // the new backup is listed once; other rows may be there (a scheduled backup the core made at start lands any time), so
  // the list is not counted against a number read before it loaded
  const row = page.getByTestId('backup-row').filter({ hasText: 'e2e-backup' });
  await expect(row).toHaveCount(1);
  await expect(row).toBeVisible();
  const name = (await row.getAttribute('data-name'))!;
  await expect(page.getByTestId('backup-last-success')).not.toHaveText('none yet');

  // restore needs a successful verify first
  await expect(page.getByRole('button', { name: `Restore ${name}` })).toBeDisabled();
  await page.getByRole('button', { name: `Verify ${name}` }).click();
  const verified = page.getByTestId('verify-result').filter({ hasText: name });
  await expect(verified).toHaveAttribute('data-ok', 'true');
  await expect(verified).toHaveAttribute('data-restorable', 'true');
  await expect(page.getByRole('button', { name: `Restore ${name}` })).toBeEnabled();

  try {
    await page.getByRole('button', { name: `Restore ${name}` }).click();
    const confirm = page.getByTestId('restore-confirm');
    await expect(confirm).toContainText('staged, not applied now');
    await expect(confirm).toContainText('Nothing is destroyed');
    // declining stages nothing
    await confirm.getByRole('button', { name: 'Do not restore' }).click();
    await expect(page.getByTestId('restore-pending')).toHaveCount(0);

    await page.getByRole('button', { name: `Restore ${name}` }).click();
    await page.getByRole('button', { name: 'Stage this restore for the next start' }).click();
    const pending = page.getByTestId('restore-pending');
    await expect(pending).toContainText(name);
    await expect(pending).toContainText('next time Drydock starts');
    await expect(pending).toContainText('Nothing has changed yet');
    await expect(page.getByTestId('restore-staged')).toContainText('staged');
    // the pending state is the core's, not the page's: it survives a reload
    await page.reload();
    await expect(page.getByTestId('restore-pending')).toContainText(name);
    // the app still works while a restore is staged
    await expect(page.getByTestId('backup-row').filter({ hasText: 'e2e-backup' })).toHaveCount(1);
  } finally {
    const cancel = page.getByRole('button', { name: 'Cancel the staged restore' });
    if (await cancel.isVisible()) await cancel.click();
  }
  await expect(page.getByTestId('restore-pending')).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId('restore-pending')).toHaveCount(0);

  // the schedule is saved by the core
  await page.getByLabel('Daily backups to keep').fill('9');
  await page.getByRole('button', { name: 'Save schedule' }).click();
  await expect(page.getByTestId('settings-saved')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Daily backups to keep')).toHaveValue('9');
  // a relative folder is refused with the core's words
  await page.getByLabel(/Backup folder/).fill('relative/dir');
  await page.getByRole('button', { name: 'Save schedule' }).click();
  await expect(page.getByRole('alert')).toContainText('must be an absolute path');
});

test('export a project, preview what is in it, import it into a second project; everything that comes in is untrusted', async ({ page }) => {
  await createProject(page, 'pkg-export-src');
  await workspaceNav(page, 'nav-data');
  await expect(page.getByRole('heading', { level: 1, name: 'Export and import' })).toBeVisible();

  await page.getByLabel('Project to export').selectOption({ label: 'pkg-export-src' });
  await page.getByRole('button', { name: 'Preview the export' }).click();
  const preview = page.getByTestId('export-preview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText('pkg-export-src');
  await expect(preview.getByRole('table', { name: 'Files in the export' }).getByRole('row')).not.toHaveCount(1);
  await expect(page.getByTestId('export-scan')).toContainText('Raw run output is not included');
  await expect(page.getByTestId('export-never')).toContainText('the repository');
  await expect(page.getByTestId('export-never')).toContainText('provider API keys');

  // raw run output is opt-in and needs an acknowledgement; the stale preview goes away when the choice changes
  await page.getByLabel(/Include raw run output/).check();
  await expect(page.getByTestId('export-preview')).toHaveCount(0);
  await page.getByRole('button', { name: 'Preview the export' }).click();
  await expect(page.getByTestId('export-runs-warning')).toContainText('Raw run output');
  await expect(page.getByRole('button', { name: 'Export', exact: true })).toBeDisabled();
  await page.getByLabel(/I have read the secret-scan result/).check();
  await expect(page.getByRole('button', { name: 'Export', exact: true })).toBeEnabled();
  await page.getByLabel(/Include raw run output/).uncheck();
  await page.getByRole('button', { name: 'Preview the export' }).click();
  await expect(page.getByTestId('export-scan')).toContainText('Raw run output is not included');

  await page.getByRole('button', { name: 'Export', exact: true }).click();
  const path = (await page.getByTestId('export-path').textContent())!;
  expect(path).toContain('.drydock-project.zip');
  await expect(page.getByTestId('export-result')).toContainText('Nothing was uploaded');
  const zip = readZip(path);
  expect(Object.keys(zip)).toContain('manifest.json');

  // import it as a NEW project, linked to a different repository
  await page.getByLabel(/Archive path/).fill(path);
  await page.getByRole('button', { name: 'Preview the import' }).click();
  const untrusted = page.getByTestId('import-untrusted');
  await expect(untrusted).toContainText('Memory entries proposed');
  await expect(untrusted).toContainText('Skills offered');
  await expect(untrusted).toContainText('Providers that come without keys');
  await expect(untrusted).toContainText('Runs not resumed (cancelled)');
  await expect(untrusted).toContainText('Approvals cancelled');
  await expect(page.getByRole('button', { name: 'Import as a new project' })).toBeDisabled();
  await page.getByLabel(/Local repository to link/).fill(freshRepo());
  await page.getByLabel(/New project name/).fill('pkg-export-copy');
  await page.getByRole('button', { name: 'Import as a new project' }).click();
  await expect(page.getByTestId('import-result-name')).toHaveText('pkg-export-copy');
  await expect(page.getByTestId('import-result')).toContainText('It came in untrusted');

  // the second project exists, is separate, and opens
  // projects are chosen in the switcher at the top of the sidebar (a menu of radio items)
  await switchProject(page, 'pkg-export-copy');
  await expect(roomLink(page, 'general')).toBeVisible();
  await openRoom(page, 'general');
  await expect(page.getByTestId('message').filter({ hasText: 'Imported "pkg-export-copy" as a new project' })).toBeVisible();
  await expect(page.getByTestId('message').filter({ hasText: 'Nothing from the file is trusted yet' })).toBeVisible();

  // a path that is not an archive is refused with the core's words
  await workspaceNav(page, 'nav-data');
  await page.getByLabel(/Archive path/).fill('/definitely/not/there.zip');
  await page.getByRole('button', { name: 'Preview the import' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
});

test('support bundle: preview lists files and the scrub report, a planted secret never appears, create writes a local file that does not contain it', async ({ page }) => {
  await createProject(page, 'pkg-support');
  await openRoom(page, 'general');
  await page.getByLabel('Message to #general').fill(`my token is ${PLANTED} please do not leak it`);
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.locator('[data-testid=message][data-author-kind=human]').filter({ hasText: 'please do not leak it' })).toBeVisible();

  await workspaceNav(page, 'nav-support');
  await expect(page.getByTestId('bundle-never-uploaded')).toContainText('never uploads');
  await page.getByRole('button', { name: 'Preview the bundle' }).click();
  const preview = page.getByTestId('bundle-preview');
  await expect(preview).toContainText('Nothing has been written yet');
  await expect(page.getByTestId('bundle-file').first()).toBeVisible();
  expect(await page.getByTestId('bundle-file').count()).toBeGreaterThan(1);
  await expect(page.getByTestId('scrub-report')).toBeVisible();
  await expect(page.getByTestId('scrub-total')).toHaveText(/^\d+$/);

  // the planted secret is in neither the file list nor the scrub report, nor anywhere on the page
  const text = (await preview.textContent())!;
  expect(text).not.toContain(PLANTED);
  expect(text).not.toContain('aB3dE5fG7hJ9');
  expect(await page.locator('body').textContent()).not.toContain('aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5');

  await page.getByRole('button', { name: 'Create the bundle' }).click();
  const created = page.getByTestId('bundle-created');
  await expect(created).toContainText('not been uploaded');
  const path = (await page.getByTestId('bundle-path').textContent())!;
  const entries = readZip(path);
  expect(Object.keys(entries).length).toBeGreaterThan(1);
  for (const [name, body] of Object.entries(entries)) {
    expect(body.toString('utf8'), `${name} must not contain the planted secret`).not.toContain(PLANTED);
  }
  // the zip on disk matches what the preview listed
  const listed = await page.getByTestId('bundle-file').allTextContents();
  expect(listed.length).toBe(Object.keys(entries).length);

  // including a run is opt-in; an unknown run is refused by the core before anything is written
  await page.getByLabel(/Include one run/).fill('run_does_not_exist');
  await page.getByRole('button', { name: 'Preview the bundle' }).click();
  await expect(page.getByRole('alert')).toContainText('there is no run');
  expect(readFileSync(path).length).toBeGreaterThan(0);
});

test('licensed test harness: no license is read-only and says why; expired is read-only; an active license turns work on; removing it turns work off again; reading, backup and export stay usable', async ({ page }) => {
  const { active, expired } = licenseTokens();
  expect(active.length).toBeGreaterThan(20);
  const url = licensedUrl();
  const token = new URL(url).searchParams.get('token')!;

  // 1. a build with keys and no license: read-only from the first screen
  await page.goto(url);
  const banner = page.getByTestId('license-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toHaveAttribute('role', 'alert');
  await expect(banner).toHaveAttribute('data-state', 'none');
  await expect(banner).toHaveAttribute('data-configured', 'true');
  await expect(banner).toContainText('No valid license');
  await showNewProjectForm(page);
  await expect(page.getByTestId('readonly-note')).toContainText('read-only');
  await wizardToReview(page, { repo: licensedRepo() });
  const create = page.getByRole('button', { name: 'Create project' });
  await expect(create).toBeDisabled();
  await expect(create).toHaveAttribute('title', /No valid license/);

  // the core refuses it too, and its words are what the page shows as the reason
  const refused = await page.request.post(`${new URL(url).origin}/rpc/project.create`, { headers: { 'X-Drydock-Token': token }, data: { name: 'x', repoPath: licensedRepo() } });
  expect(refused.ok()).toBe(false);
  expect(await refused.text()).toContain('read-only');

  // 2. an expired license: still read-only, with the expired wording
  await workspaceNav(page, 'nav-license');
  await expect(page.getByTestId('license-state')).toHaveText('No license');
  await page.getByLabel('License token').fill(expired);
  await page.getByRole('button', { name: 'Import license' }).click();
  await expect(page.getByTestId('license-state')).toHaveText('Expired');
  await expect(banner).toHaveAttribute('data-state', 'expired');
  await expect(banner).toContainText('License expired');
  await expect(page.getByTestId('license-readonly')).toContainText('Nothing has been deleted');
  // the token is gone from the field and the page
  await expect(page.getByLabel('License token')).toHaveValue('');
  expect(await page.content()).not.toContain(expired.slice(0, 40));

  // a garbage token is refused with the core's words
  await page.getByLabel('License token').fill('not-a-token');
  await page.getByRole('button', { name: 'Import license' }).click();
  await expect(page.locator('.error-note')).toContainText('License');

  // 3. an active license: work is on, the banner is gone
  await page.getByLabel('License token').fill(active);
  await page.getByRole('button', { name: 'Import license' }).click();
  await expect(page.getByTestId('license-state')).toHaveText('Active');
  await expect(page.getByTestId('license-days')).toContainText('29 day(s)');
  await expect(page.getByTestId('license-banner')).toHaveCount(0);
  expect(await page.content()).not.toContain(active.slice(0, 40));

  await page.goto(url);
  await showNewProjectForm(page);
  await wizardToReview(page, { name: 'pkg-licensed', repo: licensedRepo() });
  await expect(page.getByRole('button', { name: 'Create project' })).toBeEnabled();
  await wizardCreate(page);
  await expect(roomLink(page, 'general')).toBeVisible();
  await createAgent(page, 'lic-worker', 'backend');

  // an older token cannot replace the installed license: the core says so and the page shows its words
  await workspaceNav(page, 'nav-license');
  await page.getByLabel('License token').fill(expired);
  await page.getByRole('button', { name: 'Import license' }).click();
  await expect(page.locator('.error-note')).toContainText('older than the one installed');
  await expect(page.getByTestId('license-state')).toHaveText('Active');

  // 4. remove the license: read-only again. Mutating controls are disabled with the reason; reading, backup, export and support stay usable
  await page.getByRole('button', { name: 'Remove the installed license' }).click();
  await page.getByRole('button', { name: 'Yes, remove it' }).click();
  await expect(page.getByTestId('license-state')).toHaveText('No license');
  await expect(page.getByTestId('license-banner')).toHaveAttribute('role', 'alert');
  await expect(page.getByTestId('license-banner')).toHaveAttribute('data-state', 'none');

  await projectNav(page, 'nav-team');
  await expect(page.getByTestId('agent').filter({ hasText: 'lic-worker' })).toBeVisible(); // reading still works
  await page.getByLabel('Agent name').fill('blocked');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await expect(page.getByTestId('readonly-note')).toBeVisible();
  const make = page.getByRole('button', { name: 'Create agent' });
  await expect(make).toBeDisabled();
  await expect(make).toHaveAttribute('title', /No valid license/);
  const refusedAgent = await page.request.post(`${new URL(url).origin}/rpc/agent.create`, { headers: { 'X-Drydock-Token': token }, data: { projectId: 'x', name: 'n', role: 'r', adapter: 'claude' } });
  expect(refusedAgent.ok()).toBe(false);
  expect(await refusedAgent.text()).toContain('read-only');

  await workspaceNav(page, 'nav-providers');
  await page.getByTestId('provider-add-tile').click();
  await page.getByLabel('Provider name').fill('blocked');
  await expect(page.getByRole('button', { name: 'Add provider' })).toBeDisabled();

  await workspaceNav(page, 'nav-backups');
  await page.getByRole('button', { name: 'Back up now' }).click();
  await expect(page.getByTestId('backup-made')).toContainText('written');
  await workspaceNav(page, 'nav-data');
  await page.getByLabel('Project to export').selectOption({ label: 'pkg-licensed' });
  await page.getByRole('button', { name: 'Preview the export' }).click();
  await expect(page.getByTestId('export-preview')).toBeVisible();
  await page.getByRole('button', { name: 'Export', exact: true }).click();
  await expect(page.getByTestId('export-result')).toBeVisible();
  await workspaceNav(page, 'nav-support');
  await page.getByRole('button', { name: 'Preview the bundle' }).click();
  await expect(page.getByTestId('bundle-preview')).toBeVisible();

  // 5. importing a license turns it back on without a restart
  await workspaceNav(page, 'nav-license');
  await page.getByLabel('License token').fill(active);
  await page.getByRole('button', { name: 'Import license' }).click();
  await expect(page.getByTestId('license-state')).toHaveText('Active');
  await expect(page.getByTestId('license-banner')).toHaveCount(0);
  await projectNav(page, 'nav-team');
  await page.getByLabel('Agent name').fill('now-ok');
  await page.getByLabel('Role', { exact: true }).fill('backend');
  await expect(page.getByRole('button', { name: 'Create agent' })).toBeEnabled();
});

test('the main harness page also loads through appUrl, and no route shows a license banner in a build without licensing', async ({ page }) => {
  await page.goto(appUrl());
  await page.goto(appUrl() + '/home');
  await expect(page.getByTestId('nav-settings')).toBeVisible();
  await workspaceNav(page, 'nav-license');
  await expect(page.getByTestId('nav-license')).toHaveAttribute('aria-current', 'page');
  await expect(page.getByTestId('license-banner')).toHaveCount(0);
  // an old #/ link still opens its screen (the UI routes by path now)
  await page.goto(appUrl() + '#/settings');
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByTestId('updates-section')).toBeVisible();
  await expect(page.getByTestId('license-banner')).toHaveCount(0);
});

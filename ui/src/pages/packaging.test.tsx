import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { BackupList, BackupSettings, BundlePreview, ExportManifest, ExportPreview, ImportPreview, ImportStats, LicenseView } from '../api/pkgTypes';
import type { Project } from '../api/types';
import { ProjectCreate } from '../components/ProjectPages';
import { SchedulerPanel } from '../components/Scheduler';
import { UpdatesSection } from '../components/Settings';
import { AppServicesProvider } from '../state/context';
import { LicenseBanner, LicenseProvider } from '../state/license';
import { services } from '../test/fakes';
import BackupsPage from './Backups';
import DataPage from './Data';
import LicensePage from './License';
import SupportPage from './Support';

const withServices = (ui: React.ReactElement, client: Partial<Client> = {}) => render(<AppServicesProvider services={services(client)}>{ui}</AppServicesProvider>);

const lic = (over: Partial<LicenseView> = {}): LicenseView => ({
  configured: true, devMode: false, state: 'active', readOnly: false, message: 'paid license valid for about 40 more day(s).', daysLeft: 40, expiresAt: 1893456000,
  kind: 'paid', plan: 'pro', name: 'Ada', subject: 'ada@example.com', licenseId: 'lic_1', channels: ['stable'], clockSuspect: false, ...over,
});
const unconfigured = lic({
  configured: false, devMode: true, state: 'none', readOnly: false, daysLeft: 0, expiresAt: undefined, kind: undefined, plan: undefined, name: undefined, subject: undefined, licenseId: undefined,
  message: 'Licensing is not configured in this build. This is a development or unlicensed build, so every feature is allowed.',
  banner: 'LICENSING NOT CONFIGURED: development build, nothing is restricted.',
});
const expired = lic({
  state: 'expired', readOnly: true, daysLeft: 0, message: 'License expired. Drydock is read-only.',
  banner: 'License expired: Drydock is read-only. You can read, export and back up everything; nothing has been deleted. Import a renewed license to continue.',
});

describe('License page', () => {
  it('says plainly that licensing is not configured and offers no import', async () => {
    withServices(<LicensePage />, { licenseStatus: async () => unconfigured });
    const box = await screen.findByTestId('license-unconfigured');
    expect(box.textContent).toContain('Licensing is not configured in this build');
    expect(box.textContent).toContain('Every feature is allowed');
    expect(screen.queryByLabelText('License token')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Import license' })).toBeNull();
  });

  it('shows state, days left and what the license entitles', async () => {
    withServices(<LicensePage />, { licenseStatus: async () => lic({ state: 'trial', kind: 'trial', daysLeft: 12, channels: ['stable', 'beta'] }) });
    await screen.findByTestId('license-details');
    expect(screen.getByTestId('license-state').textContent).toBe('Trial');
    expect(screen.getByTestId('license-days').textContent).toContain('12 day(s)');
    expect(screen.getByText('stable, beta')).toBeTruthy();
    expect(screen.queryByTestId('license-readonly')).toBeNull();
  });

  it('shows grace days left', async () => {
    withServices(<LicensePage />, { licenseStatus: async () => lic({ state: 'grace', daysLeft: 5, message: 'License expired; full functionality for 5 more grace day(s).' }) });
    await screen.findByTestId('license-details');
    expect(screen.getByTestId('license-state').textContent).toBe('Grace period');
    expect(screen.getByText('Grace days left')).toBeTruthy();
    expect(screen.getByTestId('license-days').textContent).toContain('5 day(s)');
  });

  it('explains read-only: what is still usable and what is off', async () => {
    withServices(<LicensePage />, { licenseStatus: async () => expired });
    const box = await screen.findByTestId('license-readonly');
    expect(box.textContent).toContain('Nothing has been deleted');
    expect(within(box).getByText('Export')).toBeTruthy();
    expect(within(box).getByText('Starting tasks and workflows')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Import license' }).hasAttribute('disabled')).toBe(false);
  });

  it('sends the token once, clears the field and never shows it back', async () => {
    const licenseImport = vi.fn(async (_r: { token?: string; path?: string }) => lic({ message: 'paid license valid for about 365 more day(s).' }));
    withServices(<LicensePage />, { licenseStatus: async () => expired, licenseImport });
    const field = (await screen.findByLabelText('License token')) as HTMLInputElement;
    expect(field.type).toBe('password');
    field.value = 'SECRET-TOKEN.abc';
    fireEvent.click(screen.getByRole('button', { name: 'Import license' }));
    await waitFor(() => expect(licenseImport).toHaveBeenCalledWith({ token: 'SECRET-TOKEN.abc' }));
    expect(field.value).toBe('');
    await screen.findByTestId('license-done');
    expect(document.body.textContent).not.toContain('SECRET-TOKEN');
  });

  it('shows the core refusal verbatim and still clears the field', async () => {
    const licenseImport = vi.fn(async () => {
      throw new Error('this is not a Drydock license token');
    });
    withServices(<LicensePage />, { licenseStatus: async () => expired, licenseImport });
    const field = (await screen.findByLabelText('License token')) as HTMLInputElement;
    field.value = 'junk';
    fireEvent.click(screen.getByRole('button', { name: 'Import license' }));
    expect((await screen.findByRole('alert')).textContent).toContain('this is not a Drydock license token');
    expect(field.value).toBe('');
  });

  it('imports from a file path when no token is pasted, and removing needs a confirmation', async () => {
    const licenseImport = vi.fn(async () => lic());
    const licenseClear = vi.fn(async () => expired);
    withServices(<LicensePage />, { licenseStatus: async () => lic(), licenseImport, licenseClear });
    fireEvent.change(await screen.findByLabelText('Or a license file path'), { target: { value: 'C:\\x\\a.drydock-license' } });
    fireEvent.click(screen.getByRole('button', { name: 'Import license' }));
    await waitFor(() => expect(licenseImport).toHaveBeenCalledWith({ path: 'C:\\x\\a.drydock-license' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove the installed license' }));
    expect(licenseClear).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Yes, remove it' }));
    await waitFor(() => expect(licenseClear).toHaveBeenCalledTimes(1));
  });
});

describe('License banner and read-only controls', () => {
  const shell = (view: LicenseView, ui: React.ReactElement, client: Partial<Client> = {}) =>
    withServices(
      <LicenseProvider>
        <LicenseBanner />
        {ui}
      </LicenseProvider>,
      { licenseStatus: async () => view, ...client },
    );

  it('shows a loud notice when licensing is not configured, and disables nothing', async () => {
    shell(unconfigured, <ProjectCreate first={false} onCreated={() => undefined} />);
    const b = await screen.findByTestId('license-banner');
    expect(b.textContent).toContain('Licensing is not configured');
    expect(b.textContent).toContain('nothing is restricted');
    expect(b.getAttribute('data-configured')).toBe('false');
    fireEvent.change(screen.getByLabelText('Repository path'), { target: { value: 'C:\\r' } });
    expect(screen.getByRole('button', { name: 'Create project' }).hasAttribute('disabled')).toBe(false);
    expect(screen.queryByTestId('readonly-note')).toBeNull();
  });

  it('is an alert when expired and disables mutating controls with the core reason', async () => {
    shell(expired, <ProjectCreate first={false} onCreated={() => undefined} />);
    const b = await screen.findByTestId('license-banner');
    expect(b.getAttribute('role')).toBe('alert');
    expect(b.getAttribute('data-state')).toBe('expired');
    expect(b.textContent).toContain('nothing has been deleted');
    await screen.findByTestId('readonly-note');
    fireEvent.change(screen.getByLabelText('Repository path'), { target: { value: 'C:\\r' } });
    const btn = screen.getByRole('button', { name: 'Create project' });
    expect(btn.hasAttribute('disabled')).toBe(true);
    expect(btn.getAttribute('title')).toBe(expired.banner);
    expect(screen.getByTestId('readonly-note').textContent).toContain('Open License');
  });

  it('disables the scheduler tick when read-only and leaves reading alone', async () => {
    shell(expired, <SchedulerPanel projectId="prj_1" />, {
      schedulerStatus: async () => ({ settings: { autoSchedule: false, maxParallel: 0 }, activeRuns: 0, agentSlotsInUse: 0, peakAgentSlots: 0 }) as never,
    });
    await screen.findByTestId('readonly-note');
    expect(screen.getByRole('button', { name: 'Start ready tasks now' }).hasAttribute('disabled')).toBe(true);
    await screen.findByTestId('scheduler');
  });

  it('shows a grace warning that is not an alert, and an active license shows nothing', async () => {
    const { unmount } = shell(lic({ state: 'grace', daysLeft: 3, banner: 'License expired; full functionality for 3 more grace day(s). Renew to keep working.' }), <ProjectCreate first={false} onCreated={() => undefined} />);
    const b = await screen.findByTestId('license-banner');
    expect(b.getAttribute('role')).toBe('status');
    fireEvent.change(screen.getByLabelText('Repository path'), { target: { value: 'C:\\r' } });
    expect(screen.getByRole('button', { name: 'Create project' }).hasAttribute('disabled')).toBe(false);
    unmount();
    shell(lic(), <p>page</p>);
    await screen.findByText('page');
    await waitFor(() => expect(screen.queryByTestId('license-banner')).toBeNull());
  });

  it('shows the core refusal text verbatim when a mutating call is still refused', async () => {
    const refusal = 'Your license has expired, so Drydock is read-only: creating projects is turned off.';
    const projectCreate = vi.fn(async () => {
      throw new Error(refusal);
    });
    // a stale view: the page thinks it may create, the core says no
    shell(lic(), <ProjectCreate first={false} onCreated={() => undefined} />, { projectCreate });
    fireEvent.change(await screen.findByLabelText('Repository path'), { target: { value: 'C:\\r' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }));
    expect((await screen.findByRole('alert')).textContent).toContain(refusal);
  });
});

const settings = (over: Partial<BackupSettings> = {}): BackupSettings => ({
  enabled: true, dir: 'C:\\data\\backups', defaultDir: 'C:\\data\\backups', daily: 7, weekly: 4, lastSuccess: 1760000000000, ...over,
});
const backups = (over: Partial<BackupList> = {}): BackupList => ({
  dir: 'C:\\data\\backups',
  backups: [{ path: 'C:\\data\\backups\\b1.db', name: 'b1.db', size: 2048, sha256: 'abc', created: '2026-01-02T03:04:05Z', label: 'manual', keep: false }],
  replaced: [],
  settings: settings(),
  ...over,
});

describe('Backups page', () => {
  it('lists backups, the folder and last success', async () => {
    withServices(<BackupsPage />, { backupList: async () => backups(), backupRestoreStatus: async () => ({ pending: false }) });
    const row = await screen.findByTestId('backup-row');
    expect(row.getAttribute('data-name')).toBe('b1.db');
    expect(row.textContent).toContain('2.0 KB');
    expect(screen.getByTestId('backup-dir').textContent).toBe('C:\\data\\backups');
    expect(screen.queryByTestId('backup-last-error')).toBeNull();
    expect(screen.queryByTestId('restore-pending')).toBeNull();
  });

  it('shows the location warning and the last error', async () => {
    const s = settings({ dir: 'C:\\Users\\a\\OneDrive\\b', warning: 'This folder is synced by OneDrive.', lastError: 'disk full' });
    withServices(<BackupsPage />, { backupList: async () => backups({ settings: s, warning: s.warning }), backupRestoreStatus: async () => ({ pending: false }) });
    expect((await screen.findByTestId('backup-location-warning')).textContent).toContain('synced by OneDrive');
    expect(screen.getByTestId('backup-last-error').textContent).toContain('disk full');
  });

  it('backs up now with the label and reloads the list', async () => {
    const backupNow = vi.fn(async (_l?: string) => ({ backup: backups().backups![0]!, pruned: null }));
    const backupList = vi.fn(async () => backups());
    withServices(<BackupsPage />, { backupList, backupNow, backupRestoreStatus: async () => ({ pending: false }) });
    await screen.findByTestId('backup-row');
    fireEvent.change(screen.getByLabelText(/^Label \(optional/), { target: { value: 'before refactor' } });
    fireEvent.click(screen.getByRole('button', { name: 'Back up now' }));
    await screen.findByTestId('backup-made');
    expect(backupNow).toHaveBeenCalledWith('before refactor');
    expect(backupList.mock.calls.length).toBeGreaterThan(1);
  });

  it('requires a successful verify before restore, then explains the staged restore and stages it with confirm', async () => {
    const backupVerify = vi.fn(async (name: string) => ({ name, size: 2048, created: '', ok: true, schemaVersion: 30, restorableHere: true }));
    const backupRestore = vi.fn(async (name: string, _c: boolean) => ({ staged: true, restartRequired: true, backup: name, schemaVersion: 30, message: 'The restore is staged. Quit and reopen Drydock to apply it.', warnings: ['1 workflow run(s) are running and will be interrupted'] }));
    let pending = false;
    withServices(<BackupsPage />, {
      backupList: async () => backups(),
      backupVerify,
      backupRestore: async (n, c) => {
        const r = await backupRestore(n, c);
        pending = true;
        return r;
      },
      backupRestoreStatus: async () => (pending ? { pending: true, backup: 'b1.db', requested: 1760000000000 } : { pending: false }),
    });
    const restoreBtn = await screen.findByRole('button', { name: 'Restore b1.db' });
    expect(restoreBtn.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Verify b1.db' }));
    expect((await screen.findByTestId('verify-result')).getAttribute('data-ok')).toBe('true');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restore b1.db' }).hasAttribute('disabled')).toBe(false));
    fireEvent.click(screen.getByRole('button', { name: 'Restore b1.db' }));
    const c = await screen.findByTestId('restore-confirm');
    expect(c.textContent).toContain('staged, not applied now');
    expect(c.textContent).toContain('Nothing is destroyed');
    expect(backupRestore).not.toHaveBeenCalled();
    fireEvent.click(within(c).getByRole('button', { name: 'Stage this restore for the next start' }));
    await waitFor(() => expect(backupRestore).toHaveBeenCalledWith('b1.db', true));
    const p = await screen.findByTestId('restore-pending');
    expect(p.textContent).toContain('next time Drydock starts');
    expect(p.textContent).toContain('Nothing has changed yet');
    expect(screen.getByTestId('restore-warnings').textContent).toContain('will be interrupted');
    // a second restore cannot be staged on top
    expect(screen.getByRole('button', { name: 'Restore b1.db' }).hasAttribute('disabled')).toBe(true);
  });

  it('cancels a staged restore', async () => {
    let pending = true;
    const backupRestoreCancel = vi.fn(async () => {
      pending = false;
      return { cancelled: true };
    });
    withServices(<BackupsPage />, {
      backupList: async () => backups(),
      backupRestoreCancel,
      backupRestoreStatus: async () => (pending ? { pending: true, backup: 'b1.db', requested: 1760000000000 } : { pending: false }),
    });
    await screen.findByTestId('restore-pending');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel the staged restore' }));
    await waitFor(() => expect(backupRestoreCancel).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByTestId('restore-pending')).toBeNull());
  });

  it('keeps restore off for a damaged backup and shows why', async () => {
    withServices(<BackupsPage />, {
      backupList: async () => backups(),
      backupVerify: async (name) => ({ name, size: 1, created: '', ok: false, error: 'checksum mismatch' }),
      backupRestoreStatus: async () => ({ pending: false }),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Verify b1.db' }));
    expect((await screen.findByTestId('verify-result')).textContent).toContain('checksum mismatch');
    expect(screen.getByRole('button', { name: 'Restore b1.db' }).hasAttribute('disabled')).toBe(true);
  });

  it('keeps restore off for a backup from a newer Drydock', async () => {
    withServices(<BackupsPage />, {
      backupList: async () => backups(),
      backupVerify: async (name) => ({ name, size: 1, created: '', ok: true, schemaVersion: 99, restorableHere: false }),
      backupRestoreStatus: async () => ({ pending: false }),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Verify b1.db' }));
    expect((await screen.findByTestId('verify-result')).textContent).toContain('newer Drydock');
    expect(screen.getByRole('button', { name: 'Restore b1.db' }).hasAttribute('disabled')).toBe(true);
  });

  it('saves the schedule and shows the core validation message', async () => {
    const backupSettingsSet = vi.fn(async (_s: object) => settings());
    withServices(<BackupsPage />, { backupList: async () => backups(), backupSettingsSet, backupRestoreStatus: async () => ({ pending: false }) });
    fireEvent.change(await screen.findByLabelText('Daily backups to keep'), { target: { value: '10' } });
    fireEvent.change(screen.getByLabelText(/Backup folder/), { target: { value: 'D:\\bk' } });
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() => expect(backupSettingsSet).toHaveBeenCalledWith({ enabled: false, dir: 'D:\\bk', daily: 10, weekly: 4 }));
  });

  it('shows the core message when settings are refused', async () => {
    const backupSettingsSet = vi.fn(async () => {
      throw new Error('the backup folder must be an absolute path');
    });
    withServices(<BackupsPage />, { backupList: async () => backups(), backupSettingsSet, backupRestoreStatus: async () => ({ pending: false }) });
    fireEvent.change(await screen.findByLabelText(/Backup folder/), { target: { value: 'rel' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save schedule' }));
    expect((await screen.findByRole('alert')).textContent).toContain('must be an absolute path');
  });

  it('lists the databases kept from earlier restores', async () => {
    withServices(<BackupsPage />, { backupList: async () => backups({ replaced: ['replaced-20260101.db'] }), backupRestoreStatus: async () => ({ pending: false }) });
    expect((await screen.findByTestId('replaced-list')).textContent).toContain('replaced-20260101.db');
  });
});

const manifest = (over: Partial<ExportManifest> = {}): ExportManifest => ({
  format: 1, kind: 'drydock-project', appVersion: '0.0.0-dev', schemaVersion: 30, projectId: 'prj_1', projectName: 'Alpha', created: 1760000000000, includesRuns: false,
  files: [
    { path: 'drydock.db', size: 4096, sha256: 'a' },
    { path: 'manifest.json', size: 100, sha256: 'b' },
  ],
  tables: { tasks: 3 },
  scan: { dbRedactions: 2, dbByRule: { 'generic-api-key': 2 }, runsFilesScanned: 0, runsRedactions: 0 },
  neverIncluded: ['secrets and Credential Manager entries', 'the repository', 'worktree contents'],
  ...over,
});
const proj = (id: string, name: string): Project => ({ id, name }) as Project;
const pending = (over: Partial<ImportStats> = {}): ImportStats => ({
  memoryPending: 2, skillsPending: 1, requirementChunksQuarantined: 3, routingRulesDisabled: 1, runsNotResumed: 1, tasksBlocked: 1, approvalsCancelled: 2,
  providersNeedingKeys: [{ id: 'prv_1', name: 'OpenRouter', baseUrl: 'https://openrouter.ai/api/v1' }], ...over,
});

describe('Export and import page', () => {
  it('previews an export: files, secret-scan result and what is never included', async () => {
    const projectExportPreview = vi.fn(async (): Promise<ExportPreview> => ({ manifest: manifest(), totalBytes: 4196, includeRuns: false }));
    withServices(<DataPage projects={[proj('prj_1', 'Alpha')]} current={proj('prj_1', 'Alpha')} />, { projectExportPreview });
    fireEvent.click(screen.getByRole('button', { name: 'Preview the export' }));
    const pv = await screen.findByTestId('export-preview');
    expect(projectExportPreview).toHaveBeenCalledWith('prj_1', false);
    expect(within(pv).getByText('drydock.db')).toBeTruthy();
    expect(screen.getByTestId('export-scan').textContent).toContain('2');
    expect(screen.getByTestId('export-scan').textContent).toContain('Raw run output is not included');
    expect(screen.getByTestId('export-never').textContent).toContain('the repository');
  });

  it('exports into the default place without run output', async () => {
    const projectExport = vi.fn(async () => ({ path: 'C:\\data\\exports\\alpha.drydock-project.zip', size: 5000, manifest: manifest() }));
    withServices(<DataPage projects={[proj('prj_1', 'Alpha')]} current={proj('prj_1', 'Alpha')} />, {
      projectExportPreview: async () => ({ manifest: manifest(), totalBytes: 4196, includeRuns: false }),
      projectExport,
    });
    expect(screen.queryByRole('button', { name: 'Export' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Preview the export' }));
    await screen.findByTestId('export-preview');
    fireEvent.click(screen.getByRole('button', { name: 'Export' }));
    expect((await screen.findByTestId('export-path')).textContent).toContain('alpha.drydock-project.zip');
    expect(projectExport).toHaveBeenCalledWith('prj_1', { dest: undefined, includeRuns: false, acknowledgeRuns: false });
    expect(screen.getByTestId('export-result').textContent).toContain('Nothing was uploaded');
  });

  it('makes raw run output opt-in with an acknowledge step, and a changed choice invalidates the preview', async () => {
    const projectExportPreview = vi.fn(async (_p: string, runs: boolean): Promise<ExportPreview> => ({
      manifest: manifest({ includesRuns: runs, scan: { dbRedactions: 0, runsFilesScanned: 12, runsRedactions: 4, runsByRule: { 'aws-key': 4 } } }),
      totalBytes: 9000,
      includeRuns: runs,
      warning: runs ? 'Raw run output can contain your source code.' : undefined,
    }));
    const projectExport = vi.fn(async (_p: string, _o: object) => ({ path: 'x.zip', size: 1, manifest: manifest() }));
    withServices(<DataPage projects={[proj('prj_1', 'Alpha')]} current={proj('prj_1', 'Alpha')} />, { projectExportPreview, projectExport });
    fireEvent.click(screen.getByLabelText(/Include raw run output/));
    fireEvent.click(screen.getByRole('button', { name: 'Preview the export' }));
    expect((await screen.findByTestId('export-runs-warning')).textContent).toContain('source code');
    expect(screen.getByTestId('export-scan').textContent).toContain('12 file(s) scanned');
    const exportBtn = screen.getByRole('button', { name: 'Export' });
    expect(exportBtn.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText(/I have read the secret-scan result/));
    expect(exportBtn.hasAttribute('disabled')).toBe(false);
    fireEvent.click(exportBtn);
    await waitFor(() => expect(projectExport).toHaveBeenCalledWith('prj_1', { dest: undefined, includeRuns: true, acknowledgeRuns: true }));
    // turning the option off hides the stale preview and the export button
    fireEvent.click(screen.getByLabelText(/Include raw run output/));
    expect(screen.queryByTestId('export-preview')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Export' })).toBeNull();
  });

  it('previews an import and lists everything that comes in untrusted, then imports with a repo path', async () => {
    const preview: ImportPreview = { manifest: manifest(), appSchema: 30, needsMigration: false, counts: { tasks: 3 }, pending: pending() };
    const projectImportPreview = vi.fn(async (_p: string) => preview);
    const projectImport = vi.fn(async (_r: object) => ({ projectId: 'prj_new', name: 'Alpha (imported)', sourceProjectId: 'prj_1', counts: { tasks: 3 }, idsRemapped: 10, runFilesRestored: 0, migratedForward: false, pending: pending(), sourceSchema: 30 }));
    withServices(<DataPage projects={[proj('prj_1', 'Alpha')]} current={proj('prj_1', 'Alpha')} />, { projectImportPreview, projectImport });
    expect(screen.getByRole('button', { name: 'Preview the import' }).hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText(/Archive path/), { target: { value: 'C:\\x\\a.drydock-project.zip' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview the import' }));
    const u = await screen.findByTestId('import-untrusted');
    expect(projectImportPreview).toHaveBeenCalledWith('C:\\x\\a.drydock-project.zip');
    expect(u.textContent).toContain('Memory entries proposed: 2');
    expect(u.textContent).toContain('Skills offered: 1');
    expect(u.textContent).toContain('Providers that come without keys: 1');
    expect(u.textContent).toContain('disabled and unauthenticated');
    expect(u.textContent).toContain('OpenRouter');
    expect(u.textContent).toContain('Runs not resumed (cancelled): 1');
    expect(u.textContent).toContain('Approvals cancelled: 2');
    const importBtn = screen.getByRole('button', { name: 'Import as a new project' });
    expect(importBtn.hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText(/Local repository to link/), { target: { value: 'C:\\src\\repo' } });
    fireEvent.change(screen.getByLabelText(/New project name/), { target: { value: 'Copy' } });
    fireEvent.click(importBtn);
    expect((await screen.findByTestId('import-result-name')).textContent).toBe('Alpha (imported)');
    expect(projectImport).toHaveBeenCalledWith({ path: 'C:\\x\\a.drydock-project.zip', repoPath: 'C:\\src\\repo', name: 'Copy' });
    expect(screen.getByTestId('import-result').getAttribute('data-project-id')).toBe('prj_new');
  });

  it('shows the core error for a bad archive and for a bad repository', async () => {
    withServices(<DataPage projects={[proj('prj_1', 'Alpha')]} current={undefined} />, {
      projectImportPreview: async () => {
        throw new Error('not a Drydock project archive');
      },
    });
    fireEvent.change(screen.getByLabelText(/Archive path/), { target: { value: 'nope.zip' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview the import' }));
    expect((await screen.findByRole('alert')).textContent).toContain('not a Drydock project archive');
  });
});

const bundlePreview = (over: Partial<BundlePreview> = {}): BundlePreview => ({
  files: [
    { name: 'summary.json', size: 1200 },
    { name: 'logs/drydock.log', size: 3000 },
  ],
  report: {
    files: [
      { name: 'summary.json', secretsRedacted: 0 },
      { name: 'logs/drydock.log', secretsRedacted: 2, byRule: { 'generic-api-key': 2 }, identityTokens: { user: 3 } },
    ],
    totalSecretsRedacted: 2,
    byRule: { 'generic-api-key': 2 },
    notice: 'Scrubbing reduces but does not eliminate leakage of proprietary code. Review the file list and send the file yourself; Drydock never uploads it.',
  },
  notice: 'x',
  ...over,
});

describe('Support bundle page', () => {
  it('says it is never uploaded and previews files and the scrub report without writing anything', async () => {
    const supportbundlePreview = vi.fn(async (_o: object) => bundlePreview());
    const supportbundleCreate = vi.fn();
    withServices(<SupportPage />, { supportbundlePreview, supportbundleCreate });
    expect(screen.getByTestId('bundle-never-uploaded').textContent).toContain('never uploads');
    fireEvent.click(screen.getByRole('button', { name: 'Preview the bundle' }));
    const pv = await screen.findByTestId('bundle-preview');
    expect(pv.textContent).toContain('Nothing has been written yet');
    expect(screen.getAllByTestId('bundle-file').map((r) => r.textContent)).toEqual(['summary.json1.2 KB', 'logs/drydock.log2.9 KB']);
    expect(screen.getByTestId('scrub-total').textContent).toBe('2');
    expect(pv.textContent).toContain('generic-api-key');
    expect(supportbundleCreate).not.toHaveBeenCalled();
    expect(supportbundlePreview).toHaveBeenCalledWith({ eventLimit: undefined, includeRun: undefined });
  });

  it('creates the bundle and reports where it was written', async () => {
    const supportbundleCreate = vi.fn(async (_o: object) => ({ path: 'C:\\data\\support\\b.zip', size: 4000, files: bundlePreview().files, report: bundlePreview().report, notice: 'x' }));
    withServices(<SupportPage />, { supportbundlePreview: async () => bundlePreview(), supportbundleCreate });
    expect(screen.queryByRole('button', { name: 'Create the bundle' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Preview the bundle' }));
    await screen.findByTestId('bundle-preview');
    fireEvent.click(screen.getByRole('button', { name: 'Create the bundle' }));
    expect((await screen.findByTestId('bundle-path')).textContent).toBe('C:\\data\\support\\b.zip');
    expect(supportbundleCreate).toHaveBeenCalledWith({ eventLimit: undefined, includeRun: undefined, dest: undefined, acknowledgeRun: false });
    expect(screen.getByTestId('bundle-created').textContent).toContain('not been uploaded');
  });

  it('makes including a run opt-in: shows the scan result first and needs an acknowledgement', async () => {
    const supportbundleCreate = vi.fn(async (_o: object) => ({ path: 'b.zip', size: 1, files: null, report: bundlePreview().report, notice: 'x' }));
    const supportbundlePreview = vi.fn(async (_o: object) => bundlePreview({ warning: 'Including a run adds its events with payloads and its raw output.', includesRun: 'run_1' }));
    withServices(<SupportPage />, { supportbundlePreview, supportbundleCreate });
    fireEvent.change(screen.getByLabelText(/Include one run/), { target: { value: ' run_1 ' } });
    fireEvent.change(screen.getByLabelText(/Recent events to list/), { target: { value: '200' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview the bundle' }));
    expect((await screen.findByTestId('bundle-run-warning')).textContent).toContain('raw output');
    expect(supportbundlePreview).toHaveBeenCalledWith({ eventLimit: 200, includeRun: 'run_1' });
    const create = screen.getByRole('button', { name: 'Create the bundle' });
    expect(create.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText(/I have read the scrub report/));
    expect(create.hasAttribute('disabled')).toBe(false);
    fireEvent.click(create);
    await waitFor(() => expect(supportbundleCreate).toHaveBeenCalledWith({ eventLimit: 200, includeRun: 'run_1', dest: undefined, acknowledgeRun: true }));
    // changing the run id makes the preview stale: create goes away until previewed again
    fireEvent.change(screen.getByLabelText(/Include one run/), { target: { value: 'run_2' } });
    expect(screen.getByTestId('bundle-preview').getAttribute('data-fresh')).toBe('false');
    expect(screen.queryByRole('button', { name: 'Create the bundle' })).toBeNull();
  });

  it('shows the core refusal for an unknown run', async () => {
    withServices(<SupportPage />, {
      supportbundlePreview: async () => {
        throw new Error('there is no run run_x');
      },
    });
    fireEvent.change(screen.getByLabelText(/Include one run/), { target: { value: 'run_x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview the bundle' }));
    expect((await screen.findByRole('alert')).textContent).toContain('there is no run run_x');
  });
});

describe('Settings Updates section', () => {
  it('says updates are build-time and untested on Windows, with no buttons', () => {
    render(<UpdatesSection />);
    const s = screen.getByTestId('updates-section');
    expect(s.textContent).toContain('configured when Drydock is built');
    expect(s.textContent).toContain('not been run on Windows');
    expect(s.textContent).toContain('backup before every update');
    expect(within(s).queryAllByRole('button')).toHaveLength(0);
  });
});

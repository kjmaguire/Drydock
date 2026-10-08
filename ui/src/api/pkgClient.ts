import type { Transport } from './transport';
import type {
  BackupList,
  BackupResult,
  BackupSettings,
  BackupVerify,
  BundleCreated,
  BundlePreview,
  ExportPreview,
  ExportResult,
  ImportPreview,
  ImportResult,
  LicenseView,
  RestoreStaged,
  RestoreStatus,
} from './pkgTypes';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export interface PkgClient {
  licenseStatus(): Promise<LicenseView>;
  /** Exactly one of token and path. The token is sent once and never kept. */
  licenseImport(req: { token?: string; path?: string }): Promise<LicenseView>;
  licenseClear(): Promise<LicenseView>;
  backupList(): Promise<BackupList>;
  backupNow(label?: string): Promise<BackupResult>;
  backupVerify(name: string): Promise<BackupVerify>;
  backupRestore(name: string, confirm: boolean): Promise<RestoreStaged>;
  backupRestoreStatus(): Promise<RestoreStatus>;
  backupRestoreCancel(): Promise<{ cancelled: boolean }>;
  backupSettingsGet(): Promise<BackupSettings>;
  backupSettingsSet(s: { enabled?: boolean; dir?: string; daily?: number; weekly?: number }): Promise<BackupSettings>;
  projectExportPreview(projectId: string, includeRuns: boolean): Promise<ExportPreview>;
  projectExport(projectId: string, opts: { dest?: string; includeRuns: boolean; acknowledgeRuns: boolean }): Promise<ExportResult>;
  projectImportPreview(path: string): Promise<ImportPreview>;
  projectImport(req: { path: string; repoPath: string; name?: string }): Promise<ImportResult>;
  supportbundlePreview(opts: { eventLimit?: number; includeRun?: string }): Promise<BundlePreview>;
  supportbundleCreate(opts: { dest?: string; eventLimit?: number; includeRun?: string; acknowledgeRun?: boolean }): Promise<BundleCreated>;
}

export function createPkgClient(t: Transport): PkgClient {
  const call = <T>(m: string, p?: unknown) => t.call<T>(m, p);
  return {
    licenseStatus: async () => {
      const v = await call<LicenseView>('license.status');
      return { ...v, channels: arr(v.channels) };
    },
    licenseImport: (req) => call('license.import', req),
    licenseClear: () => call('license.clear'),
    backupList: async () => {
      const r = await call<BackupList>('backup.list');
      return { ...r, backups: arr(r.backups), replaced: arr(r.replaced) };
    },
    backupNow: (label = '') => call('backup.now', { label }),
    backupVerify: (name) => call('backup.verify', { name }),
    backupRestore: async (name, confirm) => {
      const r = await call<RestoreStaged>('backup.restore', { name, confirm });
      return { ...r, warnings: arr(r.warnings) };
    },
    backupRestoreStatus: () => call('backup.restore.status'),
    backupRestoreCancel: () => call('backup.restore.cancel'),
    backupSettingsGet: () => call('backup.settings.get'),
    backupSettingsSet: (s) => call('backup.settings.set', s),
    projectExportPreview: (projectId, includeRuns) => call('project.exportPreview', { projectId, includeRuns }),
    projectExport: (projectId, o) => call('project.export', { projectId, dest: o.dest ?? '', includeRuns: o.includeRuns, acknowledgeRuns: o.acknowledgeRuns }),
    projectImportPreview: (path) => call('project.importPreview', { path }),
    projectImport: (req) => call('project.import', { path: req.path, repoPath: req.repoPath, name: req.name ?? '' }),
    supportbundlePreview: (o) => call('supportbundle.preview', { eventLimit: o.eventLimit ?? 0, includeRun: o.includeRun ?? '' }),
    supportbundleCreate: (o) => call('supportbundle.create', { dest: o.dest ?? '', eventLimit: o.eventLimit ?? 0, includeRun: o.includeRun ?? '', acknowledgeRun: o.acknowledgeRun ?? false }),
  };
}

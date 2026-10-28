import type { Transport } from './transport.ts';
import type {
  BackupList,
  BackupResult,
  BackupSettings,
  BackupVerify,
  BundleExists,
  BundlePreview,
  BundleResult,
  CrashReport,
  NoticeState,
  ExportPreview,
  ExportResult,
  ImportPreview,
  ImportResult,
  LicenseView,
  RestoreStaged,
  RestoreStatus,
} from './pkgTypes.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

const isExists = (r: BundleResult): r is BundleExists => 'exists' in r && r.exists === true;

export interface PkgClient {
  licenseStatus(): Promise<LicenseView>;
  /** Exactly one of token and path. The token is sent once and never kept. */
  licenseImport(req: { token?: string; path?: string }): Promise<LicenseView>;
  licenseClear(): Promise<LicenseView>;
  /**
   * Puts the record of the latest date Drydock has seen right when it is ahead of a correct clock (a human only; audited). The
   * core refuses ("there is nothing to reset") unless that record is ahead of this computer's clock. Returns the refreshed view.
   */
  licenseResetClock(): Promise<LicenseView>;
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
  /** The crash reports on this machine, newest first. */
  crashList(): Promise<{ reports: CrashReport[] }>;
  crashDelete(name: string): Promise<{ deleted: boolean }>;
  /** The notices a person acknowledges once (D1: Claude Code runs as you, under your own terms). */
  noticeStatus(): Promise<{ notices: NoticeState[] }>;
  noticeAcknowledge(id: string, version: number): Promise<NoticeState>;
  /**
   * Writes the bundle. When a file is already at the destination nothing is written and the answer is `{exists: true, path, size}`;
   * send the request again with `overwrite` once the person has said it may be replaced.
   */
  supportbundleCreate(opts: { dest?: string; eventLimit?: number; includeRun?: string; acknowledgeRun?: boolean; overwrite?: boolean }): Promise<BundleResult>;
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
    licenseResetClock: async () => {
      const v = await call<LicenseView>('license.resetClock');
      return { ...v, channels: arr(v.channels) };
    },
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
    crashList: async () => {
      const r = await call<{ reports: CrashReport[] | null }>('crash.list', {});
      return { reports: arr(r.reports) };
    },
    crashDelete: (name) => call('crash.delete', { name }),
    noticeStatus: async () => {
      const r = await call<{ notices: NoticeState[] | null }>('notice.status', {});
      return { notices: arr(r.notices) };
    },
    noticeAcknowledge: (id, version) => call('notice.acknowledge', { id, version }),
    supportbundleCreate: async (o) => {
      const r = await call<BundleResult>('supportbundle.create', {
        dest: o.dest ?? '',
        eventLimit: o.eventLimit ?? 0,
        includeRun: o.includeRun ?? '',
        acknowledgeRun: o.acknowledgeRun ?? false,
        overwrite: o.overwrite ?? false,
      });
      // the screen asks a question on `exists`, so that answer is read as exactly what it must be
      if (isExists(r)) return { exists: true, path: String(r.path ?? ''), size: Number(r.size) || 0 };
      return { ...r, replaced: r.replaced === true };
    },
  };
}

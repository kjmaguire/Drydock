// Types for the packaging methods (internal/core/ext_packaging*.go): license, backups, project export and import, support bundle.

export type LicenseState = 'none' | 'trial' | 'active' | 'grace' | 'expired';

export interface LicenseView {
  /** False in a build with no embedded verification keys: a development build where every feature is allowed. */
  configured: boolean;
  devMode: boolean;
  state: LicenseState | string;
  readOnly: boolean;
  message: string;
  banner?: string;
  daysLeft: number;
  expiresAt?: number; // unix seconds
  graceDays?: number;
  kind?: string;
  plan?: string;
  name?: string;
  subject?: string;
  licenseId?: string;
  channels: string[] | null;
  features?: string[] | null;
  clockSuspect: boolean;
  lastSeen?: number;
}

export interface BackupInfo {
  path: string;
  name: string;
  size: number;
  sha256: string;
  created: string;
  label: string;
  keep: boolean;
}
export interface BackupSettings {
  enabled: boolean;
  dir: string;
  defaultDir: string;
  daily: number;
  weekly: number;
  lastSuccess?: number; // ms
  lastError?: string;
  /** A sync-folder or network-location warning for dir; empty when none applies. A heuristic on the path text. */
  warning?: string;
}
export interface BackupList {
  dir: string;
  backups: BackupInfo[] | null;
  replaced: string[] | null;
  warning?: string;
  settings: BackupSettings;
}
export interface BackupResult {
  backup: BackupInfo;
  pruned: string[] | null;
  warning?: string;
}
export interface BackupVerify {
  name: string;
  size: number;
  created: string;
  ok: boolean;
  error?: string;
  schemaVersion?: number;
  restorableHere?: boolean;
}
export interface RestoreStaged {
  staged: boolean;
  restartRequired: boolean;
  backup: string;
  schemaVersion: number;
  message: string;
  warnings: string[] | null;
}
export interface RestoreStatus {
  pending: boolean;
  backup?: string;
  requested?: number;
}

export interface ExportFile {
  path: string;
  size: number;
  sha256: string;
}
export interface ExportScan {
  dbRedactions: number;
  dbByRule?: Record<string, number>;
  runsFilesScanned: number;
  runsRedactions: number;
  runsByRule?: Record<string, number>;
  runsExcluded?: { path: string; reason: string }[] | null;
}
export interface ExportManifest {
  format: number;
  kind: string;
  appVersion: string;
  schemaVersion: number;
  projectId: string;
  projectName: string;
  created: number;
  includesRuns: boolean;
  files: ExportFile[] | null;
  tables: Record<string, number> | null;
  scan: ExportScan;
  neverIncluded: string[] | null;
}
export interface ExportPreview {
  manifest: ExportManifest;
  totalBytes: number;
  includeRuns: boolean;
  warning?: string;
}
export interface ExportResult {
  path?: string;
  size: number;
  manifest: ExportManifest;
}
export interface ImportStats {
  memoryPending: number;
  skillsPending: number;
  requirementChunksQuarantined: number;
  routingRulesDisabled: number;
  runsNotResumed: number;
  tasksBlocked: number;
  approvalsCancelled: number;
  agentPurposesCleared?: number;
  agentPermissionModesLowered?: number;
  providersNeedingKeys: { id: string; name: string; baseUrl: string }[] | null;
}
export interface ImportPreview {
  manifest: ExportManifest;
  appSchema: number;
  needsMigration: boolean;
  counts: Record<string, number> | null;
  pending: ImportStats;
}
export interface ImportResult {
  projectId: string;
  name: string;
  sourceProjectId: string;
  counts: Record<string, number> | null;
  idsRemapped: number;
  runFilesRestored: number;
  migratedForward: boolean;
  pending: ImportStats;
  sourceSchema: number;
}

export interface BundleFile {
  name: string;
  size: number;
}
export interface BundleFileReport {
  name: string;
  secretsRedacted: number;
  byRule?: Record<string, number>;
  identityTokens?: Record<string, number>;
}
export interface BundleReport {
  files: BundleFileReport[] | null;
  totalSecretsRedacted: number;
  byRule?: Record<string, number>;
  refused?: { name: string; reason: string }[] | null;
  runIncluded?: string;
  notice: string;
}
export interface BundlePreview {
  files: BundleFile[] | null;
  report: BundleReport;
  notice: string;
  includesRun?: string;
  warning?: string;
}
export interface BundleCreated {
  path: string;
  size: number;
  /** A file was already at the path and the person said it could go (the request carried overwrite): it was replaced. */
  replaced?: boolean;
  files: BundleFile[] | null;
  report: BundleReport;
  notice: string;
}
/**
 * The answer of supportbundle.create when a file is already at the destination: nothing was written. The screen asks the
 * person whether to replace it and sends the request again with overwrite only after they say so.
 */
export interface BundleExists {
  exists: true;
  path: string;
  size: number;
}
export type BundleResult = BundleCreated | BundleExists;

/** A crash report kept on this machine (internal/crashreport): what Go printed when a run of Drydock died, secrets redacted. */
export interface CrashReport {
  name: string;
  time: string;
  size: number;
  /** The line that says why: "panic: ..." or "fatal error: ...". */
  summary: string;
}

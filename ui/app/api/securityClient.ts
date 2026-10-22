// The security scan methods (internal/core/ext_security.go). Like the rest of the client: no behavior beyond turning Go's
// nil slices (JSON null) into empty arrays.

import type { Transport } from './transport.ts';
import type { Task } from './types.ts';
import type {
  DismissReason,
  ScanCounts,
  ScanDismissal,
  ScanFinding,
  ScanFindingContext,
  ScanReport,
  ScanSummary,
  SecurityTools,
} from './securityTypes.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const ZERO: ScanCounts = { critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0 };
const counts = (c: ScanCounts | null | undefined): ScanCounts => ({ ...ZERO, ...(c ?? {}) });
const finding = (f: ScanFinding): ScanFinding => ({ ...f, ids: arr(f.ids), unfixed: arr(f.unfixed) });
const report = (r: ScanReport): ScanReport => ({ ...r, engines: arr(r.engines), findings: arr(r.findings).map(finding), counts: counts(r.counts) });
const summary = (s: ScanSummary): ScanSummary => ({ ...s, counts: counts(s.counts) });

export interface SecurityClient {
  /** The scan tools on this computer (Drydock's own, decision P95) and the built-in rules. */
  securityTools(projectId: string): Promise<SecurityTools>;
  /** Starts a scan (status "running"). Refused while one runs or when the project has no repository. */
  securityScanStart(projectId: string): Promise<ScanReport>;
  securityScanCancel(projectId: string): Promise<ScanReport>;
  /** The running scan, else the last finished one, else null. */
  securityScanGet(projectId: string): Promise<ScanReport | null>;
  /** The last scans, newest first. */
  securityScanList(projectId: string, limit?: number): Promise<ScanSummary[]>;
  securityScanReport(id: string): Promise<ScanReport>;
  // Folder scans (decision P61): any folder on this computer, without a project. The scan only reads files.
  /** The scan tools on this computer, as folder scans use them. */
  securityWorkspaceTools(): Promise<SecurityTools>;
  /** Starts a scan of a folder by its full path. Refused for a relative path, a file, a whole drive or Drydock's data folder. */
  securityFolderScanStart(folder: string): Promise<ScanReport>;
  securityFolderScanCancel(folder: string): Promise<ScanReport>;
  /** The folder's running scan, else its last finished one, else null. */
  securityFolderScanGet(folder: string): Promise<ScanReport | null>;
  /** The recent folder scans of every folder, running ones first, then newest first. */
  securityScanFolders(limit?: number): Promise<ScanSummary[]>;
  // One finding (decision P97). A target is { projectId } or { folder }; a finding is its scan and its fingerprint.
  /** Dismisses a finding of the target's last scan: it is marked and left out of the counts of every report. Answers with that report. */
  securityFindingDismiss(target: ScanTarget, fingerprint: string, reason: DismissReason): Promise<ScanReport>;
  /** Counts a dismissed finding again. Answers with the target's last report. */
  securityFindingRestore(target: ScanTarget, fingerprint: string): Promise<ScanReport>;
  /** The target's dismissed findings, newest first. */
  securityFindingsDismissed(target: ScanTarget): Promise<ScanDismissal[]>;
  /** The lines around a finding (around before and after, default 2), read now with every secret masked, and what it means. */
  securityFindingContext(scanId: string, fingerprint: string, around?: number): Promise<ScanFindingContext>;
  /** Makes a task in the scan's project from a finding (refused for a folder scan), optionally given to an agent. */
  securityFindingTask(scanId: string, fingerprint: string, assignee?: string): Promise<{ task: Task; finding: ScanFinding }>;
}

export type ScanTarget = { projectId: string } | { folder: string };

export function createSecurityClient(t: Transport): SecurityClient {
  return {
    securityTools: (projectId) => t.call('security.tools', { projectId }),
    securityScanStart: async (projectId) => report(await t.call<ScanReport>('security.scan.start', { projectId })),
    securityScanCancel: async (projectId) => report(await t.call<ScanReport>('security.scan.cancel', { projectId })),
    securityScanGet: async (projectId) => {
      const r = await t.call<ScanReport | null>('security.scan.get', { projectId });
      return r ? report(r) : null;
    },
    securityScanList: async (projectId, limit = 10) => arr(await t.call<ScanSummary[] | null>('security.scan.list', { projectId, limit })).map(summary),
    securityScanReport: async (id) => report(await t.call<ScanReport>('security.scan.report', { id })),
    securityWorkspaceTools: () => t.call('security.tools', {}),
    securityFolderScanStart: async (folder) => report(await t.call<ScanReport>('security.scan.start', { folder })),
    securityFolderScanCancel: async (folder) => report(await t.call<ScanReport>('security.scan.cancel', { folder })),
    securityFolderScanGet: async (folder) => {
      const r = await t.call<ScanReport | null>('security.scan.get', { folder });
      return r ? report(r) : null;
    },
    securityScanFolders: async (limit = 10) => arr(await t.call<ScanSummary[] | null>('security.scan.folders', { limit })).map(summary),
    securityFindingDismiss: async (target, fingerprint, reason) => report(await t.call<ScanReport>('security.finding.dismiss', { ...target, fingerprint, reason })),
    securityFindingRestore: async (target, fingerprint) => report(await t.call<ScanReport>('security.finding.restore', { ...target, fingerprint })),
    securityFindingsDismissed: async (target) => arr(await t.call<ScanDismissal[] | null>('security.finding.dismissed', target)),
    securityFindingContext: async (scanId, fingerprint, around = 0) => {
      const c = await t.call<ScanFindingContext>('security.finding.context', { scanId, fingerprint, around });
      return { ...c, finding: finding(c.finding), lines: arr(c.lines), advice: { ...c.advice, steps: arr(c.advice?.steps), refs: arr(c.advice?.refs) } };
    },
    securityFindingTask: async (scanId, fingerprint, assignee = '') => {
      const r = await t.call<{ task: Task; finding: ScanFinding }>('security.finding.task', { scanId, fingerprint, assignee });
      return { task: r.task, finding: finding(r.finding) };
    },
  };
}

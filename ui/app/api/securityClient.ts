// The security scan methods (internal/core/ext_security.go). Like the rest of the client: no behavior beyond turning Go's
// nil slices (JSON null) into empty arrays.

import type { Transport } from './transport.ts';
import type { RunningScan, ScanCounts, ScanFinding, ScanReport, ScanSummary, SecurityTools } from './securityTypes.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const ZERO: ScanCounts = { critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0 };
const counts = (c: ScanCounts | null | undefined): ScanCounts => ({ ...ZERO, ...(c ?? {}) });
const finding = (f: ScanFinding): ScanFinding => ({ ...f, ids: arr(f.ids) });
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
  /** Every scan in progress, of a project or a folder, newest first (the sidebar's Running count, the inbox's Running tab). */
  securityScanRunning(): Promise<RunningScan[]>;
}

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
    securityScanRunning: async () =>
      arr(await t.call<RunningScan[] | null>('security.scan.running', {})).map((r) => ({ ...r, engines: arr(r.engines), counts: counts(r.counts) })),
  };
}

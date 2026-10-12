// The security scan methods (internal/core/ext_security.go). Like the rest of the client: no behavior beyond turning Go's
// nil slices (JSON null) into empty arrays.

import type { Transport } from './transport.ts';
import type { ScanCounts, ScanFinding, ScanReport, ScanSummary, SecurityTools } from './securityTypes.ts';

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);
const ZERO: ScanCounts = { critical: 0, high: 0, medium: 0, low: 0, info: 0, total: 0 };
const counts = (c: ScanCounts | null | undefined): ScanCounts => ({ ...ZERO, ...(c ?? {}) });
const finding = (f: ScanFinding): ScanFinding => ({ ...f, ids: arr(f.ids) });
const report = (r: ScanReport): ScanReport => ({ ...r, engines: arr(r.engines), findings: arr(r.findings).map(finding), counts: counts(r.counts) });
const summary = (s: ScanSummary): ScanSummary => ({ ...s, counts: counts(s.counts) });

export interface SecurityClient {
  /** The scan tools on this computer and the project's Opengrep rules folder. */
  securityTools(projectId: string): Promise<SecurityTools>;
  /** Sets the Opengrep rules folder. Only a human may; the server refuses otherwise. */
  securitySettingsSet(projectId: string, rulesDir: string): Promise<SecurityTools>;
  /** Starts a scan (status "running"). Refused while one runs or when the project has no repository. */
  securityScanStart(projectId: string): Promise<ScanReport>;
  securityScanCancel(projectId: string): Promise<ScanReport>;
  /** The running scan, else the last finished one, else null. */
  securityScanGet(projectId: string): Promise<ScanReport | null>;
  /** The last scans, newest first. */
  securityScanList(projectId: string, limit?: number): Promise<ScanSummary[]>;
  securityScanReport(id: string): Promise<ScanReport>;
}

export function createSecurityClient(t: Transport): SecurityClient {
  return {
    securityTools: (projectId) => t.call('security.tools', { projectId }),
    securitySettingsSet: (projectId, rulesDir) => t.call('security.settings.set', { projectId, rulesDir }),
    securityScanStart: async (projectId) => report(await t.call<ScanReport>('security.scan.start', { projectId })),
    securityScanCancel: async (projectId) => report(await t.call<ScanReport>('security.scan.cancel', { projectId })),
    securityScanGet: async (projectId) => {
      const r = await t.call<ScanReport | null>('security.scan.get', { projectId });
      return r ? report(r) : null;
    },
    securityScanList: async (projectId, limit = 10) => arr(await t.call<ScanSummary[] | null>('security.scan.list', { projectId, limit })).map(summary),
    securityScanReport: async (id) => report(await t.call<ScanReport>('security.scan.report', { id })),
  };
}

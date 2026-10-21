// The security scan methods (internal/core/ext_security.go). Like the rest of the client: no behavior beyond turning Go's
// nil slices (JSON null) into empty arrays.

import type { Transport } from './transport.ts';
import type { ScanCounts, ScanFinding, ScanReport, ScanSummary, SecurityTools } from './securityTypes.ts';

/** The external tools a person may choose a program for. */
export type ScanToolName = 'osv-scanner' | 'opengrep';

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
  /** Sets the program of an external tool for the whole workspace; '' goes back to finding it on PATH. Only a human may. */
  securityToolSet(projectId: string, tool: ScanToolName, path: string): Promise<SecurityTools>;
  /** Opens the window's file dialog to choose a tool's program; cancelling changes nothing. Refused where there is no dialog. */
  securityToolChoose(projectId: string, tool: ScanToolName): Promise<SecurityTools>;
  /** Drydock downloads and installs the tool's newest release (or updates the one it installed); the install runs in the background. Only a human may. */
  securityToolInstall(projectId: string, tool: ScanToolName): Promise<SecurityTools>;
  /** Stops a running install. */
  securityToolInstallCancel(projectId: string, tool: ScanToolName): Promise<SecurityTools>;
  /** Deletes the release Drydock installed; the tool is looked for on PATH again. Only a human may. */
  securityToolRemove(projectId: string, tool: ScanToolName): Promise<SecurityTools>;
  /** Read every installed tool's newest release now, instead of at the next twice-daily check (decision P77). */
  securityToolsCheckUpdates(projectId: string): Promise<SecurityTools>;
  /** Starts a scan (status "running"). Refused while one runs or when the project has no repository. */
  securityScanStart(projectId: string): Promise<ScanReport>;
  securityScanCancel(projectId: string): Promise<ScanReport>;
  /** The running scan, else the last finished one, else null. */
  securityScanGet(projectId: string): Promise<ScanReport | null>;
  /** The last scans, newest first. */
  securityScanList(projectId: string, limit?: number): Promise<ScanSummary[]>;
  securityScanReport(id: string): Promise<ScanReport>;
  // Folder scans (decision P61): any folder on this computer, without a project. The scan only reads files.
  /** The scan tools on this computer and the Opengrep rules folder folder scans use. */
  securityWorkspaceTools(): Promise<SecurityTools>;
  /** Sets the rules folder of folder scans (each project keeps its own). Only a human may. */
  securityWorkspaceRulesSet(rulesDir: string): Promise<SecurityTools>;
  /** Starts a scan of a folder by its full path. Refused for a relative path, a file, a whole drive or Drydock's data folder. */
  securityFolderScanStart(folder: string): Promise<ScanReport>;
  securityFolderScanCancel(folder: string): Promise<ScanReport>;
  /** The folder's running scan, else its last finished one, else null. */
  securityFolderScanGet(folder: string): Promise<ScanReport | null>;
  /** The recent folder scans of every folder, running ones first, then newest first. */
  securityScanFolders(limit?: number): Promise<ScanSummary[]>;
}

export function createSecurityClient(t: Transport): SecurityClient {
  return {
    securityTools: (projectId) => t.call('security.tools', { projectId }),
    securitySettingsSet: (projectId, rulesDir) => t.call('security.settings.set', { projectId, rulesDir }),
    securityToolSet: (projectId, tool, path) => t.call('security.tool.set', { projectId, tool, path }),
    securityToolChoose: (projectId, tool) => t.call('security.tool.choose', { projectId, tool }),
    securityToolInstall: (projectId, tool) => t.call('security.tool.install', { projectId, tool }),
    securityToolInstallCancel: (projectId, tool) => t.call('security.tool.install.cancel', { projectId, tool }),
    securityToolRemove: (projectId, tool) => t.call('security.tool.remove', { projectId, tool }),
    securityToolsCheckUpdates: (projectId) => t.call('security.tools.checkUpdates', { projectId }),
    securityScanStart: async (projectId) => report(await t.call<ScanReport>('security.scan.start', { projectId })),
    securityScanCancel: async (projectId) => report(await t.call<ScanReport>('security.scan.cancel', { projectId })),
    securityScanGet: async (projectId) => {
      const r = await t.call<ScanReport | null>('security.scan.get', { projectId });
      return r ? report(r) : null;
    },
    securityScanList: async (projectId, limit = 10) => arr(await t.call<ScanSummary[] | null>('security.scan.list', { projectId, limit })).map(summary),
    securityScanReport: async (id) => report(await t.call<ScanReport>('security.scan.report', { id })),
    securityWorkspaceTools: () => t.call('security.tools', {}),
    securityWorkspaceRulesSet: (rulesDir) => t.call('security.settings.set', { workspace: true, rulesDir }),
    securityFolderScanStart: async (folder) => report(await t.call<ScanReport>('security.scan.start', { folder })),
    securityFolderScanCancel: async (folder) => report(await t.call<ScanReport>('security.scan.cancel', { folder })),
    securityFolderScanGet: async (folder) => {
      const r = await t.call<ScanReport | null>('security.scan.get', { folder });
      return r ? report(r) : null;
    },
    securityScanFolders: async (limit = 10) => arr(await t.call<ScanSummary[] | null>('security.scan.folders', { limit })).map(summary),
  };
}

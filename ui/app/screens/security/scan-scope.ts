// What a security scan page scans: a project's working tree (the project's Security page, and a project chosen on the workspace
// Security page) or a folder on this computer (the workspace Security page, decision P61). The shared ScanPanel reads and acts
// only through a scope, so both pages show the same engines, findings, tools and history.

import type { DrydockEvent } from '../../api/types.ts';
import type { ScanToolName, SecurityClient } from '../../api/securityClient.ts';
import type { ScanReport, ScanSummary, SecurityTools } from '../../api/securityTypes.ts';

export interface ScanScope {
  /** Names what is scanned: the panel re-reads everything when it changes. */
  key: string;
  /** What is scanned: a project's working tree, or a folder (the panel then names the folder a report read). */
  kind: 'project' | 'folder';
  tools(): Promise<SecurityTools>;
  setRules(rulesDir: string): Promise<SecurityTools>;
  chooseTool(tool: ScanToolName): Promise<SecurityTools>;
  setTool(tool: ScanToolName, path: string): Promise<SecurityTools>;
  start(): Promise<ScanReport>;
  cancel(): Promise<ScanReport>;
  /** The running scan, else the last finished one, else null. */
  get(): Promise<ScanReport | null>;
  /** The history the page lists. */
  history(limit: number): Promise<ScanSummary[]>;
  /** An event about this scope's scans. */
  mine(e: DrydockEvent): boolean;
  /** An event that changes the tools panel. */
  toolsChanged(e: DrydockEvent): boolean;
}

/** A tool's program is the workspace's: a choice made on any Security page counts on every one. */
const toolChoice = (e: DrydockEvent) => (e.data as { tool?: string } | undefined)?.tool !== undefined;

/** A project's working tree. */
export function projectScope(client: SecurityClient, projectId: string): ScanScope {
  const mine = (e: DrydockEvent) => e.projectId === projectId;
  return {
    key: `project:${projectId}`,
    kind: 'project',
    tools: () => client.securityTools(projectId),
    setRules: (dir) => client.securitySettingsSet(projectId, dir),
    chooseTool: (tool) => client.securityToolChoose(projectId, tool),
    setTool: (tool, path) => client.securityToolSet(projectId, tool, path),
    start: () => client.securityScanStart(projectId),
    cancel: () => client.securityScanCancel(projectId),
    get: () => client.securityScanGet(projectId),
    history: (limit) => client.securityScanList(projectId, limit),
    mine: (e) => e.type.startsWith('security.') && mine(e),
    toolsChanged: (e) => e.type === 'security.settings' && (mine(e) || toolChoice(e)),
  };
}

/**
 * A folder on this computer, without a project. `folder` is the folder whose last scan is shown ('' before any); `typed()` is
 * the path in the field, which Scan reads; `onStarted` hears the folder the core scans (its cleaned full path). The history is
 * the recent folder scans of every folder; the tools panel shows the workspace's rules folder.
 */
export function folderScope(client: SecurityClient, folder: string, typed: () => string, onStarted: (report: ScanReport) => void): ScanScope {
  const folderEvent = (e: DrydockEvent) => !e.projectId && (e.data as { folder?: string } | undefined)?.folder !== undefined;
  return {
    key: `folder:${folder}`,
    kind: 'folder',
    tools: () => client.securityWorkspaceTools(),
    setRules: (dir) => client.securityWorkspaceRulesSet(dir),
    chooseTool: (tool) => client.securityToolChoose('', tool),
    setTool: (tool, path) => client.securityToolSet('', tool, path),
    start: async () => {
      const r = await client.securityFolderScanStart(typed().trim());
      onStarted(r);
      return r;
    },
    cancel: () => client.securityFolderScanCancel(folder),
    get: () => (folder ? client.securityFolderScanGet(folder) : Promise.resolve(null)),
    history: (limit) => client.securityScanFolders(limit),
    mine: (e) => e.type.startsWith('security.scan.') && folderEvent(e),
    toolsChanged: (e) => e.type === 'security.settings' && ((!e.projectId && (e.data as { workspace?: boolean } | undefined)?.workspace === true) || toolChoice(e)),
  };
}

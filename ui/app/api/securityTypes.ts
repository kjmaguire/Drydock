// The security scan (internal/scan, internal/core/ext_security.go): one scan of a project's working tree, or of any folder
// (decision P61), by three engines at once. These mirror the Go JSON exactly; timestamps are unix milliseconds. A finding never carries a secret's value.

export type EngineID = 'secrets' | 'deps' | 'sast';
export type EngineStatus = 'pending' | 'running' | 'done' | 'skipped' | 'errored' | 'cancelled';
export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info';

/** scan.EngineResult: one engine's part of a scan. `done`/`total` count files while the secrets engine runs; the others cannot count. */
export interface EngineResult {
  id: EngineID;
  name: string;
  tool: string;
  status: EngineStatus;
  /** Why it was skipped or errored. */
  reason?: string;
  startedAt?: number;
  finishedAt?: number;
  done?: number;
  total?: number;
  findings: number;
  /** Folders and files the secrets engine could not read; `reason` says so. */
  unread?: number;
  version?: string;
}

/** scan.Finding: the rule, the place and the title. A secret's value is never here. */
export interface ScanFinding {
  engine: EngineID;
  severity: Severity;
  rule: string;
  title: string;
  file?: string;
  line?: number;
  endLine?: number;
  package?: string;
  version?: string;
  /** Advisory ids (osv.dev) of a dependency finding. */
  ids?: string[];
}

export interface ScanCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
  total: number;
}

export interface ScanReport {
  id: string;
  projectId: string;
  dir: string;
  status: 'running' | 'done' | 'cancelled' | string;
  /** Who started it ("human:<name>"). */
  by: string;
  startedAt: number;
  finishedAt?: number;
  engines: EngineResult[];
  findings: ScanFinding[];
  counts: ScanCounts;
  summary?: string;
  /** True when more findings were counted than kept. */
  truncated?: boolean;
  /** Set when the finished report could not be written to the database: it is shown until the next scan. */
  storeError?: string;
}

export interface ScanSummary {
  id: string;
  projectId: string;
  /** The folder a folder scan read (decision P61); absent for a project's scan. */
  folder?: string;
  status: string;
  by: string;
  startedAt: number;
  finishedAt?: number;
  counts: ScanCounts;
  summary?: string;
}

/** scan.ToolInfo: whether a tool is on this computer, and how to install it when it is not. */
export interface ToolInfo {
  tool: string;
  builtIn?: boolean;
  found: boolean;
  path?: string;
  version?: string;
  install?: string;
  /** How it was found: the file a person chose, the PATH Drydock started with, or an install folder / a PATH changed since. */
  source?: 'chosen' | 'path' | 'folder';
  /** The file a person chose, also when it is no longer there (found is then false). */
  chosen?: string;
}

export interface SecurityTools {
  gitleaks: ToolInfo;
  osv: ToolInfo;
  opengrep: ToolInfo;
  rulesDir: string;
  rulesOk: boolean;
  rulesNote?: string;
  ready: boolean;
  /** This window can open a file dialog (the desktop app); elsewhere the person types the full path. */
  canChoose?: boolean;
}

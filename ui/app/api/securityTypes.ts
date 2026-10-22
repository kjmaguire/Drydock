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
  /** The engine's findings a person dismissed (not in `findings`). */
  dismissed?: number;
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
  /** The version of a vulnerable package that fixes every advisory osv.dev names a fix for; `unfixed` are those it names none for. */
  fixed?: string;
  unfixed?: string[];
  /** The finding's stable id (decision P97): the same in later scans while its line reads the same. Never made from a secret's value. */
  fingerprint?: string;
  /** A person dismissed it: it is not in the counts or the summary. */
  dismissed?: boolean;
  dismissedBy?: string;
  dismissedAt?: number;
  dismissReason?: DismissReason;
}

export type DismissReason = 'not_real' | 'accepted_risk' | 'false_positive';

/** scan.Dismissal: a dismissed finding of a project or folder, with what named it. */
export interface ScanDismissal {
  fingerprint: string;
  reason: DismissReason;
  by: string;
  at: number;
  engine: EngineID;
  rule: string;
  title: string;
  file?: string;
  line?: number;
  /** The scan whose report it was dismissed in. */
  scanId?: string;
}

/** scan.Advice: what a finding means and what to do. */
export interface ScanAdvice {
  meaning: string;
  steps: string[];
  /** Advisory ids, a rule's CWE. */
  refs?: string[];
}

/** One line of the code around a finding. A secret is never in `text`: it is masked ("[REDACTED:<rule>]"). */
export interface ScanContextLine {
  n: number;
  text: string;
  /** The finding's own lines. */
  hit?: boolean;
  /** A secret was masked on the line. */
  masked?: boolean;
  /** Longer than shown. */
  cut?: boolean;
}

/** scan.FindingContext: the code around a finding, read from the scanned folder when asked, and what it means. */
export interface ScanFindingContext {
  scanId: string;
  finding: ScanFinding;
  lines: ScanContextLine[];
  /** The line no longer reads as it did when the scan ran. */
  changed?: boolean;
  /** Why there are no lines (a dependency finding, a file that is gone, binary or too big). */
  note?: string;
  /** security.file.read: the file has more lines than it answered with. */
  more?: boolean;
  advice: ScanAdvice;
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
  /** Findings a person dismissed: left out of `counts` and the summary. */
  dismissed?: number;
  summary?: string;
  /** True when more findings were counted than kept. */
  truncated?: boolean;
  /** Set when the finished report could not be written to the database: it is shown until the next scan. */
  storeError?: string;
}

/** core.RunningScan (security.scan.running): a scan in progress anywhere in the workspace, newest first. */
export interface RunningScan {
  id: string;
  /** Set for a project's scan. */
  projectId?: string;
  /** Set for a folder scan (decision P61). */
  folder?: string;
  /** What it reads as a person names it: the project's name, or the folder's last element. */
  name: string;
  by: string;
  startedAt: number;
  engines: EngineResult[];
  counts: ScanCounts;
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
  /** How it was found: the file a person chose, Drydock's own install (P66), the PATH Drydock started with, or an install folder / a PATH changed since. */
  source?: 'chosen' | 'installed' | 'path' | 'folder';
  /** The file a person chose, also when it is no longer there (found is then false). */
  chosen?: string;
  /** The release Drydock installed (source 'installed'). */
  tag?: string;
  /** A newer release than `tag`, found by the twice-daily update check (decision P77); Update installs it. */
  latest?: string;
  /** The install of the tool in progress, or how the last one ended. */
  job?: ToolJob;
}

/** scan.ToolJob: an install (or update) of a tool by Drydock, decision P66. */
export interface ToolJob {
  state: 'running' | 'done' | 'failed' | 'cancelled';
  /** What is happening, in plain words ("looking up the newest release", "downloading v2.7.0", "done"). */
  phase?: string;
  tag?: string;
  done: number;
  total: number;
  error?: string;
  started: string;
  ended?: string;
}

/** One built-in rule set of the Code engine (decision P77). */
export interface RuleSet {
  id: string;
  name: string;
  source: string;
  licence: string;
  rules: number;
  languages: string[] | null;
}

export interface SecurityTools {
  gitleaks: ToolInfo;
  osv: ToolInfo;
  opengrep: ToolInfo;
  /** The rule sets built into Drydock, run on every scan (decision P77); builtinRules says they could be written for Opengrep. */
  ruleSets?: RuleSet[] | null;
  builtinRuleCount?: number;
  builtinRules?: boolean;
  /** A folder of the person's own rules, run with the built-in ones ("" for none). */
  rulesDir: string;
  rulesOk: boolean;
  rulesNote?: string;
  ready: boolean;
  /** This window can open a file dialog (the desktop app); elsewhere the person types the full path. */
  canChoose?: boolean;
  /** Drydock can download and install the external tools here (Windows, decision P66). */
  canInstall?: boolean;
}

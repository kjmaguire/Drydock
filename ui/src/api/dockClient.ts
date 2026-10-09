// Typed client for the bottom dock: the diff viewer, the human's terminals and the task logs (internal/core/ext_diffview.go,
// ext_terminal.go, ext_logs.go).

import type { Transport } from './transport';

export interface DiffFile {
  path: string;
  oldPath?: string;
  status: 'added' | 'deleted' | 'modified' | 'renamed';
  additions: number;
  deletions: number;
  binary?: boolean;
}

export interface DiffFiles {
  /** recorded: the diff of the last finished attempt; live: the worktree now; none: nothing yet. */
  source: 'recorded' | 'live' | 'none';
  base?: string;
  head?: string;
  files: DiffFile[];
  liveAvailable: boolean;
}

export interface DiffFileContent {
  path: string;
  oldPath?: string;
  status: DiffFile['status'];
  original: string;
  modified: string;
  binary?: boolean;
  /** Over the display limit: left out until the human asks for it. */
  tooLarge?: boolean;
  /** Only the changed regions are known (git no longer has the commits). */
  partial?: boolean;
  size?: number;
}

export interface TerminalInfo {
  id: string;
  title: string;
  cwd: string;
  shell: string;
  cols: number;
  rows: number;
  startedAt: string;
  exited: boolean;
  exitCode: number;
}

/** Output from an offset. data is the raw bytes (base64 on the wire). */
export interface TerminalChunk {
  data: Uint8Array;
  next: number;
  dropped: number;
  exited: boolean;
  exitCode: number;
}

export interface LogSource {
  id: string;
  kind: 'check' | 'agent';
  title: string;
  size: number;
  passed?: boolean;
  attempt?: number;
  created: number;
  running?: boolean;
}

export interface LogText {
  text: string;
  start: number;
  next: number;
  size: number;
}

export interface DockClient {
  taskDiffFiles(id: string, live?: boolean): Promise<DiffFiles>;
  taskDiffFile(id: string, path: string, live?: boolean, force?: boolean): Promise<DiffFileContent>;
  terminalOpen(where: { projectId?: string; taskId?: string }, cols: number, rows: number): Promise<TerminalInfo>;
  terminalRead(id: string, from: number, waitMs: number): Promise<TerminalChunk>;
  terminalWrite(id: string, data: string): Promise<void>;
  terminalResize(id: string, cols: number, rows: number): Promise<void>;
  terminalClose(id: string): Promise<void>;
  terminalList(): Promise<TerminalInfo[]>;
  logsList(taskId: string): Promise<LogSource[]>;
  logsRead(taskId: string, id: string, opts?: { from?: number; limit?: number; tail?: boolean }): Promise<LogText>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

export function decodeBase64(s: string | null | undefined): Uint8Array {
  if (!s) return new Uint8Array(0);
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function createDockClient(t: Transport): DockClient {
  return {
    taskDiffFiles: async (id, live = false) => {
      const r = await t.call<DiffFiles>('task.diffFiles', { id, live });
      return { ...r, files: arr(r.files) };
    },
    taskDiffFile: (id, path, live = false, force = false) => t.call('task.diffFile', { id, path, live, force }),
    terminalOpen: (where, cols, rows) => t.call('terminal.open', { ...where, cols, rows }),
    terminalRead: async (id, from, waitMs) => {
      const r = await t.call<Omit<TerminalChunk, 'data' | 'dropped'> & { data: string | null; dropped?: number }>('terminal.read', { id, from, waitMs });
      return { ...r, data: decodeBase64(r.data), dropped: r.dropped ?? 0 };
    },
    terminalWrite: async (id, data) => {
      await t.call('terminal.write', { id, data });
    },
    terminalResize: async (id, cols, rows) => {
      await t.call('terminal.resize', { id, cols, rows });
    },
    terminalClose: async (id) => {
      await t.call('terminal.close', { id });
    },
    terminalList: async () => arr(await t.call<TerminalInfo[] | null>('terminal.list')),
    logsList: async (taskId) => arr(await t.call<LogSource[] | null>('logs.list', { taskId })),
    logsRead: (taskId, id, opts = {}) => t.call('logs.read', { taskId, id, from: opts.from ?? 0, limit: opts.limit ?? 0, tail: opts.tail ?? false }),
  };
}

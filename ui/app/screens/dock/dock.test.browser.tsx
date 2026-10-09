import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import type { DiffFileContent, DiffFiles, LogSource, TerminalChunk, TerminalInfo } from '../../api/dockClient.ts';
import type { UsageRow } from '../../api/types.ts';
import { byText, cleanup, mount, testServices, waitFor } from '../../test/render.tsx';
import { ActivityPanel } from './activity-panel.tsx';
import { DiffViewer } from './diff-viewer.tsx';
import { LogsPanel } from './logs-panel.tsx';
import { TerminalPanel } from './terminal-panel.tsx';
import { setDiffEditorLoader, setTerminalLoader, type DiffEditorFactory, type TerminalFactory } from './widgets.ts';

// Monaco and xterm need the real bundle (the end-to-end suite drives them); here they are labelled TEST DOUBLES that show
// what they were given and record what was asked of them.

const editors = { created: 0, disposed: 0 };
/** TEST DOUBLE for Monaco's diff editor. */
const testDiffEditor: DiffEditorFactory = (host, init) => {
  editors.created++;
  const root = document.createElement('div');
  root.setAttribute('data-testid', 'monaco-stub');
  root.setAttribute('data-side', String(init.sideBySide));
  const orig = document.createElement('pre');
  orig.setAttribute('data-testid', 'orig');
  const mod = document.createElement('pre');
  mod.setAttribute('data-testid', 'mod');
  root.append(orig, mod);
  host.append(root);
  const set = (_p: string, o: string, m: string) => {
    orig.textContent = o;
    mod.textContent = m;
  };
  set(init.path, init.original, init.modified);
  return {
    setContent: set,
    setSideBySide: (on) => root.setAttribute('data-side', String(on)),
    dispose: () => {
      editors.disposed++;
      root.remove();
    },
  };
};

interface TestTerminal {
  host: HTMLElement;
  output: string;
  type(s: string): void;
  disposed: boolean;
  fits: number;
}
const terminals: TestTerminal[] = [];
/** TEST DOUBLE for xterm.js: an 80x24 grid that keeps what was written as text. */
const testTerminal: TerminalFactory = (host) => {
  const listeners: ((s: string) => void)[] = [];
  const dec = new TextDecoder();
  const out = document.createElement('pre');
  out.setAttribute('data-testid', 'xterm-stub');
  host.append(out);
  const rec: TestTerminal = {
    host,
    output: '',
    type: (s) => listeners.forEach((l) => l(s)),
    disposed: false,
    fits: 0,
  };
  terminals.push(rec);
  return {
    cols: 80,
    rows: 24,
    write: (d) => {
      rec.output += typeof d === 'string' ? d : dec.decode(d);
      out.textContent = rec.output;
    },
    onData: (fn) => void listeners.push(fn),
    fit: () => void rec.fits++,
    focus: () => {},
    dispose: () => {
      rec.disposed = true;
      out.remove();
    },
  };
};

beforeEach(() => {
  editors.created = 0;
  editors.disposed = 0;
  terminals.length = 0;
  setDiffEditorLoader(async () => testDiffEditor);
  setTerminalLoader(async () => testTerminal);
  try {
    localStorage.removeItem('drydock.diff.sideBySide');
  } catch {
    /* ignore */
  }
});
afterEach(() => {
  cleanup();
  setDiffEditorLoader(undefined);
  setTerminalLoader(undefined);
});

const text = (el: Element | null | undefined) => el?.textContent ?? '';
const has = (root: Element, s: string | RegExp) => {
  const tc = root.textContent ?? '';
  if (typeof s === 'string' ? !tc.includes(s) : !s.test(tc)) throw new Error(`no ${String(s)} in ${tc.slice(0, 300)}`);
};

const files = (over: Partial<DiffFiles> = {}): DiffFiles => ({
  source: 'recorded',
  base: 'b'.repeat(40),
  head: 'c'.repeat(40),
  liveAvailable: true,
  files: [
    { path: 'calc.go', status: 'modified', additions: 2, deletions: 1 },
    { path: 'img.png', status: 'added', additions: 0, deletions: 0, binary: true },
    { path: 'big.txt', status: 'modified', additions: 1, deletions: 1 },
  ],
  ...over,
});

describe('the diff viewer', () => {
  it('lists the files, shows both versions, switches layout and live mode, and guards binary and large files', async () => {
    const diffCalls: [string, boolean | undefined][] = [];
    const fileCalls: [string, string, boolean | undefined, boolean | undefined][] = [];
    const s = testServices({
      taskDiffFiles: async (id, live) => {
        diffCalls.push([id, live]);
        return files(live ? { source: 'live', head: undefined } : {});
      },
      taskDiffFile: async (id, path, live, force): Promise<DiffFileContent> => {
        fileCalls.push([id, path, live, force]);
        if (path === 'img.png') return { path, status: 'added', original: '', modified: '', binary: true };
        if (path === 'big.txt' && !force) return { path, status: 'modified', original: '', modified: '', tooLarge: true, size: 3 << 20 };
        return { path, status: 'modified', original: `old ${path}`, modified: `new ${path}` };
      },
    });
    const r = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => expect(text(r.$('[data-testid="mod"]'))).toContain('new calc.go'), r);
    expect(text(r.$('[data-testid="orig"]'))).toContain('old calc.go');
    const list = r.$('ul[aria-label="Changed files"]')!;
    expect(list.querySelectorAll('li').length).toBe(3);
    // status letters, counts, the head and the totals
    const first = list.querySelector('li button')!;
    expect(first.getAttribute('aria-current')).toBe('true');
    expect(text(first)).toMatch(/M.*calc\.go.*\+2.*-1/);
    expect(text(list.querySelectorAll('li')[1])).toContain('binary');
    has(r.container, 'Recorded with the last attempt');
    has(r.container, 'cccccccccc');
    has(r.container, '3 files+3-2');
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Previous file').disabled).toBe(true);
    expect(byText(r.container, 'button', 'Side by side').getAttribute('aria-pressed')).toBe('true');

    await r.act(() => byText(r.container, 'button', 'Inline').click());
    expect(r.$('[data-testid="monaco-stub"]')!.getAttribute('data-side')).toBe('false');
    expect(byText(r.container, 'button', 'Inline').getAttribute('aria-pressed')).toBe('true');
    expect(localStorage.getItem('drydock.diff.sideBySide')).toBe('inline');

    await r.act(() => byText(r.container, 'button', 'Next file').click());
    await waitFor(() => has(r.container, 'Binary file: not shown.'), r);
    expect(r.$('[data-testid="monaco-stub"]')).toBeNull();
    await r.act(() => byText(r.container, 'button', 'Next file').click());
    await waitFor(() => has(r.container, /3\.0 MB, too large/), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Next file').disabled).toBe(true);
    await r.act(() => byText(r.container, 'button', 'Show it anyway').click());
    await waitFor(() => expect(text(r.$('[data-testid="mod"]'))).toContain('new big.txt'), r);
    expect(fileCalls[fileCalls.length - 1]).toEqual(['t1', 'big.txt', false, true]);
    // the editor keeps the inline layout chosen earlier
    expect(r.$('[data-testid="monaco-stub"]')!.getAttribute('data-side')).toBe('false');

    await r.act(() => byText(r.container, 'button', 'Live').click());
    await waitFor(() => expect(diffCalls[diffCalls.length - 1]).toEqual(['t1', true]), r);
    await waitFor(() => has(r.container, 'Live worktree'), r);
    // the selected file stays selected, now read from the worktree
    await waitFor(() => expect(fileCalls[fileCalls.length - 1]).toEqual(['t1', 'big.txt', true, false]), r);
  });

  it('picks a file from the list and disposes the editor of the previous one', async () => {
    const s = testServices({
      taskDiffFiles: async () => files({ files: [files().files[0]!, { path: 'internal/x/y.go', oldPath: 'old/y.go', status: 'renamed', additions: 3, deletions: 0 }] }),
      taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: `old ${path}`, modified: `new ${path}` }),
    });
    const r = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => expect(text(r.$('[data-testid="mod"]'))).toContain('new calc.go'), r);
    const second = byText(r.container, 'button', /y\.go/);
    expect(second.getAttribute('title')).toBe('old/y.go → internal/x/y.go');
    has(second, 'R');
    await r.act(() => second.click());
    await waitFor(() => expect(text(r.$('[data-testid="mod"]'))).toContain('new internal/x/y.go'), r);
    has(r.$('[aria-label="Changes in internal/x/y.go"]')!, 'old/y.go → internal/x/y.go');
    expect(editors.created).toBe(2);
    expect(editors.disposed).toBe(1);
    expect(r.$$('[data-testid="monaco-diff"]').length).toBe(1);
  });

  it('takes room of its own on a page and fits the dock inside it', async () => {
    const s = testServices({
      taskDiffFiles: async () => files({ files: [files().files[0]!] }),
      taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: 'a', modified: 'b' }),
    });
    const r = mount(
      <div>
        <div data-testid="page">
          <DiffViewer taskId="t1" />
        </div>
        <div id="dock-panel" style={{ height: '200px' }}>
          <DiffViewer taskId="t1" />
        </div>
      </div>,
      s,
    );
    await waitFor(() => expect(r.$$('[data-testid="monaco-stub"]').length).toBe(2), r);
    const [onPage, inDock] = Array.from(r.$$('[data-testid="monaco-diff"]')) as HTMLElement[];
    expect(getComputedStyle(onPage!.parentElement!).minHeight).toBe('300px');
    expect(getComputedStyle(inDock!.parentElement!).minHeight).toBe('0px');
    expect(inDock!.closest('[data-testid="diff-viewer"]')!.getBoundingClientRect().height).toBe(200);
  });

  it('says so when nothing is recorded, and Live is off without a worktree', async () => {
    const r = mount(<DiffViewer taskId="t1" />, testServices({ taskDiffFiles: async () => files({ source: 'none', files: [], liveAvailable: false }) }));
    await waitFor(() => has(r.container, /No diff has been recorded yet/), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Live').disabled).toBe(true);
  });

  it('marks a partial diff whose commits are gone', async () => {
    const r = mount(
      <DiffViewer taskId="t1" />,
      testServices({
        taskDiffFiles: async () => files({ files: [{ path: 'a.go', status: 'modified', additions: 1, deletions: 1 }] }),
        taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: 'x\n⋯\ny', modified: 'x\n⋯\nz', partial: true }),
      }),
    );
    await waitFor(() => expect(r.$('[data-testid="diff-partial"]')).toBeTruthy(), r);
  });

  it('shows the error when the diff cannot be read', async () => {
    const r = mount(
      <DiffViewer taskId="t1" />,
      testServices({
        taskDiffFiles: async () => {
          throw new Error('no such task');
        },
      }),
    );
    await waitFor(() => has(r.$('[role="alert"]')!, 'Could not load the diff: no such task'), r);
  });

  it('re-reads on an event of the task', async () => {
    let n = 0;
    const s = testServices({
      taskDiffFiles: async () => {
        n++;
        return files({ files: n === 1 ? [] : files().files });
      },
    });
    const r = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => has(r.container, 'No changes.'), r);
    s.emit({ type: 'task.updated', taskId: 't2' });
    s.emit({ type: 'task.updated', taskId: 't1' });
    await waitFor(() => expect(r.$$('ul[aria-label="Changed files"] li').length).toBe(3), r);
    expect(n).toBe(2);
  });
});

describe('the logs panel', () => {
  it('lists check logs and agent streams, opens the newest at its tail, and reads earlier output on request', async () => {
    const reads: [string, { from?: number; limit?: number; tail?: boolean } | undefined][] = [];
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [
          { id: 'ev:1', kind: 'check', title: 'test', size: 10, passed: false, created: 1 },
          { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 140, created: 2 },
        ],
        logsRead: async (_t, id, o) => {
          reads.push([id, o]);
          if (o?.tail) return { text: `tail of ${id}\nsecond line\n`, start: 100, next: 140, size: 140 };
          if ((o?.from ?? 0) < 100) return { text: 'the beginning\n', start: 0, next: 100, size: 140 };
          return { text: '', start: 140, next: 140, size: 140 };
        },
      }),
    );
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1'), r);
    expect(reads[0]).toEqual(['nx:r|work|2|1', { tail: true, limit: 64 << 10 }]);
    has(r.container, 'failed');
    const logList = r.$('ul[aria-label="Logs"]')!;
    expect(logList.classList.contains('two-line')).toBe(true);
    expect(logList.classList.length).toBeGreaterThan(1);
    expect(r.$('[data-testid="log-text"]')!.getAttribute('aria-label')).toBe('work (step 2) log');
    expect(byText(r.container, 'button', /Agent:\s*work/).getAttribute('aria-current')).toBe('true');
    await r.act(() => byText(r.container, 'button', 'Show earlier output').click());
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toMatch(/^the beginning\ntail of/), r);
    expect(reads[reads.length - 1]).toEqual(['nx:r|work|2|1', { from: 0, limit: 100 }]);
    has(r.container, 'From the beginning');
    await r.act(() => byText(r.container, 'button', /Check:\s*test/).click());
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of ev:1'), r);
  });

  it('tails a running source, joining a line split across reads', async () => {
    let step = 0;
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [{ id: 'nx:1', kind: 'agent', title: 'work', size: 0, created: 1, running: true } as LogSource],
        logsRead: async (_t, _id, o) => {
          if (o?.tail) return { text: 'first\nhal', start: 0, next: 9, size: 9 };
          step++;
          if (step === 1) return { text: 'f done\n', start: 9, next: 16, size: 16 };
          return { text: '', start: 16, next: 16, size: 16 };
        },
      }),
    );
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toBe('first\nhal'), r);
    has(r.container, 'running');
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toBe('first\nhalf done'), r, 3000);
  });

  it('follows the end until the human scrolls up', async () => {
    const long = Array.from({ length: 400 }, (_, i) => `line ${i}`).join('\n') + '\n';
    const r = mount(
      <div style={{ height: '200px', display: 'flex', flexDirection: 'column' }}>
        <LogsPanel taskId="t1" />
      </div>,
      testServices({
        logsList: async () => [{ id: 'a', kind: 'check', title: 'test', size: 1, created: 1 } as LogSource],
        logsRead: async (_t, _id, o) => (o?.tail ? { text: long, start: 0, next: long.length, size: long.length } : { text: '', start: long.length, next: long.length, size: long.length }),
      }),
    );
    const box = await waitFor(() => {
      const b = r.$('[data-testid="log-text"]') as HTMLPreElement;
      if (!b.textContent?.includes('line 399')) throw new Error('not yet');
      return b;
    }, r);
    await waitFor(() => expect(box.scrollTop + box.clientHeight).toBeGreaterThanOrEqual(box.scrollHeight - 8), r);
    const follow = r.$('input[type="checkbox"]') as HTMLInputElement;
    expect(follow.checked).toBe(true);
    box.scrollTop = 0;
    box.dispatchEvent(new Event('scroll'));
    await waitFor(() => expect((r.$('input[type="checkbox"]') as HTMLInputElement).checked).toBe(false), r);
  });

  it('shows a read error', async () => {
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [{ id: 'a', kind: 'check', title: 'test', size: 1, created: 1 } as LogSource],
        logsRead: async () => {
          throw new Error('log gone');
        },
      }),
    );
    await waitFor(() => has(r.$('[role="alert"]')!, 'Could not read the log: log gone'), r);
  });

  it('says when there is nothing yet', async () => {
    const r = mount(<LogsPanel taskId="t1" />, testServices({ logsList: async () => [] }));
    await waitFor(() => has(r.container, /No logs yet/), r);
  });
});

describe('the terminal panel', () => {
  const info = (id: string, title: string): TerminalInfo => ({ id, title, cwd: '/x', shell: 'sh', cols: 100, rows: 24, startedAt: '', exited: false, exitCode: 0 });

  it('opens a terminal in the task or the repository and closes it', async () => {
    let open: TerminalInfo[] = [];
    const opens: [{ taskId?: string; projectId?: string }, number, number][] = [];
    const closes: string[] = [];
    const r = mount(
      <TerminalPanel projectId="p1" taskId="t1" />,
      testServices({
        terminalList: async () => open,
        terminalOpen: async (where, cols, rows) => {
          opens.push([where, cols, rows]);
          const t = info(`term_${open.length + 1}`, where.taskId ? 'task #3' : 'gocalc');
          open = [...open, t];
          return t;
        },
        terminalClose: async (id) => {
          closes.push(id);
          open = open.filter((t) => t.id !== id);
        },
        terminalRead: () => new Promise<TerminalChunk>(() => {}),
        terminalResize: async () => {},
      }),
    );
    await waitFor(() => has(r.container, /No terminal is open/), r);
    await r.act(() => byText(r.container, 'button', 'New terminal in the task').click());
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('task #3'), r);
    await waitFor(() => expect(r.$('[data-testid="xterm"] [data-testid="xterm-stub"]')).toBeTruthy(), r);
    expect(opens[0]).toEqual([{ taskId: 't1' }, 100, 24]);
    await r.act(() => byText(r.container, 'button', 'New terminal in the repository').click());
    await waitFor(() => expect(byText(r.container, '[role="tab"]', 'gocalc').getAttribute('aria-selected')).toBe('true'), r);
    expect(opens[1]).toEqual([{ projectId: 'p1' }, 100, 24]);
    // switching tabs disposed the first terminal's widget
    await waitFor(() => expect(terminals.length).toBe(2), r);
    expect(terminals[0]!.disposed).toBe(true);
    await r.act(() => byText(r.container, 'button', 'Close gocalc').click());
    await waitFor(() => expect(Array.from(r.$$('[role="tab"]')).map((e) => e.textContent)).toEqual(['task #3']), r);
    expect(closes).toEqual(['term_2']);
    expect(terminals[1]!.disposed).toBe(true);
  });

  it('streams output into the terminal, sends keystrokes and the size, and marks an exited shell', async () => {
    const enc = new TextEncoder();
    const reads: number[] = [];
    const writes: [string, string][] = [];
    const resizes: [string, number, number][] = [];
    let release: (() => void) | undefined;
    const r = mount(
      <TerminalPanel projectId={undefined} taskId="t1" />,
      testServices({
        terminalList: async () => [info('term_1', 'task #3')],
        terminalRead: async (_id, from) => {
          reads.push(from);
          if (from === 0) return { data: enc.encode('$ héllo\r\n'), next: 10, dropped: 3, exited: false, exitCode: 0 };
          await new Promise<void>((res) => (release = res));
          return { data: new Uint8Array(0), next: 10, dropped: 0, exited: true, exitCode: 2 };
        },
        terminalWrite: async (id, d) => void writes.push([id, d]),
        terminalResize: async (id, c, rw) => void resizes.push([id, c, rw]),
      }),
    );
    await waitFor(() => expect(terminals.length).toBe(1), r);
    const term = terminals[0]!;
    await waitFor(() => expect(term.output).toContain('$ héllo'), r);
    expect(term.output).toContain('[3 bytes of earlier output were not kept]');
    await waitFor(() => expect(reads).toEqual([0, 10]), r);
    expect(resizes).toEqual([['term_1', 80, 24]]);
    expect(term.fits).toBeGreaterThan(0);
    // the repository button needs a project
    expect(() => byText(r.container, 'button', 'New terminal in the repository')).toThrow();

    term.type('ls\r');
    await waitFor(() => expect(writes).toEqual([['term_1', 'ls\r']]), r);

    release!();
    await waitFor(() => expect(term.output).toContain('[the shell exited with code 2]'), r);
    await waitFor(() => expect(r.$('[role="tab"]')!.textContent).toBe('task #3 (exited)'), r);
  });

  it('says when the terminal is gone', async () => {
    const r = mount(
      <TerminalPanel projectId={undefined} taskId={undefined} />,
      testServices({
        terminalList: async () => [info('term_9', '')],
        terminalRead: async () => {
          throw new Error('unknown terminal');
        },
        terminalResize: async () => {},
      }),
    );
    await waitFor(() => expect(terminals[0]?.output ?? '').toContain('[the terminal is gone: unknown terminal]'), r);
    expect(r.$('[role="tab"]')!.textContent).toBe('Terminal 1');
    expect(byText(r.container, 'button', 'Close terminal 1')).toBeTruthy();
  });

  it('does not offer a repository terminal for a project without a repository', async () => {
    const s = testServices({ terminalList: async () => [], projectList: async () => [{ id: 'p1', name: 'later', repoPath: '', hasRepo: false } as never] });
    s.workspace.start();
    const r = mount(<TerminalPanel projectId="p1" taskId={undefined} />, s);
    const b = await waitFor(() => byText<HTMLButtonElement>(r.container, 'button', 'No repository for a terminal yet'), r);
    expect(b.disabled).toBe(true);
    expect(b.title).toContain('attach one in Project settings');
    expect(() => byText(r.container, 'button', 'New terminal in the repository')).toThrow();
  });

  it('shows an error from opening', async () => {
    const r = mount(
      <TerminalPanel projectId="p1" taskId={undefined} />,
      testServices({
        terminalList: async () => [],
        terminalOpen: async () => {
          throw new Error('no shell');
        },
      }),
    );
    await waitFor(() => byText(r.container, 'button', 'New terminal in the repository'), r);
    await r.act(() => byText(r.container, 'button', 'New terminal in the repository').click());
    await waitFor(() => has(r.$('[role="alert"]')!, 'Terminal: no shell'), r);
  });
});

describe('the activity panel', () => {
  const row = (over: Partial<UsageRow> = {}): UsageRow => ({
    ts: 1,
    taskId: 't1',
    agentId: 'a1',
    provider: 'anthropic',
    model: 'm',
    tier: 'standard',
    inputTokens: 10,
    outputTokens: 5,
    cachedTokens: 0,
    cacheWriteTokens: 0,
    reasoningTokens: 0,
    costUsd: 0.02,
    source: 'reported',
    cacheHit: false,
    ...over,
  });

  it('shows the usage of the task in context', async () => {
    const calls: unknown[] = [];
    const r = mount(
      <ActivityPanel taskId="t1" />,
      testServices({
        usageList: async (f) => {
          calls.push(f);
          return [row(), row({ source: 'estimated' })];
        },
      }),
    );
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(2), r);
    has(r.container, 'Usage of this task, newest first.');
    has(r.container, '2 rows');
    expect(calls[0]).toEqual({ taskId: 't1', limit: 200 });
  });

  it('shows the most recent usage without a task, and re-reads on usage events', async () => {
    let n = 0;
    const s = testServices({
      usageList: async () => {
        n++;
        return n === 1 ? [] : [row()];
      },
    });
    const r = mount(<ActivityPanel taskId={undefined} />, s);
    await waitFor(() => has(r.container, /most recent usage/), r);
    await waitFor(() => has(r.container, 'No usage has been recorded.'), r);
    s.emit({ type: 'usage.recorded' });
    await waitFor(() => expect(r.$$('[data-testid="usage-row"]').length).toBe(1), r);
  });
});

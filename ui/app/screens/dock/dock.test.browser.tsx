import { expect } from 'remix/assert';
import { afterEach, beforeEach, describe, it } from 'remix/test';
import type { DiffFileContent, DiffFiles, LogSource, TerminalChunk, TerminalInfo } from '../../api/dockClient.ts';
import type { Agent, Project, UsageRow } from '../../api/types.ts';
import { lic } from '../../test/fixtures.ts';
import { byText, cleanup, drain, mount, testServices, waitFor } from '../../test/render.tsx';
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

  it('shows the worktree while an agent works and the recorded diff once it stops, unless the person chose (X21)', async () => {
    let active = true;
    const calls: (boolean | undefined)[] = [];
    const s = testServices({
      taskDiffFiles: async (_id, live) => {
        calls.push(live);
        return files(live ? { source: 'live', head: undefined, runActive: active } : { runActive: active });
      },
      taskDiffFile: async (_id, path, live) => ({ path, status: 'modified', original: 'a', modified: live ? 'live' : 'recorded' }),
    });
    const r = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => has(r.container, 'Live worktree'), r);
    expect(byText(r.container, 'button', 'Live').getAttribute('aria-pressed')).toBe('true');
    // the run stops: back to the recorded diff
    active = false;
    s.emit({ type: 'task.updated', taskId: 't1' });
    await waitFor(() => has(r.container, 'Recorded with the last attempt'), r);
    // the person picks Live: it stays, whatever the run does
    await r.act(() => byText(r.container, 'button', 'Live').click());
    await waitFor(() => has(r.container, 'Live worktree'), r);
    s.emit({ type: 'task.updated', taskId: 't1' });
    await waitFor(() => expect(calls.length).toBeGreaterThanOrEqual(5), r);
    await drain();
    has(r.container, 'Live worktree');
    expect(s.ui.dockView('t1')).toEqual({ live: true });
  });

  it('keeps the file and the Live or Recorded pick of a task when it is mounted again (X22)', async () => {
    const s = testServices({
      taskDiffFiles: async (_id, live) => files(live ? { source: 'live', head: undefined, runActive: true } : { runActive: true }),
      taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: `old ${path}`, modified: `new ${path}` }),
    });
    const r = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => has(r.container, 'Live worktree'), r);
    await r.act(() => byText(r.container, 'button', 'Recorded').click());
    await waitFor(() => has(r.container, 'Recorded with the last attempt'), r);
    await r.act(() => byText(r.container, 'button', 'Next file').click());
    await waitFor(() => expect(text(r.$('[data-testid="mod"]'))).toContain('new img.png'), r);
    cleanup();
    const again = mount(<DiffViewer taskId="t1" />, s);
    await waitFor(() => expect(text(again.$('[data-testid="mod"]'))).toContain('new img.png'), again);
    has(again.container, 'Recorded with the last attempt');
    expect(byText(again.container, 'button', /img\.png/).getAttribute('aria-current')).toBe('true');
  });

  it('offers the worktree from the empty state when nothing is recorded yet (X21)', async () => {
    const calls: (boolean | undefined)[] = [];
    const r = mount(
      <DiffViewer taskId="t1" />,
      testServices({
        taskDiffFiles: async (_id, live) => {
          calls.push(live);
          return live ? files({ source: 'live', head: undefined }) : files({ source: 'none', files: [] });
        },
        taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: 'a', modified: 'b' }),
      }),
    );
    await waitFor(() => has(r.container, 'No diff has been recorded yet.'), r);
    await r.act(() => byText(r.container, 'button', 'Show the worktree now').click());
    await waitFor(() => has(r.container, 'Live worktree'), r);
    expect(calls[calls.length - 1]).toBe(true);
  });

  it('says so when nothing is recorded, and Live is off without a worktree', async () => {
    const r = mount(<DiffViewer taskId="t1" />, testServices({ taskDiffFiles: async () => files({ source: 'none', files: [], liveAvailable: false }) }));
    await waitFor(() => has(r.container, /No diff has been recorded yet/), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Live').disabled).toBe(true);
    expect(r.container.textContent).not.toContain('Show the worktree now');
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
  it('lists check logs and agent streams, opens the failed check at its tail, and reads earlier output on request', async () => {
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
    // the failed check is what matters when nothing runs (X23), not the newest stream
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of ev:1'), r);
    expect(reads[0]).toEqual(['ev:1', { tail: true, limit: 64 << 10 }]);
    expect(byText(r.container, 'button', /Check:\s*test/).getAttribute('aria-current')).toBe('true');
    has(r.container, 'failed');
    const logList = r.$('ul[aria-label="Logs"]')!;
    expect(logList.classList.contains('two-line')).toBe(true);
    expect(logList.classList.length).toBeGreaterThan(1);
    await r.act(() => byText(r.container, 'button', /Agent:\s*work/).click());
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1'), r);
    expect(r.$('[data-testid="log-text"]')!.getAttribute('aria-label')).toBe('work (step 2) log');
    // a named, focusable log that does not read its whole buffer out on every poll (F7)
    expect(r.$('[data-testid="log-text"]')!.getAttribute('aria-live')).toBe('off');
    expect(byText(r.container, 'button', /Agent:\s*work/).getAttribute('aria-current')).toBe('true');
    await r.act(() => byText(r.container, 'button', 'Show earlier output').click());
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toMatch(/^the beginning\ntail of/), r);
    expect(reads[reads.length - 1]).toEqual(['nx:r|work|2|1', { from: 0, limit: 100 }]);
    has(r.container, 'From the beginning');
  });

  it('opens the running stream first, and keeps the log the person picked per task (X22, X23)', async () => {
    const s = testServices({
      logsList: async () => [
        { id: 'ev:1', kind: 'check', title: 'test', size: 10, passed: false, created: 1 },
        { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 10, created: 2, running: true },
        { id: 'ev:2', kind: 'check', title: 'lint', size: 10, passed: true, created: 3 },
      ],
      logsRead: async (_t, id) => ({ text: `tail of ${id}\n`, start: 0, next: 10, size: 10 }),
    });
    const r = mount(<LogsPanel taskId="t1" />, s);
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1'), r);
    await r.act(() => byText(r.container, 'button', /Check:\s*lint/).click());
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of ev:2'), r);
    cleanup();
    // the dock's panel unmounts when it is closed or another tab is shown; mounted again, it opens where it was
    const again = mount(<LogsPanel taskId="t1" />, s);
    await waitFor(() => expect(text(again.$('[data-testid="log-text"]'))).toContain('tail of ev:2'), again);
    cleanup();
    const other = mount(<LogsPanel taskId="t2" />, s);
    await waitFor(() => expect(text(other.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1'), other);
  });

  it('stays on the stream it opened when the stream stops, not moving to an older failed check (X23)', async () => {
    let running = true;
    const s = testServices({
      logsList: async () => [
        { id: 'ev:1', kind: 'check', title: 'test', size: 10, passed: false, created: 1 },
        { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 10, created: 2, running },
      ],
      logsRead: async (_t, id) => ({ text: `tail of ${id}\n`, start: 0, next: 10, size: 10 }),
    });
    const r = mount(<LogsPanel taskId="t1" />, s);
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1'), r);
    const pre = r.$('[data-testid="log-text"]');
    running = false;
    s.emit({ type: 'task.updated', taskId: 't1' });
    await waitFor(() => expect(r.$('ul[aria-label="Logs"]')!.textContent).not.toContain('running'), r);
    await drain();
    r.root.flush();
    expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of nx:r|work|2|1');
    // the same view, not remounted on it
    expect(r.$('[data-testid="log-text"]')).toBe(pre);
  });

  it('shows every log or only the failed checks (X25)', async () => {
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [
          { id: 'ev:1', kind: 'check', title: 'test', size: 10, passed: false, created: 1 },
          { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 10, created: 2 },
          { id: 'ev:2', kind: 'check', title: 'lint', size: 10, passed: true, created: 3 },
        ],
        logsRead: async (_t, id) => ({ text: `tail of ${id}\n`, start: 0, next: 10, size: 10 }),
      }),
    );
    await waitFor(() => expect(r.$$('ul[aria-label="Logs"] li').length).toBe(3), r);
    expect(byText(r.container, 'button', 'All').getAttribute('aria-pressed')).toBe('true');
    await r.act(() => byText(r.container, 'button', 'Failed (1)').click());
    expect(r.$$('ul[aria-label="Logs"] li').length).toBe(1);
    expect(byText(r.container, 'button', 'Failed (1)').getAttribute('aria-pressed')).toBe('true');
    await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toContain('tail of ev:1'), r);
  });

  it('says when no check failed under Failed (X25)', async () => {
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [{ id: 'ev:2', kind: 'check', title: 'lint', size: 10, passed: true, created: 3 }],
        logsRead: async (_t, id) => ({ text: `tail of ${id}\n`, start: 0, next: 10, size: 10 }),
      }),
    );
    await waitFor(() => expect(r.$$('ul[aria-label="Logs"] li').length).toBe(1), r);
    await r.act(() => byText(r.container, 'button', 'Failed').click());
    has(r.container, 'No check has failed.');
  });

  it("names an agent stream by its agent, with the agent's avatar (X210)", async () => {
    const s = testServices({
      projectList: async () => [{ id: 'prj_1', name: 'gocalc' } as Project],
      licenseStatus: async () => lic(),
      appInfo: async () => ({ name: 'Drydock', user: 'u', human: 'h', dataDir: '/d' }),
      agentList: async () => [{ id: 'a1', name: 'Ada' } as Agent],
      presenceList: async () => [],
      roomList: async () => [],
      roomUnread: async () => [],
      needsYouList: async () => [],
      taskList: async () => [],
      logsList: async () => [
        { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 10, created: 2, agentId: 'a1' },
        { id: 'nx:r|review|3|1', kind: 'agent', title: 'review (step 3)', size: 10, created: 3, agentId: 'gone' },
      ],
      logsRead: async (_t, id) => ({ text: `tail of ${id}\n`, start: 0, next: 10, size: 10 }),
    });
    s.workspace.start();
    s.project.start();
    const r = mount(<LogsPanel taskId="t1" />, s);
    await waitFor(() => has(r.container, 'Ada · work (step 2)'), r);
    const ada = byText(r.container, 'button', /Ada · work/);
    expect(ada.querySelector('[data-letters="A"]')).toBeTruthy();
    // an agent the project no longer has: the step's name alone, with the generic icon
    const review = byText(r.container, 'button', /Agent:\s*review/);
    expect(review.querySelector('[data-letters]')).toBeNull();
    await r.act(() => ada.click());
    await waitFor(() => expect(r.$('[data-testid="log-text"]')!.getAttribute('aria-label')).toBe('Ada · work (step 2) log'), r);
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

  it('polls without piling up listeners, rests while the window is hidden, and stops with the panel (L47)', async (t) => {
    const timers = t.useFakeTimers();
    const live = new Map<unknown, AbortSignal>(); // the abort listeners registered on signals and not removed
    const add = EventTarget.prototype.addEventListener;
    const remove = EventTarget.prototype.removeEventListener;
    EventTarget.prototype.addEventListener = function (this: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | AddEventListenerOptions) {
      if (this instanceof AbortSignal && type === 'abort' && listener) live.set(listener, this);
      return add.call(this, type, listener, options);
    } as typeof add;
    EventTarget.prototype.removeEventListener = function (this: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null, options?: boolean | EventListenerOptions) {
      if (this instanceof AbortSignal && type === 'abort' && listener) live.delete(listener);
      return remove.call(this, type, listener, options);
    } as typeof remove;
    let visibility: DocumentVisibilityState = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
    const setVisibility = (v: DocumentVisibilityState) => {
      visibility = v;
      document.dispatchEvent(new Event('visibilitychange'));
    };
    let reads = 0;
    const r = mount(
      <LogsPanel taskId="t1" />,
      testServices({
        logsList: async () => [{ id: 'nx:1', kind: 'agent', title: 'work', size: 0, created: 1, running: true } as LogSource],
        logsRead: async (_t, _id, o) => {
          reads++;
          return o?.tail ? { text: 'first\n', start: 0, next: 6, size: 6 } : { text: '', start: 6, next: 6, size: 6 };
        },
      }),
    );
    const settle = async (ms: number) => {
      await timers.advanceAsync(ms);
      await drain();
      await drain();
    };
    try {
      await waitFor(() => expect(text(r.$('[data-testid="log-text"]'))).toBe('first'), r);
      await settle(0);
      expect(reads).toBe(1);
      const baseline = live.size;
      for (let i = 0; i < 12; i++) await settle(1000); // a running source is read every second
      expect(reads).toBe(13);
      expect(live.size - baseline).toBeLessThanOrEqual(1); // it was one more listener per poll
      // the window is hidden: nobody reads the tail, so nothing is asked for
      setVisibility('hidden');
      await settle(0);
      const before = reads;
      for (let i = 0; i < 5; i++) await settle(5000);
      expect(reads).toBe(before);
      // and it is read again as soon as the window is shown
      setVisibility('visible');
      await settle(0);
      expect(reads).toBe(before + 1);
      // the panel is closed: the poll ends with it
      r.cleanup();
      const closed = reads;
      for (let i = 0; i < 5; i++) await settle(5000);
      expect(reads).toBe(closed);
    } finally {
      delete (document as unknown as Record<string, unknown>).visibilityState;
      EventTarget.prototype.addEventListener = add;
      EventTarget.prototype.removeEventListener = remove;
    }
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
  const info = (id: string, title: string, projectId = 'p1'): TerminalInfo => ({ id, title, cwd: '/x', shell: 'sh', cols: 100, rows: 24, startedAt: '', exited: false, exitCode: 0, projectId });

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

  it("brings up the terminal another page asks for (the task page's Open the worktree), once, reusing the open one (X27)", async () => {
    let open: TerminalInfo[] = [];
    const opens: { taskId?: string; projectId?: string; reuse?: boolean }[] = [];
    const s = testServices({
      terminalList: async () => open,
      // the core's reuse: the shell already running for the same ask
      terminalOpen: async (where) => {
        opens.push(where);
        const same = where.reuse ? open.find((t) => t.title === (where.taskId ? 'worktree' : 'repo')) : undefined;
        if (same) return same;
        const t = info(`term_${open.length + 1}`, where.taskId ? 'worktree' : 'repo');
        open = [...open, t];
        return t;
      },
      terminalRead: () => new Promise<TerminalChunk>(() => {}),
      terminalResize: async () => {},
    });
    // asked before the dock is drawn: the panel opens it when it mounts
    s.ui.openTerminal({ taskId: 't1' });
    expect(s.ui.dock).toEqual({ ...s.ui.dock, open: true, tab: 'terminal' });
    const r = mount(<TerminalPanel projectId="p1" taskId="t1" />, s);
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('worktree'), r);
    expect(opens).toEqual([{ taskId: 't1', reuse: true }]);
    // the person opens a repository terminal, then presses O again: the worktree's shell comes back, no second one starts
    await r.act(() => byText(r.container, 'button', 'New terminal in the repository').click());
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('repo'), r);
    await r.act(() => s.ui.openTerminal({ taskId: 't1' }));
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('worktree'), r);
    expect(opens).toEqual([{ taskId: 't1', reuse: true }, { projectId: 'p1' }, { taskId: 't1', reuse: true }]);
    expect(Array.from(r.$$('[role="tab"]')).map((e) => e.textContent)).toEqual(['worktree', 'repo']);
    // the ask is taken once
    expect(s.ui.takeTerminalAsk()).toBe(undefined);
  });

  it('opens a terminal when the person asks for the tab and none of the project runs, once, and not on its own (G6)', async () => {
    let open: TerminalInfo[] = [];
    const opens: { taskId?: string; projectId?: string; reuse?: boolean }[] = [];
    let fail = false;
    const s = testServices({
      terminalList: async () => open,
      terminalOpen: async (where) => {
        opens.push(where);
        if (fail) throw new Error('no shell');
        const t = info(`term_${open.length + 1}`, where.taskId ? 'task #3' : 'gocalc');
        open = [...open, t];
        return t;
      },
      terminalRead: () => new Promise<TerminalChunk>(() => {}),
      terminalResize: async () => {},
    });
    // the dock reopened on the tab it was left on: nothing asked, nothing started
    s.ui.setDock({ open: true, tab: 'terminal' });
    let r = mount(<TerminalPanel projectId="p1" taskId="t1" />, s);
    await waitFor(() => has(r.container, /No terminal is open/), r);
    await drain();
    await drain();
    expect(opens).toEqual([]);
    r.cleanup();
    // Ctrl+` (or the tab, or the command menu): a terminal in the task's worktree
    s.ui.setDock({ open: false });
    s.ui.toggleTerminal();
    expect(s.ui.dock).toEqual({ ...s.ui.dock, open: true, tab: 'terminal' });
    r = mount(<TerminalPanel projectId="p1" taskId="t1" />, s);
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('task #3'), r);
    expect(opens).toEqual([{ taskId: 't1', reuse: true }]);
    r.cleanup();
    // asked again with one of the project's running: it is shown, nothing new starts
    s.ui.showTerminal();
    r = mount(<TerminalPanel projectId="p1" taskId={undefined} />, s);
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('task #3'), r);
    await drain();
    await drain();
    expect(opens.length).toBe(1);
    r.cleanup();
    // without a task, in the project's repository; a failure shows and is not tried again
    open = [{ ...info('term_9', 'other'), projectId: 'p2' }];
    fail = true;
    s.ui.showTerminal();
    r = mount(<TerminalPanel projectId="p1" taskId={undefined} />, s);
    await waitFor(() => has(r.$('[role="alert"]')!, 'Terminal: no shell'), r);
    await drain();
    await drain();
    expect(opens.slice(1)).toEqual([{ projectId: 'p1', reuse: true }]);
  });

  it("shows this project's terminals and folds the others under one button (X213)", async () => {
    const s = testServices({
      terminalList: async () => [info('term_1', 'gocalc'), info('term_2', 'webshop', 'p2'), info('term_3', 'task #4'), { ...info('term_4', 'Sign in: claude'), projectId: undefined }],
      terminalRead: () => new Promise<TerminalChunk>(() => {}),
      terminalResize: async () => {},
    });
    const r = mount(<TerminalPanel projectId="p1" taskId={undefined} />, s);
    await waitFor(() => expect(Array.from(r.$$('[role="tab"]')).map((e) => e.textContent)).toEqual(['gocalc', 'task #4']), r);
    // the last of this project's is current, not another project's newer one
    expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('task #4');
    const fold = byText<HTMLButtonElement>(r.container, 'button', 'Other terminals (2)');
    expect(fold.getAttribute('aria-expanded')).toBe('false');
    await r.act(() => fold.click());
    await waitFor(() => expect(Array.from(r.$$('[role="tab"]')).map((e) => e.textContent)).toEqual(['gocalc', 'task #4', 'webshop', 'Sign in: claude']), r);
    expect(fold.getAttribute('aria-expanded')).toBe('true');
    await r.act(() => byText(r.container, '[role="tab"]', 'webshop').click());
    await waitFor(() => expect(r.$('[role="tab"][aria-selected="true"]')?.textContent).toBe('webshop'), r);
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

  it('keeps reading after a failed poll: it retries with backoff and carries on (F20)', async () => {
    const enc = new TextEncoder();
    const reads: number[] = [];
    let failOnce = true;
    const r = mount(
      <TerminalPanel projectId={undefined} taskId="t1" />,
      testServices({
        terminalList: async () => [info('term_1', 'task #3')],
        terminalRead: async (_id, from) => {
          reads.push(from);
          if (from === 0) return { data: enc.encode('first\r\n'), next: 7, dropped: 0, exited: false, exitCode: 0 };
          if (failOnce) {
            failOnce = false;
            throw new Error('Could not reach the Drydock core: Failed to fetch');
          }
          if (from === 7) return { data: enc.encode('second\r\n'), next: 15, dropped: 0, exited: false, exitCode: 0 };
          return new Promise<TerminalChunk>(() => {});
        },
        terminalResize: async () => {},
      }),
    );
    await waitFor(() => expect(terminals.length).toBe(1), r);
    const term = terminals[0]!;
    await waitFor(() => expect(term.output).toContain('[lost the connection to the terminal'), r);
    await waitFor(() => expect(term.output).toContain('second'), r, 3000);
    expect(term.output).toContain('[reconnected]');
    expect(term.output).not.toContain('the terminal is gone');
    expect(reads).toEqual([0, 7, 7, 15]);
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

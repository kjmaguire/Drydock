import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import { createDockClient, decodeBase64, type DiffFileContent, type DiffFiles } from '../api/dockClient';
import { AppServicesProvider } from '../state/context';
import { FakeTransport, services } from '../test/fakes';
import { DiffViewer } from './DiffViewer';
import { Dock } from './Dock';
import { LogsPanel } from './LogsPanel';
import { TerminalPanel } from './TerminalPanel';

// Monaco and xterm need a real browser (the Playwright tests drive them); here they are labelled stand-ins that show what
// they were given.
vi.mock('./MonacoDiff', () => ({
  default: (p: { path: string; original: string; modified: string; sideBySide: boolean }) => (
    <div data-testid="monaco-stub" data-side={String(p.sideBySide)}>
      <pre data-testid="orig">{p.original}</pre>
      <pre data-testid="mod">{p.modified}</pre>
    </div>
  ),
}));
vi.mock('./Xterm', () => ({ default: (p: { id: string }) => <div data-testid="xterm-stub">{p.id}</div> }));

const withServices = (ui: React.ReactElement, client: Partial<Client>) => render(<AppServicesProvider services={services(client)}>{ui}</AppServicesProvider>);

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

describe('the dock client', () => {
  it('decodes terminal output from base64 and turns null lists into empty ones', async () => {
    const t = new FakeTransport();
    t.responses.set('terminal.read', { data: btoa('héllo'.normalize()), next: 6, exited: false, exitCode: 0 });
    t.responses.set('logs.list', null);
    t.responses.set('task.diffFiles', { source: 'none', files: null, liveAvailable: false });
    const c = createDockClient(t);
    const r = await c.terminalRead('term_1', 0, 10);
    expect(r.data.length).toBeGreaterThan(0);
    expect(r.dropped).toBe(0);
    expect(await c.logsList('t')).toEqual([]);
    expect((await c.taskDiffFiles('t')).files).toEqual([]);
    expect(t.calls.find((x) => x.method === 'task.diffFiles')?.params).toEqual({ id: 't', live: false });
    expect(Array.from(decodeBase64(btoa('ab')))).toEqual([97, 98]);
    expect(decodeBase64(null).length).toBe(0);
  });
});

describe('the diff viewer', () => {
  it('lists the files, shows both versions, switches layout and live mode, and guards binary and large files', async () => {
    const taskDiffFiles = vi.fn(async (_id: string, live?: boolean) => files(live ? { source: 'live', head: undefined } : {}));
    const taskDiffFile = vi.fn(async (_id: string, path: string, _live?: boolean, force?: boolean): Promise<DiffFileContent> => {
      if (path === 'img.png') return { path, status: 'added', original: '', modified: '', binary: true };
      if (path === 'big.txt' && !force) return { path, status: 'modified', original: '', modified: '', tooLarge: true, size: 3 << 20 };
      return { path, status: 'modified', original: `old ${path}`, modified: `new ${path}` };
    });
    withServices(<DiffViewer taskId="t1" />, { taskDiffFiles, taskDiffFile });
    expect((await screen.findByTestId('mod')).textContent).toContain('new calc.go');
    expect((screen.getByTestId('orig')).textContent).toContain('old calc.go');
    expect(screen.getByRole('list', { name: 'Changed files' }).querySelectorAll('li')).toHaveLength(3);

    fireEvent.click(screen.getByRole('button', { name: 'Inline' }));
    expect((screen.getByTestId('monaco-stub')).getAttribute('data-side')).toBe('false');

    fireEvent.click(screen.getByRole('button', { name: 'Next file' }));
    expect(await screen.findByText('Binary file: not shown.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Next file' }));
    expect(await screen.findByText(/3\.0 MB, too large/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Show it anyway' }));
    expect((await screen.findByTestId('mod')).textContent).toContain('new big.txt');
    expect(taskDiffFile).toHaveBeenLastCalledWith('t1', 'big.txt', false, true);

    fireEvent.click(screen.getByRole('button', { name: 'Live' }));
    await waitFor(() => expect(taskDiffFiles).toHaveBeenLastCalledWith('t1', true));
    expect(await screen.findByText('Live worktree')).toBeTruthy();
  });

  it('says so when nothing is recorded, and Live is off without a worktree', async () => {
    withServices(<DiffViewer taskId="t1" />, { taskDiffFiles: async () => files({ source: 'none', files: [], liveAvailable: false }) });
    expect(await screen.findByText(/No diff has been recorded yet/)).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Live' })) as HTMLButtonElement).toHaveProperty('disabled', true);
  });

  it('marks a partial diff whose commits are gone', async () => {
    withServices(<DiffViewer taskId="t1" />, {
      taskDiffFiles: async () => files({ files: [{ path: 'a.go', status: 'modified', additions: 1, deletions: 1 }] }),
      taskDiffFile: async (_id, path) => ({ path, status: 'modified', original: 'x\n⋯\ny', modified: 'x\n⋯\nz', partial: true }),
    });
    expect(await screen.findByTestId('diff-partial')).toBeTruthy();
  });
});

describe('the logs panel', () => {
  it('lists check logs and agent streams, opens the newest at its tail, and reads earlier output on request', async () => {
    const logsRead = vi.fn(async (_t: string, id: string, o?: { from?: number; limit?: number; tail?: boolean }) => {
      if (o?.tail) return { text: `tail of ${id}\nsecond line\n`, start: 100, next: 140, size: 140 };
      if ((o?.from ?? 0) < 100) return { text: 'the beginning\n', start: 0, next: 100, size: 140 };
      return { text: '', start: 140, next: 140, size: 140 };
    });
    withServices(<LogsPanel taskId="t1" />, {
      logsList: async () => [
        { id: 'ev:1', kind: 'check', title: 'test', size: 10, passed: false, created: 1 },
        { id: 'nx:r|work|2|1', kind: 'agent', title: 'work (step 2)', size: 140, created: 2 },
      ],
      logsRead,
    });
    await waitFor(() => expect(screen.getByTestId('log-text').textContent).toContain('tail of nx:r|work|2|1'));
    expect(screen.getByText('failed')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Show earlier output' }));
    await waitFor(() => expect(screen.getByTestId('log-text').textContent).toMatch(/^the beginning\ntail of/));
    fireEvent.click(screen.getByRole('button', { name: /Check:\s*test/ }));
    await waitFor(() => expect(screen.getByTestId('log-text').textContent).toContain('tail of ev:1'));
  });

  it('says when there is nothing yet', async () => {
    withServices(<LogsPanel taskId="t1" />, { logsList: async () => [] });
    expect(await screen.findByText(/No logs yet/)).toBeTruthy();
  });
});

describe('the terminal panel', () => {
  it('opens a terminal in the task or the repository and closes it', async () => {
    let open: { id: string; title: string }[] = [];
    const terminalOpen = vi.fn(async (where: { taskId?: string; projectId?: string }) => {
      const t = { id: `term_${open.length + 1}`, title: where.taskId ? 'task #3' : 'gocalc', cwd: '/x', shell: 'sh', cols: 100, rows: 24, startedAt: '', exited: false, exitCode: 0 };
      open = [...open, t];
      return t;
    });
    const terminalClose = vi.fn(async (id: string) => {
      open = open.filter((t) => t.id !== id);
    });
    withServices(<TerminalPanel projectId="p1" taskId="t1" />, {
      terminalList: async () => open as never,
      terminalOpen,
      terminalClose,
    });
    expect(await screen.findByText(/No terminal is open/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'New terminal in the task' }));
    expect((await screen.findByTestId('xterm-stub')).textContent).toContain('term_1');
    expect(terminalOpen).toHaveBeenCalledWith({ taskId: 't1' }, 100, 24);
    fireEvent.click(screen.getByRole('button', { name: 'New terminal in the repository' }));
    expect(await screen.findByRole('tab', { name: 'gocalc' })).toBeTruthy();
    expect(terminalOpen).toHaveBeenLastCalledWith({ projectId: 'p1' }, 100, 24);
    fireEvent.click(screen.getByRole('button', { name: 'Close gocalc' }));
    await waitFor(() => expect(screen.queryByRole('tab', { name: 'gocalc' })).toBeNull());
    expect(terminalClose).toHaveBeenCalledWith('term_2');
  });
});

describe('the dock', () => {
  it('is closed at first, opens on a tab, toggles with Ctrl+J, and remembers per project', async () => {
    localStorage.clear();
    const client: Partial<Client> = { usageList: async () => [], terminalList: async () => [] };
    const { unmount } = withServices(<Dock projectId="p1" taskId={undefined} />, client);
    expect(screen.queryByRole('tabpanel')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: 'Diff' }));
    expect((screen.getByRole('tabpanel')).textContent).toContain('Open a task (or its channel) to see its diff.');
    act(() => {
      fireEvent.keyDown(window, { key: 'j', ctrlKey: true });
    });
    expect(screen.queryByRole('tabpanel')).toBeNull();
    act(() => {
      fireEvent.keyDown(window, { key: 'j', ctrlKey: true });
    });
    fireEvent.click(screen.getByRole('tab', { name: 'Activity' }));
    expect(await screen.findByText(/most recent usage/)).toBeTruthy();
    unmount();
    withServices(<Dock projectId="p1" taskId={undefined} />, client);
    expect((screen.getByRole('tab', { name: 'Activity' })).getAttribute('aria-selected')).toBe('true');
  });
});

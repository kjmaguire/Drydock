import { lazy, Suspense, useCallback, useState } from 'react';
import { ErrorNote } from '../components/common';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';

const Xterm = lazy(() => import('./Xterm'));

/**
 * The human's terminals (docs/ui.md 6.5): a shell in the task's worktree or the project's repository. Closing a tab kills
 * the shell and everything it started. Nothing typed or printed here is stored.
 */
export function TerminalPanel({ projectId, taskId }: { projectId: string | undefined; taskId: string | undefined }) {
  const client = useClient();
  const list = useLive(() => client.terminalList(), [], () => false);
  const act = useAction();
  const [active, setActive] = useState<string | undefined>();
  const [exited, setExited] = useState<Record<string, number>>({});
  const terms = list.data ?? [];
  const current = terms.find((t) => t.id === active) ?? terms[terms.length - 1];

  const open = async (where: { projectId?: string; taskId?: string }) => {
    const t = await act.run('open', () => client.terminalOpen(where, 100, 24));
    if (t) {
      await list.reload();
      setActive(t.id);
    }
  };
  const close = async (id: string) => {
    await act.run('close', () => client.terminalClose(id));
    await list.reload();
  };
  const onExit = useCallback((id: string, code: number) => setExited((m) => ({ ...m, [id]: code })), []);

  return (
    <div className="terminal-panel" data-testid="terminal-panel">
      <div className="diff-toolbar" role="toolbar" aria-label="Terminals">
        <div role="tablist" aria-label="Open terminals" className="tabs">
          {terms.map((t, i) => (
            <span key={t.id} className="term-tab">
              <button role="tab" aria-selected={t === current} onClick={() => setActive(t.id)} title={t.cwd}>
                {t.title || `Terminal ${i + 1}`}
                {exited[t.id] !== undefined || t.exited ? ' (exited)' : ''}
              </button>
              <button className="link-button" aria-label={`Close ${t.title || `terminal ${i + 1}`}`} onClick={() => close(t.id)}>
                ×
              </button>
            </span>
          ))}
        </div>
        {taskId ? (
          <button onClick={() => open({ taskId })} disabled={!!act.busy}>
            New terminal in the task
          </button>
        ) : null}
        {projectId ? (
          <button onClick={() => open({ projectId })} disabled={!!act.busy}>
            New terminal in the repository
          </button>
        ) : null}
      </div>
      <ErrorNote error={act.error ?? list.error} what="Terminal" />
      {current ? (
        <Suspense fallback={<p className="muted">Starting the terminal...</p>}>
          <Xterm client={client} id={current.id} key={current.id} onExit={(code) => onExit(current.id, code)} />
        </Suspense>
      ) : (
        <p className="muted">No terminal is open. A terminal runs your shell in the task's worktree or the project's repository; closing it stops everything it started.</p>
      )}
    </div>
  );
}

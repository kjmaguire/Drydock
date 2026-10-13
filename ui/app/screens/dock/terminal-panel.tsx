// The human's terminals (docs/ui.md 6.5): a shell in the task's worktree or the project's repository. Closing a tab kills
// the shell and everything it started. Nothing typed or printed here is stored. Output is read with a long poll by offset
// and written straight into the terminal (never through component state); input and resizes go back as calls.

import { css, on, ref, type Handle } from 'remix/component';
import { FolderGit2, GitBranch, SquareTerminal, X } from 'lucide';
import type { TerminalInfo } from '../../api/dockClient.ts';
import { projectHasRepo } from '../../api/types.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Empty, ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { orderedWriter, terminalReadRetry } from './dock-format.ts';
import { errorSlot, panel, toolbar } from './dock-ui.tsx';
import { loadTerminal, type TerminalView } from './widgets.ts';

const tabsCss = css({ display: 'flex', alignItems: 'center', gap: '2px', minWidth: '0', overflowX: 'auto' });
const tabCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '26px',
  borderRadius: t.rMd,
  color: t.textMuted,
  '&:hover': { background: t.bgHover, color: t.text },
  '&[data-active="true"]': { background: t.bgActive, color: t.text },
  '& > button': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    minHeight: '26px',
    height: '26px',
    border: '0',
    background: 'transparent',
    color: 'inherit',
    fontSize: t.textSm,
    fontWeight: '500',
  },
  '& > button:hover': { background: 'transparent' },
  '& > button[role="tab"]': { padding: '0 4px 0 8px' },
  '& > button.close': { width: '20px', minHeight: '20px', height: '20px', padding: '0', marginRight: '3px', borderRadius: t.rSm, color: t.textFaint },
  '& > button.close:hover': { background: t.bgActive, color: t.text },
});
const exitedCss = css({ color: t.textFaint, fontWeight: '400' });
const actionsCss = css({ marginLeft: 'auto', display: 'flex', gap: '4px', flex: 'none' });
const hostCss = css({ position: 'relative', flex: '1', minHeight: '0', padding: `${t.s1} ${t.s2}`, background: t.bgApp, display: 'flex' });
const xtermHost = css({ flex: '1', minWidth: '0', minHeight: '0', '& .xterm': { height: '100%' } });
const startingCss = css({ position: 'absolute', inset: '0', margin: '0', padding: t.s3, color: t.textMuted, fontSize: t.textMd, pointerEvents: 'none' });

export function TerminalPanel(handle: Handle<{ projectId: string | undefined; taskId: string | undefined }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.ui);
  const list = query<TerminalInfo[]>(handle, { load: () => s.client.terminalList() });
  const act = action(handle);
  let active: string | undefined;
  const exited: Record<string, number> = {};
  // a terminal another screen opened (a Connection's sign-in) becomes the selected tab
  const takeWanted = () => {
    const id = s.ui.takeTerminalToShow();
    if (id && id !== active) {
      active = id;
      list.reload();
    }
  };

  const open = async (where: { projectId?: string; taskId?: string }) => {
    const term = await act.run('open', () => s.client.terminalOpen(where, 100, 24));
    if (term) {
      active = term.id;
      list.reload();
    }
  };
  const close = async (id: string) => {
    await act.run('close', () => s.client.terminalClose(id));
    list.reload();
  };
  const onExit = (id: string, code: number) => {
    exited[id] = code;
    void handle.update();
  };

  return () => {
    takeWanted();
    const { projectId, taskId } = handle.props;
    const p = s.workspace.projectList.find((x) => x.id === projectId);
    const noRepo = !!p && !projectHasRepo(p);
    const terms = list.data ?? [];
    const current = terms.find((x) => x.id === active) ?? terms[terms.length - 1];
    const err = act.error ?? list.error;
    return (
      <div mix={panel} data-testid="terminal-panel">
        <div role="toolbar" aria-label="Terminals" mix={toolbar}>
          <div role="tablist" aria-label="Open terminals" mix={tabsCss}>
            {terms.map((x, i) => {
              const title = x.title || `Terminal ${i + 1}`;
              const done = exited[x.id] !== undefined || x.exited;
              return (
                <span key={x.id} mix={tabCss} data-active={x === current ? 'true' : undefined}>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={x === current}
                    title={x.cwd}
                    mix={on('click', () => {
                      active = x.id;
                      void handle.update();
                    })}
                  >
                    <Icon icon={SquareTerminal} size={13} />
                    {title}
                    {done ? <span mix={exitedCss}> (exited)</span> : null}
                  </button>
                  <button type="button" class="close" aria-label={`Close ${x.title || `terminal ${i + 1}`}`} title="Close: stops the shell and everything it started" mix={on('click', () => void close(x.id))}>
                    <Icon icon={X} size={12} />
                  </button>
                </span>
              );
            })}
          </div>
          <div mix={actionsCss}>
            {taskId ? (
              <Button size="sm" variant="ghost" icon={GitBranch} disabled={!!act.busy} mix={on('click', () => void open({ taskId }))}>
                New terminal in the task
              </Button>
            ) : null}
            {projectId && noRepo ? (
              <Button size="sm" variant="ghost" icon={FolderGit2} disabled title="This project has no repository yet: attach one in Project settings">
                No repository for a terminal yet
              </Button>
            ) : projectId ? (
              <Button size="sm" variant="ghost" icon={FolderGit2} disabled={!!act.busy} mix={on('click', () => void open({ projectId }))}>
                New terminal in the repository
              </Button>
            ) : null}
          </div>
        </div>
        {err ? (
          <div mix={errorSlot}>
            <ErrorNote error={err} what="Terminal" />
          </div>
        ) : null}
        {current ? (
          <TerminalHost key={current.id} id={current.id} onExit={(code) => onExit(current.id, code)} />
        ) : (
          <Empty title="No open terminals" icon={SquareTerminal}>
            <p>No terminal is open. A terminal runs your shell in the task's worktree or the project's repository; closing it stops everything it started.</p>
          </Empty>
        )}
      </div>
    );
  };
}

/** One terminal of the core in a terminal widget, created in a ref on an EMPTY host and disposed with it. */
function TerminalHost(handle: Handle<{ id: string; onExit: (code: number) => void }>) {
  const s = services(handle);
  let starting = true;
  let error: unknown;

  const mount = (node: HTMLElement, signal: AbortSignal) => {
    loadTerminal().then(
      (create) => {
        if (signal.aborted) return;
        const id = handle.props.id;
        const client = s.client;
        const term: TerminalView = create(node);
        let alive = true;
        let size = '';
        term.onData(orderedWriter((d) => client.terminalWrite(id, d)));
        const resize = () => {
          try {
            term.fit();
          } catch {
            return; // not laid out yet
          }
          const k = `${term.cols}x${term.rows}`;
          if (k === size) return;
          size = k;
          void client.terminalResize(id, term.cols, term.rows).catch(() => {});
        };
        const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(resize) : undefined;
        ro?.observe(node);
        resize();
        term.focus();
        signal.addEventListener('abort', () => {
          alive = false;
          ro?.disconnect();
          term.dispose();
        });
        starting = false;
        void handle.update();

        void (async () => {
          let next = 0;
          let failures = 0;
          while (alive) {
            try {
              const c = await client.terminalRead(id, next, 15000);
              if (!alive) return;
              if (failures > 0) term.write('\r\n[reconnected]\r\n');
              failures = 0;
              if (c.dropped > 0) term.write(`\r\n[${c.dropped} bytes of earlier output were not kept]\r\n`);
              if (c.data.length) term.write(c.data);
              next = c.next;
              if (c.exited && c.data.length === 0) {
                term.write(`\r\n[the shell exited with code ${c.exitCode}]\r\n`);
                handle.props.onExit(c.exitCode);
                return;
              }
            } catch (e) {
              if (!alive) return;
              // one failed poll (the core busy or restarting, a dropped connection) is retried with backoff; only a
              // terminal the core no longer has, or a failure that outlasts the retries, ends the loop
              const msg = e instanceof Error ? e.message : String(e);
              const wait = terminalReadRetry(e, ++failures);
              if (wait === undefined) {
                term.write(`\r\n[the terminal is gone: ${msg}]\r\n`);
                return;
              }
              if (failures === 1) term.write(`\r\n[lost the connection to the terminal (${msg}); retrying]\r\n`);
              await new Promise((res) => setTimeout(res, wait));
            }
          }
        })();
      },
      (e: unknown) => {
        if (signal.aborted) return;
        error = e;
        starting = false;
        void handle.update();
      },
    );
  };

  return () => (
    <div mix={hostCss}>
      <div mix={[xtermHost, ref(mount)]} data-testid="xterm" data-own-keys="" />
      {starting ? <p mix={startingCss}>Starting the terminal...</p> : null}
      {error ? (
        <div mix={errorSlot} style={{ position: 'absolute', inset: '0' }}>
          <ErrorNote error={error} what="Could not start the terminal" />
        </div>
      ) : null}
    </div>
  );
}

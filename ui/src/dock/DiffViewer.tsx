import { lazy, Suspense, useEffect, useState } from 'react';
import type { DiffFile, DiffFileContent } from '../api/dockClient';
import { ErrorNote } from '../components/common';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';

const MonacoDiff = lazy(() => import('./MonacoDiff'));

const SIDE_KEY = 'drydock.diff.sideBySide';
function loadSide(): boolean {
  try {
    return localStorage.getItem(SIDE_KEY) !== 'inline';
  } catch {
    return true;
  }
}

const STATUS_LETTER: Record<DiffFile['status'], string> = { added: 'A', deleted: 'D', modified: 'M', renamed: 'R' };

function formatSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} bytes`;
}

/**
 * A task's changes, file by file, in Monaco's read-only diff editor (docs/ui.md 6.1). "Recorded" is the diff of the last
 * finished attempt; "Live" is the worktree as it is now, while an agent works.
 */
export function DiffViewer({ taskId }: { taskId: string }) {
  const client = useClient();
  const [live, setLive] = useState(false);
  const [side, setSide] = useState(loadSide);
  const [selected, setSelected] = useState<string | undefined>();
  const list = useLive(() => client.taskDiffFiles(taskId, live), [taskId, live], (e) => e.taskId === taskId, true, 300);
  const files = list.data?.files ?? [];
  const current = files.find((f) => f.path === selected) ?? files[0];

  useEffect(() => {
    try {
      localStorage.setItem(SIDE_KEY, side ? 'side' : 'inline');
    } catch {
      /* a convenience only */
    }
  }, [side]);
  useEffect(() => {
    if (live && list.data && !list.data.liveAvailable) setLive(false);
  }, [live, list.data]);

  if (list.loading && !list.data) return <p className="muted">Loading the diff...</p>;
  if (list.error && !list.data) return <ErrorNote error={list.error} what="Could not load the diff" />;
  const d = list.data;
  if (!d) return null;
  const idx = current ? files.indexOf(current) : -1;

  return (
    <div className="diff-viewer" data-testid="diff-viewer">
      <div className="diff-toolbar" role="toolbar" aria-label="Diff">
        <div className="segmented" role="group" aria-label="Which diff">
          <button aria-pressed={!live} onClick={() => setLive(false)}>
            Recorded
          </button>
          <button aria-pressed={live} onClick={() => setLive(true)} disabled={!d.liveAvailable} title={d.liveAvailable ? 'The worktree as it is now' : 'The task has no worktree any more'}>
            Live
          </button>
        </div>
        <div className="segmented" role="group" aria-label="Layout">
          <button aria-pressed={side} onClick={() => setSide(true)}>
            Side by side
          </button>
          <button aria-pressed={!side} onClick={() => setSide(false)}>
            Inline
          </button>
        </div>
        <button onClick={() => current && idx > 0 && setSelected(files[idx - 1]!.path)} disabled={idx <= 0}>
          Previous file
        </button>
        <button onClick={() => idx >= 0 && idx < files.length - 1 && setSelected(files[idx + 1]!.path)} disabled={idx < 0 || idx >= files.length - 1}>
          Next file
        </button>
        <span className="muted">
          {d.source === 'live' ? 'Live worktree' : d.source === 'recorded' ? 'Recorded with the last attempt' : ''}
          {d.head ? <> · <code>{d.head.slice(0, 10)}</code></> : null}
        </span>
      </div>
      <ErrorNote error={list.error} what="Could not refresh the diff" />
      {d.source === 'none' ? (
        <p className="muted">No diff has been recorded yet. {d.liveAvailable ? 'Choose Live to see the worktree now.' : ''}</p>
      ) : files.length === 0 ? (
        <p className="muted">No changes.</p>
      ) : (
        <div className="diff-body">
          <ul className="diff-files" aria-label="Changed files">
            {files.map((f) => (
              <li key={f.path}>
                <button className={f === current ? 'diff-file active' : 'diff-file'} aria-current={f === current ? 'true' : undefined} onClick={() => setSelected(f.path)} title={f.oldPath ? `${f.oldPath} → ${f.path}` : f.path}>
                  <span className={`diff-status diff-status-${f.status}`} aria-label={f.status}>
                    {STATUS_LETTER[f.status]}
                  </span>
                  <span className="diff-path">{f.path}</span>
                  <span className="diff-counts">
                    {f.binary ? 'binary' : (
                      <>
                        <span className="diff-add">+{f.additions}</span> <span className="diff-del">-{f.deletions}</span>
                      </>
                    )}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {current ? <FilePane taskId={taskId} file={current} live={live} sideBySide={side} key={`${live}:${current.path}`} /> : null}
        </div>
      )}
    </div>
  );
}

function FilePane({ taskId, file, live, sideBySide }: { taskId: string; file: DiffFile; live: boolean; sideBySide: boolean }) {
  const client = useClient();
  const [force, setForce] = useState(false);
  const c = useLive<DiffFileContent>(() => client.taskDiffFile(taskId, file.path, live, force), [taskId, file.path, live, force], (e) => e.taskId === taskId, true, 300);
  const v = c.data;
  return (
    <div className="diff-pane" aria-label={`Changes in ${file.path}`}>
      <div className="diff-pane-head">
        <strong>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</strong> <span className="muted">{file.status}</span>
      </div>
      <ErrorNote error={c.error} what="Could not load this file" />
      {!v ? (
        c.loading ? <p className="muted">Loading...</p> : null
      ) : v.binary ? (
        <p className="muted">Binary file: not shown.</p>
      ) : v.tooLarge ? (
        <p>
          This file is {formatSize(v.size ?? 0)}, too large to show by default.{' '}
          <button onClick={() => setForce(true)}>Show it anyway</button>
        </p>
      ) : (
        <>
          {v.partial ? (
            <p className="muted" data-testid="diff-partial">
              Git no longer has these commits, so only the changed lines and their context are shown (gaps are marked ⋯).
            </p>
          ) : null}
          <Suspense fallback={<p className="muted">Loading the diff editor...</p>}>
            <MonacoDiff path={v.path} original={v.original} modified={v.modified} sideBySide={sideBySide} />
          </Suspense>
        </>
      )}
    </div>
  );
}

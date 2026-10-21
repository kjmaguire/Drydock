// A task's changes, file by file, in Monaco's read-only diff editor (docs/ui.md 6.1). "Recorded" is the diff of the last
// finished attempt; "Live" is the worktree as it is now, while an agent works. Used in the dock and on the task page.

import { css, on, ref, type Handle } from 'remix/component';
import { ChevronDown, ChevronUp, Columns2, FileDiff, Rows2 } from 'lucide';
import type { DiffFile, DiffFileContent, DiffFiles } from '../../api/dockClient.ts';
import { query, services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { diffTotals, formatSize, splitPath, STATUS_LETTER } from './dock-format.ts';
import { ellipsis, errorSlot, pane, paneHead, paneNote, panel, segmented, sideList, split, toolbar, toolbarNote } from './dock-ui.tsx';
import { loadDiffEditor, type DiffEditorView } from './widgets.ts';

const SIDE_KEY = 'drydock.diff.sideBySide';
function loadSide(): boolean {
  try {
    return localStorage.getItem(SIDE_KEY) !== 'inline';
  } catch {
    return true;
  }
}
function saveSide(side: boolean): void {
  try {
    localStorage.setItem(SIDE_KEY, side ? 'side' : 'inline');
  } catch {
    /* a convenience only */
  }
}

const STATUS_COLOR: Record<DiffFile['status'], string> = { added: t.success, deleted: t.danger, modified: t.warning, renamed: t.info };

// On a page the diff gets room of its own; inside the dock it fits the dock's height.
const bodyCss = css({ minHeight: '320px', ':is(#dock-panel) &': { minHeight: '0' } });
const statusCss = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '16px',
  flex: 'none',
  fontFamily: t.mono,
  fontSize: t.textXs,
  fontWeight: '600',
});
const fileName = css({ color: t.text, fontFamily: t.mono, fontSize: t.textSm });
const fileDir = css({ color: t.textFaint, fontSize: t.textXs, marginLeft: '6px' });
const countsCss = css({ marginLeft: 'auto', paddingLeft: t.s2, display: 'inline-flex', gap: t.s2, fontFamily: t.mono, fontSize: t.textXs, flex: 'none' });
const addCss = css({ color: t.success });
const delCss = css({ color: t.danger });
const binCss = css({ color: t.textFaint });
const totalsCss = css({ marginLeft: 'auto', display: 'inline-flex', gap: t.s2, alignItems: 'center', color: t.textMuted, fontFamily: t.mono, fontSize: t.textXs });
const hostWrap = css({ position: 'relative', flex: '1', minHeight: '300px', display: 'flex', ':is(#dock-panel) &': { minHeight: '0' } });
const hostCss = css({ flex: '1', minWidth: '0', minHeight: '0' });
const hostLoading = css({ position: 'absolute', inset: '0', margin: '0', padding: t.s3, color: t.textMuted, fontSize: t.textMd, pointerEvents: 'none' });
const hostError = css({ pointerEvents: 'auto' });
const largeCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap', margin: '0', padding: t.s3, fontSize: t.textMd });
const noteSlot = css({ padding: `${t.s2} ${t.s3} 0` });

export function DiffViewer(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  let live = false;
  let side = loadSide();
  let selected: string | undefined;
  const list = query<DiffFiles>(handle, {
    load: () => s.client.taskDiffFiles(handle.props.taskId, live),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => [handle.props.taskId, live],
    debounceMs: 300,
  });
  const setLive = (v: boolean) => {
    live = v;
    void handle.update();
  };
  const setSide = (v: boolean) => {
    side = v;
    saveSide(v);
    void handle.update();
  };
  const select = (path: string) => {
    selected = path;
    void handle.update();
  };

  return () => {
    const taskId = handle.props.taskId;
    // the worktree went away: back to the recorded diff
    if (live && list.data && !list.data.liveAvailable) live = false;
    const d = list.data;
    if (!d && list.error) return <div mix={noteSlot}><ErrorNote error={list.error} what="Could not load the diff" /></div>;
    if (!d) return <Loading label="Loading the diff..." />;
    const files = d.files;
    const current = files.find((f) => f.path === selected) ?? files[0];
    const idx = current ? files.indexOf(current) : -1;
    const totals = diffTotals(files);

    return (
      <div mix={panel} data-testid="diff-viewer">
        <div role="toolbar" aria-label="Diff" mix={toolbar}>
          <div role="group" aria-label="Which diff" mix={segmented}>
            <button type="button" aria-pressed={!live} mix={on('click', () => setLive(false))}>
              Recorded
            </button>
            <button
              type="button"
              aria-pressed={live}
              disabled={!d.liveAvailable}
              title={d.liveAvailable ? 'The worktree as it is now' : 'The task has no worktree any more'}
              mix={on('click', () => setLive(true))}
            >
              Live
            </button>
          </div>
          <div role="group" aria-label="Layout" mix={segmented}>
            <button type="button" aria-pressed={side} mix={on('click', () => setSide(true))}>
              <Icon icon={Columns2} size={13} />
              Side by side
            </button>
            <button type="button" aria-pressed={!side} mix={on('click', () => setSide(false))}>
              <Icon icon={Rows2} size={13} />
              Inline
            </button>
          </div>
          <span class="sep" />
          <IconButton icon={ChevronUp} label="Previous file" size={15} disabled={idx <= 0} mix={on('click', () => {
              if (idx > 0) select(files[idx - 1]!.path);
            })} />
          <IconButton icon={ChevronDown} label="Next file" size={15} disabled={idx < 0 || idx >= files.length - 1} mix={on('click', () => {
              if (idx >= 0 && idx < files.length - 1) select(files[idx + 1]!.path);
            })} />
          <span mix={toolbarNote}>
            <span>{d.source === 'live' ? 'Live worktree' : d.source === 'recorded' ? 'Recorded with the last attempt' : ''}</span>
            {d.head ? <code title={d.head}>{d.head.slice(0, 10)}</code> : null}
          </span>
          {files.length > 0 ? (
            <span mix={totalsCss} aria-label={`${totals.files} files changed, ${totals.additions} additions, ${totals.deletions} deletions`}>
              <span>{totals.files === 1 ? '1 file' : `${totals.files} files`}</span>
              <span mix={addCss}>+{totals.additions}</span>
              <span mix={delCss}>-{totals.deletions}</span>
            </span>
          ) : null}
        </div>
        {list.error ? (
          <div mix={errorSlot}>
            <ErrorNote error={list.error} what="Could not refresh the diff" />
          </div>
        ) : null}
        {d.source === 'none' ? (
          <Empty title="No diff yet" icon={FileDiff}>
            <p>No diff has been recorded yet. {d.liveAvailable ? 'Choose Live to see the worktree now.' : ''}</p>
          </Empty>
        ) : files.length === 0 ? (
          <Empty title="No changes" icon={FileDiff}>
            <p>No changes.</p>
          </Empty>
        ) : (
          <div mix={[split, bodyCss]}>
            <ul aria-label="Changed files" mix={sideList}>
              {files.map((f) => {
                const p = splitPath(f.path);
                return (
                  <li key={f.path}>
                    <button
                      type="button"
                      aria-current={f === current ? 'true' : undefined}
                      title={f.oldPath ? `${f.oldPath} → ${f.path}` : f.path}
                      mix={on('click', () => select(f.path))}
                    >
                      <span mix={statusCss} aria-label={f.status} style={{ color: STATUS_COLOR[f.status] }}>
                        {STATUS_LETTER[f.status]}
                      </span>
                      <span mix={ellipsis}>
                        <span mix={fileName}>{p.name}</span>
                        {p.dir ? <span mix={fileDir}>{p.dir}</span> : null}
                        <span class="sr-only"> {f.path}</span>
                      </span>
                      <span mix={countsCss}>
                        {f.binary ? (
                          <span mix={binCss}>binary</span>
                        ) : (
                          <>
                            <span mix={addCss}>+{f.additions}</span>
                            <span mix={delCss}>-{f.deletions}</span>
                          </>
                        )}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
            {current ? <FilePane key={`${live}:${current.path}`} taskId={taskId} file={current} live={live} sideBySide={side} /> : null}
          </div>
        )}
      </div>
    );
  };
}

function FilePane(handle: Handle<{ taskId: string; file: DiffFile; live: boolean; sideBySide: boolean }>) {
  const s = services(handle);
  let force = false;
  const c = query<DiffFileContent>(handle, {
    load: () => s.client.taskDiffFile(handle.props.taskId, handle.props.file.path, handle.props.live, force),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => [handle.props.taskId, handle.props.file.path, handle.props.live, force],
    debounceMs: 300,
  });

  return () => {
    const { file, sideBySide } = handle.props;
    const v = c.data;
    return (
      <div mix={pane} aria-label={`Changes in ${file.path}`}>
        <div mix={paneHead}>
          <strong title={file.path}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</strong>
          <span>{file.status}</span>
        </div>
        {c.error ? (
          <div mix={errorSlot}>
            <ErrorNote error={c.error} what="Could not load this file" />
          </div>
        ) : null}
        {!v ? (
          c.loading ? <p mix={paneNote}>Loading...</p> : null
        ) : v.binary ? (
          <p mix={paneNote}>Binary file: not shown.</p>
        ) : v.tooLarge ? (
          <p mix={largeCss}>
            <span>This file is {formatSize(v.size ?? 0)}, too large to show by default.</span>
            <Button
              size="sm"
              mix={on('click', () => {
                force = true;
                void handle.update();
              })}
            >
              Show it anyway
            </Button>
          </p>
        ) : (
          <>
            {v.partial ? (
              <div mix={noteSlot}>
                <Notice tone="warn" testId="diff-partial">
                  Git no longer has these commits, so only the changed lines and their context are shown (gaps are marked ⋯).
                </Notice>
              </div>
            ) : null}
            <DiffEditorHost path={v.path} original={v.original} modified={v.modified} sideBySide={sideBySide} />
          </>
        )}
      </div>
    );
  };
}

/** The diff editor, created in a ref on an EMPTY host element and disposed when the element goes away. */
function DiffEditorHost(handle: Handle<{ path: string; original: string; modified: string; sideBySide: boolean }>) {
  let view: DiffEditorView | undefined;
  let loading = true;
  let error: unknown;
  let shown = { path: '', original: '', modified: '', sideBySide: true };

  const sync = () => {
    if (!view) return;
    const p = handle.props;
    if (p.path !== shown.path || p.original !== shown.original || p.modified !== shown.modified) view.setContent(p.path, p.original, p.modified);
    if (p.sideBySide !== shown.sideBySide) view.setSideBySide(p.sideBySide);
    shown = { path: p.path, original: p.original, modified: p.modified, sideBySide: p.sideBySide };
  };

  const mount = (node: HTMLElement, signal: AbortSignal) => {
    loadDiffEditor().then(
      (create) => {
        if (signal.aborted) return;
        const p = handle.props;
        const v = create(node, { path: p.path, original: p.original, modified: p.modified, sideBySide: p.sideBySide });
        shown = { path: p.path, original: p.original, modified: p.modified, sideBySide: p.sideBySide };
        view = v;
        signal.addEventListener('abort', () => {
          v.dispose();
          if (view === v) view = undefined;
        });
        loading = false;
        void handle.update();
      },
      (e: unknown) => {
        if (signal.aborted) return;
        error = e;
        loading = false;
        void handle.update();
      },
    );
  };

  return () => {
    handle.queueTask(sync);
    return (
      <div mix={hostWrap}>
        <div mix={[hostCss, ref(mount)]} data-testid="monaco-diff" data-own-keys="" />
        {loading ? <p mix={hostLoading}>Loading the diff editor...</p> : null}
        {error ? (
          <div mix={[hostLoading, hostError]}>
            <ErrorNote error={error} what="Could not start the diff editor" />
          </div>
        ) : null}
      </div>
    );
  };
}

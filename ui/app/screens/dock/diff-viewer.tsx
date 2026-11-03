// A task's changes, file by file, in Monaco's read-only diff editor (docs/ui.md 6.1). "Recorded" is the diff of the last
// finished attempt; "Live" is the worktree as it is now, while an agent works. Used in the dock and on the task page.
// While an agent works on the task the viewer shows Live, and Recorded once the run stops, until the person picks one (X21);
// that pick and the file are remembered per task (UiModel.dockView, X22). A row of Work's file list opens it on that file
// (ui.openDiff, X212). "Raw" shows the recorded patch as text lines, read for the exact diff evidence the list is of (L36).

import { css, on, ref, type Handle } from 'remix/component';
import { ChevronDown, ChevronUp, Columns2, FileCode, FileDiff, Rows2 } from 'lucide';
import type { DiffFile, DiffFileContent, DiffFiles } from '../../api/dockClient.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { joinPath, OpenElsewhere } from '../shared/open-elsewhere.tsx';
import { DiffView } from '../tasks/raw-diff.tsx';
import { DiffTotals, FileRowContent } from './diff-files.tsx';
import { formatSize } from './dock-format.ts';
import { errorSlot, pane, paneHead, paneNote, panel, segmented, sideList, split, toolbar, toolbarNote } from './dock-ui.tsx';
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

// On a page the diff gets room of its own; inside the dock it fits the dock's height.
const bodyCss = css({ minHeight: '320px', ':is(#dock-panel) &': { minHeight: '0' } });
const paneAction = css({ marginLeft: 'auto', display: 'inline-flex', flex: 'none' });
const rawCss = css({ flex: '1', minHeight: '0', overflow: 'auto', padding: t.s3 });
const totalsCss = css({ marginLeft: 'auto', display: 'inline-flex', gap: t.s2, alignItems: 'center', color: t.textMuted, fontFamily: t.mono, fontSize: t.textXs });
const hostWrap = css({ position: 'relative', flex: '1', minHeight: '300px', display: 'flex', ':is(#dock-panel) &': { minHeight: '0' } });
const hostCss = css({ flex: '1', minWidth: '0', minHeight: '0' });
const hostLoading = css({ position: 'absolute', inset: '0', margin: '0', padding: t.s3, color: t.textMuted, fontSize: t.textMd, pointerEvents: 'none' });
const hostError = css({ pointerEvents: 'auto' });
const largeCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap', margin: '0', padding: t.s3, fontSize: t.textMd });
const noteSlot = css({ padding: `${t.s2} ${t.s3} 0` });

export function DiffViewer(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  watch(handle, s.ui);
  // a file another page asked for (ui.openDiff) is the remembered one when the viewer mounts; taken here so it is not taken later
  s.ui.takeDiffAsk(handle.props.taskId);
  const view = s.ui.dockView(handle.props.taskId);
  // the person's pick of Live or Recorded; undefined follows the run (Live while an agent works)
  let choice = view.live;
  let live = choice ?? false;
  let side = loadSide();
  // the recorded patch as text instead of the editor (not remembered: it is only there for a recorded diff)
  let raw = false;
  let selected = view.file;
  const list = query<DiffFiles>(handle, {
    load: () => s.client.taskDiffFiles(handle.props.taskId, live),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => [handle.props.taskId, live],
    debounceMs: 300,
  });
  const setLive = (v: boolean) => {
    live = v;
    choice = v;
    s.ui.setDockView(handle.props.taskId, { live: v });
    void handle.update();
  };
  const setSide = (v: boolean) => {
    side = v;
    raw = false;
    saveSide(v);
    void handle.update();
  };
  const select = (path: string) => {
    selected = path;
    s.ui.setDockView(handle.props.taskId, { file: path });
    void handle.update();
  };

  return () => {
    const taskId = handle.props.taskId;
    // a row of Work's file list (X212) while the viewer shows: that file, in the editor
    const asked = s.ui.takeDiffAsk(taskId);
    if (asked) {
      selected = asked;
      raw = false;
    }
    // Live while an agent works (or when the person chose it) and the worktree is there; Recorded otherwise. Changing it reads
    // the other diff: the read of this one starts again (list.data is empty until it answers).
    if (list.data) {
      const want = list.data.liveAvailable && (choice ?? !!list.data.runActive);
      if (want !== live) live = want;
    }
    const d = list.data;
    if (!d && list.error) return <div mix={noteSlot}><ErrorNote error={list.error} retry={list.reload} what="Could not load the diff" /></div>;
    if (!d) return <Loading label="Loading the diff..." />;
    const files = d.files;
    const current = files.find((f) => f.path === selected) ?? files[0];
    const idx = current ? files.indexOf(current) : -1;
    // the raw patch is of a recorded diff's evidence: there is none for the live worktree
    const rawAvailable = !live && d.source === 'recorded' && d.diffId !== undefined;
    // once the view leaves the recorded diff (an agent works again), Raw is not chosen any more: coming back to it shows the
    // editor, and a newer patch is read only when the person asks (L36)
    if (raw && !rawAvailable) raw = false;
    const showRaw = raw;

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
            <button type="button" aria-pressed={!showRaw && side} mix={on('click', () => setSide(true))}>
              <Icon icon={Columns2} size={13} />
              Side by side
            </button>
            <button type="button" aria-pressed={!showRaw && !side} mix={on('click', () => setSide(false))}>
              <Icon icon={Rows2} size={13} />
              Inline
            </button>
            <button
              type="button"
              aria-pressed={showRaw}
              disabled={!rawAvailable}
              title={rawAvailable ? 'The recorded patch as text' : 'The raw patch is of the recorded diff'}
              mix={on('click', () => {
                raw = true;
                void handle.update();
              })}
            >
              <Icon icon={FileCode} size={13} />
              Raw
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
            <span mix={totalsCss}>
              <DiffTotals files={files} />
            </span>
          ) : null}
        </div>
        {list.error ? (
          <div mix={errorSlot}>
            <ErrorNote error={list.error} retry={list.reload} what="Could not refresh the diff" />
          </div>
        ) : null}
        {d.source === 'none' ? (
          <Empty title="No diff yet" icon={FileDiff}>
            <p>No diff has been recorded yet.</p>
            {d.liveAvailable ? (
              <Button size="sm" mix={on('click', () => setLive(true))}>
                Show the worktree now
              </Button>
            ) : null}
          </Empty>
        ) : files.length === 0 ? (
          <Empty title="No changes" icon={FileDiff}>
            <p>No changes.</p>
          </Empty>
        ) : (
          <div mix={[split, bodyCss]}>
            <ul aria-label="Changed files" mix={sideList}>
              {files.map((f) => (
                <li key={f.path}>
                  <button
                    type="button"
                    aria-current={!showRaw && f === current ? 'true' : undefined}
                    title={f.oldPath ? `${f.oldPath} → ${f.path}` : f.path}
                    mix={on('click', () => {
                      raw = false;
                      select(f.path);
                    })}
                  >
                    <FileRowContent file={f} />
                  </button>
                </li>
              ))}
            </ul>
            {showRaw ? (
              <div mix={[pane, rawCss]} aria-label="The raw patch" data-testid="diff-raw">
                <DiffView key={taskId} taskId={taskId} diffId={d.diffId} auto />
              </div>
            ) : current ? (
              <FilePane key={`${live}:${current.path}`} taskId={taskId} file={current} live={live} sideBySide={side} worktree={d.worktree} />
            ) : null}
          </div>
        )}
      </div>
    );
  };
}

function FilePane(handle: Handle<{ taskId: string; file: DiffFile; live: boolean; sideBySide: boolean; worktree?: string | undefined }>) {
  const s = services(handle);
  let force = false;
  const c = query<DiffFileContent>(handle, {
    load: () => s.client.taskDiffFile(handle.props.taskId, handle.props.file.path, handle.props.live, force),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => [handle.props.taskId, handle.props.file.path, handle.props.live, force],
    debounceMs: 300,
  });

  return () => {
    const { file, sideBySide, worktree, taskId } = handle.props;
    const v = c.data;
    return (
      <div mix={pane} aria-label={`Changes in ${file.path}`}>
        <div mix={paneHead}>
          <strong title={file.path}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</strong>
          <span>{file.status}</span>
          {/* the file in the worktree, outside Drydock (X26): there only while the worktree is, and not for a deleted file */}
          {worktree && file.status !== 'deleted' ? (
            <span mix={paneAction}>
              <OpenElsewhere where={{ taskId, path: file.path }} fullPath={joinPath(worktree, file.path)} label={`More ways to open ${file.path}`} testId="diff-file-open" />
            </span>
          ) : null}
        </div>
        {c.error ? (
          <div mix={errorSlot}>
            <ErrorNote error={c.error} retry={c.reload} what="Could not load this file" />
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

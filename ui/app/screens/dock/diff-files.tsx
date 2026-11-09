// A task's changed files as rows (status letter, name, folder, +/- counts), shared by the dock's Diff viewer and the task's Diff
// section in Work (effort review X212): Work lists the files with their counts, without Monaco, and a row opens the dock's viewer
// on that file (ui.openDiff).

import { css, on, type Handle } from 'remix/component';
import type { DiffFile, DiffFiles } from '../../api/dockClient.ts';
import { query, services } from '../../state/services.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { diffTotals, splitPath, STATUS_LETTER } from './dock-format.ts';
import { ellipsis } from './dock-ui.tsx';

export const STATUS_COLOR: Record<DiffFile['status'], string> = { added: t.success, deleted: t.danger, modified: t.warning, renamed: t.info };

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
export const addCss = css({ color: t.success });
export const delCss = css({ color: t.danger });
const binCss = css({ color: t.textFaint });
const totalsCss = css({ display: 'inline-flex', gap: t.s2 });

/** What a file's row shows: its status letter, its name and folder, and its counts (or "binary"). */
export function FileRowContent(handle: Handle<{ file: DiffFile }>) {
  return () => {
    const f = handle.props.file;
    const p = splitPath(f.path);
    return (
      <>
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
      </>
    );
  };
}

/** "3 files +12 -4", read out in words. */
export function DiffTotals(handle: Handle<{ files: readonly DiffFile[] }>) {
  return () => {
    const totals = diffTotals(handle.props.files);
    return (
      <span mix={totalsCss} aria-label={`${totals.files} files changed, ${totals.additions} additions, ${totals.deletions} deletions`} data-testid="diff-totals">
        <span>{totals.files === 1 ? '1 file' : `${totals.files} files`}</span>
        <span mix={addCss}>+{totals.additions}</span>
        <span mix={delCss}>-{totals.deletions}</span>
      </span>
    );
  };
}

const summaryCss = css({
  display: 'grid',
  gap: t.s2,
  '& > .totals': { display: 'flex', flexWrap: 'wrap', gap: t.s3, margin: '0', color: t.textMuted, fontSize: t.textXs, '& > span:first-child': { fontFamily: t.mono } },
  '& ul': { display: 'grid', margin: '0', padding: '0', listStyle: 'none', border: `1px solid ${t.border}`, borderRadius: t.rLg, overflow: 'hidden' },
  '& li + li': { borderTop: `1px solid ${t.border}` },
  '& li > button': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    width: '100%',
    minHeight: '32px',
    padding: `0 ${t.s3}`,
    border: '0',
    borderRadius: '0',
    background: 'transparent',
    color: t.text,
    font: 'inherit',
    textAlign: 'left',
    cursor: 'pointer',
  },
  '& li > button:hover': { background: t.bgHover },
});

/**
 * The task's changed files with their counts (Work's Diff section, X212): the diff the dock would open on (Live while an agent
 * works, Recorded otherwise, or the person's pick), each row opening the dock's viewer on its file.
 */
export function DiffSummary(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  const choice = s.ui.dockView(handle.props.taskId).live;
  let live = choice ?? false;
  const list = query<DiffFiles>(handle, {
    load: () => s.client.taskDiffFiles(handle.props.taskId, live),
    when: (e) => e.taskId === handle.props.taskId,
    key: () => [handle.props.taskId, live],
    debounceMs: 300,
  });

  return () => {
    const taskId = handle.props.taskId;
    const d = list.data;
    if (d) {
      const want = d.liveAvailable && (choice ?? !!d.runActive);
      if (want !== live) live = want; // the query reads the other diff
    }
    if (!d && list.error) return <ErrorNote error={list.error} retry={list.reload} what="Could not load the diff" />;
    if (!d) return <Loading label="Loading the diff..." />;
    if (d.source === 'none') return <p mix={muted}>No diff has been recorded yet.</p>;
    if (d.files.length === 0) return <p mix={muted}>No changes.</p>;
    return (
      <div mix={summaryCss} data-testid="diff-summary">
        <p class="totals">
          <DiffTotals files={d.files} />
          <span>{d.source === 'live' ? 'Live worktree' : 'Recorded with the last attempt'}</span>
        </p>
        <ErrorNote error={list.error} retry={list.reload} what="Could not refresh the diff" />
        <ul aria-label="Changed files">
          {d.files.map((f) => (
            <li key={f.path}>
              <button type="button" title={`Open ${f.path} in the dock`} mix={on('click', () => s.ui.openDiff(taskId, f.path))}>
                <FileRowContent file={f} />
              </button>
            </li>
          ))}
        </ul>
      </div>
    );
  };
}

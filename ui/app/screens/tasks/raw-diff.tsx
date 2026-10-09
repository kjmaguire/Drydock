// The raw patch of a task as plain text lines (never interpreted), loaded on request. The rich view is the dock's DiffViewer.

import { css, on, type Handle } from 'remix/component';
import { FileCode } from 'lucide';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { classifyDiffLine, DIFF_LINE_CAP, diffLines } from './tasks.ts';

const diffCss = css({
  margin: '0',
  padding: `${t.s2} 0`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  fontFamily: t.mono,
  fontSize: t.textSm,
  lineHeight: '1.55',
  overflow: 'auto',
  maxHeight: '560px',
  '&:focus-visible': { outline: 'none', boxShadow: t.focus },
  '& .diff-line': { padding: `0 ${t.s3}`, whiteSpace: 'pre', minWidth: 'max-content' },
  '& .diff-add': { background: t.successSoft, color: t.text },
  '& .diff-del': { background: t.dangerSoft, color: t.text },
  '& .diff-hunk': { color: t.info, background: t.infoSoft },
  '& .diff-meta': { color: t.textMuted, fontWeight: '600' },
  '& .diff-ctx': { color: t.textMuted },
  '& > button': { margin: `${t.s2} ${t.s3} 0` },
});

/** A unified diff as text lines, each classed by kind (diff-add, diff-del, diff-hunk, diff-meta, diff-ctx). */
export function UnifiedDiff(handle: Handle<{ text: string }>) {
  let all = false;
  return () => {
    const lines = diffLines(handle.props.text);
    const shown = all ? lines : lines.slice(0, DIFF_LINE_CAP);
    return (
      <div mix={diffCss} role="region" aria-label="Unified diff" tabIndex={0} data-testid="diff">
        {shown.map((l, i) => (
          <div key={i} class={`diff-line diff-${classifyDiffLine(l)}`}>
            {l || ' '}
          </div>
        ))}
        {lines.length > shown.length ? (
          <Button
            size="sm"
            mix={on('click', () => {
              all = true;
              void handle.update();
            })}
          >
            Show all {lines.length} lines
          </Button>
        ) : null}
      </div>
    );
  };
}

/** "Show the raw patch": reads task.diff once on request. Says so when no diff was recorded or it is empty. */
export function DiffView(handle: Handle<{ taskId: string; hasDiff: boolean }>) {
  const s = services(handle);
  const act = action(handle);
  let text: string | undefined;
  return () => {
    const { taskId, hasDiff } = handle.props;
    if (!hasDiff && text === undefined) return <p mix={muted}>No diff has been recorded yet.</p>;
    return (
      <div style={{ display: 'grid', gap: '8px' }}>
        {text === undefined ? (
          <div>
            <Button
              icon={FileCode}
              disabled={act.busy !== ''}
              mix={on('click', async () => {
                const r = await act.run('load', () => s.client.taskDiff(taskId));
                if (r !== undefined) {
                  text = r;
                  void handle.update();
                }
              })}
            >
              Show the raw patch
            </Button>
          </div>
        ) : text === '' ? (
          <p mix={muted}>The recorded diff is empty.</p>
        ) : (
          <UnifiedDiff text={text} />
        )}
        <ErrorNote error={act.error} what="Could not load the diff" />
      </div>
    );
  };
}

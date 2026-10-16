// The raw patch of a task as plain text lines (never interpreted), loaded on request. The rich view is the dock's DiffViewer.
//
// The patch on the page is the one the task's card is about (audit 2026-10-16, L36). DiffView is keyed on the id of the task's newest
// diff evidence and asks the core for exactly that one. A newer patch is recorded when the task goes round again, and the Approve
// button beside this one acts on the newest work: so when the id changes the text it held is dropped and the page says so. The lines
// are never swapped under the reader's eyes.
//
// A long patch is drawn as a window of its lines (L37): the first DIFF_LINE_CAP lines are in the page as they always were, and
// "Show all" turns the whole patch into a scroller that keeps only the lines near the view in the page. In that view Select all and
// Copy take the lines drawn, not the whole patch.

import { css, on, ref, type Handle } from 'remix/component';
import { FileCode } from 'lucide';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { listOf, WindowedList } from '../../ui/windowed-list.tsx';
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

/** One line of a patch, classed by kind. */
const lineOf = (l: string) => (
  <div class={`diff-line diff-${classifyDiffLine(l)}`}>{l || ' '}</div>
);

/** A unified diff as text lines, each classed by kind (diff-add, diff-del, diff-hunk, diff-meta, diff-ctx). */
export function UnifiedDiff(handle: Handle<{ text: string }>) {
  let all = false;
  // the lines of the text, split once per text: a patch can have tens of thousands
  let splitOf: string | undefined;
  let lines: string[] = [];
  let root: HTMLElement | undefined;
  return () => {
    const text = handle.props.text;
    if (splitOf !== text) {
      splitOf = text;
      lines = diffLines(text);
    }
    return (
      <div mix={ref((n: Element) => (root = n as HTMLElement))} style={{ display: 'contents' }}>
        {all ? (
          <WindowedList
            {...listOf(lines, (_, i) => String(i), (l) => lineOf(l))}
            label="Unified diff"
            role="region"
            testId="diff"
            maxHeight={560}
            estimate={20}
            plainUpTo={0}
            mix={[diffCss]}
          />
        ) : (
          <div mix={diffCss} role="region" aria-label="Unified diff" tabIndex={0} data-testid="diff" data-windowed="false">
            {lines.slice(0, DIFF_LINE_CAP).map((l, i) => (
              <div key={i} class={`diff-line diff-${classifyDiffLine(l)}`}>
                {l || ' '}
              </div>
            ))}
            {lines.length > DIFF_LINE_CAP ? (
              <Button
                size="sm"
                mix={on('click', () => {
                  all = true;
                  void handle.update();
                  // the button that was pressed is gone with the view it was in: the keyboard goes to the new one
                  handle.queueTask(() => root?.querySelector<HTMLElement>('[data-testid="diff"]')?.focus());
                })}
              >
                Show all {lines.length} lines
              </Button>
            ) : null}
          </div>
        )}
      </div>
    );
  };
}

/**
 * "Show the raw patch": reads task.diff once on request, for the task's newest diff evidence (`diffId`); offered only when a diff
 * was recorded. When a newer patch is recorded the text shown is dropped and the person is told, never given the newer text
 * without asking.
 */
export function DiffView(handle: Handle<{ taskId: string; diffId: number | undefined }>) {
  const s = services(handle);
  const act = action(handle);
  let text: string | undefined;
  /** The evidence the text is of. */
  let shownId: number | undefined;
  /** The patch changed since the person opened it. */
  let changed = false;

  const show = async () => {
    const { taskId, diffId } = handle.props;
    if (diffId === undefined) return;
    changed = false;
    const r = await act.run('load', () => s.client.taskDiff(taskId, diffId));
    if (r === undefined) return;
    // The text is of the evidence that was asked for. If a newer patch was recorded while it was read, the render below sees that
    // the text is not of the card's evidence and drops it.
    text = r;
    shownId = diffId;
    void handle.update();
  };

  return () => {
    const { diffId } = handle.props;
    if (text !== undefined && shownId !== diffId) {
      text = undefined;
      shownId = undefined;
      changed = true;
    }
    // with no diff recorded the diff viewer above already says so; there is no raw patch to offer
    if (diffId === undefined) return null;
    return (
      <div style={{ display: 'grid', gap: '8px' }}>
        {text === undefined ? (
          <div style={{ display: 'grid', gap: '8px', justifyItems: 'start' }}>
            {changed ? (
              <p mix={muted} role="status" data-testid="raw-diff-changed" style={{ margin: '0' }}>
                The patch changed since you opened it. Show it again.
              </p>
            ) : null}
            <Button icon={FileCode} disabled={act.busy !== ''} mix={on('click', () => void show())}>
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

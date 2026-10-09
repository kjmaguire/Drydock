// Start or stop the project's preview of the task result. Starting runs the project's preview command in the task worktree.

import { css, on, type Handle } from 'remix/component';
import { MonitorPlay, Square } from 'lucide';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { card } from '../../ui/layout.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

const boxCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap', '& > p': { flex: '1', minWidth: '220px', margin: '0' } });

export function PreviewControls(handle: Handle<{ taskId: string; hasWorktree: boolean }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.previewList(),
    key: () => handle.props.taskId,
    when: (e) => e.type.startsWith('preview.') || e.type.startsWith('task.'),
  });
  return () => {
    const { taskId, hasWorktree } = handle.props;
    const ro = s.workspace.license;
    const mine = (q.data ?? []).find((p) => p.taskId === taskId);
    const href = mine ? safeHref(mine.url) : undefined;
    if (!hasWorktree && !mine) return null;
    return (
      <section aria-label="Preview" style={{ display: 'grid', gap: '12px' }}>
        <h2 style={{ fontSize: 'var(--text-md)', fontWeight: '600' }}>Preview</h2>
        <ErrorNote error={act.error ?? q.error} what="Preview" />
        <div mix={[card, boxCss]}>
          {mine ? (
            <p data-testid="preview-running">
              Running (pid {mine.pid}):{' '}
              {href ? (
                <a href={href} target="_blank" rel="noopener noreferrer" title={href} data-rmx-document>
                  {href}
                </a>
              ) : (
                mine.url
              )}
            </p>
          ) : (
            <p mix={muted}>No preview is running. Starting one runs the project&apos;s preview command in this task&apos;s worktree (set it under Project settings).</p>
          )}
          {mine ? (
            <Button
              icon={Square}
              disabled={act.busy !== ''}
              mix={on('click', async () => {
                await act.run('stop', () => s.client.previewStop(taskId));
                q.reload();
              })}
            >
              Stop preview
            </Button>
          ) : (
            <Button
              icon={MonitorPlay}
              disabled={act.busy !== '' || ro.readOnly}
              title={ro.readOnly ? ro.reason : undefined}
              mix={on('click', async () => {
                await act.run('start', () => s.client.previewStart(taskId));
                q.reload();
              })}
            >
              Start preview
            </Button>
          )}
        </div>
      </section>
    );
  };
}

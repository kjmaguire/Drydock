// Start or stop the project's preview of the task result. Starting runs the project's preview command in the task worktree; a
// running preview is opened with "Open the app" (in the person's browser: it runs on this machine, not in a sandbox). In a
// sandboxed project the core refuses to run it here, so the part is not offered (the Sandbox app part opens the app there).

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, MonitorPlay, Square } from 'lucide';
import { routes } from '../../routes.ts';
import { action, query, retryFor, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Icon } from '../../ui/icon.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { card } from '../../ui/layout.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

const boxCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap', '& > p': { flex: '1', minWidth: '220px', margin: '0' } });

export function PreviewControls(handle: Handle<{ taskId: string; hasWorktree: boolean; sandboxed?: boolean }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const q = query(handle, {
    load: () => s.client.previewList(),
    key: () => handle.props.taskId,
    when: (e) => e.type.startsWith('preview.') || e.type.startsWith('task.'),
  });
  return () => {
    const { taskId, hasWorktree, sandboxed } = handle.props;
    const ro = s.workspace.license;
    const mine = (q.data ?? []).find((p) => p.taskId === taskId);
    const href = mine ? safeHref(mine.url) : undefined;
    // a preview left running from before the sandbox was switched on is still shown, so it can be stopped
    if ((!hasWorktree || sandboxed) && !mine) return null;
    return (
      <section aria-label="Preview" style={{ display: 'grid', gap: '12px' }}>
        <h2 style={{ fontSize: 'var(--text-md)', fontWeight: '600' }}>Preview</h2>
        <ErrorNote error={act.error ?? q.error} retry={retryFor(act, q)} what="Preview" />
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
            <p mix={muted}>
              No preview is running. Starting one runs the project&apos;s preview command in this task&apos;s worktree (set it under{' '}
              <a href={routes.project.href()} data-testid="preview-settings">
                Project settings
              </a>
              ).
            </p>
          )}
          {href ? (
            <a href={href} target="_blank" rel="noopener noreferrer" data-rmx-document mix={buttonStyle('primary')} data-testid="preview-open">
              <Icon icon={ExternalLink} size={14} />
              Open the app
            </a>
          ) : null}
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

// The notice a screen shows while the project's task list it presents is not every task (audit 2026-10-16, M21). task.list
// returns only the newest TASK_LIST_LIMIT tasks; the project's shared read (state/project.ts) also asks task.count, and this
// says "Showing the newest 500 of 612 tasks" wherever the list stands for the whole: the board, the pull request picker, the
// pickers that link a task. It renders nothing while the screen has every task, and it does not go away by itself or on a click.

import type { Handle, RemixNode } from 'remix/component';
import { taskNoticeText } from '../../lib/task-total.ts';
import { services, watch } from '../../state/services.tsx';
import { Notice } from '../../ui/feedback.tsx';

/** `children` says what the cut means on this screen ("Older tasks are not on this board."). */
export function TaskTotalNotice(handle: Handle<{ children?: RemixNode }>) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const c = s.project.taskCoverage;
    if (!c.partial) return null;
    return (
      <Notice tone="warn" title={`${taskNoticeText(c)}.`} testId="task-total-notice">
        {handle.props.children}
      </Notice>
    );
  };
}

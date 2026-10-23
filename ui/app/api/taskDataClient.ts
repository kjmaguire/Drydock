// The task data of the board, the inbox and the crew (decision P97): task summaries without a task.get per card, a task's labels
// and a project's key (internal/core/ext_tasksummary.go, rpc.go, projectkey.go). Like the rest of the client: one function per
// method in the core registry, Go's nil slices (JSON null) turned into empty arrays.

import type { Transport } from './transport.ts';
import type { Project, SummaryCheck, Task, TaskSummaries, TaskSummary, TaskSummaryReq } from './types.ts';

export interface TaskDataClient {
  /**
   * One row per open task and per task that ended since `finishedSince` (default: a week ago), or per task in `ids` whatever its
   * state, newest first: at most TASK_LIST_LIMIT (or `limit`); `total` says how many match and `partial` that the list is cut.
   */
  taskSummaries(req: TaskSummaryReq): Promise<TaskSummaries>;
  /** Replaces a task's labels (trimmed, lower-cased, deduplicated; more than 8 or one over 24 characters is refused). */
  taskLabelsSet(id: string, labels: string[]): Promise<Task>;
  /** Changes a project's key (2 to 5 letters or digits, upper-cased; another project's key is refused). */
  projectSetKey(id: string, key: string): Promise<Project>;
}

const arr = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : []);

const check = (c: SummaryCheck): SummaryCheck => ('failing' in c ? { ...c, failing: arr(c.failing) } : c);
const summary = (s: TaskSummary): TaskSummary => ({ ...s, labels: arr(s.labels), checks: arr(s.checks).map(check) });

export function createTaskDataClient(t: Transport): TaskDataClient {
  return {
    taskSummaries: async ({ projectId, ids, finishedSince, limit }) => {
      const r = await t.call<TaskSummaries | null>('task.summaries', { projectId, ids: ids ?? [], finishedSince: finishedSince ?? 0, limit: limit ?? 0 });
      return { summaries: arr(r?.summaries).map(summary), total: Number(r?.total) || 0, partial: r?.partial === true };
    },
    taskLabelsSet: async (id, labels) => {
      const tk = await t.call<Task>('task.labels.set', { id, labels });
      return { ...tk, labels: arr(tk.labels) };
    },
    projectSetKey: (id, key) => t.call('project.update', { id, key }),
  };
}

// A task's model routing level (decision P127, docs/model-routing.md s3): the task follows the level its template declares,
// else its project's, else the workspace's, until a person chooses its own here. The most specific wins, and each decision's
// "Why this model?" says which one applied. Saved as soon as it is chosen.

import type { Handle } from 'remix/component';
import type { RouteLevel } from '../../api/extTypes.ts';
import { action, query, services } from '../../state/services.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Select } from '../../ui/select.tsx';
import { taskLevelOptions, taskLevelSentence } from '../routing/routing.ts';

export function TaskRoutingLevel(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  const q = query(handle, {
    load: (): Promise<RouteLevel> => s.client.routeLevelTaskGet(handle.props.taskId),
    key: () => handle.props.taskId,
    when: (e) => e.type === 'routing.level_changed',
  });
  const act = action(handle);
  const choose = async (level: string) => {
    const r = await act.run('set', () => s.client.routeLevelTaskSet(handle.props.taskId, level));
    if (r) q.set(r);
  };
  return () => {
    const rl = q.data;
    // a level that cannot be read is said quietly: the rest of the panel matters more than this row
    if (!rl) return q.error ? <span title={q.error.message}>not known</span> : null;
    const sentence = taskLevelSentence(rl);
    return (
      <span style={{ display: 'grid', gap: '4px', minWidth: '0', width: '100%' }} data-testid="task-routing-level" data-source={rl.source} title={sentence}>
        <Select id={`task-level-${handle.id}`} label="Routing level" size="sm" value={rl.task ?? ''} disabled={!!act.busy} options={taskLevelOptions(rl)} onChange={(v) => void choose(v)} />
        <span class="sr-only" data-testid="task-routing-level-now">
          {sentence}
        </span>
        {act.error ? <ErrorNote error={act.error} what="Not changed" /> : null}
      </span>
    );
  };
}

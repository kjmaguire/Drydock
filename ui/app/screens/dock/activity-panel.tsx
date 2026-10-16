// Model and token activity in the dock: the usage rows of the task in context, else the most recent ones.

import { css, type Handle } from 'remix/component';
import type { UsageRow } from '../../api/types.ts';
import { query, services } from '../../state/services.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { UsageTable } from '../shared/usage.tsx';

const wrap = css({ display: 'grid', gap: t.s3, padding: t.s3, alignContent: 'start' });
const head = css({ display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', color: t.textMuted, fontSize: t.textSm });
const count = css({ marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs });

export function ActivityPanel(handle: Handle<{ taskId: string | undefined }>) {
  const s = services(handle);
  const q = query<UsageRow[]>(handle, {
    load: () => s.client.usageList({ taskId: handle.props.taskId, limit: 200 }),
    when: (e) => e.type.startsWith('usage.') || (!!handle.props.taskId && e.taskId === handle.props.taskId),
    key: () => handle.props.taskId ?? null,
    debounceMs: 500,
  });

  return () => {
    const taskId = handle.props.taskId;
    const rows = q.data;
    return (
      <div mix={wrap} data-testid="activity-panel">
        <p mix={head}>
          <span>{taskId ? 'Usage of this task, newest first.' : 'The most recent usage across the workspace.'}</span>
          {rows && rows.length > 0 ? <span mix={count}>{rows.length === 1 ? '1 row' : `${rows.length} rows`}</span> : null}
        </p>
        <ErrorNote error={q.error} what="Could not load the usage" />
        {rows ? <UsageTable rows={rows} /> : q.loading ? <Loading label="Loading usage..." /> : null}
      </div>
    );
  };
}

import type { Agent, NeedsYouItem } from '../api/types';
import { agentName, formatDateTime } from '../lib/format';
import { useNeedsYou } from '../state/hooks';
import { Badge, Empty, ErrorNote, Page } from './common';

const KIND_LABEL: Record<string, string> = {
  approval: 'Approval',
  review: 'Review',
  question: 'Question',
  failed_check: 'Failed check',
  watchdog_pause: 'Watchdog pause',
};

/** Where clicking an item goes. Items are derived by the core on every query; nothing here is stored. */
export function needsTarget(it: NeedsYouItem): string {
  if (it.taskId) return `#/task/${encodeURIComponent(it.taskId)}`;
  if (it.roomId) return `#/room/${encodeURIComponent(it.roomId)}`;
  return '#/needs-you';
}

export function NeedsYouView({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const q = useNeedsYou(projectId);
  const items = q.data ?? [];
  return (
    <Page title="Needs you">
      <ErrorNote error={q.error} what="Could not load the inbox" />
      {q.loading && !q.data ? <p className="muted">Loading...</p> : null}
      {q.data && items.length === 0 ? (
        <Empty title="Nothing is waiting for you">
          <p>Approvals, reviews, questions, failed checks and watchdog pauses appear here as they happen.</p>
        </Empty>
      ) : null}
      <ul className="needs-list" aria-label="Items that need you">
        {items.map((it) => (
          <li key={it.key} data-testid="needs-item" data-kind={it.kind}>
            <a href={needsTarget(it)}>
              <Badge tone={it.severity <= 1 ? 'err' : 'warn'}>{KIND_LABEL[it.kind] ?? it.kind}</Badge> <strong>{it.title}</strong>
            </a>
            <div className="muted">
              {it.agentId ? `${agentName(it.agentId, agents)} \u00B7 ` : ''}
              {formatDateTime(it.createdAt)}
            </div>
            {it.detail ? <div className="muted">{it.detail}</div> : null}
          </li>
        ))}
      </ul>
    </Page>
  );
}

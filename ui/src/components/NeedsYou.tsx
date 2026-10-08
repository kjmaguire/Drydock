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
  // a tool call an agent wants to make: its exact arguments and the Approve button are on the MCP servers page
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return '#/mcp';
  if (it.taskId) return `#/task/${encodeURIComponent(it.taskId)}`;
  if (it.roomId) return `#/room/${encodeURIComponent(it.roomId)}`;
  // an approval with no task is an outbound write to an integration: its exact text is on the Integrations page
  if (it.kind === 'approval') return '#/integrations';
  return '#/needs-you';
}

/** A plain-language hint for the kinds of item that need more than a click. Derived from the title; the title is outside text, so it only selects a fixed hint. */
export function needsHint(it: NeedsYouItem): string {
  if (it.kind === 'approval' && /^Budget limit/i.test(it.title)) return 'A budget stopped the next agent turn. Open the task to raise the limit and resume, or stop the run.';
  if (it.kind === 'review' && /^Accept the spec/i.test(it.title)) return 'The spec gate: nothing is built until you accept the spec.';
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return 'An agent wants to call a tool that is not marked read-only. Open MCP servers to see the exact call and approve or reject it.';
  if (it.kind === 'approval' && !it.taskId && !it.roomId) return 'An outbound write to a connected service. Open Integrations to read the exact text and approve or decline.';
  return '';
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
            {needsHint(it) ? <div className="muted" data-testid="needs-hint">{needsHint(it)}</div> : null}
          </li>
        ))}
      </ul>
    </Page>
  );
}

// The details panel on the right (Slack's member list and thread pane): the project's agents, the task in context, an
// open thread, or the agent inspector. It collapses to a slim strip.

import { css, on, type Handle } from 'remix/component';
import { PanelRightClose, PanelRightOpen } from 'lucide';
import { agentName, formatCost, presenceFor } from '../lib/format.ts';
import { routes } from '../routes.ts';
import { query, services, watch } from '../state/services.tsx';
import { SkipsBadge } from '../screens/shared/skip-approvals.tsx';
import { AgentInspector } from '../screens/team/inspector.tsx';
import { leadOf, RunStandup } from '../screens/team/lead.tsx';
import { ThreadPanel } from '../screens/room/thread-panel.tsx';
import { IconButton } from '../ui/button.tsx';
import { Avatar, OriginBadge, PresenceDot, StatusChip, kv, presenceColor } from '../ui/data.tsx';
import { ErrorNote, Loading } from '../ui/feedback.tsx';
import { Tabs, type TabItem } from '../ui/tabs.tsx';
import { t } from '../ui/tokens.ts';
import type { DetailsPage, DetailsTab } from '../state/ui.ts';

const panelCss = css({
  display: 'flex',
  flexDirection: 'column',
  width: 'var(--details-width)',
  minHeight: '0',
  borderLeft: `1px solid ${t.border}`,
  background: t.bgSidebar,
});
const closedCss = css({
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  width: '44px',
  paddingTop: t.s2,
  borderLeft: `1px solid ${t.border}`,
  background: t.bgSidebar,
});
const headCss = css({ display: 'flex', alignItems: 'flex-end', gap: t.s1, height: 'var(--header-height)', padding: `0 ${t.s2} 0 ${t.s2}`, flex: 'none', '& > div:first-child': { flex: '1', minWidth: '0' } });
const bodyCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', padding: t.s3 });
const agentRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: `${t.s2} ${t.s2}`,
  borderRadius: t.rMd,
  '&:hover': { background: t.bgHover },
  '& .who': { flex: '1', minWidth: '0', display: 'grid', gap: '1px' },
  '& .name': { fontWeight: '600', fontSize: t.textMd, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .act': { color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});
const sectionLabel = css({ margin: `${t.s1} ${t.s2} ${t.s2}`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });

export function Details(handle: Handle<{ taskId: string | undefined; page: DetailsPage }>) {
  const s = services(handle);
  watch(handle, s.ui);
  watch(handle, s.project);
  return () => {
    const { taskId, page } = handle.props;
    const ui = s.ui;
    ui.detailsPage(page);
    if (!ui.detailsOpen) {
      return (
        <aside mix={closedCss} aria-label="Details">
          <IconButton icon={PanelRightOpen} label="Show details" aria-expanded={false} mix={on('click', () => ui.setDetails(true))} />
        </aside>
      );
    }
    const items: TabItem[] = [{ name: 'agents', label: 'Agents' }];
    if (taskId) items.push({ name: 'task', label: 'Task' });
    if (ui.thread) items.push({ name: 'thread', label: 'Thread' });
    if (ui.inspectId) items.push({ name: 'inspector', label: 'Inspector' });
    const tab: DetailsTab = items.some((i) => i.name === ui.tab) ? ui.tab : 'agents';
    const agents = s.project.agentList;
    const presence = s.project.presence.data ?? [];
    return (
      <aside mix={panelCss} aria-label="Details">
        <div mix={headCss}>
          <Tabs label="Details" items={items} active={tab} onChange={(n) => ui.setTab(n as DetailsTab)} />
          <IconButton icon={PanelRightClose} label="Hide details" mix={on('click', () => ui.setDetails(false))} />
        </div>
        <div mix={bodyCss} role="tabpanel" aria-label={items.find((i) => i.name === tab)?.label as string}>
          {tab === 'agents' ? (
            <div data-testid="details-agents">
              <p mix={sectionLabel}>Team · {agents.length}</p>
              {leadOf(agents) && s.workspace.projectId ? (
                <div style={{ padding: '0 8px 8px' }}>
                  <RunStandup projectId={s.workspace.projectId} lead={leadOf(agents)!} />
                </div>
              ) : null}
              {agents.length === 0 ? <p style={{ color: t.textMuted, padding: '0 8px' }}>No agents yet.</p> : null}
              {agents.map((a) => {
                const pr = presenceFor(a.id, presence);
                return (
                  <div key={a.id} mix={agentRow}>
                    <Avatar id={a.id} name={a.name} size={32} status={presenceColor(pr)} kind="agent" />
                    <div class="who">
                      <span class="name">
                        {a.name} <span style={{ color: t.textFaint, fontWeight: '400', fontSize: t.textSm }}>{a.role}</span>
                      </span>
                      <SkipsBadge mode={a.permissionMode} />
                      <PresenceDot presence={pr} />
                      {/* the activity, unless the presence line already says it ("Paused (stopped)" over "stopped") */}
                      {pr?.activity && pr.activity !== pr.label ? <span class="act">{pr.activity}</span> : null}
                    </div>
                    <button type="button" mix={[inspectBtn, on('click', () => ui.openInspector(a.id))]} aria-label={`Inspect ${a.name}`}>
                      Inspect
                    </button>
                  </div>
                );
              })}
            </div>
          ) : null}
          {tab === 'task' && taskId ? <TaskSummary key={taskId} taskId={taskId} /> : null}
          {tab === 'thread' && ui.thread ? <ThreadPanel key={`${ui.thread.roomId}:${ui.thread.rootId}`} roomId={ui.thread.roomId} rootId={ui.thread.rootId} onClose={() => ui.closeThread()} /> : null}
          {tab === 'inspector' ? <AgentInspector key={ui.inspectId ?? ''} agentId={ui.inspectId} /> : null}
        </div>
      </aside>
    );
  };
}

const inspectBtn = css({ minHeight: '24px', padding: '0 8px', fontSize: t.textSm, background: 'transparent', borderColor: t.border, color: t.textMuted, '&:hover': { color: t.text } });

function TaskSummary(handle: Handle<{ taskId: string }>) {
  const s = services(handle);
  watch(handle, s.project);
  const d = query(handle, {
    load: () => s.client.taskGet(handle.props.taskId),
    when: (e) => e.taskId === handle.props.taskId || (e.type.startsWith('approval.') && !e.taskId),
    debounceMs: 80,
  });
  return () => {
    const v = d.data;
    // a failed read shows why (with what it had before, if anything), never an endless "Loading..."
    if (!v) return d.error ? <ErrorNote error={d.error} what="Could not load the task" /> : <Loading />;
    const agents = s.project.agentList;
    const passed = v.evidence.filter((e) => e.passed === true).length;
    const failed = v.evidence.filter((e) => e.passed === false).length;
    return (
      <div style={{ display: 'grid', gap: '12px' }}>
        <a href={routes.task.href({ id: v.task.id })} style={{ fontWeight: '600', fontSize: t.textLg, color: t.text }}>
          #{v.task.seq} {v.task.title}
        </a>
        <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px' }}>
          <StatusChip status={v.task.status} />
          <OriginBadge origin={v.task.origin} />
        </div>
        <dl mix={kv}>
          <dt>Worker</dt>
          <dd>{agentName(v.task.assignee, agents) || 'unassigned'}</dd>
          <dt>Reviewer</dt>
          <dd>{agentName(v.task.reviewer, agents) || 'unassigned'}</dd>
          <dt>Evidence</dt>
          <dd>
            {passed} passed, {failed} failed
          </dd>
          <dt>Cost</dt>
          <dd>{formatCost(v.task.costUsd)}</dd>
        </dl>
      </div>
    );
  };
}

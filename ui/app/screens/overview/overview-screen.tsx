// Overview (Crew HQ, decision P64): the project's home. A greeting with the day's numbers, what needs you (the first few,
// each a link to where it is decided), the crew as dots with what each agent is doing right now, the tasks in motion, the
// rooms, and Insights: the spend against the budget, what merged this week, the first-check pass rate and the last security
// scan's findings, each a link to its page. Everything here is a projection of the core's reads; nothing is approved here.

import { css, type Handle } from 'remix/component';
import { ArrowRight, Inbox, LayoutDashboard, Sparkles } from 'lucide';
import type { Agent, NeedsYouItem, Task } from '../../api/types.ts';
import { agentName, presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { RoomLink } from '../../shell/hubnav.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Avatar, OriginBadge, StatusChip, presenceColor } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Meter } from '../budgets/meter.tsx';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { TaskTotalNotice } from '../shared/task-total.tsx';
import { findingsText } from '../security/security.ts';
import { KIND_LABEL, needsTarget } from '../needs-you/needs-you.ts';
import { DONE, WEEK_MS, WORKING, dayStart, doing, firstCheckText, greeting, mergedSince, spendLine } from './overview.ts';

export { doing, greeting };

const layoutCss = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 340px', gap: t.s6, alignItems: 'start', '@media (max-width: 1100px)': { gridTemplateColumns: 'minmax(0, 1fr)' } });
const colCss = css({ display: 'grid', gap: t.s6, minWidth: '0' });

const needsGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
const needCard = css({
  display: 'grid',
  gap: t.s3,
  padding: '18px',
  borderRadius: t.rXl,
  border: `1px solid ${t.mentionBorder}`,
  background: t.mentionBg,
  '& .kind': { fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.06em', textTransform: 'uppercase', color: t.warning },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0' },
  '& h3': { fontSize: t.textLg, fontWeight: '600', letterSpacing: '-0.01em', overflowWrap: 'anywhere' },
  '& p': { color: t.textMuted, fontSize: t.textSm, lineHeight: '1.5', overflowWrap: 'anywhere' },
  '& .acts': { display: 'flex', gap: t.s2, flexWrap: 'wrap' },
  '&[data-urgent="true"]': { borderColor: t.danger, background: t.dangerSoft },
  '&[data-urgent="true"] .kind': { color: t.danger },
});
const quietCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd });

const crewGrid = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(168px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });
const crewCard = css({
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  gap: '4px',
  padding: `${t.s5} ${t.s3} ${t.s3}`,
  borderRadius: t.rXl,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  color: t.text,
  textAlign: 'center',
  textDecoration: 'none',
  transition: `border-color ${t.fast} ${t.ease}, transform ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none', transform: 'translateY(-1px)' },
  '& .name': { marginTop: t.s2, fontSize: t.textMd, fontWeight: '600' },
  '& .role': { color: t.textFaint, fontSize: t.textSm },
  '& .act': {
    display: 'flex',
    alignItems: 'flex-start',
    gap: '7px',
    width: '100%',
    marginTop: t.s2,
    padding: `${t.s2} 10px`,
    borderRadius: t.rMd,
    background: t.bgSidebar,
    color: t.textMuted,
    fontSize: t.textSm,
    lineHeight: '1.4',
    textAlign: 'left',
    overflowWrap: 'anywhere',
  },
  '& .act > i': { flex: 'none', width: '7px', height: '7px', marginTop: '6px', borderRadius: '50%', display: 'block' },
});

const motionList = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const motionRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: `10px 14px`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  border: `1px solid ${t.border}`,
  color: t.text,
  textDecoration: 'none',
  minWidth: '0',
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none' },
  '& .seq': { color: t.textFaint, fontVariantNumeric: 'tabular-nums', flex: 'none' },
  '& .title': { flex: '1', minWidth: '0', fontWeight: '500', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

const statsCss = css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: t.s2 });
const statCss = css({
  padding: '12px 14px',
  borderRadius: t.rMd,
  background: t.bgSidebar,
  '& b': { display: 'block', fontSize: t.text2xl, fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.1' },
  '& small': { color: t.textFaint, fontSize: t.textXs },
});
const panelTitle = css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: t.s2, margin: `0 0 ${t.s3}`, fontSize: t.textMd, fontWeight: '600', '& span': { color: t.textFaint, fontSize: t.textSm, fontWeight: '500' } });
const roomsList = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const moreLink = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: t.textSm, fontWeight: '500', color: t.textMuted, '&:hover': { color: t.text } });

// Insights: a 2x2 of linked numbers, each with the words that say what it measures
const insightGrid = css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: t.s2 });
const insightCss = css({
  display: 'grid',
  gap: '3px',
  alignContent: 'start',
  padding: '12px 14px',
  borderRadius: t.rMd,
  background: t.bgSidebar,
  color: t.text,
  textDecoration: 'none',
  minWidth: '0',
  '&:hover': { background: t.bgHover, textDecoration: 'none' },
  '& small': { color: t.textFaint, fontSize: t.textXs, lineHeight: '1.35' },
  '& b': { display: 'block', fontSize: t.text2xl, fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.1', fontVariantNumeric: 'tabular-nums' },
  '& b[data-na]': { color: t.textFaint },
});

function NeedCard(handle: Handle<{ item: NeedsYouItem; agents: readonly Agent[] }>) {
  return () => {
    const { item: it, agents } = handle.props;
    const urgent = it.severity <= 1;
    return (
      <li mix={needCard} data-testid="overview-need" data-kind={it.kind} data-urgent={urgent ? 'true' : undefined}>
        <span class="kind">{KIND_LABEL[it.kind] ?? it.kind}</span>
        <div class="head">
          {it.agentId ? <Avatar id={it.agentId} name={agentName(it.agentId, agents)} size={40} kind="agent" status={t.warning} /> : <Icon icon={Inbox} size={22} />}
          <div style={{ minWidth: '0' }}>
            <h3>{it.title}</h3>
            {it.detail ? <p>{it.detail}</p> : null}
          </div>
        </div>
        <div class="acts">
          <a href={needsTarget(it)} mix={buttonStyle('primary')}>
            Open and decide
          </a>
        </div>
      </li>
    );
  };
}

export function OverviewScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const pid = () => s.workspace.projectId;
  const budget = query(handle, {
    load: () => s.client.budgetStatus({ projectId: pid()! }),
    when: (e) => e.type.startsWith('budget.') || e.type.startsWith('task.') || e.type === 'room.message_posted',
    key: pid,
    enabled: () => !!pid(),
  });
  const evals = query(handle, {
    load: () => s.client.evalsReport({ projectId: pid()!, sinceMs: Date.now() - WEEK_MS }),
    when: (e) => e.type.startsWith('task.'),
    key: pid,
    enabled: () => !!pid(),
  });
  const scan = query(handle, {
    load: () => s.client.securityScanGet(pid()!),
    when: (e) => e.type.startsWith('security.'),
    key: pid,
    enabled: () => !!pid(),
  });
  return () => {
    const project = s.workspace.project;
    const p = s.project;
    const agents = p.agentList;
    const presence = p.presence.data;
    const tasks = p.taskList;
    const needs = p.needs.data ?? [];
    const rooms = p.roomList.filter((r) => !r.archived);
    const working = agents.filter((a) => WORKING.test(presenceFor(a.id, presence)?.presence ?? '')).length;
    const now = Date.now();
    const mergedToday = mergedSince(tasks, dayStart(now));
    const mergedWeek = mergedSince(tasks, now - WEEK_MS);
    const inMotion = tasks.filter((x) => !DONE.has(x.status) && x.status !== 'BACKLOG').slice(0, 6);
    const spend = budget.data ? spendLine(budget.data.usage['project'], budget.data.statuses) : undefined;
    const firstCheck = evals.data ? firstCheckText(evals.data.report) : undefined;
    const report = scan.data;
    const lastScan = report && report.status !== 'running' ? report : undefined;
    const numbers = [
      working ? `${working} ${working === 1 ? 'agent' : 'agents'} working` : `${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}`,
      `${mergedToday} merged today`,
      needs.length ? `${needs.length} ${needs.length === 1 ? 'thing needs' : 'things need'} you` : 'nothing needs you',
    ];
    const taskOf = (x: Task) => x;
    return (
      <Page title={greeting(new Date().getHours())} icon={LayoutDashboard} subtitle={`${project?.name ?? 'Project'} · ${numbers.join(' · ')}`} wide testId="overview">
        <TaskTotalNotice>The tasks under way and the merged counts below are counted from these tasks only.</TaskTotalNotice>
        <div mix={layoutCss}>
          <div mix={colCss}>
            <Section title="Needs you" description={needs.length ? 'Each opens where you decide it.' : undefined} actions={needs.length > 2 ? <a href={routes.needsYou.href()} mix={moreLink}>All {needs.length} <Icon icon={ArrowRight} size={14} /></a> : null} testId="overview-needs">
              <ErrorNote error={p.needs.error} what="Could not load what needs you" />
              {needs.length === 0 ? (
                <p mix={quietCss}>Nothing needs you right now. Approvals, reviews and questions land here as they happen.</p>
              ) : (
                <ul mix={needsGrid}>
                  {needs.slice(0, 2).map((it) => (
                    <NeedCard key={it.key} item={it} agents={agents} />
                  ))}
                </ul>
              )}
            </Section>

            <Section title="Your crew" description={`${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}`} actions={<a href={routes.team.href()} mix={moreLink}>Manage crew <Icon icon={ArrowRight} size={14} /></a>} testId="overview-crew">
              {agents.length === 0 ? (
                <p mix={quietCss}>
                  No agents yet. <a href={routes.team.href()}>Add the first one</a>: a task needs a worker and a different agent as reviewer.
                </p>
              ) : (
                <ul mix={crewGrid}>
                  {agents.map((a) => {
                    const pr = presenceFor(a.id, presence);
                    const busy = WORKING.test(pr?.presence ?? '');
                    return (
                      <li key={a.id}>
                        <a href={routes.agent.href({ id: a.id })} mix={crewCard} data-testid="overview-agent" data-name={a.name}>
                          <Avatar id={a.id} name={a.name} size={64} kind="agent" status={presenceColor(pr)} />
                          <span class="name">{a.name}</span>
                          <span class="role">{a.role}</span>
                          <SkipsBadge mode={a.permissionMode} />
                          <span class="act">
                            <i style={{ background: presenceColor(pr) }} aria-hidden="true" />
                            <span>{doing(pr)}</span>
                          </span>
                          <span class="sr-only">{busy ? 'working' : ''}</span>
                        </a>
                      </li>
                    );
                  })}
                </ul>
              )}
            </Section>

            <Section title="In motion" description={inMotion.length ? `${inMotion.length} ${inMotion.length === 1 ? 'task' : 'tasks'} under way` : undefined} actions={<a href={routes.tasks.href()} mix={moreLink}>All tasks <Icon icon={ArrowRight} size={14} /></a>} testId="overview-motion">
              {inMotion.length === 0 ? (
                <p mix={quietCss}>
                  Nothing is running. <a href={routes.taskNew.href()}>Describe a task</a> and the crew takes it from there.
                </p>
              ) : (
                <ul mix={motionList}>
                  {inMotion.map(taskOf).map((x) => (
                    <li key={x.id}>
                      <a href={routes.task.href({ id: x.id })} mix={motionRow} data-testid="overview-task">
                        {x.assignee ? <Avatar id={x.assignee} name={agentName(x.assignee, agents)} size={24} kind="agent" status={presenceColor(presenceFor(x.assignee, presence))} /> : <Icon icon={Sparkles} size={16} />}
                        <span class="seq">#{x.seq}</span>
                        <span class="title">{x.title}</span>
                        <OriginBadge origin={x.origin} />
                        <StatusChip status={x.status} />
                      </a>
                    </li>
                  ))}
                </ul>
              )}
            </Section>
          </div>

          <aside mix={colCss} aria-label="Today">
            <div mix={card}>
              <h2 mix={panelTitle}>
                Today <span>{new Date().toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })}</span>
              </h2>
              <div mix={statsCss}>
                <div mix={statCss}>
                  <b data-testid="overview-merged">{mergedToday}</b>
                  <small>merged today</small>
                </div>
                <div mix={statCss}>
                  <b>{tasks.filter((x) => !DONE.has(x.status)).length}</b>
                  <small>open tasks</small>
                </div>
                <div mix={statCss}>
                  <b>{working}</b>
                  <small>agents working</small>
                </div>
                <div mix={statCss}>
                  <b>{needs.length}</b>
                  <small>need you</small>
                </div>
              </div>
            </div>
            <div mix={card} data-testid="overview-insights">
              <h2 mix={panelTitle}>
                Insights <span>{project?.name}</span>
              </h2>
              <ErrorNote error={budget.error ?? evals.error ?? scan.error} what="Could not read the insights" />
              <div mix={insightGrid}>
                <a href={routes.budgets.href()} mix={insightCss} data-testid="overview-spend">
                  <small>Spend so far</small>
                  <b data-na={spend && spend.amount === 'n/a' ? '' : undefined}>{spend ? spend.amount : '…'}</b>
                  <small>{spend ? spend.note : 'reading the budget'}</small>
                  {spend?.fraction !== undefined ? <Meter value={spend.fraction} label={`Budget used: ${Math.round(spend.fraction * 100)}%`} tone={spend.fraction >= 1 ? 'err' : spend.fraction >= 0.8 ? 'warn' : 'ok'} /> : null}
                </a>
                <a href={routes.tasks.href()} mix={insightCss} data-testid="overview-merged-week">
                  <small>Merged</small>
                  <b>{mergedWeek}</b>
                  <small>{mergedWeek === 1 ? 'task' : 'tasks'} completed this week</small>
                </a>
                <a href={routes.evals.href()} mix={insightCss} data-testid="overview-first-check">
                  <small>Passed first check</small>
                  <b data-na={firstCheck === 'n/a' ? '' : undefined}>{firstCheck ?? '…'}</b>
                  <small>{firstCheck === 'n/a' ? 'no task checked in 7 days' : 'of tasks checked in 7 days'}</small>
                </a>
                <a href={routes.security.href()} mix={insightCss} data-testid="overview-findings">
                  <small>Security</small>
                  <b data-na={scan.data === null || (lastScan && lastScan.counts.total === 0) ? '' : undefined}>{scan.data === undefined ? '…' : lastScan ? lastScan.counts.total : report ? '…' : '–'}</b>
                  <small>{scan.data === undefined ? 'reading the last scan' : lastScan ? `${findingsText(lastScan.counts.total)} in the last scan` : report ? 'a scan is running' : 'no scan yet'}</small>
                </a>
              </div>
            </div>
            <div mix={card} data-rooms="">
              <h2 mix={panelTitle}>
                Rooms{' '}
                <a href={routes.rooms.href()} mix={moreLink}>
                  Open <Icon icon={ArrowRight} size={14} />
                </a>
              </h2>
              <ul mix={roomsList} aria-label="Rooms">
                {rooms.map((r) => (
                  <RoomLink key={r.id} room={r} current={false} />
                ))}
              </ul>
            </div>
          </aside>
        </div>
      </Page>
    );
  };
}

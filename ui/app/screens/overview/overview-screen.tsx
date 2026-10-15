// Overview (Crew HQ, decision P64): the project's home. A greeting with the day's numbers, what needs you (the first few,
// each a link to where it is decided), the crew as dots with what each agent is doing right now, the tasks in motion, and
// the rooms. Everything here is a projection of the core's lists; nothing is approved from this page.

import { css, type Handle } from 'remix/component';
import { ArrowRight, Inbox, Sparkles } from 'lucide';
import type { Agent, NeedsYouItem, PresenceState, Task } from '../../api/types.ts';
import { agentName, presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { services, watch } from '../../state/services.tsx';
import { RoomLink } from '../../shell/hubnav.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Avatar, StatusChip, presenceColor } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page, Section, card } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { KIND_LABEL, needsTarget } from '../needs-you/needs-you.ts';

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
  '& .kind': { fontSize: t.textXs, fontWeight: '650', letterSpacing: '0.06em', textTransform: 'uppercase', color: t.warning },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0' },
  '& h3': { fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em', overflowWrap: 'anywhere' },
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
  '& .name': { marginTop: t.s2, fontSize: t.textMd, fontWeight: '650' },
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
  '& .title': { flex: '1', minWidth: '0', fontWeight: '550', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

const statsCss = css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: t.s2 });
const statCss = css({
  padding: '12px 14px',
  borderRadius: t.rMd,
  background: t.bgSidebar,
  '& b': { display: 'block', fontSize: '24px', fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.1' },
  '& small': { color: t.textFaint, fontSize: t.textXs },
});
const panelTitle = css({ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: t.s2, margin: `0 0 ${t.s3}`, fontSize: t.textMd, fontWeight: '650', '& span': { color: t.textFaint, fontSize: t.textSm, fontWeight: '500' } });
const roomsList = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const moreLink = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: t.textSm, fontWeight: '550', color: t.textMuted, '&:hover': { color: t.text } });

const DONE = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);
const WORKING = /^(working|thinking)/i;

/** A core timestamp in milliseconds: the core writes seconds in some fields and milliseconds in others. */
function ms(n: number | undefined): number {
  if (!n) return 0;
  return n < 1e11 ? n * 1000 : n;
}

export function greeting(hour: number): string {
  return hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
}

/** What an agent is doing, in one line: its activity, else its presence label, else the presence name. */
export function doing(p: PresenceState | undefined): string {
  if (!p) return 'Resting';
  return p.activity || p.label || p.presence;
}

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
  return () => {
    const project = s.workspace.project;
    const p = s.project;
    const agents = p.agentList;
    const presence = p.presence.data;
    const tasks = p.taskList;
    const needs = p.needs.data ?? [];
    const rooms = p.roomList.filter((r) => !r.archived);
    const working = agents.filter((a) => WORKING.test(presenceFor(a.id, presence)?.presence ?? '')).length;
    const dayStart = new Date();
    dayStart.setHours(0, 0, 0, 0);
    const mergedToday = tasks.filter((x) => x.status === 'COMPLETED' && ms(x.completed ?? x.updated) >= dayStart.getTime()).length;
    const inMotion = tasks.filter((x) => !DONE.has(x.status) && x.status !== 'BACKLOG').slice(0, 6);
    const numbers = [
      working ? `${working} ${working === 1 ? 'agent' : 'agents'} working` : `${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}`,
      `${mergedToday} merged today`,
      needs.length ? `${needs.length} ${needs.length === 1 ? 'thing needs' : 'things need'} you` : 'nothing needs you',
    ];
    const taskOf = (x: Task) => x;
    return (
      <Page title={greeting(new Date().getHours())} subtitle={`${project?.name ?? 'Project'} · ${numbers.join(' · ')}`} wide testId="overview">
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
                        <a href={routes.team.href()} mix={crewCard} data-testid="overview-agent" data-name={a.name}>
                          <Avatar id={a.id} name={a.name} size={64} kind="agent" status={presenceColor(pr)} />
                          <span class="name">{a.name}</span>
                          <span class="role">{a.role}</span>
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

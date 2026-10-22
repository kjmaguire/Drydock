// The project board (graphite, decision P97): the project's colour square and name, the views Board / List / Timeline, the
// filters Agent, Label and Needs me, what the crew is doing ("2 agents working · 1 waiting on you") and New task (C). The board
// has five columns read off each task's step (task.summaries): Spec, Building, Checks, Review and Merged, with the tasks that
// failed or were cancelled in a quiet Stopped list under Merged. A card is a link to its task: its key, a tag (waiting on you,
// ready to merge, or how long it has run), its title, then what its step has to say: the spec gate, what the agent is doing on
// which try with how far along it is, the checks bar with the failing check's reason, the reviewer's verdict with the diff and
// Merge / View diff. The list is the same tasks as dense rows, the timeline the tasks along today or this week, one lane per
// agent. A card's menu edits its labels. Everything re-reads on the task, run, presence and Needs you events.

import { css, navigate, on, type Handle } from 'remix/component';
import { ClipboardList, MoreHorizontal } from 'lucide';
import type { Agent, DrydockEvent, TaskSummary } from '../../api/types.ts';
import { agentName, plural } from '../../lib/format.ts';
import { ownsKeys } from '../../lib/keys.ts';
import { leadStep } from '../../lib/task-origin.ts';
import { taskCoverage } from '../../lib/task-total.ts';
import { visible } from '../../lib/visible.ts';
import { routes } from '../../routes.ts';
import { starts } from '../../state/live.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle, IconButton } from '../../ui/button.tsx';
import { Avatar, hueFor, OriginBadge } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { Page } from '../../ui/layout.tsx';
import { Menu, type MenuItem } from '../../ui/menu.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { TaskTotalNotice } from '../shared/task-total.tsx';
import { adapterName, crewFigures } from '../team/team.ts';
import { SchedulerPanel } from './scheduler-panel.tsx';
import {
  ago,
  APPROVE,
  BOARD_COLUMNS,
  boardOf,
  cardTag,
  checkSegments,
  checksLabel,
  diffText,
  doingLine,
  failingLine,
  finishedWhen,
  labelsOf,
  matchesFilter,
  mergeRequestOf,
  needsMe,
  parseLabels,
  progressOf,
  rangeOf,
  specGateText,
  taskKey,
  timelineOf,
  timelineTicks,
  toMs,
  verdictLine,
  type BoardColumn,
  type BoardFilter,
  type TimelineRange,
} from './tasks.ts';

const VIEW_KEY = 'drydock.tasks.view';
type View = 'board' | 'list' | 'timeline';
const VIEWS: { name: View; label: string }[] = [
  { name: 'board', label: 'Board' },
  { name: 'list', label: 'List' },
  { name: 'timeline', label: 'Timeline' },
];

function readView(): View {
  try {
    const v = localStorage.getItem(VIEW_KEY);
    return v === 'list' || v === 'timeline' ? v : 'board';
  } catch {
    return 'board';
  }
}

function writeView(v: View): void {
  try {
    localStorage.setItem(VIEW_KEY, v);
  } catch {
    /* a convenience only */
  }
}

const WEEK = 7 * 86_400_000;

/** The events after which a card may say something else: its task, its run, its requests, its agent's presence, Needs you. */
const boardEvent = (e: DrydockEvent): boolean => starts(e, 'task.', 'workflow.', 'run.', 'approval.', 'room.presence_changed', 'needsyou.', 'watchdog.');

/** A key press that is the page's own single-letter shortcut: no modifier, not while typing, not inside a widget or a dialog. */
function isShortcut(e: KeyboardEvent, key: string): boolean {
  if (e.key.toLowerCase() !== key || e.ctrlKey || e.metaKey || e.altKey || e.repeat || e.defaultPrevented || ownsKeys(e.target)) return false;
  const el = e.target as HTMLElement | null;
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || el.closest?.('dialog, [role="menu"], [role="listbox"]'))) return false;
  return true;
}

// --- the bar ---

const squareCss = css({ display: 'inline-block', flex: 'none', width: '10px', height: '10px', borderRadius: '3px' });

const chipsCss = css({ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' });

// a filter: dashed while it lets everything through, solid when it is set
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '28px',
  minHeight: '28px',
  padding: '0 10px',
  border: `1px dashed ${t.borderStrong}`,
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '400',
  whiteSpace: 'nowrap',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[data-on="true"]': { borderStyle: 'solid', background: t.bgActive, color: t.text },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& .x': { color: t.textFaint },
});

const crewCss = css({ color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap' });

const newCss = css({
  gap: t.s2,
  '& kbd': { background: 'transparent', borderColor: `color-mix(in srgb, ${t.onAccent} 35%, transparent)`, color: t.onAccent },
});

// --- the board ---

const scrollCss = css({ overflowX: 'auto', minWidth: '0', margin: `0 calc(-1 * ${t.s6})`, padding: `0 ${t.s6}` });

const boardCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(5, minmax(200px, 1fr))',
  gap: '14px',
  alignItems: 'start',
  minWidth: '1056px',
});

const columnCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '10px',
  minWidth: '0',
  '& > h2, & > h3': { display: 'flex', alignItems: 'center', gap: t.s2, padding: '0 4px', fontSize: t.textMd, fontWeight: '500' },
  '& > h2 .n, & > h3 .n': { color: t.textFaint, fontWeight: '400' },
  '& > h3': { marginTop: t.s3, color: t.textMuted },
  '& > ul': { display: 'flex', flexDirection: 'column', gap: '10px', margin: '0', padding: '0', listStyle: 'none' },
});

const cardCss = css({
  position: 'relative',
  display: 'flex',
  flexDirection: 'column',
  gap: '10px',
  padding: '12px',
  border: `1px solid ${t.border}`,
  borderRadius: '9px',
  background: t.bgSurface,
  minWidth: '0',
  transition: `border-color ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&:hover, &:focus-within': { borderColor: t.borderStrong, background: t.bgRaised },
  '&[data-waiting="true"]': { borderColor: `color-mix(in srgb, ${t.mentionBorder} 45%, ${t.border})`, background: t.mentionBg },
  '&[data-column="merged"], &[data-column="stopped"]': { opacity: '0.8' },
  '& .top': { display: 'flex', alignItems: 'center', gap: t.s2, minHeight: '18px' },
  '& .key': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, fontWeight: '500', whiteSpace: 'nowrap' },
  '& .tag': { fontSize: t.textXs, fontWeight: '500', color: t.textFaint, whiteSpace: 'nowrap' },
  '& .tag[data-kind="waiting"], & .tag[data-kind="merge"]': { color: t.warning },
  '& .tag[data-kind="elapsed"]': { fontFamily: t.mono, fontVariantNumeric: 'tabular-nums' },
  '& .top > .grow': { flex: '1' },
  '& .more': { position: 'relative', zIndex: '1', width: '28px', height: '28px', minHeight: '28px', margin: '-6px -6px -6px 0', flex: 'none' },
  '& h3': { margin: '0', fontSize: t.textMd, fontWeight: '500', lineHeight: '1.4', overflowWrap: 'anywhere' },
  // a target of 28 px without taking more room than the line (the card around it is the link's real hit area)
  '& h3 a': { display: 'inline-block', padding: '5px 0', margin: '-5px 0', color: t.text, textDecoration: 'none', outline: 'none' },
  // the whole card is the link's hit area; the link's name is only the title. The card's buttons stack above it.
  '& h3 a::after': { content: '""', position: 'absolute', inset: '0', borderRadius: '9px' },
  '& h3 a:focus-visible::after': { boxShadow: t.focus },
  '& .say': { margin: '0', color: t.textMuted, lineHeight: '1.45', overflowWrap: 'anywhere' },
  '& .doing': { display: 'flex', flexDirection: 'column', gap: '6px', minWidth: '0' },
  '& .doing > span': { color: t.textMuted, fontSize: t.textXs, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .fail': { margin: '0', color: t.danger, fontSize: t.textXs, overflowWrap: 'anywhere' },
  '& .verdict': { margin: '0', color: t.textMuted, fontSize: t.textXs },
  '& .chips': { display: 'flex', gap: '6px', flexWrap: 'wrap', minWidth: '0' },
  '& .origin': { position: 'relative', zIndex: '1', display: 'inline-flex' },
  '& .who': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', fontSize: t.textXs, color: t.textMuted },
  '& .who > .name': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .who > .chip': { marginLeft: 'auto' },
  '& .acts': { position: 'relative', zIndex: '1', display: 'flex', gap: t.s2, flexWrap: 'wrap' },
  '& .acts > *': { minHeight: '28px' },
});

const chipTextCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '20px',
  padding: '0 7px',
  borderRadius: '5px',
  background: t.bgActive,
  color: t.textMuted,
  fontSize: t.textXs,
  whiteSpace: 'nowrap',
  maxWidth: '100%',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  '&[data-mono="true"]': { fontFamily: t.mono },
});

const barCss = css({ height: '3px', borderRadius: '2px', background: t.bgActive, '& > div': { height: '3px', borderRadius: '2px', background: t.accent } });

const checksCss = css({
  display: 'grid',
  gap: '4px',
  '& > span': { height: '6px', borderRadius: '3px', background: t.borderStrong },
  '& > span[data-state="passed"]': { background: t.success },
  '& > span[data-state="failed"]': { background: t.danger },
  '& > span[data-state="running"]': { background: t.accent },
  '& > span[data-state="inconclusive"]': { background: t.warning },
});

/** The column's status glyph, drawn as the mockup draws it. Decoration: the column is named in words beside it. */
function Glyph(handle: Handle<{ column: BoardColumn | 'stopped' }>) {
  return () => {
    const c = handle.props.column;
    const color = c === 'building' ? t.accent : c === 'checks' ? t.success : c === 'review' ? t.warning : c === 'merged' ? t.accent : t.textMuted;
    return (
      <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true" style={{ color, flex: 'none' }}>
        {c === 'spec' ? <circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="3 3" /> : null}
        {c === 'building' ? (
          <>
            <circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" />
            <path d="M12 4a8 8 0 0 1 8 8h-8z" fill="currentColor" />
          </>
        ) : null}
        {c === 'checks' ? (
          <>
            <circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" />
            <path d="m8.5 12 2.5 2.5 4.5-5" fill="none" stroke="currentColor" stroke-width="2" />
          </>
        ) : null}
        {c === 'review' ? (
          <>
            <circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" />
            <path d="M8 12h8" fill="none" stroke="currentColor" stroke-width="2" />
          </>
        ) : null}
        {c === 'merged' ? (
          <>
            <circle cx="12" cy="12" r="9" fill="currentColor" />
            <path d="m8.5 12 2.5 2.5 4.5-5" fill="none" stroke={t.bgApp} stroke-width="2.2" />
          </>
        ) : null}
        {c === 'stopped' ? (
          <>
            <circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" />
            <path d="m9 9 6 6M15 9l-6 6" fill="none" stroke="currentColor" stroke-width="2" />
          </>
        ) : null}
      </svg>
    );
  };
}

/** The checks bar: one segment per kind of check the project runs, coloured by its state, named as a whole for a screen reader. */
function ChecksBar(handle: Handle<{ s: TaskSummary }>) {
  return () => {
    const segs = checkSegments(handle.props.s.checks);
    if (!segs.length) return null;
    return (
      <div mix={checksCss} role="img" aria-label={checksLabel(segs)} data-testid="checks-bar" style={{ gridTemplateColumns: `repeat(${segs.length}, minmax(0, 1fr))` }}>
        {segs.map((x) => (
          <span key={x.name} data-segment={x.name} data-state={x.state} title={`${x.name}: ${x.title}`} />
        ))}
      </div>
    );
  };
}

interface CardProps {
  s: TaskSummary;
  column: BoardColumn | 'stopped';
  agents: readonly Agent[];
  asked: boolean;
  origin: unknown;
  now: number;
  readOnly: boolean;
  merging: boolean;
  onMerge: (s: TaskSummary) => void;
  onViewDiff: () => void;
  onLabels: (s: TaskSummary) => void;
}

/** The agent's dot and who it is: "Builder · Claude Code", or what it does at this step ("Planner is drafting"). */
function Who(handle: Handle<{ id: string | undefined; agents: readonly Agent[]; text: (name: string, adapter: string) => string; diff?: string }>) {
  return () => {
    const { id, agents, text, diff } = handle.props;
    if (!id) {
      return (
        <div class="who">
          <span class="name">Not assigned yet</span>
        </div>
      );
    }
    const a = agents.find((x) => x.id === id);
    const name = agentName(id, agents);
    return (
      <div class="who">
        <Avatar id={id} name={name} size={18} kind="agent" />
        <span class="name">{text(name, a ? adapterName(a.adapter) : '')}</span>
        {diff ? (
          <span class="chip" mix={chipTextCss} data-mono="true" title="Lines added and removed">
            {diff}
          </span>
        ) : null}
      </div>
    );
  };
}

const nameAdapter = (name: string, adapter: string) => (adapter ? `${name} · ${adapter}` : name);

/** The card's menu: its labels. */
function CardMenu(handle: Handle<{ label: string; onLabels: () => void }>) {
  return () => {
    const { label, onLabels } = handle.props;
    return (
      <Menu
        label={label}
        items={[{ name: 'labels', label: 'Labels...' }]}
        placement="bottom-end"
        onSelect={(name) => (name === 'labels' ? onLabels() : undefined)}
        trigger={(m) => <IconButton icon={MoreHorizontal} label={label} size={15} class="more" mix={[m]} />}
      />
    );
  };
}

function Card(handle: Handle<CardProps>) {
  return () => {
    const { s, column, agents, asked, origin, now, readOnly, merging, onMerge, onViewDiff, onLabels } = handle.props;
    const key = taskKey(s);
    const href = routes.task.href({ id: s.taskId });
    const done = column === 'merged' || column === 'stopped';
    const tag = done ? undefined : cardTag(s, asked, now);
    const waiting = tag?.kind === 'waiting' || tag?.kind === 'merge';
    const working = !!s.presence;
    const failed = !done && s.checks.some((c) => c.status === 'failed');
    const merge = mergeRequestOf(s);
    const reviewer = agentName(s.reviewer, agents);
    const verdict = column === 'review' ? verdictLine(s, reviewer) : '';
    const doing = column === 'building' ? doingLine(s) : '';
    const lead = leadStep(origin) !== undefined;
    const chips = !done && (!!s.template || s.labels.length > 0 || lead);
    return (
      <li mix={cardCss} data-testid="task-row" data-task-id={s.taskId} data-status={s.status} data-step={s.step} data-column={column} data-waiting={waiting ? 'true' : undefined}>
        <div class="top">
          <span class="key">{key}</span>
          <span class="grow" />
          {done ? (
            <span class="tag" data-kind="when" title={s.completedAt ? new Date(toMs(s.completedAt)).toLocaleString() : undefined}>
              {column === 'stopped' ? (s.status === 'FAILED' ? 'Failed' : 'Cancelled') : finishedWhen(s.completedAt || s.updatedAt, now)}
            </span>
          ) : tag ? (
            <span class="tag" data-kind={tag.kind} data-testid="card-tag" title={tag.kind === 'elapsed' ? 'How long it has run' : undefined}>
              {tag.text}
            </span>
          ) : null}
          <CardMenu label={`More for ${key}`} onLabels={() => onLabels(s)} />
        </div>
        <h3>
          <a href={href}>{s.title}</a>
        </h3>
        {column === 'spec' && s.pending?.phase === 'spec' ? (
          <p class="say" data-testid="spec-gate">
            {specGateText(s.criteria)}
          </p>
        ) : null}
        {column === 'building' ? (
          <div class="doing">
            {doing ? (
              <span title={doing} data-testid="doing">
                {doing}
              </span>
            ) : null}
            <div mix={barCss} role="progressbar" aria-label={`${key} progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progressOf(s)}>
              <div style={{ width: `${progressOf(s)}%` }} />
            </div>
          </div>
        ) : null}
        {column === 'checks' || column === 'review' || failed ? <ChecksBar s={s} /> : null}
        {failed ? (
          <p class="fail" data-testid="failing-check">
            {failingLine(s.checks)}
          </p>
        ) : null}
        {verdict ? (
          <p class="verdict" data-testid="verdict">
            {verdict}
          </p>
        ) : column === 'review' && s.step === 'review' && s.reviewer ? (
          <p class="verdict">{reviewer} is reviewing</p>
        ) : null}
        {column === 'review' && s.pending && !merge ? <p class="say">{visible(s.pending.title).slice(0, 140)}</p> : null}
        {merge ? (
          <div class="acts">
            <Button variant="primary" size="sm" disabled={readOnly || merging} aria-label={`Merge ${key}`} mix={on('click', () => onMerge(s))}>
              {merging ? 'Merging...' : 'Merge'}
            </Button>
            <a href={href} aria-label={`View diff of ${key}`} mix={[...buttonStyle('secondary', 'sm'), on('click', () => onViewDiff())]}>
              View diff
            </a>
          </div>
        ) : null}
        {chips ? (
          <div class="chips">
            {s.template ? (
              <span mix={chipTextCss} title="Template">
                {s.template}
              </span>
            ) : null}
            {s.labels.map((l) => (
              <span key={l} mix={chipTextCss} data-testid="card-label">
                {l}
              </span>
            ))}
            {lead ? (
              <span class="origin">
                <OriginBadge origin={origin} />
              </span>
            ) : null}
          </div>
        ) : null}
        {column === 'spec' && s.pending?.phase !== 'spec' ? <Who id={s.assignee} agents={agents} text={(name, adapter) => (working ? `${name} is drafting` : nameAdapter(name, adapter))} /> : null}
        {column === 'building' ? <Who id={s.assignee} agents={agents} text={nameAdapter} diff={s.diff ? diffText(s.diff) : undefined} /> : null}
        {column === 'checks' ? <Who id={s.assignee} agents={agents} text={(name, adapter) => (failed && working ? `${name} is fixing it` : nameAdapter(name, adapter))} /> : null}
      </li>
    );
  };
}

// --- the list ---

const STAGE: Record<BoardColumn | 'stopped', string> = { spec: 'Spec', building: 'Building', checks: 'Checks', review: 'Review', merged: 'Merged', stopped: 'Stopped' };

const listCss = css({
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
});

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '76px minmax(0, 1fr) 84px 120px minmax(0, 180px) 44px',
  alignItems: 'center',
  gap: t.s3,
  minHeight: '44px',
  padding: `0 ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  fontSize: t.textMd,
  '&:first-child': { borderTop: '0' },
  '&:hover, &:focus-within': { background: t.bgHover },
  '&[data-waiting="true"]': { background: t.mentionBg },
  '& .key': { color: t.textFaint, fontFamily: t.mono, fontSize: t.textXs, whiteSpace: 'nowrap' },
  '& .title': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& .title a': { padding: '6px 0', color: t.text, fontWeight: '500', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', textDecoration: 'none', outline: 'none' },
  // the whole row is the link's hit area; the link's name is only the title
  '& .title a::after': { content: '""', position: 'absolute', inset: '0' },
  '& .title a:focus-visible::after': { boxShadow: `inset ${t.focus}` },
  '& .title .chip, & .title .origin': { position: 'relative', zIndex: '1', flex: 'none' },
  '& .stage': { display: 'flex', alignItems: 'center', gap: '6px', color: t.textMuted, fontSize: t.textXs },
  '& .tag': { fontSize: t.textXs, color: t.textFaint, whiteSpace: 'nowrap' },
  '& .tag[data-kind="waiting"], & .tag[data-kind="merge"]': { color: t.warning },
  '& .tag[data-kind="elapsed"]': { fontFamily: t.mono },
  '& .who': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', color: t.textMuted, fontSize: t.textXs },
  '& .who > span:last-child': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .time': { color: t.textFaint, fontSize: t.textXs, textAlign: 'right' },
  '@media (max-width: 900px)': { gridTemplateColumns: '64px minmax(0, 1fr) 110px', '& .stage, & .who, & .time': { display: 'none' } },
});

function Row(handle: Handle<{ s: TaskSummary; column: BoardColumn | 'stopped'; agents: readonly Agent[]; asked: boolean; origin: unknown; now: number }>) {
  return () => {
    const { s, column, agents, asked, origin, now } = handle.props;
    const done = column === 'merged' || column === 'stopped';
    const tag = done ? undefined : cardTag(s, asked, now);
    const who = s.assignee;
    return (
      <li mix={rowCss} data-testid="task-row" data-task-id={s.taskId} data-status={s.status} data-column={column} data-waiting={tag?.kind === 'waiting' || tag?.kind === 'merge' ? 'true' : undefined}>
        <span class="key">{taskKey(s)}</span>
        <span class="title">
          <a href={routes.task.href({ id: s.taskId })}>{s.title}</a>
          {s.labels.map((l) => (
            <span key={l} class="chip" mix={chipTextCss} data-testid="card-label">
              {l}
            </span>
          ))}
          {leadStep(origin) !== undefined ? (
            <span class="origin">
              <OriginBadge origin={origin} />
            </span>
          ) : null}
        </span>
        <span class="stage">
          <Glyph column={column} />
          {STAGE[column]}
        </span>
        <span class="tag" data-kind={tag?.kind}>
          {done ? (column === 'stopped' ? (s.status === 'FAILED' ? 'Failed' : 'Cancelled') : finishedWhen(s.completedAt || s.updatedAt, now)) : (tag?.text ?? '')}
        </span>
        <span class="who">
          {who ? <Avatar id={who} name={agentName(who, agents)} size={18} kind="agent" /> : null}
          <span>{who ? agentName(who, agents) : 'Not assigned yet'}</span>
        </span>
        <time class="time" title={new Date(s.updatedAt).toLocaleString()}>
          {ago(s.updatedAt, now)}
        </time>
      </li>
    );
  };
}

// --- the timeline ---

const timelineCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s3, flexWrap: 'wrap' },
  '& .head .note': { color: t.textFaint, fontSize: t.textXs },
  '& .lanes': { display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' },
  '& .lane': { display: 'grid', gridTemplateColumns: '160px minmax(0, 1fr)', alignItems: 'start', gap: t.s3 },
  '& .lane > .agent': { display: 'flex', alignItems: 'center', gap: t.s2, minHeight: '28px', minWidth: '0', fontSize: t.textXs, color: t.textMuted },
  '& .lane > .agent > span:last-child': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .track': { position: 'relative', overflow: 'hidden', borderRadius: t.rSm, background: t.bgApp },
  '& .block': {
    position: 'absolute',
    display: 'flex',
    alignItems: 'center',
    height: '28px',
    minWidth: '64px',
    padding: '0 6px',
    boxSizing: 'border-box',
    borderRadius: t.rSm,
    background: t.accentSoft,
    color: t.accentText,
    fontFamily: t.mono,
    fontSize: t.textXs,
    textDecoration: 'none',
    overflow: 'hidden',
    whiteSpace: 'nowrap',
  },
  '& .block[data-state="waiting"]': { background: t.warningSoft, color: t.warning },
  '& .block[data-state="done"]': { background: t.successSoft, color: t.success },
  '& .block[data-state="stopped"]': { background: t.dangerSoft, color: t.danger },
  '& .block:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& .ticks': { position: 'relative', height: '18px', marginLeft: `calc(160px + ${t.s3})`, color: t.textFaint, fontSize: t.textXs },
  '& .ticks > span': { position: 'absolute', top: '0', transform: 'translateX(-50%)', whiteSpace: 'nowrap' },
  '& .ticks > span:first-child': { transform: 'none' },
});

const segCss = css({
  display: 'inline-flex',
  gap: '2px',
  padding: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  '& > button': { height: '28px', minHeight: '28px', padding: '0 12px', border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted, fontSize: t.textMd },
  '& > button:hover': { color: t.text },
  '& > button[aria-pressed="true"]': { background: t.bgActive, color: t.text },
  '& > button:focus-visible': { boxShadow: t.focus, outline: 'none' },
});

const BLOCK_WORDS = { running: 'running', waiting: 'waiting on you', done: 'merged', stopped: 'stopped' } as const;

function Timeline(handle: Handle<{ list: readonly TaskSummary[]; agents: readonly Agent[]; now: number; range: TimelineRange; onRange: (r: TimelineRange) => void }>) {
  return () => {
    const { list, agents, now, range, onRange } = handle.props;
    const r = rangeOf(range, now);
    const lanes = timelineOf(list, r, now);
    const waitingStart = list.filter((s) => !toMs(s.startedAt) && !s.completedAt).length;
    return (
      <section mix={timelineCss} aria-label="Timeline" data-testid="task-timeline">
        <div class="head">
          <div role="group" aria-label="Range" mix={segCss}>
            <button type="button" aria-pressed={range === 'today'} data-testid="timeline-today" mix={on('click', () => onRange('today'))}>
              Today
            </button>
            <button type="button" aria-pressed={range === 'week'} data-testid="timeline-week" mix={on('click', () => onRange('week'))}>
              This week
            </button>
          </div>
          {waitingStart ? <span class="note">{plural(waitingStart, 'task')} not started yet, so not on the timeline</span> : null}
        </div>
        {lanes.length === 0 ? (
          <p class="note" style={{ margin: '0', color: t.textFaint }}>
            No task ran {range === 'today' ? 'today' : 'this week'}.
          </p>
        ) : (
          <>
            <ul class="lanes" aria-label="Lanes">
              {lanes.map((lane) => (
                <li key={lane.agent || 'none'} class="lane" data-testid="timeline-lane" data-agent={lane.agent}>
                  <span class="agent">
                    {lane.agent ? <Avatar id={lane.agent} name={agentName(lane.agent, agents)} size={18} kind="agent" /> : null}
                    <span>{lane.agent ? agentName(lane.agent, agents) : 'Not assigned'}</span>
                  </span>
                  <div class="track" style={{ height: `${lane.rows * 32 - 4}px` }}>
                    {lane.blocks.map((b) => (
                      <a
                        key={b.task.taskId}
                        class="block"
                        href={routes.task.href({ id: b.task.taskId })}
                        data-testid="timeline-block"
                        data-state={b.state}
                        aria-label={`${taskKey(b.task)} ${b.task.title}, ${BLOCK_WORDS[b.state]}`}
                        title={`${taskKey(b.task)} ${b.task.title}`}
                        // a block as wide as its run, at least a target's width, and never past the end of the track
                        style={{ left: `min(${b.left}%, calc(100% - 64px))`, width: `${b.width}%`, top: `${b.row * 32}px` }}
                      >
                        {taskKey(b.task)}
                      </a>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
            <div class="ticks" aria-hidden="true">
              {timelineTicks(range, r).map((x) => (
                <span key={x.label + x.at} style={{ left: `${x.at}%` }}>
                  {x.label}
                </span>
              ))}
            </div>
          </>
        )}
      </section>
    );
  };
}

// --- the labels of a task ---

function LabelsDialog(handle: Handle<{ s: TaskSummary | undefined; known: readonly string[]; onClose: () => void; onSaved: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let text = '';
  let error: string | undefined;
  let forId = '';

  const save = async () => {
    const task = handle.props.s;
    if (!task) return;
    const parsed = parseLabels(text);
    if (parsed.error) {
      error = parsed.error;
      void handle.update();
      handle.queueTask(() => document.querySelector<HTMLElement>('dialog [aria-invalid="true"]')?.focus());
      return;
    }
    const done = await act.run('save', () => s.client.taskLabelsSet(task.taskId, parsed.labels));
    if (done) handle.props.onSaved();
  };

  return () => {
    const task = handle.props.s;
    if (task && task.taskId !== forId) {
      forId = task.taskId;
      text = task.labels.join(', ');
      error = undefined;
    }
    if (!task) forId = '';
    const unused = handle.props.known.filter((l) => !parseLabels(text).labels.includes(l));
    return (
      <Dialog open={!!task} title={task ? `Labels of ${taskKey(task)}` : 'Labels'} onClose={() => handle.props.onClose()}>
        {task ? (
          <form
            mix={on('submit', (e) => {
              e.preventDefault();
              void save();
            })}
            style={{ display: 'grid', gap: t.s4 }}
          >
            <ErrorNote error={act.error} what="Labels not saved" />
            <Field label="Labels" hint="Separated by commas: up to 8, each up to 24 characters." error={error}>
              {(id) => (
                <input
                  id={id}
                  value={text}
                  autofocus
                  {...errorAttrs(id, error)}
                  mix={on('input', (e) => {
                    text = e.currentTarget.value;
                    error = undefined;
                    void handle.update();
                  })}
                />
              )}
            </Field>
            {unused.length ? (
              <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', alignItems: 'center' }}>
                {unused.slice(0, 12).map((l) => (
                  <Button
                    key={l}
                    size="sm"
                    variant="ghost"
                    aria-label={`Add ${l}`}
                    mix={on('click', () => {
                      text = [...parseLabels(text).labels, l].join(', ');
                      error = undefined;
                      void handle.update();
                    })}
                  >
                    + {l}
                  </Button>
                ))}
              </div>
            ) : null}
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={!!act.busy}>
                {act.busy ? 'Saving...' : 'Save labels'}
              </Button>
              <Button mix={on('click', () => handle.props.onClose())}>Cancel</Button>
            </div>
          </form>
        ) : null}
      </Dialog>
    );
  };
}

// --- the screen ---

const pageInnerCss = css({ display: 'grid', gap: t.s5, minWidth: '0' });

export function TaskListScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  let view: View = readView();
  let filter: BoardFilter = {};
  let range: TimelineRange = 'today';
  let merging = '';
  let labelsFor: TaskSummary | undefined;
  const mergeAct = action(handle);
  const pid = () => s.workspace.projectId ?? '';
  const mine = (e: { projectId?: string }) => !e.projectId || e.projectId === pid();

  const summaries = query(handle, {
    load: () => s.client.taskSummaries({ projectId: pid() }),
    when: (e) => boardEvent(e) && mine(e),
    key: () => pid(),
    enabled: () => !!pid(),
    debounceMs: 250,
  });
  const merged = query(handle, {
    load: () => s.client.taskCount(pid(), ['COMPLETED'], { completedSince: Date.now() - WEEK }),
    when: (e) => starts(e, 'task.') && mine(e),
    key: () => pid(),
    enabled: () => !!pid(),
  });

  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));

  // C: a new task
  window.addEventListener(
    'keydown',
    (e) => {
      if (!isShortcut(e, 'c')) return;
      e.preventDefault();
      go(routes.taskNew.href());
    },
    { signal: handle.signal },
  );

  // the clocks of the running tasks tick once a second
  const timer = setInterval(() => {
    if ((summaries.data?.summaries ?? []).some((x) => cardTag(x)?.kind === 'elapsed')) void handle.update();
  }, 1000);
  handle.signal.addEventListener('abort', () => clearInterval(timer));

  const setView = (v: string) => {
    view = v as View;
    writeView(view);
    void handle.update();
  };

  const setFilter = (f: Partial<BoardFilter>) => {
    filter = { ...filter, ...f };
    void handle.update();
  };

  const merge = async (x: TaskSummary) => {
    const req = mergeRequestOf(x);
    if (!req) return;
    merging = x.taskId;
    await mergeAct.run('merge', () => s.client.runRespond(req.runId, req.requestId, APPROVE));
    merging = '';
    void summaries.reload();
    void handle.update();
  };

  return () => {
    const project = s.workspace.project;
    const agents = s.project.agentList;
    const presence = s.project.presence.data;
    const data = summaries.data;
    const all = data?.summaries ?? [];
    const asked = new Set((s.project.needs.data ?? []).map((i) => i.taskId).filter((id): id is string => !!id));
    const origins = new Map(s.project.taskList.map((x) => [x.id, x.origin] as const));
    const shown = all.filter((x) => matchesFilter(x, filter, asked));
    const board = boardOf(shown, (x) => needsMe(x, asked));
    const labels = labelsOf(all);
    const now = Date.now();
    const working = crewFigures(agents, presence ?? [], s.project.taskList, []).working;
    const waiting = all.filter((x) => needsMe(x, asked)).length;
    const readOnly = s.workspace.license.readOnly;
    const agentOn = filter.agent ? agents.find((a) => a.id === filter.agent) : undefined;
    const coverage = data ? { ...taskCoverage(all.length, data.total), partial: data.partial } : undefined;
    const filtered = !!(filter.agent || filter.label || filter.needsMe);

    const agentItems: MenuItem[] = [
      { name: '', label: 'Any agent', type: 'radio', checked: !filter.agent, value: '' },
      ...agents.map((a) => ({ name: a.id, label: a.name, type: 'radio' as const, checked: filter.agent === a.id, value: a.id })),
    ];
    const labelItems: MenuItem[] = [
      { name: '', label: 'Any label', type: 'radio', checked: !filter.label, value: '' },
      ...labels.map((l) => ({ name: l, label: l, type: 'radio' as const, checked: filter.label === l, value: l })),
    ];

    const bar = (
      <>
        <Tabs label="View" pills items={VIEWS.map((v) => ({ name: v.name, label: v.label, testId: `task-view-${v.name}` }))} active={view} onChange={setView} />
        <div mix={chipsCss} role="group" aria-label="Filters">
          <Menu
            label="Agent"
            items={agentItems}
            onSelect={(name) => setFilter({ agent: name || undefined })}
            trigger={(m) => (
              <button type="button" mix={[chipCss, m]} data-on={agentOn ? 'true' : undefined} data-testid="filter-agent">
                {agentOn ? `Agent: ${agentOn.name}` : 'Agent'}
                {agentOn ? <span class="x" aria-hidden="true">×</span> : null}
              </button>
            )}
          />
          <Menu
            label="Label"
            items={labelItems}
            onSelect={(name) => setFilter({ label: name || undefined })}
            trigger={(m) => (
              <button type="button" mix={[chipCss, m]} data-on={filter.label ? 'true' : undefined} data-testid="filter-label">
                {filter.label ? `Label: ${filter.label}` : 'Label'}
                {filter.label ? <span class="x" aria-hidden="true">×</span> : null}
              </button>
            )}
          />
          <button type="button" mix={[chipCss, on('click', () => setFilter({ needsMe: !filter.needsMe }))]} aria-pressed={!!filter.needsMe} data-on={filter.needsMe ? 'true' : undefined} data-testid="filter-needs-me">
            Needs me
            {filter.needsMe ? <span class="x" aria-hidden="true">×</span> : null}
          </button>
        </div>
      </>
    );

    const actions = (
      <>
        <span mix={crewCss} data-testid="crew-line">
          {plural(working, 'agent')} working · {waiting} waiting on you
        </span>
        <a href={routes.taskNew.href()} mix={[...buttonStyle('primary'), newCss]} aria-keyshortcuts="C">
          New task
          <kbd aria-hidden="true">C</kbd>
        </a>
      </>
    );

    const props = { agents, now };
    const cardOf = (x: TaskSummary, column: BoardColumn | 'stopped') => (
      <Card
        key={x.taskId}
        s={x}
        column={column}
        {...props}
        asked={asked.has(x.taskId)}
        origin={origins.get(x.taskId)}
        readOnly={readOnly}
        merging={merging === x.taskId}
        onMerge={(t) => void merge(t)}
        onViewDiff={() => s.ui.openDock('diff')}
        onLabels={(t) => {
          labelsFor = t;
          void handle.update();
        }}
      />
    );

    return (
      <Page
        title={project?.name ?? 'Tasks'}
        mark={<span mix={squareCss} aria-hidden="true" style={{ background: `hsl(${hueFor(project?.id ?? pid())} 62% 62%)` }} />}
        bar={bar}
        actions={actions}
        wide
        testId="tasks"
      >
        <div mix={pageInnerCss}>
          <ErrorNote error={summaries.error} what="Could not load tasks" />
          <ErrorNote error={mergeAct.error} what="Could not merge" />
          {coverage ? <TaskTotalNotice coverage={coverage}>Older tasks are not on this board, and the filters and counts work on these only.</TaskTotalNotice> : null}
          {summaries.loading && !data ? <Loading label="Loading tasks..." /> : null}
          {data && all.length === 0 ? (
            <Empty title="No tasks yet" icon={ClipboardList}>
              <p>A task is the unit of work. Create one, assign a worker and a different reviewer, then start it.</p>
            </Empty>
          ) : null}
          {all.length > 0 && filtered && shown.length === 0 ? (
            <Empty title="No tasks match" icon={ClipboardList}>
              <p>No task here matches the filters.</p>
            </Empty>
          ) : null}
          {all.length > 0 && view === 'board' ? (
            <div mix={scrollCss}>
              <div mix={boardCss} data-testid="task-board">
                {board.columns.map((col) => {
                  const def = BOARD_COLUMNS.find((c) => c.name === col.column)!;
                  const week = col.column === 'merged' ? merged.data?.total : undefined;
                  return (
                    <section key={col.column} mix={columnCss} aria-label={def.label} data-testid="board-column" data-column={col.column}>
                      <h2>
                        <Glyph column={col.column} />
                        {def.label}
                        <span class="n" data-testid="column-count">
                          {col.column === 'merged' ? `${week ?? col.tasks.length} this week` : col.tasks.length}
                        </span>
                      </h2>
                      {col.tasks.length ? <ul aria-label={def.label}>{col.tasks.map((x) => cardOf(x, col.column))}</ul> : null}
                      {col.column === 'merged' && board.stopped.length ? (
                        <>
                          <h3 data-testid="stopped-head">
                            <Glyph column="stopped" />
                            Stopped
                            <span class="n">{board.stopped.length}</span>
                          </h3>
                          <ul aria-label="Stopped" data-testid="stopped">
                            {board.stopped.map((x) => cardOf(x, 'stopped'))}
                          </ul>
                        </>
                      ) : null}
                    </section>
                  );
                })}
              </div>
            </div>
          ) : null}
          {all.length > 0 && view === 'list' && shown.length > 0 ? (
            <ul mix={listCss} aria-label="Tasks" data-testid="task-list">
              {[...board.columns.flatMap((c) => c.tasks.map((x) => [x, c.column] as const)), ...board.stopped.map((x) => [x, 'stopped' as const] as const)].map(([x, column]) => (
                <Row key={x.taskId} s={x} column={column} {...props} asked={asked.has(x.taskId)} origin={origins.get(x.taskId)} />
              ))}
            </ul>
          ) : null}
          {all.length > 0 && view === 'timeline' ? (
            <Timeline
              list={shown}
              {...props}
              range={range}
              onRange={(r) => {
                range = r;
                void handle.update();
              }}
            />
          ) : null}
          {pid() ? <SchedulerPanel projectId={pid()} /> : null}
        </div>
        <LabelsDialog
          s={labelsFor}
          known={labels}
          onClose={() => {
            labelsFor = undefined;
            void handle.update();
          }}
          onSaved={() => {
            labelsFor = undefined;
            void summaries.reload();
            void handle.update();
          }}
        />
      </Page>
    );
  };
}

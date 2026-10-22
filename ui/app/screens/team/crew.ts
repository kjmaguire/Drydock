// Pure helpers for the Crew page (graphite, decision P97): the "Today" timeline's geometry (its window, the hour ticks, one block
// per run and per wait, placed by time and clipped to the window), each agent's state in words and its one line of what it is
// doing or did. No DOM, no services: the page and its tests share them.

import type { AgentLane, CrewTimeline, RunOutcome, WaitSpan } from '../../api/inboxClient.ts';
import type { Agent, PresenceState, TaskSummary } from '../../api/types.ts';
import { agentStatus, isStopped } from '../../lib/format.ts';
import { cliName, type CliInstall } from '../shared/cli-name.ts';

export const MINUTE = 60_000;
export const HOUR = 60 * MINUTE;
/** The timeline starts at 08:00 unless something happened earlier. */
export const DAY_START_HOUR = 8;
/** A block is never drawn narrower than this share of the track (percent), so a one-minute run can still be seen. */
export const MIN_BLOCK = 0.6;

/** Local midnight of the day `now` is in (the timeline asks the core for the spans since then). */
export function localMidnight(now: number): number {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

const floorHour = (ms: number): number => {
  const d = new Date(ms);
  d.setMinutes(0, 0, 0);
  return d.getTime();
};

export interface TimeWindow {
  start: number;
  end: number;
}

/**
 * The window the timeline draws: 08:00 to now, or from the hour of the first run or wait when that is earlier (never before
 * midnight). Before 08:00 it starts at the hour now is in; it is always at least a minute wide.
 */
export function timelineWindow(tl: Pick<CrewTimeline, 'agents'> | undefined, now: number): TimeWindow {
  const midnight = localMidnight(now);
  let first = Number.POSITIVE_INFINITY;
  for (const l of tl?.agents ?? []) {
    for (const r of l.runs) first = Math.min(first, r.start);
    for (const w of l.waits) first = Math.min(first, w.start);
  }
  let start = Math.min(midnight + DAY_START_HOUR * HOUR, floorHour(now));
  if (Number.isFinite(first)) start = Math.min(start, floorHour(first));
  start = Math.max(start, midnight);
  return { start, end: Math.max(now, start + MINUTE) };
}

const pad = (n: number): string => String(n).padStart(2, '0');

/** "09:12" in local time. */
export function clock(ms: number): string {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Where a moment sits along the window, in percent (0 at its start, 100 at now), clamped to the track. */
export function at(w: TimeWindow, ms: number): number {
  const span = w.end - w.start;
  return Math.min(100, Math.max(0, ((ms - w.start) / span) * 100));
}

export interface Tick {
  label: string;
  /** Percent from the left. */
  left: number;
}

/**
 * The hour labels over the track: every 1, 2, 3, 4 or 6 hours from the first whole hour, as few steps as keep them at most
 * `max - 1` (the last label is "now", at the right end); an hour too close to "now" to read beside it is left out.
 */
export function timelineTicks(w: TimeWindow, max = 6): Tick[] {
  const firstHour = floorHour(w.start) === w.start ? w.start : floorHour(w.start) + HOUR;
  const hours = Math.max(0, Math.floor((w.end - firstHour) / HOUR) + 1);
  const step = [1, 2, 3, 4, 6].find((s) => Math.ceil(hours / s) <= max - 1) ?? 6;
  const out: Tick[] = [];
  for (let ms = firstHour; ms < w.end; ms += step * HOUR) {
    const left = at(w, ms);
    if (left > 88) break;
    out.push({ label: clock(ms), left });
  }
  out.push({ label: 'now', left: 100 });
  return out;
}

/** What a block shows: a run's outcome, or a wait on the person (still waiting, or decided since). */
export type BlockKind = RunOutcome | 'waiting' | 'waited';

export const BLOCK_WORDS: Record<BlockKind, string> = {
  working: 'working now',
  finished: 'finished',
  failed: 'failed',
  cancelled: 'cancelled',
  waiting: 'waiting on you',
  waited: 'waited on you',
};

export interface Block {
  key: string;
  kind: BlockKind;
  /** Percent from the left of the track, and percent of its width. */
  left: number;
  width: number;
  /** "Builder · GOC-43 · 09:12–09:47 · finished": the block's title and its text for assistive tech. */
  label: string;
  taskId?: string;
}

/** The time of a span as its label says it: "09:12" today, "yesterday 23:40" for one that began before midnight. */
function since(ms: number, midnight: number): string {
  return ms < midnight ? `yesterday ${clock(ms)}` : clock(ms);
}

function place(w: TimeWindow, start: number, end: number): { left: number; width: number } | undefined {
  if (end <= w.start || start >= w.end) return undefined;
  const left = at(w, start);
  const width = Math.max(MIN_BLOCK, at(w, end) - left);
  return { left: Math.min(left, 100 - width), width };
}

/**
 * One lane's blocks: its runs, then its waits (drawn over the runs), each placed by time and clipped to the window. A run or
 * wait that has not ended reaches now. `keyOf` names a task from its number ("GOC-43").
 */
export function laneBlocks(lane: Pick<AgentLane, 'name' | 'runs' | 'waits'>, w: TimeWindow, now: number, keyOf: (seq: number) => string): Block[] {
  const midnight = localMidnight(now);
  const out: Block[] = [];
  const label = (seq: number | undefined, from: number, to: number, kind: BlockKind) =>
    [lane.name, seq ? keyOf(seq) : '', `${since(from, midnight)}–${to ? clock(to) : 'now'}`, BLOCK_WORDS[kind]].filter(Boolean).join(' · ');
  for (const r of lane.runs) {
    const pos = place(w, r.start, r.end ?? now);
    if (!pos) continue;
    out.push({ key: `run:${r.runId}`, kind: r.outcome, ...pos, label: label(r.taskSeq, r.startedAt || r.start, r.endedAt, r.outcome), taskId: r.taskId || undefined });
  }
  for (const x of lane.waits) {
    const pos = place(w, x.start, x.end ?? now);
    if (!pos) continue;
    const kind: BlockKind = x.end === null && !x.resolvedAt ? 'waiting' : 'waited';
    out.push({ key: `wait:${x.approvalId}`, kind, ...pos, label: label(x.taskSeq, x.createdAt || x.start, x.resolvedAt, kind), taskId: x.taskId || undefined });
  }
  return out;
}

/** The request an agent waits on the person for right now (the oldest one still open), if any. */
export function openWait(lane: Pick<AgentLane, 'waits'> | undefined): WaitSpan | undefined {
  return lane?.waits.find((x) => x.end === null && !x.resolvedAt && (x.status === 'REQUESTED' || !x.status));
}

/** An agent's state as the card says it, on its right. */
export type CardState = 'working' | 'waiting-on-you' | 'waiting' | 'starting' | 'idle' | 'error' | 'stopped';

export const CARD_WORDS: Record<CardState, string> = {
  working: 'working',
  'waiting-on-you': 'waiting on you',
  waiting: 'waiting',
  starting: 'starting',
  idle: 'idle',
  error: 'error',
  stopped: 'stopped',
};

/** The card's state: waiting on the person when its presence says so or a request of its waits open, else its presence. */
export function cardState(p: PresenceState | undefined, waitsOnYou: boolean): CardState {
  if (waitsOnYou) return 'waiting-on-you';
  const s = agentStatus(p);
  return s === 'waiting-for-you' ? 'waiting-on-you' : s;
}

/** The task an agent is on: the one its presence names, else its open task that says the agent is at work on it. */
export function currentSummary(agentId: string, p: PresenceState | undefined, list: readonly TaskSummary[]): TaskSummary | undefined {
  const named = p?.taskId ? list.find((x) => x.taskId === p.taskId) : undefined;
  if (named) return named;
  if (isStopped(p)) return undefined;
  return list.find((x) => x.assignee === agentId && !!x.presence && !DONE.has(x.status));
}

const DONE = new Set(['COMPLETED', 'FAILED', 'CANCELLED']);
const QUEUED = new Set(['READY', 'CLAIMED', 'IN_PROGRESS', 'BLOCKED']);

/** The task it finished last (of the summaries: the open ones and those that ended this week). */
export function lastFinished(agentId: string, list: readonly TaskSummary[]): TaskSummary | undefined {
  let best: TaskSummary | undefined;
  for (const x of list) {
    if (x.assignee !== agentId || x.status !== 'COMPLETED') continue;
    if (!best || (x.completedAt ?? x.updatedAt) > (best.completedAt ?? best.updatedAt)) best = x;
  }
  return best;
}

/** The next task in its queue: an open task it works on, other than `except`, oldest first. */
export function nextQueued(agentId: string, list: readonly TaskSummary[], except?: string): TaskSummary | undefined {
  const mine = list.filter((x) => x.assignee === agentId && x.taskId !== except && QUEUED.has(x.status));
  return mine.sort((a, b) => a.seq - b.seq)[0];
}

/** A piece of the card's line: words, a task key drawn in the mono face, or the way to the Inbox (a link on the card). */
export type LinePart = string | { key: string } | { inbox: string };

export interface LineInput {
  state: CardState;
  presence?: PresenceState;
  current?: TaskSummary;
  next?: TaskSummary;
  last?: TaskSummary;
  wait?: WaitSpan;
  /** The key of the task the wait is about ("GOC-41"), when it is about one. */
  waitKey?: string;
  purpose?: string;
  keyOf: (s: { seq: number; projectKey?: string }) => string;
}

const sentence = (s: string): string => (/[.!?]$/.test(s) ? s : `${s}.`);
const firstSentence = (s: string, max = 120): string => {
  const one = s.split(/(?<=[.!?])\s/)[0]!.trim();
  return one.length > max ? `${one.slice(0, max - 3).trimEnd()}...` : one;
};

/**
 * The card's one line of what the agent is doing or did: waiting on the person (what it asked, and where to decide), at work
 * (its latest tool call or activity, on which task, and what is next in its queue), else the task it finished last, else what
 * is next, else the first sentence of its purpose.
 */
export function agentLine(o: LineInput): LinePart[] {
  const { state, presence: p, current, next, last, wait, keyOf } = o;
  if (state === 'waiting-on-you') {
    const asked = wait?.title || p?.activity || 'Asked you something';
    const on: LinePart[] = o.waitKey ? [' on ', { key: o.waitKey }, '.'] : ['.'];
    return [asked.replace(/[.]$/, ''), ...on, ' ', { inbox: 'Open the Inbox to decide.' }];
  }
  if (state === 'working' || state === 'waiting') {
    const what = p?.doing || p?.activity || current?.doing || (state === 'working' ? 'Working' : 'Waiting');
    const out: LinePart[] = current ? [`${what} on `, { key: keyOf(current) }, ` ${sentence(current.title)}`] : [sentence(what)];
    if (next) out.push(' Next in its queue: ', { key: keyOf(next) }, '.');
    return out;
  }
  if (last) return ['Last: finished ', { key: keyOf(last) }, ` ${sentence(last.title)}`];
  if (next) return ['Next in its queue: ', { key: keyOf(next) }, ` ${sentence(next.title)}`];
  if (o.purpose?.trim()) return [sentence(firstSentence(o.purpose.trim()))];
  return ['Not on a task right now.'];
}

/** The progress of a task the agent is on: its step of the task's steps, or undefined when it has none. */
export function progressOf(s: Pick<TaskSummary, 'stepIndex' | 'stepCount'> | undefined): { value: number; max: number; percent: number } | undefined {
  if (!s || !(s.stepCount > 0)) return undefined;
  const value = Math.min(Math.max(0, s.stepIndex), s.stepCount);
  return { value, max: s.stepCount, percent: Math.round((value / s.stepCount) * 100) };
}

/** The line under the page title: "6 agents · 3 working · 1 waiting on you". */
export function crewLine(f: { agents: number; working: number; waiting: number }): string {
  return `${f.agents} ${f.agents === 1 ? 'agent' : 'agents'} · ${f.working} working · ${f.waiting} waiting on you`;
}

/** What an agent's runtime is called on its card: "Models on this PC" for a local helper, else its CLI's install name. */
export function runtimeName(
  a: Pick<Agent, 'adapter' | 'runMode' | 'wslDistro'>,
  o: { local: boolean; project?: { runMode: string; wslDistro?: string }; canWSL: boolean; installs?: readonly CliInstall[] },
): string {
  if (o.local) return 'Models on this PC';
  const all = o.installs ?? [];
  if (a.runMode === 'wsl') return cliName({ adapter: a.adapter, runMode: 'wsl', distro: a.wslDistro }, all);
  if (a.runMode === 'native') return cliName({ adapter: a.adapter, runMode: 'native' }, all);
  const inWSL = o.project?.runMode === 'wsl' && o.canWSL;
  return inWSL ? cliName({ adapter: a.adapter, runMode: 'wsl', distro: o.project?.wslDistro }, all) : cliName({ adapter: a.adapter, runMode: 'native' }, all);
}

/** A channel or group of a project, with the agents in it. */
export interface CrewRoom {
  name: string;
  kind: string;
  agents: string[];
}

/**
 * The rooms a card names for an agent: the ones it is in that not every agent of its team is in (a channel the whole crew is
 * in, like general, says nothing about this one).
 */
export function roomsOf(agentId: string, team: readonly string[], rooms: readonly CrewRoom[]): { name: string; kind: string }[] {
  return rooms.filter((r) => r.agents.includes(agentId) && !team.every((id) => r.agents.includes(id))).map((r) => ({ name: r.name, kind: r.kind }));
}

/** The agents whose models run on this PC: the ones the local server names as its users, and those on its provider. */
export function localIds(agents: readonly Pick<Agent, 'id' | 'providerId'>[], local: { ids: string[]; providerId: string } | undefined): Set<string> {
  const out = new Set(local?.ids ?? []);
  if (local?.providerId) for (const a of agents) if (a.providerId === local.providerId) out.add(a.id);
  return out;
}

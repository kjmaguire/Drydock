import type { ApprovalDetail } from '../../api/inboxClient.ts';
import type { RunningScan } from '../../api/securityTypes.ts';
import type { NeedsYouItem, TaskSummary } from '../../api/types.ts';
import { agoWords, plural } from '../../lib/format.ts';
import { routes } from '../../routes.ts';

export const KIND_LABEL: Record<string, string> = {
  approval: 'Approval',
  review: 'Review',
  question: 'Question',
  failed_check: 'Failed check',
  watchdog_pause: 'Watchdog pause',
  chat_permission: 'Chat permission',
  budget: 'Budget',
  task_waiting: 'Task waiting',
  mention: 'Mention',
  join_request: 'Request to join',
  agent: 'Agent',
  connection: 'Agent CLI',
};

/** Where clicking an item goes. Items are derived by the core on every query; nothing here is stored. */
export function needsTarget(it: NeedsYouItem): string {
  // a chat's CLI waiting to be allowed a tool call: Allow once and Refuse are in the chat
  if (it.chatId) return routes.chat.href({ id: it.chatId });
  // a budget's warning or limit is raised on the Budgets page; a run waiting on a budget pause is answered on its task's page
  if (it.kind === 'budget' && it.budgetState !== 'paused') return routes.budgets.href();
  // a tool call an agent wants to make: its exact arguments and the Approve button are on the project's Integrations page
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return routes.projectIntegrations.href();
  // decision P130: a mention is read in its room (a task room too), an agent's state on its page, a CLI on the Agents page
  if (it.kind === 'mention' && it.roomId) return routes.room.href({ id: it.roomId });
  if (it.kind === 'agent' && it.agentId) return routes.agent.href({ id: it.agentId });
  if (it.kind === 'connection') return routes.agents.href({ key: it.connectionKey || it.adapter || undefined });
  if (it.taskId) return routes.task.href({ id: it.taskId });
  if (it.roomId) return routes.room.href({ id: it.roomId });
  // an approval with no task is an outbound write to an integration: its exact text is on the project's Integrations page
  if (it.kind === 'approval') return routes.projectIntegrations.href();
  return routes.needsYou.href();
}

/** A plain-language hint for the kinds of item that need more than a click. Derived from the title; the title is outside text, so it only selects a fixed hint. */
export function needsHint(it: NeedsYouItem): string {
  // a budget item says what to do in the core's own words (fixed sentences per state, P118); these stand in if it says none
  if (it.kind === 'budget') return it.detail || BUDGET_HINT[it.budgetState ?? ''] || BUDGET_HINT.exceeded!;
  if (it.kind === 'approval' && /^Budget limit/i.test(it.title)) return 'A budget stopped the next agent turn. Open the task to raise the limit and resume, or stop the run.';
  if (it.kind === 'approval' && /^The repository's git configuration changed/i.test(it.title))
    return "Drydock stopped running git in the repository: its git configuration, hooks or attributes changed. Open the task to see which items changed; trust them only if you made or expect the change.";
  if (it.kind === 'review' && /^Accept the spec/i.test(it.title)) return 'The spec gate: nothing is built until you accept the spec.';
  if (/^Iteration limit reached|^The reviewer keeps asking for the same change/i.test(it.title))
    return 'The task cannot go on by itself. Open it to continue with a fresh loop budget (and guidance, if you like) or to stop it.';
  if (/^The team lead asks for a human decision/i.test(it.title)) return "The team lead stopped and asks you. Open the task to read its recommendation, then continue with guidance or stop.";
  if (/^Watchdog paused/i.test(it.title)) return 'The watchdog stopped an agent turn. Open the task to see why, then send guidance and continue, or stop.';
  // the question a blocking gate asks when none of its checks could judge (pipeline.noVerdict, gate "verdict")
  if (it.kind === 'question' && /^No check gave a result for /i.test(it.title))
    return 'No check could judge this work, so nothing says it is fine. Open the task to check again, go on without a result this time, or stop.';
  if (it.kind === 'approval' && /^mcp_call:/.test(it.title)) return "An agent wants to call a tool that is not marked read-only. Open the project's Integrations page to see the exact call and approve or reject it.";
  if (it.kind === 'approval' && /^Permission request: /.test(it.title))
    return "A worker asks to use a tool its rules do not allow, and its turn waits. Open the task to see the exact tool and input; allow it once or refuse.";
  if (it.kind === 'join_request') return 'Let it in as an observer: it reads the channel and answers only when mentioned. Or decline; it never joins by itself.';
  if (it.kind === 'chat_permission') return "A chat's agent asks to use a tool its rules do not allow, and its turn waits. Open the chat to see the exact call; allow it once or refuse. Nobody answering refuses it after 30 minutes.";
  if (it.kind === 'approval' && !it.taskId && !it.roomId) return "An outbound write to a connected service. Open the project's Integrations page to read the exact text and approve or decline.";
  return '';
}

const BUDGET_HINT: Record<string, string> = {
  warning: 'A budget passed its warning point. Agents keep working; at the limit the next agent turn waits for you. Raise the limit on the Budgets page if this work should go on.',
  exceeded: 'A budget is at its limit: agent turns it counts toward do not start. Raise the limit on the Budgets page.',
  paused: 'A budget stopped the next agent turn. Open the task to raise the limit and resume, or stop the run.',
};

/** The agent an item is about: the one that raised it, else the task's assignee. */
export const needsAgent = (it: NeedsYouItem): string => it.agentId || it.context?.agent || '';

/**
 * The context line of a task's item, in parts (UI review P1-1): the change's size, the checks and the reviewer's verdict. The agent
 * is shown beside it with its avatar. Empty when the item is not of a task or nothing was recorded yet.
 */
export function needsContextParts(it: NeedsYouItem): string[] {
  const c = it.context;
  if (!c) return [];
  const out: string[] = [];
  if (c.hasDiff) out.push(`${plural(c.files, 'file')} +${c.insertions} \u2212${c.deletions}`);
  if (c.checksTotal > 0) out.push(`${c.checksPassed}/${c.checksTotal} checks passed`);
  if (c.verdict) out.push(`reviewer ${verdictWords(c.verdict)}`);
  return out;
}

/** The reviewer's verdict in words, never the core's SHIP or REVISE (the UI audit of 2026-10-26); the card says it the same way. */
export function verdictWords(v: string): string {
  return v === 'SHIP' ? 'approved' : v === 'REVISE' ? 'asked for changes' : v.toLowerCase().replaceAll('_', ' ');
}

// --- the Inbox (graphite, decision P97): every project's items, what runs and what was done today ---

/** What an item is, for its icon tile and its words: a spec gate and a review and merge are both reviews to the core. */
export type NeedsLook =
  | 'approval'
  | 'spec'
  | 'merge'
  | 'plan'
  | 'review'
  | 'question'
  | 'failed_check'
  | 'watchdog'
  | 'chat'
  | 'budget'
  | 'task'
  | 'mention'
  | 'join'
  | 'agent'
  | 'connection';

export function needsLook(it: Pick<NeedsYouItem, 'kind' | 'phase'>): NeedsLook {
  switch (it.kind) {
    case 'review':
      return it.phase === 'spec' ? 'spec' : it.phase === 'merge' ? 'merge' : it.phase === 'plan' ? 'plan' : 'review';
    case 'question':
      return 'question';
    case 'failed_check':
      return 'failed_check';
    case 'watchdog_pause':
      return 'watchdog';
    case 'chat_permission':
      return 'chat';
    case 'budget':
      return 'budget';
    case 'task_waiting':
      return 'task';
    case 'mention':
      return 'mention';
    case 'join_request':
      return 'join';
    case 'agent':
      return 'agent';
    case 'connection':
      return 'connection';
    default:
      return 'approval';
  }
}

/** The item's kind in the words of a row's subtitle ("approval", "spec gate", "ready to merge"). */
export const LOOK_WORDS: Record<NeedsLook, string> = {
  approval: 'approval',
  spec: 'spec gate',
  merge: 'ready to merge',
  plan: 'plan to review',
  review: 'review',
  question: 'question',
  failed_check: 'failed check',
  watchdog: 'watchdog pause',
  chat: 'chat permission',
  budget: 'budget',
  task: 'task waiting',
  mention: 'mention',
  join: 'request to join',
  agent: 'agent',
  connection: 'agent CLI',
};

/** A task's key, "GOC-41": the project's key and the task's number; empty when either is unknown. */
export const taskKey = (projectKey: string | undefined, seq: number | undefined): string => (projectKey && seq ? `${projectKey}-${seq}` : '');


/** "in 58 min", "in 2 h", "in 1 d"; "now" once it has passed. */
export function untilWords(ms: number, now = Date.now()): string {
  const s = Math.floor((ms - now) / 1000);
  if (s <= 0) return 'now';
  if (s < 3600) return `in ${Math.max(1, Math.floor(s / 60))} min`;
  if (s < 86400) return `in ${Math.floor(s / 3600)} h`;
  return `in ${Math.floor(s / 86400)} d`;
}

/** Elapsed time as a clock: "0:58", "4:12", "1:02:03". */
export function clock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/** An action hash shortened for reading: its first 8 and last 5 characters. */
export const shortHash = (h: string): string => (h.length > 16 ? `${h.slice(0, 8)}…${h.slice(-5)}` : h);

/** A time the core sent as RFC 3339 text or as milliseconds (some fields are seconds), in milliseconds; 0 when there is none. */
export function msOf(v: string | number | undefined): number {
  if (typeof v === 'number') return v > 0 && v < 1e11 ? v * 1000 : v;
  const n = v ? Date.parse(v) : NaN;
  return Number.isFinite(n) ? n : 0;
}

/** The start of today, local time. */
export function startOfToday(now = Date.now()): number {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

const PERMISSION_PREFIX = 'Permission request: ';

/**
 * A row's title: what the person decides. A spec gate is the task's title and "the spec is ready", a review and merge the task's
 * title and how its checks went, a worker's permission request who asks for what; anything else the core's own title.
 */
export function needsRowTitle(it: NeedsYouItem, summary?: Pick<TaskSummary, 'title'>): string {
  const look = needsLook(it);
  const task = summary?.title;
  if (look === 'spec' && task) return `${task}: the spec is ready`;
  if (look === 'merge' && task) {
    const c = it.context;
    return c && c.checksTotal > 0 ? `${task}: ${c.checksPassed} of ${c.checksTotal} checks passed` : `${task}: ready to merge`;
  }
  if (look === 'approval' && it.title.startsWith(PERMISSION_PREFIX)) return `${it.agentName || 'An agent'} asks to use ${it.title.slice(PERMISSION_PREFIX.length)}`;
  return it.title;
}

const titleCase = (s: string): string => s.charAt(0).toUpperCase() + s.slice(1);

/** A row's subtitle: "gocalc · approval · asked 2 min ago", "gocalc · spec gate · Feature template · 6 min ago". */
export function needsSubtitle(it: NeedsYouItem, projectName: string | undefined, summary?: Pick<TaskSummary, 'template'>, now = Date.now()): string {
  const look = needsLook(it);
  const when = agoWords(msOf(it.createdAt), now);
  const asked = look === 'approval' || look === 'question' || look === 'chat' || look === 'join';
  const parts = [projectName, LOOK_WORDS[look]];
  if (look === 'spec' && summary?.template) parts.push(`${titleCase(summary.template)} template`);
  else if (!asked && it.agentName) parts.push(it.agentName);
  parts.push(when && asked ? `asked ${when}` : when);
  return parts.filter(Boolean).join(' · ');
}

/**
 * Whether an item can be marked done (needsyou.ack): a failed check, a watchdog pause, a question in a room, a budget's warning or
 * limit (it comes back if the budget fires again after a change), a mention, an agent that failed or needs attention (P130). A
 * request (a budget pause, a request to join among them) is answered instead; a task waiting or a CLI not ready goes when it
 * changes.
 */
export const canAck = (it: Pick<NeedsYouItem, 'key'>): boolean => /^(question|check|watchdog|budget|mention|agent):/.test(it.key);

/** The agent's latest step as a row says it after the title: "editing limiter.go". */
export const doingWords = (doing: string | undefined): string => (doing ? doing.charAt(0).toLowerCase() + doing.slice(1) : '');

/** How far a task has come: its step of the steps it has, 0 to 1; undefined when the core says nothing. */
export function stepFraction(s: Pick<TaskSummary, 'stepIndex' | 'stepCount'>): number | undefined {
  return s.stepCount > 0 ? Math.min(1, Math.max(0, s.stepIndex / s.stepCount)) : undefined;
}

const ENGINE_DONE = ['done', 'skipped', 'errored', 'cancelled'];

/** A running scan's row: the engine at work and how many of its engines finished. */
export function scanProgress(scan: Pick<RunningScan, 'engines'>): { engine: string; done: number; total: number } {
  const engines = scan.engines;
  const running = engines.find((e) => e.status === 'running') ?? engines.find((e) => e.status === 'pending');
  return { engine: running ? `${running.name} running` : '', done: engines.filter((e) => ENGINE_DONE.includes(e.status)).length, total: engines.length };
}

/** How the right pane answers a request (approval.get), from what the core says of it. */
export type Answer =
  /** A worker's tool permission request: Approve (for the task when the core allows it), Approve once, Deny. */
  | { kind: 'permission'; runId: string; forTask: boolean; note?: string }
  /** A plain workflow approval: Approve or Deny on its run. */
  | { kind: 'approval'; runId: string }
  /** A spec gate: Accept the spec as written (editing the criteria and sending it back are on the task's page). */
  | { kind: 'spec'; runId: string }
  /** A review and merge: Merge (asking for changes, with the feedback, is on the task's page). */
  | { kind: 'merge'; runId: string }
  /** Decided on its page (a budget pause, a CI gate, a question to answer, an outbound write...), or decided already. */
  | { kind: 'open' };

/** Whether a request's payload is one a special panel of the task's page answers (a budget pause, a gate, a limit, a watchdog pause). */
function special(payload: unknown): boolean {
  if (!payload || typeof payload !== 'object') return false;
  const v = payload as Record<string, unknown>;
  return (typeof v.gate === 'string' && v.gate !== '') || v.phase === 'limit' || (typeof v.signal === 'string' && v.signal !== '' && typeof v.stage === 'number');
}

export function answerOf(d: ApprovalDetail): Answer {
  if (d.status !== 'REQUESTED' || !d.runId) return { kind: 'open' };
  if (d.gate === 'permission_prompt') return { kind: 'permission', runId: d.runId, forTask: d.approveForTask, note: d.approveForTask ? undefined : d.approveForTaskNote };
  if (special(d.payload)) return { kind: 'open' };
  if (d.kind === 'review' && d.phase === 'spec') return { kind: 'spec', runId: d.runId };
  if (d.kind === 'review' && d.phase === 'merge') return { kind: 'merge', runId: d.runId };
  if (d.kind === 'approval') return { kind: 'approval', runId: d.runId };
  return { kind: 'open' };
}

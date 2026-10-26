// A task's agent activity as turns (decision P87): the typed events of one agent process (api/agui.gen.ts, modelled on
// AG-UI 1.0) folded into what the task page draws. Each RUN_STARTED opens a turn; its messages, reasoning and tool calls
// are listed in the order they happened; RUN_FINISHED and RUN_ERROR close it. A turn that failed (the core reads an agent's
// failed turn as RUN_ERROR) is an error, one RUN_FINISHED with the cancelled outcome was stopped before it finished, and only
// the success outcome (or none: AG-UI reads an absent outcome as success) is done. The plan is the newest PLAN snapshot.

import type { AguiEvent, PlanEntry } from '../../api/agui.gen.ts';

export interface ToolItem {
  kind: 'tool';
  id: string;
  name: string;
  /** The arguments as the agent sent them (JSON text, bounded by the core). */
  args: string;
  result?: string;
  isError: boolean;
  /** When the call started and when its result came (ms since the epoch, from the events). */
  at: number;
  endAt?: number;
}

/**
 * A wait for a person (a permission request) and, once answered, how (P88); or a decision nobody was asked for (drydock.decided:
 * a rule refused the call or allowed it for the task, or the agent refused it itself), which was never open.
 */
export interface WaitItem {
  kind: 'wait';
  id: string;
  /** What was asked about: the request's title, or the tool of a decision nobody was asked for. */
  title: string;
  expiresAt?: number;
  /** Whether a person was asked. A decision nobody was asked for has its outcome from the start. */
  asked: boolean;
  /**
   * The gate's outcome: approved, approved_with_edits, approved_for_task, rejected, timed_out, cancelled, denied; refused for a
   * call the agent refused itself. Absent while a person is asked.
   */
  outcome?: string;
  /** Why it was decided so, when the core said (a call refused without asking). */
  reason?: string;
  /** The rule that decided, when one did (a rule of Drydock's refused the call, or a task rule the person made allowed it). */
  rule?: string;
  at: number;
}

/** A message or the reasoning, with when it started and when its last part came (ms since the epoch). */
export interface TextItem {
  kind: 'text' | 'reasoning';
  id: string;
  text: string;
  at: number;
  endAt?: number;
}

export type ActivityItem = TextItem | ToolItem | WaitItem;

export type TurnStatus = 'running' | 'done' | 'cancelled' | 'error';

export interface Turn {
  runId: string;
  status: TurnStatus;
  startedAt: number;
  endedAt?: number;
  /** The agent's word for how the turn ended (end_turn, interrupted, ...); absent when its program ended during the turn. */
  stopReason?: string;
  error?: string;
  items: ActivityItem[];
}

/** Folds one thread's events into its turns, oldest first. Events that arrive outside a turn open one. */
export function turnsOf(events: readonly AguiEvent[]): Turn[] {
  const turns: Turn[] = [];
  let cur: Turn | undefined;
  const texts = new Map<string, TextItem>();
  const tools = new Map<string, ToolItem>();
  const waits = new Map<string, WaitItem>();
  const open = (runId: string, at: number): Turn => {
    cur = { runId, status: 'running', startedAt: at, items: [] };
    turns.push(cur);
    return cur;
  };
  const turn = (at: number): Turn => cur ?? open('', at);
  for (const e of events) {
    switch (e.type) {
      case 'RUN_STARTED':
        open(e.runId, e.timestamp);
        break;
      case 'RUN_FINISHED': {
        const t = turn(e.timestamp);
        t.status = e.outcome?.type === 'cancelled' ? 'cancelled' : 'done';
        t.endedAt = e.timestamp;
        if (e.metadata.drydock.stopReason) t.stopReason = e.metadata.drydock.stopReason;
        cur = undefined;
        break;
      }
      case 'RUN_ERROR': {
        const t = turn(e.timestamp);
        t.status = 'error';
        t.endedAt = e.timestamp;
        t.error = t.error ? `${t.error}; ${e.message}` : e.message;
        cur = undefined;
        break;
      }
      case 'TEXT_MESSAGE_START':
      case 'REASONING_MESSAGE_START': {
        const item: TextItem = { kind: e.type === 'TEXT_MESSAGE_START' ? 'text' : 'reasoning', id: e.messageId, text: '', at: e.timestamp };
        turn(e.timestamp).items.push(item);
        texts.set(e.messageId, item);
        break;
      }
      case 'TEXT_MESSAGE_CONTENT':
      case 'REASONING_MESSAGE_CONTENT': {
        let item = texts.get(e.messageId);
        if (!item) {
          // content whose start was before the events read: it still shows
          const made: TextItem = { kind: e.type === 'TEXT_MESSAGE_CONTENT' ? 'text' : 'reasoning', id: e.messageId, text: '', at: e.timestamp };
          turn(e.timestamp).items.push(made);
          texts.set(e.messageId, made);
          item = made;
        }
        item.text += e.delta;
        item.endAt = e.timestamp;
        break;
      }
      case 'TEXT_MESSAGE_END':
      case 'REASONING_MESSAGE_END': {
        const item = texts.get(e.messageId);
        if (item) item.endAt = e.timestamp;
        break;
      }
      case 'TOOL_CALL_START': {
        const item: ToolItem = { kind: 'tool', id: e.toolCallId, name: e.toolCallName, args: '', isError: false, at: e.timestamp };
        turn(e.timestamp).items.push(item);
        tools.set(e.toolCallId, item);
        break;
      }
      case 'TOOL_CALL_ARGS': {
        const item = tools.get(e.toolCallId);
        if (item) item.args += e.delta;
        break;
      }
      case 'TOOL_CALL_RESULT': {
        let item = tools.get(e.toolCallId);
        if (!item) {
          item = { kind: 'tool', id: e.toolCallId, name: '', args: '', isError: false, at: e.timestamp };
          turn(e.timestamp).items.push(item);
          tools.set(e.toolCallId, item);
        }
        item.result = e.content;
        item.isError = !!e.metadata.drydock.isError;
        item.endAt = e.timestamp;
        break;
      }
      case 'CUSTOM':
        if (e.name === 'drydock.interrupt') {
          const item: WaitItem = { kind: 'wait', id: e.value.id, title: e.value.title, expiresAt: e.value.expiresAt, asked: true, at: e.timestamp };
          turn(e.timestamp).items.push(item);
          waits.set(item.id, item);
        } else if (e.name === 'drydock.resume') {
          const item = waits.get(e.value.id);
          if (item) item.outcome = e.value.outcome;
        } else if (e.name === 'drydock.decided') {
          // nobody was asked: the decision is a wait that was never open, named after the call it is about ("Write: config.json"
          // when its card is known, as a wait names its request; the core names only the tool) or after the event
          const id = e.value.toolCallId || `d${e.metadata.drydock.seq}`;
          const call = e.value.toolCallId ? tools.get(e.value.toolCallId) : undefined;
          const title = call?.name && call.args ? `${call.name}: ${argsSummary(call.args)}` : e.value.tool;
          const item: WaitItem = { kind: 'wait', id, title, asked: false, outcome: e.value.outcome, reason: e.value.reason || undefined, rule: e.value.rule || undefined, at: e.timestamp };
          turn(e.timestamp).items.push(item);
        }
        break;
      default:
        break;
    }
  }
  return turns;
}

/**
 * How a wait ended, in words. A wait in a turn that ended with no answer (the answer came after it, or never) is over. The same
 * words head a decision nobody was asked for: "Refused by Drydock's rules: Bash", "Allowed by you for this task: Bash". A word
 * the core may add later is not hidden, but it is never shown bare: "Decided (some_new_outcome)".
 */
export function waitOutcomeText(outcome: string | undefined, running = true): string {
  switch (outcome) {
    case undefined:
    case '':
      return running ? 'Waiting for you' : 'Not answered before the turn ended';
    case 'approved':
      return 'Allowed once by you';
    case 'approved_with_edits':
      return 'Allowed by you with your changes';
    case 'approved_for_task':
      return 'Allowed by you for this task';
    case 'allowed_by_rule':
      return "Allowed by Drydock's rules";
    case 'rejected':
      return 'Refused by you';
    case 'timed_out':
      return 'Nobody answered in time, so it was refused';
    case 'cancelled':
      return 'Withdrawn before anyone answered';
    case 'denied':
      return "Refused by Drydock's rules";
    case 'refused':
      return 'Refused without asking';
    default:
      return `Decided (${outcome})`;
  }
}

/** Why a decision went as it did, for the quiet words after it: the core's reason, the rule that decided, or nothing. */
export function waitWhy(w: WaitItem): string | undefined {
  const parts = [w.reason, w.rule ? `rule: ${w.rule}` : undefined].filter((x): x is string => !!x);
  return parts.length ? parts.join('; ') : undefined;
}

/** The thread's current plan: the newest PLAN snapshot, or undefined when the agent made none. */
export function planOf(events: readonly AguiEvent[]): PlanEntry[] | undefined {
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]!;
    if (e.type === 'ACTIVITY_SNAPSHOT' && e.activityType === 'PLAN') return e.content.entries ?? [];
  }
  return undefined;
}

/** "3 of 5 done" for a plan. */
export function planProgress(plan: readonly PlanEntry[]): string {
  const done = plan.filter((p) => p.status === 'completed').length;
  return `${done} of ${plan.length} done`;
}

/** A turn in one line: "4 tool calls, 1 failed" and how it ended. */
export function turnSummary(t: Turn): string {
  const tools = t.items.filter((i): i is ToolItem => i.kind === 'tool');
  const failed = tools.filter((i) => i.isError).length;
  const parts = [tools.length === 1 ? '1 tool call' : `${tools.length} tool calls`];
  if (failed) parts.push(`${failed} failed`);
  const end = t.status === 'running' ? 'working' : t.status === 'done' ? 'done' : t.status === 'cancelled' ? 'stopped' : 'error';
  return `${parts.join(', ')} · ${end}`;
}

/** The arguments as a value when they are JSON, else the text. */
export function argsValue(args: string): unknown {
  if (!args) return undefined;
  try {
    return JSON.parse(args);
  } catch {
    return args;
  }
}

/** A tool call in one line: its most telling argument (a command, a path, a pattern, a URL, a query), else its arguments. */
export function argsSummary(args: string): string {
  const v = argsValue(args);
  if (v && typeof v === 'object' && !Array.isArray(v)) {
    const f = v as Record<string, unknown>;
    for (const k of ['command', 'file_path', 'path', 'filePath', 'pattern', 'url', 'query', 'description']) {
      const x = f[k];
      if (typeof x === 'string' && x.trim()) return clip(x.trim().replace(/\s+/g, ' '), 160);
    }
  }
  if (typeof v === 'string') return clip(v.replace(/\s+/g, ' '), 160);
  return clip(args.replace(/\s+/g, ' '), 160);
}

const clip = (s: string, n: number): string => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

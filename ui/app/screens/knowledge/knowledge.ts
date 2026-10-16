// Pure helpers of the Knowledge hub (decision P64): a requirement's coverage from its trace, who wrote a memory note, and
// the words on the source cards. Nothing here is invented: every value comes from a field the core sends.

import type { MemoryEntry, Pack, RequirementTrace, TraceTask } from '../../api/extTypes.ts';
import type { Agent, Message, Task } from '../../api/types.ts';
import type { Tone } from '../../ui/tokens.ts';

export interface Coverage {
  /** verified, covered, in progress, planned, failed, cancelled, no task yet */
  label: string;
  tone: Tone;
  /** The task the chip names (the one furthest along), when the requirement has one. */
  taskId?: string;
}

const DONE = new Set(['COMPLETED']);
const GONE = new Set(['FAILED', 'CANCELLED']);
const PLANNED = new Set(['BACKLOG', 'READY']);

/**
 * How far a requirement is covered, from its linked tasks: verified (the core checked every linked task's tests), covered
 * (a linked task completed), in progress (one is under way), planned (one exists but has not started), failed or cancelled
 * (the only linked tasks ended that way), or no task yet. The chip names the task furthest along.
 */
export function coverageOf(trace: Pick<RequirementTrace, 'tasks' | 'verified'>): Coverage {
  const tasks: TraceTask[] = trace.tasks ?? [];
  if (tasks.length === 0) return { label: 'no task yet', tone: 'muted' };
  const done = tasks.find((x) => DONE.has(x.status));
  if (done) return { label: trace.verified ? 'verified' : 'covered', tone: 'ok', taskId: done.taskId };
  const active = tasks.find((x) => !GONE.has(x.status) && !PLANNED.has(x.status));
  if (active) return { label: 'in progress', tone: 'warn', taskId: active.taskId };
  const planned = tasks.find((x) => PLANNED.has(x.status));
  if (planned) return { label: 'planned', tone: 'info', taskId: planned.taskId };
  const failed = tasks.find((x) => x.status === 'FAILED');
  if (failed) return { label: 'failed', tone: 'err', taskId: failed.taskId };
  return { label: 'cancelled', tone: 'muted', taskId: tasks[0]!.taskId };
}

/** The chip's words: "covered · #12" when the task's number is known, else just the label. */
export function coverageText(c: Coverage, tasks: readonly Pick<Task, 'id' | 'seq'>[]): string {
  const seq = c.taskId ? tasks.find((x) => x.id === c.taskId)?.seq : undefined;
  return seq === undefined ? c.label : `${c.label} · #${seq}`;
}

/** The traces of the requirements that are not archived. */
export const liveTraces = (traces: readonly RequirementTrace[]): RequirementTrace[] => traces.filter((x) => !x.requirement.archived);

/** How many requirements a completed task covers (covered or verified). */
export const coveredCount = (traces: readonly RequirementTrace[]): number => traces.filter((x) => coverageOf(x).tone === 'ok').length;

/** The line under the Requirements number. */
export function requirementsBlurb(traces: readonly RequirementTrace[]): string {
  if (traces.length === 0) return 'what the project must do';
  const n = coveredCount(traces);
  return n === 0 ? 'none covered by a task yet' : `${n} covered by tasks`;
}

export interface NoteAuthor {
  kind: 'agent' | 'human' | 'other';
  id: string;
  name: string;
}

/** Who wrote a memory note: the core records the agent's id, or "human:<name>" for a person. */
export function authorOf(source: string | undefined, agents: readonly Pick<Agent, 'id' | 'name'>[]): NoteAuthor | undefined {
  if (!source) return undefined;
  const agent = agents.find((a) => a.id === source);
  if (agent) return { kind: 'agent', id: agent.id, name: agent.name };
  if (source.startsWith('human:')) return { kind: 'human', id: source, name: source.slice('human:'.length) || 'you' };
  return { kind: 'other', id: source, name: source };
}

/** The latest notes, newest change first. */
export function latestNotes(entries: readonly MemoryEntry[], n: number): MemoryEntry[] {
  return [...entries].sort((a, b) => b.updated - a.updated || b.created - a.created).slice(0, n);
}

/** The line under the Memory number: how many a person accepted and how many wait for one. */
export function memoryBlurb(entries: readonly MemoryEntry[]): string {
  if (entries.length === 0) return 'notes the crew writes down';
  const accepted = entries.filter((e) => e.status === 'accepted').length;
  const proposed = entries.filter((e) => e.status === 'proposed').length;
  const parts = [`${accepted} accepted`];
  if (proposed > 0) parts.push(`${proposed} waiting for you`);
  return parts.join(' · ');
}

/** The pack titles, first three, or what packs are. */
export function packsBlurb(packs: readonly Pick<Pack, 'title'>[]): string {
  if (packs.length === 0) return 'reference material the crew reads';
  const names = packs.slice(0, 3).map((p) => p.title);
  return packs.length > 3 ? `${names.join(', ')} and ${packs.length - 3} more` : names.join(', ');
}

/** The messages people and agents posted (not the room's system notices). */
export const decisionMessages = (list: readonly Pick<Message, 'type' | 'author'>[]): number => list.filter((m) => m.type !== 'SYSTEM' && m.author.kind !== 'system').length;

/** "200+" when the page was full (the hub reads the latest page only). */
export const countText = (n: number, pageFull: boolean): string => (pageFull ? `${n}+` : String(n));

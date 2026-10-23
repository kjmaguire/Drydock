// The task page's Activity as compact steps (the graphite mockup "Task", decision P97): each turn's items (activity.ts) read
// the way a person follows the agent: "Read parse.go parse_test.go", "Edited parse.go +14 −3" with the first lines of the
// change, "Ran go test ./... · 1 failed" with the failing line, "Thought for 6 s". Pure: no DOM and no client. Everything here
// is text the core stored (redacted, bounded); nothing is interpreted as markup.

import type { AguiThread } from '../../api/agui.gen.ts';
import { editLines, toolLabel, type DiffLine } from '../chats/chat-tool.tsx';
import { argsSummary, argsValue, planOf, turnsOf, type TextItem, type ToolItem, type Turn, type WaitItem } from './activity.ts';

/** How a step is going: running while its turn runs and no result came, ok, failed, or no result reported. */
export type StepState = 'running' | 'ok' | 'failed' | 'unknown';

export type StepKind = 'read' | 'edit' | 'run' | 'search' | 'tool' | 'text' | 'thought' | 'wait';

interface StepBase {
  kind: StepKind;
  /** Unique within its turn. */
  id: string;
  /** When it started (ms since the epoch); 0 when unknown. */
  at: number;
  endAt?: number;
  state: StepState;
}

/** One or more reads in a row: the files, in the order read. */
export interface ReadStep extends StepBase {
  kind: 'read';
  files: string[];
  tools: ToolItem[];
}

/** An edit or a write: the file, the lines added and removed, and the first lines of the change. */
export interface EditStep extends StepBase {
  kind: 'edit';
  file: string;
  added: number;
  removed: number;
  /** At most HUNK_LINES lines of the change, in order. */
  hunk: DiffLine[];
  /** How many lines of the change the hunk leaves out. */
  more: number;
  tools: ToolItem[];
}

/** A command: what ran, how many tests failed (0 when it says none or is not a test run) and the line that tells why. */
export interface RunStep extends StepBase {
  kind: 'run';
  command: string;
  failed: number;
  failing: string;
  tools: ToolItem[];
}

export interface SearchStep extends StepBase {
  kind: 'search';
  pattern: string;
  tools: ToolItem[];
}

/** Any other tool: its readable name and its most telling argument. */
export interface ToolStep extends StepBase {
  kind: 'tool';
  label: string;
  summary: string;
  tools: ToolItem[];
}

export interface TextStep extends StepBase {
  kind: 'text';
  text: string;
}

export interface ThoughtStep extends StepBase {
  kind: 'thought';
  text: string;
  /** How long the agent thought, from the events' times; 0 when unknown. */
  ms: number;
}

export interface WaitStep extends StepBase {
  kind: 'wait';
  wait: WaitItem;
}

export type ActivityStep = ReadStep | EditStep | RunStep | SearchStep | ToolStep | TextStep | ThoughtStep | WaitStep;

/** How many lines of an edit are shown under it. */
export const HUNK_LINES = 6;

type Field = Record<string, unknown>;
const fields = (v: unknown): Field => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Field) : {});
const str = (f: Field, ...keys: string[]): string => {
  for (const k of keys) {
    const x = f[k];
    if (typeof x === 'string' && x.trim()) return x.trim();
  }
  return '';
};

/** The tool's own name without an MCP server prefix, lower-cased: "Read" and "mcp__fs__read_file" read as "read" and "read_file". */
const bareName = (name: string): string => (name.startsWith('mcp__') ? (name.split('__').pop() ?? name) : name).toLowerCase();

const READ = new Set(['read', 'read_file', 'readfile', 'view', 'cat', 'notebookread', 'read_text_file', 'fs_read_text_file']);
const EDIT = new Set(['edit', 'multiedit', 'write', 'write_file', 'writefile', 'edit_file', 'str_replace', 'str_replace_editor', 'create_file', 'notebookedit', 'write_text_file', 'fs_write_text_file', 'replace']);
const RUN = new Set(['bash', 'shell', 'run_shell_command', 'execute_command', 'exec', 'exec_command', 'powershell', 'terminal', 'run_command', 'local_shell']);
const SEARCH = new Set(['grep', 'glob', 'search', 'search_files', 'find', 'find_files', 'list_files', 'ls', 'codebase_search', 'search_file_content']);

/** Which kind of step a tool call is, by the tool's name. */
export function toolKind(name: string): 'read' | 'edit' | 'run' | 'search' | 'tool' {
  const n = bareName(name);
  if (READ.has(n)) return 'read';
  if (EDIT.has(n)) return 'edit';
  if (RUN.has(n)) return 'run';
  if (SEARCH.has(n)) return 'search';
  return 'tool';
}

/** The path a tool call is about. */
export function pathOf(args: string): string {
  const f = fields(argsValue(args));
  return str(f, 'file_path', 'path', 'filePath', 'notebook_path', 'file', 'filename', 'target_file');
}

/** The command a tool call runs (a command given as a list of words is joined). */
export function commandOf(args: string): string {
  const v = argsValue(args);
  const f = fields(v);
  const c = f.command ?? f.cmd ?? f.script;
  if (Array.isArray(c)) {
    const words = c.filter((w): w is string => typeof w === 'string');
    // ["bash", "-lc", "go test ./..."] is the shell running the last word
    if (words.length === 3 && /^(ba|z)?sh$|^pwsh$|^powershell(\.exe)?$/i.test(words[0]!) && /^-\w*c$|^-command$/i.test(words[1]!)) return words[2]!.trim();
    return words.join(' ').trim();
  }
  if (typeof c === 'string') return c.trim();
  return typeof v === 'string' ? v.trim() : '';
}

/** A path's last part: "internal/calc/parse.go" is "parse.go". The full path is kept for the title. */
export const baseName = (p: string): string => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p;

/**
 * The change an edit tool call makes, as lines removed and added: an edit's old and new strings (editLines), or a write's whole
 * new content. Undefined when the arguments carry neither.
 */
export function changeLines(args: string): DiffLine[] | undefined {
  const v = argsValue(args);
  const lines = editLines(v);
  if (lines) return lines;
  const f = fields(v);
  const content = typeof f.content === 'string' ? f.content : typeof f.contents === 'string' ? f.contents : typeof f.file_text === 'string' ? f.file_text : undefined;
  if (content === undefined) return undefined;
  return content === '' ? [] : content.replace(/\n$/, '').split('\n').map((text) => ({ kind: '+' as const, text }));
}

/** "+14 −3": the lines an edit adds and removes, and the first HUNK_LINES lines of it. */
export function editStat(args: string): { added: number; removed: number; hunk: DiffLine[]; more: number } {
  const lines = changeLines(args) ?? [];
  const added = lines.filter((l) => l.kind === '+').length;
  const removed = lines.length - added;
  const hunk = lines.slice(0, HUNK_LINES);
  return { added, removed, hunk, more: lines.length - hunk.length };
}

const clip = (s: string, n: number): string => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

/**
 * What a test run's output says failed: how many tests and the line that says why. Go's "--- FAIL: TestX/case" lines (a parent
 * whose subtest failed is not counted twice; the message under the failing test is joined to its name), pytest's "FAILED x"
 * lines, Jest and Vitest's "✕ name" lines or their "Tests: 2 failed" total. Nothing found: 0 and ''.
 */
export function testFailures(output: string): { failed: number; line: string } {
  const lines = output.split(/\r?\n/);
  // Go
  const goNames: { name: string; at: number }[] = [];
  lines.forEach((l, i) => {
    const m = /^\s*--- FAIL: (\S+)/.exec(l);
    if (m) goNames.push({ name: m[1]!, at: i });
  });
  if (goNames.length) {
    const leaves = goNames.filter((g) => !goNames.some((o) => o.name.startsWith(`${g.name}/`)));
    const first = leaves[0]!;
    let why = '';
    for (let i = first.at + 1; i < lines.length && i <= first.at + 6; i++) {
      const l = lines[i]!.trim();
      if (!l || /^--- (FAIL|PASS|SKIP)/.test(l) || /^(FAIL|ok)\b/.test(l)) break;
      why = l.replace(/^\S+_test\.go:\d+:\s*/, '');
      break;
    }
    return { failed: leaves.length, line: clip(why ? `${first.name}: ${why}` : first.name, 160) };
  }
  // pytest
  const py = lines.map((l) => /^FAILED (\S+)(?: - (.*))?/.exec(l.trim())).filter((m): m is RegExpExecArray => !!m);
  if (py.length) return { failed: py.length, line: clip(py[0]![2] ? `${py[0]![1]}: ${py[0]![2]}` : py[0]![1]!, 160) };
  // Jest, Vitest
  const js = lines.map((l) => /^\s*[✕×]\s+(.+?)(?:\s+\(\d+\s*m?s\))?$/.exec(l)).filter((m): m is RegExpExecArray => !!m);
  const total = /Tests:\s+(\d+) failed/.exec(output) ?? /Tests\s+(\d+) failed/.exec(output);
  if (js.length || total) return { failed: total ? Number(total[1]) : js.length, line: js[0] ? clip(js[0][1]!.trim(), 160) : '' };
  return { failed: 0, line: '' };
}

/** The last line of a command's output that says something: what a failed command left as its reason. */
export function lastLine(output: string): string {
  const l = output.split(/\r?\n/).map((x) => x.trim()).filter(Boolean).pop() ?? '';
  return clip(l, 160);
}

/** "6 s", "under 1 s", "2 min 5 s", "1 h 4 min": how long something took, in words; '' when unknown. */
export function secondsText(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return 'under 1 s';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  if (s < 3600) {
    const rest = s % 60;
    return rest ? `${Math.floor(s / 60)} min ${rest} s` : `${Math.floor(s / 60)} min`;
  }
  const m = Math.floor((s % 3600) / 60);
  return m ? `${Math.floor(s / 3600)} h ${m} min` : `${Math.floor(s / 3600)} h`;
}

/** "9:47" or "1:02:05": a running time as a clock (minutes and seconds, with hours once over an hour). */
export function clockText(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/** The time of day a step happened, as the reader's clock shows it (hours and minutes); '' when unknown. */
export function timeOfDay(ms: number): string {
  if (!ms) return '';
  const d = new Date(ms);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

const toolState = (i: ToolItem, running: boolean): StepState => (i.result !== undefined ? (i.isError ? 'failed' : 'ok') : running ? 'running' : 'unknown');

/** The state of steps made of several calls: running if one runs, failed if one failed, else no result if one has none, else ok. */
const worst = (states: StepState[]): StepState =>
  states.includes('running') ? 'running' : states.includes('failed') ? 'failed' : states.includes('unknown') ? 'unknown' : 'ok';

function toolStep(i: ToolItem, running: boolean): ActivityStep {
  const state = toolState(i, running);
  const base = { id: i.id, at: i.at, endAt: i.endAt, state, tools: [i] };
  switch (toolKind(i.name)) {
    case 'read':
      return { ...base, kind: 'read', files: [pathOf(i.args) || argsSummary(i.args)].filter(Boolean) };
    case 'edit':
      return { ...base, kind: 'edit', file: pathOf(i.args), ...editStat(i.args) };
    case 'run': {
      const out = i.result ?? '';
      const tf = testFailures(out);
      return { ...base, kind: 'run', command: commandOf(i.args) || argsSummary(i.args), failed: tf.failed, failing: tf.line || (state === 'failed' ? lastLine(out) : '') };
    }
    case 'search': {
      const f = fields(argsValue(i.args));
      return { ...base, kind: 'search', pattern: str(f, 'pattern', 'query', 'glob', 'regex', 'path') || argsSummary(i.args) };
    }
    default:
      return { ...base, kind: 'tool', label: toolLabel(i.name), summary: argsSummary(i.args) };
  }
}

/**
 * A turn's items as steps, in the order they happened. Reads that follow one another are one step ("Read a.go b.go"); a message
 * or a thought with no text yet is left out until its text comes.
 */
export function stepsOf(turn: Pick<Turn, 'items' | 'status'>): ActivityStep[] {
  const running = turn.status === 'running';
  const out: ActivityStep[] = [];
  for (const it of turn.items) {
    if (it.kind === 'tool') {
      const st = toolStep(it, running);
      const prev = out[out.length - 1];
      if (st.kind === 'read' && prev?.kind === 'read') {
        prev.files.push(...st.files);
        prev.tools.push(...st.tools);
        prev.state = worst(prev.tools.map((x) => toolState(x, running)));
        prev.endAt = st.endAt ?? prev.endAt;
        continue;
      }
      out.push(st);
    } else if (it.kind === 'wait') {
      out.push({ kind: 'wait', id: it.id, at: it.at, state: it.outcome ? 'ok' : running ? 'running' : 'unknown', wait: it });
    } else if (it.text.trim()) {
      out.push(textStep(it, running));
    }
  }
  return out;
}

function textStep(it: TextItem, running: boolean): ActivityStep {
  if (it.kind === 'text') return { kind: 'text', id: it.id, at: it.at, endAt: it.endAt, state: 'ok', text: it.text };
  const ms = it.endAt !== undefined && it.endAt >= it.at && it.at > 0 ? it.endAt - it.at : 0;
  return { kind: 'thought', id: it.id, at: it.at, endAt: it.endAt, state: running && it.endAt === undefined ? 'running' : 'ok', text: it.text, ms };
}

/** "Thought for 6 s", or "Thought" when the time is not known. */
export const thoughtText = (s: Pick<ThoughtStep, 'ms'>): string => (s.ms > 0 ? `Thought for ${secondsText(s.ms)}` : 'Thought');

/** The verb of a step: past for one that ended, present for one that runs. */
export function stepVerb(s: ActivityStep): string {
  const now = s.state === 'running';
  switch (s.kind) {
    case 'read':
      return now ? 'Reading' : 'Read';
    case 'edit':
      return now ? 'Editing' : s.tools.some((t) => bareName(t.name).startsWith('write') || bareName(t.name) === 'create_file') ? 'Wrote' : 'Edited';
    case 'run':
      return now ? 'Running' : 'Ran';
    case 'search':
      return now ? 'Searching' : 'Searched';
    case 'tool':
      return s.label;
    case 'thought':
      return now ? 'Thinking' : thoughtText(s);
    default:
      return '';
  }
}

// ---- the rows of the feed

/** One row of the Activity feed: an agent process, its plan, a turn's line, a step, or how a turn ended. */
export type ActivityRow =
  | { type: 'thread'; key: string; thread: AguiThread; turns: number; working: boolean }
  | { type: 'plan'; key: string; thread: AguiThread }
  | { type: 'turn'; key: string; thread: AguiThread; turn: Turn; n: number; open: boolean }
  | { type: 'step'; key: string; thread: AguiThread; step: ActivityStep; turn: Turn }
  | { type: 'end'; key: string; thread: AguiThread; turn: Turn };

/**
 * The feed: every agent process in the order it started, each with its plan (when it made one) and its turns. The newest turn of
 * each process is open; an earlier one is one line until it is opened. `toggled` holds the keys of the turn lines the reader
 * opened or closed (a turn's default is flipped). A process with one turn has no turn line: its steps follow the process.
 */
export function activityRows(threads: readonly AguiThread[], toggled: ReadonlySet<string> = new Set()): ActivityRow[] {
  const rows: ActivityRow[] = [];
  for (const th of threads) {
    const turns = turnsOf(th.events);
    const last = turns[turns.length - 1];
    rows.push({ type: 'thread', key: `th:${th.threadId}`, thread: th, turns: turns.length, working: last?.status === 'running' });
    const plan = planOf(th.events);
    if (plan && plan.length) rows.push({ type: 'plan', key: `plan:${th.threadId}`, thread: th });
    turns.forEach((turn, i) => {
      const key = `turn:${th.threadId}:${i}`;
      const open = (i === turns.length - 1) !== toggled.has(key);
      if (turns.length > 1) rows.push({ type: 'turn', key, thread: th, turn, n: i + 1, open });
      if (!open) return;
      for (const step of stepsOf(turn)) rows.push({ type: 'step', key: `st:${th.threadId}:${i}:${step.kind}:${step.id}`, thread: th, step, turn });
      if (turn.status === 'error' || turn.status === 'cancelled') rows.push({ type: 'end', key: `end:${th.threadId}:${i}`, thread: th, turn });
    });
  }
  return rows;
}

/** A turn line's own text: "Turn 2 · 4 tool calls, 1 failed · done" (turnSummary). */
export { turnSummary } from './activity.ts';

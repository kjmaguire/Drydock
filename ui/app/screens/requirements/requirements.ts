// Pure helpers for the Requirements screen.

import type { LinkSuggestion, TraceEvidence } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export const MAX_FILE_BYTES = 1_000_000;

/** Why a chosen file is refused before it is read, or '' when it is fine. */
export const fileProblem = (size: number): string => (size > MAX_FILE_BYTES ? 'That file is larger than 1 MB.' : '');

/** Whether a file is a document the core reads the text of (requirement.extract) rather than text read here. */
export const isDocument = (name: string): boolean => /\.(pdf|docx)$/i.test(name);

/** Whether a file is one Add requirements reads: a PDF or DOCX (by the core), or Markdown or plain text (here). An image or a
 * spreadsheet is not, so it is never read as text. */
export const isReadable = (name: string, type = ''): boolean => isDocument(name) || /\.(md|markdown|txt)$/i.test(name) || type.startsWith('text/');

/** Shown when a file of another kind is chosen or dropped. */
export const UNREADABLE = 'Choose a PDF, DOCX, text or Markdown file.';

const MAX_DOCUMENT_BYTES = 32 << 20;

/** Why a PDF or DOCX is not sent to be read ('' when it is): the core reads one up to 32 MB. */
export const documentProblem = (size: number): string => (size > MAX_DOCUMENT_BYTES ? 'That file is larger than 32 MB.' : '');

/** A title from a file name: checkout.md -> checkout. */
export const titleFromFile = (name: string): string => name.replace(/\.[^.]+$/, '');

/** One criterion per non-empty line, trimmed. */
export const criteriaLines = (s: string): string[] =>
  s
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);

/** Evidence outcome: PASS and FAIL only when the core recorded one; otherwise INFO. */
export function evidenceOutcome(e: Pick<TraceEvidence, 'passed'>): {
  label: 'PASS' | 'FAIL' | 'INFO';
  tone: Tone;
} {
  if (e.passed === true) return { label: 'PASS', tone: 'ok' };
  if (e.passed === false) return { label: 'FAIL', tone: 'err' };
  return { label: 'INFO', tone: 'muted' };
}

/** The distinct tasks among a requirement's links, in first-seen order. */
export const linkedTaskIds = (links: readonly { taskId: string }[]): string[] => Array.from(new Set(links.map((l) => l.taskId)));

// the core's test for a criterion it links (internal/requirements/trace.go, gwt): Given, then when, then then, in that order
const GWT = /\bgiven\b[\s\S]+\bwhen\b[\s\S]+\bthen\b[\s\S]+/i;

/** Whether the core takes a line as a Given/When/Then criterion. */
export const isGWT = (c: string): boolean => GWT.test(c.trim());

/**
 * What a Link sends: the task's criteria the person kept ticked, then the lines they wrote, each once. Lines the core would refuse
 * (not Given/When/Then) are named apart, so the button can say so before the click.
 */
export function linkCriteria(taskCriteria: readonly string[], ticked: ReadonlySet<number>, other: string): { criteria: string[]; refused: string[] } {
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (c: string) => {
    const k = c.trim().toLowerCase().split(/\s+/).join(' ');
    if (!seen.has(k)) seen.add(k), out.push(c.trim());
  };
  taskCriteria.forEach((c, i) => ticked.has(i) && add(c));
  const typed = criteriaLines(other);
  typed.forEach(add);
  return { criteria: out, refused: typed.filter((c) => !isGWT(c)) };
}

/** The Link button: "Link 3 criteria of #12". */
export const linkLabel = (n: number, seq?: number): string => (seq === undefined || n === 0 ? 'Link' : `Link ${n} ${n === 1 ? 'criterion' : 'criteria'} of #${seq}`);

/** Why a task is suggested for a requirement (X43): the part of the task whose words all occur in a passage, and the passage. */
export function suggestionWhy(sg: Pick<LinkSuggestion, 'matched' | 'heading'>): string {
  const what = sg.matched === 'criterion' ? 'a criterion' : 'its title';
  const where = sg.heading.split(' > ').pop() ?? '';
  return where ? `${what} matches "${where}"` : `${what} matches the text`;
}

/** Where the suggestions the person dismissed for a requirement are kept (this browser only). */
export const dismissedKey = (reqId: string) => `drydock.reqDismissed.${reqId}`;

/** The task ids whose suggestion the person dismissed for a requirement. */
export function dismissedSuggestions(reqId: string): Set<string> {
  try {
    const v = JSON.parse(localStorage.getItem(dismissedKey(reqId)) ?? '[]') as unknown;
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  } catch {
    return new Set();
  }
}

/** Dismisses a suggestion (kept in this browser; at most the last 200) and returns the dismissed set. */
export function dismissSuggestion(reqId: string, taskId: string): Set<string> {
  const out = dismissedSuggestions(reqId);
  out.add(taskId);
  try {
    localStorage.setItem(dismissedKey(reqId), JSON.stringify([...out].slice(-200)));
  } catch {
    // storage blocked: dismissed for this page only
  }
  return out;
}

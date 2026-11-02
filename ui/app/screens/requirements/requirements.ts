// Pure helpers for the Requirements screen.

import type { TraceEvidence } from '../../api/extTypes.ts';
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

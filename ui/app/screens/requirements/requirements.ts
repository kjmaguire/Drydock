// Pure helpers for the Requirements screen.

import type { TraceEvidence } from '../../api/extTypes.ts';
import type { Tone } from '../../ui/tokens.ts';

export const MAX_FILE_BYTES = 1_000_000;

/** Why a chosen file is refused before it is read, or '' when it is fine. */
export const fileProblem = (size: number): string => (size > MAX_FILE_BYTES ? 'That file is larger than 1 MB.' : '');

/** Whether a file is a document the core reads the text of (requirement.extract) rather than text read here. */
export const isDocument = (name: string): boolean => /\.(pdf|docx)$/i.test(name);

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

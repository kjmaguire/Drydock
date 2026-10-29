// The words and choices of "Import from Claude Code" (decision P109, docs/chats.md section 7): which sessions start chosen,
// how a session is described, and what an import did. Pure helpers; the dialog is chat-import.tsx.

import type { ImportCandidate, ImportList, ImportResult } from '../../api/chatClient.ts';
import { listWhen } from './chats.ts';

/** The key a session is chosen by: the same session id may only be imported once, from wherever it is. */
export const importKey = (c: Pick<ImportCandidate, 'sessionId' | 'wslDistro'>): string => `${c.wslDistro}\u0000${c.sessionId}`;

/** A session can be imported when it was not imported yet and names a folder it can continue in. */
export const importable = (c: ImportCandidate): boolean => !c.chatId && !c.problem;

/** The sessions chosen when the dialog opens: every one not imported yet. */
export function defaultSelection(list: Pick<ImportList, 'sessions'>): Set<string> {
  return new Set(list.sessions.filter(importable).map(importKey));
}

/** Where a session ran: Windows, or the WSL distro. */
export const importWhere = (c: Pick<ImportCandidate, 'wslDistro'>): string => (c.wslDistro ? `WSL: ${c.wslDistro}` : 'Windows');

/** The line under a session's title: where it ran, its project (or folder), how long it is and when it was last active. */
export function importLine(c: ImportCandidate, projectName: string, now = Date.now()): string {
  const parts = [importWhere(c), projectName ? `in ${projectName}` : c.cwd || 'no folder', `${c.messages} ${c.messages === 1 ? 'message' : 'messages'}`];
  const when = listWhen(c.lastAt, now);
  if (when) parts.push(when);
  return parts.join(' · ');
}

/** The import button: "Import 3 chats", "Import 1 chat", "Import" with none chosen. */
export const importButton = (n: number): string => (n ? `Import ${n} ${n === 1 ? 'chat' : 'chats'}` : 'Import');

/** What an import did, in one sentence. */
export function importSummary(r: ImportResult): string {
  const n = r.imported.length;
  const done = n === 0 ? 'No chat was imported.' : `Imported ${n} ${n === 1 ? 'chat' : 'chats'}.`;
  const again = r.skipped.filter((s) => s.chatId).length;
  const other = r.skipped.length - again;
  const notes: string[] = [];
  if (again) notes.push(`${again} ${again === 1 ? 'was' : 'were'} imported before`);
  if (other) notes.push(`${other} could not be imported`);
  return notes.length ? `${done} ${capital(notes.join(', '))}.` : done;
}

const capital = (s: string): string => s.charAt(0).toUpperCase() + s.slice(1);

/** The folders Drydock looked in, for "nothing found" (a source it could not read says so on its own). */
export function importFolders(list: ImportList): string {
  return list.sources
    .filter((s) => s.folder && !s.error)
    .map((s) => (s.wslDistro ? `${s.folder} (WSL: ${s.wslDistro})` : s.folder))
    .join(', ');
}

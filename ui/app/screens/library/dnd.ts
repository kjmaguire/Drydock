// Drag and drop between the Library and the projects (native HTML5 drag events). Everything draggable also has a button or a
// list, so nothing needs a mouse.

export type Dragged = { kind: 'mcp'; id: string } | { kind: 'template'; id: string };

export const DRAG_MIME = 'application/x-drydock-item';

/** The part of a DragEvent these helpers use (a DragEvent in the browser; a plain object in a unit test). */
export interface DragLike {
  dataTransfer: Pick<DataTransfer, 'setData' | 'getData' | 'effectAllowed'> | null;
}

export function startDrag(e: DragLike, item: Dragged): void {
  if (!e.dataTransfer) return;
  e.dataTransfer.effectAllowed = 'copy';
  e.dataTransfer.setData(DRAG_MIME, JSON.stringify(item));
  // some browsers only start a drag when text is set
  e.dataTransfer.setData('text/plain', `${item.kind}:${item.id}`);
}

/** What was dropped, or undefined for anything that is not one of our items (a file, a link, text from another page). */
export function readDrag(e: DragLike): Dragged | undefined {
  try {
    const raw = e.dataTransfer?.getData(DRAG_MIME);
    if (!raw) return undefined;
    const v = JSON.parse(raw) as { kind?: unknown; id?: unknown };
    if ((v.kind === 'mcp' || v.kind === 'template') && typeof v.id === 'string' && v.id !== '') return { kind: v.kind, id: v.id };
  } catch {
    /* not ours */
  }
  return undefined;
}

/** The note after a template was applied: who joined where, what was granted and what was not. */
export function joinedNote(agentName: string, projectName: string, granted: readonly string[], skipped: readonly string[], serverName: (id: string) => string): string {
  return `${agentName} joined ${projectName}${granted.length ? `, with ${granted.map(serverName).join(', ')}` : ''}${skipped.length ? `. Not granted: ${skipped.join('; ')}` : ''}.`;
}

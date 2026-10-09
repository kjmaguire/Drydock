import type { DragEvent } from 'react';

// Drag and drop between the Library and the projects. Everything draggable also has a button or a list, so nothing needs a mouse.

export type Dragged = { kind: 'mcp'; id: string } | { kind: 'template'; id: string };

const MIME = 'application/x-drydock-item';

export function startDrag(e: DragEvent, item: Dragged): void {
  e.dataTransfer.effectAllowed = 'copy';
  e.dataTransfer.setData(MIME, JSON.stringify(item));
  // some browsers only start a drag when text is set
  e.dataTransfer.setData('text/plain', `${item.kind}:${item.id}`);
}

/** What was dropped, or undefined for anything that is not one of our items (a file, a link, text from another page). */
export function readDrag(e: DragEvent): Dragged | undefined {
  try {
    const raw = e.dataTransfer.getData(MIME);
    if (!raw) return undefined;
    const v = JSON.parse(raw) as { kind?: unknown; id?: unknown };
    if ((v.kind === 'mcp' || v.kind === 'template') && typeof v.id === 'string' && v.id !== '') return { kind: v.kind, id: v.id };
  } catch {
    /* not ours */
  }
  return undefined;
}

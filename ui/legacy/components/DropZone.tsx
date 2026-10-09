import { useState, type DragEvent, type ReactNode } from 'react';
import { readDrag, type Dragged } from '../lib/dnd';

/** A place things can be dropped. `dragging` is what is being dragged right now (the browser hides the data until the drop). */
export function DropZone({
  accepts,
  dragging,
  onDropItem,
  label,
  className = '',
  children,
  testId,
}: {
  accepts: Dragged['kind'][];
  dragging: Dragged | undefined;
  onDropItem: (item: Dragged) => void;
  label: string;
  className?: string;
  children: ReactNode;
  testId?: string;
}) {
  const [over, setOver] = useState(false);
  const live = dragging !== undefined && accepts.includes(dragging.kind);
  const onOver = (e: DragEvent) => {
    if (!live) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
    setOver(true);
  };
  return (
    <div
      className={`dropzone${live ? ' can-drop' : ''}${over && live ? ' over' : ''} ${className}`.trim()}
      data-testid={testId}
      data-can-drop={live}
      aria-label={label}
      onDragEnter={onOver}
      onDragOver={onOver}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        setOver(false);
        const item = readDrag(e);
        if (!item || !accepts.includes(item.kind)) return;
        e.preventDefault();
        e.stopPropagation();
        onDropItem(item);
      }}
    >
      {children}
    </div>
  );
}

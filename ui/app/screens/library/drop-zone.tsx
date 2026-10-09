// A place Library items can be dropped. `dragging` is what is being dragged right now (the browser hides the data until the
// drop, so the page tells the zones). A zone that does not take the dragged kind lets the event through to the zone around it:
// a template dropped on an agent row lands in that agent's project.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { t } from '../../ui/tokens.ts';
import { readDrag, type Dragged } from './dnd.ts';

const zoneCss = css({
  position: 'relative',
  borderRadius: t.rLg,
  outline: '2px dashed transparent',
  outlineOffset: '2px',
  transition: `outline-color ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&[data-can-drop="true"]': { outlineColor: `color-mix(in srgb, ${t.accent} 45%, transparent)` },
  '&[data-over="true"]': { outlineColor: t.accent, outlineStyle: 'solid', background: t.accentSoft },
});
const agentZoneCss = css({ borderRadius: t.rMd, outlineOffset: '0' });

// Events an inner zone already highlighted for, so the zone around it does not light up as well.
const claimed = new WeakSet<Event>();

export function DropZone(
  handle: Handle<{
    accepts: Dragged['kind'][];
    dragging: Dragged | undefined;
    onDropItem: (item: Dragged) => void;
    label: string;
    testId?: string;
    inner?: boolean;
    children?: RemixNode;
  }>,
) {
  let over = false;
  const live = () => handle.props.dragging !== undefined && handle.props.accepts.includes(handle.props.dragging.kind);
  const setOver = (v: boolean) => {
    if (over === v) return;
    over = v;
    void handle.update();
  };
  const onOver = (e: DragEvent) => {
    if (!live()) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    if (claimed.has(e)) return setOver(false);
    claimed.add(e);
    setOver(true);
  };

  return () => {
    const { label, testId, inner, children, accepts, onDropItem } = handle.props;
    const can = live();
    if (!can) over = false;
    return (
      <div
        data-testid={testId}
        data-can-drop={String(can)}
        data-over={String(over && can)}
        aria-label={label}
        mix={[
          zoneCss,
          on<HTMLDivElement, 'dragenter'>('dragenter', onOver),
          on<HTMLDivElement, 'dragover'>('dragover', onOver),
          on<HTMLDivElement, 'dragleave'>('dragleave', (e) => {
            const to = e.relatedTarget as Node | null;
            if (to && e.currentTarget.contains(to)) return;
            setOver(false);
          }),
          on<HTMLDivElement, 'drop'>('drop', (e) => {
            setOver(false);
            const item = readDrag(e);
            if (!item || !accepts.includes(item.kind)) return;
            e.preventDefault();
            e.stopPropagation();
            onDropItem(item);
          }),
          inner ? agentZoneCss : undefined,
        ]}
      >
        {children}
      </div>
    );
  };
}

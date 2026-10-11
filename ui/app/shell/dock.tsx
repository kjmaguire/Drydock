// The bottom dock (docs/ui.md 2): Terminal, Diff, Logs and Activity for the task in context (Diff and Logs need one). Its
// open state, tab and height are kept per project. Ctrl+J toggles it; Ctrl+Shift+D opens the diff.

import { css, on, type Handle } from 'remix/component';
import { ChevronDown, ChevronUp, FileDiff, ScrollText, SquareTerminal, BarChart3 } from 'lucide';
import { services, watch } from '../state/services.tsx';
import type { DockTab } from '../state/ui.ts';
import { ActivityPanel } from '../screens/dock/activity-panel.tsx';
import { DiffViewer } from '../screens/dock/diff-viewer.tsx';
import { LogsPanel } from '../screens/dock/logs-panel.tsx';
import { TerminalPanel } from '../screens/dock/terminal-panel.tsx';
import { IconButton } from '../ui/button.tsx';
import { Icon, type IconNode } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';

const TABS: { id: DockTab; label: string; icon: IconNode }[] = [
  { id: 'terminal', label: 'Terminal', icon: SquareTerminal },
  { id: 'diff', label: 'Diff', icon: FileDiff },
  { id: 'logs', label: 'Logs', icon: ScrollText },
  { id: 'activity', label: 'Activity', icon: BarChart3 },
];

const dockCss = css({
  position: 'relative',
  display: 'flex',
  flexDirection: 'column',
  flex: 'none',
  borderTop: `1px solid ${t.border}`,
  background: t.bgSidebar,
  minHeight: '0',
});
const resizeCss = css({
  position: 'absolute',
  top: '-3px',
  left: '0',
  right: '0',
  height: '6px',
  cursor: 'row-resize',
  zIndex: '1',
  '&:hover, &:focus-visible': { background: t.accent, opacity: '0.5' },
});
const headCss = css({ display: 'flex', alignItems: 'center', gap: '2px', height: '34px', padding: `0 ${t.s2}`, flex: 'none' });
const tabCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '26px',
  minHeight: '26px',
  padding: `0 ${t.s2}`,
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '500',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-selected="true"]': { background: t.bgActive, color: t.text },
});
const panelCss = css({ flex: '1', minHeight: '0', overflow: 'auto', borderTop: `1px solid ${t.border}`, background: t.bgApp });
const hintCss = css({ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2, color: t.textFaint, fontSize: t.textXs });
const needCss = css({ padding: t.s4, color: t.textMuted });

export function Dock(handle: Handle<{ projectId: string | undefined; taskId: string | undefined }>) {
  const s = services(handle);
  watch(handle, s.ui);
  let drag: { y: number; h: number } | undefined;
  const MIN_H = 120;
  const maxH = () => Math.max(MIN_H, Math.round(window.innerHeight * 0.8));

  return () => {
    const { projectId, taskId } = handle.props;
    const d = s.ui.dock;
    return (
      <section mix={dockCss} aria-label="Dock" style={d.open ? { height: `${d.height}px` } : undefined}>
        {d.open ? (
          <div
            role="separator"
            aria-orientation="horizontal"
            aria-label="Resize the dock"
            aria-valuemin={MIN_H}
            aria-valuemax={maxH()}
            aria-valuenow={d.height}
            aria-valuetext={`${d.height} pixels high`}
            tabIndex={0}
            mix={[
              resizeCss,
              on('pointerdown', (e) => {
                drag = { y: e.clientY, h: d.height };
                e.currentTarget.setPointerCapture?.(e.pointerId);
              }),
              on('pointermove', (e) => {
                if (!drag) return;
                const h = Math.min(Math.max(drag.h + (drag.y - e.clientY), MIN_H), maxH());
                s.ui.setDock({ height: h }, false);
              }),
              on('pointerup', () => {
                if (drag) s.ui.setDock({}, true);
                drag = undefined;
              }),
              on('keydown', (e) => {
                if (e.key === 'ArrowUp') s.ui.setDock({ height: Math.min(d.height + 40, maxH()) });
                if (e.key === 'ArrowDown') s.ui.setDock({ height: Math.max(d.height - 40, MIN_H) });
              }),
            ]}
          />
        ) : null}
        <div mix={headCss}>
          <div role="tablist" aria-label="Dock" style={{ display: 'flex', gap: '2px' }}>
            {TABS.map((tb) => (
              <button
                key={tb.id}
                type="button"
                role="tab"
                id={`dock-tab-${tb.id}`}
                aria-selected={d.open && d.tab === tb.id}
                aria-controls={d.open ? 'dock-panel' : undefined}
                mix={[tabCss, on('click', () => s.ui.setDock({ open: !(d.open && d.tab === tb.id), tab: tb.id }))]}
              >
                <Icon icon={tb.icon} size={14} />
                {tb.label}
              </button>
            ))}
          </div>
          <div mix={hintCss}>
            <span>
              <kbd>Ctrl</kbd> <kbd>J</kbd>
            </span>
            <IconButton icon={d.open ? ChevronDown : ChevronUp} label={d.open ? 'Hide dock' : 'Show dock'} aria-expanded={d.open} title="Ctrl+J" mix={on('click', () => s.ui.toggleDock())} />
          </div>
        </div>
        {d.open ? (
          <div mix={panelCss} id="dock-panel" role="tabpanel" aria-labelledby={`dock-tab-${d.tab}`}>
            {d.tab === 'terminal' ? <TerminalPanel projectId={projectId} taskId={taskId} /> : null}
            {d.tab === 'diff' ? taskId ? <DiffViewer key={taskId} taskId={taskId} /> : <p mix={needCss}>Open a task (or its channel) to see its diff.</p> : null}
            {d.tab === 'logs' ? taskId ? <LogsPanel key={taskId} taskId={taskId} /> : <p mix={needCss}>Open a task (or its channel) to see its logs.</p> : null}
            {d.tab === 'activity' ? <ActivityPanel taskId={taskId} /> : null}
          </div>
        ) : null}
      </section>
    );
  };
}

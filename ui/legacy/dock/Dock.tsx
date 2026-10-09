import { useCallback, useEffect, useRef, useState } from 'react';
import { UsageTable } from '../components/Usage';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { DiffViewer } from './DiffViewer';
import { LogsPanel } from './LogsPanel';
import { TerminalPanel } from './TerminalPanel';

export type DockTab = 'terminal' | 'diff' | 'logs' | 'activity';
const TABS: { id: DockTab; label: string }[] = [
  { id: 'terminal', label: 'Terminal' },
  { id: 'diff', label: 'Diff' },
  { id: 'logs', label: 'Logs' },
  { id: 'activity', label: 'Activity' },
];

interface DockState {
  open: boolean;
  tab: DockTab;
  height: number;
}
const KEY = 'drydock.dock';
function load(projectId: string | undefined): DockState {
  try {
    const all = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Record<string, Partial<DockState>>;
    const s = all[projectId ?? ''] ?? {};
    return { open: s.open ?? false, tab: s.tab ?? 'diff', height: s.height ?? 280 };
  } catch {
    return { open: false, tab: 'diff', height: 280 };
  }
}
function save(projectId: string | undefined, s: DockState) {
  try {
    const all = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Record<string, DockState>;
    all[projectId ?? ''] = s;
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    /* a convenience only */
  }
}

/** Opens the dock on a tab from anywhere (the task view, the command palette). */
export function openDock(tab: DockTab) {
  window.dispatchEvent(new CustomEvent('drydock:dock', { detail: tab }));
}

/**
 * The bottom dock (docs/ui.md 2): Terminal, Diff, Logs and Activity for the selected task (Diff and Logs need one). Its
 * open state, tab and height are kept per project. Ctrl+J toggles it; Ctrl+Shift+D opens the diff.
 */
export function Dock({ projectId, taskId }: { projectId: string | undefined; taskId: string | undefined }) {
  const [s, setS] = useState<DockState>(() => load(projectId));
  useEffect(() => setS(load(projectId)), [projectId]);
  const update = useCallback(
    (p: Partial<DockState>) =>
      setS((cur) => {
        const n = { ...cur, ...p };
        save(projectId, n);
        return n;
      }),
    [projectId],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'j') {
        e.preventDefault();
        setS((cur) => {
          const n = { ...cur, open: !cur.open };
          save(projectId, n);
          return n;
        });
      } else if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'd') {
        e.preventDefault();
        update({ open: true, tab: 'diff' });
      }
    };
    const onOpen = (e: Event) => update({ open: true, tab: (e as CustomEvent<DockTab>).detail });
    window.addEventListener('keydown', onKey);
    window.addEventListener('drydock:dock', onOpen);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('drydock:dock', onOpen);
    };
  }, [projectId, update]);

  // drag the top edge to resize
  const drag = useRef<{ y: number; h: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent) => {
    drag.current = { y: e.clientY, h: s.height };
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!drag.current) return;
    const h = Math.min(Math.max(drag.current.h + (drag.current.y - e.clientY), 120), Math.round(window.innerHeight * 0.8));
    setS((cur) => ({ ...cur, height: h }));
  };
  const onPointerUp = () => {
    if (drag.current) save(projectId, s);
    drag.current = null;
  };

  return (
    <section className={s.open ? 'dock open' : 'dock'} aria-label="Dock" style={s.open ? { height: s.height } : undefined}>
      {s.open ? (
        <div
          className="dock-resize"
          role="separator"
          aria-orientation="horizontal"
          aria-label="Resize the dock"
          tabIndex={0}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onKeyDown={(e) => {
            if (e.key === 'ArrowUp') update({ height: Math.min(s.height + 40, Math.round(window.innerHeight * 0.8)) });
            if (e.key === 'ArrowDown') update({ height: Math.max(s.height - 40, 120) });
          }}
        />
      ) : null}
      <div className="dock-head">
        <div role="tablist" aria-label="Dock" className="tabs">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              id={`dock-tab-${t.id}`}
              aria-selected={s.open && s.tab === t.id}
              aria-controls="dock-panel"
              onClick={() => update({ open: !(s.open && s.tab === t.id), tab: t.id })}
            >
              {t.label}
            </button>
          ))}
        </div>
        <button className="link-button" onClick={() => update({ open: !s.open })} aria-expanded={s.open} title="Ctrl+J">
          {s.open ? 'Hide dock' : 'Show dock'}
        </button>
      </div>
      {s.open ? (
        <div className="dock-panel" id="dock-panel" role="tabpanel" aria-labelledby={`dock-tab-${s.tab}`}>
          {s.tab === 'terminal' ? <TerminalPanel projectId={projectId} taskId={taskId} /> : null}
          {s.tab === 'diff' ? taskId ? <DiffViewer taskId={taskId} key={taskId} /> : <NeedTask what="diff" /> : null}
          {s.tab === 'logs' ? taskId ? <LogsPanel taskId={taskId} key={taskId} /> : <NeedTask what="logs" /> : null}
          {s.tab === 'activity' ? <ActivityPanel taskId={taskId} /> : null}
        </div>
      ) : null}
    </section>
  );
}

function NeedTask({ what }: { what: string }) {
  return <p className="muted pad">Open a task (or its channel) to see its {what}.</p>;
}

/** Model and token activity: the usage rows of the selected task, else the most recent ones. */
function ActivityPanel({ taskId }: { taskId: string | undefined }) {
  const client = useClient();
  const q = useLive(() => client.usageList({ taskId, limit: 200 }), [taskId], (e) => e.type.startsWith('usage.') || (!!taskId && e.taskId === taskId), true, 500);
  return (
    <div className="pad">
      <p className="muted">{taskId ? 'Usage of this task, newest first.' : 'The most recent usage across the workspace.'}</p>
      <UsageTable rows={q.data ?? []} />
    </div>
  );
}

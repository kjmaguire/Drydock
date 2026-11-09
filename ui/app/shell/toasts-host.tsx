// The toast stack (decision G2): bottom right, above the dock, at most three, each one line with one action, gone after eight
// seconds (a warning a page left for the next stays until closed). It also decides what is news: a new Inbox item, a task
// that ended and a chat that answered, each only when the person is not looking at it (shell/toasts.ts). A toast never
// carries a request's own words, code or a prompt: its kind, the project and the task.

import { css, navigate, on, type Handle } from 'remix/component';
import { CircleAlert, CircleCheck, Info, X } from 'lucide';
import { services, watch } from '../state/services.tsx';
import type { Toast } from '../state/ui.ts';
import { IconButton } from '../ui/button.tsx';
import { Icon } from '../ui/icon.tsx';
import { t, tone } from '../ui/tokens.ts';
import { chatToast, hereWithWork, NeedsDiff, needsToast, taskToast, type Here } from './toasts.ts';

/** How long a toast stays. */
export const TOAST_MS = 8000;

const stackCss = css({
  position: 'fixed',
  right: t.s4,
  bottom: '56px',
  zIndex: '40',
  display: 'grid',
  gap: t.s2,
  width: 'min(360px, calc(100vw - 32px))',
  pointerEvents: 'none',
});
const toastCss = css({
  display: 'flex',
  alignItems: 'flex-start',
  gap: t.s2,
  padding: '8px 8px 8px 12px',
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgRaised,
  color: t.text,
  fontSize: t.textMd,
  boxShadow: '0 6px 24px color-mix(in srgb, black 25%, transparent)',
  pointerEvents: 'auto',
  '& > svg': { marginTop: '2px', flex: 'none' },
});
const bodyCss = css({ flex: '1', minWidth: '0', display: 'grid', gap: '2px' });
const detailCss = css({ color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' });
const actionCss = css({
  flex: 'none',
  alignSelf: 'center',
  padding: '2px 8px',
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.accent,
  fontWeight: '600',
  cursor: 'pointer',
  minHeight: '0',
});

const ICON = { ok: CircleCheck, warn: CircleAlert, info: Info };

export function Toasts(handle: Handle<{ here: Here; lifeMs?: number; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.ui);
  watch(handle, s.workspace);
  const timers = new Map<number, ReturnType<typeof setTimeout>>();
  handle.signal.addEventListener('abort', () => {
    for (const h of timers.values()) clearTimeout(h);
  });

  // the project and task an event or an item is in, from what the window already read: never a read of its own
  const projectName = (id: string | undefined) => (id ? s.workspace.projects.data?.find((p) => p.id === id)?.name : undefined);
  const taskOf = (id: string | undefined) => (id ? s.workspace.summaries.data?.summaries.find((x) => x.taskId === id) : undefined);

  // a new Inbox item: the workspace's own list (every project), told apart from the last one it read
  // where the person is now, with the item Work has open when Work is the page
  const here = () => hereWithWork(handle.props.here, s.ui.workOpen);

  const diff = new NeedsDiff();
  let lastNeeds: unknown;
  const onWorkspace = () => {
    const items = s.workspace.needs.data;
    if (!items || items === lastNeeds) return;
    lastNeeds = items;
    for (const it of diff.next(items, s.store.getSnapshot().epoch)) {
      const init = needsToast(it, here(), { projectName: projectName(it.projectId), task: taskOf(it.taskId) });
      if (init) s.ui.toast(init);
    }
  };
  s.workspace.addEventListener('change', onWorkspace, { signal: handle.signal });
  onWorkspace();

  // a task that ended and a chat that answered, as the events arrive
  const off = s.store.onEvent((e) => {
    const now = here();
    const init = e.type === 'chat.turn' ? chatToast(e, now, projectName(e.projectId)) : taskToast(e, now, taskOf(e.taskId));
    if (init) s.ui.toast(init);
  });
  handle.signal.addEventListener('abort', off);

  const close = (id: number) => {
    const h = timers.get(id);
    if (h !== undefined) clearTimeout(h);
    timers.delete(id);
    s.ui.dismissToast(id);
  };
  const open = (x: Toast) => {
    close(x.id);
    if (!x.action) return;
    if (handle.props.onNavigate) handle.props.onNavigate(x.action.href);
    else void navigate(x.action.href);
  };

  return () => {
    const list = s.ui.toasts;
    for (const x of list) {
      if (x.sticky || timers.has(x.id)) continue;
      timers.set(
        x.id,
        setTimeout(() => close(x.id), handle.props.lifeMs ?? TOAST_MS),
      );
    }
    for (const [id, h] of [...timers]) {
      if (list.some((x) => x.id === id)) continue;
      clearTimeout(h); // pushed out by a newer one
      timers.delete(id);
    }
    return (
      // a landmark of its own, as everything on the page is (axe "region"); the stack inside is a polite live region
      <aside mix={stackCss} aria-label="Notifications" aria-live="polite" data-testid="toasts">
        {list.map((x) => (
          <div key={String(x.id)} mix={toastCss} data-testid="toast" data-tone={x.tone}>
            <span style={{ color: tone[x.tone].fg, display: 'inline-flex' }}>
              <Icon icon={ICON[x.tone]} size={15} />
            </span>
            <div mix={bodyCss}>
              <strong data-testid="toast-text">{x.text}</strong>
              {x.detail ? <span mix={detailCss}>{x.detail}</span> : null}
            </div>
            {x.action ? (
              <button type="button" mix={[actionCss, on('click', () => open(x))]} data-testid="toast-action">
                {x.action.label}
              </button>
            ) : null}
            <IconButton icon={X} label="Close" size={14} mix={[on('click', () => close(x.id))]} />
          </div>
        ))}
      </aside>
    );
  };
}

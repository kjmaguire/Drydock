// The command palette (docs/ui.md 6.3), Ctrl+K: a modal <dialog> with an input that drives a list of options
// (aria-activedescendant), ranked by palette-items.ts.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { CornerDownLeft, Search } from 'lucide';
import { ownsKeys } from '../lib/keys.ts';
import { taskSearchNote } from '../lib/task-total.ts';
import type { ThemeChoice } from '../lib/theme.ts';
import { routes } from '../routes.ts';
import { query as liveQuery, services, watch } from '../state/services.tsx';
import type { DockTab } from '../state/ui.ts';
import { Icon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import { GROUPS, PALETTE_CHATS, buildItems, filterItems, isChatsPath, type PaletteItem } from './palette-items.ts';

const dialogCss = css({
  width: 'min(640px, calc(100vw - 32px))',
  marginTop: '12vh',
  padding: '0',
  border: '0',
  borderRadius: t.rXl,
  background: t.bgOverlay,
  color: t.text,
  boxShadow: t.shadowLg,
  overflow: 'hidden',
  '&::backdrop': { background: t.scrim },
  '&[open]': { animation: `dd-pop ${t.med} ${t.ease}` },
  '@keyframes dd-pop': { from: { opacity: '0', transform: 'translateY(-6px) scale(0.98)' }, to: { opacity: '1', transform: 'none' } },
});
const inputRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: `0 ${t.s4}`,
  borderBottom: `1px solid ${t.border}`,
  color: t.textMuted,
  '& input': {
    flex: '1',
    height: '52px',
    padding: '0',
    border: '0',
    background: 'transparent',
    fontSize: t.textLg,
    boxShadow: 'none',
  },
  '& input:focus-visible': { boxShadow: 'none', border: '0' },
});
const listCss = css({ maxHeight: 'min(420px, 60vh)', overflowY: 'auto', padding: t.s2, margin: '0', listStyle: 'none' });
const groupCss = css({ padding: `${t.s2} ${t.s2} ${t.s1}`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });
const optCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '36px',
  padding: `0 ${t.s3}`,
  borderRadius: t.rMd,
  fontSize: t.textMd,
  cursor: 'pointer',
  '& .hint': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textSm },
  '& .enter': { display: 'none', color: t.textMuted },
  '&[aria-selected="true"]': { background: t.bgActive },
  '&[aria-selected="true"] .enter': { display: 'inline-flex' },
  '&[aria-selected="true"] .hint': { display: 'none' },
});
const emptyCss = css({ padding: t.s6, textAlign: 'center', color: t.textMuted });
const footCss = css({ display: 'flex', gap: t.s4, padding: `${t.s2} ${t.s4}`, borderTop: `1px solid ${t.border}`, color: t.textFaint, fontSize: t.textXs });

export function CommandPalette(handle: Handle<{ onMessage: (agentId: string) => void; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href));
  watch(handle, s.ui);
  let dialog: HTMLDialogElement | undefined;
  let input: HTMLInputElement | undefined;
  let query = '';
  let active = 0;
  let pressedBackdrop = false; // see ui/dialog.tsx: a drag from the input out to the backdrop is not a click on it
  // recent chats, read while the palette is open
  const chats = liveQuery(handle, {
    load: () => s.client.chatList({ limit: PALETTE_CHATS }),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.updated' || e.type === 'chat.deleted',
    enabled: () => s.ui.paletteOpen,
    debounceMs: 300,
  });

  // Ctrl+K toggles the palette anywhere but inside the terminal (kill to end of line) or the diff editor (a chord)
  window.addEventListener(
    'keydown',
    (e) => {
      if (ownsKeys(e.target)) return;
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        s.ui.setPalette(!s.ui.paletteOpen);
      }
    },
    { signal: handle.signal },
  );

  const sync = () => {
    if (!dialog) return;
    if (s.ui.paletteOpen && !dialog.open) {
      query = '';
      active = 0;
      if (input) input.value = '';
      dialog.showModal();
      input?.focus();
      void handle.update();
    } else if (!s.ui.paletteOpen && dialog.open) dialog.close();
  };

  const run = (it: PaletteItem) => {
    s.ui.setPalette(false);
    if (it.href) {
      go(it.href);
      return;
    }
    const [kind, arg] = (it.action ?? '').split(':') as [string, string];
    switch (kind) {
      case 'inspect':
        s.ui.openInspector(arg);
        break;
      case 'dm':
        handle.props.onMessage(arg);
        break;
      case 'dock':
        if (arg === 'toggle') s.ui.toggleDock();
        else s.ui.openDock(arg as DockTab);
        break;
      case 'details':
        s.ui.setDetails(!s.ui.detailsOpen);
        break;
      case 'theme':
        s.ui.setTheme(arg as ThemeChoice);
        break;
      case 'chat':
        // the Chats screen opens the dialog over the chat that is open (no URL flag: Back and a reload never reopen it)
        s.ui.requestNewChat();
        if (!isChatsPath(window.location.pathname)) go(routes.chats.href());
        break;
    }
  };

  return () => {
    handle.queueTask(sync);
    const p = s.project;
    const all = s.ui.paletteOpen ? buildItems(p.roomList, p.taskList, p.agentList, chats.data ?? []) : [];
    const shown = filterItems(all, query);
    const idx = Math.min(active, Math.max(shown.length - 1, 0));
    const listId = `palette-list-${handle.id}`;
    const optId = (i: number) => `${listId}-${i}`;
    const grouped = !query.trim();
    let n = -1;
    const option = (it: PaletteItem) => {
      n++;
      const i = n;
      return (
        <li
          key={it.id}
          id={optId(i)}
          role="option"
          aria-selected={i === idx}
          mix={[
            optCss,
            on('mousemove', () => {
              if (active !== i) {
                active = i;
                void handle.update();
              }
            }),
            on('mousedown', (e) => {
              e.preventDefault();
              run(it);
            }),
          ]}
        >
          <span>{it.label}</span>
          {it.hint ? <span class="hint">{it.hint}</span> : null}
          <span class="enter">
            <Icon icon={CornerDownLeft} size={14} />
          </span>
        </li>
      );
    };
    return (
      <dialog
        aria-label="Command palette"
        mix={[
          dialogCss,
          ref((d) => {
            dialog = d;
            sync();
          }),
          on('cancel', (e) => {
            e.preventDefault();
            s.ui.setPalette(false);
          }),
          on('close', () => {
            if (s.ui.paletteOpen) s.ui.setPalette(false);
          }),
          on('pointerdown', (e) => {
            pressedBackdrop = e.target === e.currentTarget;
          }),
          on('click', (e) => {
            const closing = pressedBackdrop && e.target === e.currentTarget;
            pressedBackdrop = false;
            if (closing) s.ui.setPalette(false);
          }),
        ]}
      >
        <div mix={inputRow}>
          <Icon icon={Search} size={18} />
          <input
            type="text"
            placeholder="Go to, open or do... (Esc to close)"
            aria-label="Command"
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={shown.length ? optId(idx) : undefined}
            aria-autocomplete="list"
            autocomplete="off"
            spellcheck={false}
            mix={[
              ref((n) => (input = n)),
              on('input', (e) => {
                query = e.currentTarget.value;
                active = 0;
                void handle.update();
              }),
              on('keydown', (e) => {
                if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                  e.preventDefault();
                  if (!shown.length) return;
                  active = (idx + (e.key === 'ArrowDown' ? 1 : -1) + shown.length) % shown.length;
                  void handle.update().then(() => document.getElementById(optId(active))?.scrollIntoView({ block: 'nearest' }));
                } else if (e.key === 'Enter') {
                  e.preventDefault();
                  const it = shown[idx];
                  if (it) run(it);
                }
              }),
            ]}
          />
        </div>
        <ul id={listId} role="listbox" aria-label="Results" mix={listCss}>
          {shown.length === 0 && s.ui.paletteOpen ? (
            <li mix={emptyCss}>
              Nothing matches.{p.taskCoverage.partial ? ` ${taskSearchNote(p.taskCoverage)}` : ''}
            </li>
          ) : null}
          {grouped
            ? GROUPS.map((g) => {
                const items = shown.filter((i) => i.group === g);
                if (!items.length) return null;
                return [
                  <li key={`g-${g}`} role="presentation" mix={groupCss}>
                    {g}
                  </li>,
                  ...items.map(option),
                ];
              })
            : shown.map(option)}
        </ul>
        <div mix={footCss} aria-hidden="true">
          <span>
            <kbd>↑</kbd> <kbd>↓</kbd> to move
          </span>
          <span>
            <kbd>Enter</kbd> to open
          </span>
          <span>
            <kbd>Esc</kbd> to close
          </span>
          <span style={{ marginLeft: 'auto' }}>
            <a href={routes.settings.href()} tabIndex={-1} style={{ color: 'inherit' }}>
              Drydock
            </a>
          </span>
        </div>
      </dialog>
    );
  };
}

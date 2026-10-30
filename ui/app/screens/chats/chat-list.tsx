// The chat list column (graphite, decision P97): "Chats" and New (Ctrl+N), a search box (chat.search, debounced, matches
// highlighted), "All chats" (off: the current project's chats only), then the chats, pinned first. Each row shows the title and
// a faint line: the project (when the chat is in one), the CLI (named as the Connections page names it: "Claude Code WSL") and
// when it was last active, or that a turn is running; its actions menu pins, renames, forks, archives or deletes (with a
// confirmation). The import button beside New opens "Import from Claude Code" (chat-import.tsx, decision P109).

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Archive, ArchiveRestore, Ellipsis, GitFork, Import, Pencil, Pin, PinOff, Search, Trash2 } from 'lucide';
import type { ChatHit, ChatListItem } from '../../api/chatClient.ts';
import type { Connection } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Menu, type MenuItem } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { comboFor, shortcutOf } from '../../lib/keys.ts';
import { chatCliText, chatRowLine, chatTitle, listWhen, snippetParts } from './chats.ts';
import { ImportClaudeDialog } from './chat-import.tsx';

const colCss = css({
  display: 'flex',
  flexDirection: 'column',
  width: '280px',
  flex: 'none',
  minHeight: '0',
  borderRight: `1px solid ${t.border}`,
  background: t.bgSidebar,
});
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: '16px 16px 10px',
  flex: 'none',
  '& h1': { flex: '1', fontSize: t.textLg, fontWeight: '600', margin: '0' },
  '& kbd': { marginLeft: '2px' },
});
const toolsCss = css({ display: 'grid', gap: '6px', padding: `0 10px ${t.s2}`, flex: 'none' });
const searchCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '6px',
  height: '30px',
  padding: `0 ${t.s2}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgInput,
  color: t.textFaint,
  '&:focus-within': { borderColor: t.accent, boxShadow: `0 0 0 3px ${t.accentSoft}` },
  '& input': { flex: '1', minWidth: '0', height: '28px', padding: '0', border: '0', background: 'transparent', boxShadow: 'none', fontSize: t.textMd },
  '& input:focus-visible': { boxShadow: 'none', border: '0' },
});
const filterRow = css({ display: 'flex', alignItems: 'center', gap: '4px' });
const pillCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '28px',
  minHeight: '0',
  padding: '0 10px',
  border: `1px solid transparent`,
  borderRadius: t.rFull,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-pressed="true"]': { background: t.bgActive, color: t.text, borderColor: t.border },
});
const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', padding: `${t.s1} 10px ${t.s4}` });
const groupCss = css({ padding: `${t.s2} ${t.s2} 4px`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });
const listCss = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', gap: '4px', margin: '0', padding: '0', listStyle: 'none' });
// a row: the title, then a faint line (the project, the CLI, when); the open chat is highlighted
const rowCss = css({
  position: 'relative',
  '& > a': {
    display: 'flex',
    flexDirection: 'column',
    gap: '3px',
    padding: '10px 12px',
    borderRadius: t.rLg,
    color: t.text,
    textDecoration: 'none',
  },
  '& > a:hover': { background: t.bgHover, textDecoration: 'none' },
  '& > a[aria-current="page"]': { background: t.bgActive },
  '& > a:focus-visible': { boxShadow: `inset ${t.focus}`, outline: 'none' },
  '& .title': { display: 'flex', alignItems: 'center', gap: '5px', minWidth: '0', fontSize: t.textMd, fontWeight: '500' },
  '& .title > span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .title svg': { flex: 'none', color: t.textFaint },
  '& .line': { display: 'flex', alignItems: 'center', gap: '8px', minWidth: '0', color: t.textFaint, fontSize: t.textXs, fontVariantNumeric: 'tabular-nums' },
  '& .line > span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .when': { color: t.textFaint, fontSize: t.textXs, fontVariantNumeric: 'tabular-nums', marginLeft: 'auto', flex: 'none' },
  '& .preview': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: t.textMuted, fontSize: t.textSm },
  '& .preview mark': { background: t.mentionBg, color: t.text, borderRadius: '2px', padding: '0 1px' },
  '& .run': { display: 'inline-flex', alignItems: 'center', gap: '4px', flex: 'none', color: t.success, fontWeight: '500' },
  '& .run i': { width: '6px', height: '6px', borderRadius: '50%', background: t.success, animation: 'dd-chat-pulse 1.2s ease-in-out infinite' },
  '@keyframes dd-chat-pulse': { '50%': { opacity: '0.35' } },
  '& .err': { width: '6px', height: '6px', borderRadius: '50%', background: t.danger, flex: 'none' },
});
// the actions button sits over the row's time and shows on hover or focus
const moreCss = css({
  position: 'absolute',
  top: '5px',
  right: '4px',
  display: 'grid',
  placeItems: 'center',
  width: '28px',
  height: '28px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rSm,
  background: t.bgRaised,
  color: t.textMuted,
  boxShadow: t.shadowSm,
  opacity: '0',
  '&:hover': { color: t.text },
  '&:focus-visible, &[aria-expanded="true"]': { opacity: '1' },
  ':is(.chat-row:hover) &': { opacity: '1' },
});
const emptyCss = css({ padding: `${t.s6} ${t.s3}`, textAlign: 'center', color: t.textMuted, fontSize: t.textMd, '& p': { margin: `0 0 ${t.s3}` } });

const DEBOUNCE_MS = 250;

// in a narrow window (ui.ts NARROW_RAILS) the list is either the whole screen (no chat open) or a drawer over the open chat
const fullCss = css({ width: '100%', borderRight: '0' });

/**
 * A key press that opens the new chat dialog: Ctrl+N (or the person's binding, decision P132), not while typing in a field, inside
 * a widget with its own keys or a dialog.
 */
export function isNewChatKey(e: KeyboardEvent): boolean {
  if (e.repeat || e.defaultPrevented || shortcutOf(e) !== 'new-chat') return false;
  const el = e.target as HTMLElement | null;
  return !(el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || el.closest?.('dialog, [role="menu"], [role="listbox"]')));
}
const drawerCss = css({ position: 'absolute', top: '0', left: '0', bottom: '0', zIndex: '30', width: 'min(320px, calc(100% - 48px))', boxShadow: t.shadowLg });

export function ChatList(
  handle: Handle<{ projectId: string | undefined; selectedId: string | undefined; onNew: () => void; onNavigate?: (href: string) => void; layout?: 'column' | 'full' | 'drawer' }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  // each dialog has its own action: its error shows (and is cleared) with the dialog, never in the list
  const renameAct = action(handle);
  const deleteAct = action(handle);
  // every chat by default: a chat need not belong to a project (decision P107); the project's pill narrows the list to it
  let all = true;
  let archived = false;
  let typed = '';
  let q = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  let renaming: ChatListItem | undefined;
  let renameText = '';
  let deleting: ChatListItem | undefined;
  let importing = false;
  const openImport = () => ((importing = true), void handle.update());
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : navigate(href));

  const scope = () => (all || !handle.props.projectId ? '' : handle.props.projectId);
  const chats = query(handle, {
    load: () => s.client.chatList({ projectId: scope(), includeArchived: archived }),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.updated' || e.type === 'chat.deleted' || e.type === 'chat.turn_started' || e.type === 'chat.turn',
    key: () => [scope(), archived],
    debounceMs: 150,
  });
  // the chat that is open, when the list as filtered does not hold it (a chat with no project, or another project's): it is
  // shown on top so the list never hides where you are (UI review P1-4). Read only then.
  const missingOpen = () => {
    const id = handle.props.selectedId;
    return !!id && !!chats.data && !chats.data.some((c) => c.id === id);
  };
  const open = query(handle, {
    load: () => s.client.chatList({ projectId: '', includeArchived: true }),
    when: (e) => e.type === 'chat.updated' || e.type === 'chat.deleted',
    key: () => handle.props.selectedId,
    enabled: missingOpen,
    debounceMs: 150,
  });
  // a chat in WSL is named as the Connections page names its install ("Claude Code WSL", the distro only when the CLI is in
  // another distro too): the Connections list, read only while such a chat is listed
  const installs = query(handle, {
    enabled: () => (chats.data ?? []).some((c) => !!c.wslDistro) || (open.data ?? []).some((c) => !!c.wslDistro),
    when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started',
    load: async (): Promise<Connection[]> => {
      try {
        return await s.client.connectionList();
      } catch {
        return [];
      }
    },
  });
  const hits = query(handle, {
    load: () => s.client.chatSearch(q, scope()),
    when: (e) => e.type === 'chat.message' || e.type === 'chat.updated' || e.type === 'chat.deleted',
    key: () => [q, scope()],
    enabled: () => q.trim().length > 0,
    debounceMs: 500,
  });

  const onSearch = (v: string) => {
    typed = v;
    clearTimeout(timer);
    timer = setTimeout(() => {
      q = typed.trim();
      void handle.update();
    }, DEBOUNCE_MS);
    void handle.update();
  };

  // after a change the list is read again: the chat.* events may be late (the stream reconnecting) while RPC works
  const run = async (name: string, c: ChatListItem) => {
    switch (name) {
      case 'pin':
        if (await act.run('pin', () => s.client.chatUpdate({ id: c.id, pinned: !c.pinned }))) void chats.reload();
        break;
      case 'rename':
        renaming = c;
        renameText = c.title;
        renameAct.clear(); // a previous rename's error; also renders the dialog
        break;
      case 'fork': {
        const f = await act.run('fork', () => s.client.chatFork(c.id));
        if (f) {
          void chats.reload();
          await go(routes.chat.href({ id: f.id }));
        }
        break;
      }
      case 'archive':
        if (await act.run('archive', () => s.client.chatUpdate({ id: c.id, archived: !c.archived }))) void chats.reload();
        break;
      case 'delete':
        deleting = c;
        deleteAct.clear();
        break;
    }
  };

  const items = (c: ChatListItem): MenuItem[] => [
    { name: 'pin', label: c.pinned ? 'Unpin' : 'Pin', icon: c.pinned ? PinOff : Pin },
    { name: 'rename', label: 'Rename', icon: Pencil },
    { name: 'fork', label: 'Fork', icon: GitFork },
    { name: 'archive', label: c.archived ? 'Unarchive' : 'Archive', icon: c.archived ? ArchiveRestore : Archive, disabled: !c.archived && c.status === 'running' },
    { name: 'delete', label: 'Delete…', icon: Trash2, danger: true, separatorBefore: true, disabled: c.status === 'running' },
  ];

  const projectName = (id: string) => s.workspace.projectList.find((p) => p.id === id)?.name ?? '';
  const row = (c: ChatListItem) => {
    const { selectedId } = handle.props;
    const title = chatTitle(c);
    return (
      <li key={c.id} class="chat-row" mix={rowCss} data-testid="chat-row" data-chat-id={c.id} data-pinned={c.pinned ? 'true' : undefined}>
        <a href={routes.chat.href({ id: c.id })} aria-current={selectedId === c.id ? 'page' : undefined} aria-label={`${title}${c.status === 'running' ? ', running' : ''}${c.pinned ? ', pinned' : ''}`}>
          <span class="title">
            {c.pinned ? <Icon icon={Pin} size={11} /> : null}
            <span>{title}</span>
            {c.archived ? <Badge>archived</Badge> : null}
            {c.status === 'error' ? <span class="err" title={c.lastError || 'The last turn failed'} /> : null}
          </span>
          <span class="line">
            <span title={new Date(c.updatedAt).toLocaleString()}>
              {chatRowLine({ project: c.projectId ? projectName(c.projectId) : '', cli: chatCliText(c, installs.data ?? []), when: c.status === 'running' ? '' : listWhen(c.updatedAt) })}
            </span>
            {c.status === 'running' ? (
              <span class="run" data-testid="chat-running">
                <i aria-hidden="true" />
                running
              </span>
            ) : null}
          </span>
        </a>
        <Menu
          label={`Actions for ${title}`}
          items={items(c)}
          placement="bottom-end"
          onSelect={(name) => void run(name, c)}
          trigger={(mix) => (
            <button type="button" mix={[moreCss, mix]} aria-label={`Actions for ${title}`} data-testid="chat-row-menu">
              <Icon icon={Ellipsis} size={14} />
            </button>
          )}
        />
      </li>
    );
  };

  const hitRow = (h: ChatHit, i: number) => (
    <li key={`${h.chatId}:${h.seq}:${i}`} class="chat-row" mix={rowCss} data-testid="chat-hit">
      <a href={routes.chat.href({ id: h.chatId })} aria-current={handle.props.selectedId === h.chatId ? 'page' : undefined}>
        <span class="title">
          <span>{chatTitle(h)}</span>
          {h.archived ? <Badge>archived</Badge> : null}
          <span class="when">{h.seq === 0 ? 'title' : h.role ?? ''}</span>
        </span>
        <span class="preview">
          {snippetParts(h.snippet).map((p, j) => (p.hit ? <mark key={j}>{p.text}</mark> : p.text))}
        </span>
      </a>
    </li>
  );

  return () => {
    const { projectId, onNew } = handle.props;
    const list = chats.data ?? [];
    const openChat = missingOpen() ? (open.data ?? []).find((c) => c.id === handle.props.selectedId) : undefined;
    const pinned = list.filter((c) => c.pinned);
    const rest = list.filter((c) => !c.pinned);
    const searching = q.length > 0;
    const scopeName = projectId ? projectName(projectId) : '';
    return (
      <aside mix={[colCss, handle.props.layout === 'full' ? fullCss : handle.props.layout === 'drawer' ? drawerCss : undefined]} aria-label="Chats" data-layout={handle.props.layout ?? 'column'}>
        <div mix={headCss}>
          <h1>Chats</h1>
          <IconButton icon={Import} size={15} label="Import from Claude Code" mix={[on('click', openImport)]} data-testid="chat-import-open-dialog" />
          <Button size="sm" variant="secondary" mix={[on('click', () => onNew())]} data-testid="chat-new" aria-label="New chat" aria-keyshortcuts={comboFor('new-chat').replace('Ctrl', 'Control')} title={`New chat (${comboFor('new-chat')})`}>
            New
            <kbd aria-hidden="true">{comboFor('new-chat')}</kbd>
          </Button>
        </div>
        <div mix={toolsCss}>
          <label mix={searchCss}>
            <Icon icon={Search} size={14} />
            <span class="sr-only">Search chats</span>
            <input type="search" placeholder="Search chats" aria-label="Search chats" value={typed} mix={on("input", (e) => onSearch(e.currentTarget.value))} />
          </label>
          <div mix={filterRow} role="group" aria-label="Filter chats">
            {projectId ? (
              <button type="button" mix={[pillCss, on('click', () => ((all = false), void handle.update()))]} aria-pressed={!all ? 'true' : 'false'} title={`Only the chats of ${scopeName || 'this project'}`}>
                {scopeName || 'This project'}
              </button>
            ) : null}
            <button type="button" mix={[pillCss, on('click', () => ((all = true), void handle.update()))]} aria-pressed={all || !projectId ? 'true' : 'false'}>
              All chats
            </button>
            <button type="button" mix={[pillCss, on('click', () => ((archived = !archived), void handle.update()))]} aria-pressed={archived ? 'true' : 'false'} style={{ marginLeft: 'auto' }}>
              Archived
            </button>
          </div>
        </div>
        <div mix={scrollCss}>
          <ErrorNote error={act.error} />
          {searching ? (
            <>
              <div mix={groupCss} aria-live="polite">
                {hits.loading && !hits.data ? 'Searching...' : `${hits.data?.length ?? 0} ${hits.data?.length === 1 ? 'match' : 'matches'}`}
              </div>
              <ErrorNote error={hits.error} />
              <ul mix={listCss} aria-label="Search results">
                {(hits.data ?? []).map(hitRow)}
              </ul>
            </>
          ) : chats.loading && !chats.data ? (
            <Loading label="Loading chats..." />
          ) : (
            <>
              <ErrorNote error={chats.error} />
              {openChat ? (
                <>
                  <div mix={groupCss}>{openChat.projectId ? 'Open, from another project' : 'Open, not in a project'}</div>
                  <ul mix={listCss} aria-label="Open chat" data-testid="chat-list-open">
                    {row(openChat)}
                  </ul>
                </>
              ) : null}
              {list.length === 0 && !chats.error ? (
                <div mix={emptyCss}>
                  <p>{archived ? 'No chats here.' : projectId && !all ? 'No chats in this project yet.' : 'No chats yet.'}</p>
                  <Button size="sm" mix={[on('click', () => onNew())]}>
                    Start a chat
                  </Button>
                  {!archived ? (
                    <p style={{ margin: '12px 0 0' }}>
                      <Button size="sm" variant="link" mix={[on('click', openImport)]} data-testid="chat-import-empty">
                        Import from Claude Code
                      </Button>
                    </p>
                  ) : null}
                </div>
              ) : null}
              {pinned.length ? (
                <>
                  <div mix={groupCss}>Pinned</div>
                  <ul mix={listCss} aria-label="Pinned chats">
                    {pinned.map(row)}
                  </ul>
                </>
              ) : null}
              {rest.length ? (
                <>
                  {pinned.length ? <div mix={groupCss}>Recent</div> : null}
                  <ul mix={listCss} aria-label={pinned.length ? 'Recent chats' : 'Chats'}>
                    {rest.map(row)}
                  </ul>
                </>
              ) : null}
            </>
          )}
        </div>
        <ImportClaudeDialog
          open={importing}
          onClose={() => {
            importing = false;
            void chats.reload();
            void handle.update();
          }}
          onNavigate={handle.props.onNavigate}
        />
        <Dialog open={!!renaming} onClose={() => ((renaming = undefined), void handle.update())} title="Rename chat">
          <form
            style={{ display: 'grid', gap: '16px' }}
            mix={on('submit', async (e) => {
              e.preventDefault();
              const c = renaming;
              if (!c) return;
              const ok = await renameAct.run('rename', () => s.client.chatUpdate({ id: c.id, title: renameText.trim() }));
              if (ok) {
                renaming = undefined;
                void chats.reload();
              }
              void handle.update();
            })}
          >
            <ErrorNote error={renameAct.error} what="Could not rename the chat" />
            <Field label="Title">{(id) => <input id={id} value={renameText} maxLength={200} mix={[on('input', (e) => ((renameText = e.currentTarget.value), void handle.update())), ref((n) => queueMicrotask(() => (n as HTMLInputElement).select()))]} />}</Field>
            <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
              <Button variant="ghost" mix={[on('click', () => ((renaming = undefined), void handle.update()))]}>
                Cancel
              </Button>
              <Button variant="primary" type="submit" disabled={!!renameAct.busy}>
                Save
              </Button>
            </div>
          </form>
        </Dialog>
        <Dialog open={!!deleting} onClose={() => ((deleting = undefined), void handle.update())} title="Delete chat">
          <p>
            Delete <strong>{deleting ? chatTitle(deleting) : ''}</strong> and all {deleting?.messageCount ?? 0} of its messages? This cannot be undone. Archive it instead to keep it searchable.
          </p>
          <ErrorNote error={deleteAct.error} what="Could not delete the chat" />
          <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
            <Button variant="ghost" mix={[on('click', () => ((deleting = undefined), void handle.update()))]}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={!!deleteAct.busy}
              mix={[
                on('click', async () => {
                  const c = deleting;
                  if (!c) return;
                  const ok = await deleteAct.run('delete', async () => (await s.client.chatDelete(c.id), true));
                  if (ok) {
                    deleting = undefined;
                    void chats.reload();
                    if (handle.props.selectedId === c.id) await go(routes.chats.href());
                  }
                  void handle.update();
                }),
              ]}
            >
              {deleteAct.busy ? 'Deleting...' : 'Delete chat'}
            </Button>
          </div>
        </Dialog>
      </aside>
    );
  };
}

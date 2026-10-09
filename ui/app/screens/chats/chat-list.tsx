// The chat list column: New chat, a search box (chat.search, debounced, matches highlighted), "All chats" (off: the current
// project's chats only), then the chats, pinned first. Each row shows the title, the preview, a relative time and whether a
// turn is running; its actions menu pins, renames, forks, archives or deletes (with a confirmation).

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { Archive, ArchiveRestore, Ellipsis, GitFork, MessagesSquare, Pencil, Pin, PinOff, Plus, Search, Trash2 } from 'lucide';
import type { ChatHit, ChatListItem } from '../../api/chatClient.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Menu, type MenuItem } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { adapterLabel, chatTitle, relTime, snippetParts } from './chats.ts';

const colCss = css({
  display: 'flex',
  flexDirection: 'column',
  width: '300px',
  flex: 'none',
  minHeight: '0',
  borderRight: `1px solid ${t.border}`,
  background: t.bgApp,
});
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: 'var(--header-height)',
  padding: `0 ${t.s2} 0 ${t.s4}`,
  borderBottom: `1px solid ${t.border}`,
  flex: 'none',
  '& h1': { flex: '1', display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textLg, fontWeight: '600', margin: '0' },
  '& h1 svg': { color: t.textMuted },
});
const toolsCss = css({ display: 'grid', gap: '6px', padding: `${t.s2} ${t.s2} ${t.s1}`, flex: 'none' });
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
  height: '24px',
  minHeight: '0',
  padding: '0 8px',
  border: `1px solid transparent`,
  borderRadius: t.rFull,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-pressed="true"]': { background: t.bgActive, color: t.text, borderColor: t.border },
});
const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', padding: `${t.s1} ${t.s2} ${t.s4}` });
const groupCss = css({ padding: `${t.s2} ${t.s2} 4px`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });
const listCss = css({ display: 'grid', gap: '1px', margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({
  position: 'relative',
  '& > a': {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) auto',
    gap: '2px 8px',
    padding: `7px ${t.s2}`,
    borderRadius: t.rMd,
    color: t.text,
    textDecoration: 'none',
  },
  '& > a:hover': { background: t.bgHover, textDecoration: 'none' },
  '& > a[aria-current="page"]': { background: t.bgActive },
  '& > a:focus-visible': { boxShadow: `inset ${t.focus}`, outline: 'none' },
  '& .title': { display: 'flex', alignItems: 'center', gap: '5px', minWidth: '0', fontSize: t.textMd, fontWeight: '500' },
  '& .title > span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .title svg': { flex: 'none', color: t.textFaint },
  '& .when': { color: t.textFaint, fontSize: t.textXs, fontVariantNumeric: 'tabular-nums', alignSelf: 'center' },
  '& .preview': { gridColumn: '1 / -1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: t.textMuted, fontSize: t.textSm },
  '& .preview mark': { background: t.mentionBg, color: t.text, borderRadius: '2px', padding: '0 1px' },
  '& .run': { display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.success, fontSize: t.textXs, fontWeight: '500' },
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
  width: '24px',
  height: '24px',
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

export function ChatList(handle: Handle<{ projectId: string | undefined; selectedId: string | undefined; onNew: () => void; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let all = false;
  let archived = false;
  let typed = '';
  let q = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  let renaming: ChatListItem | undefined;
  let renameText = '';
  let deleting: ChatListItem | undefined;
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : navigate(href));

  const scope = () => (all || !handle.props.projectId ? '' : handle.props.projectId);
  const chats = query(handle, {
    load: () => s.client.chatList({ projectId: scope(), includeArchived: archived }),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.updated' || e.type === 'chat.deleted' || e.type === 'chat.turn_started' || e.type === 'chat.turn',
    key: () => [scope(), archived],
    debounceMs: 150,
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

  const run = async (name: string, c: ChatListItem) => {
    switch (name) {
      case 'pin':
        await act.run('pin', () => s.client.chatUpdate({ id: c.id, pinned: !c.pinned }));
        break;
      case 'rename':
        renaming = c;
        renameText = c.title;
        break;
      case 'fork': {
        const f = await act.run('fork', () => s.client.chatFork(c.id));
        if (f) await go(routes.chat.href({ id: f.id }));
        break;
      }
      case 'archive':
        await act.run('archive', () => s.client.chatUpdate({ id: c.id, archived: !c.archived }));
        break;
      case 'delete':
        deleting = c;
        break;
    }
    chats.reload();
    void handle.update();
  };

  const items = (c: ChatListItem): MenuItem[] => [
    { name: 'pin', label: c.pinned ? 'Unpin' : 'Pin', icon: c.pinned ? PinOff : Pin },
    { name: 'rename', label: 'Rename', icon: Pencil },
    { name: 'fork', label: 'Fork', icon: GitFork },
    { name: 'archive', label: c.archived ? 'Unarchive' : 'Archive', icon: c.archived ? ArchiveRestore : Archive, disabled: !c.archived && c.status === 'running' },
    { name: 'delete', label: 'Delete…', icon: Trash2, danger: true, separatorBefore: true, disabled: c.status === 'running' },
  ];

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
          {c.status === 'running' ? (
            <span class="run" data-testid="chat-running">
              <i aria-hidden="true" />
              running
            </span>
          ) : (
            <span class="when" title={new Date(c.updatedAt).toLocaleString()}>
              {relTime(c.updatedAt)}
            </span>
          )}
          <span class="preview">{c.preview || `${adapterLabel(c.adapter)}${c.projectId ? '' : ', no project'}`}</span>
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
          <span>{h.title.trim() || 'New chat'}</span>
          {h.archived ? <Badge>archived</Badge> : null}
        </span>
        <span class="when">{h.seq === 0 ? 'title' : h.role ?? ''}</span>
        <span class="preview">
          {snippetParts(h.snippet).map((p, j) => (p.hit ? <mark key={j}>{p.text}</mark> : p.text))}
        </span>
      </a>
    </li>
  );

  return () => {
    const { projectId, onNew } = handle.props;
    const list = chats.data ?? [];
    const pinned = list.filter((c) => c.pinned);
    const rest = list.filter((c) => !c.pinned);
    const searching = q.length > 0;
    const projectName = s.workspace.projectList.find((p) => p.id === projectId)?.name;
    return (
      <aside mix={colCss} aria-label="Chats">
        <div mix={headCss}>
          <h1>
            <Icon icon={MessagesSquare} size={16} />
            Chats
          </h1>
          <Button size="sm" variant="primary" icon={Plus} mix={[on('click', () => onNew())]} data-testid="chat-new">
            New chat
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
              <button type="button" mix={[pillCss, on('click', () => ((all = false), void handle.update()))]} aria-pressed={!all ? 'true' : 'false'} title={`Only the chats of ${projectName ?? 'this project'}`}>
                {projectName ?? 'This project'}
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
              {list.length === 0 && !chats.error ? (
                <div mix={emptyCss}>
                  <p>{archived ? 'No chats here.' : projectId && !all ? 'No chats in this project yet.' : 'No chats yet.'}</p>
                  <Button size="sm" mix={[on('click', () => onNew())]}>
                    Start a chat
                  </Button>
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
        <Dialog open={!!renaming} onClose={() => ((renaming = undefined), void handle.update())} title="Rename chat">
          <form
            style={{ display: 'grid', gap: '16px' }}
            mix={on('submit', async (e) => {
              e.preventDefault();
              const c = renaming;
              if (!c) return;
              const ok = await act.run('rename', () => s.client.chatUpdate({ id: c.id, title: renameText.trim() }));
              if (ok) {
                renaming = undefined;
                chats.reload();
              }
              void handle.update();
            })}
          >
            <Field label="Title">{(id) => <input id={id} value={renameText} maxLength={200} mix={[on('input', (e) => void (renameText = e.currentTarget.value)), ref((n) => queueMicrotask(() => (n as HTMLInputElement).select()))]} />}</Field>
            <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
              <Button variant="ghost" mix={[on('click', () => ((renaming = undefined), void handle.update()))]}>
                Cancel
              </Button>
              <Button variant="primary" type="submit" disabled={act.busy === 'rename'}>
                Save
              </Button>
            </div>
          </form>
        </Dialog>
        <Dialog open={!!deleting} onClose={() => ((deleting = undefined), void handle.update())} title="Delete chat">
          <p>
            Delete <strong>{deleting ? chatTitle(deleting) : ''}</strong> and all {deleting?.messageCount ?? 0} of its messages? This cannot be undone. Archive it instead to keep it searchable.
          </p>
          <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
            <Button variant="ghost" mix={[on('click', () => ((deleting = undefined), void handle.update()))]}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={act.busy === 'delete'}
              mix={[
                on('click', async () => {
                  const c = deleting;
                  if (!c) return;
                  const ok = await act.run('delete', async () => (await s.client.chatDelete(c.id), true));
                  if (ok) {
                    deleting = undefined;
                    chats.reload();
                    if (handle.props.selectedId === c.id) await go(routes.chats.href());
                  }
                  void handle.update();
                }),
              ]}
            >
              {act.busy === 'delete' ? 'Deleting...' : 'Delete chat'}
            </Button>
          </div>
        </Dialog>
      </aside>
    );
  };
}

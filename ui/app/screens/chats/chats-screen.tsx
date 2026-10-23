// The Chats section (docs/chats.md, decisions P29, P97): the human's own conversations with an agent CLI (Claude Code by
// default). A list column on the left (search, pinned first, the current project's chats or all of them) and the open
// conversation on the right. It opens with or without a project: a chat may have none. Ctrl+N (outside a field) starts a
// new chat.

import { css, navigate, on, type Handle } from 'remix/component';
import { MessagesSquare, Plus } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Empty } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { ChatList, isNewChatKey } from './chat-list.tsx';
import { ChatView } from './chat-view.tsx';
import { NewChatDialog } from './new-chat-dialog.tsx';

const screenCss = css({ position: 'relative', display: 'flex', height: '100%', minHeight: '0', minWidth: '0', background: t.bgApp });
const scrimCss = css({ position: 'absolute', inset: '0', zIndex: '29', border: '0', borderRadius: '0', padding: '0', minHeight: '0', background: 'color-mix(in srgb, black 25%, transparent)', cursor: 'default' });
const emptyCss = css({ flex: '1', minWidth: '0', display: 'grid', placeItems: 'center', '& kbd': { margin: '0 2px' } });

export function ChatsScreen(handle: Handle<{ id?: string; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.ui); // the palette's New chat (s.ui.requestNewChat)
  const adapters = query(handle, { load: () => s.client.adapterList(), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  let dialog = false;

  const open = () => {
    dialog = true;
    void handle.update();
  };
  // the dialog is not in the URL: closing it leaves the chat (or the list) behind it as it was
  const close = () => {
    dialog = false;
    void handle.update();
  };
  // Ctrl+N: the new chat dialog (WebView2 would open a new window: the app keeps the key)
  window.addEventListener(
    'keydown',
    (e) => {
      if (!isNewChatKey(e)) return;
      e.preventDefault();
      open();
    },
    { signal: handle.signal },
  );
  const created = (chatId: string) => {
    dialog = false;
    void handle.update();
    const href = routes.chat.href({ id: chatId });
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href).catch(() => {});
  };

  return () => {
    const { id, onNavigate } = handle.props;
    if (s.ui.takeNewChat()) dialog = true;
    const ws = s.workspace;
    // a narrow window (a Windows half-screen) shows the list or the chat, and the list over the chat when asked for
    const narrow = s.ui.narrowRails;
    const list = (layout: 'column' | 'full' | 'drawer') => <ChatList projectId={ws.projectId} selectedId={id} onNew={open} onNavigate={onNavigate} layout={layout} />;
    return (
      <div mix={screenCss} data-testid="chats-screen">
        {!narrow ? list('column') : !id ? list('full') : null}
        {narrow && id && s.ui.drawerOpen ? (
          <>
            <button type="button" mix={[scrimCss, on('click', () => s.ui.setDrawer(false))]} aria-label="Close the chat list" />
            {list('drawer')}
          </>
        ) : null}
        {id ? (
          <ChatView key={id} id={id} adapters={adapters.data ?? []} onNavigate={onNavigate} onShowList={narrow ? () => s.ui.setDrawer(true) : undefined} />
        ) : narrow ? null : (
          <div mix={emptyCss}>
            <Empty title="Chats" icon={MessagesSquare}>
              <p>Talk to Claude Code (or another agent CLI) directly. Every chat is kept with its history, continues the same session, and can be searched, forked, attached to a message or read by a later chat.</p>
              <Button variant="primary" icon={Plus} mix={[on('click', () => open())]}>
                New chat
              </Button>
              <p style={{ fontSize: 'var(--text-sm)', color: 'var(--text-faint)' }}>
                Or press <kbd>Ctrl+N</kbd>.
              </p>
            </Empty>
          </div>
        )}
        <NewChatDialog open={dialog} onClose={close} projects={ws.projectList} projectId={ws.projectId} adapters={adapters.data} adaptersError={adapters.error} onCreated={created} />
      </div>
    );
  };
}

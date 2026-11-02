// The Chats section (docs/chats.md, decisions P29, P97): the human's own conversations with an agent CLI (Claude Code by
// default). A list column on the left (search, pinned first, the current project's chats or all of them) and the open
// conversation on the right. It opens with or without a project: a chat may have none. New chat (and Ctrl+N outside a field,
// and the palette's New chat) starts one at once, in no project, and opens it: its agent CLI, model, effort and permission mode
// are chosen in the chat itself, and Move to a project puts it in one (decision P107).

import { css, navigate, on, type Handle } from 'remix/component';
import { Plus } from 'lucide';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { ChatList, isNewChatKey } from './chat-list.tsx';
import { ChatView } from './chat-view.tsx';
import { NewChatDialog } from './new-chat-dialog.tsx';

const screenCss = css({ position: 'relative', display: 'flex', height: '100%', minHeight: '0', minWidth: '0', background: t.bgApp });
const scrimCss = css({ position: 'absolute', inset: '0', zIndex: '29', border: '0', borderRadius: '0', padding: '0', minHeight: '0', background: 'color-mix(in srgb, black 25%, transparent)', cursor: 'default' });
const errorCss = css({ position: 'absolute', left: t.s4, right: t.s4, bottom: t.s4, zIndex: '30', maxWidth: '560px', margin: '0 auto' });
const emptyCss = css({ flex: '1', minWidth: '0', display: 'grid', placeContent: 'center', justifyItems: 'center', gap: t.s3, color: t.textMuted, fontSize: t.textMd, '& p': { margin: '0' } });
const errorRow = css({ display: 'grid', gap: t.s2, justifyItems: 'start' });

export function ChatsScreen(handle: Handle<{ id?: string; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.ui); // the palette's New chat (s.ui.requestNewChat)
  const adapters = query(handle, { load: () => s.client.adapterList(), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  // New chat: made at once, in no project, with the agent CLI's own defaults, and opened; one at a time (a second press while
  // the first is being made would make two)
  let starting = false;
  let startError: Error | undefined;
  // a chat that could not be made (its CLI refused: a notice to accept, a licence) can be started with another agent CLI
  let chooseOpen = false;
  const open = async () => {
    if (starting) return;
    starting = true;
    startError = undefined;
    void handle.update();
    try {
      const c = await s.client.chatCreate({});
      const href = routes.chat.href({ id: c.id });
      if (handle.props.onNavigate) handle.props.onNavigate(href);
      else void navigate(href).catch(() => {});
    } catch (e) {
      startError = e instanceof Error ? e : new Error(String(e));
    } finally {
      starting = false;
      if (!handle.signal.aborted) void handle.update();
    }
  };
  // Ctrl+N: a new chat (WebView2 would open a new window: the app keeps the key)
  window.addEventListener(
    'keydown',
    (e) => {
      if (!isNewChatKey(e)) return;
      e.preventDefault();
      void open();
    },
    { signal: handle.signal },
  );

  return () => {
    const { id, onNavigate } = handle.props;
    if (s.ui.takeNewChat()) handle.queueTask(() => void open());
    const ws = s.workspace;
    // a narrow window (a Windows half-screen) shows the list or the chat, and the list over the chat when asked for
    const narrow = s.ui.narrowRails;
    const list = (layout: 'column' | 'full' | 'drawer') => <ChatList projectId={ws.projectId} selectedId={id} onNew={() => void open()} onNavigate={onNavigate} layout={layout} />;
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
            <p>Talk to an agent CLI directly.</p>
            <Button variant="primary" icon={Plus} disabled={starting} title="New chat (Ctrl+N)" mix={[on('click', () => void open())]} data-testid="chats-empty-new">
              {starting ? 'Starting…' : 'New chat'}
            </Button>
          </div>
        )}
        {startError ? (
          <div mix={[errorCss, errorRow]} data-testid="new-chat-error">
            <ErrorNote error={startError} what="New chat" />
            <Button size="sm" variant="secondary" data-testid="new-chat-choose" mix={on('click', () => ((chooseOpen = true), (startError = undefined), void handle.update()))}>
              Choose another agent
            </Button>
          </div>
        ) : null}
        <NewChatDialog
          open={chooseOpen}
          onClose={() => ((chooseOpen = false), void handle.update())}
          projects={ws.projectList}
          projectId={undefined}
          adapters={adapters.data}
          adaptersError={adapters.error}
          onCreated={(chatId) => {
            chooseOpen = false;
            const href = routes.chat.href({ id: chatId });
            if (onNavigate) onNavigate(href);
            else void navigate(href).catch(() => {});
          }}
        />
      </div>
    );
  };
}

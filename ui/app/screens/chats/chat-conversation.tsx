// One chat's conversation on its own, without the Chats list (decision P139): the header (title, folder chip, ⋯), the feed and
// the composer, for any page that shows a chat in place, as a project's Work view will. It reads the agent CLIs itself (the
// chat's model, effort and permission modes come from its CLI's capabilities), and again when a connection or an adapter
// changes. Keyed by chat id inside: another id is another conversation.
//
//   <ChatConversation chatId="chat_123" />
//   <ChatConversation chatId={id} onNavigate={(href) => ...} />   // a fork or a link opens through the page's own navigation

import type { Handle } from 'remix/component';
import { query, services } from '../../state/services.tsx';
import { ChatView } from './chat-view.tsx';

export interface ChatConversationProps {
  /** The chat to show. */
  chatId: string;
  /** Where a fork or a link to another chat goes; by default the app navigates to it. */
  onNavigate?: (href: string) => void;
}

export function ChatConversation(handle: Handle<ChatConversationProps>) {
  const s = services(handle);
  const adapters = query(handle, { load: () => s.client.adapterList(), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  return () => {
    const { chatId, onNavigate } = handle.props;
    return <ChatView key={chatId} id={chatId} adapters={adapters.data ?? []} onNavigate={onNavigate} />;
  };
}

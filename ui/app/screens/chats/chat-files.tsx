// The files the person sent with a chat message (decision P90), as the room's file cards: name, size, Save and, for a
// picture, Show image. The bytes are read through chat.attachment on request.

import type { Handle } from 'remix/component';
import type { ChatAttachment } from '../../api/chatClient.ts';
import { services } from '../../state/services.tsx';
import { FileCards } from '../room/attachments.tsx';

export function ChatFiles(handle: Handle<{ list: readonly ChatAttachment[] }>) {
  const s = services(handle);
  return () => <FileCards list={handle.props.list} read={(id) => s.client.chatAttachment(id)} />;
}

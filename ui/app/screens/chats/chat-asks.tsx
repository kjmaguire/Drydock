// The tool calls a chat's CLI is waiting to be allowed (an ACP agent asking over its own protocol; chats/permission.go). Each
// shows the call as stored and waits for the person: Allow once lets exactly that call run once, Refuse tells the agent no. A
// request nobody answers is refused when it expires; one still waiting when the turn ends is withdrawn.

import { on, type Handle } from 'remix/component';
import { isRecord } from '../../api/guards.ts';
import type { Chat, ChatAsk } from '../../api/chatClient.ts';
import { formatTime } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';

/** The input as a person reads it: the JSON shown whole, or nothing when there was none. */
export function askInput(a: Pick<ChatAsk, 'input'>): string {
  if (a.input === undefined || a.input === null) return '';
  return typeof a.input === 'string' ? a.input : JSON.stringify(a.input, null, 2);
}

export function ChatAsks(handle: Handle<{ chat: Chat; agentName: string }>) {
  const s = services(handle);
  const act = action(handle);
  const list = query(handle, {
    key: () => handle.props.chat.id,
    when: (e) => e.type.startsWith('chat.permission') && isRecord(e.data) && e.data.chatId === handle.props.chat.id,
    load: (): Promise<ChatAsk[]> => s.client.chatAsks(handle.props.chat.id),
  });
  const answer = async (id: string, allow: boolean) => {
    await act.run(`${allow ? 'allow' : 'refuse'}:${id}`, () => s.client.chatAnswer(handle.props.chat.id, id, allow));
    void list.reload();
  };
  return () => {
    const items = list.data ?? [];
    if (!items.length && !act.error) return null;
    return (
      <div style={{ display: 'grid', gap: '6px', padding: '8px 20px 0' }} data-testid="chat-asks">
        {items.map((a) => {
          const input = askInput(a);
          return (
            <Notice key={a.id} tone="warn" role="alert" testId="chat-ask" title={`${handle.props.agentName} asks to use ${visible(a.summary)}`}>
              {input ? (
                <code style={{ display: 'block', overflowWrap: 'anywhere', whiteSpace: 'pre-wrap', marginTop: '4px' }} data-testid="chat-ask-input">
                  {visible(input)}
                </code>
              ) : null}
              <span style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px', marginTop: '6px' }}>
                <Button size="sm" variant="primary" disabled={!!act.busy} mix={[on('click', () => void answer(a.id, true))]}>
                  Allow once
                </Button>
                <Button size="sm" variant="secondary" disabled={!!act.busy} mix={[on('click', () => void answer(a.id, false))]}>
                  Refuse
                </Button>
                <span style={{ fontSize: t.textXs }} data-testid="chat-ask-expiry">
                  Refused at {formatTime(a.expiresAt)} if nobody answers.
                </span>
              </span>
            </Notice>
          );
        })}
        <ErrorNote error={act.error} />
      </div>
    );
  };
}

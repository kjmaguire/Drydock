// One thread in the details panel (Slack's thread pane): the root message, its replies (room.thread), and a reply box that
// posts with thread_root. Re-read when a message is posted in the room.

import { css, on, ref, type Handle } from 'remix/component';
import { SendHorizontal, X } from 'lucide';
import type { Message } from '../../api/types.ts';
import { authorName, formatDateTime, roomLabel } from '../../lib/format.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { IconButton } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { composerBar, composerBox, sendCss } from './composer.tsx';
import { Attachments } from './attachments.tsx';
import { repliesText, shortTime } from './messages.ts';

const panelCss = css({ display: 'grid', gap: t.s3, minWidth: '0' });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  '& .title': { display: 'grid', flex: '1', minWidth: '0' },
  '& strong': { fontSize: t.textLg, fontWeight: '600' },
  '& .room': { color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
});
const logCss = css({ display: 'grid', gap: '2px', minWidth: '0' });
const msgCss = css({
  display: 'grid',
  gridTemplateColumns: '32px minmax(0, 1fr)',
  columnGap: t.s2,
  padding: `6px ${t.s2}`,
  borderRadius: t.rMd,
  '&:hover': { background: t.bgHover },
  '& .meta': { display: 'flex', alignItems: 'baseline', gap: '6px', flexWrap: 'wrap', lineHeight: '20px' },
  '& .meta strong': { fontSize: t.textMd, fontWeight: '600' },
  '& .meta time': { color: t.textFaint, fontSize: t.textXs },
  '& .body': { minWidth: '0', overflowWrap: 'anywhere', fontSize: t.textMd, lineHeight: '1.5' },
  '& .body p': { margin: '0' },
  '& .body p + p, & .body pre, & .body ul, & .body ol': { marginTop: '6px' },
});
const rootCss = css({ paddingBottom: t.s3 });
const countCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  margin: `2px ${t.s2}`,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '600',
  '&::after': { content: '""', flex: '1', height: '1px', background: t.border },
});
const formCss = css({ display: 'grid', gap: t.s2 });

export function ThreadPanel(handle: Handle<{ roomId: string; rootId: string; onClose: () => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  const thread = query(handle, {
    load: () => s.client.roomThread(handle.props.rootId),
    when: (e) => e.type === 'room.message_posted' && (e.data as { room?: string } | undefined)?.room === handle.props.roomId,
    key: () => handle.props.rootId,
  });
  const act = action(handle);
  let text = '';
  let area: HTMLTextAreaElement | undefined;
  const id = `thread-reply-${handle.id}`;

  const send = async () => {
    const body = text.trim();
    if (!body || act.busy) return;
    const ok = await act.run('reply', async () => {
      await s.client.roomPost(handle.props.roomId, body, handle.props.rootId);
      return true;
    });
    if (ok) {
      text = '';
      if (area) {
        area.value = '';
        area.style.height = 'auto';
      }
      thread.reload();
      void handle.update();
    }
  };

  const item = (m: Message, root: boolean) => {
    const agents = s.project.agentList;
    const name = authorName(m.author, agents);
    return (
      <article key={m.id} mix={[msgCss, root ? rootCss : undefined]} data-testid="thread-message" data-author-kind={m.author.kind} aria-label={`${m.author.kind} ${name}`}>
        <Avatar id={m.author.id} name={name} size={32} kind={m.author.kind === 'agent' ? 'agent' : m.author.kind === 'human' ? 'human' : 'system'} />
        <div style={{ minWidth: '0' }}>
          <header class="meta">
            <strong>{name}</strong>
            <time dateTime={m.created_at} title={formatDateTime(m.created_at)}>
              {shortTime(m.created_at)}
            </time>
          </header>
          <div class="body">
            <Markdown source={m.body} />
          </div>
          <Attachments m={m} />
        </div>
      </article>
    );
  };

  return () => {
    const { roomId, onClose } = handle.props;
    const msgs = thread.data ?? [];
    const room = s.project.roomList.find((r) => r.id === roomId);
    const [root, ...rest] = msgs;
    return (
      <div mix={panelCss} data-testid="thread">
        <div mix={headCss}>
          <div class="title">
            <strong>Thread</strong>
            {room ? <span class="room">#{roomLabel(room, s.project.agentList)}</span> : null}
          </div>
          <IconButton icon={X} label="Close" title="Close the thread" mix={[on('click', () => onClose())]} />
        </div>
        <ErrorNote error={thread.error ?? act.error} />
        {thread.loading && msgs.length === 0 ? <Loading /> : null}
        <div mix={logCss} role="log" aria-live="polite" aria-label="Thread messages">
          {root ? item(root, true) : null}
          {root ? <div mix={countCss}>{rest.length ? repliesText(rest.length) : 'No replies yet'}</div> : null}
          {rest.map((m) => item(m, false))}
        </div>
        <form
          mix={[
            formCss,
            on('submit', (e) => {
              e.preventDefault();
              void send();
            }),
          ]}
        >
          <label for={id} class="sr-only">
            Reply in thread
          </label>
          <div mix={composerBox}>
            <textarea
              id={id}
              rows={1}
              placeholder="Reply in thread..."
              mix={[
                ref((n) => {
                  area = n as HTMLTextAreaElement;
                  if (area.value !== text) area.value = text;
                }),
                on('input', (e) => {
                  const el = e.currentTarget as HTMLTextAreaElement;
                  text = el.value;
                  el.style.height = 'auto';
                  el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
                  void handle.update();
                }),
                on('keydown', (e) => {
                  const k = e as KeyboardEvent;
                  if (k.key === 'Enter' && (k.ctrlKey || k.metaKey)) {
                    k.preventDefault();
                    void send();
                  }
                }),
              ]}
            />
            <div mix={composerBar}>
              <span style={{ flex: '1', color: 'var(--text-faint)', fontSize: 'var(--text-xs)' }} aria-hidden="true">
                <kbd>Ctrl</kbd>+<kbd>Enter</kbd> to reply
              </span>
              <button type="submit" mix={sendCss} disabled={act.busy !== '' || !text.trim()}>
                <Icon icon={SendHorizontal} size={15} />
                Reply
              </button>
            </div>
          </div>
        </form>
      </div>
    );
  };
}

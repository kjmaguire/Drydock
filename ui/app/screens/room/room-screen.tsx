// A channel (Discord/Slack): the header (# name, topic, members, export, the task link), the agents bar, the message list
// with day dividers, groups, threads and a "new messages" line, and the composer at the bottom. The room comes from the
// project's room list, or from room.get so an archived task channel still opens.

import { css, on, ref, type Handle } from 'remix/component';
import { ArrowDown, Hash, ListChecks, MessageCircle, MessagesSquare } from 'lucide';
import type { Room } from '../../api/types.ts';
import { authorName, roomLabel } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { starts } from '../../state/live.ts';
import { query, services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { headerCss, Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { Composer } from './composer.tsx';
import { MessageFeed } from './feed.ts';
import { MessageItem } from './message-item.tsx';
import { buildRows, lastHumanMessageId, replyCounts } from './messages.ts';
import { RoomAgentsPanel } from './room-agents.tsx';
import { RoomExports } from './room-exports.tsx';
import { RoomMembers } from './room-members.tsx';

export function RoomScreen(handle: Handle<{ id: string }>) {
  const s = services(handle);
  watch(handle, s.project);
  const inList = () => s.project.roomList.find((r) => r.id === handle.props.id);
  // the room list is still coming (or the project is not chosen yet): wait for it before asking room.get
  const loadingRooms = () => !s.project.rooms.data && (s.project.rooms.loading || (!s.project.id && s.workspace.projects.loading));
  const fetched = query(handle, {
    load: () => s.client.roomGet(handle.props.id),
    when: (e) => starts(e, 'room.archived', 'room.created'),
    key: () => handle.props.id,
    enabled: () => !inList() && !loadingRooms(),
  });
  return () => {
    const room = inList() ?? (fetched.data?.id === handle.props.id ? fetched.data : undefined);
    if (room) return <Conversation key={room.id} room={room} />;
    if (loadingRooms() || fetched.loading) {
      return (
        <Page title="Channel" icon={Hash}>
          <Loading />
        </Page>
      );
    }
    return (
      <Page title="Channel" icon={Hash}>
        <Empty title="Channel not found" icon={MessagesSquare}>
          <p>It may belong to another project, or it was never created.</p>
          <ErrorNote error={fetched.error} />
        </Empty>
      </Page>
    );
  };
}

const screenCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0', background: t.bgApp });
const titleIcon = css({ display: 'inline-flex', color: t.textFaint });
const topicCss = css({
  minWidth: '0',
  paddingLeft: t.s3,
  borderLeft: `1px solid ${t.border}`,
  color: t.textMuted,
  fontSize: t.textMd,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, marginLeft: 'auto', flex: 'none' });
const modeCss = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap' });

const logWrap = css({ position: 'relative', flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' });
const logCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', overflowX: 'hidden', padding: `${t.s4} 0 ${t.s5}`, outline: 'none', '&:focus-visible': { boxShadow: `inset ${t.focus}` } });
const introCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s6} ${t.s6} ${t.s4}`,
  '& h2': { margin: '0', fontSize: t.text2xl, fontWeight: '700', letterSpacing: '-0.01em' },
  '& p': { margin: '0', color: t.textMuted, fontSize: t.textMd },
});
const introIcon = css({ display: 'grid', placeItems: 'center', width: '56px', height: '56px', borderRadius: t.rXl, background: t.accentSoft, color: t.accentText, marginBottom: t.s1 });
const dividerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  margin: `${t.s4} ${t.s6} ${t.s1}`,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '600',
  '&::before, &::after': { content: '""', flex: '1', height: '1px', background: t.border },
  '& span': { padding: '2px 10px', border: `1px solid ${t.border}`, borderRadius: t.rFull, background: t.bgApp },
});
const newCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  margin: `${t.s3} ${t.s6} 0 ${t.s4}`,
  color: t.danger,
  fontSize: t.textXs,
  fontWeight: '700',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  '&::before': { content: '""', flex: '1', height: '0', borderTop: `1px solid ${t.danger}` },
  '& span': { padding: '0 6px', borderRadius: t.rSm, background: t.danger, color: t.onAccent, lineHeight: '16px' },
});
const emptyCss = css({ margin: `0 ${t.s6}`, color: t.textMuted, fontSize: t.textMd });
const jumpCss = css({
  position: 'absolute',
  left: '50%',
  bottom: t.s3,
  transform: 'translateX(-50%)',
  zIndex: '3',
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '28px',
  minHeight: '0',
  padding: '0 12px',
  border: '0',
  borderRadius: t.rFull,
  background: t.accent,
  color: t.onAccent,
  fontSize: t.textSm,
  fontWeight: '600',
  boxShadow: t.shadowMd,
  '&:hover': { background: t.accentHover },
});
const notesCss = css({ padding: `0 ${t.s5}` });

const TITLE_ICON = { channel: Hash, task: ListChecks, dm: MessageCircle } as const;

/** The conversation of one room. Keyed by room id: a new room is a new feed. */
export function Conversation(handle: Handle<{ room: Room }>) {
  const s = services(handle);
  watch(handle, s.project);
  const feed = new MessageFeed(s.client, s.store, handle.props.room.id).start(handle.signal);
  const members = query(handle, {
    load: () => s.client.roomMembers(handle.props.room.id),
    when: (e) => e.type.startsWith('room.member'),
    key: () => handle.props.room.id,
  });

  let log: HTMLDivElement | undefined;
  let stick = true;
  let seenCount = 0;
  let marked = 0;
  /** Messages after this seq are "new" (the read cursor when the room was opened); undefined = no divider. */
  let newAfter: number | undefined;
  let captured = false;

  feed.addEventListener('change', () => {
    const msgs = feed.messages;
    if (!captured && !feed.loading) {
      captured = true;
      const u = s.project.unread.data?.find((x) => x.roomId === handle.props.room.id);
      if (u && u.unread > 0 && msgs.some((m) => m.seq > u.lastReadSeq && m.author.kind !== 'human')) newAfter = u.lastReadSeq;
    }
    // mark read (the core clamps and never moves the cursor back)
    const last = msgs.length ? msgs[msgs.length - 1]!.seq : 0;
    if (last > marked && !handle.props.room.archived) {
      marked = last;
      s.client.roomMarkRead(handle.props.room.id, last).catch(() => undefined);
    }
    void handle.update();
  });

  const toBottom = () => {
    if (log) log.scrollTop = log.scrollHeight;
  };

  return () => {
    const { room } = handle.props;
    const agents = s.project.agentList;
    const msgs = feed.messages;
    const replies = replyCounts(msgs);
    const rows = buildRows(msgs, { newAfterSeq: newAfter });
    const label = roomLabel(room, agents);
    const mem = members.data ?? [];
    if (stick) seenCount = msgs.length;
    const unseen = msgs.length - seenCount;
    handle.queueTask(() => {
      if (stick) toBottom();
    });
    return (
      <section mix={screenCss} class="conversation" aria-label={`Conversation ${room.name}`}>
        <header mix={headerCss}>
          <h1>
            <span mix={titleIcon}>
              <Icon icon={TITLE_ICON[room.kind as keyof typeof TITLE_ICON] ?? Hash} size={18} />
            </span>
            {label}
          </h1>
          {room.archived ? (
            <Badge tone="muted" title="This channel is archived: it can be read, not written">
              archived
            </Badge>
          ) : null}
          {room.topic ? (
            <div mix={topicCss} title={room.topic}>
              {room.topic}
            </div>
          ) : null}
          <div mix={actionsCss}>
            <span mix={modeCss} title="How agents take turns in this room">
              {room.mode} mode
            </span>
            {room.kind === 'task' && room.taskId ? (
              <a href={routes.task.href({ id: room.taskId })} mix={buttonStyle('secondary', 'sm')}>
                <Icon icon={ListChecks} size={14} />
                Task details
              </a>
            ) : null}
            <RoomMembers members={mem} agents={agents} />
            <RoomExports room={room} />
          </div>
        </header>
        {!room.archived ? <RoomAgentsPanel room={room} agents={agents} members={mem} lastHumanMessageId={lastHumanMessageId(msgs)} /> : null}
        {feed.error ? (
          <div mix={notesCss} style={{ paddingTop: '12px' }}>
            <ErrorNote error={feed.error} what="Could not load messages" />
          </div>
        ) : null}
        <div mix={logWrap}>
          <div
            mix={[
              logCss,
              ref((n) => {
                log = n as HTMLDivElement;
              }),
              on('scroll', (e) => {
                const el = e.currentTarget as HTMLDivElement;
                const was = stick;
                stick = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
                if (stick !== was) void handle.update();
              }),
            ]}
            role="log"
            aria-live="polite"
            aria-relevant="additions"
            aria-label={`Messages in ${room.name}`}
            tabIndex={0}
          >
            {!feed.loading ? (
              <div mix={introCss}>
                <span mix={introIcon} aria-hidden="true">
                  <Icon icon={TITLE_ICON[room.kind as keyof typeof TITLE_ICON] ?? Hash} size={28} />
                </span>
                <h2>{room.kind === 'dm' ? label : `Welcome to #${label}`}</h2>
                <p>
                  {room.kind === 'dm'
                    ? `This is the beginning of your direct messages with ${label}.`
                    : room.kind === 'task'
                      ? `This channel follows the task: the agents' reports, check results and reviews land here.`
                      : `This is the start of the #${label} channel.`}
                  {room.topic ? ` ${room.topic}` : ''}
                </p>
              </div>
            ) : null}
            {feed.loading && msgs.length === 0 ? <Loading label="Loading messages..." /> : null}
            {!feed.loading && msgs.length === 0 && !feed.error ? <p mix={emptyCss}>No messages yet.</p> : null}
            {rows.map((r) =>
              r.kind === 'day' ? (
                <div key={r.key} mix={dividerCss} role="separator" aria-label={r.label}>
                  <span>{r.label}</span>
                </div>
              ) : r.kind === 'new' ? (
                <div key={r.key} mix={newCss} role="separator" aria-label="New messages">
                  <span>New</span>
                </div>
              ) : (
                <MessageItem
                  key={r.key}
                  m={r.m}
                  name={authorName(r.m.author, agents)}
                  head={r.head}
                  replies={replies.get(r.m.id) ?? 0}
                  onReply={() => s.ui.openThread(room.id, r.m.id)}
                  onOpenRoot={r.m.thread_root_id ? () => s.ui.openThread(room.id, r.m.thread_root_id!) : undefined}
                />
              ),
            )}
          </div>
          {!stick ? (
            <button
              type="button"
              mix={[
                jumpCss,
                on('click', () => {
                  stick = true;
                  toBottom();
                  void handle.update();
                }),
              ]}
            >
              <Icon icon={ArrowDown} size={14} />
              {unseen > 0 ? `${unseen} new ${unseen === 1 ? 'message' : 'messages'}` : 'Jump to the latest'}
            </button>
          ) : null}
        </div>
        <Composer
          room={room}
          agents={agents}
          members={mem}
          rooms={s.project.roomList}
          disabled={room.archived}
          onSent={() => {
            stick = true;
            newAfter = undefined;
            void handle.update();
          }}
        />
      </section>
    );
  };
}

// A channel: the header (name, topic, members, export, the task link), the agents bar, the message list
// with day dividers, groups, threads and a "new messages" line, and the composer at the bottom. The room comes from the
// project's room list, or from room.get so an archived task channel still opens.

import { css, on, ref, type Handle } from 'remix/component';
import { ArrowDown, Bell, BellOff, ListChecks, MessageSquare, MessagesSquare } from 'lucide';
import type { Message, Room } from '../../api/types.ts';
import { authorName, roomLabel } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { starts } from '../../state/live.ts';
import { action, query, retryFor, services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Icon, roomIcon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { VirtualList } from '../../ui/virtual-list.tsx';
import { askableAgents, askAgainOf, newestHumanMessage, outcomeWords } from './asks.ts';
import { Composer } from './composer.tsx';
import { MessageFeed } from './feed.ts';
import { CheckGroup, MessageItem } from './message-item.tsx';
import { buildRows, replyCounts, type Row } from './messages.ts';
import { RoomExports } from './room-exports.tsx';
import { RoomMembers } from './room-members.tsx';
import { modeWords } from './turn-taking.ts';

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
        <Page title="Room" icon={MessageSquare}>
          <Loading />
        </Page>
      );
    }
    return (
      <Page title="Room" icon={MessageSquare}>
        <Empty title="Room not found" icon={MessagesSquare}>
          <p>It may belong to another project, or it was never created.</p>
          <ErrorNote error={fetched.error} retry={fetched.reload} />
        </Empty>
      </Page>
    );
  };
}

const screenCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0', background: t.bgApp });
// the room's header (Crew HQ, decision P64): a tile with the room's icon, the name with its topic beneath, the actions
const roomHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  flex: 'none',
  minHeight: '68px',
  padding: `${t.s3} ${t.s6} ${t.s3} ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  '& h1': { margin: '0', fontSize: t.textXl, fontWeight: '600', lineHeight: '24px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .title': { display: 'grid', minWidth: '0', gap: '1px' },
  '& .title .line': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
});
const titleIcon = css({ display: 'grid', placeItems: 'center', flex: 'none', width: '38px', height: '38px', borderRadius: '12px', background: t.bgActive, color: t.textMuted });
const topicCss = css({ minWidth: '0', color: t.textMuted, fontSize: t.textSm, lineHeight: '18px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' });
// the header's actions shrink and wrap before they push the room past its column (they ran under the Details panel at 1280 px)
const actionsCss = css({ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', flexWrap: 'wrap', gap: t.s2, marginLeft: 'auto', flex: '0 1 auto', minWidth: '0' });
const modeBtn = css({
  height: '30px',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textMuted,
  cursor: 'pointer',
  '&:hover': { borderColor: t.borderStrong, color: t.text },
});
const modeCss = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', '@media (max-width: 719px)': { display: 'none' } });

const logWrap = css({ position: 'relative', flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' });
const logCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', overflowX: 'hidden', padding: `${t.s4} 0 ${t.s5}`, outline: 'none', '&:focus-visible': { boxShadow: `inset ${t.focus}` } });
// the start of a room: a quiet heading and one line, not a banner
const introCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr)',
  alignItems: 'center',
  columnGap: t.s3,
  rowGap: '2px',
  padding: `${t.s5} ${t.s6} ${t.s3} ${t.s5}`,
  '& h2': { margin: '0', fontSize: t.textLg, fontWeight: '600' },
  '& p': { gridColumn: '2', margin: '0', color: t.textMuted, fontSize: t.textMd },
});
const introIcon = css({
  gridRow: 'span 2',
  display: 'grid',
  placeItems: 'center',
  width: '40px',
  height: '40px',
  borderRadius: '12px',
  background: t.bgActive,
  color: t.textMuted,
});
const dividerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  margin: `${t.s4} ${t.s6} ${t.s1} ${t.s5}`,
  color: t.textFaint,
  fontSize: t.textXs,
  fontWeight: '500',
  '&::before, &::after': { content: '""', flex: '1', height: '1px', background: t.border },
});
const newCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  margin: `${t.s3} ${t.s6} 0 ${t.s5}`,
  color: t.danger,
  fontSize: t.textXs,
  fontWeight: '500',
  '&::before': { content: '""', flex: '1', height: '0', borderTop: `1px solid color-mix(in srgb, ${t.danger} 45%, transparent)` },
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
// the ask row (X12): why nobody answered, then one button per agent that can be asked
const askRowCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: t.s2,
  margin: '0',
  padding: `${t.s2} ${t.s6} 0 ${t.s5}`,
  '& .why': { color: t.textMuted, fontSize: t.textSm },
});
const olderCss = css({ display: 'flex', justifyContent: 'center', padding: `${t.s2} 0` });

import { windowAttended } from '../../lib/attended.ts';
export { windowAttended };


/** The conversation of one room. Keyed by room id: a new room is a new feed. */
export function Conversation(handle: Handle<{ room: Room }>) {
  const s = services(handle);
  watch(handle, s.project);
  const feed = new MessageFeed(s.client, s.store, handle.props.room.id, 100).start(handle.signal);
  const members = query(handle, {
    load: () => s.client.roomMembers(handle.props.room.id),
    when: (e) => e.type.startsWith('room.member'),
    key: () => handle.props.room.id,
  });
  // who answers in the room now (decision P130): the composer's Stop and Esc Esc stop them
  const turns = query(handle, {
    load: () => s.client.roomTurns(handle.props.room.id, 10),
    when: (e) => starts(e, 'room.turn_', 'room.message_posted', 'agent.started', 'agent.waiting'),
    key: () => handle.props.room.id,
    enabled: () => !handle.props.room.archived,
  });
  // the project's own "agents answer" switch (not Enabled, which also covers the license): while it is off the room says so
  // with Turn on, and nothing promises an answer to a mention (X13)
  const dev = query(handle, {
    load: () => s.client.projectdevGet(handle.props.room.projectId),
    when: (e) => e.type === 'project.dev_settings' && e.projectId === handle.props.room.projectId,
    key: () => handle.props.room.projectId,
    enabled: () => !handle.props.room.archived,
  });
  // what the newest chain of the room did (X12): why nobody answered the person's message, with Ask under it
  const outcome = query(handle, {
    load: () => s.client.roomReplyOutcome(handle.props.room.id),
    when: (e) => e.type === 'room.reply_outcome' && (e.data as { room?: string } | undefined)?.room === handle.props.room.id,
    key: () => handle.props.room.id,
    enabled: () => !handle.props.room.archived,
  });
  const act = action(handle);
  /** The person's message the ask row is open for from its hover action; undefined: only after a message nobody answered. */
  let askFor: string | undefined;
  /** What the last Ask said, when it gave no answer (X12): the outcome in words. */
  let askNote = '';
  const ask = (messageId: string, agentId: string) =>
    void act.run('ask', async () => {
      askFor = undefined;
      const o = await s.client.roomReply(messageId, agentId);
      askNote = (o?.turns ?? []).length ? '' : outcomeWords(o);
      await Promise.all([turns.reload(), outcome.reload()]);
    });
  const turnOn = () =>
    void act.run('replies', async () => {
      const cur = dev.data ?? (await s.client.projectdevGet(handle.props.room.projectId));
      dev.set(await s.client.projectdevSet(handle.props.room.projectId, { ...cur, respond: true }));
    });
  const stop = () =>
    void act.run('stop', async () => {
      await s.client.roomStop(handle.props.room.id);
      await turns.reload();
    });
  const setMuted = (muted: boolean) =>
    void act.run('mute', async () => {
      await s.client.roomSetMuted(handle.props.room.id, muted);
      await s.project.unread.reload();
    });

  // the rows near the view are rendered; the list stays at the bottom while the reader is there (ui/virtual-list.tsx)
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal, estimate: 44 });
  /** The newest seq seen while at the bottom: only messages after it count as unseen. */
  let seenSeq = 0;
  let marked = 0;
  /** Messages after this seq are "new" (the read cursor when the room was opened); undefined = no divider. */
  let newAfter: number | undefined;
  let captured = false;
  /** The log announces additions only once the first page is on screen (not the whole history on open). */
  let announce = false;

  /**
   * Mark the room read up to the newest message, only while the person can see it: the window is visible and focused and
   * the log is at the bottom (the core clamps and never moves the cursor back).
   */
  const markRead = () => {
    const last = feed.lastSeq;
    if (last <= marked || handle.props.room.archived || !list.stick || !windowAttended()) return;
    marked = last;
    s.client.roomMarkRead(handle.props.room.id, last).catch(() => undefined);
  };
  window.addEventListener('focus', markRead, { signal: handle.signal });
  document.addEventListener('visibilitychange', markRead, { signal: handle.signal });

  feed.addEventListener('change', () => {
    const msgs = feed.messages;
    if (!captured && !feed.loading) {
      captured = true;
      const u = s.project.unread.data?.find((x) => x.roomId === handle.props.room.id);
      if (u && u.unread > 0 && msgs.some((m) => m.seq > u.lastReadSeq && m.author.kind !== 'human')) newAfter = u.lastReadSeq;
      // the first page is in the DOM after this render: from then on new messages are announced
      handle.queueTask(() => {
        if (!announce) {
          announce = true;
          void handle.update();
        }
      });
    }
    markRead();
    void handle.update();
  });

  // the rows are rebuilt only when the messages or the "new" line change, not on every scroll that moves the window
  let built: { msgs: readonly Message[]; newAfter: number | undefined; rows: Row[]; replies: Map<string, number>; byId: Map<string, Message> } | undefined;
  const rowsOf = (msgs: readonly Message[]) => {
    if (built?.msgs !== msgs || built.newAfter !== newAfter)
      built = { msgs, newAfter, rows: buildRows(msgs, { newAfterSeq: newAfter }), replies: replyCounts(msgs), byId: new Map(msgs.map((m) => [m.id, m])) };
    return built;
  };

  // the page is added above; the list keeps the row at the top of the view where it was
  const loadOlder = async () => {
    if (!feed.hasMore || feed.loadingOlder) return;
    await feed.loadOlder();
  };

  return () => {
    const { room } = handle.props;
    const agents = s.project.agentList;
    const msgs = feed.messages;
    const { rows, replies, byId } = rowsOf(msgs);
    const label = roomLabel(room, agents);
    const mem = members.data ?? [];
    const muted = !!s.project.unread.data?.find((u) => u.roomId === room.id)?.muted;
    const repliesOff = !room.archived && dev.data?.respond === false;
    const answering = (turns.data ?? [])
      .filter((x) => x.status === 'running')
      .map((x) => agents.find((a) => a.id === x.agentId)?.name || x.agentId)
      .join(' and ');
    const askable = room.archived || repliesOff ? [] : askableAgents(mem);
    const nameOf = (id: string) => agents.find((a) => a.id === id)?.name || id;
    // the ask row (X12): under a message of the person nobody answered (the responder's outcome says why), or for the message
    // whose hover action was clicked; never while an agent answers it
    const newest = newestHumanMessage(msgs);
    const o = outcome.data;
    const unanswered = !!o && o.trigger === newest && !(o.turns ?? []).length && !answering ? outcomeWords(o) : '';
    const askTarget = askFor ?? (unanswered ? newest : undefined);
    const stick = list.stick;
    if (stick) seenSeq = feed.lastSeq;
    // the messages are in seq order: count back from the newest (older pages above are never "new")
    let unseen = 0;
    if (!stick) for (let i = msgs.length - 1; i >= 0 && msgs[i]!.seq > seenSeq; i--) unseen++;
    // before reading list.quiet: rows revealed by this render (not new ones) keep the live region silent
    const shown = list.rows(
      rows,
      (r) => r.key,
      (r) =>
        r.kind === 'day' ? (
          <div mix={dividerCss} role="separator" aria-label={r.label}>
            <span>{r.label}</span>
          </div>
        ) : r.kind === 'new' ? (
          <div mix={newCss} role="separator" aria-label="New messages">
            <span>New</span>
          </div>
        ) : r.kind === 'checks' ? (
          <CheckGroup ms={r.ms} />
        ) : (
          <MessageItem
            m={r.m}
            name={authorName(r.m.author, agents)}
            role={r.m.author.kind === 'agent' ? agents.find((a) => a.id === r.m.author.id)?.role : undefined}
            head={r.head}
            replies={replies.get(r.m.id) ?? 0}
            onReply={() => s.ui.openThread(room.id, r.m.id)}
            onOpenRoot={r.m.thread_root_id ? () => s.ui.openThread(room.id, r.m.thread_root_id!) : undefined}
            onAsk={
              askable.length && r.m.author.kind === 'human' && r.m.type === 'CHAT'
                ? () => {
                    askFor = r.m.id;
                    void handle.update();
                  }
                : undefined
            }
            {...askAgainProps(r.m)}
          />
        ),
    );
    function askAgainProps(m: Message) {
      const again = askAgainOf(m, (id) => byId.get(id));
      if (!again || !askable.includes(again.agentId)) return {};
      return { onAskAgain: () => ask(again.messageId, again.agentId), askAgainName: nameOf(again.agentId) };
    }
    handle.queueTask(() => list.settle());
    return (
      <section mix={screenCss} class="conversation" aria-label={`Conversation ${room.name}`}>
        <header mix={roomHead}>
          <span mix={titleIcon} aria-hidden="true">
            <Icon icon={roomIcon(room.kind)} size={18} />
          </span>
          <div class="title">
            <div class="line">
              <h1>{label}</h1>
              {room.archived ? (
                <Badge tone="muted" title="This room is archived: it can be read, not written">
                  archived
                </Badge>
              ) : null}
            </div>
            <div mix={topicCss} title={room.topic || undefined}>
              {room.topic ||
                (room.kind === 'dm'
                  ? 'A direct conversation'
                  : room.kind === 'task'
                    ? "The task's own room: reports, check results and reviews"
                    : repliesOff
                      ? 'The whole crew and you'
                      : 'The whole crew and you; agents answer when you mention them')}
            </div>
          </div>
          <div mix={actionsCss}>
            {/* a direct message has one agent: no turn-taking, so no mode chip (X16) */}
            {room.kind === 'dm' ? null : room.archived ? (
              <span mix={modeCss} title="How agents took turns in this room">
                {modeWords(room, agents)}
              </span>
            ) : (
              <button type="button" mix={[modeCss, modeBtn, on('click', () => s.ui.openRoomPanel())]} data-testid="room-mode" title={`${room.mode} mode: who answers in this room and how. Opens the Room tab on the right`}>
                {repliesOff ? 'Agents do not answer' : modeWords(room, agents)}
              </button>
            )}
            {room.kind === 'task' && room.taskId ? (
              <a href={routes.task.href({ id: room.taskId })} mix={buttonStyle('secondary', 'sm')}>
                <Icon icon={ListChecks} size={14} />
                Task details
              </a>
            ) : null}
            <RoomMembers members={mem} agents={agents} />
            <RoomExports room={room} />
            {room.archived ? null : (
              <IconButton
                icon={muted ? BellOff : Bell}
                label={muted ? 'Unmute room' : 'Mute room'}
                title={muted ? 'Muted: only messages that name you count. Click to unmute' : 'Mute room: only messages that name you will count'}
                data-testid="room-mute"
                disabled={act.busy === 'mute'}
                mix={[on('click', () => setMuted(!muted))]}
              />
            )}
          </div>
        </header>
        {act.error ? (
          <div mix={notesCss} style={{ paddingTop: '12px' }}>
            <ErrorNote error={act.error} what="Not done" />
          </div>
        ) : null}
        {repliesOff ? (
          <div mix={notesCss} style={{ paddingTop: '12px' }}>
            <Notice tone="warn" testId="replies-off">
              Agents do not answer in this project's rooms ·{' '}
              <Button variant="link" size="sm" disabled={act.busy === 'replies'} mix={[on('click', turnOn)]}>
                {act.busy === 'replies' ? 'Turning on...' : 'Turn on'}
              </Button>
            </Notice>
          </div>
        ) : null}
        {feed.error ? (
          <div mix={notesCss} style={{ paddingTop: '12px' }}>
            <ErrorNote error={feed.error} retry={retryFor(feed)} what="Could not load messages" />
          </div>
        ) : null}
        <div mix={logWrap}>
          <div
            mix={[
              logCss,
              ref(list.scroller),
              on('scroll', (e) => {
                const el = e.currentTarget as HTMLDivElement;
                const was = list.stick;
                list.onScroll();
                if (el.scrollTop < 80 && feed.hasMore && !feed.loadingOlder) void loadOlder();
                if (list.stick && !was) markRead();
              }),
            ]}
            role="log"
            aria-live={announce && !feed.loadingOlder && !list.quiet ? 'polite' : 'off'}
            aria-relevant="additions"
            aria-busy={feed.loading || feed.loadingOlder ? 'true' : undefined}
            aria-label={`Messages in ${room.name}`}
            tabIndex={0}
          >
            {feed.hasMore ? (
              <div mix={olderCss}>
                <Button size="sm" variant="ghost" disabled={feed.loadingOlder} data-testid="room-older" mix={[on('click', () => void loadOlder())]}>
                  {feed.loadingOlder ? 'Loading older messages...' : 'Load older messages'}
                </Button>
              </div>
            ) : null}
            {!feed.loading && !feed.hasMore ? (
              <div mix={introCss}>
                <span mix={introIcon} aria-hidden="true">
                  <Icon icon={roomIcon(room.kind)} size={18} />
                </span>
                <h2>{label}</h2>
                <p>
                  {room.kind === 'dm'
                    ? `This is the beginning of your direct messages with ${label}.`
                    : room.kind === 'task'
                      ? `This room follows the task: the agents' reports, check results and reviews land here.`
                      : `This is the start of the ${label} room.`}
                  {room.topic ? ` ${room.topic}` : ''}
                </p>
              </div>
            ) : null}
            {feed.loading && msgs.length === 0 ? <Loading label="Loading messages..." /> : null}
            {!feed.loading && msgs.length === 0 && !feed.error ? <p mix={emptyCss}>No messages yet.</p> : null}
            {shown}
          </div>
          {/* away from the bottom the new rows are not rendered, so the log cannot announce them: the count is said instead */}
          <span class="sr-only" role="status" data-testid="room-unseen">
            {!stick && unseen > 0 ? `${unseen} new ${unseen === 1 ? 'message' : 'messages'} below` : ''}
          </span>
          {!stick ? (
            <button
              type="button"
              mix={[
                jumpCss,
                on('click', () => {
                  list.toBottom();
                  markRead();
                }),
              ]}
            >
              <Icon icon={ArrowDown} size={14} />
              {unseen > 0 ? `${unseen} new ${unseen === 1 ? 'message' : 'messages'}` : 'Jump to the latest'}
            </button>
          ) : null}
        </div>
        {askTarget && askable.length ? (
          <div mix={askRowCss} role="group" aria-label="Ask an agent to answer" data-testid="room-ask">
            <span class="why" data-testid="room-ask-why">
              {askFor ? 'Ask an agent to answer this message:' : unanswered}
            </span>
            {askable.map((id) => (
              <Button key={id} size="sm" disabled={!!act.busy} data-testid="room-ask-agent" mix={[on('click', () => ask(askTarget, id))]}>
                Ask {nameOf(id)}
              </Button>
            ))}
            {askFor ? (
              <Button size="sm" variant="ghost" mix={[on('click', () => ((askFor = undefined), void handle.update()))]}>
                Cancel
              </Button>
            ) : null}
          </div>
        ) : askNote ? (
          <p mix={askRowCss} role="status" data-testid="room-ask-note">
            <span class="why">{askNote}</span>
          </p>
        ) : null}
        <Composer
          room={room}
          agents={agents}
          members={mem}
          rooms={s.project.roomList}
          disabled={room.archived}
          answering={answering}
          repliesOff={repliesOff}
          stopping={act.busy === 'stop'}
          onStop={stop}
          onSent={() => {
            newAfter = undefined;
            list.toBottom();
          }}
        />
      </section>
    );
  };
}

// The conversation of one chat (graphite P97, calm chat P139). The header bar is the title (renamed in place), the folder (or
// the project) as a chip, one warning chip when the CLI is in a bad state, and ⋯: the CLI and its state, where the chat runs,
// its skills and MCP servers, then Allowed tools, Properties, Fork and Move to a project. Then the messages in a reading column
// (older pages on scroll-up, the streamed answer outside the live region, a request the agent waits on as an amber card where
// it was made): the tool calls between two pieces of text are one quiet line, a turn's file edits one "Changed N files" card,
// and after the final answer the replies it invites (chat-groups.ts). Ctrl+O opens every line (verbose, kept per viewer). The
// composer's row holds the agent CLI, the model and effort (the chat settings), the approval mode, attach and send.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowDown, ArrowLeft, Bot, ChevronDown, Ellipsis, Folder, FolderInput, GitFork, PanelLeftOpen, Pencil, ShieldCheck, SlidersHorizontal, TriangleAlert } from 'lucide';
import { isRecord } from '../../api/guards.ts';
import type { Chat, ChatAsk, ChatMessage } from '../../api/chatClient.ts';
import type { AdapterInfo, Connection } from '../../api/types.ts';
import { needsConfirmation, skipsApprovals } from '../../lib/permission.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { headerCss } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { Popover } from '../../ui/popover.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ModelField } from '../shared/model-field.tsx';
import { SkipApprovals } from '../shared/skip-approvals.tsx';
import { VirtualList } from '../../ui/virtual-list.tsx';
import { AskCard, ChatAsks } from './chat-asks.tsx';
import { BurstLine, ChangesCard, ReplyChips } from './chat-burst.tsx';
import { ChatComposer, smallBtn } from './chat-composer.tsx';
import { ChatFeed } from './chat-feed.ts';
import { burstLine, burstState, isVerboseKey, memoGroups, readVerbose, suggestReplies, thinkingMs, writeVerbose, type FeedItem, type Reply } from './chat-groups.ts';
import { ChatMessageRow } from './chat-message.tsx';
import { MoveChatDialog } from './chat-move.tsx';
import type { ComposerFile } from './chat-composer.tsx';
import { ThinkingFold } from './chat-thinking.tsx';
import { ChatApprovals, ChatPropertiesDialog } from './chat-properties.tsx';
import { adapterLabel, chatCliText, chatEffort, chatModelText, chatPlaceWords, chatTitle, chatWhere, cliWarning, folderChipText, folderText, modeWords, modelName, propsChips, RETRY_TEXT, startsGroup } from './chats.ts';

const screenCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0', flex: '1', background: t.bgApp });
const barCss = css({ flexWrap: 'nowrap', gap: '10px', padding: `${t.s2} ${t.s6}`, '& h1': { flex: '0 1 auto', maxWidth: '50%' } });
const titleBtn = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  minWidth: '0',
  maxWidth: '100%',
  height: '28px',
  padding: '0 6px',
  margin: '0',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  fontWeight: '600',
  '& .txt': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& svg': { flex: 'none', color: t.textFaint, opacity: '0' },
  '&:hover': { background: t.bgHover },
  '&:hover svg, &:focus-visible svg': { opacity: '1' },
});
const titleInput = css({ height: '28px', minWidth: '240px', fontSize: t.textMd, fontWeight: '600' });
// the folder (or the project) chip beside the title, and the one warning chip
const headChip = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  flex: '0 1 auto',
  minWidth: '0',
  height: '24px',
  padding: '0 9px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textMuted,
  fontSize: t.textXs,
  whiteSpace: 'nowrap',
  '& svg': { flex: 'none' },
  '& .txt': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '@media (max-width: 719px)': { '&[data-testid="chat-folder"]': { display: 'none' } },
});
const warnChip = css({
  minHeight: '0',
  borderColor: `color-mix(in srgb, ${t.warning} 50%, ${t.border})`,
  background: t.warningSoft,
  color: t.warning,
  fontWeight: '500',
  '&:hover': { borderColor: t.warning },
});
const actionsCss = css({ display: 'flex', alignItems: 'center', gap: '4px', marginLeft: 'auto', flex: 'none' });
const moreBtn = css({
  display: 'inline-grid',
  placeItems: 'center',
  width: '30px',
  height: '30px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  '&:hover, &[aria-expanded="true"]': { background: t.bgHover, color: t.text },
});
// the ⋯ panel: what the chat is (its CLI with its state, where it runs, its skills and servers), then what can be done with it
const moreCss = css({
  display: 'grid',
  gap: '2px',
  width: '300px',
  maxWidth: 'calc(100vw - 32px)',
  '& .facts': { display: 'grid', gap: '4px', padding: '2px 4px 8px', borderBottom: `1px solid ${t.border}`, marginBottom: '4px', fontSize: t.textSm, color: t.textMuted },
  '& .cli': { display: 'flex', alignItems: 'center', gap: '6px', color: t.text, fontWeight: '500' },
  '& .dot': { width: '7px', height: '7px', borderRadius: '50%', flex: 'none', background: t.textFaint },
  '& .cli[data-state="session"] .dot, & .cli[data-state="running"] .dot': { background: t.success },
  '& .cli[data-state="error"] .dot': { background: t.danger },
  '& .state': { color: t.textMuted, fontWeight: '400' },
  '& .path': { fontFamily: t.mono, fontSize: t.textXs, color: t.textFaint, overflowWrap: 'anywhere' },
  '& .chips': { display: 'flex', flexWrap: 'wrap', gap: '4px', padding: '0', border: '0', background: 'transparent', minHeight: '0', justifyContent: 'flex-start' },
  '& .chip': { display: 'inline-flex', alignItems: 'center', height: '22px', padding: '0 8px', border: `1px solid ${t.border}`, borderRadius: '11px', color: t.textMuted, fontSize: t.textXs },
  '& .act': {
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
    width: '100%',
    minHeight: '30px',
    padding: '0 8px',
    border: '0',
    borderRadius: t.rMd,
    background: 'transparent',
    color: t.text,
    fontSize: t.textSm,
    textAlign: 'left',
  },
  '& .act:hover': { background: t.bgHover },
  '& .act:disabled': { opacity: '0.5' },
  '& .act svg': { color: t.textMuted, flex: 'none' },
  '& .act .n': { marginLeft: 'auto', color: t.textFaint, fontVariantNumeric: 'tabular-nums' },
});

const logWrap = css({ position: 'relative', flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' });
const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', overflowX: 'hidden', outline: 'none', '&:focus-visible': { boxShadow: `inset ${t.focus}` } });
// the conversation reads in a column of 860 px at most, in the middle of the window
const columnCss = css({ width: '100%', maxWidth: '860px', margin: '0 auto', padding: '22px 24px', boxSizing: 'border-box' });
const olderCss = css({ display: 'flex', justifyContent: 'center', padding: `0 0 ${t.s2}` });
const streamCss = css({
  padding: '5px 0',
  '& .md': { color: t.text, lineHeight: '1.65', maxWidth: '720px' },
  '& .typing': { display: 'inline-flex', gap: '3px', alignItems: 'center', height: '20px' },
  '& .typing i': { width: '5px', height: '5px', borderRadius: '50%', background: t.textFaint, animation: 'dd-chat-pulse 1.2s ease-in-out infinite' },
  '& .typing i:nth-child(2)': { animationDelay: '0.2s' },
  '& .typing i:nth-child(3)': { animationDelay: '0.4s' },
  '@keyframes dd-chat-pulse': { '50%': { opacity: '0.35' } },
});
const noteCss = css({ width: '100%', maxWidth: '860px', margin: '0 auto', padding: `${t.s2} ${t.s6} 0`, boxSizing: 'border-box' });
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
const ruleList = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'flex', alignItems: 'center', gap: t.s2, minHeight: '28px', fontSize: t.textSm },
  '& li code': { flex: '1', minWidth: '0', overflowWrap: 'anywhere' },
});
const hintCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });
const toolsHead = css({ display: 'flex', alignItems: 'center', gap: '6px', '& h2': { margin: '0' } });

/** The chat's state for the dot beside its CLI, and the words a reader is told. */
function chatState(c: Chat, running: boolean): { state: 'running' | 'error' | 'session' | 'none'; words: string } {
  if (running) return { state: 'running', words: 'Running' };
  if (c.status === 'error') return { state: 'error', words: 'Last turn failed' };
  return c.sessionId ? { state: 'session', words: 'Session kept' } : { state: 'none', words: 'No session yet' };
}

/** The conversation of one chat. Keyed by chat id: a new chat is a new feed. */
export function ChatView(handle: Handle<{ id: string; adapters: AdapterInfo[]; onNavigate?: (href: string) => void; onShowList?: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const feed = new ChatFeed(s.client, s.store, handle.props.id).start(handle.signal);
  const act = action(handle);
  // the answers to the requests the agent waits on: their own action, so a refusal shows on the request it was for
  const askAct = action(handle);
  let askErrFor = '';
  // the rows near the view are rendered; the list stays at the bottom while the reader is there (ui/virtual-list.tsx)
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal, estimate: 40 });
  // the rows of the conversation (bursts, changes cards), worked out again only when the feed's list is a new one (P139)
  const groups = memoGroups();
  // the replies offered after the final answer, worked out once per answer
  let repliesFor = '';
  let replies: Reply[] = [];
  // verbose (Ctrl+O): every burst open, kept per viewer; a burst the person opened or closed keeps that until the next toggle
  let verbose = readVerbose();
  const opened = new Map<string, boolean>();
  /** The newest message seen while at the bottom: only messages after it are "new" (older pages above are not). */
  let seenSeq = 0;
  let editing = false;
  let draftTitle = '';
  let settingsOpen = false;
  let modeOpen = false;
  let moreOpen = false; // the ⋯ panel
  let moreView: 'main' | 'tools' = 'main'; // the ⋯ panel shows its actions, or the allowed tools
  let propsOpen = false; // the properties dialog (decision P60): CLI, folder, skills, MCP servers
  let moveOpen = false;
  let newRule = '';
  const closeMore = () => ((moreOpen = false), (moreView = 'main'));
  const openProps = () => (closeMore(), (propsOpen = true), void handle.update());

  feed.addEventListener('change', () => void handle.update());
  window.addEventListener(
    'keydown',
    (e) => {
      if (e.defaultPrevented || !isVerboseKey(e)) return;
      e.preventDefault(); // WebView2 would offer to open a file
      verbose = !verbose;
      writeVerbose(verbose);
      opened.clear();
      void handle.update();
    },
    { signal: handle.signal },
  );
  // a chat in WSL names its install as the Connections page does, which needs the other installs (the distro only when the
  // CLI is in another distro too): the Connections list, read only for such a chat, and again when a sign-in or a gate changes
  // one. A core without it names the install without the distro.
  const installs = query(handle, {
    enabled: () => !!feed.chat?.wslDistro,
    when: (e) => e.type.startsWith('connection.') && e.type !== 'connection.sign_in_started',
    load: async (): Promise<Connection[]> => {
      try {
        return await s.client.connectionList();
      } catch {
        return [];
      }
    },
  });
  // the tool calls the chat's CLI waits to be allowed (chats/permission.go)
  const asks = query(handle, {
    key: () => handle.props.id,
    when: (e) => e.type.startsWith('chat.permission') && isRecord(e.data) && e.data.chatId === handle.props.id,
    load: (): Promise<ChatAsk[]> => s.client.chatAsks(handle.props.id),
  });
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : navigate(href));

  // the requests answered here: their cards go at once, so a second click cannot answer one again while the list is read
  // again (audit 2026-10-27: Deny after Allow once was sent, refused, and left an error note)
  const answered = new Set<string>();
  const answer = async (a: ChatAsk, allow: boolean) => {
    askErrFor = a.id;
    if (await askAct.run(`${allow ? 'allow' : 'deny'}:${a.id}`, () => s.client.chatAnswer(handle.props.id, a.id, allow).then(() => true))) answered.add(a.id);
    void asks.reload();
  };
  // "Allow <tool> in this chat": the call runs and its rule is recorded for the chat; refused (with the core's reason) the
  // request keeps waiting, to be allowed once or denied
  const remember = async (a: ChatAsk) => {
    askErrFor = a.id;
    if (await askAct.run(`remember:${a.id}`, () => s.client.chatAllowAndRemember(handle.props.id, a.id))) answered.add(a.id);
    void asks.reload();
  };

  // the page is added above; the list keeps the row at the top of the view where it was
  const loadOlder = async () => {
    if (!feed.hasMore || feed.loadingOlder) return;
    await feed.loadOlder();
  };

  const update = async (u: Omit<Parameters<typeof s.client.chatUpdate>[0], 'id'>, label = 'update') => {
    const c = await act.run(label, () => s.client.chatUpdate({ id: handle.props.id, ...u }));
    if (c) feed.setChat(c);
    return c;
  };

  const send = async (text: string, attach: string[] = [], files: ComposerFile[] = []): Promise<boolean> => {
    const r = await act.run('send', () => s.client.chatSend(handle.props.id, text, attach, files));
    if (!r) return false;
    list.toBottom(); // before sent(): it may not emit (a turn that already ended), so this renders as well
    feed.sent(r.turnId, r.version);
    return true;
  };

  // P89: the person's message at seq, changed (or unchanged: regenerate), and everything after it, replaced by a new turn
  const resend = async (seq: number, text?: string): Promise<boolean> => {
    const r = await act.run('resend', () => s.client.chatResend(handle.props.id, seq, text));
    if (!r) return false;
    list.toBottom();
    feed.sent(r.turnId, r.version);
    return true;
  };

  const fork = async (uptoSeq?: number) => {
    closeMore();
    const c = await act.run('fork', () => s.client.chatFork(handle.props.id, uptoSeq));
    if (c) await go(routes.chat.href({ id: c.id }));
  };

  const allow = async (rule: string): Promise<boolean> => {
    const c = await act.run(`allow:${rule}`, () => s.client.chatAllowTool(handle.props.id, rule));
    if (c) feed.setChat(c);
    return !!c;
  };

  const disallow = async (rule: string) => {
    const c = await act.run(`disallow:${rule}`, () => s.client.chatDisallowTool(handle.props.id, rule));
    if (c) feed.setChat(c);
  };

  const saveTitle = async () => {
    if (!editing) return;
    editing = false;
    const next = draftTitle.trim();
    if (feed.chat && next !== feed.chat.title) await update({ title: next }, 'title');
    else void handle.update();
  };

  return () => {
    const { adapters } = handle.props;
    const chat = feed.chat;
    if (!chat) {
      return (
        <section mix={screenCss} aria-label="Chat">
          {feed.loading ? (
            <Loading label="Loading the chat..." />
          ) : (
            <Empty title="Chat not found" icon={Bot}>
              <p>{feed.deleted ? 'This chat was deleted.' : 'It may have been deleted.'}</p>
              <ErrorNote error={feed.error} />
            </Empty>
          )}
        </section>
      );
    }
    const msgs = feed.messages;
    const items = groups(msgs);
    const running = feed.running;
    const adapter = adapters.find((a) => a.id === chat.adapter);
    const readOnly = s.workspace.license.readOnly;
    const cli = chatCliText(chat, installs.data ?? []);
    const projectName = s.workspace.projectList.find((p) => p.id === chat.projectId)?.name;
    const state = chatState(chat, running);
    const warning = cliWarning(chat, adapter);
    const waiting = (asks.data ?? []).filter((a) => !answered.has(a.id));
    const askOf = new Map(waiting.map((a) => [a.messageId, a] as const));
    const askCard = (a: ChatAsk) => (
      <AskCard
        ask={a}
        cli={adapterLabel(chat.adapter)}
        busy={askAct.busy}
        readOnly={readOnly}
        error={askErrFor === a.id ? askAct.error : undefined}
        onAnswer={(ok) => void answer(a, ok)}
        onRemember={() => void remember(a)}
      />
    );
    let lastUserSeq = 0;
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i]!.role === 'user') {
        lastUserSeq = msgs[i]!.seq;
        break;
      }
    }
    const stick = list.stick;
    if (stick) seenSeq = msgs.at(-1)?.seq ?? 0;
    // the messages are in seq order: count back from the newest (nothing is unseen while stuck to the bottom)
    let unseen = 0;
    if (!stick) for (let i = msgs.length - 1; i >= 0 && msgs[i]!.seq > seenSeq; i--) unseen++;
    const locked = chat.archived || readOnly;
    const row = (m: ChatMessage, i: number, extra: { toolOpen?: boolean } = {}) => {
      const a = askOf.get(m.id);
      return (
        <ChatMessageRow
          key={m.id}
          m={m}
          head={startsGroup(msgs[i - 1], m)}
          adapter={chat.adapter}
          allowedTools={chat.allowedTools}
          latest={m.seq > lastUserSeq}
          running={running}
          busy={act.busy}
          ask={a ? askCard(a) : undefined}
          thoughtMs={m.thinking ? thinkingMs(msgs[i - 1], m) : 0}
          toolOpen={extra.toolOpen}
          onAllow={chat.adapter === 'claude' && !readOnly ? (r) => void allow(r) : undefined}
          onRetry={locked ? undefined : () => void send(RETRY_TEXT)}
          onAllowAndRetry={chat.adapter === 'claude' && !locked ? (r) => void allow(r).then((ok) => (ok ? send(RETRY_TEXT) : false)) : undefined}
          onFork={(seq) => void fork(seq)}
          onEdit={locked ? undefined : (seq, text) => resend(seq, text)}
          onRegenerate={locked || i !== msgs.length - 1 || !lastUserSeq ? undefined : () => void resend(lastUserSeq)}
        />
      );
    };
    const draw = (it: FeedItem) => {
      if (it.kind === 'msg') return row(it.m, it.index);
      if (it.kind === 'changes') return <ChangesCard changes={it.changes} />;
      const first = it.tools[0]!;
      const st = burstState(it.tools, running && first.seq > lastUserSeq);
      const expanded = opened.get(it.key) ?? (verbose || st === 'failed');
      const line = burstLine(it.tools, { any: it.tools.some((m) => !!m.thinking), ms: first.thinking ? thinkingMs(msgs[it.index - 1], first) : 0 });
      return (
        <BurstLine
          tools={it.tools}
          line={line}
          state={st}
          expanded={expanded}
          onToggle={() => {
            opened.set(it.key, !expanded);
            void handle.update();
          }}
        >
          {expanded ? it.tools.map((m, j) => row(m, it.index + j, { toolOpen: m.toolStatus === 'failed' })) : null}
        </BurstLine>
      );
    };
    // before reading list.quiet: rows revealed by this render (not new ones) keep the live region silent
    const shown = list.rows(items, (it) => it.key, draw);
    handle.queueTask(() => list.settle());
    const streaming = feed.streaming;
    const turnNote = feed.lastTurn && feed.lastTurn.fallback ? 'This turn started a new session with the earlier conversation quoted, because the previous session could not be continued.' : '';
    // a request whose note is not among the messages loaded (an older page) waits above the composer
    const loose = waiting.filter((a) => !msgs.some((m) => m.id === a.messageId));
    const n = chat.allowedTools.length;
    const hasBursts = items.some((it) => it.kind === 'burst');
    // the replies the final answer invites (P139): only after the answer to the person's latest message, while nothing runs
    const last = msgs.at(-1);
    const answer_ = last && last.role === 'assistant' && last.seq > lastUserSeq && lastUserSeq > 0 ? last : undefined;
    if ((answer_?.id ?? '') !== repliesFor) {
      repliesFor = answer_?.id ?? '';
      replies = answer_ ? suggestReplies(answer_.body) : [];
    }
    const showReplies = !!answer_ && !running && !streaming && !locked && replies.length > 0;

    // the composer's row: the agent CLI (its Properties), the model and effort by name (the chat settings) and the approval
    // mode. A chat that chose none shows what its CLI reported, and the effort Claude Code uses for that model (decision P107)
    const modelText = chatModelText(chat);
    const effort = chatEffort(chat);
    const controls = (
      <>
        <button
          type="button"
          mix={[smallBtn, on('click', openProps)]}
          disabled={locked}
          aria-label={`Agent CLI: ${cli}`}
          title={chatWhere(chat) || 'The agent CLI: change it in Properties'}
          data-testid="chat-cli-chip"
        >
          <span data-testid="chip-adapter" title={chatWhere(chat) || undefined}>
            {cli}
          </span>
          <Icon icon={ChevronDown} size={12} />
        </button>
        <Popover
          open={settingsOpen}
          onOpen={() => ((settingsOpen = true), (modeOpen = false), void handle.update())}
          onClose={() => ((settingsOpen = false), void handle.update())}
          label="Chat settings"
          placement="top-start"
          testId="chat-settings"
          trigger={(mix) => (
            <button
              type="button"
              mix={[smallBtn, mix]}
              disabled={locked}
              aria-label={`Model ${modelText}${effort.text ? `, ${effort.text} effort` : ''}: chat settings`}
              aria-expanded={settingsOpen ? 'true' : 'false'}
              title={chat.model ? 'Change the model, effort, permission mode or history access' : `${modelText === 'default model' ? 'The CLI chooses the model' : `${modelText} is the CLI's default`}. Change the model, effort, permission mode or history access`}
              data-testid="chat-settings-open"
            >
              <span data-testid="chip-model" data-default={chat.model ? undefined : 'true'}>
                {modelText}
              </span>
              {effort.text ? (
                <span data-testid="chip-effort" data-default={effort.chosen ? undefined : 'true'} style={{ color: 'var(--text-faint)' }}>
                  · {effort.text}
                </span>
              ) : null}
              <Icon icon={ChevronDown} size={12} />
            </button>
          )}
        >
          <ChatSettings chat={chat} adapter={adapter} busy={act.busy} error={act.error} onSave={async (u) => (await update(u, 'settings')) && ((settingsOpen = false), void handle.update())} onCancel={() => ((settingsOpen = false), void handle.update())} />
        </Popover>
        <Popover
          open={modeOpen}
          onOpen={() => ((modeOpen = true), (settingsOpen = false), void handle.update())}
          onClose={() => ((modeOpen = false), void handle.update())}
          label="Approval mode"
          placement="top-start"
          testId="chat-mode-settings"
          trigger={(mix) => (
            <button
              type="button"
              mix={[smallBtn, mix]}
              disabled={locked}
              aria-expanded={modeOpen ? 'true' : 'false'}
              data-warn={chat.skipsApprovals ? 'true' : undefined}
              data-testid="chip-mode"
              title={chat.skipsApprovals ? 'This mode skips approvals: the agent runs tools without asking' : `Permission mode ${chat.permissionMode || 'default'}`}
            >
              {chat.skipsApprovals ? <Icon icon={TriangleAlert} size={12} /> : null}
              <span>{modeWords(chat.permissionMode)}</span>
              <Icon icon={ChevronDown} size={12} />
            </button>
          )}
        >
          <ModeSettings chat={chat} adapter={adapter} busy={act.busy} error={act.error} onSave={async (mode) => (await update({ permissionMode: mode }, 'mode')) && ((modeOpen = false), void handle.update())} onCancel={() => ((modeOpen = false), void handle.update())} />
        </Popover>
      </>
    );

    // the ⋯ panel's two views: what the chat is and what can be done with it, or its allowed tools
    const moreMain = (
      <div mix={moreCss}>
        <div class="facts">
          <div class="cli" data-state={state.state} title={state.state === 'error' ? chat.lastError || undefined : chat.sessionId ? `Session ${chat.sessionId}` : undefined}>
            <span class="dot" aria-hidden="true" />
            <span data-testid="chat-cli">{cli}</span>
            <span class="state">· {state.words}</span>
          </div>
          <div data-testid="chat-place">{chatPlaceWords(chat, projectName)}</div>
          <div class="path" title={chat.cwd}>
            {folderText(chat, 80)}
          </div>
          {propsChips(chat).length ? (
            <button type="button" class="chips" mix={on('click', openProps)} aria-label={`Skills and MCP servers: ${propsChips(chat).join(', ')}`} title="Change the skills and MCP servers" data-testid="chat-props-chips">
              {(chat.skills?.length ?? 0) > 0 ? (
                <span class="chip" data-testid="chip-skills" title={chat.skills!.map((k) => k.name).join(', ')}>
                  {propsChips({ skills: chat.skills })[0]}
                </span>
              ) : null}
              {(chat.mcpServers?.length ?? 0) > 0 ? (
                <span class="chip" data-testid="chip-mcp" title={chat.mcpServers!.map((g) => g.serverId).join(', ')}>
                  {propsChips({ mcpServers: chat.mcpServers })[0]}
                </span>
              ) : null}
            </button>
          ) : null}
          {chat.forkedFrom ? (
            <a href={routes.chat.href({ id: chat.forkedFrom })} style={{ fontSize: 'var(--text-xs)' }}>
              Forked from an earlier chat
            </a>
          ) : null}
        </div>
        <button type="button" class="act" aria-label={`Allowed tools${n ? ` (${n})` : ''}`} title="The tools this chat may use without asking" mix={on('click', () => ((moreView = 'tools'), void handle.update()))}>
          <Icon icon={ShieldCheck} size={15} />
          <span>Allowed tools</span>
          {n ? (
            <span class="n" aria-hidden="true">
              {n}
            </span>
          ) : null}
        </button>
        <button type="button" class="act" title="Agent CLI, folder, skills, MCP servers and usage" data-testid="chat-properties-open" mix={on('click', openProps)}>
          <Icon icon={SlidersHorizontal} size={15} />
          <span>Properties</span>
        </button>
        <button type="button" class="act" title="Fork: a new chat with this chat's messages, its own session" disabled={!!act.busy} mix={on('click', () => void fork())}>
          <Icon icon={GitFork} size={15} />
          <span>{act.busy === 'fork' ? 'Forking...' : 'Fork'}</span>
        </button>
        {!locked ? (
          <button
            type="button"
            class="act"
            disabled={running}
            title={running ? 'A chat moves between turns: wait for the answer, or stop it' : 'Run this chat in a project'}
            data-testid="chat-move"
            mix={on('click', () => (closeMore(), (moveOpen = true), void handle.update()))}
          >
            <Icon icon={FolderInput} size={15} />
            <span>{chat.projectId ? 'Move to another project' : 'Move to a project'}</span>
          </button>
        ) : null}
      </div>
    );
    const moreTools = (
      <div mix={moreCss} data-testid="chat-allowed-tools">
        <div mix={toolsHead}>
          <IconButton icon={ArrowLeft} size={14} label="Back" mix={on('click', () => ((moreView = 'main'), void handle.update()))} />
          <h2>Allowed tools</h2>
        </div>
        <p mix={hintCss}>{chat.adapter === 'claude' ? 'Rules this chat passes to Claude Code as --allowedTools, from the next message.' : 'Allow rules work only in Claude Code chats.'}</p>
        {chat.allowedTools.length ? (
          <ul mix={ruleList} aria-label="Allowed tool rules">
            {chat.allowedTools.map((r) => (
              <li key={r}>
                <code>{r}</code>
                <Button size="sm" variant="ghost" disabled={!!act.busy} aria-label={`Remove ${r}`} mix={[on('click', () => void disallow(r))]}>
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p mix={hintCss}>None yet.</p>
        )}
        {chat.adapter === 'claude' ? (
          <form
            style={{ display: 'flex', gap: '6px' }}
            mix={on('submit', (e) => {
              e.preventDefault();
              const r = newRule.trim();
              if (!r) return;
              void allow(r).then(() => {
                if (!act.error) newRule = '';
                void handle.update();
              });
            })}
          >
            <input aria-label="Rule to allow" placeholder="Bash(npm test)" value={newRule} spellcheck={false} style={{ flex: '1', fontFamily: 'var(--font-mono)' }} mix={on('input', (e) => ((newRule = e.currentTarget.value), void handle.update()))} />
            <Button type="submit" size="sm" disabled={!!act.busy}>
              Allow
            </Button>
          </form>
        ) : null}
        <ErrorNote error={act.error} />
      </div>
    );

    return (
      <section mix={screenCss} aria-label={`Chat ${chatTitle(chat)}`} data-testid="chat-view" data-status={chat.status} data-verbose={verbose ? 'true' : undefined}>
        <header mix={[headerCss, barCss]}>
          {handle.props.onShowList ? <IconButton icon={PanelLeftOpen} label="Show the chat list" mix={on('click', () => handle.props.onShowList!())} /> : null}
          <h1 style={{ minWidth: '0', marginLeft: '-6px' }}>
            {editing ? (
              <input
                aria-label="Chat title"
                value={draftTitle}
                maxLength={200}
                mix={[
                  titleInput,
                  ref((n) => queueMicrotask(() => (n as HTMLInputElement).select())),
                  on('input', (e) => ((draftTitle = e.currentTarget.value), void handle.update())),
                  on('keydown', (e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      void saveTitle();
                    } else if (e.key === 'Escape') {
                      e.preventDefault();
                      editing = false;
                      void handle.update();
                    }
                  }),
                  on('blur', () => void saveTitle()),
                ]}
              />
            ) : (
              <button
                type="button"
                mix={[
                  titleBtn,
                  on('click', () => {
                    editing = true;
                    draftTitle = chat.title;
                    void handle.update();
                  }),
                ]}
                aria-label={`Rename chat ${chatTitle(chat)}`}
                title="Rename"
                data-testid="chat-title"
              >
                <span class="txt">{chatTitle(chat)}</span>
                <Icon icon={Pencil} size={13} />
              </button>
            )}
          </h1>
          <span mix={headChip} data-testid="chat-folder" title={chat.cwd} data-project={projectName ? 'true' : undefined}>
            <Icon icon={Folder} size={12} />
            <span class="txt">{folderChipText(chat, projectName)}</span>
          </span>
          {warning ? (
            <button type="button" mix={[headChip, warnChip, on('click', openProps)]} title={chat.status === 'error' && chat.lastError ? chat.lastError : 'Open Properties to pick another agent CLI'} data-testid="chat-cli-warning">
              <Icon icon={TriangleAlert} size={12} />
              <span class="txt">{warning}</span>
            </button>
          ) : null}
          <span class="sr-only" data-testid="chat-status">
            {state.words}
          </span>
          <div mix={actionsCss}>
            <Popover
              open={moreOpen}
              onOpen={() => ((moreOpen = true), (moreView = 'main'), void handle.update())}
              onClose={() => (closeMore(), void handle.update())}
              label="Chat options"
              placement="bottom-end"
              testId="chat-more-panel"
              trigger={(mix) => (
                <button type="button" mix={[moreBtn, mix]} aria-label="Chat options" title="The CLI, where the chat runs, allowed tools, properties, fork, move" aria-expanded={moreOpen ? 'true' : 'false'} data-testid="chat-more">
                  <Icon icon={Ellipsis} size={16} />
                </button>
              )}
            >
              {moreView === 'tools' ? moreTools : moreMain}
            </Popover>
          </div>
        </header>
        {feed.error ? (
          <div mix={noteCss}>
            <ErrorNote error={feed.error} what="Could not load the chat" />
          </div>
        ) : null}
        <div mix={logWrap}>
          <div
            tabIndex={0}
            aria-label="Conversation"
            data-testid="chat-scroll"
            mix={[
              scrollCss,
              ref(list.scroller),
              on('scroll', (e) => {
                const el = e.currentTarget as HTMLDivElement;
                list.onScroll();
                if (el.scrollTop < 80 && feed.hasMore && !feed.loadingOlder) void loadOlder();
              }),
            ]}
          >
            <div mix={columnCss}>
              {feed.hasMore ? (
                <div mix={olderCss}>
                  <Button size="sm" variant="ghost" disabled={feed.loadingOlder} data-testid="chat-older" mix={[on('click', () => void loadOlder())]}>
                    {feed.loadingOlder ? 'Loading older messages...' : 'Load older messages'}
                  </Button>
                </div>
              ) : null}
              <div role="log" aria-live={list.quiet ? 'off' : 'polite'} aria-relevant="additions" aria-label={`Messages in ${chatTitle(chat)}`} aria-busy={feed.loadingOlder ? 'true' : undefined} data-testid="chat-log">
                {shown}
              </div>
              {streaming ? <ChatStream feed={feed} who={adapterLabel(chat.adapter)} onGrow={() => list.follow()} /> : null}
              {showReplies ? <ReplyChips replies={replies} disabled={!!act.busy} onReply={(text) => void send(text)} /> : null}
              {turnNote ? <Notice tone="info">{turnNote}</Notice> : null}
            </div>
          </div>
          {/* away from the bottom the new rows are not rendered, so the log cannot announce them: the count is said instead */}
          <span class="sr-only" role="status" data-testid="chat-unseen">
            {!stick && unseen > 0 ? `${unseen} new ${unseen === 1 ? 'message' : 'messages'} below` : ''}
          </span>
          {!stick ? (
            <button type="button" data-testid="chat-jump" mix={[jumpCss, on('click', () => list.toBottom())]}>
              <Icon icon={ArrowDown} size={14} />
              {unseen > 0 ? `${unseen} new ${unseen === 1 ? 'message' : 'messages'}` : 'Jump to the latest'}
            </button>
          ) : null}
        </div>
        {chat.archived ? (
          <div mix={noteCss}>
            <Notice tone="muted" title="This chat is archived.">
              <Button size="sm" variant="link" mix={[on('click', () => void update({ archived: false }, 'unarchive'))]}>
                Unarchive
              </Button>
            </Notice>
          </div>
        ) : null}
        <ChatAsks
          asks={loose}
          cli={adapterLabel(chat.adapter)}
          busy={askAct.busy}
          readOnly={readOnly}
          error={askAct.error}
          errorFor={askErrFor}
          onAnswer={(a, ok) => void answer(a, ok)}
          onRemember={(a) => void remember(a)}
        />
        {askAct.error && !waiting.some((a) => a.id === askErrFor) ? (
          <div mix={noteCss}>
            <ErrorNote error={askAct.error} />
          </div>
        ) : null}
        <ChatApprovals chat={chat} />
        {!settingsOpen && !modeOpen && !moreOpen && !moveOpen && act.error ? (
          <div mix={noteCss}>
            <ErrorNote error={act.error} />
          </div>
        ) : null}
        <ChatComposer
          chat={chat}
          running={running}
          disabled={locked}
          disabledReason={readOnly ? s.workspace.license.reason : chat.archived ? 'This chat is archived.' : ''}
          sending={act.busy === 'send'}
          empty={msgs.length === 0}
          controls={controls}
          hint={hasBursts ? (verbose ? 'Ctrl O hides the tool calls' : 'Ctrl O shows every tool call') : ''}
          onSend={send}
          onStop={() => void act.run('stop', () => s.client.chatStop(handle.props.id))}
          stopping={act.busy === 'stop'}
        />
        <MoveChatDialog
          open={moveOpen}
          chat={chat}
          projects={s.workspace.projectList}
          running={running}
          onClose={() => ((moveOpen = false), void handle.update())}
          onMoved={(c) => {
            feed.setChat(c);
            moveOpen = false;
            void handle.update();
          }}
        />
        <ChatPropertiesDialog
          open={propsOpen}
          chat={chat}
          adapters={adapters}
          onClose={() => ((propsOpen = false), void handle.update())}
          onSaved={(c) => {
            feed.setChat(c);
            propsOpen = false;
            void handle.update();
          }}
        />
      </section>
    );
  };
}

/**
 * The answer streaming in. It redraws itself on the feed's `stream` events, at most once a frame, so text arriving costs this
 * answer's render and nothing else: the conversation above it, the header and the composer are not drawn again (the review of
 * 2026-10-27: with a long history open, every delta redrew and measured every row in the window).
 */
function ChatStream(handle: Handle<{ feed: ChatFeed; who: string; onGrow: () => void }>) {
  let frame = 0;
  const redraw = () => {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      void handle.update();
    });
  };
  const feed = handle.props.feed;
  feed.addEventListener('stream', redraw);
  handle.signal.addEventListener('abort', () => {
    feed.removeEventListener('stream', redraw);
    if (frame) cancelAnimationFrame(frame);
  });
  return () => {
    const streaming = handle.props.feed.streaming;
    // the answer grew: the list stays at the bottom while the reader is there
    handle.queueTask(() => handle.props.onGrow());
    if (!streaming) return null;
    return (
      <div mix={streamCss} aria-live="off" data-testid="chat-streaming" aria-label={`${handle.props.who} is answering`}>
        {streaming.thinking ? <ThinkingFold text={streaming.thinking} live /> : null}
        {streaming.text ? (
          <Markdown source={streaming.text} />
        ) : (
          <span class="typing" role="status" aria-label="Working">
            <i />
            <i />
            <i />
          </span>
        )}
      </div>
    );
  };
}

/** The permission modes the chat's adapter takes (its own first), as select options. */
const modeOptions = (adapter: AdapterInfo | undefined, mode: string) => {
  const modes = adapter?.capabilities?.permissionModes?.length ? adapter.capabilities.permissionModes : [mode || 'default'];
  return selectValues([...new Set([...modes, ...(mode ? [mode] : [])])]);
};

/** The settings popover: model, effort, permission mode (with the skip-approvals confirmation) and history access. */
function ChatSettings(
  handle: Handle<{ chat: Chat; adapter: AdapterInfo | undefined; busy: string; error: Error | undefined; onSave: (u: { model: string; effort: string; permissionMode: string; readHistory: boolean }) => void; onCancel: () => void }>,
) {
  const c = handle.props.chat;
  let model = c.model;
  let effort = c.effort;
  let mode = c.permissionMode;
  // a mode the chat already has was confirmed when it was chosen; another one that skips approvals asks again
  let confirmed = skipsApprovals(c.permissionMode) ? c.permissionMode : '';
  let readHistory = c.readHistory;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  return () => {
    const { adapter, busy, error, onSave, onCancel } = handle.props;
    const caps = adapter?.capabilities;
    const models = caps?.models ?? [];
    const efforts = caps?.efforts ?? [];
    // the "none chosen" choices say what that means: the CLI's default model by name (as it last reported it) and its effort
    const reported = (c.cliModel ?? '').trim();
    const defaultModelLabel = reported && !c.model ? `CLI default (${modelName(reported)})` : 'CLI default';
    const defaultEffortLabel = (m: string) => {
      const e = chatEffort({ adapter: c.adapter, model: m, effort: '', cliModel: c.model ? '' : reported }).text;
      return e ? `default (${e})` : 'CLI default';
    };
    return (
      <form
        style={{ display: 'grid', gap: '12px' }}
        mix={on('submit', (e) => {
          e.preventDefault();
          if (needsConfirmation(mode, confirmed)) return;
          onSave({ model: model.trim(), effort, permissionMode: mode, readHistory });
        })}
      >
        <h2>Chat settings</h2>
        <Field label="Model" hint="Applies from the next message.">
          {(id) => <ModelField id={id} value={model} models={models} emptyLabel={defaultModelLabel} placeholder={defaultModelLabel} onChange={(v) => set(() => (model = v))} />}
        </Field>
        <Field label="Effort">
          {(id) => (
            <Select
              id={id}
              value={effort}
              disabled={efforts.length === 0 && !effort}
              options={[{ value: '', label: defaultEffortLabel(model) }, ...selectValues([...new Set([...efforts, ...(effort ? [effort] : [])])])]}
              onChange={(v) => set(() => (effort = v))}
            />
          )}
        </Field>
        <Field label="Permission mode">{(id) => <Select id={id} value={mode} options={modeOptions(adapter, mode)} onChange={(v) => set(() => (mode = v))} />}</Field>
        <SkipApprovals mode={mode} confirmed={confirmed} who="this chat's agent" onConfirm={(m) => set(() => (confirmed = m))} />
        <Checkbox label="Let this chat read my other chats" description="A read-only tool to list, search and read your earlier chats." checked={readHistory} onChange={(v) => set(() => (readHistory = v))} />
        <ErrorNote error={error} />
        <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
          <Button size="sm" variant="ghost" mix={[on('click', () => onCancel())]}>
            Cancel
          </Button>
          <Button size="sm" variant="primary" type="submit" disabled={!!busy || needsConfirmation(mode, confirmed)}>
            {busy === 'settings' ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </form>
    );
  };
}

/** The approval mode popover: the permission mode alone, in words, with the skip-approvals confirmation. */
function ModeSettings(handle: Handle<{ chat: Chat; adapter: AdapterInfo | undefined; busy: string; error: Error | undefined; onSave: (mode: string) => void; onCancel: () => void }>) {
  const c = handle.props.chat;
  let mode = c.permissionMode;
  let confirmed = skipsApprovals(c.permissionMode) ? c.permissionMode : '';
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  return () => {
    const { adapter, busy, error, onSave, onCancel } = handle.props;
    return (
      <form
        style={{ display: 'grid', gap: '12px' }}
        mix={on('submit', (e) => {
          e.preventDefault();
          if (needsConfirmation(mode, confirmed)) return;
          onSave(mode);
        })}
      >
        <h2>Approval mode</h2>
        <Field label="Permission mode" hint={`${modeWords(mode)}. Applies from the next message.`}>
          {(id) => <Select id={id} value={mode} options={modeOptions(adapter, mode)} onChange={(v) => set(() => (mode = v))} />}
        </Field>
        <SkipApprovals mode={mode} confirmed={confirmed} who="this chat's agent" onConfirm={(m) => set(() => (confirmed = m))} />
        <ErrorNote error={error} />
        <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
          <Button size="sm" variant="ghost" mix={[on('click', () => onCancel())]}>
            Cancel
          </Button>
          <Button size="sm" variant="primary" type="submit" disabled={!!busy || needsConfirmation(mode, confirmed)}>
            {busy === 'mode' ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </form>
    );
  };
}

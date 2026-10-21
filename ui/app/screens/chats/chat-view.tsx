// The conversation of one chat: a header (inline title, adapter/model/effort/mode chips with a settings popover, the
// folder, the session status, tokens and cost, Fork, the allowed tools), the message log (older pages on scroll-up, the
// streamed assistant text in its own bubble outside the live region) and the composer (Ctrl+Enter, attached chats, Stop).

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowDown, Bot, FolderOpen, GitFork, PanelLeftOpen, Pencil, ShieldCheck, SlidersHorizontal, TriangleAlert } from 'lucide';
import type { Chat } from '../../api/chatClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { headerCss, actionsEnd } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { Popover } from '../../ui/popover.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ModelField } from '../shared/model-field.tsx';
import { VirtualList } from '../../ui/virtual-list.tsx';
import { ChatComposer } from './chat-composer.tsx';
import { ChatFeed } from './chat-feed.ts';
import { ChatMessageRow } from './chat-message.tsx';
import { ChatAsks } from './chat-asks.tsx';
import type { ComposerFile } from './chat-composer.tsx';
import { ThinkingFold } from './chat-thinking.tsx';
import { ChatApprovals, ChatPropertiesDialog } from './chat-properties.tsx';
import { adapterLabel, chatTitle, chatWhere, compactNumber, costText, propsChips, RETRY_TEXT, shortPath, skipsApprovals, startsGroup } from './chats.ts';

const screenCss = css({ display: 'flex', flexDirection: 'column', height: '100%', minHeight: '0', minWidth: '0', flex: '1', background: t.bgApp });
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
  fontSize: t.textLg,
  fontWeight: '600',
  '& .txt': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& svg': { flex: 'none', color: t.textFaint, opacity: '0' },
  '&:hover': { background: t.bgHover },
  '&:hover svg, &:focus-visible svg': { opacity: '1' },
});
const titleInput = css({ height: '28px', minWidth: '240px', fontSize: t.textLg, fontWeight: '600' });
const actionsCss = actionsEnd;

// the second header line: chips, folder, status, usage
const subCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `4px ${t.s3}`,
  padding: `6px ${t.s4} 6px ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  color: t.textMuted,
  fontSize: t.textSm,
  flex: 'none',
});
const chipsBtn = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  minHeight: '0',
  height: '24px',
  padding: '0 4px',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  '&:hover': { background: t.bgHover },
  '&[aria-expanded="true"]': { background: t.bgActive },
});
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  height: '20px',
  padding: '0 6px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rSm,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '&[data-warn="true"]': { borderColor: `color-mix(in srgb, ${t.warning} 50%, ${t.border})`, color: t.warning, background: t.warningSoft },
});
const cwdCss = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', minWidth: '0', maxWidth: '360px', fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted, '& span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } });
const statusCss = css({ display: 'inline-flex', alignItems: 'center', gap: '5px', whiteSpace: 'nowrap' });
const dotCss = css({ width: '7px', height: '7px', borderRadius: '50%', flex: 'none' });
const pulse = css({ animation: 'dd-chat-pulse 1.2s ease-in-out infinite', '@keyframes dd-chat-pulse': { '50%': { opacity: '0.35' } } });
const usageCss = css({ marginLeft: 'auto', whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums', color: t.textFaint });

const logWrap = css({ position: 'relative', flex: '1', minHeight: '0', display: 'flex', flexDirection: 'column' });
const scrollCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', overflowX: 'hidden', padding: `${t.s3} 0 ${t.s5}`, outline: 'none', '&:focus-visible': { boxShadow: `inset ${t.focus}` } });
const olderCss = css({ display: 'flex', justifyContent: 'center', padding: `${t.s2} 0` });
const introCss = css({ padding: `${t.s5} ${t.s6} ${t.s2} ${t.s5}`, color: t.textMuted, fontSize: t.textMd, '& strong': { color: t.text } });
const streamCss = css({
  display: 'grid',
  gridTemplateColumns: '32px minmax(0, 1fr)',
  columnGap: '12px',
  padding: `1px ${t.s6} 1px ${t.s5}`,
  marginTop: '6px',
  '& .md': { color: t.text },
  '& .typing': { display: 'inline-flex', gap: '3px', alignItems: 'center', height: '20px' },
  '& .typing i': { width: '5px', height: '5px', borderRadius: '50%', background: t.textFaint, animation: 'dd-chat-pulse 1.2s ease-in-out infinite' },
  '& .typing i:nth-child(2)': { animationDelay: '0.2s' },
  '& .typing i:nth-child(3)': { animationDelay: '0.4s' },
});
const noteCss = css({ padding: `${t.s2} ${t.s5} 0` });
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

/** The conversation of one chat. Keyed by chat id: a new chat is a new feed. */
export function ChatView(handle: Handle<{ id: string; adapters: AdapterInfo[]; onNavigate?: (href: string) => void; onShowList?: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const feed = new ChatFeed(s.client, s.store, handle.props.id).start(handle.signal);
  const act = action(handle);
  // the rows near the view are rendered; the list stays at the bottom while the reader is there (ui/virtual-list.tsx)
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal, estimate: 40 });
  /** The newest message seen while at the bottom: only messages after it are "new" (older pages above are not). */
  let seenSeq = 0;
  let editing = false;
  let draftTitle = '';
  let settingsOpen = false;
  let toolsOpen = false;
  let propsOpen = false; // the properties dialog (decision P60): CLI, folder, skills, MCP servers
  let newRule = '';
  const openProps = () => ((propsOpen = true), void handle.update());

  feed.addEventListener('change', () => void handle.update());
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : navigate(href));

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
    const c = await act.run('fork', () => s.client.chatFork(handle.props.id, uptoSeq));
    if (c) await go(routes.chat.href({ id: c.id }));
  };

  const allow = async (rule: string) => {
    const c = await act.run(`allow:${rule}`, () => s.client.chatAllowTool(handle.props.id, rule));
    if (c) feed.setChat(c);
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
    const running = feed.running;
    const adapter = adapters.find((a) => a.id === chat.adapter);
    const readOnly = s.workspace.license.readOnly;
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
    // before reading list.quiet: rows revealed by this render (not new ones) keep the live region silent
    const shown = list.rows(
      msgs,
      (m) => m.id,
      (m, i) => (
        <ChatMessageRow
          m={m}
          head={startsGroup(msgs[i - 1], m)}
          adapter={chat.adapter}
          allowedTools={chat.allowedTools}
          latest={m.seq > lastUserSeq}
          running={running}
          busy={act.busy}
          onAllow={chat.adapter === 'claude' && !readOnly ? (r) => void allow(r) : undefined}
          onRetry={chat.archived || readOnly ? undefined : () => void send(RETRY_TEXT)}
          onFork={(seq) => void fork(seq)}
          onEdit={chat.archived || readOnly ? undefined : (seq, text) => resend(seq, text)}
          onRegenerate={chat.archived || readOnly || i !== msgs.length - 1 || !lastUserSeq ? undefined : () => void resend(lastUserSeq)}
        />
      ),
    );
    handle.queueTask(() => list.settle());
    const streaming = feed.streaming;
    const turnNote = feed.lastTurn && feed.lastTurn.fallback ? 'This turn started a new session with the earlier conversation quoted, because the previous session could not be continued.' : '';

    return (
      <section mix={screenCss} aria-label={`Chat ${chatTitle(chat)}`} data-testid="chat-view" data-status={chat.status}>
        <header mix={headerCss}>
          {handle.props.onShowList ? (
            <IconButton icon={PanelLeftOpen} label="Show the chat list" mix={on('click', () => handle.props.onShowList!())} />
          ) : null}
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
          <div mix={actionsCss}>
            <Popover
              open={toolsOpen}
              onOpen={() => ((toolsOpen = true), void handle.update())}
              onClose={() => ((toolsOpen = false), void handle.update())}
              label="Allowed tools"
              placement="bottom-end"
              testId="chat-allowed-tools"
              trigger={(mix) => (
                <Button size="sm" variant="ghost" icon={ShieldCheck} aria-expanded={toolsOpen ? 'true' : 'false'} mix={[mix]}>
                  Allowed tools{chat.allowedTools.length ? ` (${chat.allowedTools.length})` : ''}
                </Button>
              )}
            >
              <h2>Allowed tools</h2>
              <p mix={hintCss}>
                {chat.adapter === 'claude'
                  ? 'Rules this chat passes to Claude Code as --allowedTools. They apply from the next message.'
                  : 'Allow rules work only in Claude Code chats.'}
              </p>
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
                <p mix={hintCss}>None yet. A refused tool offers its rule here with "Allow for this chat".</p>
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
              {toolsOpen ? <ErrorNote error={act.error} /> : null}
            </Popover>
            <Button size="sm" variant="ghost" icon={SlidersHorizontal} data-testid="chat-properties-open" mix={[on('click', openProps)]} title="Agent CLI, folder, skills and MCP servers">
              Properties
            </Button>
            <Button size="sm" variant="secondary" icon={GitFork} disabled={!!act.busy} mix={[on('click', () => void fork())]} title="A new chat with this chat's messages, its own session">
              {act.busy === 'fork' ? 'Forking...' : 'Fork'}
            </Button>
          </div>
        </header>
        <div mix={subCss}>
          <Popover
            open={settingsOpen}
            onOpen={() => ((settingsOpen = true), void handle.update())}
            onClose={() => ((settingsOpen = false), void handle.update())}
            label="Chat settings"
            testId="chat-settings"
            trigger={(mix) => (
              <button type="button" mix={[chipsBtn, mix]} aria-label="Chat settings" aria-expanded={settingsOpen ? 'true' : 'false'} title="Change the model, effort, permission mode or history access">
                <span mix={chipCss} data-testid="chip-adapter">
                  <Avatar id={chat.adapter} name={adapterLabel(chat.adapter)} size={16} kind="agent" />
                  {chat.wslDistro ? `${adapterLabel(chat.adapter)} ${chatWhere(chat)}` : adapterLabel(chat.adapter)}
                </span>
                <span mix={chipCss} data-testid="chip-model">
                  {chat.model || 'default model'}
                </span>
                {chat.effort ? (
                  <span mix={chipCss} data-testid="chip-effort">
                    {chat.effort} effort
                  </span>
                ) : null}
                <span mix={chipCss} data-testid="chip-mode" data-warn={chat.skipsApprovals ? 'true' : undefined} title={chat.skipsApprovals ? 'This mode skips approvals: the agent runs tools without asking' : undefined}>
                  {chat.skipsApprovals ? <Icon icon={TriangleAlert} size={11} /> : null}
                  {chat.permissionMode || 'default'} mode
                  {chat.skipsApprovals ? ' (skips approvals)' : ''}
                </span>
              </button>
            )}
          >
            <ChatSettings chat={chat} adapter={adapter} busy={act.busy} error={act.error} onSave={async (u) => (await update(u, 'settings')) && ((settingsOpen = false), void handle.update())} onCancel={() => ((settingsOpen = false), void handle.update())} />
          </Popover>
          {propsChips(chat).length ? (
            <button type="button" mix={[chipsBtn, on('click', openProps)]} aria-label={`Skills and MCP servers: ${propsChips(chat).join(', ')}`} title="Change the skills and MCP servers" data-testid="chat-props-chips">
              {(chat.skills?.length ?? 0) > 0 ? (
                <span mix={chipCss} data-testid="chip-skills" title={chat.skills!.map((k) => k.name).join(', ')}>
                  {propsChips({ skills: chat.skills })[0]}
                </span>
              ) : null}
              {(chat.mcpServers?.length ?? 0) > 0 ? (
                <span mix={chipCss} data-testid="chip-mcp" title={chat.mcpServers!.map((g) => g.serverId).join(', ')}>
                  {propsChips({ mcpServers: chat.mcpServers })[0]}
                </span>
              ) : null}
            </button>
          ) : null}
          <span mix={cwdCss} title={chat.cwd} data-testid="chat-cwd">
            <Icon icon={FolderOpen} size={12} />
            <span>{shortPath(chat.cwd)}</span>
          </span>
          <span mix={statusCss} data-testid="chat-status" title={chat.status === 'error' ? chat.lastError : chat.sessionId ? `Session ${chat.sessionId}` : undefined}>
            <span
              mix={[dotCss, running ? pulse : undefined]}
              aria-hidden="true"
              style={{ background: running ? 'var(--success)' : chat.status === 'error' ? 'var(--danger)' : chat.sessionId ? 'var(--accent)' : 'var(--text-faint)' }}
            />
            {running ? 'Running' : chat.status === 'error' ? 'Last turn failed' : chat.sessionId ? 'Session kept' : 'No session yet'}
          </span>
          {chat.forkedFrom ? (
            <a href={routes.chat.href({ id: chat.forkedFrom })} style={{ color: 'inherit', fontSize: 'var(--text-xs)' }}>
              forked from an earlier chat
            </a>
          ) : null}
          <span mix={usageCss} data-testid="chat-usage" title="Tokens and cost reported by the agent CLI">
            {compactNumber(chat.tokensIn)} in · {compactNumber(chat.tokensOut)} out · {costText(chat)}
          </span>
        </div>
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
            {feed.hasMore ? (
              <div mix={olderCss}>
                <Button size="sm" variant="ghost" disabled={feed.loadingOlder} data-testid="chat-older" mix={[on('click', () => void loadOlder())]}>
                  {feed.loadingOlder ? 'Loading older messages...' : 'Load older messages'}
                </Button>
              </div>
            ) : msgs.length ? (
              <div mix={introCss}>
                This is the start of <strong>{chatTitle(chat)}</strong>, a chat with {adapterLabel(chat.adapter)}
                {chat.readHistory ? ' that can read your other chats' : ''}.
              </div>
            ) : null}
            <div role="log" aria-live={list.quiet ? 'off' : 'polite'} aria-relevant="additions" aria-label={`Messages in ${chatTitle(chat)}`} aria-busy={feed.loadingOlder ? 'true' : undefined} data-testid="chat-log">
              {shown}
            </div>
            {!feed.loading && msgs.length === 0 && !streaming ? (
              <Empty title="Start the conversation" icon={Bot}>
                <p>
                  Ask {adapterLabel(chat.adapter)} anything about <code>{shortPath(chat.cwd, 60)}</code>. Every message is kept, and the next one continues the same session.
                </p>
              </Empty>
            ) : null}
            {streaming ? (
              <div mix={streamCss} aria-live="off" data-testid="chat-streaming" aria-label={`${adapterLabel(chat.adapter)} is answering`}>
                <div>{startsGroup(msgs[msgs.length - 1], { role: 'assistant' } as never) ? <Avatar id={chat.adapter} name={adapterLabel(chat.adapter)} size={32} kind="agent" /> : null}</div>
                <div style={{ minWidth: '0' }}>
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
              </div>
            ) : null}
            {turnNote ? (
              <div mix={noteCss}>
                <Notice tone="info">{turnNote}</Notice>
              </div>
            ) : null}
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
              It can be read, searched and attached, not continued.{' '}
              <Button size="sm" variant="link" mix={[on('click', () => void update({ archived: false }, 'unarchive'))]}>
                Unarchive
              </Button>
            </Notice>
          </div>
        ) : null}
        <ChatAsks chat={chat} agentName={adapterLabel(chat.adapter)} />
        <ChatApprovals chat={chat} />
        {!settingsOpen && !toolsOpen && act.error ? (
          <div mix={noteCss}>
            <ErrorNote error={act.error} />
          </div>
        ) : null}
        <ChatComposer
          chat={chat}
          running={running}
          disabled={chat.archived || readOnly}
          disabledReason={readOnly ? s.workspace.license.reason : chat.archived ? 'This chat is archived.' : ''}
          sending={act.busy === 'send'}
          onSend={send}
          onStop={() => void act.run('stop', () => s.client.chatStop(handle.props.id))}
          stopping={act.busy === 'stop'}
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

/** The settings popover: model, effort, permission mode (with the skip-approvals warning) and history access. */
function ChatSettings(
  handle: Handle<{ chat: Chat; adapter: AdapterInfo | undefined; busy: string; error: Error | undefined; onSave: (u: { model: string; effort: string; permissionMode: string; readHistory: boolean }) => void; onCancel: () => void }>,
) {
  const c = handle.props.chat;
  let model = c.model;
  let effort = c.effort;
  let mode = c.permissionMode;
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
    const modes = caps?.permissionModes?.length ? caps.permissionModes : [mode || 'default'];
    return (
      <form
        style={{ display: 'grid', gap: '12px' }}
        mix={on('submit', (e) => {
          e.preventDefault();
          onSave({ model: model.trim(), effort, permissionMode: mode, readHistory });
        })}
      >
        <h2>Chat settings</h2>
        <Field label="Model" hint="Applies from the next message.">
          {(id) => (
            <ModelField id={id} value={model} models={models} emptyLabel="adapter default" placeholder="adapter default" onChange={(v) => set(() => (model = v))} />
          )}
        </Field>
        <Field label="Effort">
          {(id) => (
            <Select
              id={id}
              value={effort}
              disabled={efforts.length === 0 && !effort}
              options={[{ value: '', label: 'adapter default' }, ...selectValues([...new Set([...efforts, ...(effort ? [effort] : [])])])]}
              onChange={(v) => set(() => (effort = v))}
            />
          )}
        </Field>
        <Field label="Permission mode">
          {(id) => (
            <Select id={id} value={mode} options={selectValues([...new Set([...modes, ...(mode ? [mode] : [])])])} onChange={(v) => set(() => (mode = v))} />
          )}
        </Field>
        {skipsApprovals(mode) ? (
          <Notice tone="warn" role="alert" testId="skip-warning" title="This mode skips approvals.">
            The agent runs tools (shell commands, file edits) without asking you. Use it only in a folder you can afford to lose changes in.
          </Notice>
        ) : null}
        <Checkbox label="Let this chat read my other chats" description="A read-only tool to list, search and read your earlier chats." checked={readHistory} onChange={(v) => set(() => (readHistory = v))} />
        <ErrorNote error={error} />
        <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
          <Button size="sm" variant="ghost" mix={[on('click', () => onCancel())]}>
            Cancel
          </Button>
          <Button size="sm" variant="primary" type="submit" disabled={!!busy}>
            {busy === 'settings' ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </form>
    );
  };
}

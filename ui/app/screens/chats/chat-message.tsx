// One stored chat message (graphite, decision P97): the person's own message is a bubble on the right, the agent's answer is
// text on the left (through ui/markdown.tsx, never innerHTML), tool calls are compact cards with how they went (chat-tool.tsx),
// a request the agent waits on is an amber card in its place (chat-asks.tsx), refusals a callout with the suggested allow rule,
// system notes and errors quiet lines. The time, Edit, Regenerate and Fork show on hover or focus.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { Check, CircleAlert, GitFork, Info, Pencil, RotateCcw, ShieldOff } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { Button } from '../../ui/button.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { adapterLabel, agentWord, isRetryMessage } from './chats.ts';
import { ToolCard } from './chat-tool.tsx';
import { ChatFiles } from './chat-files.tsx';
import { ThinkingFold } from './chat-thinking.tsx';

const rowCss = css({
  position: 'relative',
  display: 'grid',
  minWidth: '0',
  padding: '5px 0',
});
// a new side of the conversation (you, or the agent) starts a little further down
const headCss = css({ marginTop: '8px' });
const contentCss = css({ minWidth: '0', display: 'grid', gap: '8px' });
// a reading column (P89): lines of prose past about 90 characters are hard to follow, so an answer stops at 720 px however
// wide the window is (code blocks and tables inside scroll on their own)
export const READING_COLUMN = '720px';
const bodyCss = css({
  minWidth: '0',
  maxWidth: READING_COLUMN,
  overflowWrap: 'anywhere',
  color: t.text,
  lineHeight: '1.65',
  '& p': { margin: '0' },
  '& p + p, & ul, & ol, & pre, & blockquote, & table': { marginTop: '10px' },
});
const quietCss = css({ display: 'grid', gridTemplateColumns: '20px minmax(0, 1fr)', alignItems: 'start', gap: '6px' });
const quietIcon = css({ display: 'grid', placeItems: 'center', height: '20px', color: t.textFaint });
const quietBody = css({ color: t.textMuted, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });
const errorBody = css({ color: t.danger, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });
// the person's own message: a bubble on the right (the accent's soft tint over the surface), corner cut where it points
const mineContent = css({ justifyItems: 'end' });
const bubbleCss = css({
  justifySelf: 'end',
  maxWidth: '70%',
  padding: '10px 14px',
  borderRadius: '12px 12px 4px 12px',
  background: `linear-gradient(${t.accentSoft}, ${t.accentSoft}), ${t.bgSurface}`,
  color: t.text,
  lineHeight: '1.55',
});

const deniedCss = css({
  display: 'grid',
  gap: t.s2,
  maxWidth: READING_COLUMN,
  margin: '2px 0',
  padding: '10px 12px',
  border: `1px solid color-mix(in srgb, ${t.warning} 30%, ${t.border})`,
  borderRadius: t.rMd,
  background: `color-mix(in srgb, ${t.warningSoft} 60%, ${t.bgSurface})`,
  fontSize: t.textMd,
  '& .head': { display: 'flex', alignItems: 'center', gap: '10px', fontWeight: '500' },
  '& .head svg': { color: t.warning },
  '& .what': { color: t.textMuted, fontSize: t.textXs, fontFamily: t.mono, overflowWrap: 'anywhere' },
  '& .rule': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& .actions': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 },
  '& .ok': { display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.success, fontSize: t.textSm, fontWeight: '500' },
});

const toolsCss = css({
  position: 'absolute',
  top: '-12px',
  right: '0',
  zIndex: '2',
  display: 'flex',
  alignItems: 'center',
  gap: '2px',
  padding: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  boxShadow: t.shadowSm,
  opacity: '0',
  transition: `opacity ${t.fast} ${t.ease}`,
  ':is(.chat-msg:hover, .chat-msg:focus-within) > &': { opacity: '1' },
  '& time': { padding: '0 6px', fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' },
});
const toolBtn = css({
  display: 'grid',
  placeItems: 'center',
  width: '28px',
  height: '28px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.textMuted,
  '&:hover': { background: t.bgHover, color: t.text },
});

export interface ChatMessageProps {
  m: ChatMessage;
  /** First message of a group (a new side of the conversation): a little more space above. */
  head: boolean;
  adapter: string;
  /** The chat's allow rules (a refusal whose rule is in here reads "Allowed"). */
  allowedTools: readonly string[];
  /** A refusal of the latest turn offers Retry once its rule is allowed. */
  latest: boolean;
  running: boolean;
  busy: string;
  /** The request the agent waits on that this note records (chat-asks.tsx AskCard): shown in the note's place while it waits. */
  ask?: RemixNode;
  onAllow?: (rule: string) => void;
  onRetry?: () => void;
  /** Allow the rule for the chat, then run the turn again (UI review P1-3): one step instead of two. */
  onAllowAndRetry?: (rule: string) => void;
  onFork?: (seq: number) => void;
  /** The person's own message can be changed and sent again: it and everything after it are replaced (P89). */
  onEdit?: (seq: number, text: string) => Promise<boolean>;
  /** The newest answer can be asked for again (its question sent again unchanged). */
  onRegenerate?: () => void;
  /** How long the agent thought before this row (0: not known), for its reasoning's line "Thought for 6 s" (P139). */
  thoughtMs?: number;
  /** A tool call's card is drawn open (a failed call in a burst, P139). */
  toolOpen?: boolean;
}

const editCss = css({
  display: 'grid',
  gap: '6px',
  width: 'min(100%, 720px)',
  justifySelf: 'end',
  '& textarea': { width: '100%', minHeight: '72px', resize: 'vertical', fontSize: t.textMd },
  '& .row': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'flex-end', gap: t.s2 },
  '& .note': { fontSize: t.textXs, color: t.textMuted, marginRight: 'auto' },
});

// one formatter for every row: toLocaleTimeString makes a new one on each call, and a row is drawn on every change of the chat
const clock = new Intl.DateTimeFormat([], { hour: '2-digit', minute: '2-digit' });

export function ChatMessageRow(handle: Handle<ChatMessageProps>) {
  let editing = false;
  let draft = '';
  let sending = false;
  /** The row's time as shown, worked out once for its createdAt (formatting dates is slow, and a row is drawn often). */
  let stampOf = -1;
  let stamp = { iso: '', short: '', full: '' };
  const stampFor = (ms: number) => {
    if (ms !== stampOf) {
      stampOf = ms;
      stamp = { iso: new Date(ms).toISOString(), short: clock.format(ms), full: formatDateTime(ms) };
    }
    return stamp;
  };
  const startEdit = () => {
    editing = true;
    draft = handle.props.m.body;
    void handle.update();
  };
  const sendEdit = async () => {
    if (!draft.trim() || !handle.props.onEdit) return;
    sending = true;
    void handle.update();
    const ok = await handle.props.onEdit(handle.props.m.seq, draft);
    sending = false;
    if (ok) editing = false;
    void handle.update();
  };
  return () => {
    const { m, head, adapter, allowedTools, latest, running, busy, ask, onAllow, onRetry, onAllowAndRetry, onFork, onEdit, onRegenerate, thoughtMs, toolOpen } = handle.props;
    // the canned "continue" Drydock sends after an allow is a note of what happened, not words the person typed (P1-2)
    const retry = isRetryMessage(m);
    const role = retry ? 'system' : m.role;
    const quiet = role === 'system' || role === 'error';
    const who = role === 'user' ? 'You' : adapterLabel(adapter);
    const forkable = !!onFork && (role === 'user' || role === 'assistant') && !retry;
    const mine = role === 'user';
    return (
      <article
        class={`chat-msg chat-msg-${role}`}
        data-testid="chat-message"
        data-role={role}
        data-seq={String(m.seq)}
        data-mine={mine ? 'true' : undefined}
        data-ask={ask ? 'true' : undefined}
        aria-label={quiet ? `${role === 'error' ? 'Error' : 'Note'}: ${m.body.slice(0, 80)}` : `${who}${role === 'tool' ? ' used a tool' : role === 'denied' ? ' was refused a tool' : ''}`}
        mix={[rowCss, head && !quiet ? headCss : undefined]}
      >
        {quiet && ask ? (
          ask
        ) : quiet ? (
          <div mix={quietCss}>
            <span mix={quietIcon} aria-hidden="true">
              <Icon icon={role === 'error' ? CircleAlert : Info} size={14} />
            </span>
            <div mix={role === 'error' ? errorBody : quietBody} data-retry={retry ? 'true' : undefined}>
              {retry ? 'You allowed the tool and asked to continue.' : m.body}
            </div>
          </div>
        ) : (
          <div mix={[contentCss, mine ? mineContent : undefined]}>
            {m.thinking && (role === 'tool' || role === 'assistant') ? <ThinkingFold text={m.thinking} ms={thoughtMs} /> : null}
            {role === 'tool' ? (
              <ToolCard m={m} summary={summaryOf(m)} running={running && latest} open={toolOpen} />
            ) : role === 'denied' ? (
              <DeniedCallout m={m} adapter={adapter} allowedTools={allowedTools} latest={latest} running={running} busy={busy} onAllow={onAllow} onRetry={onRetry} onAllowAndRetry={onAllowAndRetry} />
            ) : mine && editing ? (
              <div mix={editCss} data-testid="chat-edit">
                <textarea
                  aria-label="Edit your message"
                  value={draft}
                  mix={[
                    on('input', (e) => {
                      draft = (e.currentTarget as HTMLTextAreaElement).value;
                    }),
                    on('keydown', (e) => {
                      const k = e as KeyboardEvent;
                      if (k.key === 'Enter' && (k.ctrlKey || k.metaKey)) {
                        k.preventDefault();
                        void sendEdit();
                      } else if (k.key === 'Escape') {
                        editing = false;
                        void handle.update();
                      }
                    }),
                  ]}
                />
                <div class="row">
                  <span class="note">Sending replaces this message and everything after it.</span>
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={sending}
                    mix={on('click', () => {
                      editing = false;
                      void handle.update();
                    })}
                  >
                    Cancel
                  </Button>
                  <Button size="sm" variant="primary" disabled={sending || running || !draft.trim()} mix={on('click', () => void sendEdit())}>
                    Send again
                  </Button>
                </div>
              </div>
            ) : (
              <div mix={[bodyCss, mine ? bubbleCss : undefined]}>
                <Markdown source={m.body} />
              </div>
            )}
            {m.attachments?.length && !editing ? <ChatFiles list={m.attachments} /> : null}
          </div>
        )}
        {forkable && !editing ? (
          <div class="chat-tools" mix={toolsCss}>
            <time dateTime={stampFor(m.createdAt).iso} title={stampFor(m.createdAt).full}>
              {stampFor(m.createdAt).short}
            </time>
            {mine && onEdit && !running ? (
              <button type="button" mix={[toolBtn, on('click', startEdit)]} aria-label="Edit and send again" title="Edit this message and send it again">
                <Icon icon={Pencil} size={14} />
              </button>
            ) : null}
            {role === 'assistant' && onRegenerate && !running ? (
              <button type="button" mix={[toolBtn, on('click', () => onRegenerate())]} aria-label="Regenerate" title="Ask for this answer again">
                <Icon icon={RotateCcw} size={14} />
              </button>
            ) : null}
            <button type="button" mix={[toolBtn, on('click', () => onFork!(m.seq))]} aria-label="Fork from here" title="Fork a new chat from this message">
              <Icon icon={GitFork} size={14} />
            </button>
          </div>
        ) : null}
      </article>
    );
  };
}

/** "Bash: npm test" -> "npm test" (the name is shown already). */
function summaryOf(m: ChatMessage): string {
  const name = m.toolName ?? '';
  return name && m.body.startsWith(`${name}: `) ? m.body.slice(name.length + 2) : m.body;
}

function DeniedCallout(
  handle: Handle<{
    m: ChatMessage;
    adapter: string;
    allowedTools: readonly string[];
    latest: boolean;
    running: boolean;
    busy: string;
    onAllow?: (rule: string) => void;
    onRetry?: () => void;
    onAllowAndRetry?: (rule: string) => void;
  }>,
) {
  return () => {
    const { m, adapter, allowedTools, latest, running, busy, onAllow, onRetry, onAllowAndRetry } = handle.props;
    const rule = m.suggestedRule ?? '';
    const allowed = !!rule && allowedTools.includes(rule);
    const tool = m.toolName || 'a tool';
    const claude = adapter === 'claude';
    return (
      <div mix={deniedCss} role="note" data-testid="chat-denied" data-rule={rule || undefined}>
        <div class="head">
          <Icon icon={ShieldOff} size={15} />
          <span>
            {agentWord(adapter)} was not allowed to run {tool}
          </span>
        </div>
        <div class="what">{m.body}</div>
        {rule ? (
          <div class="rule">
            <span>Rule that would allow it:</span>
            <code>{rule}</code>
          </div>
        ) : (
          <div class="rule">{claude ? 'No safe exact rule exists for this call (for example a pipeline). Change the permission mode in the chat settings if you want to allow it.' : 'Allow rules work only in Claude Code chats: this agent asks you each time it wants to use a tool.'}</div>
        )}
        {rule ? (
          <div class="actions">
            {allowed ? (
              <span class="ok">
                <Icon icon={Check} size={14} />
                Allowed for this chat
              </span>
            ) : (
              <>
                {latest && onAllowAndRetry ? (
                  <Button size="sm" variant="primary" disabled={running || !!busy} mix={[on('click', () => onAllowAndRetry(rule))]}>
                    {busy === `allow:${rule}` ? 'Allowing...' : 'Allow and retry'}
                  </Button>
                ) : null}
                <Button size="sm" variant="secondary" disabled={!onAllow || !!busy} mix={[on('click', () => onAllow?.(rule))]}>
                  {latest && onAllowAndRetry ? 'Only allow' : busy === `allow:${rule}` ? 'Allowing...' : 'Allow for this chat'}
                </Button>
              </>
            )}
            {allowed && latest ? (
              <Button size="sm" variant="primary" disabled={!onRetry || running || !!busy} mix={[on('click', () => onRetry?.())]}>
                Retry
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>
    );
  };
}

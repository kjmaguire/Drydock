// One stored chat message, drawn like the room's compact comment thread: a 28px avatar and an author line at the head of a
// group (you, or the agent), the text through ui/markdown.tsx (never innerHTML), tool calls as cards with how they went
// (chat-tool.tsx), refusals as a callout with the suggested allow rule, system notes and errors as quiet lines.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleAlert, GitFork, Info, Pencil, RotateCcw, ShieldOff } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { Button } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { adapterLabel, agentWord } from './chats.ts';
import { ToolCard } from './chat-tool.tsx';
import { ThinkingFold } from './chat-thinking.tsx';

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '32px minmax(0, 1fr)',
  columnGap: '12px',
  padding: `1px ${t.s6} 1px ${t.s5}`,
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: `color-mix(in srgb, ${t.bgHover} 60%, transparent)` },
});
const headCss = css({ marginTop: '12px', paddingTop: '3px' });
const metaCss = css({ display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0', lineHeight: '20px' });
const contentCss = css({ minWidth: '0' });
const nameCss = css({ fontWeight: '600', fontSize: t.textMd, color: t.text });
const timeCss = css({ fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' });
// a reading column (P89): lines of prose past about 90 characters are hard to follow, so an answer stops at 760 px however
// wide the window is (code blocks and tables inside scroll on their own)
export const READING_COLUMN = '760px';
const bodyCss = css({
  minWidth: '0',
  maxWidth: READING_COLUMN,
  overflowWrap: 'anywhere',
  color: t.text,
  lineHeight: '1.5',
  '& p': { margin: '0' },
  '& p + p, & ul, & ol, & pre, & blockquote, & table': { marginTop: '6px' },
});
const quietIcon = css({ display: 'grid', placeItems: 'center', width: '32px', height: '20px', color: t.textFaint });
const quietBody = css({ color: t.textMuted, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });
const errorBody = css({ color: t.danger, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });
// the person's own message (Crew HQ, decision P64, as in a room): a dark bubble on the right, the author line above it,
// no avatar. The bubble's colours sit on the body element, mixed in after bodyCss, so their later layer wins.
const mineCss = css({ '& .chat-head': { justifyContent: 'flex-end' }, '& .chat-tools': { right: 'auto', left: t.s5 } });
const mineContent = css({ display: 'grid', justifyItems: 'end' });
const bubbleCss = css({
  justifySelf: 'end',
  maxWidth: 'min(72%, 720px)',
  marginTop: '3px',
  padding: '9px 14px',
  borderRadius: '18px 4px 18px 18px',
  background: t.accent,
  color: t.onAccent,
  '& a': { color: 'inherit', textDecoration: 'underline' },
  '& code': { background: 'color-mix(in srgb, currentColor 14%, transparent)', color: 'inherit' },
  '& pre': { background: 'color-mix(in srgb, currentColor 10%, transparent)', color: 'inherit' },
});

const deniedCss = css({
  display: 'grid',
  gap: t.s2,
  maxWidth: '760px',
  margin: '4px 0',
  padding: `10px ${t.s3}`,
  border: `1px solid color-mix(in srgb, ${t.warning} 35%, ${t.border})`,
  borderRadius: t.rLg,
  background: `color-mix(in srgb, ${t.warningSoft} 70%, ${t.bgSurface})`,
  boxShadow: `inset 3px 0 0 ${t.warning}`,
  fontSize: t.textMd,
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, fontWeight: '600' },
  '& .head svg': { color: t.warning },
  '& .what': { color: t.textMuted, fontSize: t.textSm, fontFamily: t.mono, overflowWrap: 'anywhere' },
  '& .rule': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& .actions': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 },
  '& .ok': { display: 'inline-flex', alignItems: 'center', gap: '4px', color: t.success, fontSize: t.textSm, fontWeight: '500' },
});

const toolsCss = css({
  position: 'absolute',
  top: '-10px',
  right: t.s5,
  zIndex: '2',
  display: 'flex',
  gap: '2px',
  padding: '2px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  boxShadow: t.shadowSm,
  opacity: '0',
  transition: `opacity ${t.fast} ${t.ease}`,
  ':is(.chat-msg:hover, .chat-msg:focus-within) > &': { opacity: '1' },
});
const toolBtn = css({
  display: 'grid',
  placeItems: 'center',
  width: '26px',
  height: '22px',
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
  /** First message of a group: avatar and author line. */
  head: boolean;
  adapter: string;
  /** The chat's allow rules (a refusal whose rule is in here reads "Allowed"). */
  allowedTools: readonly string[];
  /** A refusal of the latest turn offers Retry once its rule is allowed. */
  latest: boolean;
  running: boolean;
  busy: string;
  onAllow?: (rule: string) => void;
  onRetry?: () => void;
  onFork?: (seq: number) => void;
  /** The person's own message can be changed and sent again: it and everything after it are replaced (P89). */
  onEdit?: (seq: number, text: string) => Promise<boolean>;
  /** The newest answer can be asked for again (its question sent again unchanged). */
  onRegenerate?: () => void;
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

const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

export function ChatMessageRow(handle: Handle<ChatMessageProps>) {
  let editing = false;
  let draft = '';
  let sending = false;
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
    const { m, head, adapter, allowedTools, latest, running, busy, onAllow, onRetry, onFork, onEdit, onRegenerate } = handle.props;
    const role = m.role;
    const quiet = role === 'system' || role === 'error';
    const showHead = head && (role === 'user' || role === 'assistant' || role === 'tool' || role === 'denied');
    const who = role === 'user' ? 'You' : adapterLabel(adapter);
    const forkable = !!onFork && (role === 'user' || role === 'assistant');
    const mine = role === 'user';
    return (
      <article
        class={`chat-msg chat-msg-${role}`}
        data-testid="chat-message"
        data-role={role}
        data-seq={String(m.seq)}
        data-mine={mine ? 'true' : undefined}
        aria-label={quiet ? `${role === 'error' ? 'Error' : 'Note'}: ${m.body.slice(0, 80)}` : `${who}${role === 'tool' ? ' used a tool' : role === 'denied' ? ' was refused a tool' : ''}`}
        mix={[rowCss, showHead ? headCss : undefined, mine ? mineCss : undefined]}
      >
        <div>
          {quiet ? (
            <span mix={quietIcon} aria-hidden="true">
              <Icon icon={role === 'error' ? CircleAlert : Info} size={14} />
            </span>
          ) : showHead && !mine ? (
            <Avatar id={adapter} name={who} size={32} kind="agent" />
          ) : null}
        </div>
        <div mix={[contentCss, mine ? mineContent : undefined]}>
          {showHead ? (
            <header class="chat-head" mix={metaCss}>
              <strong mix={nameCss}>{who}</strong>
              <time mix={timeCss} dateTime={new Date(m.createdAt).toISOString()} title={formatDateTime(m.createdAt)}>
                {time(m.createdAt)}
              </time>
            </header>
          ) : null}
          {m.thinking && (role === 'tool' || role === 'assistant') ? <ThinkingFold text={m.thinking} /> : null}
          {role === 'tool' ? (
            <ToolCard m={m} summary={summaryOf(m)} running={running && latest} />
          ) : role === 'denied' ? (
            <DeniedCallout m={m} adapter={adapter} allowedTools={allowedTools} latest={latest} running={running} busy={busy} onAllow={onAllow} onRetry={onRetry} />
          ) : quiet ? (
            <div mix={role === 'error' ? errorBody : quietBody}>{m.body}</div>
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
        </div>
        {forkable && !editing ? (
          <div class="chat-tools" mix={toolsCss}>
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
  handle: Handle<{ m: ChatMessage; adapter: string; allowedTools: readonly string[]; latest: boolean; running: boolean; busy: string; onAllow?: (rule: string) => void; onRetry?: () => void }>,
) {
  return () => {
    const { m, adapter, allowedTools, latest, running, busy, onAllow, onRetry } = handle.props;
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
              <Button size="sm" variant="secondary" disabled={!onAllow || !!busy} mix={[on('click', () => onAllow?.(rule))]}>
                {busy === `allow:${rule}` ? 'Allowing...' : 'Allow for this chat'}
              </Button>
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

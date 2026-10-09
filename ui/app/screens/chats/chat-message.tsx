// One stored chat message, drawn like the room's compact comment thread: a 28px avatar and an author line at the head of a
// group (you, or the agent), the text through ui/markdown.tsx (never innerHTML), tool calls as compact rows with the input
// collapsible, refusals as a callout with the suggested allow rule, system notes and errors as quiet lines.

import { css, on, type Handle } from 'remix/component';
import { Check, ChevronRight, CircleAlert, GitFork, Info, ShieldOff, Wrench } from 'lucide';
import type { ChatMessage } from '../../api/chatClient.ts';
import { formatDateTime } from '../../lib/format.ts';
import { Button } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { adapterLabel, agentWord, toolInputText } from './chats.ts';

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '28px minmax(0, 1fr)',
  columnGap: '10px',
  padding: `1px ${t.s6} 1px ${t.s5}`,
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: `color-mix(in srgb, ${t.bgHover} 60%, transparent)` },
});
const headCss = css({ marginTop: '12px', paddingTop: '3px' });
const metaCss = css({ display: 'flex', alignItems: 'center', gap: '6px', minWidth: '0', lineHeight: '20px' });
const nameCss = css({ fontWeight: '600', fontSize: t.textMd, color: t.text });
const timeCss = css({ fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' });
const bodyCss = css({
  minWidth: '0',
  overflowWrap: 'anywhere',
  color: t.text,
  lineHeight: '1.5',
  '& p': { margin: '0' },
  '& p + p, & ul, & ol, & pre, & blockquote, & table': { marginTop: '6px' },
});
const quietIcon = css({ display: 'grid', placeItems: 'center', width: '28px', height: '20px', color: t.textFaint });
const quietBody = css({ color: t.textMuted, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });
const errorBody = css({ color: t.danger, fontSize: t.textSm, lineHeight: '20px', overflowWrap: 'anywhere' });

const toolCss = css({
  maxWidth: '760px',
  margin: '2px 0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textSm,
  '& > summary': {
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    minWidth: '0',
    height: '26px',
    padding: `0 ${t.s2}`,
    listStyle: 'none',
    cursor: 'pointer',
    borderRadius: t.rMd,
    color: t.textMuted,
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { color: t.text, background: t.bgHover },
  '& > summary:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > summary .chev': { display: 'inline-flex', transition: `transform ${t.fast} ${t.ease}` },
  '&[open] > summary .chev': { transform: 'rotate(90deg)' },
  '& > summary strong': { color: t.text, fontWeight: '600', flex: 'none' },
  '& > summary .sum': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: t.mono, fontSize: t.textXs },
  '& pre': { margin: `0 ${t.s2} ${t.s2}`, maxHeight: '260px', fontSize: t.textXs },
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
}

const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

export function ChatMessageRow(handle: Handle<ChatMessageProps>) {
  return () => {
    const { m, head, adapter, allowedTools, latest, running, busy, onAllow, onRetry, onFork } = handle.props;
    const role = m.role;
    const quiet = role === 'system' || role === 'error';
    const showHead = head && (role === 'user' || role === 'assistant' || role === 'tool' || role === 'denied');
    const who = role === 'user' ? 'You' : adapterLabel(adapter);
    const forkable = !!onFork && (role === 'user' || role === 'assistant');
    return (
      <article
        class={`chat-msg chat-msg-${role}`}
        data-testid="chat-message"
        data-role={role}
        data-seq={String(m.seq)}
        aria-label={quiet ? `${role === 'error' ? 'Error' : 'Note'}: ${m.body.slice(0, 80)}` : `${who}${role === 'tool' ? ' used a tool' : role === 'denied' ? ' was refused a tool' : ''}`}
        mix={[rowCss, showHead ? headCss : undefined]}
      >
        <div>
          {quiet ? (
            <span mix={quietIcon} aria-hidden="true">
              <Icon icon={role === 'error' ? CircleAlert : Info} size={14} />
            </span>
          ) : showHead ? (
            <Avatar id={role === 'user' ? 'you' : adapter} name={who} size={28} kind={role === 'user' ? 'human' : 'agent'} />
          ) : null}
        </div>
        <div style={{ minWidth: '0' }}>
          {showHead ? (
            <header mix={metaCss}>
              <strong mix={nameCss}>{who}</strong>
              <time mix={timeCss} dateTime={new Date(m.createdAt).toISOString()} title={formatDateTime(m.createdAt)}>
                {time(m.createdAt)}
              </time>
            </header>
          ) : null}
          {role === 'tool' ? (
            <details mix={toolCss} data-testid="chat-tool">
              <summary>
                <span class="chev" aria-hidden="true">
                  <Icon icon={ChevronRight} size={12} />
                </span>
                <Icon icon={Wrench} size={12} />
                <strong>{m.toolName || 'Tool'}</strong>
                <span class="sum">{summaryOf(m)}</span>
              </summary>
              {m.toolInput !== undefined ? <pre aria-label={`Input of ${m.toolName || 'the tool'}`}>{toolInputText(m.toolInput)}</pre> : <p style={{ margin: '0 8px 8px', color: t.textFaint }}>No input recorded.</p>}
            </details>
          ) : role === 'denied' ? (
            <DeniedCallout m={m} adapter={adapter} allowedTools={allowedTools} latest={latest} running={running} busy={busy} onAllow={onAllow} onRetry={onRetry} />
          ) : quiet ? (
            <div mix={role === 'error' ? errorBody : quietBody}>{m.body}</div>
          ) : (
            <div mix={bodyCss}>
              <Markdown source={m.body} />
            </div>
          )}
        </div>
        {forkable ? (
          <div mix={toolsCss}>
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
          <div class="rule">{claude ? 'No safe exact rule exists for this call (for example a pipeline). Change the permission mode in the chat settings if you want to allow it.' : 'Allow rules work only in Claude Code chats: this agent asks, and Drydock refuses on your behalf.'}</div>
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

// The tool calls a chat's CLI is waiting to be allowed (an ACP agent asking over its own protocol; chats/permission.go), as
// an amber card in the conversation where the request was made (graphite, decision P97): "<CLI> wants to run a command",
// waiting on you, the call in a box, then Allow once (exactly that call, once), "Allow <tool> in this chat" (also records its
// allow rule for the chat's later turns; offered only when the core names a rule, that is when the chat's agent takes per-chat
// rules) and Deny. A request nobody answers is refused when it expires; one still waiting when the turn ends is withdrawn.

import { css, on, type Handle } from 'remix/component';
import { Shield } from 'lucide';
import type { ChatAsk } from '../../api/chatClient.ts';
import { formatTime } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { askCallText, askHead, ruleWords } from './chats.ts';

/** The input as a person reads it: the JSON shown whole, or nothing when there was none. */
export function askInput(a: Pick<ChatAsk, 'input'>): string {
  if (a.input === undefined || a.input === null) return '';
  return typeof a.input === 'string' ? a.input : JSON.stringify(a.input, null, 2);
}

const cardCss = css({
  display: 'grid',
  gap: '10px',
  maxWidth: '720px',
  padding: '10px 12px',
  border: `1px solid color-mix(in srgb, ${t.warning} 30%, ${t.border})`,
  borderRadius: t.rMd,
  background: `color-mix(in srgb, ${t.warningSoft} 60%, ${t.bgSurface})`,
  '& .head': { display: 'flex', alignItems: 'center', gap: '10px', minWidth: '0' },
  '& .head svg': { flex: 'none', color: t.warning },
  '& .head strong': { fontWeight: '500', minWidth: '0', overflowWrap: 'anywhere' },
  '& .wait': { marginLeft: 'auto', flex: 'none', color: t.warning, fontSize: t.textXs },
  '& code.call': {
    display: 'block',
    padding: '8px 10px',
    border: '0',
    borderRadius: t.rSm,
    background: t.bgApp,
    color: t.text,
    fontFamily: t.mono,
    fontSize: t.textXs,
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
    maxHeight: '180px',
    overflow: 'auto',
  },
  '& .actions': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 },
  '& .expiry': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs },
});

export interface AskCardProps {
  ask: ChatAsk;
  /** The chat's CLI as the header names it ("Claude Code WSL"). */
  cli: string;
  /** The action in flight ('allow:<id>', 'deny:<id>', 'remember:<id>'), '' when none. */
  busy: string;
  /** The core's answer to this request's last answer, when it was refused (the reason "Allow in this chat" cannot remember). */
  error?: Error;
  /** Read-only (no license): the request is shown, not answered here. */
  readOnly?: boolean;
  onAnswer: (allow: boolean) => void;
  onRemember: () => void;
}

/** One waiting request. */
export function AskCard(handle: Handle<AskCardProps>) {
  return () => {
    const { ask: a, cli, busy, error, readOnly, onAnswer, onRemember } = handle.props;
    const head = askHead(cli, a);
    const rule = a.rule ?? '';
    const off = !!busy || !!readOnly;
    return (
      <div mix={cardCss} role="group" aria-label={`${head}, waiting on you`} data-testid="chat-ask" data-ask-id={a.id}>
        <div class="head">
          <Icon icon={Shield} size={14} />
          <strong>{visible(head)}</strong>
          <span class="wait">waiting on you</span>
        </div>
        <code class="call" data-testid="chat-ask-input" title={askInput(a) ? visible(askInput(a)) : undefined}>
          {visible(askCallText(a))}
        </code>
        <div class="actions">
          <Button size="sm" variant="primary" disabled={off} mix={[on('click', () => onAnswer(true))]}>
            {busy === `allow:${a.id}` ? 'Allowing...' : 'Allow once'}
          </Button>
          {rule ? (
            <Button size="sm" variant="secondary" disabled={off} title={`Records ${rule} for this chat: its later turns may make this call without asking`} data-testid="chat-ask-remember" mix={[on('click', () => onRemember())]}>
              {busy === `remember:${a.id}` ? 'Allowing...' : `Allow ${visible(ruleWords(rule))} in this chat`}
            </Button>
          ) : null}
          <Button size="sm" variant="ghost" disabled={off} mix={[on('click', () => onAnswer(false))]}>
            Deny
          </Button>
          <span class="expiry" data-testid="chat-ask-expiry">
            Refused at {formatTime(a.expiresAt)} if nobody answers.
          </span>
        </div>
        {error ? <ErrorNote error={error} /> : null}
      </div>
    );
  };
}

const stripCss = css({ display: 'grid', gap: t.s2, width: '100%', maxWidth: '860px', margin: '0 auto', padding: `${t.s2} ${t.s6} 0`, boxSizing: 'border-box' });

/** The waiting requests whose note is not among the messages loaded (an older page), above the composer. */
export function ChatAsks(handle: Handle<Omit<AskCardProps, 'ask' | 'onAnswer' | 'onRemember' | 'error'> & { asks: ChatAsk[]; errorFor?: string; error?: Error; onAnswer: (a: ChatAsk, allow: boolean) => void; onRemember: (a: ChatAsk) => void }>) {
  return () => {
    const { asks, cli, busy, readOnly, error, errorFor, onAnswer, onRemember } = handle.props;
    if (!asks.length) return null;
    return (
      <div mix={stripCss} data-testid="chat-asks">
        {asks.map((a) => (
          <AskCard key={a.id} ask={a} cli={cli} busy={busy} readOnly={readOnly} error={errorFor === a.id ? error : undefined} onAnswer={(allow) => onAnswer(a, allow)} onRemember={() => onRemember(a)} />
        ))}
      </div>
    );
  };
}

// One message in the list, drawn the Discord/Slack way: avatar and author line at the head of a group, continuations
// indented under it with the time in the gutter on hover, a hover toolbar (reply in thread, copy), markers as chips,
// mentions of a human highlighted, check results and approval records as cards, attachments as file cards. Message text
// is inert (ui/markdown.tsx): it never becomes an element or an action.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, CircleSlash, CircleX, Copy, CornerDownRight, Info, MessageSquareReply, ShieldAlert } from 'lucide';
import type { Message } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { Avatar, Badge, hueFor } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { Attachments } from './attachments.tsx';
import { checkOf, isQuiet, isRecord, markersOf, mentionsHuman, repliesText, shortTime, type CheckPayload } from './messages.ts';

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '40px minmax(0, 1fr)',
  columnGap: t.s3,
  padding: `2px ${t.s6} 2px ${t.s4}`,
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: t.bgHover },
  '&:hover .msg-tools, &:focus-within .msg-tools': { opacity: '1', pointerEvents: 'auto' },
  '&:hover .msg-gutter-time': { opacity: '1' },
});
const headCss = css({ marginTop: t.s3, paddingTop: '4px' });
const mentionCss = css({ background: t.mentionBg, boxShadow: `inset 2px 0 0 ${t.mentionBorder}`, '&:hover': { background: t.mentionBg } });
const errorCss = css({ background: t.dangerSoft, boxShadow: `inset 2px 0 0 ${t.danger}`, '&:hover': { background: t.dangerSoft } });
const quietCss = css({ color: t.textMuted, fontSize: t.textSm });

const gutterCss = css({ display: 'flex', justifyContent: 'center', paddingTop: '2px' });
const gutterTime = css({
  paddingTop: '3px',
  fontSize: t.textXs,
  color: t.textFaint,
  opacity: '0',
  whiteSpace: 'nowrap',
  fontVariantNumeric: 'tabular-nums',
  transition: `opacity ${t.fast} ${t.ease}`,
});
const quietIcon = css({ display: 'grid', placeItems: 'center', width: '28px', height: '22px', color: t.textFaint });

const contentCss = css({ minWidth: '0' });
const metaCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px', minWidth: '0', lineHeight: '22px' });
const nameCss = css({ fontWeight: '600', fontSize: t.textMd, color: t.text });
const tagCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '16px',
  padding: '0 5px',
  borderRadius: t.rSm,
  fontSize: '10px',
  fontWeight: '600',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
});
const timeCss = css({ fontSize: t.textXs, color: t.textFaint, fontVariantNumeric: 'tabular-nums' });
const inThreadCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '3px',
  minHeight: '0',
  height: '18px',
  padding: '0 4px',
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  '&:hover': { background: t.bgActive, color: t.text },
});
const bodyCss = css({
  minWidth: '0',
  overflowWrap: 'anywhere',
  color: t.text,
  lineHeight: '1.5',
  '& p': { margin: '0' },
  '& p + p, & ul, & ol, & pre, & blockquote, & table': { marginTop: '6px' },
});
const quietBodyCss = css({ color: t.textMuted, fontSize: t.textSm });

const toolsCss = css({
  position: 'absolute',
  top: '-14px',
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
  pointerEvents: 'none',
  transition: `opacity ${t.fast} ${t.ease}`,
});
const toolBtn = css({
  display: 'grid',
  placeItems: 'center',
  width: '28px',
  height: '26px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rSm,
  background: 'transparent',
  color: t.textMuted,
  '&:hover': { background: t.bgHover, color: t.text },
});
const repliesBtn = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  minHeight: '0',
  height: '26px',
  marginTop: '4px',
  padding: '0 8px 0 6px',
  border: '1px solid transparent',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.accentText,
  fontSize: t.textSm,
  fontWeight: '600',
  '& .hint': { color: t.textFaint, fontWeight: '400', opacity: '0', transition: `opacity ${t.fast} ${t.ease}` },
  '&:hover': { background: t.bgSurface, borderColor: t.border },
  '&:hover .hint': { opacity: '1' },
});

const MARKER_TONE: Record<string, Tone> = { DECISION: 'accent', TODO: 'warn', STATUS: 'info', RESULT: 'ok' };

/** The author's name color: the agent's own hue mixed toward the text color so it reads in both themes. */
function nameColor(m: Message): string {
  switch (m.author.kind) {
    case 'agent':
      return `color-mix(in oklab, hsl(${hueFor(m.author.id)} 70% 52%) 72%, ${t.text})`;
    case 'tool':
      return t.info;
    case 'system':
      return t.textMuted;
    default:
      return t.text;
  }
}

const KIND_TAG: Record<string, { label: string; tone: Tone }> = {
  agent: { label: 'agent', tone: 'accent' },
  tool: { label: 'tool', tone: 'info' },
  system: { label: 'system', tone: 'muted' },
};

export interface MessageItemProps {
  m: Message;
  /** The author's display name (authorName). */
  name: string;
  /** First message of a group: avatar and author line. */
  head: boolean;
  replies: number;
  /** Open the thread on this message (absent: no thread actions). */
  onReply?: () => void;
  /** Open the thread this reply belongs to. */
  onOpenRoot?: () => void;
}

export function MessageItem(handle: Handle<MessageItemProps>) {
  let copied = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(handle.props.m.body);
      copied = true;
      void handle.update();
      clearTimeout(timer);
      timer = setTimeout(() => {
        copied = false;
        void handle.update();
      }, 1500);
    } catch {
      /* the clipboard can be refused; nothing to report */
    }
  };

  return () => {
    const { m, name, head, replies, onReply, onOpenRoot } = handle.props;
    const check = checkOf(m);
    const quiet = isQuiet(m);
    const markers = markersOf(m);
    const canReply = !!onReply && !m.thread_root_id && m.type !== 'SYSTEM';
    const tag = KIND_TAG[m.author.kind];
    const classes = ['msg', `msg-${m.author.kind}`, `msg-type-${m.type.toLowerCase()}`, m.type === 'ERROR' ? 'msg-error' : '', quiet ? 'msg-quiet' : ''].filter(Boolean).join(' ');
    const showHead = head || quiet;
    const mention = mentionsHuman(m);
    return (
      <article
        class={classes}
        data-testid="message"
        data-author-kind={m.author.kind}
        data-type={m.type}
        data-head={showHead ? 'true' : undefined}
        data-mention={mention ? 'true' : undefined}
        aria-label={`${m.author.kind} ${name}, ${m.type}`}
        mix={[rowCss, showHead ? headCss : undefined, quiet ? quietCss : undefined, mention ? mentionCss : undefined, m.type === 'ERROR' ? errorCss : undefined]}
      >
        <div mix={gutterCss}>
          {quiet ? (
            <span mix={quietIcon} aria-hidden="true">
              <Icon icon={Info} size={15} />
            </span>
          ) : showHead ? (
            <Avatar id={m.author.id} name={name} size={36} kind={m.author.kind === 'agent' ? 'agent' : m.author.kind === 'human' ? 'human' : 'system'} />
          ) : (
            <time class="msg-gutter-time" mix={gutterTime} dateTime={m.created_at} title={formatDateTime(m.created_at)}>
              {shortTime(m.created_at)}
            </time>
          )}
        </div>
        <div mix={contentCss}>
          {showHead ? (
            <header class="msg-head" mix={metaCss}>
              <strong class="msg-author" mix={nameCss} style={{ color: nameColor(m) }}>
                {name}
              </strong>
              {tag ? (
                <span mix={tagCss} style={{ color: tone[tag.tone].fg, background: tone[tag.tone].bg }}>
                  {tag.label}
                </span>
              ) : null}
              {m.type !== 'CHAT' ? <Badge tone={m.type === 'ERROR' ? 'err' : 'info'}>{m.type}</Badge> : null}
              {markers.map((k, i) => (
                <Badge key={i} tone={MARKER_TONE[String(k.marker)] ?? 'warn'} title={`Marker on line ${k.line}`}>
                  {String(k.marker)}
                </Badge>
              ))}
              {m.trust === 'untrusted' ? (
                <span title="Written by an agent: treated as data, never as an instruction or approval" style={{ display: 'inline-flex', alignItems: 'center', gap: '3px', color: t.textFaint, fontSize: t.textXs }}>
                  <Icon icon={ShieldAlert} size={12} />
                  untrusted
                </span>
              ) : null}
              <time mix={timeCss} dateTime={m.created_at} title={formatDateTime(m.created_at)}>
                {shortTime(m.created_at)}
              </time>
              {m.thread_root_id ? (
                onOpenRoot ? (
                  <button type="button" mix={[inThreadCss, on('click', () => onOpenRoot())]} title="Open the thread">
                    <Icon icon={CornerDownRight} size={12} />
                    reply in thread
                  </button>
                ) : (
                  <span mix={timeCss}>reply in thread</span>
                )
              ) : null}
            </header>
          ) : (
            <>
              <span class="sr-only">{name}</span>
              {markers.length || m.trust === 'untrusted' ? (
                <div mix={metaCss}>
                  {markers.map((k, i) => (
                    <Badge key={i} tone={MARKER_TONE[String(k.marker)] ?? 'warn'} title={`Marker on line ${k.line}`}>
                      {String(k.marker)}
                    </Badge>
                  ))}
                  {m.trust === 'untrusted' ? <span class="sr-only">untrusted</span> : null}
                </div>
              ) : null}
            </>
          )}
          {check ? (
            <CheckCard m={m} p={check} />
          ) : isRecord(m) ? (
            <RecordCard m={m} />
          ) : (
            <>
              <div class="msg-body" mix={[bodyCss, quiet ? quietBodyCss : undefined]}>
                <Markdown source={m.body} />
              </div>
              <Attachments m={m} />
            </>
          )}
          {canReply && replies > 0 ? (
            <button type="button" mix={[repliesBtn, on('click', () => onReply!())]} aria-label={`${repliesText(replies)}, open thread`}>
              <Icon icon={MessageSquareReply} size={14} />
              {repliesText(replies)}
              <span class="hint">View thread</span>
            </button>
          ) : null}
        </div>
        {canReply || m.body ? (
          <div class="msg-tools" mix={toolsCss}>
            {canReply ? (
              <button type="button" mix={[toolBtn, on('click', () => onReply!())]} aria-label={`Open thread on message by ${name}`} title="Reply in thread">
                <Icon icon={MessageSquareReply} size={15} />
              </button>
            ) : null}
            {m.body ? (
              <button type="button" mix={[toolBtn, on('click', () => void copy())]} aria-label={copied ? 'Copied' : 'Copy message text'} title={copied ? 'Copied' : 'Copy text'}>
                <Icon icon={copied ? Check : Copy} size={15} />
              </button>
            ) : null}
          </div>
        ) : null}
      </article>
    );
  };
}

const checkCss = css({
  display: 'grid',
  gap: t.s2,
  marginTop: '4px',
  padding: `10px ${t.s3}`,
  maxWidth: '720px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& details summary': { fontSize: t.textSm },
  '& pre': { marginTop: t.s2, maxHeight: '280px' },
});
const checkHead = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textMd, '& .muted': { color: t.textMuted, fontSize: t.textSm } });
const checkLog = css({ color: t.textFaint, fontSize: t.textSm, fontFamily: t.mono, overflowWrap: 'anywhere' });

function CheckCard(handle: Handle<{ m: Message; p: CheckPayload }>) {
  return () => {
    const { m, p } = handle.props;
    const status = p.check_status ?? 'unknown';
    const ok = status === 'pass';
    const skipped = status === 'skipped';
    const tn: Tone = ok ? 'ok' : skipped ? 'muted' : 'err';
    const tool = m.author.id.charAt(0).toUpperCase() + m.author.id.slice(1);
    return (
      <div
        class={`check-card check-${status}`}
        mix={checkCss}
        style={{ boxShadow: `inset 3px 0 0 ${tone[tn].fg}` }}
        data-testid="check-card"
        data-tool={tool}
        data-kind={p.check_kind}
        data-status={status}
      >
        <div mix={checkHead}>
          <span style={{ display: 'inline-flex', color: tone[tn].fg }} aria-hidden="true">
            <Icon icon={ok ? CircleCheck : skipped ? CircleSlash : CircleX} size={16} />
          </span>
          <strong>{tool}</strong>
          <span class="muted">{p.check_kind}</span>
          <Badge tone={tn}>{status.toUpperCase()}</Badge>
          {typeof p.duration_ms === 'number' ? <span class="muted">{p.duration_ms} ms</span> : null}
          {p.failure_signature ? <code title="Normalized failure signature">{p.failure_signature}</code> : null}
        </div>
        {!ok && p.summary ? (
          <details open>
            <summary>Failure summary{p.summary_truncated ? ' (truncated)' : ''}</summary>
            <pre class="check-summary">{p.summary}</pre>
          </details>
        ) : null}
        {p.log_ref ? <div mix={checkLog}>log: {p.log_ref}</div> : null}
      </div>
    );
  };
}

const recordCss = css({
  display: 'grid',
  gap: t.s2,
  marginTop: '4px',
  padding: `10px ${t.s3}`,
  maxWidth: '720px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  boxShadow: `inset 3px 0 0 ${t.warning}`,
  '& .note': { color: t.textMuted, fontSize: t.textSm, margin: '0' },
});

function RecordCard(handle: Handle<{ m: Message }>) {
  return () => {
    const m = handle.props.m;
    return (
      <div class="record-card" mix={recordCss}>
        <div class="msg-body" mix={bodyCss}>
          <Markdown source={m.body} />
        </div>
        <p class="note">This is a record. Decisions are made only with the buttons in the task view or Needs you, never by message text.</p>
        {m.task_id ? <a href={routes.task.href({ id: m.task_id })}>Open the task</a> : null}
      </div>
    );
  };
}

// One message in the list, drawn like a compact comment thread (Linear, Slack compact): a 28px avatar and one author
// line (name, role, time) at the head of a group, continuations indented under it with the time in the gutter on hover,
// a hover toolbar (reply in thread, copy), markers as chips, mentions of a human highlighted, check results and approval
// records as cards, attachments as file cards. Message text is inert (ui/markdown.tsx): it never becomes an element or an
// action.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, CircleSlash, CircleX, Copy, CornerDownRight, Info, MessageSquareReply, ShieldAlert } from 'lucide';
import type { Message } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { Avatar, Badge } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { Attachments } from './attachments.tsx';
import { checkOf, isQuiet, isRecord, markersOf, mentionsHuman, repliesText, shortTime, type CheckPayload } from './messages.ts';

const rowCss = css({
  position: 'relative',
  display: 'grid',
  gridTemplateColumns: '28px minmax(0, 1fr)',
  columnGap: '10px',
  padding: `1px ${t.s6} 1px ${t.s5}`,
  transition: `background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: `color-mix(in srgb, ${t.bgHover} 60%, transparent)` },
});
const headCss = css({ marginTop: '10px', paddingTop: '3px' });
const mentionCss = css({ background: t.mentionBg, boxShadow: `inset 2px 0 0 ${t.mentionBorder}`, '&:hover': { background: t.mentionBg } });
const errorCss = css({ background: `color-mix(in srgb, ${t.dangerSoft} 55%, transparent)`, boxShadow: `inset 2px 0 0 ${t.danger}`, '&:hover': { background: t.dangerSoft } });
const quietCss = css({ color: t.textMuted, fontSize: t.textSm });

const gutterCss = css({ display: 'flex', justifyContent: 'center', paddingTop: '1px' });
const gutterTime = css({
  paddingTop: '2px',
  fontSize: t.textXs,
  color: t.textFaint,
  opacity: '0',
  whiteSpace: 'nowrap',
  fontVariantNumeric: 'tabular-nums',
  transition: `opacity ${t.fast} ${t.ease}`,
  // each css() is its own cascade layer, so the row's hover is matched from here (an ancestor selector)
  ':is(.msg:hover) &': { opacity: '1' },
});
const quietIcon = css({ display: 'grid', placeItems: 'center', width: '28px', height: '20px', color: t.textFaint });

const contentCss = css({ minWidth: '0' });
const metaCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px', minWidth: '0', lineHeight: '20px' });
const nameCss = css({ fontWeight: '600', fontSize: t.textMd, color: t.text });
// the author's role (or kind): a quiet outlined label, not a colored tag
const tagCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '16px',
  padding: '0 5px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rSm,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  lineHeight: '1',
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
  lineHeight: '1.45',
  '& p': { margin: '0' },
  '& p + p, & ul, & ol, & pre, & blockquote, & table': { marginTop: '6px' },
});
const quietBodyCss = css({ color: t.textMuted, fontSize: t.textSm });

const toolsCss = css({
  position: 'absolute',
  top: '-12px',
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
  ':is(.msg:hover, .msg:focus-within) > &': { opacity: '1' },
});
const toolBtn = css({
  display: 'grid',
  placeItems: 'center',
  width: '26px',
  height: '24px',
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

/** The author's name color: plain text (names are not colored by author); system notes are muted. */
function nameColor(m: Message): string {
  return m.author.kind === 'system' ? t.textMuted : t.text;
}

/** The label after the name: an agent's role when known, else the author kind (people get none). */
function authorTag(m: Message, role: string | undefined): string | undefined {
  if (m.author.kind === 'human') return undefined;
  if (m.author.kind === 'agent') return role || 'agent';
  return m.author.kind;
}

export interface MessageItemProps {
  m: Message;
  /** The author's display name (authorName). */
  name: string;
  /** The agent author's role, shown after the name. */
  role?: string;
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
    const { m, name, role, head, replies, onReply, onOpenRoot } = handle.props;
    const check = checkOf(m);
    const quiet = isQuiet(m);
    const markers = markersOf(m);
    const canReply = !!onReply && !m.thread_root_id && m.type !== 'SYSTEM';
    const tag = authorTag(m, role);
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
            <Avatar id={m.author.id} name={name} size={28} kind={m.author.kind === 'agent' ? 'agent' : m.author.kind === 'human' ? 'human' : 'system'} />
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
                <span class="msg-role" mix={tagCss}>
                  {tag}
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

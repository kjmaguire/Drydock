// One message in the list, drawn like a compact comment thread (Linear, Slack compact): a 28px avatar and one author
// line (name, role, time) at the head of a group, continuations indented under it with the time in the gutter on hover,
// a hover toolbar (reply in thread, copy), markers as chips, mentions of a human highlighted, check results and approval
// records as cards, attachments as file cards. Message text is inert (ui/markdown.tsx): it never becomes an element or an
// action.

import { css, on, type Handle } from 'remix/component';
import { Check, CircleCheck, CircleSlash, CircleX, Copy, CornerDownRight, Info, MessageSquareReply, ShieldAlert, Wrench } from 'lucide';
import type { Message } from '../../api/types.ts';
import { formatDateTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { Avatar, Badge } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { Attachments } from './attachments.tsx';
import { checkOf, checksSummary, isQuiet, isRecord, markersOf, mentionsHuman, repliesText, shortTime } from './messages.ts';

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
const toolTile = css({ display: 'grid', placeItems: 'center', width: '32px', height: '32px', borderRadius: '10px', background: t.bgActive, color: t.textMuted });
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
const quietIcon = css({ display: 'grid', placeItems: 'center', width: '32px', height: '20px', color: t.textFaint });

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

// the person's own message (Crew HQ, decision P64): a dark bubble on the right, its author line above it, no avatar.
// Each css() rule lives in its own cascade layer, ordered by first use on the page, so the bubble's colours go on the
// body element itself, mixed in after bodyCss (bubbleCss): a layer declared later always wins there.
const mineCss = css({
  '& .msg-head': { justifyContent: 'flex-end' },
  '& .msg-tools': { right: 'auto', left: t.s5 },
});
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
  '& blockquote': { borderColor: 'color-mix(in srgb, currentColor 40%, transparent)', color: 'inherit' },
});
const mineContent = css({ display: 'grid', justifyItems: 'end' });

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
    // the person's own words are a bubble; their records and check results (none today) would stay cards
    const mine = m.author.kind === 'human' && !quiet && !check && !isRecord(m) && m.type !== 'ERROR';
    return (
      <article
        class={classes}
        data-testid="message"
        data-author-kind={m.author.kind}
        data-type={m.type}
        data-head={showHead ? 'true' : undefined}
        data-mention={mention ? 'true' : undefined}
        aria-label={`${m.author.kind} ${name}, ${m.type}`}
        data-mine={mine ? 'true' : undefined}
        mix={[rowCss, showHead ? headCss : undefined, quiet ? quietCss : undefined, mention ? mentionCss : undefined, m.type === 'ERROR' ? errorCss : undefined, mine ? mineCss : undefined]}
      >
        <div mix={gutterCss}>
          {quiet ? (
            <span mix={quietIcon} aria-hidden="true">
              <Icon icon={Info} size={15} />
            </span>
          ) : mine ? null : showHead ? (
            m.author.kind === 'tool' ? (
              <span mix={toolTile} aria-hidden="true" title={name}>
                <Icon icon={Wrench} size={15} />
              </span>
            ) : (
              <Avatar id={m.author.id} name={name} size={32} kind={m.author.kind === 'agent' ? 'agent' : m.author.kind === 'human' ? 'human' : 'system'} />
            )
          ) : (
            <time class="msg-gutter-time" mix={gutterTime} dateTime={m.created_at} title={formatDateTime(m.created_at)}>
              {shortTime(m.created_at)}
            </time>
          )}
        </div>
        <div mix={[contentCss, mine ? mineContent : undefined]}>
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
            <CheckGroup ms={[m]} />
          ) : isRecord(m) ? (
            <RecordCard m={m} />
          ) : (
            <>
              <div class="msg-body" mix={[bodyCss, quiet ? quietBodyCss : undefined, mine ? bubbleCss : undefined]}>
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

// a room's checks (types, tests, lint, secrets) posted one after another: one row, "Checks: 4/4 passed · 0.4 s", that opens
// into a line per check; a failure opens it and shows what failed (decision P85). The log's path is on its line's title.
const checksCss = css({
  maxWidth: '720px',
  marginTop: '4px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& > summary': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    minHeight: '34px',
    padding: `0 ${t.s3}`,
    listStyle: 'none',
    cursor: 'pointer',
    borderRadius: t.rLg,
    fontSize: t.textMd,
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { background: t.bgHover },
  '& > summary:focus-visible': { boxShadow: t.focus, outline: 'none' },
  '& > summary .muted': { color: t.textMuted, fontSize: t.textSm },
  '& > ul': { display: 'grid', gap: '2px', margin: '0', padding: `0 ${t.s2} ${t.s2}`, listStyle: 'none' },
});
const checkRow = css({
  display: 'grid',
  gap: '4px',
  padding: `4px ${t.s2}`,
  borderRadius: t.rMd,
  '& .line': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textSm },
  '& .muted': { color: t.textMuted },
  '& pre': { margin: '0', maxHeight: '240px', fontSize: t.textXs },
});

const checkTone = (status: string): Tone => (status === 'pass' ? 'ok' : status === 'skipped' ? 'muted' : 'err');
const checkIcon = (status: string) => (status === 'pass' ? CircleCheck : status === 'skipped' ? CircleSlash : CircleX);
const toolName = (m: Message) => m.author.id.charAt(0).toUpperCase() + m.author.id.slice(1);
const fileOf = (path: string) => path.split(/[\\/]/).pop() || path;

/** A row of checks posted one after another. */
export function CheckGroup(handle: Handle<{ ms: Message[] }>) {
  return () => {
    const ms = handle.props.ms;
    const sum = checksSummary(ms);
    const tn: Tone = sum.failed ? 'err' : 'ok';
    const first = ms[0]!;
    return (
      <article class="msg msg-tool msg-checks" data-testid="message" data-author-kind="tool" data-type="CHECKS" aria-label={`Checks: ${sum.text}`} mix={[rowCss, headCss]}>
        <div mix={gutterCss}>
          <span mix={toolTile} aria-hidden="true">
            <Icon icon={Wrench} size={15} />
          </span>
        </div>
        <div mix={contentCss}>
          <details mix={checksCss} open={sum.failed > 0} data-testid="checks" data-failed={String(sum.failed)}>
            <summary>
              <span style={{ display: 'inline-flex', color: tone[tn].fg }} aria-hidden="true">
                <Icon icon={sum.failed ? CircleX : CircleCheck} size={16} />
              </span>
              <strong>Checks</strong>
              <span data-testid="checks-summary">{sum.text}</span>
              {sum.ms ? <span class="muted">{sum.ms < 1000 ? `${sum.ms} ms` : `${(sum.ms / 1000).toFixed(1)} s`}</span> : null}
              <time class="muted" dateTime={first.created_at} title={formatDateTime(first.created_at)} style={{ marginLeft: 'auto' }}>
                {shortTime(first.created_at)}
              </time>
            </summary>
            <ul>
              {ms.map((m) => {
                const p = checkOf(m)!;
                const status = p.check_status ?? 'unknown';
                return (
                  <li key={m.id} class={`check-card check-${status}`} mix={checkRow} data-testid="check-card" data-tool={toolName(m)} data-kind={p.check_kind} data-status={status}>
                    <div class="line">
                      <span style={{ display: 'inline-flex', color: tone[checkTone(status)].fg }} aria-hidden="true">
                        <Icon icon={checkIcon(status)} size={14} />
                      </span>
                      <strong>{toolName(m)}</strong>
                      {status !== 'pass' ? <Badge tone={checkTone(status)}>{status.toUpperCase()}</Badge> : <span class="sr-only">passed</span>}
                      {typeof p.duration_ms === 'number' ? <span class="muted">{p.duration_ms} ms</span> : null}
                      {p.failure_signature ? <code title="Normalized failure signature">{p.failure_signature}</code> : null}
                      {p.log_ref ? (
                        <span class="muted" title={p.log_ref} style={{ marginLeft: 'auto' }}>
                          log {fileOf(p.log_ref)}
                        </span>
                      ) : null}
                    </div>
                    {status !== 'pass' && p.summary ? <pre class="check-summary" aria-label={`What ${toolName(m)} reported${p.summary_truncated ? ' (truncated)' : ''}`}>{p.summary}</pre> : null}
                  </li>
                );
              })}
            </ul>
          </details>
        </div>
      </article>
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

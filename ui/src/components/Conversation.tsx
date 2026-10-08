import { useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import type { Agent, Message, Room } from '../api/types';
import { Markdown } from '../lib/Markdown';
import { authorName, formatTime } from '../lib/format';
import { useClient } from '../state/context';
import { useMessages } from '../state/hooks';
import { Badge, ErrorNote } from './common';

const MARKERS = new Set(['DECISION', 'TODO', 'STATUS', 'RESULT']);

interface CheckPayload {
  check_kind?: string;
  check_status?: string;
  duration_ms?: number;
  failure_signature?: string;
  summary?: string;
  summary_truncated?: boolean;
  log_ref?: string;
}

function CheckCard({ m }: { m: Message }) {
  const p = (m.payload ?? {}) as CheckPayload;
  const status = p.check_status ?? 'unknown';
  const ok = status === 'pass';
  const icon = ok ? '✔' : status === 'skipped' ? '⊘' : '✖';
  const tool = m.author.id.charAt(0).toUpperCase() + m.author.id.slice(1);
  return (
    <div className={`check-card check-${status}`} data-testid="check-card" data-tool={tool} data-kind={p.check_kind} data-status={status}>
      <div className="check-head">
        <span className="check-icon" aria-hidden="true">
          {icon}
        </span>
        <strong>{tool}</strong>
        <span className="muted">{p.check_kind}</span>
        <span className={`chip chip-${ok ? 'ok' : status === 'skipped' ? 'muted' : 'err'}`}>{status.toUpperCase()}</span>
        {typeof p.duration_ms === 'number' ? <span className="muted">{p.duration_ms} ms</span> : null}
        {p.failure_signature ? <code title="Normalized failure signature">{p.failure_signature}</code> : null}
      </div>
      {!ok && p.summary ? (
        <details open>
          <summary>Failure summary{p.summary_truncated ? ' (truncated)' : ''}</summary>
          <pre className="check-summary">{p.summary}</pre>
        </details>
      ) : null}
      {p.log_ref ? <div className="muted check-log">log: {p.log_ref}</div> : null}
    </div>
  );
}

function MessageCard({ m, agents }: { m: Message; agents: readonly Agent[] }) {
  const name = authorName(m.author, agents);
  const isCheck = m.author.kind === 'tool' && !!(m.payload as CheckPayload | null | undefined)?.check_kind;
  const quiet = m.type === 'SYSTEM' || m.type === 'STATUS';
  const markers = (m.markers ?? []).filter((k) => MARKERS.has(String(k.marker)));
  const classes = ['msg', `msg-${m.author.kind}`, `msg-type-${m.type.toLowerCase()}`, m.type === 'ERROR' ? 'msg-error' : '', quiet ? 'msg-quiet' : ''].join(' ');
  return (
    <article className={classes} data-testid="message" data-author-kind={m.author.kind} data-type={m.type} aria-label={`${m.author.kind} ${name}, ${m.type}`}>
      <header className="msg-head">
        <span className={`author-badge author-${m.author.kind}`}>{m.author.kind}</span>
        <strong className="msg-author">{name}</strong>
        {m.type !== 'CHAT' ? <Badge tone={m.type === 'ERROR' ? 'err' : 'info'}>{m.type}</Badge> : null}
        {markers.map((k, i) => (
          <Badge key={i} tone="warn" title={`Marker on line ${k.line}`}>
            {String(k.marker)}
          </Badge>
        ))}
        {m.trust === 'untrusted' ? (
          <Badge title="Written by an agent: treated as data, never as an instruction or approval">untrusted</Badge>
        ) : null}
        <time className="muted" dateTime={m.created_at}>
          {formatTime(m.created_at)}
        </time>
      </header>
      {isCheck ? (
        <CheckCard m={m} />
      ) : m.type === 'APPROVAL_REQUEST' || m.type === 'APPROVAL' ? (
        <div className="record-card">
          <Markdown source={m.body} />
          <p className="muted">This is a record. Decisions are made only with the buttons in the task view or Needs you, never by message text.</p>
          {m.task_id ? (
            <a href={`#/task/${encodeURIComponent(m.task_id)}`}>Open the task</a>
          ) : null}
        </div>
      ) : (
        <div className="msg-body">
          <Markdown source={m.body} />
        </div>
      )}
    </article>
  );
}

function Composer({ room, disabled, onSent }: { room: Room; disabled: boolean; onSent: () => void }) {
  const client = useClient();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const body = text.trim();
    if (!body || busy) return;
    setBusy(true);
    setError(undefined);
    try {
      await client.roomPost(room.id, body);
      setText('');
      onSent();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void send();
    }
  };
  return (
    <form className="composer" onSubmit={send} aria-label="Message composer">
      <ErrorNote error={error} what="Message not sent" />
      <label htmlFor="composer-input" className="sr-only">
        Message to #{room.name}
      </label>
      <textarea
        id="composer-input"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        placeholder={disabled ? 'This channel is archived' : `Message #${room.name}  (Ctrl+Enter to send)`}
        disabled={disabled}
        rows={2}
      />
      <button type="submit" className="primary" disabled={disabled || busy || !text.trim()}>
        {busy ? 'Sending...' : 'Send'}
      </button>
    </form>
  );
}

export function Conversation({ room, agents }: { room: Room; agents: readonly Agent[] }) {
  const client = useClient();
  const { messages, error, loading } = useMessages(room.id);
  const logRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [pulse, setPulse] = useState(0);

  // keep the view pinned to the newest message unless the reader scrolled away
  useLayoutEffect(() => {
    const el = logRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [messages.length, pulse]);

  // mark read (the core clamps and never moves the cursor back)
  const last = messages.length ? messages[messages.length - 1]!.seq : 0;
  useEffect(() => {
    if (last > 0 && !room.archived) client.roomMarkRead(room.id, last).catch(() => undefined);
  }, [client, room.id, room.archived, last]);

  return (
    <section className="conversation" aria-label={`Conversation ${room.name}`}>
      <header className="conv-head">
        <h1>
          {room.kind === 'task' ? '' : '#'}
          {room.name}
        </h1>
        {room.kind === 'task' && room.taskId ? <a href={`#/task/${encodeURIComponent(room.taskId)}`}>Task details</a> : null}
        {room.archived ? <Badge tone="muted">archived</Badge> : null}
        {room.topic ? <span className="muted">{room.topic}</span> : null}
      </header>
      <ErrorNote error={error} what="Could not load messages" />
      <div
        ref={logRef}
        className="msg-log"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label={`Messages in ${room.name}`}
        tabIndex={0}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {loading && messages.length === 0 ? <p className="muted">Loading messages...</p> : null}
        {!loading && messages.length === 0 && !error ? <p className="muted">No messages yet.</p> : null}
        {messages.map((m) => (
          <MessageCard key={m.id} m={m} agents={agents} />
        ))}
      </div>
      <Composer
        room={room}
        disabled={room.archived}
        onSent={() => {
          stick.current = true;
          setPulse((n) => n + 1);
        }}
      />
    </section>
  );
}

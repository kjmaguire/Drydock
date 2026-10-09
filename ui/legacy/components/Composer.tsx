import { useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type FormEvent, type KeyboardEvent } from 'react';
import type { Agent, Room, RoomMember } from '../api/types';
import { activeToken, applySuggestion, mentionHandle, parseSlash, SLASH_COMMANDS, suggest, type MentionTarget, type Suggestion, type Token } from '../lib/composer';
import { authorName } from '../lib/format';
import { useClient } from '../state/context';
import { ErrorNote } from './common';

const MAX_FILES = 4;
const MAX_SIZE = 10 << 20;

interface Pending {
  name: string;
  size: number;
  data: Uint8Array;
}

function formatSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

/**
 * The room composer (brief 21): plain text, with help typing @mentions, @role: mentions and #channels, /commands, and files
 * attached to the message. Ctrl+Enter sends. What a mention means is decided by the room core when the message is posted.
 */
export function Composer({
  room,
  agents,
  members,
  rooms,
  disabled,
  threadRoot,
  onSent,
}: {
  room: Room;
  agents: readonly Agent[];
  members: readonly RoomMember[];
  rooms: readonly Room[];
  disabled: boolean;
  threadRoot?: string;
  onSent: () => void;
}) {
  const client = useClient();
  const [text, setText] = useState('');
  const [caret, setCaret] = useState(0);
  const [files, setFiles] = useState<Pending[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const [note, setNote] = useState('');
  const [help, setHelp] = useState(false);
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState<number | undefined>();
  const area = useRef<HTMLTextAreaElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const targets: MentionTarget[] = useMemo(
    () => members.map((m) => ({ kind: m.member.kind, id: m.member.id, name: authorName(m.member, agents), role: m.role })),
    [members, agents],
  );
  const tok: Token | undefined = disabled ? undefined : activeToken(text, caret);
  const list: Suggestion[] = tok && dismissed !== tok.start ? suggest(tok, { members: targets, rooms: rooms.filter((r) => !r.archived && (r.kind === 'channel' || r.kind === 'task')) }) : [];
  const open = list.length > 0;
  const idx = Math.min(active, Math.max(list.length - 1, 0));

  const pick = (s: Suggestion) => {
    if (!tok) return;
    const r = applySuggestion(text, tok, s);
    setText(r.text);
    setCaret(r.caret);
    setActive(0);
    requestAnimationFrame(() => {
      area.current?.focus();
      area.current?.setSelectionRange(r.caret, r.caret);
    });
  };

  const addFiles = async (list: FileList | File[]) => {
    setError(undefined);
    const incoming = Array.from(list);
    if (files.length + incoming.length > MAX_FILES) {
      setError(new Error(`At most ${MAX_FILES} files go with one message.`));
      return;
    }
    const read: Pending[] = [];
    for (const f of incoming) {
      if (f.size > MAX_SIZE) {
        setError(new Error(`${f.name} is over ${MAX_SIZE >> 20} MB.`));
        return;
      }
      read.push({ name: f.name, size: f.size, data: new Uint8Array(await f.arrayBuffer()) });
    }
    setFiles((cur) => [...cur, ...read]);
  };

  const reset = () => {
    setText('');
    setCaret(0);
    setFiles([]);
    onSent();
  };

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    if (busy || disabled) return;
    const p = parseSlash(text);
    if (p.kind === 'post' && !p.body && files.length === 0) return;
    if (p.kind !== 'post' && files.length > 0) {
      setError(new Error('Files go with a message, not with a command.'));
      return;
    }
    setBusy(true);
    setError(undefined);
    setNote('');
    try {
      switch (p.kind) {
        case 'post':
          if (files.length) await client.roomAttach(room.id, p.body, files, threadRoot);
          else await client.roomPost(room.id, p.body, threadRoot);
          reset();
          break;
        case 'task': {
          const t = await client.taskCreate({ projectId: room.projectId, title: p.title });
          reset();
          window.location.hash = `#/task/${encodeURIComponent(t.id)}`;
          break;
        }
        case 'invite': {
          const names = agents.map((a) => a.name);
          const a = agents.find((x) => x.id === p.handle || mentionHandle(x.name, x.id, names) === p.handle.toLowerCase());
          if (!a) throw new Error(`No agent of this project is called @${p.handle}.`);
          await client.roomInvite(room.id, a.id);
          setNote(`${a.name} was added to the room.`);
          reset();
          break;
        }
        case 'mode':
          await client.roomSetMode({ roomId: room.id, mode: p.mode });
          setNote(`The room is now in ${p.mode} mode.`);
          reset();
          break;
        case 'help':
          setHelp(true);
          setText('');
          break;
        case 'error':
          throw new Error(p.message);
      }
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (open) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActive((idx + 1) % list.length);
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActive((idx - 1 + list.length) % list.length);
        return;
      }
      if ((e.key === 'Enter' && !e.ctrlKey && !e.metaKey) || e.key === 'Tab') {
        e.preventDefault();
        pick(list[idx]!);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        setDismissed(tok?.start);
        return;
      }
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void send();
    }
  };

  const onDrop = (e: DragEvent) => {
    if (!e.dataTransfer?.files?.length || disabled) return;
    e.preventDefault();
    void addFiles(e.dataTransfer.files);
  };
  const onPaste = (e: ClipboardEvent) => {
    const fs = e.clipboardData?.files;
    if (fs && fs.length && !disabled) {
      e.preventDefault();
      void addFiles(fs);
    }
  };

  const listId = `composer-suggest-${room.id}`;
  return (
    <form className="composer" onSubmit={send} aria-label="Message composer" onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
      <ErrorNote error={error} what="Not sent" />
      {note ? (
        <p className="muted" role="status">
          {note}
        </p>
      ) : null}
      {help ? (
        <div className="composer-help" role="region" aria-label="Commands">
          <button type="button" className="link-button" onClick={() => setHelp(false)}>
            Close
          </button>
          <ul>
            {SLASH_COMMANDS.map((c) => (
              <li key={c.name}>
                <code>
                  /{c.name}
                  {c.args ? ` ${c.args}` : ''}
                </code>{' '}
                {c.help}
              </li>
            ))}
          </ul>
          <p className="muted">Type @ to mention someone in the room, @role: for everyone with a role, # for a channel. Markers: [DECISION] [TODO] [STATUS] [RESULT] at the start of a line.</p>
        </div>
      ) : null}
      {files.length ? (
        <ul className="composer-files" aria-label="Files to attach">
          {files.map((f, i) => (
            <li key={`${f.name}-${i}`} className="chip">
              {f.name} <span className="muted">{formatSize(f.size)}</span>
              <button type="button" className="link-button" aria-label={`Remove ${f.name}`} onClick={() => setFiles((cur) => cur.filter((_, j) => j !== i))}>
                ×
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="composer-row">
        <label htmlFor={`composer-input-${room.id}`} className="sr-only">
          Message to #{room.name}
        </label>
        <div className="composer-input-wrap">
          {open ? (
            <ul className="composer-suggest" role="listbox" id={listId} aria-label="Suggestions">
              {list.map((s, i) => (
                <li
                  key={s.insert + i}
                  id={`${listId}-${i}`}
                  role="option"
                  aria-selected={i === idx}
                  className={i === idx ? 'active' : ''}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    pick(s);
                  }}
                >
                  <strong>{s.label}</strong> {s.detail ? <span className="muted">{s.detail}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}
          <textarea
            id={`composer-input-${room.id}`}
            ref={area}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setCaret(e.target.selectionStart ?? e.target.value.length);
              setActive(0);
              setDismissed(undefined);
            }}
            onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
            onKeyDown={onKey}
            onPaste={onPaste}
            placeholder={disabled ? 'This channel is archived' : `Message #${room.name}  (Ctrl+Enter to send, / for commands, @ to mention)`}
            disabled={disabled}
            rows={2}
            role="combobox"
            aria-expanded={open}
            aria-controls={open ? listId : undefined}
            aria-activedescendant={open ? `${listId}-${idx}` : undefined}
            aria-autocomplete="list"
          />
        </div>
        <input ref={fileInput} type="file" multiple hidden onChange={(e) => e.target.files && void addFiles(e.target.files).then(() => (e.target.value = ''))} data-testid="composer-file" />
        <button type="button" onClick={() => fileInput.current?.click()} disabled={disabled || files.length >= MAX_FILES} title="Attach files (or drop or paste them here)">
          Attach
        </button>
        <button type="submit" className="primary" disabled={disabled || busy || (!text.trim() && files.length === 0)}>
          {busy ? 'Sending...' : 'Send'}
        </button>
      </div>
    </form>
  );
}

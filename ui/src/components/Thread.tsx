import { useCallback, useEffect, useState, type FormEvent } from 'react';
import type { Agent, Message } from '../api/types';
import { Markdown } from '../lib/Markdown';
import { authorName, formatTime } from '../lib/format';
import { useClient, useServices } from '../state/context';
import { ErrorNote } from './common';

/** One thread: the root message and its replies (room.thread), with a reply box that posts with thread_root. */
export function ThreadPanel({ roomId, rootId, agents, onClose }: { roomId: string; rootId: string; agents: readonly Agent[]; onClose: () => void }) {
  const client = useClient();
  const { store } = useServices();
  const [msgs, setMsgs] = useState<Message[]>([]);
  const [error, setError] = useState<Error | undefined>();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setMsgs(await client.roomThread(rootId));
      setError(undefined);
    } catch (e) {
      setError(e as Error);
    }
  }, [client, rootId]);

  useEffect(() => {
    void load();
    return store.onEvent((e) => {
      const d = e.data as { room?: string } | undefined;
      if (e.type === 'room.message_posted' && d?.room === roomId) void load();
    });
  }, [store, roomId, load]);

  const send = async (e: FormEvent) => {
    e.preventDefault();
    if (!text.trim() || busy) return;
    setBusy(true);
    try {
      await client.roomPost(roomId, text.trim(), rootId);
      setText('');
      await load();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="thread" data-testid="thread">
      <div className="form-actions">
        <strong>Thread</strong>
        <button onClick={onClose}>Close</button>
      </div>
      <ErrorNote error={error} />
      <div role="log" aria-live="polite" aria-label="Thread messages">
        {msgs.map((m) => (
          <article key={m.id} className="msg" data-testid="thread-message">
            <header className="msg-head">
              <span className={`author-badge author-${m.author.kind}`}>{m.author.kind}</span>
              <strong>{authorName(m.author, agents)}</strong>
              <time className="muted">{formatTime(m.created_at)}</time>
            </header>
            <Markdown source={m.body} />
          </article>
        ))}
      </div>
      <form onSubmit={send} className="form">
        <label>
          Reply in thread
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={2} />
        </label>
        <div className="form-actions">
          <button type="submit" className="primary" disabled={busy || !text.trim()}>
            Reply
          </button>
        </div>
      </form>
    </div>
  );
}

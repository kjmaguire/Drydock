import { useEffect, useState } from 'react';
import type { Room } from '../api/types';
import { useClient } from '../state/context';
import { ErrorNote } from './common';

interface ExportView {
  id: string;
  kind: string;
  format: string;
  content: string;
  sha256: string;
  createdAt: string;
}

/** Markdown minutes and ADR candidates of a room (room-protocol s8): an immutable snapshot, shown and offered for download. */
export function RoomExports({ room }: { room: Room }) {
  const client = useClient();
  const [ex, setEx] = useState<ExportView | undefined>();
  const [error, setError] = useState<Error | undefined>();
  const [busy, setBusy] = useState(false);
  const [url, setUrl] = useState<string | undefined>();
  useEffect(() => () => {
    if (url) URL.revokeObjectURL(url);
  }, [url]);
  const run = async (kind: 'minutes' | 'adr_candidates') => {
    setBusy(true);
    setError(undefined);
    try {
      const r = await client.roomExport(room.id, kind);
      setEx(r);
      setUrl(URL.createObjectURL(new Blob([r.content], { type: 'text/markdown' })));
    } catch (e) {
      setError(e as Error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <details className="room-exports">
      <summary>Export</summary>
      <div className="room-exports-body">
        <button disabled={busy} onClick={() => run('minutes')}>
          Minutes (Markdown)
        </button>{' '}
        <button disabled={busy} onClick={() => run('adr_candidates')}>
          ADR candidates
        </button>
        <ErrorNote error={error} what="Export failed" />
        {ex ? (
          <div data-testid="room-export">
            <p className="muted">
              Snapshot <code>{ex.sha256.slice(0, 12)}</code>, kept and verifiable.{' '}
              {url ? (
                <a href={url} download={`${room.name}-${ex.kind === 'minutes' ? 'minutes' : 'adr-candidates'}.${ex.format === 'json' ? 'json' : 'md'}`}>
                  Download
                </a>
              ) : null}
            </p>
            <pre className="log-text export-text">{ex.content}</pre>
          </div>
        ) : null}
      </div>
    </details>
  );
}

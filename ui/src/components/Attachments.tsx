import { useEffect, useState } from 'react';
import type { AttachmentInfo } from '../api/attachClient';
import type { Message } from '../api/types';
import { useClient } from '../state/context';
import { ErrorNote } from './common';

/** Raster image types previewed inline (as a data: URL, which the CSP allows); SVG is never rendered. */
const PREVIEW = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

function formatSize(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

export function attachmentsOf(m: Message): AttachmentInfo[] {
  const a = (m.payload as { attachments?: AttachmentInfo[] } | null | undefined)?.attachments;
  return Array.isArray(a) ? a : [];
}

/** The files attached to a message: name, size, Save, and a preview for raster images on request. */
export function Attachments({ m }: { m: Message }) {
  const list = attachmentsOf(m);
  if (list.length === 0) return null;
  return (
    <ul className="attachments" aria-label="Attached files">
      {list.map((a) => (
        <AttachmentItem key={a.id} a={a} />
      ))}
    </ul>
  );
}

function AttachmentItem({ a }: { a: AttachmentInfo }) {
  const client = useClient();
  const [error, setError] = useState<Error | undefined>();
  const [preview, setPreview] = useState<string | undefined>();
  const [url, setUrl] = useState<string | undefined>();
  useEffect(() => () => {
    if (url) URL.revokeObjectURL(url);
  }, [url]);
  const fetchIt = async () => {
    setError(undefined);
    try {
      return await client.roomAttachment(a.id);
    } catch (e) {
      setError(e as Error);
      return undefined;
    }
  };
  return (
    <li className="attachment" data-testid="attachment">
      <span aria-hidden="true">📎</span> <strong>{a.name}</strong> <span className="muted">{formatSize(a.size)}</span>{' '}
      {url ? (
        <a href={url} download={a.name}>
          Download {a.name}
        </a>
      ) : (
        <button
          className="link-button"
          onClick={async () => {
            const d = await fetchIt();
            if (d) setUrl(URL.createObjectURL(new Blob([d.data as BlobPart], { type: 'application/octet-stream' })));
          }}
        >
          Save
        </button>
      )}
      {PREVIEW.has(a.mime) && !preview ? (
        <>
          {' '}
          <button
            className="link-button"
            onClick={async () => {
              const d = await fetchIt();
              if (!d) return;
              let s = '';
              for (let i = 0; i < d.data.length; i += 0x8000) s += String.fromCharCode(...d.data.subarray(i, i + 0x8000));
              setPreview(`data:${a.mime};base64,${btoa(s)}`);
            }}
          >
            Show image
          </button>
        </>
      ) : null}
      {preview ? <img className="attachment-preview" src={preview} alt={a.name} /> : null}
      <ErrorNote error={error} what="Could not read the file" />
    </li>
  );
}

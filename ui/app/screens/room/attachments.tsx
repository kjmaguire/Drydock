// The files attached to a message, drawn as file cards: name, size, Save (read through the core, then offered as a
// download) and, for raster images, a preview on request. SVG is never rendered; a preview is a data: URL.

import { css, on, type Handle } from 'remix/component';
import { Download, Eye, FileImage, FileText } from 'lucide';
import type { AttachmentInfo } from '../../api/attachClient.ts';
import type { Message } from '../../api/types.ts';
import { services } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { attachmentsOf, formatSize } from './messages.ts';

/** Raster image types previewed inline (as a data: URL, which the CSP allows); SVG is never rendered. */
const PREVIEW = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

const listCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, margin: `${t.s2} 0 2px`, padding: '0', listStyle: 'none' });
const cardCss = css({
  display: 'grid',
  gap: t.s2,
  width: 'min(380px, 100%)',
  padding: `10px ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  boxShadow: t.shadowSm,
});
const topCss = css({ display: 'flex', alignItems: 'center', gap: t.s3, minWidth: '0' });
const iconBox = css({
  display: 'grid',
  placeItems: 'center',
  flex: 'none',
  width: '36px',
  height: '36px',
  borderRadius: t.rMd,
  background: t.accentSoft,
  color: t.accentText,
});
const metaCss = css({
  display: 'grid',
  flex: '1',
  minWidth: '0',
  '& strong': { fontSize: t.textMd, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& span': { fontSize: t.textSm, color: t.textMuted },
});
const actsCss = css({ display: 'flex', alignItems: 'center', gap: '2px', flex: 'none' });
const previewCss = css({ display: 'block', maxWidth: '100%', maxHeight: '320px', borderRadius: t.rMd, border: `1px solid ${t.border}` });

export function Attachments(handle: Handle<{ m: Message }>) {
  return () => {
    const list = attachmentsOf(handle.props.m);
    if (list.length === 0) return null;
    return (
      <ul mix={listCss} aria-label="Attached files">
        {list.map((a) => (
          <AttachmentItem key={a.id} a={a} />
        ))}
      </ul>
    );
  };
}

function AttachmentItem(handle: Handle<{ a: AttachmentInfo }>) {
  const s = services(handle);
  let error: Error | undefined;
  let preview: string | undefined;
  let url: string | undefined;
  let busy = false;
  handle.signal.addEventListener('abort', () => {
    if (url) URL.revokeObjectURL(url);
  });

  const fetchIt = async () => {
    error = undefined;
    busy = true;
    void handle.update();
    try {
      return await s.client.roomAttachment(handle.props.a.id);
    } catch (e) {
      error = e instanceof Error ? e : new Error(String(e));
      return undefined;
    } finally {
      busy = false;
      void handle.update();
    }
  };

  const save = async () => {
    const d = await fetchIt();
    if (!d) return;
    url = URL.createObjectURL(new Blob([d.data as BlobPart], { type: 'application/octet-stream' }));
    void handle.update();
  };

  const show = async () => {
    const d = await fetchIt();
    if (!d) return;
    let bin = '';
    for (let i = 0; i < d.data.length; i += 0x8000) bin += String.fromCharCode(...d.data.subarray(i, i + 0x8000));
    preview = `data:${handle.props.a.mime};base64,${btoa(bin)}`;
    void handle.update();
  };

  return () => {
    const a = handle.props.a;
    const image = PREVIEW.has(a.mime);
    return (
      <li mix={cardCss} data-testid="attachment">
        <div mix={topCss}>
          <span mix={iconBox} aria-hidden="true">
            <Icon icon={image ? FileImage : FileText} size={18} />
          </span>
          <div mix={metaCss}>
            <strong title={a.name}>{a.name}</strong>
            <span>{formatSize(a.size)}</span>
          </div>
          <div mix={actsCss}>
            {url ? (
              <a href={url} download={a.name} mix={buttonStyle('secondary', 'sm')} data-rmx-document>
                <Icon icon={Download} size={14} />
                Download {a.name}
              </a>
            ) : (
              <button type="button" mix={[...buttonStyle('ghost', 'sm'), on('click', () => void save())]} disabled={busy}>
                <Icon icon={Download} size={14} />
                Save
              </button>
            )}
            {image && !preview ? (
              <button type="button" mix={[...buttonStyle('ghost', 'sm'), on('click', () => void show())]} disabled={busy}>
                <Icon icon={Eye} size={14} />
                Show image
              </button>
            ) : null}
          </div>
        </div>
        {preview ? <img mix={previewCss} src={preview} alt={a.name} /> : null}
        <ErrorNote error={error} what="Could not read the file" />
      </li>
    );
  };
}

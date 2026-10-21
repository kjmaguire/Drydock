// Markdown minutes and ADR candidates of a room (room-protocol s8): an immutable snapshot the core keeps, shown in a dialog
// and offered for download.

import { css, on, type Handle } from 'remix/component';
import { Download, FileDown, Gavel, ScrollText } from 'lucide';
import type { RoomExportView } from '../../api/runClient.ts';
import type { Room } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { exportFileName } from './messages.ts';

const introCss = css({ margin: '0', color: t.textMuted, fontSize: t.textMd });
const choices = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2 });
const resultCss = css({
  display: 'grid',
  gap: t.s2,
  '& .meta': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', margin: '0', color: t.textMuted, fontSize: t.textSm },
  '& pre': { maxHeight: '50vh', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
});

export function RoomExports(handle: Handle<{ room: Room }>) {
  const s = services(handle);
  const act = action(handle);
  let open = false;
  let ex: RoomExportView | undefined;
  let url: string | undefined;
  handle.signal.addEventListener('abort', () => {
    if (url) URL.revokeObjectURL(url);
  });

  const run = async (kind: 'minutes' | 'adr_candidates') => {
    const r = await act.run(kind, () => s.client.roomExport(handle.props.room.id, kind));
    if (!r) return;
    if (url) URL.revokeObjectURL(url);
    ex = r;
    url = URL.createObjectURL(new Blob([r.content], { type: 'text/markdown' }));
    void handle.update();
  };

  return () => {
    const room = handle.props.room;
    return (
      <>
        <Button
          size="sm"
          variant="ghost"
          icon={FileDown}
          aria-haspopup="dialog"
          mix={[
            on('click', () => {
              open = true;
              void handle.update();
            }),
          ]}
        >
          Export
        </Button>
        <Dialog
          open={open}
          wide
          title="Export the conversation"
          onClose={() => {
            open = false;
            void handle.update();
          }}
        >
          <p mix={introCss}>A snapshot of #{room.name} as Markdown. The core keeps it and its hash, so it can be verified later. Nothing leaves this machine.</p>
          <div mix={choices} class="room-exports">
            <Button icon={ScrollText} disabled={act.busy !== ''} mix={[on('click', () => void run('minutes'))]}>
              {act.busy === 'minutes' ? 'Exporting...' : 'Minutes (Markdown)'}
            </Button>
            <Button icon={Gavel} disabled={act.busy !== ''} mix={[on('click', () => void run('adr_candidates'))]}>
              {act.busy === 'adr_candidates' ? 'Exporting...' : 'ADR candidates'}
            </Button>
          </div>
          <ErrorNote error={act.error} what="Export failed" />
          {ex ? (
            <div mix={resultCss} data-testid="room-export">
              <p class="meta">
                <span>
                  Snapshot <code>{ex.sha256.slice(0, 12)}</code>, kept and verifiable.
                </span>
                {url ? (
                  <a href={url} download={exportFileName(room.name, ex.kind, ex.format)} mix={buttonStyle('secondary', 'sm')} data-rmx-document>
                    <Icon icon={Download} size={14} />
                    Download
                  </a>
                ) : null}
              </p>
              <pre class="export-text">{ex.content}</pre>
            </div>
          ) : null}
        </Dialog>
      </>
    );
  };
}
